package activities

// import (
// 	"io"
// 	"strings"
// 	"sync"

// 	"github.com/dr-dobermann/gobpm/pkg/common"
// 	mid "github.com/dr-dobermann/gobpm/pkg/identity"
// 	vars "github.com/dr-dobermann/gobpm/pkg/variables"
// )

// type OutputDescr struct {
// 	Locker *sync.Mutex
// 	To     io.Writer
// }

// type OutputTask struct {
// 	Activity

// 	Destination OutputDescr
// 	Vars        []vars.Variable
// }

// func NewOutputTask(
// 	p *Process,
// 	name string,
// 	output OutputDescr,
// 	vl ...vars.Variable) *OutputTask {

// 	id := mid.NewID()

// 	name = strings.Trim(name, " ")
// 	if name == "" {
// 		name = "Task " + id.String()
// 	}

// 	ot := new(OutputTask)

// 	ot.SetNewID(id)
// 	ot.SetName(name)
// 	ot.SetType(common.EtActivity)
// 	ot.process = p
// 	ot.class = AcAbstract
// 	ot.aType = AtOutputTask
// 	ot.Destination = output
// 	ot.Vars = append(ot.Vars, vl...)

// 	return ot
// }

// func (ot *OutputTask) Copy(snapshot *Process) (TaskModel, error) {

// 	otc := new(OutputTask)

// 	*otc = *ot

// 	otc.process = snapshot
// 	otc.SetNewID(mid.NewID())

// 	otc.Vars = make([]vars.Variable, len(ot.Vars))
// 	copy(otc.Vars, ot.Vars)

// 	return otc, nil
// }
