package events_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockscope"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestStartEventClone verifies that StartEvent.Clone shares configuration by
// reference, zeroes the dataPath runtime field, starts with empty flows and
// carries no container.
func TestStartEventClone(t *testing.T) {
	data.CreateDefaultStates()

	prop, err := data.NewProperty(
		"prop",
		data.MustItemDefinition(nil),
		data.ReadyDataState)
	require.NoError(t, err)

	se, err := events.NewStartEvent("start", data.WithProperties(prop))
	require.NoError(t, err)

	// give the original an outgoing flow and a runtime dataPath.
	ee, err := events.NewEndEvent("end")
	require.NoError(t, err)
	_, err = flow.Link(se, ee)
	require.NoError(t, err)

	s := mockscope.NewMockScope(t)
	s.EXPECT().LoadData(mock.Anything, mock.Anything).Return(nil).Maybe()
	require.NoError(t, se.RegisterData(scope.DataPath("/root"), s))
	require.Equal(t, scope.DataPath("/root"), se.DataPath())

	clone, ok := se.Clone().(*events.StartEvent)
	require.True(t, ok)

	// independent object, same id.
	require.NotSame(t, se, clone)
	require.Equal(t, se.ID(), clone.ID())

	// configuration shared by reference.
	require.Equal(t, se.Properties(), clone.Properties())
	require.Equal(t, se.IsInterrupting(), clone.IsInterrupting())

	// runtime state fresh.
	require.Equal(t, scope.DataPath(""), clone.DataPath())

	// flows empty, no container.
	require.Empty(t, clone.Outgoing())
	require.Empty(t, clone.Incoming())
	require.Nil(t, clone.Container())
}

// TestEndEventClone verifies that EndEvent.Clone shares configuration by
// reference, zeroes the dataPath runtime field, starts with empty flows and
// carries no container.
func TestEndEventClone(t *testing.T) {
	data.CreateDefaultStates()

	se, err := events.NewStartEvent("start")
	require.NoError(t, err)

	ee, err := events.NewEndEvent("end")
	require.NoError(t, err)
	_, err = flow.Link(se, ee)
	require.NoError(t, err)

	s := mockscope.NewMockScope(t)
	s.EXPECT().LoadData(mock.Anything).Return(nil).Maybe()
	require.NoError(t, ee.RegisterData(scope.DataPath("/root"), s))
	require.Equal(t, scope.DataPath("/root"), ee.DataPath())

	clone, ok := ee.Clone().(*events.EndEvent)
	require.True(t, ok)

	require.NotSame(t, ee, clone)
	require.Equal(t, ee.ID(), clone.ID())

	// runtime state fresh.
	require.Equal(t, scope.DataPath(""), clone.DataPath())

	// flows empty, no container.
	require.Empty(t, clone.Outgoing())
	require.Empty(t, clone.Incoming())
	require.Nil(t, clone.Container())
}
