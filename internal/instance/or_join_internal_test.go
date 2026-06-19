package instance

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// amountProcess builds a process carrying an integer "amount" property the OR
// conditions read.
func amountProcess(t *testing.T, id string, amount int) *process.Process {
	t.Helper()

	p, err := process.New(id,
		data.WithProperties(
			data.MustProperty("amount",
				data.MustItemDefinition(
					values.NewVariable(amount),
					foundation.WithID("amount")),
				data.ReadyDataState)))
	require.NoError(t, err)

	return p
}

// amountCondT builds a bool condition over the "amount" property.
func amountCondT(t *testing.T, pred func(a int) bool) data.FormalExpression {
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

// runToCompletion runs p as an instance and asserts it completes (the OR-join
// fired; no hang). Exercises the loop's OR-join machinery in-package so the
// per-package coverage profile records it.
func runToCompletion(t *testing.T, p *process.Process) {
	t.Helper()

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	leak := assertNoGoroutineLeak(t)
	defer leak()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, inst.Run(ctx))
	require.Eventually(t,
		func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond,
		"the OR-join must fire and the instance complete")
	require.NoError(t, inst.LastErr())
}

// TestORJoinUntakenInstance: amount 50 forks two of the three split→join flows;
// the third is never taken, so the join fires on the parking recheck (no death).
func TestORJoinUntakenInstance(t *testing.T) {
	_ = data.CreateDefaultStates()

	p := amountProcess(t, "or-untaken", 50)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split, err := gateways.NewInclusiveGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
	join, err := gateways.NewInclusiveGateway(
		gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, join, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, split)
	_, err = flow.Link(split, join,
		flow.WithCondition(amountCondT(t, func(a int) bool { return a < 100 })))
	require.NoError(t, err)
	_, err = flow.Link(split, join,
		flow.WithCondition(amountCondT(t, func(a int) bool { return a > 10 })))
	require.NoError(t, err)
	_, err = flow.Link(split, join,
		flow.WithCondition(amountCondT(t, func(a int) bool { return a > 1000 })))
	require.NoError(t, err)
	link(t, join, end)

	runToCompletion(t, p)
}

// TestORJoinDeathInstance: a branch is activated but diverts through an XOR to a
// separate end and dies; the join fires only on the loop's death-recheck (the
// Camunda-7 anti-hang).
func TestORJoinDeathInstance(t *testing.T) {
	_ = data.CreateDefaultStates()

	p := amountProcess(t, "or-death", 50)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	split, err := gateways.NewInclusiveGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
	xor, err := gateways.NewExclusiveGateway()
	require.NoError(t, err)
	join, err := gateways.NewInclusiveGateway(
		gateways.WithDirection(gateways.Converging))
	require.NoError(t, err)
	mid, err := gateways.NewParallelGateway() // structurally feeds the join
	require.NoError(t, err)
	end1, err := events.NewEndEvent("end1")
	require.NoError(t, err)
	end2, err := events.NewEndEvent("end2")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, split, xor, join, mid, end1, end2} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, split)
	_, err = flow.Link(split, join,
		flow.WithCondition(amountCondT(t, func(a int) bool { return a < 100 })))
	require.NoError(t, err)
	_, err = flow.Link(split, xor,
		flow.WithCondition(amountCondT(t, func(a int) bool { return a > 10 })))
	require.NoError(t, err)
	_, err = flow.Link(xor, mid,
		flow.WithCondition(amountCondT(t, func(a int) bool { return a > 1000 })))
	require.NoError(t, err)
	df, err := flow.Link(xor, end2) // default: diverts and dies
	require.NoError(t, err)
	require.NoError(t, xor.UpdateDefaultFlow(df))
	link(t, mid, join)
	link(t, join, end1)

	runToCompletion(t, p)
}
