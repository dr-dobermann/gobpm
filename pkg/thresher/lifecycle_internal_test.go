package thresher

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/stretchr/testify/require"
)

// failStartHub is an EventHub whose Start fails; every other method is the
// embedded nil interface (none is reached, because Run rolls back at Start).
type failStartHub struct {
	eventproc.EventHub

	err error
}

func (h failStartHub) Start(context.Context) error {
	return h.err
}

// TestStateLockFreeUnderHeldMutex verifies State() does not acquire t.m, so it
// is callable while t.m is held without deadlocking — the property that removes
// the FIX-002 RC2 re-entrant self-deadlock vector (SRD-031.B FR-3, T-2). If
// State still locked, this test would hang on the held mutex and fail by
// timeout.
func TestStateLockFreeUnderHeldMutex(t *testing.T) {
	th, err := New("lc-lockfree")
	require.NoError(t, err)

	th.state.Store(uint32(Started))

	th.m.Lock()
	got := th.State()
	th.m.Unlock()

	require.Equal(t, Started, got)
}

// TestShutdownWhileStartingRejected verifies Shutdown rejects when the engine is
// mid-start (Starting): it cannot tear down a transition that has not completed,
// and the state is left untouched (SRD-031.B FR-6, T-9).
func TestShutdownWhileStartingRejected(t *testing.T) {
	th, err := New("lc-starting")
	require.NoError(t, err)

	th.state.Store(uint32(Starting))

	err = th.Shutdown(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "starting")
	require.Equal(t, Starting, th.State())
}

// TestShutdownFromInvalidRejected verifies the defensive default branch: an
// Invalid state (unreachable in the normal lifecycle) is rejected rather than
// silently torn down (SRD-031.B FR-6, T-9 sibling).
func TestShutdownFromInvalidRejected(t *testing.T) {
	th, err := New("lc-invalid")
	require.NoError(t, err)

	th.state.Store(uint32(Invalid))

	err = th.Shutdown(context.Background())
	require.Error(t, err)
	require.Equal(t, Invalid, th.State())
}

// TestRunRollsBackOnHubStartFailure verifies that when hub.Start fails, Run
// rolls the claimed Starting transition back to NotStarted (so the engine is not
// stranded mid-start) and stays re-runnable: a second Run passes the
// NotStarted->Starting claim again and reaches the hub, failing with the hub
// error rather than an "already started" rejection (SRD-031.B FR-6, T-10).
func TestRunRollsBackOnHubStartFailure(t *testing.T) {
	th, err := New("lc-rollback")
	require.NoError(t, err)

	th.eventHub = failStartHub{err: errors.New("hub boom")}

	err = th.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't start eventHub")
	require.Equal(t, NotStarted, th.State())

	// Re-runnable: not locked out by the state machine.
	err = th.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't start eventHub")
	require.Equal(t, NotStarted, th.State())
}
