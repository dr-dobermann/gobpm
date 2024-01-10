package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

type ActivityClass uint8

const (
	AcAbstract ActivityClass = 1 << iota
	AcLoop
	AcMultiInstance
	AcCompensation
	AcCall
)

type LoopDef struct {
	// loopVar   Variable
	// condition *Expression
	// iteration *Expression
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
	}[at]
}

type activity struct {
	common.FlowNode
	loop *LoopDef

	class ActivityClass
	aType ActivityType

	isForCompensation bool

	resources []common.ResourceRole

	// number of tokens to start the Acitivity
	startQuantity int

	// number of tokens the Activity should generate
	// on completion
	completionQuantity int

	//boundaryEvents []*Event

	ioSpec data.InputOutputSpecification

	// Actitvity performer role name
	// should be a Group name
	// If the Activity belongs to Line, then
	// Line's performerRole is used
	performerRole string

	defaultFlowID identity.Id

	//transaction   *Transaction
}

func (a activity) Class() ActivityClass {
	return a.class
}

func (a activity) TaskType() ActivityType {
	return a.aType
}

func (a activity) DefaultFlowId() identity.Id {
	return a.defaultFlowID
}

func (a activity) Check() error {

	return nil
}

func (a activity) GetIOSpec() data.InputOutputSpecification {

	return a.ioSpec
}

func (a activity) PerformerRole() string {

	return a.performerRole
}

// ------------ TaskModel interfaces --------------------------------------
type TaskModel interface {
	common.Node

	// returns task type
	TaskType() ActivityType

	DefaultFlowId() identity.Id

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
