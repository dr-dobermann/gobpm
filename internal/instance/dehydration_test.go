package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
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
