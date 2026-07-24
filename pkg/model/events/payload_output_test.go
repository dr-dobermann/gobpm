package events_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
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

// TestMessageBoundaryPayloadOutput covers the boundary constructor's payload
// output registration through the FIX-026 error-returning path.
func TestMessageBoundaryPayloadOutput(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	msg, err := bpmncommon.NewMessage("late order",
		data.MustItemDefinition(values.NewVariable("")))
	require.NoError(t, err)

	med, err := events.NewMessageEventDefinition(msg, nil)
	require.NoError(t, err)

	host, err := activities.NewManualTask("host")
	require.NoError(t, err)

	be, err := events.NewBoundaryEvent("msg-bnd", host, med, true)
	require.NoError(t, err)
	require.NotNil(t, be)
}
