package model

import (
	"io"
	"strings"
	"sync"
)

type OutputDescr struct {
	Locker *sync.Mutex
	To     io.Writer
}

type OutputTask struct {
	Activity

	Destination OutputDescr
	Vars        []Variable
}

func NewOutputTask(
	p *Process,
	name string,
	output OutputDescr,
	vl ...Variable) *OutputTask {

	id := NewID()

	name = strings.Trim(name, " ")
	if name == "" {
		name = "Task " + id.String()
	}

	ot := new(OutputTask)

	ot.id = id
	ot.name = name
	ot.elementType = EtActivity
	ot.process = p
	ot.class = AcAbstract
	ot.aType = AtOutputTask
	ot.Destination = output
	ot.Vars = append(ot.Vars, vl...)

	return ot
}

func (ot *OutputTask) Copy(snapshot *Process) TaskModel {

	otc := new(OutputTask)

	*otc = *ot

	otc.process = snapshot
	otc.id = NewID()

	otc.Vars = make([]Variable, len(ot.Vars))
	copy(otc.Vars, ot.Vars)

	return otc
}

func (ot *OutputTask) FloatNode() *FlowNode {
	return &ot.FlowNode
}
