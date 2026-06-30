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
