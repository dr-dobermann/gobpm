package events_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

// TestErrorEventDefinitionNilStructure pins FIX-010 §3.2.2 at the consumer: an
// Error event whose Error carries no ItemDefinition routes through the
// readiness/clone guards without panicking — GetItemsList is empty and
// CheckItemDefinition is false.
func TestErrorEventDefinitionNilStructure(t *testing.T) {
	e, err := bpmncommon.NewError("boom", "E1", nil)
	require.NoError(t, err)

	eed, err := events.NewErrorEventDefinition(e)
	require.NoError(t, err)

	require.NotPanics(t, func() {
		require.Empty(t, eed.GetItemsList())
		require.False(t, eed.CheckItemDefinition("anything"))
	})
}
