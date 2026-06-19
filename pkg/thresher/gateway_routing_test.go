package thresher_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// amountProcess seeds a process with an integer "amount" property the gateway
// conditions read.
func amountProcess(t *testing.T, id string, amount int) *process.Process {
	t.Helper()

	proc, err := process.New(id,
		data.WithProperties(
			data.MustProperty("amount",
				data.MustItemDefinition(
					values.NewVariable(amount),
					foundation.WithID("amount")),
				data.ReadyDataState)))
	require.NoError(t, err)

	return proc
}

// amountCond builds a bool condition over the "amount" property.
func amountCond(t *testing.T, pred func(a int) bool) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(
		nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			v, err := ds.Find(ctx, "amount")
			if err != nil {
				return nil, err
			}

			a, _ := v.Value().Get(ctx).(int)

			return values.NewVariable(pred(a)), nil
		})
	require.NoError(t, err)

	return c
}

// recordTask builds a ServiceTask whose Go functor records that it ran.
func recordTask(t *testing.T, label string, rec chan<- string) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(label,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			rec <- label

			return nil, nil
		})
	require.NoError(t, err)

	task, err := activities.NewServiceTask(label, op, activities.WithoutParams())
	require.NoError(t, err)

	return task
}

func drain(rec chan string) []string {
	close(rec)

	var got []string
	for s := range rec {
		got = append(got, s)
	}

	sort.Strings(got)

	return got
}

// TestExclusiveRoutingEndToEnd routes through an exclusive gateway by data:
// amount 50 (<100) takes the conditional branch, not the default (FR-6).
func TestExclusiveRoutingEndToEnd(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 4)
	proc := amountProcess(t, "xor-routing", 50)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	xor, err := gateways.NewExclusiveGateway()
	require.NoError(t, err)
	approve := recordTask(t, "approve", rec)
	review := recordTask(t, "review", rec)
	endA, err := events.NewEndEvent("end-approve")
	require.NoError(t, err)
	endR, err := events.NewEndEvent("end-review")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, xor, approve, review, endA, endR} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, xor)
	_, err = flow.Link(xor, approve,
		flow.WithCondition(amountCond(t, func(a int) bool { return a < 100 })))
	require.NoError(t, err)
	df, err := flow.Link(xor, review)
	require.NoError(t, err)
	require.NoError(t, xor.UpdateDefaultFlow(df))
	link(t, approve, endA)
	link(t, review, endR)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	require.Equal(t, []string{"approve"}, drain(rec))
}

// TestInclusiveSplitEndToEnd forks every true branch of an inclusive split:
// amount 50 satisfies both conditions, so both branches run (FR-4).
func TestInclusiveSplitEndToEnd(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 4)
	proc := amountProcess(t, "or-routing", 50)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	or, err := gateways.NewInclusiveGateway()
	require.NoError(t, err)
	a := recordTask(t, "a", rec)
	b := recordTask(t, "b", rec)
	c := recordTask(t, "c", rec) // default branch — must NOT run
	endA, err := events.NewEndEvent("end-a")
	require.NoError(t, err)
	endB, err := events.NewEndEvent("end-b")
	require.NoError(t, err)
	endC, err := events.NewEndEvent("end-c")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, or, a, b, c, endA, endB, endC} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, or)
	_, err = flow.Link(or, a,
		flow.WithCondition(amountCond(t, func(x int) bool { return x < 100 })))
	require.NoError(t, err)
	_, err = flow.Link(or, b,
		flow.WithCondition(amountCond(t, func(x int) bool { return x > 10 })))
	require.NoError(t, err)
	df, err := flow.Link(or, c)
	require.NoError(t, err)
	require.NoError(t, or.UpdateDefaultFlow(df))
	link(t, a, endA)
	link(t, b, endB)
	link(t, c, endC)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartProcess(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	require.Equal(t, []string{"a", "b"}, drain(rec))
}
