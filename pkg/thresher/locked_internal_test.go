package thresher

import (
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
