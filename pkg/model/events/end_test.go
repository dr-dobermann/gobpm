package events_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/generated/mockscope"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
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

	msg := common.MustMessage(
		"message",
		data.MustItemDefinition(
			values.NewVariable(23),
			foundation.WithId("message_item")))
	msgEd, err := events.NewMessageEventDefintion(msg, nil)
	require.NoError(t, err)

	sig, err := events.NewSignal(
		"signal",
		data.MustItemDefinition(nil))
	require.NoError(t, err)

	sigEd, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	er, err := common.NewError(
		"test_error",
		"test_error_code",
		data.MustItemDefinition(
			values.NewVariable(42),
			foundation.WithId("error_item")))
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
			foundation.WithId("escalation_item")))
	require.NoError(t, err)

	escEd, err := events.NewEscalationEventDefintion(esc)
	require.NoError(t, err)

	termEd, err := events.NewTerminateEventDefinition()
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
				foundation.WithId("my empty end event"))

			require.NoError(t, err)
			require.NotEmpty(t, ee)

			require.NotEqual(t, "", ee.Id())
			require.Equal(t, "no triggers end_event", ee.Name())
			require.Equal(t, ee, ee.Node())
			require.Equal(t, flow.EndEventClass, ee.EventClass())
			require.Nil(t, ee.AcceptIncomingFlow(nil))
			require.Equal(t, flow.EventNodeType, ee.NodeType())
			require.Empty(t, ee.DataPath())

			ms := mockscope.NewMockScope(t)
			ms.EXPECT().
				LoadData(mock.Anything, mock.Anything).
				RunAndReturn(
					func(ndl scope.NodeDataLoader, dd ...data.Data) error {
						for _, d := range dd {
							t.Log("Loading data to datapath [", ndl.Name(), "]: ",
								d.Name(), " - ", d.Value().Get())
						}
						return nil
					})

			require.NoError(t, ee.RegisterData(
				scope.DataPath("my end_event data_path"), ms))
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
				events.WithTerminateTrigger(termEd),
			)

			require.NoError(t, err)
			require.NotEmpty(t, ee)

			t.Log(ee.Id())
			triggers := ee.Triggers()
			t.Log(triggers)

			require.True(t, ee.HasTrigger(flow.TriggerCancel))
			require.True(t, ee.HasTrigger(flow.TriggerCompensation))
			require.True(t, ee.HasTrigger(flow.TriggerError))
			require.True(t, ee.HasTrigger(flow.TriggerEscalation))
			require.True(t, ee.HasTrigger(flow.TriggerMessage))
			require.True(t, ee.HasTrigger(flow.TriggerSignal))
			require.True(t, ee.HasTrigger(flow.TriggerTerminate))

			require.Equal(t, 7, len(triggers))
			require.Equal(t, 7, len(ee.Definitions()))

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
			mre.EXPECT().GetDataById(mock.Anything, mock.Anything).
				RunAndReturn(
					func(dp scope.DataPath, s string) (data.Data, error) {
						dd := map[string]data.Data{
							"error_item": data.MustParameter(
								"error",
								data.MustItemAwareElement(
									data.MustItemDefinition(values.NewVariable(42)),
									data.ReadyDataState)),
							"message_item": data.MustParameter(
								"message",
								data.MustItemAwareElement(
									data.MustItemDefinition(values.NewVariable(23)),
									data.ReadyDataState)),
							"escalation_item": data.MustParameter(
								"escalation",
								data.MustItemAwareElement(
									data.MustItemDefinition(values.NewVariable(100)),
									data.ReadyDataState)),
						}

						d, ok := dd[s]
						require.True(t, ok)

						return d, nil
					})

			_, err = ee.Exec(context.Background(), mre)
			require.NoError(t, err)
		})
}
