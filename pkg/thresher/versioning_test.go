package thresher_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// TestRegisterProcessReturnsHandle verifies RegisterProcess returns a
// registration handle naming the (key, version) of the registered definition
// (SRD-031.A FR-2, T-3): the key is the process id, the first version is 1, and
// the registration id is non-empty.
func TestRegisterProcessReturnsHandle(t *testing.T) {
	th, err := thresher.New("reg-handle")
	require.NoError(t, err)

	proc := linearProcess(t, "p-handle", 0)

	reg, err := th.RegisterProcess(proc)
	require.NoError(t, err)
	require.NotNil(t, reg)
	require.Equal(t, proc.ID(), reg.Key())
	require.Equal(t, 1, reg.Version())
	require.NotEmpty(t, reg.ID())
}

// TestReRegisterCreatesNewVersion verifies that re-registering the same key
// mints a new version rather than the old silent no-op (SRD-031.A FR-3, T-4):
// versions increment per key in registration order, the key is shared, and each
// registration has a distinct registration id.
func TestReRegisterCreatesNewVersion(t *testing.T) {
	th, err := thresher.New("reg-version")
	require.NoError(t, err)

	proc := linearProcess(t, "p-version", 0)

	reg1, err := th.RegisterProcess(proc)
	require.NoError(t, err)

	reg2, err := th.RegisterProcess(proc)
	require.NoError(t, err)

	require.Equal(t, reg1.Key(), reg2.Key())
	require.Equal(t, 1, reg1.Version())
	require.Equal(t, 2, reg2.Version())
	require.NotEqual(t, reg1.ID(), reg2.ID())
}

// TestAnonymousProcessesAreDistinctKeys verifies that processes registered
// without an explicit id (each gets a generated unique id) are distinct keys,
// each a singleton version 1 (SRD-031.A FR-3, T-5) — there is no shared identity
// to form a version lineage.
func TestAnonymousProcessesAreDistinctKeys(t *testing.T) {
	th, err := thresher.New("reg-anon")
	require.NoError(t, err)

	reg1, err := th.RegisterProcess(linearProcess(t, "anon-a", 0))
	require.NoError(t, err)

	reg2, err := th.RegisterProcess(linearProcess(t, "anon-b", 0))
	require.NoError(t, err)

	require.NotEqual(t, reg1.Key(), reg2.Key())
	require.Equal(t, 1, reg1.Version())
	require.Equal(t, 1, reg2.Version())
}
