package events

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// valuelessProperty builds a property with no structure — Value() is nil, so it
// can never be filled and is unclonable (FIX-017).
func valuelessProperty(t *testing.T) *data.Property {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	return data.MustProperty("empty",
		data.MustItemDefinition(nil), data.UnavailableDataState)
}

// The three tests below cover the FIX-017 defensive error branch in the Clone of
// the event types whose constructors do not accept property options today (see
// docs/backlog.md). A value-less property is injected directly to exercise the
// guard — the same pattern the snapshot package uses for a guard its constructor
// prevents. Clone fails at property clone, before the node shell is copied, so a
// bare struct suffices.

func TestIntermediateCatchEventCloneRejectsValueLessProperty(t *testing.T) {
	ice := &IntermediateCatchEvent{}
	ice.properties = []*data.Property{valuelessProperty(t)}

	_, err := ice.Clone()
	require.Error(t, err)
}

func TestIntermediateThrowEventCloneRejectsValueLessProperty(t *testing.T) {
	ite := &IntermediateThrowEvent{}
	ite.properties = []*data.Property{valuelessProperty(t)}

	_, err := ite.Clone()
	require.Error(t, err)
}

func TestBoundaryEventCloneRejectsValueLessProperty(t *testing.T) {
	be := &BoundaryEvent{}
	be.properties = []*data.Property{valuelessProperty(t)}

	_, err := be.Clone()
	require.Error(t, err)
}
