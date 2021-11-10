package thresher

import (
	"context"

	"github.com/dr-dobermann/gobpm/model"
)

func GetTaskExecutor(t model.TaskDefinition) (TaskExecutor, error) {

	var (
		te TaskExecutor
	)
	switch t.TaskType() {
	case model.AtStoreTask:
		st, ok := t.GetTaskDefStr().(model.StoreTask)
		if !ok {
			return nil, NewProcExecError(nil, "cannot get StoreTask struct", nil)
		}
		te = NewStoreTaskExecutor(&st)
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
	tr *track) (TrackState, []*model.SequenceFlow, error) {

	for _, v := range ste.Vars {
		if _, err := tr.instance.vs.NewVar(v); err != nil {
			return TsError,
				nil,
				NewProcExecError(tr, "couldn't add variable %s to instance", err)
		}
	}

	return TsEnded, nil, nil
}
