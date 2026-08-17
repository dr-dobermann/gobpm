package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// hostAwaitingScope makes tr read as a composite host parked for its body's
// drain: the executor it is running says awaitScope, which is the only thing
// the residency rule asks (SRD-090.A FR-8).
//
// Built directly rather than by running a Sub-Process, because the property
// under test is what the LOOP does with a host in that state, and driving a
// real body to the same point adds a body's worth of timing to a decision
// that is made in one pass over the track table.
func hostAwaitingScope(t *testing.T, inst *Instance, tr *track) {
	t.Helper()

	e := newPlainScopeExec(tr, &stepInfo{node: tr.currentStep().node})
	e.parked.Store(true)
	tr.exec.Store(&execHandle{e: e})

	// what a real host reads as while its body runs (ADR-025 §2.13b.1e) —
	// the fixture has to say so, because newTrack on a UserTask node leaves
	// the track parked in TrackWaitForEvent, which is a different arm of the
	// residency switch entirely.
	tr.updateState(TrackHostingScope)

	require.Equal(t, awaitScope, tr.awaits(),
		"the host reports what its executor awaits")
	require.NotNil(t, inst)
}

// TestIteratedHostReleasesWithItsBody (T-6, SRD-090.A FR-8): a composite host
// parked for its body's drain does not pin its instance. The body's parked,
// held wait is what decides — and when it releases, the host goes with it.
//
// This is the case ADR-007 v.2.1 exists for: an iterated Sub-Process holding
// parked User Tasks used to pin its process instance in memory for as long as
// the approvals took, because the host reached dehydratableParked's default
// arm — "live but not a long wait" — and disqualified the whole instance.
func TestIteratedHostReleasesWithItsBody(t *testing.T) {
	inst, body, ls := userTaskArmed(t)
	inst.waitHeld = held

	// the host: a second track on the same instance, parked for a drain.
	host, err := newTrack(body.currentStep().node, inst, nil)
	require.NoError(t, err)

	inst.tracks[host.ID()] = host
	hostAwaitingScope(t, inst, host)

	ls.maybeDehydrate(context.Background())

	require.True(t, ls.dehydrating,
		"the host holds no wait of its own — the body's decides")

	for name, tr := range map[string]*track{"body": body, "host": host} {
		select {
		case <-tr.dehydrateCh:
			// released.
		default:
			t.Fatalf("the %s track was not signaled to release", name)
		}
	}
}

// TestAHostIsNeverTheReasonAnInstanceReleases (T-7, SRD-090.A FR-8): a scope
// host rides along with the body's waits; it cannot BE one.
//
// ADR-007 §2.4 is per-WAIT, and a host holds none: nothing external can wake
// it, because what it waits for — its body's drain — arrives from inside. An
// instance released on a host alone would park on a drain that was about to
// happen and would never be woken by anything. Found by the existing restore
// suites, which dehydrated between passes when the first version of this rule
// let a host into the released set on its own.
func TestAHostIsNeverTheReasonAnInstanceReleases(t *testing.T) {
	inst, body, ls := userTaskArmed(t)
	inst.waitHeld = held

	host, err := newTrack(body.currentStep().node, inst, nil)
	require.NoError(t, err)

	inst.tracks[host.ID()] = host
	hostAwaitingScope(t, inst, host)

	// the body's wait goes away — its track ended, as it does between the
	// passes of a sequential composite. The host is now the only live track.
	body.updateState(TrackEnded)
	delete(ls.waiting, body.ID())

	ls.maybeDehydrate(context.Background())

	require.False(t, ls.dehydrating,
		"a host alone is not a reason to release — the drain it waits for "+
			"arrives from inside, and no trigger would ever hydrate it back")

	select {
	case <-host.dehydrateCh:
		t.Fatal("the host was released with nothing to wake it")
	default:
	}
}

// TestAnUnholdableWaitAmongNKeepsTheInstanceResident (T-7, the conjunction):
// releasability over a set is the conjunction of its members'. One body track
// whose wait no holder took keeps the whole instance resident, host included.
func TestAnUnholdableWaitAmongNKeepsTheInstanceResident(t *testing.T) {
	inst, body, ls := userTaskArmed(t)

	host, err := newTrack(body.currentStep().node, inst, nil)
	require.NoError(t, err)

	inst.tracks[host.ID()] = host
	hostAwaitingScope(t, inst, host)

	// nothing holds the body's wait.
	inst.waitHeld = func(*track) bool { return false }

	ls.maybeDehydrate(context.Background())

	require.False(t, ls.dehydrating,
		"one unholdable wait disqualifies the set")

	select {
	case <-host.dehydrateCh:
		t.Fatal("the host was released while its instance stayed resident")
	default:
	}
}

// TestAnInstanceCarryingAnOpenIncidentDoesNotRelease (T-17, SRD-090.A FR-8 /
// M3d): the incident park and the dehydration park must not both claim the
// instance.
//
// The loop's tail tries dehydration BEFORE parkOnIncidents, so the two were
// never mutually exclusive by position. They were mutually exclusive by
// accident: a failing track ends TrackIncident, which is absent from
// liveTrackStates and so reads as a terminal record, while its composite HOST
// disqualified the instance through the default arm. FR-8 removes that second
// half, so the guard has to be stated.
func TestAnInstanceCarryingAnOpenIncidentDoesNotRelease(t *testing.T) {
	inst, body, ls := userTaskArmed(t)
	inst.waitHeld = held

	host, err := newTrack(body.currentStep().node, inst, nil)
	require.NoError(t, err)

	inst.tracks[host.ID()] = host
	hostAwaitingScope(t, inst, host)

	// a third track failed and its incident is open, awaiting a retry or an
	// operator — the continuation lives in the incident record, not in a
	// wait anything could hold.
	failed, err := newTrack(body.currentStep().node, inst, nil)
	require.NoError(t, err)

	failed.updateState(TrackIncident)
	inst.tracks[failed.ID()] = failed
	inst.incidents = map[string]*incident{
		"inc-1": {id: "inc-1", state: incidentOpen, trackID: failed.ID()},
	}

	ls.maybeDehydrate(context.Background())

	require.False(t, ls.dehydrating,
		"an open incident owns the park — the instance is waiting for a "+
			"retry or an operator, not for a trigger")

	select {
	case <-host.dehydrateCh:
		t.Fatal("released while an incident owned the park")
	default:
	}
}

// TestAnUnheldBoundaryOnTheHostKeepsTheInstanceResident (SRD-090.A FR-8, the
// per-arm half): a host whose guarding boundary nothing holds keeps its
// instance resident, exactly as an unheld boundary on a parked track does
// (SRD-071 FR-9a).
//
// A boundary watch is not a track, so it never reaches waitReleasable — yet
// an unheld one dies with the released instance, and "approve within 24h or
// escalate" would simply never escalate. The composite is the guarded
// activity here, and its body running inside does not change that.
func TestAnUnheldBoundaryOnTheHostKeepsTheInstanceResident(t *testing.T) {
	inst, body, ls := userTaskArmed(t)
	inst.waitHeld = held

	host, err := newTrack(body.currentStep().node, inst, nil)
	require.NoError(t, err)

	inst.tracks[host.ID()] = host
	hostAwaitingScope(t, inst, host)

	// a boundary guards the composite, and nothing took it.
	ls.watchers[host.ID()] = []*boundaryWatch{{host: host, held: false}}

	ls.maybeDehydrate(context.Background())

	require.False(t, ls.dehydrating,
		"an unheld boundary on the HOST disqualifies the instance, for the "+
			"same reason it does on a parked track")

	// and with a holder, the same instance releases — so the assertion above
	// is about the boundary and not about some other disqualifier.
	ls.watchers[host.ID()][0].held = true

	ls.maybeDehydrate(context.Background())

	require.True(t, ls.dehydrating, "a held boundary costs no residency")
}

// TestAReleasedHostUnwindsWithoutFailing (SRD-090.A FR-8, the release path):
// the loop closes the host's dehydrateCh while it is parked for a drain that
// will never arrive in this process. The executor reports errDehydrated —
// neither a discard nor a failure — and the track ends TrackDehydrated, which
// is what the checkpoint persists as the hydration source.
func TestAReleasedHostUnwindsWithoutFailing(t *testing.T) {
	inst, body, _ := userTaskArmed(t)

	host, err := newTrack(body.currentStep().node, inst, nil)
	require.NoError(t, err)

	e := newPlainScopeExec(host, &stepInfo{node: host.currentStep().node})

	// the loop released this host: the body is parked on a held wait, the
	// whole instance is going away, and the scope stays open behind it.
	close(host.dehydrateCh)

	require.ErrorIs(t, e.awaitDrain(t.Context()), errDehydrated)
	require.True(t, host.inState(TrackDehydrated),
		"the released host persists as the record a restore re-enters from")
}

// TestADrainStillWinsOverAReleaseThatNeverCame: the release case is an added
// arm, not a replacement — a scope that drains normally still returns nil and
// leaves the host live.
func TestADrainStillWinsOverAReleaseThatNeverCame(t *testing.T) {
	inst, body, _ := userTaskArmed(t)

	host, err := newTrack(body.currentStep().node, inst, nil)
	require.NoError(t, err)

	e := newPlainScopeExec(host, &stepInfo{node: host.currentStep().node})

	close(e.drain)

	require.NoError(t, e.awaitDrain(t.Context()))
	require.False(t, host.inState(TrackDehydrated))
	require.NotNil(t, inst)
}

// compositeHeldWaitSnapshot builds start → Sub-Process(start → signal catch →
// end) → end: the host hosts a scope while the body's own token parks on a
// wait a holder can take. A SIGNAL catch rather than a conditional one — a
// conditional wait is loop-owned and never holdable (SRD-048 FR-15), so it
// would keep the instance resident for a reason that has nothing to do with
// the host.
func compositeHeldWaitSnapshot(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("resi-sp")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID("resi-start"))
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body",
		foundation.WithID("resi-body"))
	require.NoError(t, err)

	sig, err := events.NewSignal("resi-sig", nil)
	require.NoError(t, err)

	sdef, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start",
		foundation.WithID("resi-b-start"))
	require.NoError(t, err)

	catch, err := events.NewIntermediateCatchEvent("b-catch", sdef,
		foundation.WithID("resi-b-catch"))
	require.NoError(t, err)

	bEnd, err := events.NewEndEvent("b-end", foundation.WithID("resi-b-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, catch, bEnd} {
		require.NoError(t, body.Add(e))
	}

	link(t, bStart, catch)
	link(t, catch, bEnd)

	end, err := events.NewEndEvent("end", foundation.WithID("resi-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, body, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, body)
	link(t, body, end)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	return s
}

// TestAReleasedHostEndsDehydratedNotFailed (SRD-090.A FR-8, the runner's
// unwind): a real host, released mid-activity by a real loop, ends
// TrackDehydrated — not Failed and not Canceled.
//
// The distinction is the whole point of the sentinel. `executeStep` returning
// an error normally means discard-or-fail, and a host released while holding
// a scope open returns one; classified as either, the instance would tear
// down the very activity dehydration exists to preserve. Driven through the
// loop rather than by calling awaitDrain directly, because what is under test
// is the classification the run loop makes.
func TestAReleasedHostEndsDehydratedNotFailed(t *testing.T) {
	s := compositeHeldWaitSnapshot(t)

	inst, err := New(s, scope.EmptyDataPath, cpRuntime(t), laxEP(t), nil,
		WithCheckpointing("engine-A", "engine-A", time.Minute))
	require.NoError(t, err)

	inst.waitHeld = held

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t, func() bool {
		return inst.State() == Dehydrated
	}, 3*time.Second, 5*time.Millisecond,
		"the body's held wait releases the instance, host included")

	var host *track

	for _, tr := range inst.tracks {
		if tr.currentStep().node.ID() == "resi-body" {
			host = tr
		}
	}

	require.NotNil(t, host, "the composite host is a retained record")
	require.True(t, host.inState(TrackDehydrated),
		"a released host unwinds to Dehydrated — a discard or a failure "+
			"here would tear down the activity dehydration exists to keep")
}
