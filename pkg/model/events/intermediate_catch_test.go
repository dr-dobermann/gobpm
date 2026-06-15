package events_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func catchMessageDef(t *testing.T) *events.MessageEventDefinition {
	t.Helper()

	return events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_item"))),
		nil)
}

func TestNewIntermediateCatchEvent(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("happy path with a message trigger",
		func(t *testing.T) {
			med := catchMessageDef(t)

			ice, err := events.NewIntermediateCatchEvent("await order", med)
			require.NoError(t, err)
			require.Equal(t, flow.IntermediateEventClass, ice.EventClass())
			require.Equal(t, ice, ice.Node())

			defs := ice.Definitions()
			require.Len(t, defs, 1)
			require.Equal(t, flow.TriggerMessage, defs[0].Type())

			require.NoError(t, ice.AcceptIncomingFlow(nil))
			require.NoError(t, ice.SupportOutgoingFlow(nil))
		})

	t.Run("a nil definition is rejected",
		func(t *testing.T) {
			_, err := events.NewIntermediateCatchEvent("await", nil)
			require.Error(t, err)
		})

	t.Run("a disallowed trigger is rejected",
		func(t *testing.T) {
			cancel, err := events.NewCancelEventDefinition()
			require.NoError(t, err)

			_, err = events.NewIntermediateCatchEvent("await", cancel)
			require.Error(t, err)
		})

	t.Run("a message with a structureless item is accepted",
		func(t *testing.T) {
			med := events.MustMessageEventDefinition(
				bpmncommon.MustMessage("ping",
					data.MustItemDefinition(nil,
						foundation.WithID("ping_item"))),
				nil)

			_, err := events.NewIntermediateCatchEvent("await", med)
			require.NoError(t, err)
		})
}

func TestIntermediateCatchEventExec(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ice, err := events.NewIntermediateCatchEvent("await", catchMessageDef(t))
	require.NoError(t, err)

	// the payload was bound by UploadData on resume; Exec just emits the
	// (here empty) outgoing flows.
	flows, err := ice.Exec(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, flows)
}

func TestIntermediateCatchEventClone(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ice, err := events.NewIntermediateCatchEvent("await", catchMessageDef(t))
	require.NoError(t, err)

	cl, ok := ice.Clone().(*events.IntermediateCatchEvent)
	require.True(t, ok)
	require.Equal(t, flow.IntermediateEventClass, cl.EventClass())
	require.Len(t, cl.Definitions(), 1)
}
