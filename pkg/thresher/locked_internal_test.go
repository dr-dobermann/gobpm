package thresher

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// regWithStarters builds a registration whose starter-slice length is n, so a
// promoted/returned slice is identifiable by length in the assertions below.
func regWithStarters(key string, version, n int) *ProcessRegistration {
	return &ProcessRegistration{
		key:      key,
		version:  version,
		starters: make([]*instanceStarter, n),
	}
}

// TestRemoveVersionLockedContract pins removeVersionLocked's return tuple:
// not-found, middle removal (no promote), latest removal (promote = the new
// latest's starters), and last-version removal (full drop + counter forgotten).
func TestRemoveVersionLockedContract(t *testing.T) {
	th, err := New("lk-remove-version")
	require.NoError(t, err)

	const key = "p"

	// Three versions whose starter-slice lengths encode their version, so a
	// promoted slice is identifiable by length.
	v1 := regWithStarters(key, 1, 1)
	v2 := regWithStarters(key, 2, 2)
	v3 := regWithStarters(key, 3, 3)
	th.registrations[key] = []*ProcessRegistration{v1, v2, v3}
	th.nextVersion[key] = 3

	// An unknown registration reports not-found and changes nothing.
	found, wasLatest, promote := th.removeVersionLocked(
		&ProcessRegistration{key: key, version: 9})
	require.False(t, found)
	require.False(t, wasLatest)
	require.Nil(t, promote)
	require.Equal(t, []*ProcessRegistration{v1, v2, v3}, th.registrations[key])

	// Removing the middle version: found, not latest, nothing to promote.
	found, wasLatest, promote = th.removeVersionLocked(v2)
	require.True(t, found)
	require.False(t, wasLatest)
	require.Nil(t, promote)
	require.Equal(t, []*ProcessRegistration{v1, v3}, th.registrations[key])

	// Removing the latest (v3) promotes the now-newest remaining (v1, starters
	// length 1).
	found, wasLatest, promote = th.removeVersionLocked(v3)
	require.True(t, found)
	require.True(t, wasLatest)
	require.Len(t, promote, 1)

	// Removing the last remaining version drops the key and forgets the counter.
	found, wasLatest, promote = th.removeVersionLocked(v1)
	require.True(t, found)
	require.True(t, wasLatest)
	require.Nil(t, promote)
	_, hasRegs := th.registrations[key]
	require.False(t, hasRegs)
	_, hasCounter := th.nextVersion[key]
	require.False(t, hasCounter)
}

// TestRemoveKeyLockedContract pins removeKeyLocked: unknown key reports
// not-existed; a populated key returns the latest version's starters and drops
// both the registrations and the version counter.
func TestRemoveKeyLockedContract(t *testing.T) {
	th, err := New("lk-remove-key")
	require.NoError(t, err)

	live, existed := th.removeKeyLocked("nope")
	require.False(t, existed)
	require.Nil(t, live)

	const key = "p"

	v1 := regWithStarters(key, 1, 1)
	v2 := regWithStarters(key, 2, 2)
	th.registrations[key] = []*ProcessRegistration{v1, v2}
	th.nextVersion[key] = 2

	live, existed = th.removeKeyLocked(key)
	require.True(t, existed)
	require.Len(t, live, 2) // the latest (v2) starters
	_, hasRegs := th.registrations[key]
	require.False(t, hasRegs)
	_, hasCounter := th.nextVersion[key]
	require.False(t, hasCounter)
}

// TestReserveReleaseKeyLocked pins the correlation-key reservation contract:
// the first reserve wins, a second is refused (a join), and releasing lets a
// later reserve win again.
func TestReserveReleaseKeyLocked(t *testing.T) {
	th, err := New("lk-reserve")
	require.NoError(t, err)

	require.True(t, th.reserveKeyLocked("k"))
	require.False(t, th.reserveKeyLocked("k"))

	th.releaseKeyLocked("k")
	require.True(t, th.reserveKeyLocked("k"))
}

// TestWiringClaimIsExclusive pins the claim handshake directly (FIX-036 §1.8).
// Its losing side — a second claim on an already-wired version — only happens
// when RegisterProcess and Run's sweep reach the same registration, which is a
// race no test can schedule; the primitive that decides it is pinned here
// instead, and T-8 pins the behaviour it produces.
func TestWiringClaimIsExclusive(t *testing.T) {
	th, err := New("claim", WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	reg := &ProcessRegistration{key: "k", id: "r1", version: 1}

	require.True(t, th.claimWiringLocked(reg), "the first claim wins")
	require.False(t, th.claimWiringLocked(reg),
		"a second claim finds the wiring already owned")

	th.releaseWiringLocked(reg)
	require.True(t, th.claimWiringLocked(reg),
		"a released claim can be taken again — promote-on-removal depends on it")

	// the sweep skips a version already claimed, and claims one that is not.
	th.registrations["k"] = []*ProcessRegistration{reg}
	require.Empty(t, th.claimLatestRegistrationsLocked(),
		"the sweep leaves an already-wired version alone")

	th.setLatestWiredLocked("k", false)
	require.Len(t, th.claimLatestRegistrationsLocked(), 1)

	// an unknown key is a no-op, not a panic.
	th.setLatestWiredLocked("absent", true)
}

// TestReservationIgnoresAnUntrackedOwner covers the takeover rule's other
// branch (FIX-036 §1.2): a reservation naming an id the registry no longer
// knows — forgotten, or lost with the engine that ran it — is not a live
// conversation, so the key is free.
func TestReservationIgnoresAnUntrackedOwner(t *testing.T) {
	th, err := New("ghost", WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	th.m.Lock()
	th.seenKeys["p\x1fORD-1"] = "an-instance-this-engine-never-heard-of"
	th.m.Unlock()

	require.True(t, th.reserveKeyLocked("p\x1fORD-1"),
		"a reservation whose owner is gone must not block a new conversation")
}

// TestRebindSkipsEmptyKeyValues: a checkpoint may carry a declared conversation
// key that was never derived (ADR-033 §2.1 records the map, not only the
// populated entries). An empty value identifies no conversation, so rebinding
// it would reserve the namespaced key "" for the instance and swallow the next
// unkeyed start.
func TestRebindSkipsEmptyKeyValues(t *testing.T) {
	th, err := New("rebind", WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	th.rebindKeysLocked("p", "i-1", map[string]string{
		"orderKey": "",
		"caseKey":  "C-9",
	})

	th.m.Lock()
	defer th.m.Unlock()

	require.Equal(t, map[string]string{nsKeyFor("p", "C-9"): "i-1"}, th.seenKeys,
		"only the derived key is reserved")
}

// TestRebuildReleasesThePreviousContext is FIX-037 T-4: every launch derives a
// child of the engine context and retains its cancel in instanceReg.stop. A
// rebuild REPLACES that registration, and the cancel it displaces must be run —
// otherwise the old child stays attached to the engine context's children for
// the engine's whole lifetime, and a dehydrating instance replaces its
// registration on every wake, so the leak is one context per CYCLE.
//
// FIX-036 §8.2 fixed the same defect in Forget and missed this path.
func TestRebuildReleasesThePreviousContext(t *testing.T) {
	th, err := New("track-displace", WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	proc := noneStartProcess(t, "p-track-displace")
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	handle, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	inst, err := th.instanceByID(handle.ID())
	require.NoError(t, err)

	// re-register the SAME instance, which is what a rebuild does.
	first, firstCancel := context.WithCancel(context.Background())
	h, displaced := th.trackInstanceLocked(inst, firstCancel, make(chan struct{}))
	require.NotNil(t, h)
	require.NotNil(t, displaced,
		"the launch's own cancel is displaced by this re-registration")
	stopDisplaced(displaced)

	// a rebuild of the SAME id hands back the previous cancel
	second, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()

	_, displaced = th.trackInstanceLocked(inst, secondCancel, make(chan struct{}))
	require.NotNil(t, displaced,
		"a rebuild must hand back the cancel it replaced")

	require.NoError(t, first.Err(), "the displaced context is still live")
	stopDisplaced(displaced)
	require.Error(t, first.Err(),
		"running the displaced cancel must end the previous context")
	require.NoError(t, second.Err(), "the current context is untouched")
}
