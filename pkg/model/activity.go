package model

import (
	"github.com/dr-dobermann/gobpm/pkg/common"
	"github.com/dr-dobermann/gobpm/pkg/data"
	"github.com/dr-dobermann/gobpm/pkg/expression"
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

type ResourceRole struct {
	foundation.BaseElement

	resurceRef *common.Resource
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

	class ActivityClass
	aType ActivityType

	//boundaryEvents []*Event

	ioSpec *data.InputOutputSpecification

	// Expression could be used to add or manipulate
	// processes variables
	// All expressions are evaluated BEFORE the Activity ends.
	expressions []expression.Expression

	// Actitvity performer role name
	// should be a Group name
	// If the Activity belongs to Line, then
	// Line's performerRole is used
	performerRole string

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

func (a *Activity) GetIOSpec() data.InputOutputSpecification {

	ioSpec := new(data.InputOutputSpecification)

	if a.ioSpec != nil {
		*ioSpec = *a.ioSpec
	}

	return *ioSpec
}

func (a Activity) PerformerRole() string {
	return a.performerRole
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
