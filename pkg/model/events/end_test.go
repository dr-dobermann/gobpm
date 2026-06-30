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
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewEndEvent(t *testing.T) {
	// create default DataSet
	require.NoError(t, data.CreateDefaultStates())

	cancEd, err := events.NewCancelEventDefinition()
	require.NoError(t, err)

	msg := bpmncommon.MustMessage(
		"message",
		data.MustItemDefinition(
			values.NewVariable(23),
			foundation.WithID("message_item")))
	msgEd, err := events.NewMessageEventDefinition(msg, nil)
	require.NoError(t, err)

	sig, err := events.NewSignal("signal", nil)
	require.NoError(t, err)

	sigEd, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	er, err := bpmncommon.NewError(
		"test_error",
		"test_error_code",
		data.MustItemDefinition(
			values.NewVariable(42),
			foundation.WithID("error_item")))
	require.NoError(t, err)

	eed, err := events.NewErrorEventDefinition(er)
	require.NoError(t, err)

	compEd, err := events.NewCompensationEventDefinition(nil, true)
	require.NoError(t, err)

	esc, err := events.NewEscalation(
		"test_escalaltion",
		"test_escalation_code",
		data.MustItemDefinition(
			values.NewVariable(100),
			foundation.WithID("escalation_item")))
	require.NoError(t, err)

	escEd, err := events.NewEscalationEventDefintion(esc)
	require.NoError(t, err)

	t.Run("empty trigger list end event",
		func(t *testing.T) {
			// invalid option
			_, err := events.NewEndEvent("ivalid_options",
				options.WithName("fake name"))
			require.Error(t, err)
			_, err = events.NewEndEvent("empty_eDef",
				events.WithTerminateTrigger(nil))
			require.Error(t, err)

			ee, err := events.NewEndEvent(
				"no triggers end_event",
				data.WithProperties(
					data.MustProperty(
						"end event property",
						data.MustItemDefinition(
							values.NewVariable(true)),
						data.ReadyDataState)),
				foundation.WithID("my empty end event"))

			require.NoError(t, err)
			require.NotEmpty(t, ee)

			require.NotEqual(t, "", ee.ID())
			require.Equal(t, "no triggers end_event", ee.Name())
			require.Equal(t, ee, ee.Node())
			require.Equal(t, flow.EndEventClass, ee.EventClass())
			require.Nil(t, ee.AcceptIncomingFlow(nil))
			require.Equal(t, flow.EventNodeType, ee.NodeType())
		})

	t.Run("all triggers end event",
		func(t *testing.T) {
			ee, err := events.NewEndEvent("all triggers end_event",
				events.WithCancelTrigger(cancEd),
				events.WithCompensationTrigger(compEd),
				events.WithErrorTrigger(eed),
				events.WithEscalationTrigger(escEd),
				events.WithMessageTrigger(msgEd),
				events.WithSignalTrigger(sigEd),
			)

			require.NoError(t, err)
			require.NotEmpty(t, ee)

			t.Log(ee.ID())
			triggers := ee.Triggers()
			t.Log(triggers)

			require.True(t, ee.HasTrigger(flow.TriggerCancel))
			require.True(t, ee.HasTrigger(flow.TriggerCompensation))
			require.True(t, ee.HasTrigger(flow.TriggerError))
			require.True(t, ee.HasTrigger(flow.TriggerEscalation))
			require.True(t, ee.HasTrigger(flow.TriggerMessage))
			require.True(t, ee.HasTrigger(flow.TriggerSignal))

			require.Equal(t, 6, len(triggers))
			require.Equal(t, 6, len(ee.Definitions()))

			mep := mockeventproc.NewMockEventProducer(t)
			mep.EXPECT().
				PropagateEvent(mock.Anything, mock.Anything).
				RunAndReturn(
					func(_ context.Context, ed flow.EventDefinition) error {
						t.Log("    >>> propagating event: ", ed.Type())
						return nil
					}).Maybe()

			mre := mockrenv.NewMockRuntimeEnvironment(t)
			mre.EXPECT().EventProducer().Return(mep)
			// the message trigger is now published to the broker (SRD-014),
			// not propagated through the event bus.
			mre.EXPECT().MessageBroker().Return(membroker.New()).Maybe()
			mre.EXPECT().GetDataByID(mock.Anything).
				RunAndReturn(
					func(s string) (data.Data, error) {
						dd := map[string]data.Data{
							"error_item": data.MustParameter(
								"error",
								data.MustItemAwareElement(
									data.MustItemDefinition(values.NewVariable(42),
										foundation.WithID("error_item")),
									data.ReadyDataState)),
							"message_item": data.MustParameter(
								"message",
								data.MustItemAwareElement(
									data.MustItemDefinition(values.NewVariable(23),
										foundation.WithID("message_item")),
									data.ReadyDataState)),
							"escalation_item": data.MustParameter(
								"escalation",
								data.MustItemAwareElement(
									data.MustItemDefinition(values.NewVariable(100),
										foundation.WithID("escalation_item")),
									data.ReadyDataState)),
						}

						d, ok := dd[s]
						require.True(t, ok)

						return d, nil
					})

			// the Error trigger ends the process in error (SRD-029 FR-10): the
			// non-error definitions still emit, then Exec returns a typed BpmnError
			// carrying the errorCode.
			_, err = ee.Exec(context.Background(), mre)

			var be *events.BpmnError
			require.ErrorAs(t, err, &be)
			require.Equal(t, "test_error_code", be.Code)
		})
}

// TestEndEventTerminateWins covers the Terminate End Event branch of Exec (SRD-030
// T-5, FR-3/§4.4): Terminate is checked first, so it WINS over a co-located trigger —
// it calls renv.Terminate(), emits NOTHING (the strict mock fails on any
// EventProducer/PropagateEvent call), and returns no flows and no error (not a fault).
func TestEndEventTerminateWins(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	termEd, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	// a co-located message trigger that must NOT be emitted
	msg := bpmncommon.MustMessage("m",
		data.MustItemDefinition(values.NewVariable(1),
			foundation.WithID("term_msg_item")))
	msgEd, err := events.NewMessageEventDefinition(msg, nil)
	require.NoError(t, err)

	ee, err := events.NewEndEvent("terminate end_event",
		events.WithMessageTrigger(msgEd),
		events.WithTerminateTrigger(termEd))
	require.NoError(t, err)

	mre := mockrenv.NewMockRuntimeEnvironment(t)
	mre.EXPECT().Terminate().Return()

	flows, err := ee.Exec(context.Background(), mre)
	require.NoError(t, err)
	require.Empty(t, flows)
}

// TestEndEventMessageThrowError covers the EndEvent.Exec error path when a
// message throw fails (the payload can't be bound from scope).
func TestEndEventMessageThrowError(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	msg := bpmncommon.MustMessage("message",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("message_item")))
	msgEd, err := events.NewMessageEventDefinition(msg, nil)
	require.NoError(t, err)

	ee, err := events.NewEndEvent("end", events.WithMessageTrigger(msgEd))
	require.NoError(t, err)

	re := mockrenv.NewMockRuntimeEnvironment(t)
	re.EXPECT().
		GetDataByID("message_item").
		Return(nil, fmt.Errorf("not in scope"))

	_, err = ee.Exec(context.Background(), re)
	require.Error(t, err)
}
