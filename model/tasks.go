package model

import (
	"fmt"
	"io"
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
}

func (st *SendTask) GetTaskDefStr() interface{} {
	return st
}

func (st *SendTask) Check() error {

	for _, m := range st.process.messages {
		if m.name == st.msgName && m.direction&MfdOutgoing == MfdOutgoing {
			return nil
		}
	}

	return NewProcessModelError(st.ProcessID(),
		fmt.Sprintf("couldn't find outgoing message %s nedeed for task %s",
			st.msgName, st.name),
		nil)
}

func NewSendTask(p *Process, n string, msgName string) *SendTask {
	id := NewID()

	if n == "" {
		n = "Task " + id.String()
	}

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
		msgName: msgName}
}

func (st *SendTask) Copy(snapshot *Process) TaskDefinition {
	cst := new(SendTask)

	*cst = *st

	cst.id = NewID()
	cst.process = snapshot
	cst.incoming = []*SequenceFlow{}
	cst.outcoming = []*SequenceFlow{}

	return cst
}

//-----------------------------------------------------------------------------

type ReceiveTask struct {
	Activity
	msgName string
}

func (rt *ReceiveTask) GetTaskDefStr() interface{} {
	return rt
}

func (rt *ReceiveTask) Check() error {
	for _, m := range rt.process.messages {
		if m.name == rt.msgName && m.direction&MfdIncoming == MfdIncoming {
			return nil
		}
	}

	return NewProcessModelError(rt.ProcessID(),
		fmt.Sprintf("couldn't find incoming message %s nedeed for task %s",
			rt.msgName, rt.name),
		nil)
}

func NewReceiveTask(p *Process, n string, msgName string) *ReceiveTask {
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

	return rt
}

func (rt *ReceiveTask) Copy(snapshot *Process) TaskDefinition {
	crt := new(ReceiveTask)

	*crt = *rt

	crt.id = NewID()
	crt.process = snapshot
	crt.incoming = []*SequenceFlow{}
	crt.outcoming = []*SequenceFlow{}

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

func (st *StoreTask) GetTaskDefStr() interface{} {
	return st
}

func NewStoreTask(p *Process, n string, vl ...Variable) *StoreTask {

	id := NewID()

	if n == "" {
		n = "Task " + id.String()
	}

	st := StoreTask{
		Activity: Activity{
			FlowNode: FlowNode{
				FlowElement: FlowElement{
					NamedElement: NamedElement{
						BaseElement: BaseElement{
							id: id},
						name: n},
					elementType: EtActivity},
				process: p},
			class: AcAbstract,
			aType: AtStoreTask},
		Vars: []Variable{}}
	st.Vars = append(st.Vars, vl...)

	return &st
}

func (st *StoreTask) Copy(snapshot *Process) TaskDefinition {

	// TODO: refactor to new and then copy from *st
	stc := StoreTask{
		Activity: Activity{
			FlowNode: FlowNode{
				FlowElement: st.FlowElement,
				incoming:    []*SequenceFlow{},
				outcoming:   []*SequenceFlow{}},
			aType: AtStoreTask},
		Vars: make([]Variable, len(st.Vars))}

	stc.process = snapshot
	stc.id = NewID()
	copy(stc.Vars, st.Vars)

	return &stc
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
	Vars        []Variable
}

func (ot *OutputTask) GetTaskDefStr() interface{} {
	return ot
}

func NewOutputTask(p *Process, n string, dest io.Writer, vl ...Variable) *OutputTask {

	id := NewID()

	if n == "" {
		n = "Task " + id.String()
	}

	ot := OutputTask{
		Activity: Activity{
			FlowNode: FlowNode{
				FlowElement: FlowElement{
					NamedElement: NamedElement{
						BaseElement: BaseElement{
							id: id},
						name: n},
					elementType: EtActivity},
				process: p},
			aType: AtOutputTask,
			class: AcAbstract},
		Destination: dest,
		Vars:        []Variable{}}
	ot.Vars = append(ot.Vars, vl...)

	return &ot
}

func (ot *OutputTask) Copy(snapshot *Process) TaskDefinition {

	otc := OutputTask{
		Activity: Activity{
			FlowNode: FlowNode{
				FlowElement: ot.FlowElement,
				incoming:    []*SequenceFlow{},
				outcoming:   []*SequenceFlow{}},
			aType: AtOutputTask},
		Vars: make([]Variable, len(ot.Vars))}

	otc.process = snapshot
	otc.id = NewID()
	copy(otc.Vars, ot.Vars)

	return &otc
}

func (ot *OutputTask) FloatNode() *FlowNode {
	return &ot.FlowNode
}

// ----------------------------------------------------------------------------
