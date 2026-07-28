package instance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/repository"
)

// TestTrackDehydratedReleasesGoroutine covers SRD-071 T-1: closing a parked
// track's dehydrateCh makes its run() return in TrackDehydrated (the goroutine
// exits), and that return classifies as evDehydrated.
func TestTrackDehydratedReleasesGoroutine(t *testing.T) {
	val := false
	evals := 0

	def, err := events.NewConditionalEventDefinition(condExpr(t, &val, &evals))
	require.NoError(t, err)

	_, tr, _ := condInstance(t, def)
	require.True(t, tr.inState(TrackWaitForEvent))

	done := make(chan struct{})
	go func() {
		tr.run(context.Background())
		close(done)
	}()

	// A closed channel is permanently ready, so the release is delivered
	// whether run() is already parked on the select or reaches it after.
	ls := newLoopState(tr.instance)
	ls.dehydrateTrack(tr)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not return after dehydrateTrack")
	}

	require.True(t, tr.inState(TrackDehydrated),
		"a released track ends in TrackDehydrated")
	require.Equal(t, evDehydrated, trackEndKind(tr),
		"a TrackDehydrated return classifies as evDehydrated")
}

// TestApplyDehydratedRetainsRecord covers SRD-071 FR-1: applyDehydrated
// decrements the active count but retains the track record and its bookkeeping.
func TestApplyDehydratedRetainsRecord(t *testing.T) {
	val := false
	evals := 0

	def, err := events.NewConditionalEventDefinition(condExpr(t, &val, &evals))
	require.NoError(t, err)

	inst, tr, ls := condInstance(t, def)
	inst.tracks[tr.ID()] = tr
	ls.active = 1

	ls.applyDehydrated(tr)

	require.Equal(t, 0, ls.active, "the released goroutine is no longer active")
	require.Contains(t, inst.tracks, tr.ID(),
		"the track record is retained (its wait is held externally)")
	require.Contains(t, ls.waiting, tr.ID(),
		"the wait registry entry is retained")
	require.Contains(t, ls.position, tr.ID(),
		"the token position is retained (projects Alive at the wait node)")
}

// TestTrackDehydratedStateString: the new state renders.
func TestTrackDehydratedStateString(t *testing.T) {
	require.Equal(t, "TrackDehydrated", TrackDehydrated.String())
	require.Equal(t, "dehydrated", evDehydrated.String())
}

// userTaskSnapshot builds the start → UserTask → end snapshot whose only wait
// node is a Dehydratable human task (activities.UserTask.Dehydratable == true).
func userTaskSnapshot(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("dehy-ut")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	form, err := data.NewProperty("FORM_ID",
		data.MustItemDefinition(values.NewVariable("form-1")),
		data.ReadyDataState)
	require.NoError(t, err)

	ut, err := activities.NewUserTask("ut",
		activities.WithCandidateUsers("alice"),
		activities.WithOutput("result", "string", true),
		data.WithProperties(form),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ut, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, ut)
	link(t, ut, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// userTaskArmed builds a checkpoint-armed instance parked at its UserTask
// (built by newTrack, the condInstance direct-drive style), seeded the way
// spawn would leave the loopState. cpOwner is set (WithCheckpointing), so the
// detector's arming guard is satisfied; waitHeld is left at its nil default —
// each test injects the predicate it needs.
func userTaskArmed(t *testing.T) (*Instance, *track, *loopState) {
	t.Helper()

	s := userTaskSnapshot(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), &failDist{},
		WithCheckpointing("engine-A", time.Minute))
	require.NoError(t, err)
	inst.tracks = map[string]*track{}

	node := findNode(t, inst.s, "ut")

	tr, err := newTrack(node, inst, nil)
	require.NoError(t, err)
	require.True(t, tr.inState(TrackWaitForEvent),
		"a UserTask starts parked")

	inst.tracks[tr.ID()] = tr

	ls := newLoopState(inst)
	ls.position[tr.ID()] = node
	ls.waiting[tr.ID()] = struct{}{}
	ls.active = 1

	return inst, tr, ls
}

// held is the injected "this wait has an engine holder" predicate.
func held(*track) bool { return true }

// TestMaybeDehydrateReleasesIdleInstance covers SRD-071 T-2 (positive): a
// fully-idle instance — one track parked on a Dehydratable, held wait, the
// capture guards clear, checkpointing armed — is released: the detector arms
// ls.dehydrating and signals each parked track's dehydrateCh.
func TestMaybeDehydrateReleasesIdleInstance(t *testing.T) {
	inst, tr, ls := userTaskArmed(t)
	inst.waitHeld = held

	ls.maybeDehydrate(context.Background())

	require.True(t, ls.dehydrating, "a fully-idle held instance dehydrates")

	select {
	case <-tr.dehydrateCh:
		// released — the closed channel wakes the parked run().
	default:
		t.Fatal("the parked track was not signaled to release")
	}
}

// TestMaybeDehydrateStaysResident covers SRD-071 T-2 (negatives): each
// disqualifier — no holder, an in-flight capture guard, disarmed
// checkpointing, a non-Dehydratable wait, or a live executing track — keeps
// the instance resident (ls.dehydrating stays false, no track is signaled).
func TestMaybeDehydrateStaysResident(t *testing.T) {
	assertResident := func(t *testing.T, tr *track, ls *loopState) {
		t.Helper()

		ls.maybeDehydrate(context.Background())
		require.False(t, ls.dehydrating, "the instance must stay resident")

		if tr != nil {
			select {
			case <-tr.dehydrateCh:
				t.Fatal("a resident track must not be signaled to release")
			default:
			}
		}
	}

	t.Run("no engine holder for the wait", func(t *testing.T) {
		_, tr, ls := userTaskArmed(t)
		// waitHeld stays nil — nothing can wake a released wait.
		assertResident(t, tr, ls)
	})

	t.Run("a capture guard is in flight", func(t *testing.T) {
		inst, tr, ls := userTaskArmed(t)
		inst.waitHeld = held
		ls.calls["busy"] = nil // a Call Activity mid-construct

		assertResident(t, tr, ls)
	})

	t.Run("checkpointing is disarmed", func(t *testing.T) {
		inst, tr, ls := userTaskArmed(t)
		inst.waitHeld = held
		inst.cpOwner = "" // no repository — a released wait is unrecoverable

		assertResident(t, tr, ls)
	})

	t.Run("the wait node is not Dehydratable", func(t *testing.T) {
		// a conditional catch declares no Dehydratable capability.
		val := false
		evals := 0

		def, err := events.NewConditionalEventDefinition(
			condExpr(t, &val, &evals))
		require.NoError(t, err)

		inst, tr, ls := condInstance(t, def)
		inst.tracks[tr.ID()] = tr
		ls.active = 1
		inst.cpOwner = "engine-A" // arm the guard so eligibility is the sole gate
		inst.waitHeld = held

		assertResident(t, tr, ls)
	})

	t.Run("a live track is still executing", func(t *testing.T) {
		inst, parked, ls := userTaskArmed(t)
		inst.waitHeld = held

		// a second track doing work disqualifies the whole instance.
		busy := &track{state: TrackExecutingStep}
		inst.tracks["busy"] = busy
		ls.active = 2

		ls.maybeDehydrate(context.Background())
		require.False(t, ls.dehydrating,
			"an executing track keeps the instance resident")

		select {
		case <-parked.dehydrateCh:
			t.Fatal("no track may be released while one is executing")
		default:
		}
	})
}

// TestDehydrationLoopTail covers SRD-071 T-2 end-to-end: a running,
// checkpoint-armed instance whose sole track parks on a held UserTask
// dehydrates through the real loop — the loop releases the track, exits, sets
// Dehydrated, and writes the checkpoint carrying the TrackDehydrated track as
// the (in-flight) hydration source.
func TestDehydrationLoopTail(t *testing.T) {
	s := userTaskSnapshot(t)
	s.Version = 7 // the FR-1 pin a registration would stamp

	rt := enginert.Default()

	inst, err := New(s, scope.EmptyDataPath, rt,
		mockeventproc.NewMockEventProducer(t), &failDist{},
		WithCheckpointing("engine-A", time.Minute))
	require.NoError(t, err)

	// inject the holder predicate BEFORE Run: M2 has no engine holder yet, so
	// the test stands in for the M3+ wake source (production waitHeld is nil
	// until a holder lands, so an un-wokeable wait never dehydrates).
	inst.waitHeld = held

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, inst.Run(ctx))

	require.Eventually(t, func() bool { return inst.State() == Dehydrated },
		2*time.Second, 5*time.Millisecond,
		"a fully-idle held instance must reach Dehydrated")

	select {
	case <-inst.Done():
		// the loop released every goroutine and exited.
	case <-time.After(2 * time.Second):
		t.Fatal("the loop did not exit after dehydration")
	}

	rec, ok, err := rt.Repository().Load(context.Background(), inst.ID())
	require.NoError(t, err)
	require.True(t, ok, "the dehydration checkpoint must be written")
	require.Equal(t, repository.StatusActive, rec.Status,
		"a dehydrated instance persists as an in-flight record")

	doc, err := checkpoint.Unmarshal(rec.Payload)
	require.NoError(t, err)
	require.Len(t, doc.Tracks, 1, "the released track is the hydration source")
	require.Equal(t, TrackDehydrated.String(), doc.Tracks[0].State)
}

// TestDehydratedStateString: the new lifecycle state renders.
func TestDehydratedStateString(t *testing.T) {
	require.Equal(t, "Dehydrated", Dehydrated.String())
}

// TestRestoreWithPendingTrigger covers SRD-071 T-3 (FR-4): Restore with a
// PendingTrigger turns the named track's cold RE-ENTER into a wake — a fresh
// continuation fork that re-enters the wait node with the trigger already in
// hand, is NOT registered as a waiter, and whose persisted lineage inherits the
// dehydrated track's prev WITHOUT appending it (§4.1, bounded across cycles).
func TestRestoreWithPendingTrigger(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rt, inst, doc, cancel := parkAndInspect(t)
	cancel()

	_ = rt
	_ = inst

	require.Len(t, doc.Tracks, 1)

	// a recorded fork lineage the continuation must inherit verbatim.
	woken := doc.Tracks[0]
	woken.Prev = []string{"fork-a", "fork-b"}

	withPending := *doc
	withPending.Tracks = []checkpoint.TrackRecord{woken}

	s2 := condSnapshotFor(t, doc)

	ep := mockeventproc.NewMockEventProducer(t)
	// the continuation fork must NOT re-register: an unexpected RegisterEvent
	// fails the test, which IS the no-re-arm assertion.

	trigger := &events.SignalEventDefinition{}

	restored, err := Restore(&withPending, s2, scope.EmptyDataPath,
		enginert.Default(), ep, nil,
		&PendingTrigger{TrackID: woken.ID, EDef: trigger})
	require.NoError(t, err)

	require.Len(t, restored.tracks, 1, "the woken track rebuilt")

	for _, tr := range restored.tracks {
		require.NotEqual(t, woken.ID, tr.ID(),
			"the wake is a FRESH continuation fork, not the dehydrated track")
		require.True(t, tr.woken,
			"a continuation fork is marked woken (skips waiter registration)")
		require.Equal(t, []string{"fork-a", "fork-b"}, tr.prev,
			"the fork inherits the dehydrated track's lineage WITHOUT "+
				"appending it (§4.1)")
		require.Equal(t, woken.NodeID, tr.currentStep().node.ID(),
			"it re-enters the recorded WAIT node")
		require.True(t, tr.inState(TrackWaitForEvent),
			"it starts parked so run() reads the preloaded trigger")

		select {
		case got := <-tr.evtCh:
			require.Same(t, trigger, got,
				"the trigger is preloaded — run() fires the node through it")
		default:
			t.Fatal("the continuation fork carries no preloaded trigger")
		}
	}
}

// TestRestoreWithPendingTriggerGuards: a wake without a trigger definition is
// loud (a wake is trigger-PRESENT by definition — trigger-absent is the cold
// restart path).
func TestRestoreWithPendingTriggerGuards(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, _, doc, cancel := parkAndInspect(t)
	cancel()

	s2 := condSnapshotFor(t, doc)
	ep := mockeventproc.NewMockEventProducer(t)

	_, err := Restore(doc, s2, scope.EmptyDataPath,
		enginert.Default(), ep, nil,
		&PendingTrigger{TrackID: doc.Tracks[0].ID})
	require.Error(t, err)
	require.Contains(t, err.Error(), "needs a trigger")
}

// fakeHolders records HoldTimer/ReleaseTimer calls and can decline a hold.
type fakeHolders struct {
	held     map[string]time.Time
	subs     map[string]bool
	tasks    map[string]string
	released map[string]int
	decline  bool
}

func newFakeHolders() *fakeHolders {
	return &fakeHolders{
		held:     map[string]time.Time{},
		subs:     map[string]bool{},
		tasks:    map[string]string{},
		released: map[string]int{},
	}
}

func (f *fakeHolders) HoldTimer(
	instanceID, trackID string,
	_ flow.EventDefinition,
	deadline time.Time,
	_ int,
) error {
	if f.decline {
		return errors.New("declined")
	}

	f.held[instanceID+"|"+trackID] = deadline

	return nil
}

func (f *fakeHolders) HoldSubscription(
	instanceID, trackID string,
	eDef flow.EventDefinition,
	_ []string,
) error {
	if f.decline {
		return errors.New("declined")
	}

	f.subs[instanceID+"|"+trackID+"|"+eDef.ID()] = true

	return nil
}

func (f *fakeHolders) HoldTask(instanceID, trackID, taskID string) error {
	if f.decline {
		return errors.New("declined")
	}

	f.tasks[instanceID+"|"+trackID] = taskID

	return nil
}

func (f *fakeHolders) ReleaseWaits(instanceID, trackID string) {
	f.released[instanceID+"|"+trackID]++

	for k := range f.subs {
		if strings.HasPrefix(k, instanceID+"|"+trackID+"|") {
			delete(f.subs, k)
		}
	}
}

// timerTrack builds a track parked on a timer catch whose deadline is `in` from
// the instance's now, with holders injected. It returns the instance, the track
// and the holder spy.
func timerTrack(
	t *testing.T, in time.Duration, cycles int, opts ...Option,
) (*Instance, *track, *fakeHolders) {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	holders := newFakeHolders()

	p, err := process.New("dehy-timer-unit")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	rt := enginert.Default()
	when := rt.Clock().Now().Add(in)

	texpr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Time{})),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(when), nil
		})
	require.NoError(t, err)

	def, err := events.NewTimerEventDefinition(texpr, nil, nil)
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("timer-catch", def)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, catch, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, catch)
	link(t, catch, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	all := append([]Option{
		WithCheckpointing("engine-A", time.Minute),
		WithWaitHolders(holders),
	}, opts...)

	inst, err := New(s, scope.EmptyDataPath, rt, ep, nil, all...)
	require.NoError(t, err)
	inst.tracks = map[string]*track{}

	node := findNode(t, inst.s, "timer-catch")

	tr, err := newTrack(node, inst, nil)
	require.NoError(t, err)

	// a cyclic plan is stashed post-hoc: TimerPlan derives cycles from the
	// definition, and this fixture drives the cycles branch directly.
	if cycles > 0 {
		tr.m.Lock()
		tr.timerCycles = cycles
		tr.m.Unlock()
	}

	inst.tracks[tr.ID()] = tr

	return inst, tr, holders
}

// TestHoldTimerAcceptsLongOneShot covers SRD-071 FR-3/FR-6: a long one-shot
// timer hands its ABSOLUTE deadline to the engine holder at arm time and marks
// the track held — the idle detector's wakeability gate.
func TestHoldTimerAcceptsLongOneShot(t *testing.T) {
	inst, tr, holders := timerTrack(t, 3*time.Hour, 0)

	require.True(t, tr.held.Load(),
		"a held wait marks its track wakeable")
	require.Contains(t, holders.held, inst.ID()+"|"+tr.ID(),
		"the deadline is registered with the engine holder")
}

// TestHoldTimerDeclines covers the fall-back-to-resident cases: no holder
// registry, a sub-threshold deadline, a cyclic timer, and a holder that
// declines — each keeps the wait resident on its in-hub waiter, never losing
// the timer.
func TestHoldTimerDeclines(t *testing.T) {
	t.Run("no holder registry", func(t *testing.T) {
		require.NoError(t, data.CreateDefaultStates())

		inst, tr, _ := timerTrack(t, 3*time.Hour, 0)
		inst.waitHolders = nil
		tr.held.Store(false)

		require.False(t, tr.holdTimer(nil),
			"without a registry nothing can be held")
	})

	t.Run("a sub-threshold deadline", func(t *testing.T) {
		_, tr, holders := timerTrack(t, 5*time.Minute, 0)

		require.False(t, tr.held.Load(),
			"a short timer is not worth a hydrate round-trip")
		require.Empty(t, holders.held)
	})

	t.Run("a cyclic timer", func(t *testing.T) {
		_, tr, _ := timerTrack(t, 3*time.Hour, 0)

		tr.m.Lock()
		tr.timerCycles = 3
		tr.m.Unlock()
		tr.held.Store(false)

		require.False(t, tr.holdTimer(nil),
			"a repeating timer would lose its later cycles to a fire-once wake")
	})

	t.Run("a zero deadline", func(t *testing.T) {
		_, tr, _ := timerTrack(t, 3*time.Hour, 0)

		tr.m.Lock()
		tr.timerDeadline = time.Time{}
		tr.m.Unlock()
		tr.held.Store(false)

		require.False(t, tr.holdTimer(nil), "an unstashed plan holds nothing")
	})

	t.Run("the holder declines", func(t *testing.T) {
		_, tr, holders := timerTrack(t, 3*time.Hour, 0)

		holders.decline = true
		tr.held.Store(false)

		require.False(t, tr.holdTimer(nil),
			"a declined hold keeps the wait resident, never lost")
	})
}

// TestHeldWaitReleasesOnTeardown covers the withdraw paths (SRD-071 FR-3): a
// held deadline must not outlive its instance — stopAll withdraws it.
func TestHeldWaitReleasesOnTeardown(t *testing.T) {
	inst, tr, holders := timerTrack(t, 3*time.Hour, 0)
	require.True(t, tr.held.Load())

	// spawn normally seeds the per-track cancel; this track was built directly.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	tr.ctx = ctx
	tr.cancel = cancel

	ls := newLoopState(inst)
	ls.stopAll()

	require.Equal(t, 1, holders.released[inst.ID()+"|"+tr.ID()],
		"teardown withdraws the held deadline")
	require.False(t, tr.held.Load(), "the withdraw is one-shot")
}

// TestWakeParkedTrack covers SRD-071 FR-3's resident fork: a holder's trigger
// for a RESIDENT instance delivers into the live parked track via evDeliver; a
// nil trigger is loud and an unknown/stale track id is a no-op.
func TestWakeParkedTrack(t *testing.T) {
	val := false
	evals := 0

	def, err := events.NewConditionalEventDefinition(condExpr(t, &val, &evals))
	require.NoError(t, err)

	inst, tr, _ := condInstance(t, def)
	inst.addToSnap(tr)

	t.Run("a nil trigger is loud", func(t *testing.T) {
		_, err := inst.WakeParkedTrack(tr.ID(), nil)
		require.Error(t, err)
	})

	t.Run("an unknown track is a stale no-op", func(t *testing.T) {
		delivered, err := inst.WakeParkedTrack("no-such-track", def)
		require.NoError(t, err)
		require.True(t, delivered, "a stale fire is benignly dropped")
	})

	t.Run("the parked track receives the trigger", func(t *testing.T) {
		got := make(chan trackEvent, 1)
		go func() { got <- <-inst.events }()

		delivered, err := inst.WakeParkedTrack(tr.ID(), def)
		require.NoError(t, err)
		require.True(t, delivered)

		ev := <-got
		require.Equal(t, evDeliver, ev.kind)
		require.Same(t, tr, ev.track)
		require.Equal(t, def.ID(), ev.eDef.ID())
	})
}

// TestWakeParkedTrackNoSnapshot: an instance with no track snapshot yet
// (nothing spawned) drops the stale trigger instead of panicking.
func TestWakeParkedTrackNoSnapshot(t *testing.T) {
	val := false
	evals := 0

	def, err := events.NewConditionalEventDefinition(condExpr(t, &val, &evals))
	require.NoError(t, err)

	inst, _, _ := condInstance(t, def)
	inst.tracksSnap.Store(nil)

	delivered, err := inst.WakeParkedTrack("any", def)
	require.NoError(t, err)
	require.True(t, delivered)
}

// TestDehydratableParkedRetainsReleased covers the detector's already-released
// branch: a track flipped TrackDehydrated by a PRIOR pass is a retained record,
// not a disqualifier and not re-released.
func TestDehydratableParkedRetainsReleased(t *testing.T) {
	inst, tr, ls := userTaskArmed(t)
	inst.waitHeld = held

	tr.updateState(TrackDehydrated)

	require.Nil(t, ls.dehydratableParked(context.Background()),
		"an instance with nothing left to release does not re-dehydrate")
}

// TestOfferReportsAReleasedLoop covers the no-lost-trigger rule (SRD-071
// NFR-1/§4.6): a delivery offered to an instance whose loop has GONE reports
// that it was not taken, so the engine wakes the instance from its checkpoint
// instead of dropping the trigger.
func TestOfferReportsAReleasedLoop(t *testing.T) {
	val := false
	evals := 0

	def, err := events.NewConditionalEventDefinition(condExpr(t, &val, &evals))
	require.NoError(t, err)

	inst, tr, _ := condInstance(t, def)
	inst.addToSnap(tr)

	// the loop has exited (the dehydration tail closes loopDone).
	close(inst.loopDone)

	delivered, err := inst.WakeParkedTrack(tr.ID(), def)
	require.NoError(t, err)
	require.False(t, delivered,
		"a released loop must report the trigger as NOT taken")

	// the message shape reports the same way (it routes track-less).
	require.False(t, inst.offer(trackEvent{kind: evDeliver, eDef: def}))
}

// TestWokenForkRunsThrough covers the continuation fork END TO END inside the
// instance (SRD-071 FR-4): a Restore carrying a PendingTrigger — with prepared
// node input — runs the woken track through its wait node without re-arming and
// on to completion. It exercises the spawn path's woken guard: the fork is
// never registered as a waiter, so nothing waits for a trigger already in hand.
func TestWokenForkRunsThrough(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, _, doc, cancel := parkAndInspect(t)
	cancel()

	require.Len(t, doc.Tracks, 1)

	s2 := condSnapshotFor(t, doc)

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()
	ep.EXPECT().UnregisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	val := true
	evals := 0

	trigger, err := events.NewConditionalEventDefinition(
		condExpr(t, &val, &evals))
	require.NoError(t, err)

	restored, err := Restore(doc, s2, scope.EmptyDataPath,
		enginert.Default(), ep, nil,
		&PendingTrigger{
			TrackID: doc.Tracks[0].ID,
			EDef:    trigger,
			Data:    []data.Data{mustParam(t, "woken", 1)},
		})
	require.NoError(t, err)

	// the prepared input landed in the woken track's scope before it fired.
	own, err := restored.sc.plane.OwnData(restored.sc.root)
	require.NoError(t, err)

	var seeded bool

	for _, d := range own {
		if d.Name() == "woken" {
			seeded = true
		}
	}

	require.True(t, seeded, "the trigger's prepared input is committed")

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	require.NoError(t, restored.Run(runCtx))

	require.Eventually(t, func() bool {
		return restored.State() == Completed
	}, 3*time.Second, 5*time.Millisecond,
		"the continuation fork must fire through its wait node and finish")
}

// TestContinuationTrackInputFailureIsLoud covers the wake's prepared-input
// guard (SRD-071 FR-4): input that cannot be committed fails the wake loudly
// rather than firing the woken node against a half-seeded scope.
func TestContinuationTrackInputFailureIsLoud(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	_, _, doc, cancel := parkAndInspect(t)
	cancel()

	s2 := condSnapshotFor(t, doc)
	ep := mockeventproc.NewMockEventProducer(t)

	val := true
	evals := 0

	trigger, err := events.NewConditionalEventDefinition(
		condExpr(t, &val, &evals))
	require.NoError(t, err)

	// the woken track records a scope that the restore never reopened, so its
	// prepared input has nowhere to land.
	broken := *doc
	broken.Tracks = append([]checkpoint.TrackRecord{}, doc.Tracks...)
	broken.Tracks[0].ScopePath = "/no/such/scope"

	_, err = Restore(&broken, s2, scope.EmptyDataPath,
		enginert.Default(), ep, nil,
		&PendingTrigger{
			TrackID: broken.Tracks[0].ID,
			EDef:    trigger,
			Data:    []data.Data{mustParam(t, "orphan", 1)},
		})
	require.Error(t, err)
	require.Contains(t, err.Error(), "prepared input")
}

// TestValidatedTemplateGuards covers New's collaborator guards, which the
// SRD-071 refactor moved into validatedTemplate: each missing dependency is a
// classified error, never a nil-dereference later.
func TestValidatedTemplateGuards(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	s := userTaskSnapshot(t)
	ep := mockeventproc.NewMockEventProducer(t)

	t.Run("a nil snapshot", func(t *testing.T) {
		_, err := New(nil, scope.EmptyDataPath, enginert.Default(), ep, nil)
		require.Error(t, err)
	})

	t.Run("a nil engine runtime", func(t *testing.T) {
		_, err := New(s, scope.EmptyDataPath, nil, ep, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "engine runtime")
	})

	t.Run("a nil event producer", func(t *testing.T) {
		_, err := New(s, scope.EmptyDataPath, enginert.Default(), nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "event producer")
	})
}

// TestWireWaitHeldWithoutHolders: without an injected holder registry the
// wakeability gate stays nil, so nothing ever dehydrates (the safe default).
func TestWireWaitHeldWithoutHolders(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst, err := New(userTaskSnapshot(t), scope.EmptyDataPath,
		enginert.Default(), mockeventproc.NewMockEventProducer(t), nil,
		WithCheckpointing("engine-A", time.Minute))
	require.NoError(t, err)

	require.Nil(t, inst.waitHeld,
		"no holder registry means no wait is ever releasable")
}

// signalTrack builds a track parked on a SIGNAL catch with holders injected —
// the subscription-hold counterpart of timerTrack.
func signalTrack(t *testing.T) (*Instance, *track, *fakeHolders) {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	holders := newFakeHolders()

	p, err := process.New("dehy-sig-unit")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	sig, err := events.NewSignal("unit-go", nil)
	require.NoError(t, err)

	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("sig-catch", def)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, catch, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, catch)
	link(t, catch, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil,
		WithCheckpointing("engine-A", time.Minute),
		WithWaitHolders(holders))
	require.NoError(t, err)
	inst.tracks = map[string]*track{}

	tr, err := newTrack(findNode(t, inst.s, "sig-catch"), inst, nil)
	require.NoError(t, err)

	inst.tracks[tr.ID()] = tr

	return inst, tr, holders
}

// TestHoldSubscription covers SRD-071 FR-7's arm-time handover: a signal wait
// registers its subscription with the ENGINE instead of the hub, marking the
// track held; without a registry, or when the holder declines, the wait stays
// resident on its own subscription — never lost.
func TestHoldSubscription(t *testing.T) {
	t.Run("the engine takes the subscription", func(t *testing.T) {
		inst, tr, holders := signalTrack(t)

		require.True(t, tr.held.Load(),
			"a held subscription makes the wait releasable")
		require.Len(t, holders.subs, 1,
			"the engine holds the subscription, not the instance")

		// the withdraw drops it again.
		inst.waitHolders.ReleaseWaits(inst.ID(), tr.ID())
		require.Empty(t, holders.subs)
	})

	t.Run("no registry declines", func(t *testing.T) {
		inst, tr, _ := signalTrack(t)
		inst.waitHolders = nil

		require.False(t, tr.holdSubscription(nil),
			"without a registry nothing can be held")
	})

	t.Run("a declining holder keeps the wait resident", func(t *testing.T) {
		_, tr, holders := signalTrack(t)

		holders.decline = true

		require.False(t, tr.holdSubscription(
			tr.currentStep().node.(flow.EventNode).Definitions()[0]),
			"a declined hold falls back to the instance's own subscription")
	})
}

// TestWakeParkedTrackRoutesMessagesTrackless covers the correlation-preserving
// shape (SRD-071 FR-7): a MESSAGE delivery must reach the loop track-LESS, so
// the loop's own correlation gate (which keys on exactly that) still runs.
func TestWakeParkedTrackRoutesMessagesTrackless(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst, tr, _ := signalTrack(t)
	inst.addToSnap(tr)
	inst.setState(Active)

	msg := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("route-me", data.MustItemDefinition(
			values.NewVariable(""))), nil)

	got := make(chan trackEvent, 1)
	go func() { got <- <-inst.events }()

	delivered, err := inst.WakeParkedTrack(tr.ID(), msg)
	require.NoError(t, err)
	require.True(t, delivered)

	ev := <-got
	require.Equal(t, evDeliver, ev.kind)
	require.Nil(t, ev.track,
		"a message routes track-less so the loop's correlation gate applies")
	require.Equal(t, msg.ID(), ev.eDef.ID())
}

// TestWakeRefusesAForeignConversation covers the wake's authoritative
// correlation rule (SRD-071 FR-7, ADR-016): the continuation fork bypasses the
// loop's dispatch gate, so the REBUILT instance applies the rule itself — a
// message whose derived key contradicts the recorded conversation refuses the
// wake (classified as a benign drop) instead of firing the woken node.
func TestWakeRefusesAForeignConversation(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	msgName := "payment received"

	// the correlation key reads the order id out of the message payload.
	keyExpr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "pay_in")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(
				fmt.Sprint(d.Value().Get(ctx))), nil
		})
	require.NoError(t, err)

	msg := bpmncommon.MustMessage(msgName, data.MustItemDefinition(
		values.NewVariable(""), foundation.WithID("pay_in")))

	re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(
		keyExpr, msg)
	require.NoError(t, err)

	prop, err := bpmncommon.NewCorrelationProperty("orderId", "string",
		[]bpmncommon.CorrelationPropertyRetrievalExpression{*re})
	require.NoError(t, err)

	corrKey, err := bpmncommon.NewCorrelationKey("orderKey",
		[]bpmncommon.CorrelationProperty{*prop})
	require.NoError(t, err)

	p, err := process.New("dehy-corr")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(events.MustMessageEventDefinition(msg, nil)),
		events.WithCorrelationKey(corrKey))
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("await",
		events.MustMessageEventDefinition(msg, nil))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, catch, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, catch)
	link(t, catch, end)

	// the process-level subscription is what publishes the key to the snapshot
	// (a start event's key seeds the born instance; the DECLARED key is what
	// validateAndAssociate rules on).
	p.CorrelationSubscriptions = []*bpmncommon.CorrelationSubscription{
		{Key: corrKey},
	}

	s, err := snapshot.New(p)
	require.NoError(t, err)
	require.NotEmpty(t, s.CorrelationKeys,
		"the process must declare its conversation key")

	catchNode := findNode(t, s, "await")

	// the recorded instance is mid-conversation with ORD-1.
	doc := &checkpoint.Document{
		InstanceID: "corr-inst",
		ProcessID:  s.ProcessID,
		Version:    s.Version,
		Status:     "Active",
		ConvKeys:   map[string]string{"orderKey": "ORD-1"},
		Tracks: []checkpoint.TrackRecord{{
			ID:     "trk-1",
			State:  TrackDehydrated.String(),
			NodeID: catchNode.ID(),
		}},
	}

	// the arriving message belongs to ORD-9 — another conversation.
	foreign := events.MustMessageEventDefinition(
		bpmncommon.MustMessage(msgName, data.MustItemDefinition(
			values.NewVariable("ORD-9"), foundation.WithID("pay_in"))), nil)

	ep := mockeventproc.NewMockEventProducer(t)
	ep.EXPECT().RegisterEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	_, err = Restore(doc, s, scope.EmptyDataPath,
		enginert.Default(), ep, nil,
		&PendingTrigger{TrackID: "trk-1", EDef: foreign})

	require.Error(t, err, "a foreign conversation must not wake the instance")
	require.Contains(t, err.Error(), "another conversation")
}
