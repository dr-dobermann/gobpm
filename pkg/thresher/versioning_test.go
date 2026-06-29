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

// regVersions extracts the version numbers from a registration list.
func regVersions(regs []*thresher.ProcessRegistration) []int {
	out := make([]int, len(regs))
	for i, r := range regs {
		out[i] = r.Version()
	}

	return out
}

// TestGappedVersionRemoval verifies that removing a non-latest version leaves a
// gap addressed correctly: StartVersion finds a version by NUMBER (not slice
// position), version numbers are never reused while the key lives, StartLatest
// stays the highest present version, Registrations enumerates the gap, and a
// fully-removed key resets to v1 (SRD-031.A FR-3, FR-5, FR-8, FR-10, T-13).
func TestGappedVersionRemoval(t *testing.T) {
	th, err := thresher.New("gap")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	proc := linearProcess(t, "p-gap", 0)
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)
	reg2, err := th.RegisterProcess(proc)
	require.NoError(t, err)
	reg3, err := th.RegisterProcess(proc)
	require.NoError(t, err)
	key := reg2.Key()
	require.Equal(t, []int{1, 2, 3}, regVersions(th.Registrations(key)))

	// remove the MIDDLE version (v2) while v3 is latest.
	require.NoError(t, th.UnregisterVersion(reg2))
	require.Equal(t, []int{1, 3}, regVersions(th.Registrations(key)))

	// StartVersion addresses by number: v3 still starts, the removed v2 errors,
	// v1 still starts.
	h3, err := th.StartVersion(key, 3)
	require.NoError(t, err)
	require.NotNil(t, h3)
	_, err = th.StartVersion(key, 2)
	require.Error(t, err)
	h1, err := th.StartVersion(key, 1)
	require.NoError(t, err)
	require.NotNil(t, h1)

	// StartLatest resolves to the highest present version (v3, not the removed v2).
	require.Equal(t, 3, reg3.Version())
	hL, err := th.StartLatest(key)
	require.NoError(t, err)
	require.NotNil(t, hL)

	// re-registering reuses neither the removed 2 nor the live 3: the monotonic
	// counter yields 4.
	reg4, err := th.RegisterProcess(proc)
	require.NoError(t, err)
	require.Equal(t, 4, reg4.Version())
	require.Equal(t, []int{1, 3, 4}, regVersions(th.Registrations(key)))

	// an unknown key enumerates as empty.
	require.Empty(t, th.Registrations("no-such-key"))

	// the returned handles are live: each is accepted by UnregisterVersion.
	// Removing every version one by one resets the key — a later registration is
	// v1 again (exercises UnregisterVersion's drop-the-counter-on-empty path).
	for _, r := range th.Registrations(key) {
		require.NoError(t, th.UnregisterVersion(r))
	}
	require.Empty(t, th.Registrations(key))

	regReset, err := th.RegisterProcess(proc)
	require.NoError(t, err)
	require.Equal(t, 1, regReset.Version())
}

// TestUnregisterProcessRemovesAllVersions verifies the whole-process teardown:
// UnregisterProcess(key) drops every registered version at once, errors on an
// unknown/empty key, and resets the version counter so a later registration is
// v1 again (SRD-031.A FR-11, T-14).
func TestUnregisterProcessRemovesAllVersions(t *testing.T) {
	th, err := thresher.New("unreg-all")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	proc := linearProcess(t, "p-unreg-all", 0)
	for range 3 {
		_, err = th.RegisterProcess(proc)
		require.NoError(t, err)
	}
	key := proc.ID()
	require.Equal(t, []int{1, 2, 3}, regVersions(th.Registrations(key)))

	// an empty key and an unknown key are rejected.
	require.Error(t, th.UnregisterProcess(""))
	require.Error(t, th.UnregisterProcess("no-such-key"))

	// the whole process goes in one call.
	require.NoError(t, th.UnregisterProcess(key))
	require.Empty(t, th.Registrations(key))
	_, err = th.StartLatest(key)
	require.Error(t, err)

	// the counter reset: a later registration of the same key restarts at v1.
	regNew, err := th.RegisterProcess(proc)
	require.NoError(t, err)
	require.Equal(t, 1, regNew.Version())

	// before Run (engine not started) UnregisterProcess still drops the key —
	// nothing is on the hub to tear down.
	th2, err := thresher.New("unreg-all-prerun")
	require.NoError(t, err)
	pre := linearProcess(t, "p-prerun", 0)
	_, err = th2.RegisterProcess(pre)
	require.NoError(t, err)
	require.NoError(t, th2.UnregisterProcess(pre.ID()))
	require.Empty(t, th2.Registrations(pre.ID()))
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
