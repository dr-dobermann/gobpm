package events_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockdata"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

func TestErrorDefinitions(t *testing.T) {
	data.CreateDefaultStates()

	ctx := context.Background()

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
			_, err := events.NewConditionalEventDefinition(nil)
			require.Error(t, err)
			require.Panics(t, func() {
				_ = events.MustConditionalEventDefinition(
					expr,
					options.WithName("my great name"))
			})

			// normal params
			ced, err := events.NewConditionalEventDefinition(expr)
			require.NoError(t, err)
			require.Equal(t, expr.ID(), ced.Condition().ID())
			require.Len(t, ced.GetItemsList(), 0)
		})

	t.Run("error",
		func(t *testing.T) {
			e, err := bpmncommon.NewError("fsio propalo", "ZHOPA",
				data.MustItemDefinition(values.NewVariable(-1),
					foundation.WithID("error_item")))
			require.NoError(t, err)

			// empty error
			_, err = events.NewErrorEventDefinition(nil)
			require.Error(t, err)

			// invalid option
			_, err = events.NewErrorEventDefinition(e, options.WithName("invalid option"))
			require.Error(t, err)

			// with error
			eed, err := events.NewErrorEventDefinition(e)
			require.NoError(t, err)
			require.Equal(t, e.ID(), eed.Error().ID())
			require.True(t, eed.CheckItemDefinition("error_item"))

			// cloning error
			_, err = eed.CloneEventDefinition(
				[]data.Data{
					data.MustParameter("invalid",
						data.MustItemAwareElement(
							data.MustItemDefinition(
								values.NewVariable(200),
								foundation.WithID("invalid")),
							data.ReadyDataState)),
				})
			require.Error(t, err)

			need, err := eed.CloneEventDefinition(
				[]data.Data{
					data.MustParameter(
						"new_error",
						data.MustItemAwareElement(
							data.MustItemDefinition(
								values.NewVariable(1000),
								foundation.WithID("error_item")),
							data.ReadyDataState)),
				})
			require.NoError(t, err)
			require.Equal(t, 1000, need.GetItemsList()[0].Structure().Get(ctx))
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
				foundation.WithID("test escalation id"))
			require.NoError(t, err)
			require.NotEmpty(t, e)
			require.Equal(t, 42, e.Item().Structure().Get(ctx))
			require.Equal(t, "test", e.Name())
			require.Empty(t, e.Code())

			// test EscalationErrorDefinition
			//
			// empty params
			_, err = events.NewEscalationEventDefinition(nil)
			require.Error(t, err)

			// normal params
			eed, err := events.NewEscalationEventDefinition(e)
			require.NoError(t, err)
			require.NotEmpty(t, eed)
			require.Equal(t, e.ID(), eed.Escalation().ID())
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
			require.Equal(t, "success!", s.Item().Structure().Get(ctx))

			// signal evnet definition test
			//
			// invalid params
			_, err = events.NewSignalEventDefinition(nil)
			require.Error(t, err)

			// normal params
			sed, err := events.NewSignalEventDefinition(s)
			require.NoError(t, err)
			require.NotEmpty(t, sed)
			require.Equal(t, "success!", sed.Signal().Item().Structure().Get(ctx))
			require.Equal(t, s.ID(), sed.Signal().ID())
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

			tmr := getTimerExpression(t, "time")
			cycle := getTimerExpression(t, "cycle")
			duration := getTimerExpression(t, "duration")

			// invalid params
			_, err := events.NewTimerEventDefinition(nil, nil, nil)
			require.Error(t, err)

			_, err = events.NewTimerEventDefinition(tmr, cycle, duration)
			require.Error(t, err)

			_, err = events.NewTimerEventDefinition(tmr, nil, duration)
			require.Error(t, err)

			_, err = events.NewTimerEventDefinition(tmr, cycle, nil)
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

			sed, err = events.NewTimerEventDefinition(nil, cycle, duration)
			require.NoError(t, err)
			require.NotEmpty(t, sed)
		})
}

// TestSignalEventDefinitionGetItemsList pins the FIX-011 rename: GetItemsList
// (plural) now overrides flow.EventDefinition, so a signal built with an
// ItemDefinition reports it (the misspelled GetItemList used to be dead and the
// embedded definition's empty list was returned instead).
func TestSignalEventDefinitionGetItemsList(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// payload-less signal reports no items
	bare, err := events.NewSignal("bare", nil)
	require.NoError(t, err)
	bareEd, err := events.NewSignalEventDefinition(bare)
	require.NoError(t, err)
	require.Empty(t, bareEd.GetItemsList())

	// signal carrying an ItemDefinition reports exactly it
	item := data.MustItemDefinition(values.NewVariable("payload"),
		foundation.WithID("sig_item"))
	sig, err := events.NewSignal("sig", item)
	require.NoError(t, err)
	sigEd, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	items := sigEd.GetItemsList()
	require.Len(t, items, 1)
	require.Equal(t, "sig_item", items[0].ID())
}

// TestEventDefClonerSatisfied pins the FIX-011 rename: the three throw-able
// event definitions name their clone method CloneEventDefinition and so satisfy
// flow.EventDefCloner (the old CloneEvent never did, leaving the throw-path
// clone-with-data step a silent no-op). It also checks the clone carries the
// supplied data item.
func TestEventDefClonerSatisfied(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, data.CreateDefaultStates())

	readyItem := func(id string, v any) []data.Data {
		return []data.Data{
			data.MustParameter(id,
				data.MustItemAwareElement(
					data.MustItemDefinition(values.NewVariable(v),
						foundation.WithID(id)),
					data.ReadyDataState)),
		}
	}

	t.Run("message", func(t *testing.T) {
		msg, err := bpmncommon.NewMessage("msg",
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("msg_item")))
		require.NoError(t, err)
		med, err := events.NewMessageEventDefinition(msg, nil)
		require.NoError(t, err)

		var cloner flow.EventDefCloner = med
		cloned, err := cloner.CloneEventDefinition(readyItem("msg_item", 7))
		require.NoError(t, err)
		require.Equal(t, 7, cloned.GetItemsList()[0].Structure().Get(ctx))

		// data whose item id does not match the definition's is rejected
		_, err = cloner.CloneEventDefinition(readyItem("other", 7))
		require.Error(t, err)
	})

	t.Run("error", func(t *testing.T) {
		cErr, err := bpmncommon.NewError("err", "E1",
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("err_item")))
		require.NoError(t, err)
		eed, err := events.NewErrorEventDefinition(cErr)
		require.NoError(t, err)

		var cloner flow.EventDefCloner = eed
		cloned, err := cloner.CloneEventDefinition(readyItem("err_item", 9))
		require.NoError(t, err)
		require.Equal(t, 9, cloned.GetItemsList()[0].Structure().Get(ctx))
	})

	t.Run("escalation", func(t *testing.T) {
		esc, err := events.NewEscalation("esc", "S1",
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("esc_item")))
		require.NoError(t, err)
		eed, err := events.NewEscalationEventDefinition(esc)
		require.NoError(t, err)

		var cloner flow.EventDefCloner = eed
		cloned, err := cloner.CloneEventDefinition(readyItem("esc_item", 11))
		require.NoError(t, err)
		require.Equal(t, 11, cloned.GetItemsList()[0].Structure().Get(ctx))

		// data whose item id does not match the definition's is rejected
		_, err = cloner.CloneEventDefinition(readyItem("other", 11))
		require.Error(t, err)
	})
}
