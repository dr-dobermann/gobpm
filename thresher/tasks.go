package thresher

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/model"
)

func GetTaskExecutor(t model.TaskDefinition) (TaskExecutor, error) {
	var te TaskExecutor

	switch t.TaskType() {
	case model.AtStoreTask:
		st, ok := t.GetTaskDefStr().(*model.StoreTask)
		if !ok {
			return nil, NewProcExecError(nil, "cannot get StoreTask struct", nil)
		}
		te = NewStoreTaskExecutor(st)

	case model.AtOutputTask:
		ot, ok := t.GetTaskDefStr().(*model.OutputTask)
		if !ok {
			return nil, NewProcExecError(nil, "cannot get OutputTask struct", nil)
		}
		te = NewOutputTaskExecutor(ot)

	default:
		return nil, NewProcExecError(nil,
			fmt.Sprintf("The task type %s doesn't have an Executor", t.TaskType().String()),
			nil)
	}

	return te, nil
}

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
	tr *track) (StepState, []*model.SequenceFlow, error) {

	for _, v := range ste.Vars {
		if _, err := tr.instance.vs.NewVar(v); err != nil {
			return SsFailed,
				nil,
				NewProcExecError(tr, "couldn't add variable %s to instance", err)
		}
	}

	// TODO: Add expression check on output flows
	return SsEnded, ste.GetOutputFlows(), nil
}

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
	tr *track) (StepState, []*model.SequenceFlow, error) {

	for _, ov := range ote.Vars {
		v, err := tr.instance.vs.GetVar(ov.Name())
		if err != nil {
			return SsFailed, nil,
				NewProcExecError(tr,
					fmt.Sprintf("couldn't print variable %s : %v",
						ov.Name(), err),
					err)
		}
		fmt.Printf("[%s]%s %s(%s) = %v\n",
			tr.instance.id,
			tr.instance.snapshot.Name(),
			v.Name(), v.Type().String(), v.Value())
	}

	// TODO: Add expression check on output flows
	return SsEnded, ote.GetOutputFlows(), nil
}
