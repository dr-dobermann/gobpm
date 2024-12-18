package events_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestEscalation(t *testing.T) {
	ctx := context.Background()

	t.Run(
		"empty name",
		func(t *testing.T) {
			_, err := events.NewEscalation("", "",
				data.MustItemDefinition(values.NewVariable(42)))
			require.Error(t, err)
		})

	t.Run(
		"empty item definition",
		func(t *testing.T) {
			_, err := events.NewEscalation("test", "test", nil)
			require.Error(t, err)
		})

	t.Run(
		"normal",
		func(t *testing.T) {
			const (
				eName = "test_escalation"
				eCode = "test_eCode"
			)

			e, err := events.NewEscalation(
				eName,
				eCode,
				data.MustItemDefinition(values.NewVariable("test")))
			require.NoError(t, err)
			require.NotEmpty(t, e)

			require.Equal(t, eName, e.Name())
			require.Equal(t, eCode, e.Code())

			require.Equal(t, "test", e.Item().Structure().Get(ctx))
		})
}

func TestEscalationDefinition(t *testing.T) {

	ctx := context.Background()

	t.Run(
		"empty escalation",
		func(t *testing.T) {
			_, err := events.NewEscalationEventDefintion(nil)
			require.Error(t, err)
		})

	t.Run(
		"normal",
		func(t *testing.T) {
			iDef := data.MustItemDefinition(values.NewVariable(42))
			ed, err := events.NewEscalationEventDefintion(
				events.MustEscalation("test", "code", iDef))
			require.NoError(t, err)
			require.NotEmpty(t, ed)

			require.Equal(t, flow.TriggerEscalation, ed.Type())
			require.True(t, ed.CheckItemDefinition(iDef.Id()))
			iDefs := ed.GetItemsList()
			require.Equal(t, 1, len(iDefs))
			require.Equal(t, iDef.Id(), iDefs[0].Id())
		})

	t.Run(
		"clone with different ItemDefinition id",
		func(t *testing.T) {
			require.NoError(t, data.CreateDefaultStates())

			iDef := data.MustItemDefinition(
				values.NewVariable(42),
				foundation.WithId("42"))

			ed, err := events.NewEscalationEventDefintion(
				events.MustEscalation("test", "code", iDef))
			require.NoError(t, err)

			_, err = ed.CloneEvent(
				[]data.Data{data.MustParameter(
					"test",
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(100),
							foundation.WithId("100")),
						data.ReadyDataState))})
			require.Error(t, err)
		})

	t.Run(
		"clone",
		func(t *testing.T) {
			require.NoError(t, data.CreateDefaultStates())

			iDef := data.MustItemDefinition(values.NewVariable(42))

			ed, err := events.NewEscalationEventDefintion(
				events.MustEscalation("test", "code", iDef))
			require.NoError(t, err)

			ced, err := ed.CloneEvent(
				[]data.Data{data.MustParameter(
					"test",
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(100),
							foundation.WithId(iDef.Id())),
						data.ReadyDataState))})
			require.NoError(t, err)

			iDefs := ced.GetItemsList()
			require.Equal(t, 1, len(iDefs))
			require.Equal(t, 100, iDefs[0].Structure().Get(ctx))
			require.Equal(t, iDef.Id(), iDefs[0].Id())
		})
}
