package model

import (
	"io"
	"strings"
	"sync"
)

// ----------------------------------------------------------------------------
//                              Standard tasks
// ----------------------------------------------------------------------------
// type GenericTask struct {
// 	Activity
// }

// type ServiceTask struct {
// 	Activity
// 	// could be "##unspecified", "##WebService" or
// 	// URI or coordination protocl
// 	Implementation string
// 	Operation      *Operation // invoked operation
// }

//-----------------------------------------------------------------------------

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

func NewSendTask(p *Process, n string, msgName, qName string) *SendTask {
	id := NewID()

	n = strings.Trim(n, " ")
	if n == "" {
		n = "Task " + id.String()
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
						BaseElement: BaseElement{
							id: id},
						name: n},
					elementType: EtActivity},
				process: p},
			aType: AtSendTask,
			class: AcAbstract},
		msgName: msgName,
		qName:   qName}
}

func (st *SendTask) Copy(snapshot *Process) TaskModel {
	cst := new(SendTask)

	*cst = *st

	cst.id = NewID()
	cst.process = snapshot

	return cst
}

//-----------------------------------------------------------------------------

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

func NewReceiveTask(p *Process, n, msgName, qName string) *ReceiveTask {
	id := NewID()

	if n == "" {
		n = "Task " + id.String()
	}

	rt := new(ReceiveTask)
	rt.id = id
	rt.name = n
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

//-----------------------------------------------------------------------------

// type BusinessRuleTask struct {
// 	Activity
// }

// type ScriptTask struct {
// 	Activity
// 	format string
// 	script string
// }

// type UserTask struct {
// 	Activity
// 	renderings map[string]string
// 	owner      string
// 	priority   int
// }

// ----------------------------------------------------------------------------
//                            Ad-Hoc Sub-Processes
// ----------------------------------------------------------------------------

// type AdHocOrdering uint8

// const (
// 	ParallelOrder AdHocOrdering = iota
// 	SequentalOrder
// )

// type AdHocSubProc struct {
// 	Activity
// 	CompletionCond           *Expression
// 	Order                    AdHocOrdering
// 	CancelRemainingInstances bool
// }

// ----------------------------------------------------------------------------
//                                  Global task
// ----------------------------------------------------------------------------
type GlobalTask struct {
	CallableElement
	//resources []ResourceRole
}

// ----------------------------------------------------------------------------
//                                Special tasks
//
// Those tasks are introduced until DMN-engine be built
// As soon as DMN will be realized those task will become unneccessary and might
// be deleted
// -----------------------------------------------------------------------------

// StoreTask stores a bunch of variables into local VarStore of process instance
type StoreTask struct {
	Activity
	Vars []Variable
}

func NewStoreTask(p *Process, n string, vl ...Variable) *StoreTask {

	id := NewID()

	if n == "" {
		n = "Task " + id.String()
	}

	st := new(StoreTask)
	st.id = id
	st.name = n
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

// ----------------------------------------------------------------------------

// Calc function provides generic interface to custom fuctions which
// could be expressed out as equation
//               out = F(in)
// where in and out variables mapped to the variables from the Process
// Instance local VarStorage. For a success mapping the variables
// should have identical names and types.
// In case user needs a constant in equation such as x**2,
// variable doesn't have a name, only type and value
// If output variable doesn't have correlated variable in the
// local VarStore, a new variable would be created in VarStore
type CalcFunc func(
	in []Variable,
	out []Variable) error

type CalculateTask struct {
	Activity
	//	funcs []CalcFunc
}

// ----------------------------------------------------------------------------
type OutputTask struct {
	Activity

	Destination io.Writer
	DestLocker  *sync.Mutex
	Vars        []Variable
}

func NewOutputTask(p *Process, n string, dest io.Writer,
	locker *sync.Mutex, vl ...Variable) *OutputTask {

	id := NewID()

	if n == "" {
		n = "Task " + id.String()
	}

	ot := new(OutputTask)

	ot.id = id
	ot.name = n
	ot.DestLocker = locker
	ot.elementType = EtActivity
	ot.process = p
	ot.class = AcAbstract
	ot.aType = AtOutputTask
	ot.Destination = dest
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
