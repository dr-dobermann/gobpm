package model

import (
	"github.com/dr-dobermann/gobpm/pkg/foundation"
	mid "github.com/dr-dobermann/gobpm/pkg/identity"
)

type ActivityClass uint8

const (
	AcAbstract ActivityClass = 1 << iota
	AcLoop
	AcMultiInstance
	AcCompensation
	AcCall
)

// type LoopDef struct {
// 	loopVar   Variable
// 	condition *Expression
// 	iteration *Expression
// }

type ParameterBinding struct {
	foundation.BaseElement
	//parRef Id
	//expr   *Expression
}

type ResourceRole struct {
	foundation.BaseElement
	//assignExpr *Expression // should return Users or Groups resources
	//bindings   []*ParameterBinding
}

type Transaction struct {
}

type CustomTaskInvoker interface {
	Invoke(t TaskModel) error
}

type ActivityType uint8

const (
	AtGenericTask ActivityType = iota
	AtUserTask
	AtReceiveTask
	AtSendTask
	AtServiceTask
	AtBusinessRuleTask
	AtScriptTask
	AtCustomTask // the task with user-defined function

	// temporary tasks until DMN realization
	AtStoreTask
	AtCalculateTask
	AtOutputTask
)

func (at ActivityType) String() string {
	return []string{
		"GenericTask",
		"UserTask",
		"ReceiveTask",
		"SendTask",
		"ServiceTask",
		"BusinessRuleTask",
		"ScriptTask",
		"CustomTask",
		"StoreTask",
		"CalculateTask",
		"OutputTask",
	}[at]
}

type Activity struct {
	FlowNode
	//loop        *LoopDef

	// conditionExpressions on other outgoing Sequence Flows evaluate
	// to true. The default Sequence Flow should not have a
	// conditionExpression. Any such Expression SHALL be ignored

	class ActivityClass
	aType ActivityType

	//boundaryEvents []*Event

	IOSpec InputOutputSpecification

	// not empty in case the Activity used as CallActivity

	//calledElement *CallableElement

	//transaction   *Transaction
}

func (a Activity) Class() ActivityClass {
	return a.class
}

func (a Activity) TaskType() ActivityType {
	return a.aType
}

func (a Activity) DefaultFlowId() mid.Id {
	return a.defaultFlowID
}

func (a Activity) Check() error {

	return nil
}

// ------------ TaskModel interfaces --------------------------------------
type TaskModel interface {
	Node

	// returns task type
	TaskType() ActivityType

	DefaultFlowId() mid.Id

	// Copy returns a copy of the Task with a new Id
	// To prevent errors of duplication flows
	// 'THE COPIED NODE SHOULD HAVE _EMPTY_ INCOMING AND OUTCOMING FLOWS
	Copy(snapshot *Process) (TaskModel, error)

	// Check makes a test if it possible to use the Task in the process.
	// It called in process.AddTask
	Check() error
}

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
// 	render Render // renderer of the UI
// 	actualOwner      string
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
// type GlobalTask struct {
// 	CallableElement
// 	//resources []ResourceRole
// }

// ----------------------------------------------------------------------------
//                                Special tasks
//
// Those tasks are introduced until DMN-engine be built
// As soon as DMN will be realized those task will become unneccessary and might
// be deleted
// -----------------------------------------------------------------------------

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
// type CalcFunc func(
// 	in []Variable,
// 	out []Variable) error

// type CalculateTask struct {
// 	Activity
// 	//	funcs []CalcFunc
// }
