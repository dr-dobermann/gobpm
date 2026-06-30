package eventhub_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub"
	"github.com/stretchr/testify/require"
)

// TestEventHubRejectsNilEventDefinition pins FIX-010 §3.2.1: a started hub must
// reject a nil EventDefinition with a classified error from each of the three
// public entry points, instead of panicking on a nil dereference.
func TestEventHubRejectsNilEventDefinition(t *testing.T) {
	hub, err := eventhub.New(enginert.Default())
	require.NoError(t, err)

	ctx := t.Context()
	require.NoError(t, hub.Start(ctx))

	ep := mockeventproc.NewMockEventProcessor(t)

	require.Error(t, hub.RegisterEvent(ep, nil))
	require.Error(t, hub.RegisterPersistentEvent(ep, nil))
	require.Error(t, hub.PropagateEvent(ctx, nil))
}
