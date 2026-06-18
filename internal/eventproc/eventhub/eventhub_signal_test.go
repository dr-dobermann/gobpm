package eventhub_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func signalDef(t *testing.T, name string) *events.SignalEventDefinition {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)
	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	return def
}

// TestBroadcastSignalFanOut verifies a thrown signal is matched to catchers by
// NAME, not eDef.ID(): a throw with a DIFFERENT signal definition of the same
// name reaches the registered catcher (ADR-006 §2.1, SRD-020).
func TestBroadcastSignalFanOut(t *testing.T) {
	hub, err := eventhub.New(enginert.Default())
	require.NoError(t, err)
	require.NoError(t, hub.Start(context.Background()))

	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().ID().Return("catcher").Maybe()
	ep.EXPECT().ProcessEvent(mock.Anything, mock.Anything).Return(nil).Once()

	// Register a catcher of signal "GO".
	require.NoError(t, hub.RegisterEvent(ep, signalDef(t, "GO")))

	// Throw a DIFFERENT signal definition of the same name — it must still reach
	// the catcher (matched by name, not by the throw's eDef.ID()).
	require.NoError(t, hub.PropagateEvent(context.Background(), signalDef(t, "GO")))
}
