package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// SRD-054 M2 — the leaf-Task Standard Loop through the public engine surface: a
// ServiceTask marked WithLoop re-runs in place, driven by loopCondition,
// testBefore, and loopMaximum, with the 0-based loopCounter visible to the
// condition.

// loopCounterLt builds the condition "loopCounter < n" — the inner reader that
// proves the engine-published loopCounter is resolvable by name each pass.
func loopCounterLt(t *testing.T, n int) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "loopCounter")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v < n), nil
		})
	require.NoError(t, err)

	return c
}

// countingLoopTask builds a ServiceTask that increments count each run and
// carries the given Standard Loop.
func countingLoopTask(
	t *testing.T, name string, count *atomic.Int32,
	sl *activities.StandardLoopCharacteristics,
) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			count.Add(1)

			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op,
		activities.WithLoop(sl), activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// loopProcess wraps a looped task as start → task → end.
func loopProcess(
	t *testing.T, name string, task *activities.ServiceTask,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(name)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, proc.Add(e))
	}
	link(t, start, task)
	link(t, task, end)

	return proc
}

func TestStandardLoopLeafE2E(t *testing.T) {
	t.Run("post-tested runs while the condition holds", func(t *testing.T) {
		var count atomic.Int32

		sl, err := activities.NewStandardLoop(loopCounterLt(t, 3))
		require.NoError(t, err)

		require.NoError(t, runFlows(t,
			loopProcess(t, "std-loop-post",
				countingLoopTask(t, "work", &count, sl))))
		// do-while: run, counter→1 (1<3), run, →2 (2<3), run, →3 (3<3 false).
		require.Equal(t, int32(3), count.Load())
	})

	t.Run("pre-tested false at entry runs zero times", func(t *testing.T) {
		var count atomic.Int32

		sl, err := activities.NewStandardLoop(loopCounterLt(t, 0),
			activities.WithTestBefore())
		require.NoError(t, err)

		// zero iterations, yet the instance still completes (the token leaves
		// via the task's outgoing flow — FR-7).
		require.NoError(t, runFlows(t,
			loopProcess(t, "std-loop-pre-zero",
				countingLoopTask(t, "work", &count, sl))))
		require.Equal(t, int32(0), count.Load())
	})

	t.Run("loopMaximum caps an always-true condition", func(t *testing.T) {
		var count atomic.Int32

		sl, err := activities.NewStandardLoop(loopCounterLt(t, 1000),
			activities.WithLoopMaximum(2))
		require.NoError(t, err)

		require.NoError(t, runFlows(t,
			loopProcess(t, "std-loop-max",
				countingLoopTask(t, "work", &count, sl))))
		require.Equal(t, int32(2), count.Load())
	})
}
