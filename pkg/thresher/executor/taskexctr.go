package executor

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/tasks"
	"github.com/dr-dobermann/gobpm/pkg/model"
)

// ----------------------------------------------------------------------------
// TaskExecutor defines the run-time functionatlity of the Task objects
type TaskExecutor interface {
	model.TaskModel

	NodeExecutor
}

// ----------------------------------------------------------------------------

// GetTaskExecutor returns a Task Executor linked to given TaskModel.
// if there is no known Executor or there are errors in retrieving it,
// error would be returned
func GetTaskExecutor(t model.TaskModel) (TaskExecutor, error) {
	var te TaskExecutor

	switch ts := t.(type) {
	case *model.StoreTask:
		te = tasks.NewStoreTaskExecutor(ts)

	case *model.SendTask:
		te = tasks.NewSendTaskExecutor(ts)

	case *model.OutputTask:
		te = tasks.NewOutputTaskExecutor(ts)

	case *model.ReceiveTask:
		te = tasks.NewReceiveTaskExecutor(ts)

	default:
		return nil, fmt.Errorf("invalid task type: %s", t.TaskType().String())
	}

	return te, nil
}
