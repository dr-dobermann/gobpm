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
	require.Equal(t, tasks.OutcomeComplete, o.Kind())
	require.Equal(t, item, o.Output())
	require.Nil(t, o.GetItemsList())
	require.NotEmpty(t, o.Type())
	require.NotEmpty(t, o.ID())

	var _ flow.EventDefinition = o
}

// TestWorkerFaultOutcome: a fault outcome carries the raw fault and no output.
func TestWorkerFaultOutcome(t *testing.T) {
	cause := errors.New("upstream 503")

	o := tasks.NewWorkerFault("j2", tasks.Fault{Code: "503", Cause: cause})

	require.Equal(t, tasks.JobID("j2"), o.JobID())
	require.Equal(t, tasks.OutcomeFault, o.Kind())
	require.Nil(t, o.Output())
	require.Equal(t, "503", o.Fault().Code)
	require.ErrorIs(t, o.Fault().Cause, cause)
	require.Nil(t, o.GetItemsList())
}

// TestWorkerBpmnErrorOutcome: a worker-declared Business Error carries its code
// and message.
func TestWorkerBpmnErrorOutcome(t *testing.T) {
	o := tasks.NewWorkerBpmnError("j3", "ResourceConflict", "already exists")

	require.Equal(t, tasks.OutcomeBpmnError, o.Kind())

	code, msg := o.BpmnError()
	require.Equal(t, "ResourceConflict", code)
	require.Equal(t, "already exists", msg)
}

// TestWorkerStatusOutcome: a worker-declared Business Status carries its value.
func TestWorkerStatusOutcome(t *testing.T) {
	v := values.NewVariable("NOT_FOUND")

	o := tasks.NewWorkerStatus("j4", v)

	require.Equal(t, tasks.OutcomeStatus, o.Kind())
	require.Equal(t, v, o.StatusValue())
}

// TestOutcomeKindString covers the kind→name table used for logging.
func TestOutcomeKindString(t *testing.T) {
	require.Equal(t, "complete", tasks.OutcomeComplete.String())
	require.Equal(t, "bpmnError", tasks.OutcomeBpmnError.String())
	require.Equal(t, "status", tasks.OutcomeStatus.String())
	require.Equal(t, "fault", tasks.OutcomeFault.String())
	require.Equal(t, "unknown", tasks.OutcomeKind(99).String())
}
