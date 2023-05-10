package tasks

// import (
// 	"context"
// 	"fmt"

// 	"github.com/dr-dobermann/gobpm/pkg/model"
// 	"github.com/dr-dobermann/gobpm/pkg/thresher/executor"
// 	"go.uber.org/zap"
// )

// // StoreTaskExecutor save a list of variables inte the internal VarStore of
// // the instance.
// type StoreTaskExecutor struct {
// 	model.StoreTask
// }

// func NewStoreTaskExecutor(st *model.StoreTask) *StoreTaskExecutor {
// 	if st == nil {
// 		return nil
// 	}

// 	return &StoreTaskExecutor{*st}
// }

// func (ste *StoreTaskExecutor) Exec(_ context.Context,
// 	exEnv executor.ExecutionEnvironment) ([]*model.SequenceFlow, error) {

// 	var err error

// 	log := exEnv.Logger().Named(ste.Name())

// 	log.Debug("task execution started")

// 	defer log.Debugw("task execution complete",
// 		zap.Error(err))

// 	for _, v := range ste.Vars {
// 		log.Debugw("storing variable",
// 			zap.String("var_name", v.Name()))

// 		if _, err := exEnv.VStore().NewVar(v); err != nil {
// 			return nil,
// 				fmt.Errorf(
// 					"couldn't add variable %s to instance: %v",
// 					v.Name(), err)
// 		}
// 	}

// 	return ste.GetOutputFlows(), nil
// }
