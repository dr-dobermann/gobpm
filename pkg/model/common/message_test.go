package common_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

func TestMessage(t *testing.T) {
	t.Run("invalid_params",
		func(t *testing.T) {
			// no name
			_, err := common.NewMessage("", nil)
			require.Error(t, err)

			// no item definition
			_, err = common.NewMessage("invalid_message", nil)
			require.Error(t, err)

			// must with empty name
			require.Panics(t,
				func() {
					_ = common.MustMessage("", nil)
				})
		})

	t.Run("normal",
		func(t *testing.T) {
			m, err := common.NewMessage(
				"message",
				data.MustItemDefinition(
					values.NewVariable(100)))
			require.NoError(t, err)
			require.Equal(t, "message", m.Name())
			require.Equal(t, 100, m.Item().Structure().Get(context.Background()))

			require.NotPanics(t,
				func() {
					_ = common.MustMessage("message",
						data.MustItemDefinition(
							values.NewVariable(42)))
				})
		})
}
