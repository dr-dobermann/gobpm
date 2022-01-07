package model

import "strings"

type ReceiveTask struct {
	Activity

	msgName string

	qName string
}

func (rt *ReceiveTask) MessageName() string {
	return rt.msgName
}

func (rt *ReceiveTask) QueueName() string {
	return rt.qName
}

func (rt *ReceiveTask) Check() error {
	for _, m := range rt.process.messages {
		if m.name == rt.msgName && m.direction&Incoming != 0 {
			return nil
		}
	}

	return NewPMErr(rt.ProcessID(), nil,
		"couldn't find incoming message %s nedeed for task %s",
		rt.msgName, rt.name)
}

func NewReceiveTask(p *Process, name, msgName, qName string) *ReceiveTask {
	id := NewID()

	name = strings.Trim(name, " ")
	if name == "" {
		name = "Task " + id.String()
	}

	rt := new(ReceiveTask)
	rt.id = id
	rt.name = name
	rt.process = p
	rt.elementType = EtActivity
	rt.aType = AtReceiveTask
	rt.msgName = msgName
	rt.qName = qName

	return rt
}

func (rt *ReceiveTask) Copy(snapshot *Process) TaskModel {
	crt := new(ReceiveTask)

	*crt = *rt

	crt.id = NewID()
	crt.process = snapshot

	return crt
}
