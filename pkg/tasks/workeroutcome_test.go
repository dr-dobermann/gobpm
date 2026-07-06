package tasks_test

import (
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/stretchr/testify/require"
)

// TestWorkerCompleteOutcome: a completion outcome carries the output item and no
// cause, and is a well-formed flow.EventDefinition.
func TestWorkerCompleteOutcome(t *testing.T) {
	item := data.MustItemDefinition(
		values.NewVariable("ok"), foundation.WithID("out"))

	o := tasks.NewWorkerComplete("j1", item)

	require.Equal(t, tasks.JobID("j1"), o.JobID())
	require.Equal(t, item, o.Output())
	require.NoError(t, o.Cause())
	require.Nil(t, o.GetItemsList())
	require.NotEmpty(t, o.Type())
	require.NotEmpty(t, o.ID())

	var _ flow.EventDefinition = o
}

// TestWorkerFailOutcome: a fault outcome carries the cause and no output.
func TestWorkerFailOutcome(t *testing.T) {
	cause := errors.New("upstream 503")

	o := tasks.NewWorkerFail("j2", cause)

	require.Equal(t, tasks.JobID("j2"), o.JobID())
	require.Nil(t, o.Output())
	require.ErrorIs(t, o.Cause(), cause)
	require.Nil(t, o.GetItemsList())
}
