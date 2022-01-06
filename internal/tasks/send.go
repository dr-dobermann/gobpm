package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/dr-dobermann/gobpm/model"
	"github.com/dr-dobermann/gobpm/pkg/excenv"
	"github.com/dr-dobermann/srvbus/ms"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

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
	exEnv excenv.ExecutionEnvironment) ([]*model.SequenceFlow, error) {

	var err error

	log := exEnv.Logger().Named(ste.Name())

	log.Debug("task execution started")

	defer log.Debugw("task execution complete",
		zap.Error(err))

	msg, err := exEnv.Snapshot().GetMessage(ste.MessageName())
	if err != nil {
		return nil, fmt.Errorf("couldn't get message '%s': %v",
			ste.MessageName(), err)
	}

	vl := []model.MessageVariable{}
	// check existance of non-optional variables in
	// instance's VarStore
	vv := msg.GetVariables(model.AllVariables)
	for _, mv := range vv {
		v, err := exEnv.VStore().GetVar(mv.Name())
		if err != nil && !mv.IsOptional() { // if non-optional
			return nil,
				fmt.Errorf("couldn't get variable '%s': %v",
					mv.Name(), err)
		}
		vl = append(vl, *model.NewMVar(v, mv.IsOptional()))
	}

	// create a message snapshot to send and marshall it
	nm, err := model.NewMessage(msg.Name(), model.Outgoing, vl...)
	if err != nil {
		return nil, fmt.Errorf(
			"couldn't create an message '%s' snapshot: %v",
			msg.Name(), err)
	}

	marshMsg, err := json.Marshal(nm)
	if err != nil {
		return nil,
			fmt.Errorf("couldn't marshal message '%s': %v",
				msg.Name(), err)
	}

	// send a message
	queue := exEnv.MSQueue(ste.QueueName())

	if ste.mSrv == nil {
		mSrv, err := exEnv.SrvBus().GetMessageServer()
		if err != nil {
			return nil, fmt.Errorf("couldn't get message server: %v", err)
		}
		ste.mSrv = mSrv
	}

	m, err := ms.NewMsg(uuid.New(),
		ste.MessageName(),
		bytes.NewBuffer(marshMsg))
	if err != nil {
		return nil, fmt.Errorf("couldn't create message to send: %v", err)
	}

	err = ste.mSrv.PutMessages(
		uuid.UUID(exEnv.InstanceID()),
		queue,
		m)
	if err != nil {
		return nil,
			fmt.Errorf("couldn't put message '%s' on server: %v",
				msg.Name(), err)
	}

	return ste.GetOutputFlows(), nil
}
