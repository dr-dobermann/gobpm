package instance

import (
	"context"
	"errors"
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
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
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
	released map[string]int
	decline  bool
}

func newFakeHolders() *fakeHolders {
	return &fakeHolders{
		held:     map[string]time.Time{},
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

func (f *fakeHolders) ReleaseTimer(instanceID, trackID string) {
	f.released[instanceID+"|"+trackID]++
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
		require.Error(t, inst.WakeParkedTrack(tr.ID(), nil))
	})

	t.Run("an unknown track is a stale no-op", func(t *testing.T) {
		require.NoError(t, inst.WakeParkedTrack("no-such-track", def))
	})

	t.Run("the parked track receives the trigger", func(t *testing.T) {
		got := make(chan trackEvent, 1)
		go func() { got <- <-inst.events }()

		require.NoError(t, inst.WakeParkedTrack(tr.ID(), def))

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

	require.NoError(t, inst.WakeParkedTrack("any", def))
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
