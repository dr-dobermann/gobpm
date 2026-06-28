package activities_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

// TestActivityAddBoundaryEvent covers the activity-side attachment: a nil
// boundary is rejected, and a real boundary (attached via BoundaryEvent.BoundTo)
// lands in BoundaryEvents().
func TestActivityAddBoundaryEvent(t *testing.T) {
	data.CreateDefaultStates()

	msg := bpmncommon.MustMessage("h",
		data.MustItemDefinition(values.NewVariable(1)))

	rt, err := activities.NewReceiveTask("h", msg)
	require.NoError(t, err)

	// nil boundary event is rejected.
	require.Error(t, rt.AddBoundaryEvent(nil))
	require.Empty(t, rt.BoundaryEvents())

	// a real boundary attaches through BoundTo -> AddBoundaryEvent.
	s, err := events.NewSignal("s", nil)
	require.NoError(t, err)

	sed, err := events.NewSignalEventDefinition(s)
	require.NoError(t, err)

	_, err = events.NewBoundaryEvent("b", rt, sed, true)
	require.NoError(t, err)
	require.Len(t, rt.BoundaryEvents(), 1)
}
