package model

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/base"
	mid "github.com/dr-dobermann/gobpm/pkg/identity"
)

// SendTask represent the Task that sends the message outside the process.
type SendTask struct {
	Activity

	msgName string
	qName   string
}

func (st *SendTask) MessageName() string {
	return st.msgName
}

func (st *SendTask) QueueName() string {
	return st.qName
}

func (st *SendTask) Check() error {

	for _, m := range st.process.messages {
		if m.name == st.msgName && m.direction&Outgoing != 0 {
			return nil
		}
	}

	return NewPMErr(st.ProcessID(), nil,
		"couldn't find outgoing message %s nedeed for task %s",
		st.msgName, st.name)
}

func NewSendTask(p *Process, name, msgName, qName string) *SendTask {
	id := mid.NewID()

	name = strings.Trim(name, " ")
	if name == "" {
		name = "Task " + id.String()
	}

	msgName = strings.Trim(msgName, " ")
	if msgName == "" {
		return nil
	}

	qName = strings.Trim(qName, " ")

	return &SendTask{
		Activity: Activity{
			FlowNode: FlowNode{
				FlowElement: FlowElement{
					NamedElement: NamedElement{
						BaseElement: *base.New(mid.NewID()),
						name:        name},
					elementType: EtActivity},
				process: p},
			aType: AtSendTask,
			class: AcAbstract},
		msgName: msgName,
		qName:   qName}
}

func (st *SendTask) Copy(snapshot *Process) (TaskModel, error) {
	cst := new(SendTask)

	*cst = *st

	cst.SetNewID(mid.NewID())
	cst.process = snapshot

	return cst, nil
}
