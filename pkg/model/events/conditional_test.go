package events_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

func TestConditionalDefinitionBoolGate(t *testing.T) {
	data.CreateDefaultStates()

	t.Run("bool condition accepted", func(t *testing.T) {
		cond := goexpr.Must(nil,
			data.MustItemDefinition(values.NewVariable(false)),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(true), nil
			})

		ced, err := events.NewConditionalEventDefinition(cond)
		require.NoError(t, err)
		require.Equal(t, cond.ID(), ced.Condition().ID())
	})

	t.Run("non-bool condition rejected", func(t *testing.T) {
		cond := goexpr.Must(nil,
			data.MustItemDefinition(values.NewVariable(42)),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(42), nil
			})

		_, err := events.NewConditionalEventDefinition(cond)
		require.Error(t, err)
		require.Contains(t, err.Error(), "boolean")
	})
}
