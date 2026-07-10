package tasks_test

import (
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/stretchr/testify/require"
)

// TestWorkerErrorSelfClassifies covers WorkerError.Error over its precedence:
// a Business Error code, a Status, a technical cause, or an empty error.
func TestWorkerErrorSelfClassifies(t *testing.T) {
	be := &tasks.WorkerError{BpmnErrorCode: "Conflict", Message: "dup"}
	require.Contains(t, be.Error(), "Conflict")
	require.Contains(t, be.Error(), "dup")

	beNoMsg := &tasks.WorkerError{BpmnErrorCode: "Conflict"}
	require.Contains(t, beNoMsg.Error(), "Conflict")

	st := &tasks.WorkerError{Status: values.NewVariable("NOT_FOUND")}
	require.Contains(t, st.Error(), "status")

	tech := &tasks.WorkerError{Cause: errors.New("boom")}
	require.Contains(t, tech.Error(), "boom")

	require.NotEmpty(t, (&tasks.WorkerError{}).Error())

	var _ error = &tasks.WorkerError{}
}
