package events_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

func TestNewEndEvent(t *testing.T) {

	// create default DataSet
	require.NoError(t, data.CreateDefaultStates())

	cancEd, err := events.NewCancelEventDefinition("test_cancel_id")
	require.NoError(t, err)

	msg := common.MustMessage(
		"message",
		data.MustItemDefinition(
			values.NewVariable[int](23)))
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
		data.MustItemDefinition(values.NewVariable[int](42)))
	require.NoError(t, err)

	eed, err := events.NewErrorEventDefinition(er)
	require.NoError(t, err)

	compEd, err := events.NewCompensationEventDefinition(nil, true)
	require.NoError(t, err)

	esc, err := events.NewEscalation(
		"test_escalaltion",
		"test_escalation_code",
		data.MustItemDefinition(
			values.NewVariable[int](100)))
	require.NoError(t, err)

	escEd, err := events.NewEscalationEventDefintion(esc)
	require.NoError(t, err)

	termEd, err := events.NewTerminateEventDefinition()
	require.NoError(t, err)

	t.Run("empty trigger list end event",
		func(t *testing.T) {
			ee, err := events.NewEndEvent("no triggers end_event")

			require.NoError(t, err)
			require.NotEmpty(t, ee)

			t.Log(ee.Id())

			require.NotEqual(t, "", ee.Id())
			require.Equal(t, "no triggers end_event", ee.Name())
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

			require.True(t, ee.HasTrigger(events.TriggerCancel))
			require.True(t, ee.HasTrigger(events.TriggerCompensation))
			require.True(t, ee.HasTrigger(events.TriggerError))
			require.True(t, ee.HasTrigger(events.TriggerEscalation))
			require.True(t, ee.HasTrigger(events.TriggerMessage))
			require.True(t, ee.HasTrigger(events.TriggerSignal))
			require.True(t, ee.HasTrigger(events.TriggerTerminate))

			require.Equal(t, 7, len(triggers))
			require.Equal(t, 7, len(ee.Definitions()))
		})
}
