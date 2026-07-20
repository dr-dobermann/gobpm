package instance

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

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
// committing the assembled collection at an unopened host scope fails.
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
		open:      map[scope.DataPath]int{},  // empty → this drain is the last
		outputRef: "res",
	}
	entry := &scopeEntry{group: grp, ordinal: 0}

	err = ls.parallelInstanceDrained(t.Context(), scope.DataPath("/x"), entry)
	require.Error(t, err)
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
// OpenScope duplicate, faulting the instance (mirrors the onScopeOpen white-box).
func TestParallelMultiInstanceOpenScopeError(t *testing.T) {
	var count atomic.Int32

	mi := mustParallelMI(t, activities.WithCardinality(cardExpr(t, 2)))

	inst := miSubProcessInstance(t, &count, mi)
	// no tracks are spawned in this white-box; clear the birth tracks so the
	// fault path's stopAll has no un-spawned (nil-cancel) track to touch.
	inst.tracks = map[string]*track{}
	ls := newLoopState(inst)
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	child, err := host.scopePath.Append(scopeSegment(node) + "-0")
	require.NoError(t, err)
	require.NoError(t, inst.sc.plane.OpenScope(child))

	ls.fanOutParallelMI(t.Context(), host, node, node.(scopeHost))

	require.True(t, ls.stopping)
	require.Error(t, inst.LastErr())
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
