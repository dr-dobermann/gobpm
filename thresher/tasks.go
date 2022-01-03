package thresher

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/srvbus/ms"
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

	case *model.OutputTask:
		te = NewOutputTaskExecutor(ts)

	case *model.ReceiveTask:
		te = NewReceiveTaskExecutor(ts)

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

	return &StoreTaskExecutor{*st}
}

func (ste *StoreTaskExecutor) Exec(_ context.Context,
	tr *track) ([]*model.SequenceFlow, error) {

	for _, v := range ste.Vars {
		if _, err := tr.Instance().VarStore().NewVar(v); err != nil {
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

	return &OutputTaskExecutor{*ot}
}

func (ote *OutputTaskExecutor) Exec(_ context.Context,
	tr *track) ([]*model.SequenceFlow, error) {

	for _, v := range ote.Vars {
		vv, err := tr.Instance().VarStore().GetVar(v.Name())
		if err != nil {
			return nil,
				fmt.Errorf(
					"couldn't get a variable `%s`(%s) from "+
						"instance's var storage: %v",
					v.Name(), v.Type().String(), err)
		}

		if ote.DestLocker != nil {
			ote.DestLocker.Lock()
			defer ote.DestLocker.Unlock()
		}

		switch vv.Type() {
		case model.VtInt:
			fmt.Fprintf(ote.Destination, "%s = %d\n", vv.Name(), vv.Int())

		case model.VtBool:
			fmt.Fprintf(ote.Destination, "%s = %t\n", vv.Name(), vv.Bool())

		case model.VtFloat:
			fmt.Fprintf(ote.Destination, "%s = %f\n", vv.Name(), vv.Float64())

		case model.VtTime:
			fmt.Fprintf(ote.Destination, "%s = %v\n", vv.Name(), vv.Time())

		default:
			return nil, fmt.Errorf("invalid var type %s", vv.Type().String())
		}
	}

	return ote.GetOutputFlows(), nil
}

//-----------------------------------------------------------------------------

// Send task uses default message queue of the instance to send messages.
type SendTaskExecutor struct {
	model.SendTask

	mSrv *ms.MessageServer
}

func NewSendTaskExecutor(st *model.SendTask) *SendTaskExecutor {
	if st == nil {
		return nil
	}

	return &SendTaskExecutor{*st, nil}
}

func (ste *SendTaskExecutor) Exec(ctx context.Context,
	tr *track) ([]*model.SequenceFlow, error) {

	// if ste.mSrv == nil {
	// 	mSrv, err := tr.instance.Thr.SrvBus().GetMessageServer()
	// 	if err != nil {
	// 		return nil, fmt.Errorf("couldn't get message server: %v", err)
	// 	}

	// }

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

	return &ReceiveTaskExecutor{*rt}
}

func (rte *ReceiveTaskExecutor) Exec(ctx context.Context,
	tr *track) ([]*model.SequenceFlow, error) {

	return nil, nil
}

//-----------------------------------------------------------------------------
