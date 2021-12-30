package thresher

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/model"
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
		te = NewStoreTaskExecutor(ts)

	case *model.SendTask:
		te = NewSendTaskExecutor(ts)

	default:
		return nil, fmt.Errorf("invalid task type: %s", t.TaskType().String())
	}

	return te, nil
}

//-----------------------------------------------------------------------------
//                           Special Tasks
//-----------------------------------------------------------------------------
type StoreTaskExecutor struct {
	model.StoreTask
}

func NewStoreTaskExecutor(st *model.StoreTask) *StoreTaskExecutor {
	if st == nil {
		return nil
	}

	ste := new(StoreTaskExecutor)
	ste.StoreTask = *st

	return ste
}

func (ste *StoreTaskExecutor) Exec(_ context.Context,
	tr *track) ([]*model.SequenceFlow, error) {

	for _, v := range ste.Vars {
		if _, err := tr.instance.vs.NewVar(v); err != nil {
			return nil,
				fmt.Errorf(
					"couldn't add variable %s to instance: %v",
					v.Name(), err)
		}
	}

	// TODO: Add expression check on output flows
	return ste.GetOutputFlows(), nil
}

//-----------------------------------------------------------------------------

type OutputTaskExecutor struct {
	model.OutputTask
}

func NewOutputTaskExecutor(ot *model.OutputTask) *OutputTaskExecutor {
	if ot == nil {
		return nil
	}

	ote := new(OutputTaskExecutor)
	ote.OutputTask = *ot

	return ote
}

func (ote *OutputTaskExecutor) Exec(_ context.Context,
	tr *track) ([]*model.SequenceFlow, error) {

	// TODO: Add expression check on output flows
	return ote.GetOutputFlows(), nil
}

//-----------------------------------------------------------------------------

type SendTaskExecutor struct {
	model.SendTask
}

func NewSendTaskExecutor(st *model.SendTask) *SendTaskExecutor {
	if st == nil {
		return nil
	}

	ste := new(SendTaskExecutor)
	ste.SendTask = *st

	return ste
}

func (ste *SendTaskExecutor) Exec(ctx context.Context,
	tr *track) ([]*model.SequenceFlow, error) {

	// create the results channel
	// make the call for service

	return nil, nil
}

//-----------------------------------------------------------------------------

type ReceiveTaskExecutor struct {
	model.ReceiveTask
}

func NewReceiveTaskExecutor(rt *model.ReceiveTask) *ReceiveTaskExecutor {
	if rt == nil {
		return nil
	}

	rte := new(ReceiveTaskExecutor)
	rte.ReceiveTask = *rt

	return rte
}

func (rte *ReceiveTaskExecutor) Exec(ctx context.Context,
	tr *track) ([]*model.SequenceFlow, error) {

	return nil, nil
}

//-----------------------------------------------------------------------------
