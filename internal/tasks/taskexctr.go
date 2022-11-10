package tasks

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/thresher/executor"
)

// ----------------------------------------------------------------------------
// TaskExecutor defines the run-time functionatlity of the Task objects
type TaskExecutor interface {
	model.TaskModel

	executor.NodeExecutor
}

// ----------------------------------------------------------------------------

// GetTaskExecutor returns a Task Executor linked to given TaskModel.
// if there is no known Executor or there are errors in retrieving it,
// error would be returned
func GetTaskExecutor(t model.TaskModel) (TaskExecutor, error) {
	var te TaskExecutor

	switch ts := t.(type) {
	case *model.StoreTask:
		te = NewStoreTaskExecutor(ts)

	case *model.SendTask:
		te = NewSendTaskExecutor(ts)

	case *model.OutputTask:
		te = NewOutputTaskExecutor(ts)

	case *model.ReceiveTask:
		te = NewReceiveTaskExecutor(ts)

	default:
		return nil, fmt.Errorf("invalid task type: %s", t.TaskType().String())
	}

	return te, nil
}