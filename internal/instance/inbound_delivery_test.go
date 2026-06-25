package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// SRD-027 M1 — inbound channel-park plumbing + loop dispatch. The production delivery
// path is unchanged in M1 (ProcessEvent still runs synchronously); these tests drive
// the loop's new evWaiting/evDeliver handling directly.

func TestTrackEventKindStringInbound(t *testing.T) {
	require.Equal(t, "waiting", evWaiting.String())
	require.Equal(t, "deliver", evDeliver.String())
}

// parkedSignalTrack builds an instance plus one track that starts at a signal catch
// event, so it parks in TrackWaitForEvent (and busy-waits in M1) — keeping the loop
// alive (active == 1) so a test can feed it evWaiting/evDeliver. Returns the instance,
// the parked track, and the signal definition to deliver.
func parkedSignalTrack(t *testing.T) (*Instance, *track, flow.EventDefinition) {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("srd027-park")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	arm, end, def := ebSignalArm(t, "go")

	for _, e := range []flow.Element{start, arm, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, arm)
	link(t, arm, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)
	// newTrack(arm) parks the track and registers its catch definition with the
	// producer (checkNodeType). The exact processor/def args are immaterial here.
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).Return(nil).Maybe()

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	// Start from a clean tracks set so the loop owns exactly the track we spawn.
	inst.tracks = map[string]*track{}

	tr, err := newTrack(arm, inst, nil)
	require.NoError(t, err)
	require.True(t, tr.inState(TrackWaitForEvent),
		"a signal-catch track must park in WaitForEvent")

	return inst, tr, def
}

// runLoop spawns the instance loop over the given track and returns a stop func that
// cancels it and waits for the loop to drain — so the goroutine never outlives the test.
func runLoop(t *testing.T, inst *Instance, tr *track) func() {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	go inst.loop(ctx, []*track{tr})

	return func() {
		cancel()
		select {
		case <-inst.Done():
		case <-time.After(2 * time.Second):
			t.Error("loop did not stop after cancellation")
		}
	}
}

// TestLoopDeliversEventToParkedTrack: evWaiting records the track as parked; the
// following evDeliver dispatches the event to the track's evtCh (SRD-027 FR-1..4).
func TestLoopDeliversEventToParkedTrack(t *testing.T) {
	inst, tr, def := parkedSignalTrack(t)
	stop := runLoop(t, inst, tr)
	defer stop()

	inst.emit(trackEvent{kind: evWaiting, track: tr})
	inst.emit(trackEvent{kind: evDeliver, track: tr, eDef: def})

	select {
	case got := <-tr.evtCh:
		require.Equal(t, def, got)
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not dispatch the event to the parked track's evtCh")
	}
}

// TestLoopDropsSecondDeliverDeferredChoice: the flip (delete-on-first-delivery) makes
// deferred choice atomic — a second event for the same parked track is dropped as the
// losing arm (SRD-027 FR-4).
func TestLoopDropsSecondDeliverDeferredChoice(t *testing.T) {
	inst, tr, def := parkedSignalTrack(t)
	stop := runLoop(t, inst, tr)
	defer stop()

	inst.emit(trackEvent{kind: evWaiting, track: tr})
	inst.emit(trackEvent{kind: evDeliver, track: tr, eDef: def})
	inst.emit(trackEvent{kind: evDeliver, track: tr, eDef: def})

	select {
	case <-tr.evtCh:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not dispatch the first (winning) event")
	}

	select {
	case <-tr.evtCh:
		t.Fatal("second event must be dropped (deferred-choice loser)")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestLoopDropsDeliverToUnknownTrack: an evDeliver for a track that never emitted
// evWaiting (not parked, or already delivered) is dropped, not sent (SRD-027 FR-4).
func TestLoopDropsDeliverToUnknownTrack(t *testing.T) {
	inst, tr, def := parkedSignalTrack(t)
	stop := runLoop(t, inst, tr)
	defer stop()

	// No evWaiting first → the track is not in the waiting set.
	inst.emit(trackEvent{kind: evDeliver, track: tr, eDef: def})

	select {
	case <-tr.evtCh:
		t.Fatal("event for a non-parked track must be dropped")
	case <-time.After(100 * time.Millisecond):
	}
}
