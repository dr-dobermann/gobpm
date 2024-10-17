package events_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

func TestErrorDefinitions(t *testing.T) {
	t.Run("cancel",
		func(t *testing.T) {
			// invalid params
			_, err := events.NewCancelEventDefinition(
				options.WithName("my great name"))
			require.Error(t, err)
		})

	t.Run("compensation",
		func(t *testing.T) {
			// invalid params
			_, err := events.NewCompensationEventDefinition(
				nil, false, options.WithName("my great name"))
			require.Error(t, err)
		})

	t.Run("conditional",
		func(t *testing.T) {
			expr := getDummyCondition(t)
			require.NotEmpty(t, expr)

			// invalid params
			ced, err := events.NewConditionalEventDefinition(nil)
			require.Error(t, err)
			require.Empty(t, ced)
			require.Panics(t, func() {
				_ = events.MustConditionalEventDefinition(
					expr,
					options.WithName("my great name"))
			})

			// normal params
			ced, err = events.NewConditionalEventDefinition(expr)
			require.NoError(t, err)
			require.NotEmpty(t, ced)
			require.Equal(t, expr.Id(), ced.Condition().Id())
			require.Len(t, ced.GetItemsList(), 0)
		})

	t.Run("error",
		func(t *testing.T) {
			e, err := common.NewError("fsio propalo", "ZHOPA",
				data.MustItemDefinition(values.NewVariable(-1)))
			require.NoError(t, err)
			require.NotEmpty(t, e)

			// empty error
			eed, err := events.NewErrorEventDefinition(nil)
			require.Error(t, err)
			require.Empty(t, eed)

			// with error
			eed, err = events.NewErrorEventDefinition(e)
			require.NoError(t, err)
			require.NotEmpty(t, eed)
			require.Equal(t, e.Id(), eed.Error().Id())
		})

	t.Run("escalation",
		func(t *testing.T) {
			// test escalation building
			//
			// invalid params
			_, err := events.NewEscalation("", "", nil)
			require.Error(t, err)

			_, err = events.NewEscalation("test", "", nil)
			require.Error(t, err)

			// normal params
			e, err := events.NewEscalation("test", "",
				data.MustItemDefinition(values.NewVariable(42)),
				foundation.WithId("test escalation id"))
			require.NoError(t, err)
			require.NotEmpty(t, e)
			require.Equal(t, 42, e.Item().Structure().Get())
			require.Equal(t, "test", e.Name())
			require.Empty(t, e.Code())

			// test EscalationErrorDefinition
			//
			// empty params
			_, err = events.NewEscalationEventDefintion(nil)
			require.Error(t, err)

			// normal params
			eed, err := events.NewEscalationEventDefintion(e)
			require.NoError(t, err)
			require.NotEmpty(t, eed)
			require.Equal(t, e.Id(), eed.Escalation().Id())
		})

	t.Run("signal",
		func(t *testing.T) {
			// signal test
			//
			// invalid params
			_, err := events.NewSignal("", nil)
			require.Error(t, err)

			// normal params
			s, err := events.NewSignal("test signal", nil)
			require.NoError(t, err)
			require.NotEmpty(t, s)

			s, err = events.NewSignal("test signal",
				data.MustItemDefinition(values.NewVariable("success!")))
			require.NoError(t, err)
			require.NotEmpty(t, s)
			require.Equal(t, "test signal", s.Name())
			require.Equal(t, "success!", s.Item().Structure().Get())

			// signal evnet definition test
			//
			// invalid params
			_, err = events.NewSignalEventDefinition(nil)
			require.Error(t, err)

			// normal params
			sed, err := events.NewSignalEventDefinition(s)
			require.NoError(t, err)
			require.NotEmpty(t, sed)
			require.Equal(t, "success!", sed.Signal().Item().Structure().Get())
			require.Equal(t, s.Id(), sed.Signal().Id())
		})

	t.Run("timer",
		func(t *testing.T) {
			ctx := context.Background()

			mds := mockdata.NewMockSource(t)
			mds.EXPECT().Find(ctx, "x").Return(nil, nil).Maybe()

			invExprType := goexpr.Must(
				mds,
				data.MustItemDefinition(
					values.NewVariable("wrong_res_value")),
				func(ctx context.Context, ds data.Source) (data.Value, error) {
					return values.NewVariable("wrong_res_type"), nil
				})

			tmr := getTimerExpression(t)

			// invalid params
			_, err := events.NewTimerEventDefinition(nil, nil, nil)
			require.Error(t, err)

			_, err = events.NewTimerEventDefinition(tmr, tmr, tmr)
			require.Error(t, err)

			_, err = events.NewTimerEventDefinition(tmr, nil, tmr)
			require.Error(t, err)

			_, err = events.NewTimerEventDefinition(tmr, tmr, nil)
			require.Error(t, err)

			// invalid expression type
			_, err = events.NewTimerEventDefinition(invExprType, nil, nil)
			require.Error(t, err)

			_, err = events.NewTimerEventDefinition(nil, invExprType, nil)
			require.Error(t, err)

			_, err = events.NewTimerEventDefinition(nil, nil, invExprType)
			require.Error(t, err)

			// normal params
			sed, err := events.NewTimerEventDefinition(tmr, nil, nil)
			require.NoError(t, err)
			require.NotEmpty(t, sed)

			sed, err = events.NewTimerEventDefinition(nil, tmr, nil)
			require.NoError(t, err)
			require.NotEmpty(t, sed)

			sed, err = events.NewTimerEventDefinition(nil, nil, tmr)
			require.NoError(t, err)
			require.NotEmpty(t, sed)
		})
}
