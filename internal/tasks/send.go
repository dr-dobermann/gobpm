package tasks

// import (
// 	"bytes"
// 	"context"
// 	"encoding/json"
// 	"fmt"

// 	"github.com/dr-dobermann/gobpm/pkg/model"
// 	"github.com/dr-dobermann/gobpm/pkg/thresher/executor"
// 	"github.com/dr-dobermann/gobpm/pkg/variables"
// 	"github.com/dr-dobermann/srvbus/ms"
// 	"github.com/google/uuid"
// 	"go.uber.org/zap"
// )

// // Send task uses concrete or default message queue of the instance
// // to send message intp an in-memory MessageServer.
// type SendTaskExecutor struct {
// 	model.SendTask

// 	exEnv executor.ExecutionEnvironment
// }

// func NewSendTaskExecutor(st *model.SendTask) *SendTaskExecutor {
// 	if st == nil {
// 		return nil
// 	}

// 	return &SendTaskExecutor{SendTask: *st}
// }

// func (ste *SendTaskExecutor) Exec(ctx context.Context,
// 	exEnv executor.ExecutionEnvironment) ([]*model.SequenceFlow, error) {

// 	ste.exEnv = exEnv

// 	var err error

// 	log := ste.exEnv.Logger().Named(ste.Name())
// 	log.Debug("task execution started")
// 	defer log.Debugw("task execution complete",
// 		zap.Error(err))

// 	// compose message to send
// 	msg, err := ste.composeMessage()
// 	if err != nil {
// 		return nil,
// 			fmt.Errorf("couldn't compose message '%s': %v",
// 				ste.MessageName(), err)
// 	}

// 	// send a message
// 	err = ste.sendMsg(msg)
// 	if err != nil {
// 		return nil,
// 			fmt.Errorf("couldn't send message %s: %v",
// 				ste.MessageName(), err)
// 	}

// 	return ste.GetOutputFlows(), nil
// }

// func (ste *SendTaskExecutor) composeMessage() (*model.Message, error) {

// 	msg, err := ste.exEnv.Snapshot().GetMessage(ste.MessageName())
// 	if err != nil {
// 		return nil, fmt.Errorf("couldn't get message '%s': %v",
// 			ste.MessageName(), err)
// 	}

// 	vl := []model.MessageVariable{}

// 	vv := msg.GetVariables(model.AllVariables)
// 	for _, mv := range vv {
// 		v, err := ste.exEnv.VStore().GetVar(mv.Name())
// 		// check existance of variables in
// 		// instance's VarStore
// 		if err != nil {
// 			if !mv.IsOptional() { // non-optional variable couldn't be found
// 				return nil,
// 					fmt.Errorf("couldn't get variable '%s': %v",
// 						mv.Name(), err)
// 			}
// 			// create an optional variable
// 			v = variables.V(mv.Name(), mv.Type(), mv.Value())
// 		}
// 		vl = append(vl, *model.NewMVar(v, mv.IsOptional()))
// 	}

// 	// create a snapshot of process message with
// 	// actual variable's values
// 	nm, err := model.NewMessage(msg.Name(), model.Outgoing, vl...)
// 	if err != nil {
// 		return nil,
// 			fmt.Errorf(
// 				"couldn't create an message '%s' snapshot: %v",
// 				msg.Name(), err)
// 	}

// 	return nm, nil
// }

// func (ste *SendTaskExecutor) sendMsg(msg *model.Message) error {
// 	marshMsg, err := json.Marshal(msg)
// 	if err != nil {
// 		return fmt.Errorf("couldn't marshal message '%s': %v",
// 			msg.Name(), err)
// 	}

// 	queue := ste.exEnv.MSQueue(ste.QueueName())

// 	mSrv, err := ste.exEnv.SrvBus().GetMessageServer()
// 	if err != nil {
// 		return fmt.Errorf("couldn't get message server: %v", err)
// 	}

// 	m, err := ms.NewMsg(uuid.New(),
// 		ste.MessageName(),
// 		bytes.NewBuffer(marshMsg))
// 	if err != nil {
// 		return fmt.Errorf("couldn't create message to send: %v", err)
// 	}

// 	err = mSrv.PutMessages(
// 		uuid.UUID(ste.exEnv.InstanceID()),
// 		queue,
// 		m)
// 	if err != nil {
// 		return fmt.Errorf("couldn't put message '%s' on server: %v",
// 			msg.Name(), err)
// 	}

// 	return nil
// }
