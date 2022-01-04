package thresher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/srvbus/ms"
	"github.com/google/uuid"
	"go.uber.org/zap"
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

// Send task uses concrete or default message queue of the instance
// to send messages.
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

	var err error

	log := tr.log.Named(ste.Name())

	log.Debug("task execution started")

	defer func() {
		log.Debugw("task execution complete",
			zap.Error(err))
	}()

	msg, err := tr.instance.snapshot.GetMessage(ste.MessageName())
	if err != nil {
		return nil, NewPEErr(tr, err,
			"couldn't get message '%s'",
			ste.MessageName())
	}

	vl := []model.MessageVariable{}
	// check existance of non-optional variables in
	// instance's VarStore
	vv, vo := msg.GetVariables(model.AllVariables)
	for i, mv := range vv {
		v, err := tr.Instance().vs.GetVar(mv.Name())
		if err != nil && !vo[i] { // if non-optional
			return nil, NewPEErr(tr, err,
				"couldn't get variable '%s'", mv.Name())
		}
		vl = append(vl, *model.NewMVar(v, vo[i]))
	}

	// create a message snapshot to send and marshall it
	nm, err := model.NewMessage(msg.Name(), model.Outgoing, vl...)
	if err != nil {
		return nil, NewPEErr(tr, err,
			"couldn't create an message '%s' snapshot", msg.Name())
	}

	marshMsg, err := json.Marshal(nm)
	if err != nil {
		return nil, NewPEErr(tr, err,
			"couldn't marshal message '%s'", msg.Name())
	}

	// send a message
	queue := ste.QueueName()

	if queue != "" {
		queue = tr.instance.mQueue
	}

	if ste.mSrv == nil {
		mSrv, err := tr.instance.Thr.SrvBus().GetMessageServer()
		if err != nil {
			return nil, NewPEErr(tr, err,
				"couldn't get message server")
		}
		ste.mSrv = mSrv
	}

	m, err := ms.NewMsg(uuid.New(),
		"MSG_"+msg.ID().String(),
		bytes.NewBuffer(marshMsg))
	if err != nil {
		return nil, NewPEErr(tr, err,
			"couldn't create message to send")
	}

	err = ste.mSrv.PutMessages(
		uuid.UUID(tr.instance.id),
		queue,
		m)
	if err != nil {
		return nil, NewPEErr(tr, err,
			"couldn't put message '%s' on server", msg.Name())
	}

	return ste.GetOutputFlows(), nil
}

//-----------------------------------------------------------------------------

type ReceiveTaskExecutor struct {
	model.ReceiveTask

	mSrv *ms.MessageServer
}

func NewReceiveTaskExecutor(rt *model.ReceiveTask) *ReceiveTaskExecutor {
	if rt == nil {
		return nil
	}

	return &ReceiveTaskExecutor{ReceiveTask: *rt, mSrv: nil}
}

func (rte *ReceiveTaskExecutor) Exec(ctx context.Context,
	tr *track) ([]*model.SequenceFlow, error) {

	// get a server
	if rte.mSrv == nil {
		mSrv, err := tr.instance.Thr.SrvBus().GetMessageServer()
		if err != nil {
			return nil, NewPEErr(tr, err,
				"couldn't get message server")
		}
		rte.mSrv = mSrv
	}

	// read the message
	queue := rte.QueueName()
	if queue == "" {
		queue = tr.instance.mQueue
	}

	// unmarshall it

	// load all variables from the message into the instance internal
	// VarStore

	return nil, errs.ErrNotImplementedYet
}

//-----------------------------------------------------------------------------
