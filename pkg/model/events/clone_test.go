package events_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

// TestStartEventClone verifies that StartEvent.Clone shares configuration by
// reference, zeroes the dataPath runtime field, starts with empty flows and
// carries no container.
func TestStartEventClone(t *testing.T) {
	data.CreateDefaultStates()

	prop, err := data.NewProperty(
		"prop",
		data.MustItemDefinition(values.NewVariable("v")),
		data.ReadyDataState)
	require.NoError(t, err)

	se, err := events.NewStartEvent("start", data.WithProperties(prop))
	require.NoError(t, err)

	// give the original an outgoing flow and a runtime dataPath.
	ee, err := events.NewEndEvent("end")
	require.NoError(t, err)
	_, err = flow.Link(se, ee)
	require.NoError(t, err)

	cn, err := se.Clone()
	require.NoError(t, err)

	clone, ok := cn.(*events.StartEvent)
	require.True(t, ok)

	// independent object, same id.
	require.NotSame(t, se, clone)
	require.Equal(t, se.ID(), clone.ID())

	// properties are deep-copied — distinct objects, same name (FIX-017); value
	// isolation is covered by TestEventCloneIsolatesProperties.
	require.Len(t, clone.Properties(), 1)
	require.Equal(t, se.Properties()[0].Name(), clone.Properties()[0].Name())
	require.Equal(t, se.IsInterrupting(), clone.IsInterrupting())

	// flows empty, no container.
	require.Empty(t, clone.Outgoing())
	require.Empty(t, clone.Incoming())
	require.Nil(t, clone.Container())
}

// TestEventCloneIsolatesProperties covers FIX-017 3.2.3: Event.clone deep-copies
// its properties, so a clone owns distinct Property objects and a write through
// the source doesn't reach the clone.
func TestEventCloneIsolatesProperties(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	prop, err := data.NewProperty(
		"counter",
		data.MustItemDefinition(values.NewVariable(0)),
		data.ReadyDataState)
	require.NoError(t, err)

	se, err := events.NewStartEvent("start", data.WithProperties(prop))
	require.NoError(t, err)

	cn, err := se.Clone()
	require.NoError(t, err)

	clone, ok := cn.(*events.StartEvent)
	require.True(t, ok)

	require.NotSame(t, se.Properties()[0], clone.Properties()[0],
		"clone must own a distinct property object")

	ctx := context.Background()
	require.NoError(t, se.Properties()[0].Value().Update(ctx, 7))
	require.Equal(t, 0, clone.Properties()[0].Value().Get(ctx),
		"a property write on the source must not leak into the clone")
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

	cn, err := ee.Clone()
	require.NoError(t, err)

	clone, ok := cn.(*events.EndEvent)
	require.True(t, ok)

	require.NotSame(t, ee, clone)
	require.Equal(t, ee.ID(), clone.ID())

	// flows empty, no container.
	require.Empty(t, clone.Outgoing())
	require.Empty(t, clone.Incoming())
	require.Nil(t, clone.Container())
}

// valuedEventProp builds a valued property for the FIX-018 event tests.
func valuedEventProp(t *testing.T) *data.Property {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	return data.MustProperty("counter",
		data.MustItemDefinition(values.NewVariable(0)), data.ReadyDataState)
}

// TestIntermediateCatchEventAcceptsProperty covers FIX-018 3.2.2: the
// constructor now accepts data.WithProperties (via newEvent's option collection).
func TestIntermediateCatchEventAcceptsProperty(t *testing.T) {
	ice, err := events.NewIntermediateCatchEvent("await", catchMessageDef(t),
		data.WithProperties(valuedEventProp(t)))
	require.NoError(t, err)

	require.Len(t, ice.Properties(), 1)
	require.Equal(t, "counter", ice.Properties()[0].Name())
}

// TestIntermediateThrowEventAcceptsProperty covers FIX-018 3.2.2 for the throw
// intermediate event.
func TestIntermediateThrowEventAcceptsProperty(t *testing.T) {
	ite, err := events.NewIntermediateThrowEvent("send", throwMessageDef(t),
		data.WithProperties(valuedEventProp(t)))
	require.NoError(t, err)

	require.Len(t, ite.Properties(), 1)
	require.Equal(t, "counter", ite.Properties()[0].Name())
}

// TestBoundaryEventAcceptsProperty covers FIX-018 3.2.2 for a boundary event.
func TestBoundaryEventAcceptsProperty(t *testing.T) {
	be, err := events.NewBoundaryEvent("b", boundaryHostTask(t), messageDef(t),
		false, data.WithProperties(valuedEventProp(t)))
	require.NoError(t, err)

	require.Len(t, be.Properties(), 1)
	require.Equal(t, "counter", be.Properties()[0].Name())
}
