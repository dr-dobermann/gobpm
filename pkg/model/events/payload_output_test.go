package events_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

// TestMessageCatchNilStructureOutput covers FIX-026 §3.2.12: a message item
// with a nil structure registers its payload output in the Undefined state —
// through the error-returning constructors, no panic.
func TestMessageCatchNilStructureOutput(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	msg, err := bpmncommon.NewMessage("bare",
		data.MustItemDefinition(nil))
	require.NoError(t, err)

	med, err := events.NewMessageEventDefinition(msg, nil)
	require.NoError(t, err)

	ice, err := events.NewIntermediateCatchEvent("catch-bare", med)
	require.NoError(t, err)
	require.NotNil(t, ice)
}
