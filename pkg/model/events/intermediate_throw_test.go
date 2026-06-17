package events_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func throwMessageDef(t *testing.T) *events.MessageEventDefinition {
	t.Helper()

	return events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_out"))),
		nil)
}

func throwReadyParam(id string, value any) data.Data {
	return data.MustParameter(id,
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(value),
				foundation.WithID(id)),
			data.ReadyDataState))
}

func TestNewIntermediateThrowEvent(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("happy path with a message trigger",
		func(t *testing.T) {
			med := throwMessageDef(t)

			ite, err := events.NewIntermediateThrowEvent("send order", med)
			require.NoError(t, err)
			require.Equal(t, flow.IntermediateEventClass, ite.EventClass())
			require.Equal(t, ite, ite.Node())
			require.Equal(t, med.Message(), ite.MessageToSend())
			require.NoError(t, ite.AcceptIncomingFlow(nil))
			require.NoError(t, ite.SupportOutgoingFlow(nil))
		})

	t.Run("a nil definition is rejected",
		func(t *testing.T) {
			_, err := events.NewIntermediateThrowEvent("send", nil)
			require.Error(t, err)
		})

	t.Run("a disallowed trigger is rejected",
		func(t *testing.T) {
			term, err := events.NewTerminateEventDefinition()
			require.NoError(t, err)

			_, err = events.NewIntermediateThrowEvent("send", term)
			require.Error(t, err)
		})

	t.Run("MessageToSend is nil for a non-message throw",
		func(t *testing.T) {
			sig, err := events.NewSignal("sig", data.MustItemDefinition(nil))
			require.NoError(t, err)
			sigEd, err := events.NewSignalEventDefinition(sig)
			require.NoError(t, err)

			ite, err := events.NewIntermediateThrowEvent("signal", sigEd)
			require.NoError(t, err)
			require.Nil(t, ite.MessageToSend())
		})
}

func TestIntermediateThrowEventClone(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ite, err := events.NewIntermediateThrowEvent("send", throwMessageDef(t))
	require.NoError(t, err)

	cl, ok := ite.Clone().(*events.IntermediateThrowEvent)
	require.True(t, ok)
	require.Equal(t, flow.IntermediateEventClass, cl.EventClass())
}

func TestIntermediateThrowEventExec(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("a message throw publishes to the broker",
		func(t *testing.T) {
			ite, err := events.NewIntermediateThrowEvent("send",
				throwMessageDef(t))
			require.NoError(t, err)

			broker := membroker.New()
			sub, err := broker.Subscribe(ctx, "order placed")
			require.NoError(t, err)
			ch := sub.C()

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				GetDataByID("order_out").
				Return(throwReadyParam("order_out", "ORD-9"), nil)
			re.EXPECT().MessageBroker().Return(broker)

			flows, err := ite.Exec(ctx, re)
			require.NoError(t, err)
			require.Empty(t, flows)

			select {
			case env := <-ch:
				require.Equal(t, "order placed", env.Name)
				require.Equal(t, "ORD-9", env.Payload)
			default:
				t.Fatal("no envelope published to the broker")
			}
		})

	t.Run("a non-message throw propagates through the event bus",
		func(t *testing.T) {
			sig, err := events.NewSignal("sig", data.MustItemDefinition(nil))
			require.NoError(t, err)
			sigEd, err := events.NewSignalEventDefinition(sig)
			require.NoError(t, err)

			ite, err := events.NewIntermediateThrowEvent("signal", sigEd)
			require.NoError(t, err)

			propagated := false
			mep := mockeventproc.NewMockEventProducer(t)
			mep.EXPECT().
				PropagateEvent(mock.Anything, mock.Anything).
				RunAndReturn(func(context.Context, flow.EventDefinition) error {
					propagated = true

					return nil
				})

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().EventProducer().Return(mep)

			_, err = ite.Exec(ctx, re)
			require.NoError(t, err)
			require.True(t, propagated)
		})

	t.Run("a failed publish is reported",
		func(t *testing.T) {
			ite, err := events.NewIntermediateThrowEvent("send",
				throwMessageDef(t))
			require.NoError(t, err)

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				GetDataByID("order_out").
				Return(nil, fmt.Errorf("not in scope"))

			_, err = ite.Exec(ctx, re)
			require.Error(t, err)
		})
}
