package instance

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
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
