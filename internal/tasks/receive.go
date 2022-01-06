package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/gobpm/pkg/excenv"
	"github.com/dr-dobermann/srvbus/ms"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ReceiveTaskExecutor implements a service that reads from in-memory
// Message Server a message with stpcific name.
// Queue name could be specific or default to the process (tr.instance.mQueue)
type ReceiveTaskExecutor struct {
	model.ReceiveTask

	exEnv excenv.ExecutionEnvironment
	log   *zap.SugaredLogger
}

func NewReceiveTaskExecutor(rt *model.ReceiveTask) *ReceiveTaskExecutor {
	if rt == nil {
		return nil
	}

	return &ReceiveTaskExecutor{ReceiveTask: *rt}
}

func (rte *ReceiveTaskExecutor) Exec(
	ctx context.Context,
	exEnv excenv.ExecutionEnvironment) ([]*model.SequenceFlow, error) {

	rte.exEnv = exEnv

	var err error

	rte.log = rte.exEnv.Logger().Named(rte.Name())
	rte.log.Debug("task execution started")
	defer rte.log.Debugw("task execution complete",
		zap.Error(err))

	// get message description from the process instance
	// check message direction.
	// for receiving task it should incoming
	msgDescr, err := rte.getMsgDescr()
	if err != nil {
		return nil,
			fmt.Errorf("couldn't get message description '%s' "+
				"from instance: %v", rte.MessageName(), err)
	}

	// read the messageEnvelope from the MessageServer
	rte.log.Debugw("getting message...",
		zap.String("name", rte.MessageName()))

	mEnv, err := rte.getMessage(ctx,
		rte.MessageName(), rte.QueueName(),
		uuid.UUID(rte.ID()))
	if err != nil {
		return nil,
			fmt.Errorf("couldn't get message '%s' "+
				"from MessageServer: %v", rte.MessageName(), err)
	}

	// load all variables from the message into the instance internal
	// VarStore
	if err = rte.saveMsgVars(msgDescr, mEnv); err != nil {
		return nil, err
	}

	return rte.GetOutputFlows(), nil
}

// takes message description from the instance's process snapshot
//  and check it's direction and state.
func (rte *ReceiveTaskExecutor) getMsgDescr() (*model.Message, error) {
	name := rte.MessageName()

	msgDef, err := rte.exEnv.Snapshot().GetMessage(name)
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
func (rte *ReceiveTaskExecutor) getMessage(ctx context.Context,
	name, queue string,
	receiverID uuid.UUID) (*ms.MessageEnvelope, error) {

	// get a server
	mSrv, err := rte.exEnv.SrvBus().GetMessageServer()
	if err != nil {
		return nil, fmt.Errorf("couldn't get message server: %v", err)
	}

	queue = rte.exEnv.MSQueue(queue)

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
func (rte *ReceiveTaskExecutor) saveMsgVars(
	msgDef *model.Message,
	mEnv *ms.MessageEnvelope) error {

	var err error

	// unmarshall processes message from MessageServer MessageEnvelope
	var msg model.Message

	rte.log.Debug("unmarshalling json")

	err = json.Unmarshal(mEnv.Data(), &msg)
	if err != nil {
		return fmt.Errorf("couldn't get message from the envelope: %v", err)
	}

	for _, v := range msgDef.GetVariables(model.OnlyNonOptional) {
		rte.log.Debugw("loading variable",
			zap.String("name", v.Name()))

		mv, ok := msg.GetVar(v.Name())
		if !ok {
			return fmt.Errorf("no required variable '%s' in the message '%s'",
				v.Name(), msg.Name())
		}

		switch mv.Type() {
		case model.VtInt:
			_, err = rte.exEnv.VStore().NewInt(v.Name(), mv.Int())

		case model.VtBool:
			_, err = rte.exEnv.VStore().NewBool(v.Name(), mv.Bool())

		case model.VtString:
			_, err = rte.exEnv.VStore().NewString(v.Name(), mv.StrVal())

		case model.VtFloat:
			_, err = rte.exEnv.VStore().NewFloat(v.Name(), mv.Float64())

		case model.VtTime:
			_, err = rte.exEnv.VStore().NewTime(v.Name(), mv.Time())
		}

		if err != nil {
			return fmt.Errorf("couldn't add var '%s' of %s "+
				"from message '%s': %v", v.Name(), mv.Type().String(),
				msgDef.Name(), err)
		}
	}

	return nil
}
