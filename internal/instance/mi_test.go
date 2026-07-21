package instance

import (
	"context"
	"sync"
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
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// cardExpr builds an integer loopCardinality expression that evaluates to n
// (ResultType "int", as NewMultiInstance requires).
func cardExpr(t *testing.T, n int) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(0)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(n), nil
		})
}

// cardExprBoom is an integer cardinality expression whose evaluation errors.
func cardExprBoom(t *testing.T) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(0)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return nil, errs.New(errs.M("cardinality boom"),
				errs.C(errorClass, errs.OperationFailed))
		})
}

// cardExprLyingInt declares an int result but evaluates to a non-integer — the
// runtime non-integer the model-layer type check cannot catch. A mock is used
// because goexpr coerces/rejects a mismatched literal before it reaches the
// caller.
func cardExprLyingInt(t *testing.T) data.FormalExpression {
	t.Helper()

	me := mockdata.NewMockFormalExpression(t)
	me.EXPECT().ResultType().Return("int")
	me.EXPECT().Language().Return("mock").Maybe()
	me.EXPECT().Evaluate(mock.Anything, mock.Anything).
		Return(values.NewVariable("not-an-int"), nil)

	return me
}

// loopCounterAtLeast builds the boolean completionCondition "loopCounter >= n".
func loopCounterAtLeast(t *testing.T, n int) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "loopCounter")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v >= n), nil
		})
}

// miSubProcessInstance builds start -> body(SubProcess, Multi-Instance mi) ->
// end, where the body is b-start -> work(counting op) -> b-end. Optional
// process properties seed the root scope (e.g. an input collection).
func miSubProcessInstance(
	t *testing.T, count *atomic.Int32,
	mi *activities.MultiInstanceLoopCharacteristics,
	props ...*data.Property,
) *Instance {
	t.Helper()

	return miSubProcessInstanceOp(t, countOp(t, count), mi, props...)
}

// miSubProcessInstanceOp is miSubProcessInstance with an explicit body operation
// (to read the per-instance item / runtime attributes the host publishes).
func miSubProcessInstanceOp(
	t *testing.T, op service.Operation,
	mi *activities.MultiInstanceLoopCharacteristics,
	props ...*data.Property,
) *Instance {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("mi-sp", data.WithProperties(props...))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body", activities.WithLoop(mi))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start")
	require.NoError(t, err)
	work, err := activities.NewServiceTask("work", op,
		activities.WithoutParams())
	require.NoError(t, err)
	bEnd, err := events.NewEndEvent("b-end")
	require.NoError(t, err)
	for _, e := range []flow.Element{bStart, work, bEnd} {
		require.NoError(t, body.Add(e))
	}
	_, err = flow.Link(bStart, work)
	require.NoError(t, err)
	_, err = flow.Link(work, bEnd)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, end} {
		require.NoError(t, p.Add(e))
	}
	_, err = flow.Link(start, body)
	require.NoError(t, err)
	_, err = flow.Link(body, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	return inst
}

// mustSeqMI builds a valid sequential Multi-Instance from the given options.
func mustSeqMI(
	t *testing.T, opts ...activities.MultiInstanceOption,
) *activities.MultiInstanceLoopCharacteristics {
	t.Helper()

	mi, err := activities.NewMultiInstance(
		append([]activities.MultiInstanceOption{
			activities.WithSequential()}, opts...)...)
	require.NoError(t, err)

	return mi
}

// TestMultiInstanceRunsNSequentially: a cardinality-driven sequential
// Multi-Instance re-opens the body scope exactly N times (§13.3.7).
func TestMultiInstanceRunsNSequentially(t *testing.T) {
	var count atomic.Int32

	mi := mustSeqMI(t, activities.WithCardinality(cardExpr(t, 3)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(3), count.Load(),
		"the body runs once per Multi-Instance instance")
}

// TestMultiInstanceZeroCardinality: a zero count runs no instances, yet the
// host resumes and the instance completes.
func TestMultiInstanceZeroCardinality(t *testing.T) {
	var count atomic.Int32

	mi := mustSeqMI(t, activities.WithCardinality(cardExpr(t, 0)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State(),
		"the host resumes without opening the body scope")
	require.Equal(t, int32(0), count.Load())
}

// TestMultiInstanceCardinalityFromCollection: the count is the size of the
// loopDataInputRef collection when no loopCardinality is given.
func TestMultiInstanceCardinalityFromCollection(t *testing.T) {
	var count atomic.Int32

	require.NoError(t, data.CreateDefaultStates())

	mi := mustSeqMI(t, activities.WithInputCollection("items", "item"))

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray(10, 20, 30, 40),
			foundation.WithID("items")),
		data.ReadyDataState)

	inst := miSubProcessInstance(t, &count, mi, items)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(4), count.Load(),
		"the body runs once per collection element")
}

// TestMultiInstanceInputItemVisible: each instance sees its collection element
// bound as the inputDataItem name, in order (§13.3.7 data-input mediator).
func TestMultiInstanceInputItemVisible(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []any
	)

	op, err := gooper.New("read-item",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("item")
			if err != nil {
				return nil, err
			}

			mu.Lock()
			seen = append(seen, d.Value().Get(ctx))
			mu.Unlock()

			return nil, nil
		})
	require.NoError(t, err)
	require.NoError(t, data.CreateDefaultStates())

	mi := mustSeqMI(t, activities.WithInputCollection("items", "item"))
	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray(10, 20, 30),
			foundation.WithID("items")),
		data.ReadyDataState)

	inst := miSubProcessInstanceOp(t, op, mi, items)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, []any{10, 20, 30}, seen,
		"each instance sees its collection element as `item`")
}

// TestMultiInstanceRuntimeCounters: each instance sees the §13.3.7 runtime
// attributes — loopCounter, numberOfInstances, numberOfActiveInstances (always
// 1 while an instance runs), numberOfCompletedInstances — progressing per pass.
func TestMultiInstanceRuntimeCounters(t *testing.T) {
	var (
		mu   sync.Mutex
		rows [][4]int
	)

	op, err := gooper.New("read-counters",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			read := func(name string) (int, error) {
				d, rerr := r.GetData(name)
				if rerr != nil {
					return 0, rerr
				}

				n, _ := d.Value().Get(ctx).(int)

				return n, nil
			}

			lc, err := read("loopCounter")
			if err != nil {
				return nil, err
			}
			ni, err := read("numberOfInstances")
			if err != nil {
				return nil, err
			}
			na, err := read("numberOfActiveInstances")
			if err != nil {
				return nil, err
			}
			nc, err := read("numberOfCompletedInstances")
			if err != nil {
				return nil, err
			}

			mu.Lock()
			rows = append(rows, [4]int{lc, ni, na, nc})
			mu.Unlock()

			return nil, nil
		})
	require.NoError(t, err)

	mi := mustSeqMI(t, activities.WithCardinality(cardExpr(t, 3)))

	inst := miSubProcessInstanceOp(t, op, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, [][4]int{{0, 3, 1, 0}, {1, 3, 1, 1}, {2, 3, 1, 2}}, rows,
		"loopCounter / numberOfInstances / numberOfActiveInstances / "+
			"numberOfCompletedInstances per pass")
}

// (SRD-055's parallel-rejected gap gate was removed in SRD-056.A, which
// implements parallel Multi-Instance — see mi_parallel_test.go.)

// TestMultiInstanceCardinalityEvalError: a cardinality expression that errors on
// evaluation faults the instance before any instance runs.
func TestMultiInstanceCardinalityEvalError(t *testing.T) {
	var count atomic.Int32

	mi := mustSeqMI(t, activities.WithCardinality(cardExprBoom(t)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a cardinality evaluation error faults the instance")
	require.Equal(t, int32(0), count.Load())
}

// TestMultiInstanceNonIntCardinality: a cardinality that evaluates to a
// non-integer value (a lying expression the model check cannot catch) faults.
func TestMultiInstanceNonIntCardinality(t *testing.T) {
	var count atomic.Int32

	mi := mustSeqMI(t, activities.WithCardinality(cardExprLyingInt(t)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a non-integer cardinality result faults the instance")
	require.Equal(t, int32(0), count.Load())
}

// TestMultiInstanceNonCollectionRef: a loopDataInputRef naming a non-collection
// datum faults the instance.
func TestMultiInstanceNonCollectionRef(t *testing.T) {
	var count atomic.Int32

	require.NoError(t, data.CreateDefaultStates())

	mi := mustSeqMI(t, activities.WithInputCollection("items", "item"))

	notACollection := data.MustProperty("items",
		data.MustItemDefinition(values.NewVariable(5),
			foundation.WithID("items")),
		data.ReadyDataState)

	inst := miSubProcessInstance(t, &count, mi, notACollection)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a non-collection loopDataInputRef faults the instance")
	require.Equal(t, int32(0), count.Load())
}

// TestMultiInstanceMissingCollectionRef: a loopDataInputRef naming an absent
// datum faults the instance.
func TestMultiInstanceMissingCollectionRef(t *testing.T) {
	var count atomic.Int32

	mi := mustSeqMI(t, activities.WithInputCollection("absent", "item"))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a missing loopDataInputRef faults the instance")
	require.Equal(t, int32(0), count.Load())
}

// TestMultiInstanceAssemblesOutput: an output-collecting Multi-Instance stages
// each instance's output item and publishes the assembled collection once, at
// completion (§13.3.7 output mediator + visibility barrier).
func TestMultiInstanceAssemblesOutput(t *testing.T) {
	op, err := gooper.New("emit-out",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("loopCounter")
			if err != nil {
				return nil, err
			}

			lc, _ := d.Value().Get(ctx).(int)

			return data.MustItemDefinition(
				values.NewVariable(lc*lc), foundation.WithID("out")), nil
		})
	require.NoError(t, err)

	mi := mustSeqMI(t,
		activities.WithCardinality(cardExpr(t, 3)),
		activities.WithOutputCollection("squares", "out"))

	inst := miSubProcessInstanceOp(t, op, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())

	d, err := inst.DataReader().GetData("squares")
	require.NoError(t, err)
	col, ok := d.Value().(data.Collection)
	require.True(t, ok, "loopDataOutputRef is a collection")
	require.Equal(t, []any{0, 1, 4}, col.GetAll(context.Background()),
		"each instance's output assembled in order")
}

// TestMultiInstanceOutputUnpublishedMidRun (FR-10): the assembled output
// collection is not visible in any scope during the run — it publishes once, at
// completion (the visibility barrier).
func TestMultiInstanceOutputUnpublishedMidRun(t *testing.T) {
	var (
		mu         sync.Mutex
		midRunSeen bool
	)

	op, err := gooper.New("emit-out",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("loopCounter")
			if err != nil {
				return nil, err
			}

			lc, _ := d.Value().Get(ctx).(int)

			// mid-run the output collection must not resolve by name.
			if _, e := r.GetData("squares"); e == nil {
				mu.Lock()
				midRunSeen = true
				mu.Unlock()
			}

			return data.MustItemDefinition(
				values.NewVariable(lc*lc), foundation.WithID("out")), nil
		})
	require.NoError(t, err)

	mi := mustSeqMI(t,
		activities.WithCardinality(cardExpr(t, 3)),
		activities.WithOutputCollection("squares", "out"))

	inst := miSubProcessInstanceOp(t, op, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.False(t, midRunSeen,
		"the output collection is invisible until completion (the barrier)")

	_, err = inst.DataReader().GetData("squares")
	require.NoError(t, err, "the output collection is published at completion")
}

// TestMultiInstanceEmitsIterationFacts (FR-13): each instance's scope-Opened
// fact carries its 0-based loopCounter, so MI passes are individually
// observable.
func TestMultiInstanceEmitsIterationFacts(t *testing.T) {
	var count atomic.Int32

	mi := mustSeqMI(t, activities.WithCardinality(cardExpr(t, 3)))

	inst := miSubProcessInstance(t, &count, mi)

	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	runToDone(t, inst)

	seen := map[string]bool{}
	rec.mu.Lock()
	for _, e := range rec.events {
		if e.Kind == observability.KindScope &&
			e.Phase == observability.PhaseOpened {
			if lc, ok := e.Details[observability.AttrLoopCounter]; ok {
				seen[lc] = true
			}
		}
	}
	rec.mu.Unlock()

	require.True(t, seen["0"] && seen["1"] && seen["2"],
		"each Multi-Instance pass emits a scope-Opened fact with its ordinal")
}

// TestMultiInstanceCompletionConditionTruncates: a completionCondition that
// holds after an instance stops launching the remaining instances (§13.3.7).
func TestMultiInstanceCompletionConditionTruncates(t *testing.T) {
	var count atomic.Int32

	mi := mustSeqMI(t,
		activities.WithCardinality(cardExpr(t, 5)),
		activities.WithCompletionCondition(loopCounterAtLeast(t, 1)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(2), count.Load(),
		"completionCondition stops launching after the second instance")
}

// TestMultiInstanceNonBoolCompletion: a completionCondition that evaluates to a
// non-boolean value faults the instance.
func TestMultiInstanceNonBoolCompletion(t *testing.T) {
	var count atomic.Int32

	me := mockdata.NewMockFormalExpression(t)
	me.EXPECT().ResultType().Return("bool")
	me.EXPECT().Language().Return("mock").Maybe()
	me.EXPECT().Evaluate(mock.Anything, mock.Anything).
		Return(values.NewVariable(42), nil)

	mi := mustSeqMI(t,
		activities.WithCardinality(cardExpr(t, 3)),
		activities.WithCompletionCondition(me))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a non-boolean completionCondition faults the instance")
}

// TestMultiInstanceOutputItemMissing: an output-collecting Multi-Instance whose
// body never produces the outputDataItem faults when the item is captured.
func TestMultiInstanceOutputItemMissing(t *testing.T) {
	var count atomic.Int32

	mi := mustSeqMI(t,
		activities.WithCardinality(cardExpr(t, 3)),
		activities.WithOutputCollection("coll", "missing_out"))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a missing outputDataItem faults the instance")
}

// TestMIEvalCompletionFrameError covers evalCompletion's defensive frame-open
// error: a track pinned to a non-existent scope path cannot open the completion
// evaluation frame.
func TestMIEvalCompletionFrameError(t *testing.T) {
	mi := mustSeqMI(t, activities.WithCardinality(cardExpr(t, 1)),
		activities.WithCompletionCondition(loopCounterAtLeast(t, 0)))

	inst := miSubProcessInstance(t, new(atomic.Int32), mi)
	node := findNode(t, inst.s, "body")

	tr, err := newTrack(node, inst, nil)
	require.NoError(t, err)
	tr.scopePath = scope.DataPath("/does/not/exist")

	_, err = miIterator{mi: mi}.evalCompletion(
		context.Background(), tr, node)
	require.Error(t, err)
}

// TestMIResolveActivationFrameError covers resolveActivation's defensive
// frame-open error: a track pinned to a non-existent scope path cannot open the
// cardinality evaluation frame.
func TestMIResolveActivationFrameError(t *testing.T) {
	mi := mustSeqMI(t, activities.WithCardinality(cardExpr(t, 1)))

	inst := miSubProcessInstance(t, new(atomic.Int32), mi)
	node := findNode(t, inst.s, "body")

	tr, err := newTrack(node, inst, nil)
	require.NoError(t, err)
	tr.scopePath = scope.DataPath("/does/not/exist")

	_, _, err = miIterator{mi: mi}.resolveActivation(
		context.Background(), tr, node)
	require.Error(t, err)
}

// TestDrivesOwnIteration: a looped composite drives its own off-loop iteration —
// a Standard Loop or a SEQUENTIAL Multi-Instance (ADR-025 v.2 §2.12); a parallel
// MI parks and fans out, and a plain composite / non-activity node do not iterate.
func TestDrivesOwnIteration(t *testing.T) {
	sl, err := activities.NewStandardLoop(loopCondLt(t, 1))
	require.NoError(t, err)
	slNode, err := activities.NewSubProcess("sl", activities.WithLoop(sl))
	require.NoError(t, err)

	seqNode, err := activities.NewSubProcess("seq",
		activities.WithLoop(mustSeqMI(t, activities.WithCardinality(
			cardExpr(t, 1)))))
	require.NoError(t, err)

	parNode, err := activities.NewSubProcess("par",
		activities.WithLoop(mustParallelMI(t, activities.WithCardinality(
			cardExpr(t, 1)))))
	require.NoError(t, err)

	plainNode, err := activities.NewSubProcess("plain")
	require.NoError(t, err)

	// a non-activity node carries no LoopCharacteristics() method at all.
	evNode, err := events.NewStartEvent("ev")
	require.NoError(t, err)

	require.True(t, drivesOwnIteration(slNode),
		"a Standard Loop composite self-drives")
	require.True(t, drivesOwnIteration(seqNode),
		"a sequential Multi-Instance composite self-drives")
	require.False(t, drivesOwnIteration(parNode),
		"a parallel Multi-Instance parks and fans out")
	require.False(t, drivesOwnIteration(plainNode))
	require.False(t, drivesOwnIteration(evNode))
}
