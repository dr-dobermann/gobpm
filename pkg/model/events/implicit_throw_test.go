package events_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

// TestImplicitThrowEventBuild: an ImplicitThrowEvent carries its definition; a
// nil definition is rejected.
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
}
