package thresher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

// returns a given message queue name if it's not empty or
// a default message queue name for the instance
func getQueueName(tr *Track, queue string) string {
	if queue == "" {
		return tr.instance.mQueue
	}

	return queue
}

//-----------------------------------------------------------------------------
//                           Special Tasks
//-----------------------------------------------------------------------------

// StoreTaskExecutor save a list of variables inte the internal VarStore of
// the instance.
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
	tr *Track) ([]*model.SequenceFlow, error) {

	var err error

	log := tr.Logger().Named(ste.Name())

	log.Debug("task execution started")

	defer log.Debugw("task execution complete",
		zap.Error(err))

	for _, v := range ste.Vars {
		log.Debugw("storing variable", zap.String("var_name", v.Name()))

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

// OutputTaskExecutors prints a list of instance internal variable onto
// given io.Writer.
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
	tr *Track) ([]*model.SequenceFlow, error) {

	var err error

	log := tr.log.Named(ote.Name())

	log.Debug("task execution started")

	defer log.Debugw("task execution complete",
		zap.Error(err))

	for _, v := range ote.Vars {
		log.Debugw("output vairable", zap.String("var_name", v.Name()))

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
// to send message intp an in-memory MessageServer.
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
	tr *Track) ([]*model.SequenceFlow, error) {

	var err error

	log := tr.log.Named(ste.Name())

	log.Debug("task execution started")

	defer log.Debugw("task execution complete",
		zap.Error(err))

	msg, err := tr.instance.snapshot.GetMessage(ste.MessageName())
	if err != nil {
		return nil, NewPEErr(tr, err,
			"couldn't get message '%s'",
			ste.MessageName())
	}

	vl := []model.MessageVariable{}
	// check existance of non-optional variables in
	// instance's VarStore
	vv := msg.GetVariables(model.AllVariables)
	for _, mv := range vv {
		v, err := tr.Instance().vs.GetVar(mv.Name())
		if err != nil && !mv.IsOptional() { // if non-optional
			return nil, NewPEErr(tr, err,
				"couldn't get variable '%s'", mv.Name())
		}
		vl = append(vl, *model.NewMVar(v, mv.IsOptional()))
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
		ste.MessageName(),
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

// ReceiveTaskExecutor implements a service that reads from in-memory
// Message Server a message with stpcific name.
// Queue name could be specific or default to the process (tr.instance.mQueue)
type ReceiveTaskExecutor struct {
	model.ReceiveTask
}

func NewReceiveTaskExecutor(rt *model.ReceiveTask) *ReceiveTaskExecutor {
	if rt == nil {
		return nil
	}

	return &ReceiveTaskExecutor{ReceiveTask: *rt}
}

func (rte *ReceiveTaskExecutor) Exec(ctx context.Context,
	tr *Track) ([]*model.SequenceFlow, error) {

	var err error

	log := tr.log.Named(rte.Name())
	log.Debug("task execution started")
	defer log.Debugw("task execution complete",
		zap.Error(err))

	// get message description from the process instance
	// check message direction.
	// for receiving task it should incoming
	msgDef, err := getMsgDescr(tr, rte)
	if err != nil {
		return nil,
			NewPEErr(tr, err,
				"couldn't get message description '%s' from instance",
				rte.MessageName())
	}

	// read the messageEnvelope from the MessageServer
	log.Debugw("getting message...",
		zap.String("name", rte.MessageName()))

	mEnv, err := getMessage(ctx,
		rte.MessageName(), rte.QueueName(),
		tr, uuid.UUID(rte.ID()))
	if err != nil {
		return nil, NewPEErr(tr, err,
			"couldn't get message '%s' from MessageServer", rte.MessageName())
	}

	// unmarshall processes message from MessageServer MessageEnvelope
	var msg model.Message

	log.Debug("unmarshalling json")

	err = json.Unmarshal(mEnv.Data(), &msg)
	if err != nil {
		return nil,
			fmt.Errorf("couldn't get message from the envelope: %v", err)
	}

	// load all variables from the message into the instance internal
	// VarStore
	if err = saveMsgVars(msgDef, log, &msg, tr); err != nil {
		return nil, err
	}

	return rte.GetOutputFlows(), nil
}

// takes message description from the instance's process snapshot
//  and check it's direction and state.
func getMsgDescr(tr *Track, rte *ReceiveTaskExecutor) (*model.Message, error) {
	msgDef, err := tr.instance.snapshot.GetMessage(rte.MessageName())
	if err != nil {
		return nil, err
	}

	if msgDef.Direction()&model.Incoming == 0 {
		return nil, errors.New("invalid message direction")
	}

	if msgDef.State() != model.Created {
		return nil, errors.New("invalidd message state: should be Created")
	}
	return msgDef, nil
}

// reads single message from MessageServer queue
func getMessage(ctx context.Context,
	name, queue string,
	tr *Track,
	receiverID uuid.UUID) (*ms.MessageEnvelope, error) {

	// get a server
	mSrv, err := tr.instance.Thr.SrvBus().GetMessageServer()
	if err != nil {
		return nil, fmt.Errorf("couldn't get message server: %v", err)
	}

	queue = getQueueName(tr, queue)

	meCh, err := mSrv.GetMessages(receiverID, queue, true)
	if err != nil {
		return nil, err
	}

	var mEnv ms.MessageEnvelope

	quit := false
	for !quit {
		var ok bool

		select {

		// if context cancelled, return context error
		case <-ctx.Done():
			err = ctx.Err()
			return nil, err

		case mEnv, ok = <-meCh:
			// if channel closed, return error
			if !ok {
				err = fmt.Errorf("message '%s' is not found", name)
				return nil, err
			}

			// read until message name is not equal with
			// task's message name
			if mEnv.Name == name {
				quit = true

				// read rest of the messages from the channel to
				// allow MessageServer to close it,
				go func() {
					for {
						select {
						case <-ctx.Done():
							return

						case _, ok := <-meCh:
							if !ok {
								return
							}
						}
					}
				}()
			}
		}
	}

	return &mEnv, nil
}

// saved readed message's variables into the instance's VarStore
// according to processes message description
func saveMsgVars(msgDef *model.Message,
	log *zap.SugaredLogger,
	msg *model.Message,
	tr *Track) error {

	var err error

	for _, v := range msgDef.GetVariables(model.OnlyNonOptional) {
		log.Debugw("loading variable",
			zap.String("name", v.Name()))

		mv, ok := msg.GetVar(v.Name())
		if !ok {
			return fmt.Errorf("no required variable '%s' in the message '%s'",
				v.Name(), msg.Name())
		}

		switch mv.Type() {
		case model.VtInt:
			_, err = tr.instance.vs.NewInt(v.Name(), mv.Int())

		case model.VtBool:
			_, err = tr.instance.vs.NewBool(v.Name(), mv.Bool())

		case model.VtString:
			_, err = tr.instance.vs.NewString(v.Name(), mv.StrVal())

		case model.VtFloat:
			_, err = tr.instance.vs.NewFloat(v.Name(), mv.Float64())

		case model.VtTime:
			_, err = tr.instance.vs.NewTime(v.Name(), mv.Time())
		}

		if err != nil {
			return fmt.Errorf("couldn't add var '%s' of %s "+
				"from message '%s': %v", v.Name(), mv.Type().String(),
				msgDef.Name(), err)
		}
	}

	return nil
}

//-----------------------------------------------------------------------------
