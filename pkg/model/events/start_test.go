package events_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestNewStartEvent(t *testing.T) {

	msg := common.MustMessage(
		"message",
		data.MustItemDefinition(nil))

	sig, err := events.NewSignal(
		"signal",
		data.MustItemDefinition(nil))
	require.NoError(t, err)

	t.Run("empty definitions list",
		func(t *testing.T) {
			se, err := events.NewStartEvent("NoneTrigger")

			require.NoError(t, err)
			require.NotNil(t, se)

			t.Log(se.Id())

			require.NotEqual(t, "", se.Id())
			require.Equal(t, "NoneTrigger", se.Name())
			require.Equal(t, 0, len(se.Triggers()))
			require.False(t, se.IsInterrupting())
			require.False(t, se.IsParallelMultiple())

			require.Equal(t, 0, len(se.Definitions()))
		})

	t.Run("empty definitions list with properties",
		func(t *testing.T) {
			se, err := events.NewStartEvent(
				"NoneTrigger",
				events.WithProperty(*data.NewProperty(
					"event_property", nil, nil)),
				foundation.WithId("none_trigger_start_event"))

			require.NoError(t, err)
			require.NotNil(t, se)

			t.Log(se.Id())

			props := se.Properties()

			require.Equal(t, 1, len(props))
			require.Equal(t, "event_property", props[0].Name())
		})

	t.Run("message interrupting event",
		func(t *testing.T) {
			se, err := events.NewStartEvent("message_interrupting_event",
				events.WithMessageTrigger(
					events.MustMessageEventDefinition(msg, nil)),
				events.WithInterrupting(),
			)

			require.NoError(t, err)
			require.NotEmpty(t, se)

			t.Log(se.Id())
			triggers := se.Triggers()

			require.Equal(t, 1, len(triggers))
			require.True(t, se.IsInterrupting())
			require.Equal(t, events.TriggerMessage, triggers[0])
			require.Equal(t, 1, len(se.Definitions()))
		})

	t.Run("message and signal event",
		func(t *testing.T) {
			se, err := events.NewStartEvent("message_and_signal_event",
				events.WithMessageTrigger(
					events.MustMessageEventDefinition(msg, nil)),
				events.WithSignalTrigger(
					*events.MustSignalEventDefinition(sig)),
			)

			require.NoError(t, err)
			require.NotEmpty(t, se)

			t.Log(se.Id())
			triggers := se.Triggers()

			require.True(t, se.HasTrigger(events.TriggerSignal))
			require.True(t, se.HasTrigger(events.TriggerMessage))

			require.Equal(t, 2, len(triggers))
			require.Equal(t, 2, len(se.Definitions()))
		})

	t.Run("multiple parallel with id event",
		func(t *testing.T) {
			se, err := events.NewStartEvent("message_and_signal_parallel_event",
				events.WithMessageTrigger(
					events.MustMessageEventDefinition(msg, nil)),
				events.WithSignalTrigger(
					*events.MustSignalEventDefinition(sig)),
				events.WithParallel(),
				foundation.WithId("start_event_id"),
			)

			require.NoError(t, err)
			require.NotEmpty(t, se)

			t.Log(se.Id())
			triggers := se.Triggers()

			require.True(t, se.HasTrigger(events.TriggerSignal))
			require.True(t, se.HasTrigger(events.TriggerMessage))

			require.True(t, se.IsParallelMultiple())

			require.Equal(t, "start_event_id", se.Id())

			require.Equal(t, 2, len(triggers))
			require.Equal(t, 2, len(se.Definitions()))
		})

	t.Run("start event with all triggers",
		func(t *testing.T) {
			se, err := events.NewStartEvent("message_and_signal_parallel_event",
				events.WithMessageTrigger(
					events.MustMessageEventDefinition(msg, nil)),
				events.WithSignalTrigger(
					*events.MustSignalEventDefinition(sig)),
				events.WithConditionalTrigger(
					*events.MustConditionalEventDefinition(
						data.NewExpression("this is a dummy expression"))),
				events.WithTimerTrigger(
					*events.MustTimerEventDefinition(nil, nil, nil)),
			)

			require.NoError(t, err)
			require.NotEmpty(t, se)

			t.Log(se.Id())
			triggers := se.Triggers()

			require.True(t, se.HasTrigger(events.TriggerSignal))
			require.True(t, se.HasTrigger(events.TriggerMessage))
			require.True(t, se.HasTrigger(events.TriggerConditional))
			require.True(t, se.HasTrigger(events.TriggerTimer))

			require.False(t, se.IsParallelMultiple())

			require.Equal(t, 4, len(triggers))
			require.Equal(t, 4, len(se.Definitions()))
		})
}
