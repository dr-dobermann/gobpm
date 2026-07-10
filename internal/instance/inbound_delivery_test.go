package instance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// SRD-027 M2 — inbound channel-park delivery. These tests drive the loop's evWaiting/
// evDeliver dispatch directly. The loop is kept alive by one parked "keeper" track; the
// dispatch subjects are bare, un-spawned tracks, so no run() goroutine competes with the
// test for evtCh and the loop's send is observed deterministically (also under -race).

func TestTrackEventKindStringInbound(t *testing.T) {
	require.Equal(t, "waiting", evWaiting.String())
	require.Equal(t, "deliver", evDeliver.String())
}

// armTrack builds an instance plus one track that starts at a signal catch event, so it
// parks in TrackWaitForEvent. cfg configures the mock hub producer beyond the default
// RegisterEvent (e.g. to make UnregisterEvent fail). Returns the instance, the parked
// track, and the signal definition.
func armTrack(
	t *testing.T,
	name string,
	cfg func(*mockeventproc.MockEventProducer),
) (*Instance, *track, flow.EventDefinition) {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("srd027-" + name)
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
	cfg(ep)

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

// parkedSignalTrack is armTrack with the default producer — used as the loop's keeper:
// spawned, never delivered, it keeps the instance active so the loop stays alive.
func parkedSignalTrack(t *testing.T) (*Instance, *track, flow.EventDefinition) {
	t.Helper()

	return armTrack(t, "park", func(*mockeventproc.MockEventProducer) {})
}

// loopHarness spawns the instance loop over a single parked keeper track and returns the
// instance, a deliverable signal definition, and a stop func that cancels the loop and
// waits for it to drain. The keeper keeps the loop alive without consuming any test event.
func loopHarness(t *testing.T) (*Instance, flow.EventDefinition, func()) {
	t.Helper()

	inst, keeper, def := parkedSignalTrack(t)

	ctx, cancel := context.WithCancel(t.Context())
	go inst.loop(ctx, []*track{keeper})

	stop := func() {
		cancel()
		select {
		case <-inst.Done():
		case <-time.After(2 * time.Second):
			t.Error("loop did not stop after cancellation")
		}
	}

	return inst, def, stop
}

// subjectTrack builds a bare parked track (a unique id + an evtCh) the loop can dispatch
// to. It is NOT spawned, so its run() goroutine never starts and only the test reads its
// evtCh — making the loop's send observable without a race.
func subjectTrack(t *testing.T, inst *Instance) *track {
	t.Helper()

	be, err := foundation.NewBaseElement()
	require.NoError(t, err)

	return &track{
		BaseElement: *be,
		instance:    inst,
		evtCh:       make(chan flow.EventDefinition, eventBufferDepth),
		state:       TrackWaitForEvent,
	}
}

// registeredProcessorFor builds an instance whose only catch is node, captures the
// EventProcessor that checkNodeType registers with the hub for it, and returns that
// processor together with the instance and the built track. It lets a test assert the
// hybrid registration boundary (Message → Instance, Signal/Timer → track — SRD-027 FR-8).
func registeredProcessorFor(
	t *testing.T,
	node flow.Node,
) (eventproc.EventProcessor, *Instance, *track) {
	t.Helper()

	_ = data.CreateDefaultStates()

	p, err := process.New("srd027-reg")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, node, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, node)
	link(t, node, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	var captured eventproc.EventProcessor

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Run(func(proc eventproc.EventProcessor, _ flow.EventDefinition) {
			captured = proc
		}).Return(nil).Maybe()

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)
	inst.tracks = map[string]*track{}

	// newTrack(node) parks the track and runs checkNodeType, which registers the per-trigger
	// processor. The instance is still Created, so no evWaiting is emitted (the loop is not
	// draining) and the call does not block.
	tr, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	return captured, inst, tr
}

// TestLoopDeliversEventToParkedTrack: evWaiting records the track as parked; the
// following evDeliver dispatches the event to the track's evtCh (SRD-027 FR-1..4).
func TestLoopDeliversEventToParkedTrack(t *testing.T) {
	inst, def, stop := loopHarness(t)
	defer stop()

	sub := subjectTrack(t, inst)

	inst.emit(trackEvent{kind: evWaiting, track: sub})
	inst.emit(trackEvent{kind: evDeliver, track: sub, eDef: def})

	select {
	case got := <-sub.evtCh:
		require.Equal(t, def, got)
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not dispatch the event to the parked track's evtCh")
	}
}

// TestLoopDropsSecondDeliverDeferredChoice: the flip (delete-on-first-delivery) makes
// deferred choice atomic — a second event for the same parked track is dropped as the
// losing arm (SRD-027 FR-4).
func TestLoopDropsSecondDeliverDeferredChoice(t *testing.T) {
	inst, def, stop := loopHarness(t)
	defer stop()

	sub := subjectTrack(t, inst)

	inst.emit(trackEvent{kind: evWaiting, track: sub})
	inst.emit(trackEvent{kind: evDeliver, track: sub, eDef: def})
	inst.emit(trackEvent{kind: evDeliver, track: sub, eDef: def})

	select {
	case <-sub.evtCh:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not dispatch the first (winning) event")
	}

	select {
	case <-sub.evtCh:
		t.Fatal("second event must be dropped (deferred-choice loser)")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestLoopDropsDeliverToUnknownTrack: an evDeliver for a track that never emitted
// evWaiting (not parked, or already delivered) is dropped, not sent (SRD-027 FR-4).
func TestLoopDropsDeliverToUnknownTrack(t *testing.T) {
	inst, def, stop := loopHarness(t)
	defer stop()

	sub := subjectTrack(t, inst)

	// No evWaiting first → the track is not in the waiting set.
	inst.emit(trackEvent{kind: evDeliver, track: sub, eDef: def})

	select {
	case <-sub.evtCh:
		t.Fatal("event for a non-parked track must be dropped")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestTrackParkWokenByClosedEvtCh: the loop closing a parked track's evtCh wakes it from
// the run() park and cancels the track (SRD-027 FR-7 — the closed-channel teardown arm).
func TestTrackParkWokenByClosedEvtCh(t *testing.T) {
	_, tr, _ := parkedSignalTrack(t)

	done := make(chan struct{})
	go func() { tr.run(context.Background()); close(done) }()

	close(tr.evtCh) // the loop's stop path closes evtCh (FR-7)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a parked track was not woken by its closed evtCh")
	}
	require.True(t, tr.inState(TrackCanceled),
		"a track woken by a closed evtCh must cancel")
}

// TestTrackParkDeliverErrorFaultsTrack: a delivery whose node teardown fails makes deliver()
// return an error, and run() faults the track (TrackFailed) instead of resuming it.
func TestTrackParkDeliverErrorFaultsTrack(t *testing.T) {
	_, tr, def := armTrack(t, "deliver-err",
		func(ep *mockeventproc.MockEventProducer) {
			// Unregister fails on consume → deliver() errors → run() faults the track.
			ep.EXPECT().UnregisterEvent(mock.Anything, mock.Anything).
				Return(fmt.Errorf("unregister boom")).Maybe()
		})

	done := make(chan struct{})
	go func() { tr.run(context.Background()); close(done) }()

	tr.evtCh <- def

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("track did not return after a failed delivery")
	}
	require.True(t, tr.inState(TrackFailed),
		"a delivery error must fault the track")
}

// TestTrackParkWokenByContextCancel: cancelling the context wakes a parked track via the
// run() park's ctx.Done arm.
func TestTrackParkWokenByContextCancel(t *testing.T) {
	_, tr, _ := parkedSignalTrack(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tr.run(ctx); close(done) }()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a parked track was not woken by context cancellation")
	}
	require.True(t, tr.inState(TrackCanceled),
		"a track woken by a cancelled context must cancel")
}

// TestTrackRunCancelWhileRunning: a running (non-parked) track whose context is already
// cancelled returns canceled via the run() loop's running-path ctx.Done select arm.
func TestTrackRunCancelWhileRunning(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("srd027-run-cancel")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)
	inst.tracks = map[string]*track{}

	// A NONE start event is not a catch, so the track stays TrackReady (not parked).
	tr, err := newTrack(start, inst, nil)
	require.NoError(t, err)
	require.True(t, tr.inState(TrackReady))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before run() reaches the running-path select

	tr.run(ctx)

	require.True(t, tr.inState(TrackCanceled),
		"a running track must cancel when its context is done")
}

// TestLoopKeepsParkedOnCorrelationMismatch: a Message evDeliver is track-less (resolved via
// msgIdx — FR-8) and correlation-gated in the loop before the flip. A message whose derived key
// conflicts with a held conversation key is dropped and the track stays parked; a matching
// message is delivered (SRD-027 FR-8 / NFR-2).
func TestLoopKeepsParkedOnCorrelationMismatch(t *testing.T) {
	inst, keeper, _ := parkedSignalTrack(t)

	// Held key ORD-1 plus a correlation key derived from the "order_in" payload, set
	// before the loop starts so the loop reads them without a race.
	inst.corr.keys = map[string]string{"orderKey": "ORD-1"}
	inst.s.CorrelationKeys = []*bpmncommon.CorrelationKey{testCorrKey(t, "reply")}

	ctx, cancel := context.WithCancel(t.Context())
	go inst.loop(ctx, []*track{keeper})
	defer func() {
		cancel()
		select {
		case <-inst.Done():
		case <-time.After(2 * time.Second):
			t.Error("loop did not stop after cancellation")
		}
	}()

	const defID = "reply-catch"
	sub := subjectTrack(t, inst)
	// Park the subject and index its message catch-def id → track (FR-5/FR-8), so a track-less
	// Message evDeliver carrying that id resolves back to this track.
	inst.emit(trackEvent{kind: evWaiting, track: sub, msgDefIDs: []string{defID}})

	// Mismatch (derives ORD-2, conflicts with held ORD-1): resolved via msgIdx, gated and
	// dropped at the loop; the track stays in waiting + msgIdx.
	inst.emit(trackEvent{kind: evDeliver, eDef: msgEDefID(t, "reply", "ORD-2", defID)})
	select {
	case <-sub.evtCh:
		t.Fatal("a correlation mismatch must be dropped, not delivered")
	case <-time.After(100 * time.Millisecond):
	}

	// Match (derives ORD-1): the still-parked track receives it via msgIdx resolution.
	match := msgEDefID(t, "reply", "ORD-1", defID)
	inst.emit(trackEvent{kind: evDeliver, eDef: match})
	select {
	case got := <-sub.evtCh:
		require.Equal(t, match, got)
	case <-time.After(2 * time.Second):
		t.Fatal("a matching message must be delivered to the still-parked track")
	}
}

// TestInstanceProcessEventReachesParkedTrack: Instance.ProcessEvent — the Message hub entry
// (FR-8) — emits a track-less evDeliver that the loop resolves via msgIdx and dispatches; a nil
// definition is rejected before any emit.
func TestInstanceProcessEventReachesParkedTrack(t *testing.T) {
	inst, _, stop := loopHarness(t)
	defer stop()

	const defID = "msg-catch"
	sub := subjectTrack(t, inst)
	inst.emit(trackEvent{kind: evWaiting, track: sub, msgDefIDs: []string{defID}})

	// No CorrelationKeys configured → the gate is a no-op → the message is delivered.
	def := msgEDefID(t, "reply", "ORD-1", defID)
	require.NoError(t, inst.ProcessEvent(context.Background(), def))

	select {
	case got := <-sub.evtCh:
		require.Equal(t, def, got)
	case <-time.After(2 * time.Second):
		t.Fatal("Instance.ProcessEvent did not reach the parked track via msgIdx")
	}

	require.Error(t, inst.ProcessEvent(context.Background(), nil),
		"a nil EventDefinition must be rejected")
}

// TestLoopDropsTracklessDeliverWithNoIndex: a track-less Message evDeliver whose definition id
// is not in msgIdx (no parked receiver) is a benign drop — no panic, no send (SRD-027 FR-8).
func TestLoopDropsTracklessDeliverWithNoIndex(t *testing.T) {
	inst, _, stop := loopHarness(t)
	defer stop()

	// No evWaiting seeded msgIdx for this id; the loop must resolve to nil and drop.
	require.NoError(t, inst.ProcessEvent(context.Background(),
		msgEDefID(t, "reply", "ORD-1", "absent")))

	// Nothing to assert beyond the loop surviving — give it a tick, then the keeper-backed
	// loop is still draining (stop() verifies it stops cleanly).
	time.Sleep(50 * time.Millisecond)
}

// TestSpawnSeedsMsgIdxForParkedMessageTrack: a track that starts parked on a Message catch at
// construction (a parallelMultiple / instantiating-message start, parked before the loop drains
// events) is seeded into the loop's msgIdx by spawn — the construction-time companion to the
// evWaiting path (SRD-027 FR-5/FR-8). The loop then cancels the parked track cleanly on stop.
func TestSpawnSeedsMsgIdxForParkedMessageTrack(t *testing.T) {
	_ = data.CreateDefaultStates()

	p, err := process.New("srd027-msg-park")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	arm, err := events.NewIntermediateCatchEvent("marm", msgEDef(t, "reply", ""))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, arm, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, arm)
	link(t, arm, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).Return(nil).Maybe()

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)
	inst.tracks = map[string]*track{}

	// newTrack parks the track on the message catch and records its def ids (checkNodeType).
	mt, err := newTrack(arm, inst, nil)
	require.NoError(t, err)
	require.True(t, mt.inState(TrackWaitForEvent))
	require.NotEmpty(t, mt.msgDefIDs, "a message catch must record its definition ids")

	ctx, cancel := context.WithCancel(t.Context())
	go inst.loop(ctx, []*track{mt}) // spawn seeds msgIdx from mt.msgDefIDs before run() starts

	cancel()
	select {
	case <-inst.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not stop after cancellation")
	}
	require.Equal(t, Terminated, inst.State(),
		"the loop must terminate after the parked track stops")
}

// TestCheckNodeTypeRegistersPerTrigger: the hybrid boundary (FR-8/§3.7) — a Message catch
// registers the Instance as the hub processor, a Signal catch registers the track.
func TestCheckNodeTypeRegistersPerTrigger(t *testing.T) {
	t.Run("message catch registers the Instance", func(t *testing.T) {
		catch, err := events.NewIntermediateCatchEvent("m", msgEDef(t, "reply", ""))
		require.NoError(t, err)

		proc, inst, _ := registeredProcessorFor(t, catch)
		require.Same(t, inst, proc,
			"a Message catch must register the Instance (correlation owner)")
	})

	t.Run("signal catch registers the track", func(t *testing.T) {
		arm, _, _ := ebSignalArm(t, "go")

		proc, _, tr := registeredProcessorFor(t, arm)
		require.Same(t, tr, proc,
			"a Signal catch must register the track")
	})
}

// TestLoopMixedArmDeferredChoice: an Event-Based gateway with a Message arm (delivered via the
// Instance, track-less) and a Signal arm (delivered via the track) on the SAME track — the first
// delivery wins and flips the track out; the second arm's event is dropped (SRD-027 FR-4 mixed
// trigger).
func TestLoopMixedArmDeferredChoice(t *testing.T) {
	inst, _, stop := loopHarness(t)
	defer stop()

	const msgDefID = "mixed-msg"
	sub := subjectTrack(t, inst)
	// One track, parked on both a message arm (indexed) and a signal arm (track-carried).
	inst.emit(trackEvent{kind: evWaiting, track: sub, msgDefIDs: []string{msgDefID}})

	// The message arm fires first (track-less, resolved via msgIdx) and wins.
	msg := msgEDefID(t, "reply", "ORD-1", msgDefID)
	inst.emit(trackEvent{kind: evDeliver, eDef: msg})

	select {
	case got := <-sub.evtCh:
		require.Equal(t, msg, got)
	case <-time.After(2 * time.Second):
		t.Fatal("the winning message arm was not delivered")
	}

	// The signal arm fires after the flip (track-carried) and must be dropped — the track is
	// no longer in waiting, and the flip also cleared its msgIdx entry.
	sigArm, _, sigDef := ebSignalArm(t, "late")
	_ = sigArm
	inst.emit(trackEvent{kind: evDeliver, track: sub, eDef: sigDef})
	select {
	case <-sub.evtCh:
		t.Fatal("the losing (post-flip) arm must be dropped")
	case <-time.After(100 * time.Millisecond):
	}
}
