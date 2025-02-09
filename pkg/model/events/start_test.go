package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestNewStartEvent(t *testing.T) {
	msg := common.MustMessage(
		"message",
		data.MustItemDefinition(nil))

	iDef, err := data.NewItemDefinition(values.NewVariable(42))
	require.NoError(t, err)

	prop, err := data.NewProperty("event_property", iDef, data.ReadyDataState)
	require.NoError(t, err)

	sig, err := events.NewSignal(
		"signal",
		data.MustItemDefinition(nil))
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
				data.WithProperties(prop),
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
			require.Equal(t, flow.TriggerMessage, triggers[0])
			require.Equal(t, 1, len(se.Definitions()))
		})

	t.Run("message and signal event",
		func(t *testing.T) {
			se, err := events.NewStartEvent("message_and_signal_event",
				events.WithMessageTrigger(
					events.MustMessageEventDefinition(msg, nil)),
				events.WithSignalTrigger(
					events.MustSignalEventDefinition(sig)),
			)

			require.NoError(t, err)
			require.NotEmpty(t, se)

			t.Log(se.Id())
			triggers := se.Triggers()

			require.True(t, se.HasTrigger(flow.TriggerSignal))
			require.True(t, se.HasTrigger(flow.TriggerMessage))

			require.Equal(t, 2, len(triggers))
			require.Equal(t, 2, len(se.Definitions()))
		})

	t.Run("multiple parallel with id event",
		func(t *testing.T) {
			se, err := events.NewStartEvent("message_and_signal_parallel_event",
				events.WithMessageTrigger(
					events.MustMessageEventDefinition(msg, nil)),
				events.WithSignalTrigger(
					events.MustSignalEventDefinition(sig)),
				events.WithParallel(),
				foundation.WithId("start_event_id"),
			)

			require.NoError(t, err)
			require.NotEmpty(t, se)

			t.Log(se.Id())
			triggers := se.Triggers()

			require.True(t, se.HasTrigger(flow.TriggerSignal))
			require.True(t, se.HasTrigger(flow.TriggerMessage))

			require.True(t, se.IsParallelMultiple())

			require.Equal(t, "start_event_id", se.Id())

			require.Equal(t, 2, len(triggers))
			require.Equal(t, 2, len(se.Definitions()))
		})

	t.Run("start event with all triggers",
		func(t *testing.T) {
			se, err := events.NewStartEvent("start_event_with_all_triggers",
				events.WithCompensationTrigger(compEd),
				events.WithConditionalTrigger(
					events.MustConditionalEventDefinition(
						getDummyCondition(t))),
				events.WithErrorTrigger(eed),
				events.WithEscalationTrigger(escEd),
				events.WithMessageTrigger(
					events.MustMessageEventDefinition(msg, nil)),
				events.WithSignalTrigger(
					events.MustSignalEventDefinition(sig)),
				events.WithTimerTrigger(
					events.MustTimerEventDefinition(getTimerExpression(t, "time"), nil, nil)),
			)

			require.NoError(t, err)
			require.NotEmpty(t, se)

			t.Log(se.Id())
			triggers := se.Triggers()

			require.True(t, se.HasTrigger(flow.TriggerCompensation))
			require.True(t, se.HasTrigger(flow.TriggerConditional))
			require.True(t, se.HasTrigger(flow.TriggerError))
			require.True(t, se.HasTrigger(flow.TriggerEscalation))
			require.True(t, se.HasTrigger(flow.TriggerMessage))
			require.True(t, se.HasTrigger(flow.TriggerSignal))
			require.True(t, se.HasTrigger(flow.TriggerTimer))

			require.False(t, se.IsParallelMultiple())

			require.Equal(t, 7, len(triggers))
			require.Equal(t, 7, len(se.Definitions()))
		})
}

func getDummyCondition(t *testing.T) data.FormalExpression {
	ctx := context.Background()

	mds := mockdata.NewMockSource(t)
	mds.EXPECT().Find(ctx, "x").Return(nil, nil).Maybe()

	return goexpr.Must(
		mds,
		data.MustItemDefinition(
			values.NewVariable(true)),
		func(
			_ context.Context,
			ds data.Source,
		) (data.Value, error) {
			return values.NewVariable(true), nil
		})
}

func getTimerExpression(t *testing.T, fName string) data.FormalExpression {
	ctx := context.Background()

	mds := mockdata.NewMockSource(t)
	mds.EXPECT().Find(ctx, "x").Return(nil, nil).Maybe()

	switch fName {
	case "time":
		return goexpr.Must(
			mds,
			data.MustItemDefinition(
				values.NewVariable(time.Now())),
			func(_ context.Context, ds data.Source) (data.Value, error) {
				return values.NewVariable(time.Now()), nil
			})

	case "cycle":
		return goexpr.Must(
			mds,
			data.MustItemDefinition(
				values.NewVariable(10)),
			func(_ context.Context, ds data.Source) (data.Value, error) {
				return values.NewVariable(10), nil
			})

	case "duration":
		return goexpr.Must(
			mds,
			data.MustItemDefinition(
				values.NewVariable(time.Second*5)),
			func(_ context.Context, ds data.Source) (data.Value, error) {
				return values.NewVariable(time.Second * 5), nil
			})
	}

	panic("invalid timer expression name: " + fName)
}
