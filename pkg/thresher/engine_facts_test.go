package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// kinds returns the set of fact kinds the collector saw.
func (c *collector) kinds() map[observability.Kind]bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := map[observability.Kind]bool{}
	for _, e := range c.events {
		out[e.Kind] = true
	}

	return out
}

// sawKindPhase reports whether a (kind, phase) fact was seen.
func (c *collector) sawKindPhase(k observability.Kind, p observability.Phase) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, e := range c.events {
		if e.Kind == k && e.Phase == p {
			return true
		}
	}

	return false
}

// TestEngineScopeSeesLifecycleFacts (SRD-041 M4): an engine-scope observer sees
// the engine, hub, and process-registration facts — EngineState Started through
// Stopped, HubState Started/Stopped, ProcessLifecycle Registered.
func TestEngineScopeSeesLifecycleFacts(t *testing.T) {
	proc := linearProcess(t, "eng-facts", 0)

	th, err := thresher.New("eng-facts-engine")
	require.NoError(t, err)

	c := &collector{}
	sub := th.Observe(c) // engine-scope, before Run — sees the whole lifecycle

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer wcancel()
	_, err = h.WaitCompletion(wctx)
	require.NoError(t, err)

	require.NoError(t, th.Shutdown(context.Background()))

	sub.Cancel() // drains the buffered facts so the asserts see them

	require.True(t,
		c.sawKindPhase(observability.KindEngineState, observability.PhaseStarted),
		"engine Started")
	require.True(t,
		c.sawKindPhase(observability.KindEngineState, observability.PhaseStopped),
		"engine Stopped")
	require.True(t,
		c.sawKindPhase(observability.KindHubState, observability.PhaseStarted),
		"hub Started")
	require.True(t,
		c.sawKindPhase(observability.KindProcessLifecycle, observability.PhaseRegistered),
		"process Registered")
}

// TestEngineStatePausedAndSupersede (SRD-041 M4): UpdateState(Paused) emits an
// EngineState/Paused fact; registering a second version of a key emits
// ProcessLifecycle/VersionSuperseded; UnregisterProcess emits Unregistered.
func TestEngineStatePausedAndSupersede(t *testing.T) {
	proc := linearProcess(t, "sup", 0)

	th, err := thresher.New("sup-engine")
	require.NoError(t, err)

	c := &collector{}
	sub := th.Observe(c)

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)
	_, err = th.RegisterProcess(proc) // a second version supersedes the first
	require.NoError(t, err)
	require.NoError(t, th.UnregisterProcess(proc.ID()))

	// Paused is set via UpdateState (no code drives it yet — the reserved-but-
	// reachable path); done last so it doesn't perturb the registry above.
	// The engine must be RUNNING first: pausing is an operator transition out
	// of Started, and UpdateState no longer lets a host jump the lifecycle
	// ladder to reach it (FIX-036 §1.6). The no-phase early return that the
	// removed UpdateState(NotStarted) line used to exercise is no longer
	// reachable through the public API and is pinned directly instead, by
	// TestUnmappedEngineStateReportsNothing.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))
	require.NoError(t, th.UpdateState(thresher.Paused))

	sub.Cancel() // drains the buffered facts so the asserts see them

	require.True(t,
		c.sawKindPhase(observability.KindEngineState, observability.PhasePaused),
		"engine Paused")
	require.True(t,
		c.sawKindPhase(observability.KindProcessLifecycle,
			observability.PhaseVersionSuperseded), "version superseded")
	require.True(t,
		c.sawKindPhase(observability.KindProcessLifecycle,
			observability.PhaseUnregistered), "process unregistered")
}
