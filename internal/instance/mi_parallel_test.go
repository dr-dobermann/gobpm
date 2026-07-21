package instance

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// attrAtLeast builds the boolean completionCondition "<attr> >= n" over a
// §2.9 runtime attribute published at the host scope.
func attrAtLeast(t *testing.T, attr string, n int) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, attr)
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(v >= n), nil
		})
}

// mustParallelMI builds a valid PARALLEL Multi-Instance — NewMultiInstance
// without WithSequential (parallel is the §13.3.7 default).
func mustParallelMI(
	t *testing.T, opts ...activities.MultiInstanceOption,
) *activities.MultiInstanceLoopCharacteristics {
	t.Helper()

	mi, err := activities.NewMultiInstance(opts...)
	require.NoError(t, err)

	return mi
}

// TestParallelMultiInstanceRunsAll: a parallel Multi-Instance fans out all N
// instances and completes when the last drains (SRD-056.A FR-1/FR-2/FR-3).
func TestParallelMultiInstanceRunsAll(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 3)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(3), count.Load(),
		"all N instances run and the activity completes")
}

// TestParallelMultiInstanceZeroCardinality: a zero count runs no instances, yet
// the host resumes and the activity completes.
func TestParallelMultiInstanceZeroCardinality(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 0)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(0), count.Load())
}

// TestParallelMultiInstanceDistinctScopes: each instance opens a distinct scope
// carrying its own 0-based ordinal (FR-2/FR-11).
func TestParallelMultiInstanceDistinctScopes(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 3)))

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
		"each parallel instance opens a distinct scope with its ordinal")
}

// TestParallelMultiInstanceInputItemPerScope: each concurrent instance sees its
// own collection element bound as `item` in its own scope (FR-5). Parallel
// completion order is nondeterministic, so the SET is asserted.
func TestParallelMultiInstanceInputItemPerScope(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

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

	mi := mustParallelMI(t, activities.WithInputCollection("items", "item"))
	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray(10, 20, 30),
			foundation.WithID("items")),
		data.ReadyDataState)

	inst := miSubProcessInstanceOp(t, op, mi, items)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())

	mu.Lock()
	got := append([]any{}, seen...)
	mu.Unlock()

	require.ElementsMatch(t, []any{10, 20, 30}, got,
		"each instance sees its own element (order nondeterministic)")
}

// TestParallelMultiInstanceAssemblesOutput: per-instance outputs assemble
// positionally (slot = ordinal) into the output collection, published once at
// completion — in input order despite concurrent, out-of-order completion
// (FR-6/FR-7).
func TestParallelMultiInstanceAssemblesOutput(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	op, err := gooper.New("double",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("item")
			if err != nil {
				return nil, err
			}

			v, _ := d.Value().Get(ctx).(int)

			return data.MustItemDefinition(
				values.NewVariable(v*2), foundation.WithID("out")), nil
		})
	require.NoError(t, err)

	mi := mustParallelMI(t,
		activities.WithInputCollection("nums", "item"),
		activities.WithOutputCollection("doubled", "out"))
	nums := data.MustProperty("nums",
		data.MustItemDefinition(values.NewArray(2, 3, 4),
			foundation.WithID("nums")),
		data.ReadyDataState)

	inst := miSubProcessInstanceOp(t, op, mi, nums)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())

	d, err := inst.DataReader().GetData("doubled")
	require.NoError(t, err)
	col, ok := d.Value().(data.Collection)
	require.True(t, ok, "loopDataOutputRef is a collection")
	require.Equal(t, []any{4, 6, 8}, col.GetAll(context.Background()),
		"positional assembly, input order, despite concurrent completion")
}

// TestParallelMultiInstanceOutputItemMissing: an output-collecting parallel MI
// whose body produces no output item faults when the item is captured.
func TestParallelMultiInstanceOutputItemMissing(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t,
		activities.WithCardinality(cardExpr(t, 3)),
		activities.WithOutputCollection("coll", "missing"))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a missing outputDataItem faults the instance")
}

// TestParallelMultiInstanceCompletionCancelsRemainder (FR-8): once the
// completionCondition holds, the still-running instances are canceled as a
// unit. Bodies block per-instance so the truncation is deterministic (parallel
// instant bodies would all complete first).
func TestParallelMultiInstanceCompletionCancelsRemainder(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	const total = 5

	gates := make([]chan struct{}, total)
	for i := range gates {
		gates[i] = make(chan struct{})
	}

	var canceled atomic.Int32

	op, err := gooper.New("wait",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("loopCounter")
			if err != nil {
				return nil, err
			}

			i, _ := d.Value().Get(ctx).(int)

			select {
			case <-gates[i]: // released → completes normally
			case <-ctx.Done(): // canceled by the completionCondition
				canceled.Add(1)
			}

			return nil, nil
		})
	require.NoError(t, err)

	mi := mustParallelMI(t,
		activities.WithCardinality(cardExpr(t, total)),
		activities.WithCompletionCondition(
			attrAtLeast(t, "numberOfCompletedInstances", 2)))

	// release exactly two instances; the other three must be canceled — they
	// never see their gate, so a completing run proves cancellation.
	close(gates[0])
	close(gates[1])

	inst := miSubProcessInstanceOp(t, op, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Eventually(t, func() bool { return canceled.Load() == total-2 },
		2*time.Second, 5*time.Millisecond,
		"the three not-yet-completed instances are canceled")
}

// TestParallelMultiInstanceRuntimeAttributes (FR-9): the §2.9 attributes are
// published at the host scope and readable by the completionCondition — a
// never-true condition over numberOfInstances runs every instance (and a
// missing attribute would fault the evaluation).
func TestParallelMultiInstanceRuntimeAttributes(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t,
		activities.WithCardinality(cardExpr(t, 3)),
		activities.WithCompletionCondition(
			attrAtLeast(t, "numberOfInstances", 100)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(3), count.Load(),
		"the never-true condition lets all instances run")
}

// TestParallelMultiInstanceBoundaryInterruptsAll (FR-10): an interrupting
// boundary firing on a parallel-MI host tears down ALL N instance scopes (not
// just the default segment), and drops the group.
func TestParallelMultiInstanceBoundaryInterruptsAll(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 3)))
	inst := miSubProcessInstance(t, new(atomic.Int32), mi)
	inst.tracks = map[string]*track{}
	ls := newLoopState(inst)
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	// simulate a fan-out with three in-flight instances: open their scopes and
	// register the group.
	grp := &miGroup{host: host, node: node, open: map[scope.DataPath]int{}}
	for i := 0; i < 3; i++ {
		child, err := host.scopePath.Append(
			scopeSegment(node) + "-" + strconv.Itoa(i))
		require.NoError(t, err)
		require.NoError(t, inst.sc.plane.OpenScope(child))

		ls.scopes[child] = &scopeEntry{
			host: host, node: node, group: grp, ordinal: i,
		}
		grp.open[child] = i
	}
	ls.miGroups[host.ID()] = grp

	ls.cancelHostScope(host)

	require.Empty(t, ls.scopes, "all N parallel instance scopes are canceled")
	require.NotContains(t, ls.miGroups, host.ID(), "the group is dropped")
}

// TestParallelMultiInstanceNonBoolCompletion: a completionCondition that
// evaluates to a non-boolean value faults the instance.
func TestParallelMultiInstanceNonBoolCompletion(t *testing.T) {
	var count atomic.Int32

	me := mockdata.NewMockFormalExpression(t)
	me.EXPECT().ResultType().Return("bool")
	me.EXPECT().Language().Return("mock").Maybe()
	me.EXPECT().Evaluate(mock.Anything, mock.Anything).
		Return(values.NewVariable(42), nil).Maybe()

	mi := mustParallelMI(t,
		activities.WithCardinality(cardExpr(t, 3)),
		activities.WithCompletionCondition(me))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a non-boolean completionCondition faults the instance")
}

// TestParallelMultiInstanceInputGetAtError covers the per-instance split's
// defensive collection-read error: opening an ordinal beyond the collection's
// size fails at GetAt.
func TestParallelMultiInstanceInputGetAtError(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 1)))

	inst := miSubProcessInstance(t, &count, mi)
	inst.tracks = map[string]*track{}
	ls := newLoopState(inst)
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	grp := &miGroup{
		host:       host,
		node:       node,
		collection: values.NewArray[any](99), // one element
		open:       map[scope.DataPath]int{},
		inputItem:  "item",
		n:          1,
	}

	// ordinal 5 is out of range for a 1-element collection → GetAt fails.
	err = ls.openParallelInstance(
		t.Context(), grp, host, node, node.(scopeHost), 5)
	require.Error(t, err)
}

// TestParallelMultiInstancePublishError covers the completion publish's error:
// committing the assembled collection at an unopened host scope fails when the
// decorator asks the loop to finalize the group (scopeComplete).
func TestParallelMultiInstancePublishError(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 1)))

	inst := miSubProcessInstance(t, &count, mi)
	inst.tracks = map[string]*track{}
	ls := newLoopState(inst)
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)
	// an unopened host scope makes the publish Commit fail.
	host.scopePath = scope.DataPath("/does/not/exist")

	grp := &miGroup{
		host:      host,
		node:      node,
		staging:   values.NewArray[any](nil), // non-nil → publish is attempted
		open:      map[scope.DataPath]int{},  // empty → nothing to cancel
		outputRef: "res",
	}
	ls.miGroups[host.ID()] = grp

	reply := make(chan scopeReply, 1)
	ls.handleComplete(scopeRequest{host: host, node: node, reply: reply})
	require.Error(t, (<-reply).err)
}

// TestParallelMultiInstanceCardinalityError: a cardinality that errors on
// evaluation faults the instance before any instance opens.
func TestParallelMultiInstanceCardinalityError(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExprBoom(t)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.NotEqual(t, Completed, inst.State(),
		"a cardinality evaluation error faults the instance")
	require.Equal(t, int32(0), count.Load())
}

// TestParallelMultiInstanceOpenScopeError covers the fan-out's defensive
// data-plane open failure: pre-opening instance 0's child path makes its
// OpenScope duplicate, so the fan-out handler replies an error the decorator
// faults on.
func TestParallelMultiInstanceOpenScopeError(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 2)))

	inst := miSubProcessInstance(t, &count, mi)
	inst.tracks = map[string]*track{}
	ls := newLoopState(inst)
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	child, err := host.scopePath.Append(scopeSegment(node) + "-0")
	require.NoError(t, err)
	require.NoError(t, inst.sc.plane.OpenScope(child))

	reply := make(chan scopeReply, 1)
	ls.handleFanOut(t.Context(),
		scopeRequest{host: host, node: node, n: 2, reply: reply})
	require.Error(t, (<-reply).err)
}

// TestParallelMultiInstanceSequentialStillWorks: the parallel dispatch leaves the
// serial Multi-Instance path (SRD-055) untouched (NFR-2).
func TestParallelMultiInstanceSequentialStillWorks(t *testing.T) {
	var count atomic.Int32

	mi := mustSeqMI(t, activities.WithCardinality(cardExpr(t, 3)))

	inst := miSubProcessInstance(t, &count, mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(3), count.Load())
}

// TestParallelBarrierStepBindError: the per-drain §2.9 attribute bind at an unopened
// host scope faults the barrier step (covering bindMICounters' error path too).
func TestParallelBarrierStepBindError(t *testing.T) {
	_, node, host := miParFixture(t)
	host.scopePath = scope.DataPath("/does/not/exist") // bindDataItemAt fails

	_, err := host.parallelBarrierStep(
		t.Context(), &stepInfo{node: node}, multiInstanceOf(node), 3, 1)
	require.Error(t, err)
}

// TestParallelBarrierStepCompleteError: a true completionCondition drives the
// scopeComplete roundtrip, which faults on a stopped instance (loopDone closed).
func TestParallelBarrierStepCompleteError(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 3)),
		activities.WithCompletionCondition(attrAtLeast(t, "numberOfInstances", 1)))

	inst := miSubProcessInstance(t, &count, mi)
	inst.tracks = map[string]*track{}
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)
	close(inst.loopDone) // the scopeComplete roundtrip returns not-running

	_, err = host.parallelBarrierStep(
		t.Context(), &stepInfo{node: node}, mi, 3, 1)
	require.Error(t, err)
}

// TestHandleReArmGroupGone: re-arming a group already torn down (e.g. by an
// interrupting boundary) just replies so the runner unblocks — no error.
func TestHandleReArmGroupGone(t *testing.T) {
	inst, node, host := miParFixture(t)
	ls := newLoopState(inst)

	reply := make(chan scopeReply, 1)
	ls.handleReArm(t.Context(),
		scopeRequest{op: scopeReArm, host: host, node: node, reply: reply})
	require.NoError(t, (<-reply).err)
}

// TestHandleCompleteGroupGone: completing a group already torn down just replies —
// no error, nothing to publish or cancel.
func TestHandleCompleteGroupGone(t *testing.T) {
	inst, node, host := miParFixture(t)
	ls := newLoopState(inst)

	reply := make(chan scopeReply, 1)
	ls.handleComplete(
		scopeRequest{op: scopeComplete, host: host, node: node, reply: reply})
	require.NoError(t, (<-reply).err)
}

// TestHandleFanOutNonComposite: a fan-out for a node that is not a composite is a
// corrupt-graph error surfaced to the decorator.
func TestHandleFanOutNonComposite(t *testing.T) {
	inst, _, host := miParFixture(t)
	ls := newLoopState(inst)
	leaf := findNode(t, inst.s, "start") // a StartEvent is not a scopeHost

	reply := make(chan scopeReply, 1)
	ls.handleFanOut(t.Context(),
		scopeRequest{op: scopeFanOut, host: host, node: leaf, n: 1, reply: reply})
	require.Error(t, (<-reply).err)
}
