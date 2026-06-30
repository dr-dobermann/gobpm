package thresher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

// runErrHub is an EventHub whose Start succeeds and whose Run returns a
// non-context error; every other method is the embedded nil interface (none is
// reached — Run with no registered processes only calls Start then Run).
type runErrHub struct {
	eventproc.EventHub

	runErr error
}

func (runErrHub) Start(context.Context) error { return nil }

func (h runErrHub) Run(context.Context) error { return h.runErr }

// captureLogger records Error() messages on a channel; Debug/Info/Warn no-op.
type captureLogger struct {
	errs chan string
}

func (captureLogger) Debug(string, ...any) {}
func (captureLogger) Info(string, ...any)  {}
func (captureLogger) Warn(string, ...any)  {}

func (c captureLogger) Error(msg string, _ ...any) {
	select {
	case c.errs <- msg:
	default:
	}
}

// TestEventHubRunErrorLogged covers FIX-013 §1.5: a non-context EventHub.Run
// error is surfaced to the logger instead of being discarded.
func TestEventHubRunErrorLogged(t *testing.T) {
	cl := captureLogger{errs: make(chan string, 1)}

	th, err := New("lc-runerr", WithLogger(cl))
	require.NoError(t, err)

	// swap in a hub whose Run loop fails with a non-context error.
	th.eventHub = runErrHub{runErr: errors.New("hub boom")}

	require.NoError(t, th.Run(context.Background()))

	select {
	case msg := <-cl.errs:
		require.Equal(t, "event hub run loop failed", msg)
	case <-time.After(2 * time.Second):
		t.Fatal("EventHub.Run error was not logged")
	}
}

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

// regFailHub starts cleanly and runs until its context is canceled, but fails
// every persistent-event registration — it drives the registerAllStarters
// failure path in Run while leaving Start (so the engine reaches Started) and
// Run (so the hub goroutine is live and must be torn down) intact.
type regFailHub struct {
	eventproc.EventHub

	regErr error
}

func (regFailHub) Start(context.Context) error { return nil }

func (regFailHub) Run(ctx context.Context) error {
	<-ctx.Done()

	return ctx.Err()
}

func (h regFailHub) RegisterPersistentEvent(
	eventproc.EventProcessor, flow.EventDefinition,
) error {
	return h.regErr
}

// TestRunRollsBackWhenStarterRegistrationFails covers FIX-013 §1.2 (audit
// third-pass §2.7): when registerAllStarters fails after Started is published,
// Run must roll the lifecycle back to NotStarted and stop the hub goroutine,
// instead of stranding a half-started engine that rejects a retry and leaves
// Shutdown to tear down a half-wired engine.
func TestRunRollsBackWhenStarterRegistrationFails(t *testing.T) {
	th, err := New("lc-starter-rollback")
	require.NoError(t, err)

	// Seed one registered process whose single starter will fail to subscribe,
	// so registerAllStarters returns an error after Started is published.
	th.registrations["k"] = []*ProcessRegistration{
		{
			key:      "k",
			id:       "r1",
			version:  1,
			starters: []*instanceStarter{mkStarter(t, "x")},
		},
	}

	th.eventHub = regFailHub{regErr: errors.New("subscribe boom")}

	err = th.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't register instance-starters")
	require.Equal(t, NotStarted, th.State())

	// Re-runnable: the rollback left the CAS claim free, so a second Run is not
	// rejected by the state machine — it reaches the hub and fails the same way.
	err = th.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't register instance-starters")
	require.Equal(t, NotStarted, th.State())
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
