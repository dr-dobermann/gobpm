package instance

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
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
