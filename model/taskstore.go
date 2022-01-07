package model

import "strings"

// StoreTask stores a bunch of variables into local VarStore of process instance
type StoreTask struct {
	Activity
	Vars []Variable
}

func NewStoreTask(p *Process, name string, vl ...Variable) *StoreTask {

	id := NewID()

	name = strings.Trim(name, " ")
	if name == "" {
		name = "Task " + id.String()
	}

	st := new(StoreTask)
	st.id = id
	st.name = name
	st.elementType = EtActivity
	st.process = p
	st.class = AcAbstract
	st.aType = AtStoreTask
	st.Vars = append(st.Vars, vl...)

	return st
}

func (st *StoreTask) Copy(snapshot *Process) TaskModel {

	stc := new(StoreTask)

	*stc = *st
	stc.process = snapshot
	stc.id = NewID()

	stc.Vars = make([]Variable, len(st.Vars))
	copy(stc.Vars, st.Vars)

	return stc
}
