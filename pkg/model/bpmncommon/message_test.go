package bpmncommon_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

func TestMessage(t *testing.T) {
	t.Run("invalid_params",
		func(t *testing.T) {
			// no name
			_, err := bpmncommon.NewMessage("", nil)
			require.Error(t, err)

			// no item definition
			_, err = bpmncommon.NewMessage("invalid_message", nil)
			require.Error(t, err)

			// must with empty name
			require.Panics(t,
				func() {
					_ = bpmncommon.MustMessage("", nil)
				})
		})

	t.Run("normal",
		func(t *testing.T) {
			m, err := bpmncommon.NewMessage(
				"message",
				data.MustItemDefinition(
					values.NewVariable(100)))
			require.NoError(t, err)
			require.Equal(t, "message", m.Name())
			require.Equal(t, 100, m.Item().Structure().Get(context.Background()))

			require.NotPanics(t,
				func() {
					_ = bpmncommon.MustMessage("message",
						data.MustItemDefinition(
							values.NewVariable(42)))
				})
		})
}
