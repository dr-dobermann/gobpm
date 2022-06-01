package tasks

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/thresher/executor"
	"github.com/dr-dobermann/gobpm/pkg/variables"
	"go.uber.org/zap"
)

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

func (ote *OutputTaskExecutor) Exec(
	_ context.Context,
	exEnv executor.ExecutionEnvironment) ([]*model.SequenceFlow, error) {

	var err error

	log := exEnv.Logger().Named(ote.Name())

	log.Debug("task execution started")

	defer log.Debugw("task execution complete",
		zap.Error(err))

	for _, v := range ote.Vars {
		log.Debugw("output vairable", zap.String("var_name", v.Name()))

		vv, err := exEnv.VStore().GetVar(v.Name())
		if err != nil {
			return nil,
				fmt.Errorf(
					"couldn't get a variable `%s`(%s) from "+
						"instance's var storage: %v",
					v.Name(), v.Type().String(), err)
		}

		if ote.Destination.Locker != nil {
			ote.Destination.Locker.Lock()
			defer ote.Destination.Locker.Unlock()
		}

		switch vv.Type() {
		case variables.Int:
			fmt.Fprintf(ote.Destination.To, "%s = %d\n", vv.Name(), vv.Int())

		case variables.Bool:
			fmt.Fprintf(ote.Destination.To, "%s = %t\n", vv.Name(), vv.Bool())

		case variables.Float:
			fmt.Fprintf(ote.Destination.To, "%s = %f\n", vv.Name(), vv.Float64())

		case variables.Time:
			fmt.Fprintf(ote.Destination.To, "%s = %v\n", vv.Name(), vv.Time())

		default:
			return nil, fmt.Errorf("invalid var type %s", vv.Type().String())
		}
	}

	return ote.GetOutputFlows(), nil
}
