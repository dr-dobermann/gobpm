package activities

// import (
// 	"strings"

// 	"github.com/dr-dobermann/gobpm/pkg/common"
// 	mid "github.com/dr-dobermann/gobpm/pkg/identity"
// )

// type ReceiveTask struct {
// 	Activity

// 	msgName string

// 	qName string
// }

// func (rt *ReceiveTask) MessageName() string {
// 	return rt.msgName
// }

// func (rt *ReceiveTask) QueueName() string {
// 	return rt.qName
// }

// func (rt *ReceiveTask) Check() error {
// 	for _, m := range rt.process.messages {
// 		if m.Name() == rt.msgName && m.direction&Incoming != 0 {
// 			return nil
// 		}
// 	}

// 	return NewPMErr(rt.ProcessID(), nil,
// 		"couldn't find incoming message %s nedeed for task %s",
// 		rt.msgName, rt.Name())
// }

// func NewReceiveTask(p *Process, name, msgName, qName string) *ReceiveTask {
// 	id := mid.NewID()

// 	name = strings.Trim(name, " ")
// 	if name == "" {
// 		name = "Task " + id.String()
// 	}

// 	rt := new(ReceiveTask)
// 	rt.SetNewID(id)
// 	rt.SetName(name)
// 	rt.process = p
// 	rt.SetType(common.EtActivity)
// 	rt.aType = AtReceiveTask
// 	rt.msgName = msgName
// 	rt.qName = qName

// 	return rt
// }

// func (rt *ReceiveTask) Copy(snapshot *Process) (TaskModel, error) {
// 	crt := new(ReceiveTask)

// 	*crt = *rt

// 	crt.SetNewID(mid.NewID())
// 	crt.process = snapshot

// 	return crt, nil
// }
