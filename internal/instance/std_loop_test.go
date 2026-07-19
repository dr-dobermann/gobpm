package instance

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// SRD-054 M2 — the leaf-Task Standard Loop seam in the run loop: standardLoopOf
// detection, runStandardLoop (testBefore / loopMaximum / loopCounter), and the
// zero-iteration outgoing-flow path.

// loopCondLt builds the boolean condition "loopCounter < n", read by the loop
// each pass through the engine-published counter.
func loopCondLt(t *testing.T, n int) data.FormalExpression {
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

// countOp builds a ServiceTask operation that increments count each run.
func countOp(t *testing.T, count *atomic.Int32) service.Operation {
	t.Helper()

	op, err := gooper.New("work",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			count.Add(1)

			return nil, nil
		})
	require.NoError(t, err)

	return op
}

// loopedTaskInstance builds start -> work(ServiceTask, loop sl) -> end, the work
// op incrementing count each run.
func loopedTaskInstance(
	t *testing.T, count *atomic.Int32,
	sl *activities.StandardLoopCharacteristics,
) *Instance {
	t.Helper()

	return loopedTaskInstanceOp(t, countOp(t, count), sl)
}

// loopedTaskInstanceOp builds start -> work(ServiceTask op, loop sl) -> end.
func loopedTaskInstanceOp(
	t *testing.T, op service.Operation,
	sl *activities.StandardLoopCharacteristics,
) *Instance {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("std-loop")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	work, err := activities.NewServiceTask("work", op,
		activities.WithLoop(sl), activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, end} {
		require.NoError(t, p.Add(e))
	}
	_, err = flow.Link(start, work)
	require.NoError(t, err)
	_, err = flow.Link(work, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	return inst
}

func TestStandardLoopRunsWhileConditionHolds(t *testing.T) {
	var count atomic.Int32

	sl, err := activities.NewStandardLoop(loopCondLt(t, 3))
	require.NoError(t, err)

	inst := loopedTaskInstance(t, &count, sl)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(3), count.Load(),
		"post-tested loop runs while loopCounter < 3")
}

func TestStandardLoopPreTestedZeroIterations(t *testing.T) {
	var count atomic.Int32

	sl, err := activities.NewStandardLoop(loopCondLt(t, 0),
		activities.WithTestBefore())
	require.NoError(t, err)

	inst := loopedTaskInstance(t, &count, sl)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State(),
		"the instance completes even though the body never ran")
	require.Equal(t, int32(0), count.Load())
}

func TestStandardLoopMaximumCaps(t *testing.T) {
	var count atomic.Int32

	sl, err := activities.NewStandardLoop(loopCondLt(t, 1000),
		activities.WithLoopMaximum(2))
	require.NoError(t, err)

	inst := loopedTaskInstance(t, &count, sl)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(2), count.Load(),
		"loopMaximum caps an otherwise-unbounded condition")
}

// TestStandardLoopOf covers the capability detection: a looped node reports its
// Standard Loop, a plain node reports nil.
func TestStandardLoopOf(t *testing.T) {
	_ = data.CreateDefaultStates()

	sl, err := activities.NewStandardLoop(loopCondLt(t, 1))
	require.NoError(t, err)

	op, err := gooper.New("op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)

	looped, err := activities.NewServiceTask("looped", op,
		activities.WithLoop(sl), activities.WithoutParams())
	require.NoError(t, err)
	require.NotNil(t, standardLoopOf(looped), "a looped task reports its loop")

	plain, err := activities.NewServiceTask("plain", op,
		activities.WithoutParams())
	require.NoError(t, err)
	require.Nil(t, standardLoopOf(plain), "a plain task reports no loop")
}

// loopCondBoom builds a condition whose evaluation fails.
func loopCondBoom(t *testing.T) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return nil, errs.New(errs.M("loop condition boom"),
				errs.C(errorClass, errs.OperationFailed))
		})
	require.NoError(t, err)

	return c
}

// loopCondLyingInt declares a bool result but evaluates to an int — the runtime
// non-boolean the model-layer type check cannot catch.
func loopCondLyingInt(t *testing.T) data.FormalExpression {
	t.Helper()

	c, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(1), nil
		})
	require.NoError(t, err)

	return c
}

// TestStandardLoopConditionErrorFaults: a pre-tested condition that errors on
// evaluation faults the instance before the body runs.
func TestStandardLoopConditionErrorFaults(t *testing.T) {
	var count atomic.Int32

	sl, err := activities.NewStandardLoop(loopCondBoom(t),
		activities.WithTestBefore())
	require.NoError(t, err)

	inst := loopedTaskInstance(t, &count, sl)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a condition-evaluation error faults the instance")
	require.Equal(t, int32(0), count.Load())
}

// TestStandardLoopNonBoolConditionFaults: a condition that evaluates to a
// non-boolean value faults the instance.
func TestStandardLoopNonBoolConditionFaults(t *testing.T) {
	var count atomic.Int32

	sl, err := activities.NewStandardLoop(loopCondLyingInt(t))
	require.NoError(t, err)

	inst := loopedTaskInstance(t, &count, sl)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a non-boolean condition result faults the instance")
}

// TestStandardLoopBodyErrorFaults: an error from the looped activity's body
// propagates out of the loop and faults the instance.
func TestStandardLoopBodyErrorFaults(t *testing.T) {
	failOp, err := gooper.New("boom",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return nil, errs.New(errs.M("body boom"),
				errs.C(errorClass, errs.OperationFailed))
		})
	require.NoError(t, err)

	sl, err := activities.NewStandardLoop(loopCondLt(t, 3))
	require.NoError(t, err)

	inst := loopedTaskInstanceOp(t, failOp, sl)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a body error faults the instance")
}

// TestEvalLoopCondNonBool covers the defensive non-boolean guard directly: a
// condition that evaluates to a non-bool value (a mock bypasses goexpr's result
// type enforcement) is rejected by evalLoopCond.
func TestEvalLoopCondNonBool(t *testing.T) {
	_ = data.CreateDefaultStates()

	me := mockdata.NewMockFormalExpression(t)
	me.EXPECT().ResultType().Return("bool")
	me.EXPECT().Language().Return("mock").Maybe()
	me.EXPECT().Evaluate(mock.Anything, mock.Anything).
		Return(values.NewVariable(42), nil)

	sl, err := activities.NewStandardLoop(me)
	require.NoError(t, err)

	inst := loopedTaskInstanceOp(t, countOp(t, new(atomic.Int32)), sl)
	node := findNode(t, inst.s, "work")

	tr, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	_, err = tr.evalLoopCond(context.Background(), node, sl)
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-boolean")
}
