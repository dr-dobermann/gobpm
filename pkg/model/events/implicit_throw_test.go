package events_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// TestImplicitThrowEventBuild: an ImplicitThrowEvent carries its definition; a
// nil definition is rejected, and a base-construction error propagates.
func TestImplicitThrowEventBuild(t *testing.T) {
	sig, err := events.NewSignal("quorum", nil)
	require.NoError(t, err)
	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	ite, err := events.NewImplicitThrowEvent("quorum-reached", def)
	require.NoError(t, err)
	require.Len(t, ite.Definitions(), 1)

	_, err = events.NewImplicitThrowEvent("bad", nil)
	require.Error(t, err)

	// a failing base option (empty id) propagates out of the throw-event base.
	_, err = events.NewImplicitThrowEvent("bad-opt", def, foundation.WithID(""))
	require.Error(t, err)
}
