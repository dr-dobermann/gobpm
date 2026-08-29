package instance

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// TestParallelLeafSpawnsNoTrack is SRD-090.A T-2 (FR-3): a track means a
// token walking a path again. Iterating a leaf three times used to fork
// three tracks — the only runtime object that could both execute a node
// and be an event processor — and the decorator's executors replace them,
// so the run's track count is the one token that walked in.
func TestParallelLeafSpawnsNoTrack(t *testing.T) {
	var gate, count atomic.Int32

	gate.Store(1) // nothing blocks; this test is about shape, not overlap

	s := gatedLeafMISnapshot(t, "lm-tracks", false, &gate, &count)
	inst := runLeafMI(t, s)

	require.Equal(t, int32(3), count.Load(), "all three instances ran")
	require.Len(t, inst.tracks, 1,
		"3 instances of one leaf activity are 3 executors on ONE track, "+
			"not 3 tracks")
}

// TestParallelLeafOpensNoScope is SRD-090.A T-3 (FR-4): one isolation
// rule. A leaf activity is not a scope host, so its instances are
// isolated by their frames and NOT by a scope apiece.
//
// It asserts the scope OPENINGS, not the scopes still open at the end:
// an instance scope closes when its iteration drains, so a finished run
// shows none either way and the surviving-path form of this test passes
// on the very code it is meant to reject.
func TestParallelLeafOpensNoScope(t *testing.T) {
	var gate, count atomic.Int32

	gate.Store(1)

	s := gatedLeafMISnapshot(t, "lm-scopes", false, &gate, &count)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())
	require.Equal(t, int32(3), count.Load(), "all three instances ran")

	rec.mu.Lock()
	defer rec.mu.Unlock()

	opened := 0

	for _, e := range rec.events {
		if e.Kind == observability.KindScope &&
			e.Phase == observability.PhaseOpened &&
			e.NodeID == "lm-scopes-work" {
			opened++
		}
	}

	require.Zero(t, opened,
		"a leaf instance opens no scope of its own; the fan-out used to "+
			"open one per instance")
}

// TestParallelLeafUndeclaredWriteReachesEnclosingScope is SRD-090.A
// T-11 — NFR-1's ONE named exception, asserted rather than discovered.
//
// An instance's own per-execution write (here the `res` item the task
// produces) used to land in that iteration's scope and die with it. With
// no per-iteration scope it reaches the ENCLOSING scope, and for a
// parallel activity the last writer wins — which is order-dependent, so
// the test pins membership rather than a particular winner. The
// declared output collection is unaffected: still positional by
// ordinal, still complete, which is what a model actually reads.
func TestParallelLeafUndeclaredWriteReachesEnclosingScope(t *testing.T) {
	var gate, count atomic.Int32

	gate.Store(1)

	s := gatedLeafMISnapshot(t, "lm-writes", false, &gate, &count)
	inst := runLeafMI(t, s)

	outs := leafOuts(t, inst, "/lm-writes")
	require.ElementsMatch(t, []any{"R:a", "R:b", "R:c"}, outs,
		"the DECLARED output is complete and positional — unchanged")

	res, err := inst.sc.plane.GetData(scope.DataPath("/lm-writes"), "res")
	require.NoError(t, err,
		"a per-instance write now reaches the enclosing scope; it used to "+
			"die with the instance's own scope")
	require.Contains(t, []any{"R:a", "R:b", "R:c"},
		res.Value().Get(context.Background()),
		"whichever instance committed last — order-dependent by design")
}

// TestRestoredStatesSkipsCompletedOrdinals is SRD-090.A T-4/FR-7's
// decision, isolated: WHICH ordinals a restore relaunches. A parallel
// iteration completes out of order, so the completed COUNT cannot say
// which ones are done — only the recorded set can, and re-running a
// completed iteration is what FR-7 forbids.
func TestRestoredStatesSkipsCompletedOrdinals(t *testing.T) {
	seed := &checkpoint.IterationRecord{
		Kind: "mi_parallel", N: 3, Completed: 1,
		Instances: []checkpoint.IterationEntry{
			{Ordinal: 0, State: iterationRunning},
			{Ordinal: 1, State: iterationCompleted},
			{Ordinal: 2, State: iterationRunning},
		},
	}

	require.Equal(t,
		[]string{iterationRunning, iterationCompleted, iterationRunning},
		restoredStates(seed, 3),
		"ordinal 1 finished before the capture and must not run again")

	require.Equal(t,
		[]string{iterationRunning, iterationRunning},
		restoredStates(nil, 2),
		"a fresh activation runs every instance")
}

// TestRestoredStatesIgnoresOutOfRangeOrdinal pins the guard on a record
// that disagrees with the activation it is restored against: the frozen N
// wins, because §2.4 fixes cardinality once and an ordinal outside it
// describes no iteration this run has.
func TestRestoredStatesIgnoresOutOfRangeOrdinal(t *testing.T) {
	seed := &checkpoint.IterationRecord{
		Kind: "mi_parallel", N: 2,
		Instances: []checkpoint.IterationEntry{
			{Ordinal: -1, State: iterationCompleted},
			{Ordinal: 5, State: iterationCompleted},
			{Ordinal: 1, State: iterationCompleted},
		},
	}

	require.Equal(t,
		[]string{iterationRunning, iterationCompleted},
		restoredStates(seed, 2))
}

// TestPresizedStagingKeepsRecordedSlots pins the pre-sizing a parallel
// assembly needs: SetAt replaces rather than appends, so an out-of-order
// completion needs its slot to exist — and a restored position's already
// staged outputs must survive the resize that creates them.
func TestPresizedStagingKeepsRecordedSlots(t *testing.T) {
	ctx := context.Background()

	staged := values.NewArray[any]("x", nil, "z")

	sized := presizedStaging(ctx, staged, 3)
	require.Equal(t, []any{"x", nil, "z"}, sized.GetAll(ctx),
		"a restored run's completed outputs survive the pre-sizing")

	require.NoError(t, sized.SetAt(ctx, 1, "y"),
		"the hole is a real slot, writable by the ordinal that owns it")
	require.Equal(t, []any{"x", "y", "z"}, sized.GetAll(ctx))

	empty := presizedStaging(ctx, values.NewArray[any](), 2)
	require.Equal(t, []any{nil, nil}, empty.GetAll(ctx),
		"a fresh activation pre-sizes to N holes")
}

// TestInstanceOutputsStageByOrdinal pins the positional handoff: an
// iteration's output goes to the slot its ORDINAL owns, whatever order the
// instances finished in, and an instance that produced none leaves its
// slot nil — as a canceled one does (§2.7).
func TestInstanceOutputsStageByOrdinal(t *testing.T) {
	ctx := context.Background()

	outs := newIterationOutputs(3)
	outs.values[2], outs.filled[2] = "R:c", true
	outs.values[0], outs.filled[0] = "R:a", true

	st := &miState{staging: values.NewArray[any](make([]any, 3)...)}

	require.NoError(t, outs.stage(ctx, st, 2))
	require.NoError(t, outs.stage(ctx, st, 0))
	require.NoError(t, outs.stage(ctx, st, 1))

	require.Equal(t, []any{"R:a", nil, "R:c"}, st.staging.GetAll(ctx),
		"c finished first but still lands third; b produced nothing")

	require.NoError(t, outs.stage(ctx, &miState{}, 0),
		"an activity assembling no output stages nothing")
	require.NoError(t, outs.stage(ctx, nil, 0))
}
