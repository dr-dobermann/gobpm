package interactor_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/stretchr/testify/require"
)

func TestTaskCompletion(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	outputs := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("ok")),
				data.ReadyDataState)),
	}

	c := interactor.NewTaskCompletion(outputs)

	// It is a flow.EventDefinition with an internal sentinel trigger and no items.
	require.NotEmpty(t, c.ID())
	require.Equal(t, flow.EventTrigger("UserTaskCompletion"), c.Type())
	require.Nil(t, c.GetItemsList())

	// Outputs returns an independent copy of the carried outputs.
	got := c.Outputs()
	require.Len(t, got, 1)
	require.Equal(t, "result", got[0].Name())
	require.Equal(t, "ok", got[0].Value().Get(context.Background()))
}
