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

func TestMessageClone(t *testing.T) {
	ctx := context.Background()

	t.Run("item with structure is isolated per clone",
		func(t *testing.T) {
			m := bpmncommon.MustMessage("msg",
				data.MustItemDefinition(values.NewVariable(7)))

			clone := m.Clone()

			// identity preserved, independent objects.
			require.NotSame(t, m, clone)
			require.Equal(t, m.ID(), clone.ID())
			require.Equal(t, m.Name(), clone.Name())
			require.Equal(t, m.Item().ID(), clone.Item().ID())
			require.NotSame(t, m.Item(), clone.Item())

			// mutating the clone's value leaves the original untouched.
			require.NoError(t, clone.Item().Structure().Update(ctx, 99))
			require.Equal(t, 99, clone.Item().Structure().Get(ctx))
			require.Equal(t, 7, m.Item().Structure().Get(ctx))
		})

	t.Run("item with nil structure clones to a fresh empty item",
		func(t *testing.T) {
			m := bpmncommon.MustMessage("msg",
				data.MustItemDefinition(nil))

			clone := m.Clone()

			require.Equal(t, m.Item().ID(), clone.Item().ID())
			require.NotSame(t, m.Item(), clone.Item())
			require.Nil(t, clone.Item().Structure())
		})
}
