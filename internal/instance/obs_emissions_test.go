package instance

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// obsRecorder captures every observable event an instance emits.
type obsRecorder struct {
	mu     sync.Mutex
	events []observability.Fact
}

func (r *obsRecorder) record(ev observability.Fact) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

// phasesOf returns the phases seen for a given kind.
func (r *obsRecorder) phasesOf(kind observability.Kind) map[observability.Phase]bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := map[observability.Phase]bool{}
	for _, e := range r.events {
		if e.Kind == kind {
			out[e.Phase] = true
		}
	}

	return out
}

// TestNodePhaseFor covers the track-state → node-phase table and the fallback
// for an out-of-range state (open vocabulary).
func TestNodePhaseFor(t *testing.T) {
	cases := map[trackState]observability.Phase{
		TrackCreated:            observability.PhaseEntered,
		TrackReady:              observability.PhaseEntered,
		TrackExecutingStep:      observability.PhaseExecuting,
		TrackProcessStepResults: observability.PhaseExecuting,
		TrackWaitForEvent:       observability.PhaseParked,
		TrackAwaitingMerge:      observability.PhaseParked,
		TrackAwaitSync:          observability.PhaseParked,
		TrackMerged:             observability.PhaseMerged,
		TrackEnded:              observability.PhaseCompleted,
		TrackCanceled:           observability.PhaseCanceled,
		TrackFailed:             observability.PhaseFailed,
	}

	for state, want := range cases {
		require.Equal(t, want, nodePhaseFor(state))
	}

	// An out-of-range state (never produced by the engine) yields the zero phase.
	require.Equal(t, observability.Phase(""), nodePhaseFor(trackState(99)))
}

// TestNodeProgressUnCollapsed (SRD-041 T-5): the node stream reports the real
// node phases (Entered/Executing/Completed) rather than the 3-value token
// projection (Alive/WaitForEvent/Consumed).
func TestNodeProgressUnCollapsed(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("t5-nodephase")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	task, err := activities.NewServiceTask("work",
		nopOperation(t), activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, task)
	require.NoError(t, err)
	_, err = flow.Link(task, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	rec := &obsRecorder{}
	inst.AddObserver(rec.record)

	runToDone(t, inst)

	phases := rec.phasesOf(observability.KindNodeProgress)
	// Executing and Completed are the un-collapsed phases proving the stream no
	// longer reports the 3-value token projection. (Entered fires at track
	// creation inside New, before this instance-level observer registers; the
	// engine-scope observer, which registers earlier, sees it — T-4 covers that.)
	require.True(t, phases[observability.PhaseExecuting], "a node is Executing")
	require.True(t, phases[observability.PhaseCompleted], "a node Completes")

	// The old token projection must NOT appear as a node phase.
	require.False(t, phases[observability.Phase("Alive")])
	require.False(t, phases[observability.Phase("WaitForEvent")])
	require.False(t, phases[observability.Phase("Consumed")])
}

// TestFaultTriple (SRD-041 T-6): a boundary-caught BpmnError emits Thrown then
// Caught and no Uncaught (the instance completes); an uncaught one emits Thrown
// then Uncaught (the instance faults).
func TestFaultTriple(t *testing.T) {
	t.Run("caught: Thrown + Caught, no Uncaught", func(t *testing.T) {
		inst, _, _ := errorGuardedInstance(
			t, &recordingProducer{}, raiseOp(t, "E1"), "E1")

		rec := &obsRecorder{}
		inst.AddObserver(rec.record)

		runToDone(t, inst)

		phases := rec.phasesOf(observability.KindFault)
		require.True(t, phases[observability.PhaseThrown], "the fault is Thrown")
		require.True(t, phases[observability.PhaseCaught], "the boundary Catches it")
		require.False(t, phases[observability.PhaseUncaught],
			"a caught fault is not Uncaught")
	})

	t.Run("uncaught: Thrown + Uncaught", func(t *testing.T) {
		inst, _, _ := errorGuardedInstance(
			t, &recordingProducer{}, raiseOp(t, "E2"), "E1") // op raises E2, boundary catches E1

		rec := &obsRecorder{}
		inst.AddObserver(rec.record)

		runToDone(t, inst)

		phases := rec.phasesOf(observability.KindFault)
		require.True(t, phases[observability.PhaseThrown], "the fault is Thrown")
		require.True(t, phases[observability.PhaseUncaught],
			"an unmatched fault is Uncaught")
		require.False(t, phases[observability.PhaseCaught],
			"an uncaught fault is not Caught")
	})
}

// nopOperation is a service operation that succeeds without producing output.
func nopOperation(t *testing.T) service.Operation {
	t.Helper()

	op, err := gooper.New("nop",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			return nil, nil
		})
	require.NoError(t, err)

	return op
}
