package waiters_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

// The two guards below reject a definition of the right TYPE whose payload is
// missing. Neither constructor can produce one — NewMessageEventDefinition and
// NewSignalEventDefinition both reject a nil payload — so the branch exists for
// a value that bypassed them, which a zero-valued struct literal does exactly.
// They were uncovered until FIX-035's sweep made their diagnostic details part
// of a change, and they are worth covering rather than excluding: a waiter built
// around a nil payload would fail later, far from the registration that caused it.

// TestMessageWaiterRejectsDefinitionWithoutMessage covers message.go's nil-Message
// guard (FIX-035 §4.1).
func TestMessageWaiterRejectsDefinitionWithoutMessage(t *testing.T) {
	hub := mockeventproc.NewMockEventHub(t)
	ep := mockeventproc.NewMockEventProcessor(t)

	w, err := waiters.NewMessageWaiter(hub, ep,
		&events.MessageEventDefinition{}, "w-msg", enginert.Default())

	require.Error(t, err)
	require.Nil(t, w)
	require.ErrorContains(t, err, "has no message")
}

// TestSignalWaiterRejectsDefinitionWithoutSignal is its signal-side twin.
func TestSignalWaiterRejectsDefinitionWithoutSignal(t *testing.T) {
	hub := mockeventproc.NewMockEventHub(t)
	ep := mockeventproc.NewMockEventProcessor(t)

	w, err := waiters.NewSignalWaiter(hub, ep,
		&events.SignalEventDefinition{}, "w-sig", enginert.Default())

	require.Error(t, err)
	require.Nil(t, w)
	require.ErrorContains(t, err, "has no signal")
}
