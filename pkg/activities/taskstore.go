package activities

// import (
// 	"strings"

// 	"github.com/dr-dobermann/gobpm/pkg/common"
// 	mid "github.com/dr-dobermann/gobpm/pkg/identity"
// 	vars "github.com/dr-dobermann/gobpm/pkg/variables"
// )

// // StoreTask stores a bunch of variables into local VarStore of process instance
// type StoreTask struct {
// 	Activity
// 	Vars []vars.Variable
// }

// func NewStoreTask(p *Process, name string, vl ...vars.Variable) *StoreTask {

// 	id := mid.NewID()

// 	name = strings.Trim(name, " ")
// 	if name == "" {
// 		name = "Task " + id.String()
// 	}

// 	st := new(StoreTask)
// 	st.SetNewID(id)
// 	st.SetName(name)
// 	st.SetType(common.EtActivity)
// 	st.process = p
// 	st.class = AcAbstract
// 	st.aType = AtStoreTask
// 	st.Vars = append(st.Vars, vl...)

// 	return st
// }

// func (st *StoreTask) Copy(snapshot *Process) (TaskModel, error) {

// 	stc := new(StoreTask)

// 	*stc = *st
// 	stc.process = snapshot
// 	stc.SetNewID(mid.NewID())

// 	stc.Vars = make([]vars.Variable, len(st.Vars))
// 	copy(stc.Vars, st.Vars)

// 	return stc, nil
// }
