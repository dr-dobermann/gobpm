package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// orderRoutingProcess carries an "order" record property {total} — the fixture
// for structural-path gateway routing.
func orderRoutingProcess(t *testing.T, id string, total int) *process.Process {
	t.Helper()

	proc, err := process.New(id,
		data.WithProperties(
			data.MustProperty("order",
				data.MustItemDefinition(
					values.MustRecord(
						values.F("total", values.NewVariable(total))),
					foundation.WithID("order")),
				data.ReadyDataState)))
	require.NoError(t, err)

	return proc
}

// orderTotalCond builds a bool condition over the structural path "order.total".
func orderTotalCond(t *testing.T, pred func(int) bool) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(
		nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			v, err := ds.Find(ctx, "order.total")
			if err != nil {
				return nil, err
			}

			a, _ := v.Value().Get(ctx).(int)

			return values.NewVariable(pred(a)), nil
		})
	require.NoError(t, err)

	return c
}

// TestExclusiveRoutingOnStructuralPath (SRD-042 T-5): an exclusive gateway
// routes on a condition that reads a structural path (order.total > 100)
// end-to-end through the real runtime seam — the gateway itself is unchanged.
func TestExclusiveRoutingOnStructuralPath(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec := make(chan string, 4)
	proc := orderRoutingProcess(t, "structural-xor", 150) // 150 > 100 → premium

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	xor, err := gateways.NewExclusiveGateway()
	require.NoError(t, err)
	premium := recordTask(t, "premium", rec)
	standard := recordTask(t, "standard", rec)
	endP, err := events.NewEndEvent("end-premium")
	require.NoError(t, err)
	endS, err := events.NewEndEvent("end-standard")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		start, xor, premium, standard, endP, endS,
	} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, xor)
	_, err = flow.Link(xor, premium,
		flow.WithCondition(orderTotalCond(t, func(a int) bool { return a > 100 })))
	require.NoError(t, err)
	df, err := flow.Link(xor, standard)
	require.NoError(t, err)
	require.NoError(t, xor.UpdateDefaultFlow(df))
	link(t, premium, endP)
	link(t, standard, endS)

	th, cancel := runEngine(t, proc)
	defer cancel()

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := h.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	require.Equal(t, []string{"premium"}, drain(rec))
}
