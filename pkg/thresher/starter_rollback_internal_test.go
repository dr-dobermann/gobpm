package thresher

import (
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mkStarter builds a minimal instance-starter with a signal event-definition
// carrying a distinct id — enough for the (un)register loops, which only touch
// st.eDef and st.snapshot.ProcessID.
func mkStarter(t *testing.T, id string) *instanceStarter {
	t.Helper()

	sig, err := events.NewSignal("sig-"+id, nil)
	require.NoError(t, err)

	sed, err := events.NewSignalEventDefinition(sig, foundation.WithID("ed-"+id))
	require.NoError(t, err)

	return &instanceStarter{
		snapshot: &snapshot.Snapshot{ProcessID: "p-" + id},
		eDef:     sed,
		id:       "starter-" + id,
	}
}

// TestRegisterStartersRollsBackOnPartialFailure covers FIX-013 §1.3: when a
// starter subscription fails mid-loop, the ones already subscribed in this call
// are unsubscribed, so no partial subscription set persists. The mock asserts
// exactly two RegisterPersistentEvent calls (1st ok, 2nd fails) and one
// UnregisterEvent (the rollback of the first); the 3rd starter is never reached.
func TestRegisterStartersRollsBackOnPartialFailure(t *testing.T) {
	th, err := New("starter-reg-rollback")
	require.NoError(t, err)

	starters := []*instanceStarter{
		mkStarter(t, "0"), mkStarter(t, "1"), mkStarter(t, "2"),
	}

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().RegisterPersistentEvent(mock.Anything, mock.Anything).
		Return(nil).Once()
	hub.EXPECT().RegisterPersistentEvent(mock.Anything, mock.Anything).
		Return(errors.New("register boom")).Once()
	hub.EXPECT().UnregisterEvent(mock.Anything, mock.Anything).
		Return(nil).Once()

	th.eventHub = hub

	require.Error(t, th.registerStarters(starters))
}

// TestUnregisterStartersRollsBackOnPartialFailure is the symmetric case: a
// failed teardown re-subscribes the ones already torn down in this call. The
// mock asserts two UnregisterEvent calls (1st ok, 2nd fails) and one
// RegisterPersistentEvent (the rollback re-subscribe of the first).
func TestUnregisterStartersRollsBackOnPartialFailure(t *testing.T) {
	th, err := New("starter-unreg-rollback")
	require.NoError(t, err)

	starters := []*instanceStarter{
		mkStarter(t, "0"), mkStarter(t, "1"), mkStarter(t, "2"),
	}

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().UnregisterEvent(mock.Anything, mock.Anything).
		Return(nil).Once()
	hub.EXPECT().UnregisterEvent(mock.Anything, mock.Anything).
		Return(errors.New("unregister boom")).Once()
	hub.EXPECT().RegisterPersistentEvent(mock.Anything, mock.Anything).
		Return(nil).Once()

	th.eventHub = hub

	require.Error(t, th.unregisterStarters(starters))
}

// TestRegisterStartersJoinsRollbackFailure (FIX-022 §4.1.4, A7): when the
// register fails AND a rollback unsubscribe ALSO fails, the returned error
// carries BOTH — the rollback failure is joined into the cause, no longer
// swallowed by a bare _ = discard (ADR-022 v.1 §2.2).
func TestRegisterStartersJoinsRollbackFailure(t *testing.T) {
	th, err := New("starter-reg-rollback-join")
	require.NoError(t, err)

	starters := []*instanceStarter{mkStarter(t, "0"), mkStarter(t, "1")}

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().RegisterPersistentEvent(mock.Anything, mock.Anything).
		Return(nil).Once()
	hub.EXPECT().RegisterPersistentEvent(mock.Anything, mock.Anything).
		Return(errors.New("register boom")).Once()
	hub.EXPECT().UnregisterEvent(mock.Anything, mock.Anything).
		Return(errors.New("rollback unreg boom")).Once()

	th.eventHub = hub

	err = th.registerStarters(starters)
	require.Error(t, err)
	require.ErrorContains(t, err, "register boom", "the triggering error surfaces")
	require.ErrorContains(t, err, "rollback unreg boom",
		"the rollback failure is joined, not swallowed")
}

// TestUnregisterStartersJoinsRollbackFailure (FIX-022 §4.1.4, A8): the symmetric
// case — a failed teardown whose rollback re-subscribe ALSO fails surfaces both
// errors joined into the cause.
func TestUnregisterStartersJoinsRollbackFailure(t *testing.T) {
	th, err := New("starter-unreg-rollback-join")
	require.NoError(t, err)

	starters := []*instanceStarter{mkStarter(t, "0"), mkStarter(t, "1")}

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().UnregisterEvent(mock.Anything, mock.Anything).
		Return(nil).Once()
	hub.EXPECT().UnregisterEvent(mock.Anything, mock.Anything).
		Return(errors.New("unregister boom")).Once()
	hub.EXPECT().RegisterPersistentEvent(mock.Anything, mock.Anything).
		Return(errors.New("rollback reg boom")).Once()

	th.eventHub = hub

	err = th.unregisterStarters(starters)
	require.Error(t, err)
	require.ErrorContains(t, err, "unregister boom")
	require.ErrorContains(t, err, "rollback reg boom",
		"the rollback failure is joined, not swallowed")
}
