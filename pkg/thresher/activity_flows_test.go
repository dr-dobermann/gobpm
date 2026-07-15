package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// laneTask builds an in-process task flipping its flag when executed — the
// probe for which conditional lanes fired.
func laneTask(t *testing.T, name string, hit *atomic.Bool) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			hit.Store(true)

			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// totalGt builds the condition "total > n" over the process property.
func totalGt(t *testing.T, n int) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "total")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v > n), nil
		})
	require.NoError(t, err)

	return c
}

// flowsProcess builds: start → route(no-op task) → {premium [total>100],
// discount [total>50], review (default)} → ends.
func flowsProcess(
	t *testing.T, name string, total int,
	premium, discount, review *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(name,
		data.WithProperties(
			data.MustProperty("total",
				data.MustItemDefinition(values.NewVariable(total),
					foundation.WithID("total")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	route := laneTask(t, "route", &atomic.Bool{}) // the multi-flow source

	pTask := laneTask(t, "premium", premium)
	dTask := laneTask(t, "discount", discount)
	rTask := laneTask(t, "review", review)

	ends := make([]*events.EndEvent, 3)
	for i, n := range []string{"end-p", "end-d", "end-r"} {
		e, err := events.NewEndEvent(n)
		require.NoError(t, err)
		ends[i] = e
	}

	for _, e := range []flow.Element{
		start, route, pTask, dTask, rTask, ends[0], ends[1], ends[2],
	} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, route)

	_, err = flow.Link(route, pTask, flow.WithCondition(totalGt(t, 100)))
	require.NoError(t, err)
	_, err = flow.Link(route, dTask, flow.WithCondition(totalGt(t, 50)))
	require.NoError(t, err)

	rf, err := flow.Link(route, rTask)
	require.NoError(t, err)
	require.NoError(t, route.SetDefaultFlow(rf.ID()))

	link(t, pTask, ends[0])
	link(t, dTask, ends[1])
	link(t, rTask, ends[2])

	return proc
}

// flowsFaultProcess builds route with ONLY false-able conditional flows and no
// default — the FR-4 zero-selected fault case.
func flowsFaultProcess(t *testing.T, name string, total int) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(name,
		data.WithProperties(
			data.MustProperty("total",
				data.MustItemDefinition(values.NewVariable(total),
					foundation.WithID("total")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	route := laneTask(t, "route", &atomic.Bool{})
	lane := laneTask(t, "lane", &atomic.Bool{})

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, route, lane, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, route)

	_, err = flow.Link(route, lane, flow.WithCondition(totalGt(t, 100)))
	require.NoError(t, err)
	_, err = flow.Link(route, end, flow.WithCondition(totalGt(t, 200)))
	require.NoError(t, err)

	link(t, lane, end)

	return proc
}

// runFlows registers and runs proc to a terminal state, returning the
// completion error.
func runFlows(t *testing.T, proc *process.Process) error {
	t.Helper()

	th, err := thresher.New(proc.Name() + "-engine")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()

	_, werr := h.WaitCompletion(wctx)

	require.NoError(t, th.Shutdown(context.Background()))

	return werr
}

// TestActivityConditionalFlowsE2E (SRD-046 T-3): a real instance routes a
// task's conditional and default outgoing flows per the BPMN rule; the
// zero-selected case faults the instance. Every instance runs a CLONED node
// graph, so a firing default is the §4.2 by-ID robustness proof.
func TestActivityConditionalFlowsE2E(t *testing.T) {
	t.Run("total=150 — both conditionals, default suppressed", func(t *testing.T) {
		var p, d, r atomic.Bool
		require.NoError(t,
			runFlows(t, flowsProcess(t, "flows-150", 150, &p, &d, &r)))
		require.True(t, p.Load(), "premium must fire")
		require.True(t, d.Load(), "discount must fire")
		require.False(t, r.Load(), "default must be suppressed")
	})

	t.Run("total=75 — one conditional", func(t *testing.T) {
		var p, d, r atomic.Bool
		require.NoError(t,
			runFlows(t, flowsProcess(t, "flows-75", 75, &p, &d, &r)))
		require.False(t, p.Load())
		require.True(t, d.Load())
		require.False(t, r.Load())
	})

	t.Run("total=10 — default fires on a cloned instance", func(t *testing.T) {
		var p, d, r atomic.Bool
		require.NoError(t,
			runFlows(t, flowsProcess(t, "flows-10", 10, &p, &d, &r)))
		require.False(t, p.Load())
		require.False(t, d.Load())
		require.True(t, r.Load(), "the default lane must fire")
	})

	t.Run("all false, no default — the instance faults", func(t *testing.T) {
		err := runFlows(t, flowsFaultProcess(t, "flows-fault", 10))
		require.Error(t, err)
		require.ErrorContains(t, err, "no outgoing flow selected")
	})
}
