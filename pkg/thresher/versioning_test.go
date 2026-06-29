package thresher_test

import (
	"context"
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

// TestStartProcessByHandle verifies StartProcess starts the exact version named
// by its registration handle, and rejects a nil handle with a self-identifying
// error (SRD-031.A FR-4, NFR-1, T-6).
func TestStartProcessByHandle(t *testing.T) {
	th, err := thresher.New("start-handle")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	reg, err := th.RegisterProcess(linearProcess(t, "p-byhandle", 0))
	require.NoError(t, err)

	h, err := th.StartProcess(reg)
	require.NoError(t, err)
	require.NotNil(t, h)

	_, err = th.StartProcess(nil)
	require.Error(t, err)
}

// TestStartVersion verifies StartVersion starts a specific registered version of
// a key and rejects unknown keys/versions and out-of-range versions (SRD-031.A
// FR-5, NFR-1, T-7).
func TestStartVersion(t *testing.T) {
	th, err := thresher.New("start-version")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	proc := linearProcess(t, "p-ver-start", 0)
	reg1, err := th.RegisterProcess(proc)
	require.NoError(t, err)
	reg2, err := th.RegisterProcess(proc)
	require.NoError(t, err)
	key := reg1.Key()

	// each registered version starts.
	h1, err := th.StartVersion(key, reg1.Version())
	require.NoError(t, err)
	require.NotNil(t, h1)

	h2, err := th.StartVersion(key, reg2.Version())
	require.NoError(t, err)
	require.NotNil(t, h2)

	// an unregistered version, an unknown key, a sub-1 version, and an empty key
	// are each rejected.
	_, err = th.StartVersion(key, 3)
	require.Error(t, err)
	_, err = th.StartVersion("no-such-key", 1)
	require.Error(t, err)
	_, err = th.StartVersion(key, 0)
	require.Error(t, err)
	_, err = th.StartVersion("", 1)
	require.Error(t, err)
}

// TestStartLatest verifies StartLatest starts the newest registered version of a
// key and rejects unknown/empty keys (SRD-031.A FR-6, NFR-1, T-8).
func TestStartLatest(t *testing.T) {
	th, err := thresher.New("start-latest")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	reg, err := th.RegisterProcess(linearProcess(t, "p-latest", 0))
	require.NoError(t, err)

	h, err := th.StartLatest(reg.Key())
	require.NoError(t, err)
	require.NotNil(t, h)

	_, err = th.StartLatest("no-such-key")
	require.Error(t, err)
	_, err = th.StartLatest("")
	require.Error(t, err)
}

// TestStartMethodsRejectWhenNotStarted verifies every Start* entry point refuses
// to launch before the engine is Run, even with a valid registration in hand
// (SRD-031.A NFR-1). RegisterProcess itself works before Run.
func TestStartMethodsRejectWhenNotStarted(t *testing.T) {
	th, err := thresher.New("not-started")
	require.NoError(t, err)

	reg, err := th.RegisterProcess(linearProcess(t, "p-ns", 0))
	require.NoError(t, err)

	_, err = th.StartProcess(reg)
	require.Error(t, err)

	_, err = th.StartVersion(reg.Key(), 1)
	require.Error(t, err)

	_, err = th.StartLatest(reg.Key())
	require.Error(t, err)
}
