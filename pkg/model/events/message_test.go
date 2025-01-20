package events_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestNewMessageEventDefintion(t *testing.T) {
	ctx := context.Background()

	t.Run(
		"empty message",
		func(t *testing.T) {
			med, err := events.NewMessageEventDefintion(nil, nil)
			require.Nil(t, med, "message should be nil with empty message")
			require.Error(t, err)
		})

	t.Run(
		"normal",
		func(t *testing.T) {
			msg := common.MustMessage("test_message",
				data.MustItemDefinition(
					values.NewVariable(42),
					foundation.WithId("42")))

			med, err := events.NewMessageEventDefintion(
				msg, nil)
			require.NoError(t, err)

			require.False(t, med.CheckItemDefinition("fake_id"))
			require.True(t, med.CheckItemDefinition("42"))

			iDefs := med.GetItemsList()
			require.Equal(t, 1, len(iDefs))
			require.Equal(t, "42", iDefs[0].Id())
		})

	t.Run(
		"clone with different ItemDefinition id",
		func(t *testing.T) {
			require.NoError(t, data.CreateDefaultStates())

			msg := common.MustMessage("test_message",
				data.MustItemDefinition(values.NewVariable(42)),
				foundation.WithId("42"))

			med := events.MustMessageEventDefinition(msg, nil)

			_, err := med.CloneEvent([]data.Data{
				data.MustParameter(
					"test_param",
					data.MustItemAwareElement(
						data.MustItemDefinition(values.NewVariable(100),
							foundation.WithId("100")),
						data.ReadyDataState)),
			})
			require.Error(t, err)
		})

	t.Run(
		"clone",
		func(t *testing.T) {
			require.NoError(t, data.CreateDefaultStates())

			msg := common.MustMessage("test_message",
				data.MustItemDefinition(
					values.NewVariable(42),
					foundation.WithId("42")))

			med := events.MustMessageEventDefinition(msg, nil)

			nmed, err := med.CloneEvent([]data.Data{
				data.MustParameter(
					"test_param",
					data.MustItemAwareElement(
						data.MustItemDefinition(
							values.NewVariable(100),
							foundation.WithId("42")),
						data.ReadyDataState)),
			})
			require.NoError(t, err)

			require.Equal(t, nmed.GetItemsList()[0].Id(), "42")
			require.Equal(t, 100, nmed.GetItemsList()[0].Structure().Get(ctx))
		})
}
