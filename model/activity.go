package model

import "context"

type ActivityClass uint8

const (
	AcAbstract ActivityClass = 1 << iota
	AcLoop
	AcMultiInstance
	AcCompensation
	AcCall
)

type LoopDef struct {
	loopVar   Variable
	condition *Expression
	iteration *Expression
}

type ParameterBinding struct {
	BaseElement
	parRef Id
	expr   *Expression
}

type ResourceRole struct {
	BaseElement
	assignExpr *Expression // should return Users or Groups resources
	bindings   []*ParameterBinding
}

type Transaction struct {
}

type CustomTaskInvoker interface {
	Invoke(pi *ProcessInstance) error
}

type ActivityType uint8

const (
	AtGenericTask ActivityType = iota
	AtUserTask
	AtRecieveTask
	AtSendTask
	AtServiceTask
	AtBusinessRuleTask
	AtScriptTask
	AtCustomTask // the task with user-defined function
)

type Activity struct {
	FlowNode
	loop        *LoopDef
	defaultFlow Id // that will receive a token when none of the
	// conditionExpressions on other outgoing Sequence Flows evaluate
	// to true. The default Sequence Flow should not have a
	// conditionExpression. Any such Expression SHALL be ignored
	class          ActivityClass
	aType          ActivityType
	boundaryEvents []*Event
	data           InputOutputSpecification
	// not empty in case the Activity used as CallActivity
	calledElement *CallableElement
	transaction   *Transaction

	// variables should be in the instance's VarStore
	// _before_ Task starts execution
	input []VarDefinition
	// variables should be in the instance's VarStore
	// _after_ Tasks finishes execution
	output []VarDefinition
}

func (a Activity) Class() ActivityClass {
	return a.class
}

func (a Activity) Type() ActivityType {
	return a.aType
}

type GenericTask struct {
	Activity
}

type ServiceTask struct {
	Activity
	// could be "##unspecified", "##WebService" or
	// URI or coordination protocl
	Implementation string
	Operation      *Operation // invoked operation
}

type SendTask struct {
	Activity
	message   Id
	operation *Operation
}

type ReceiveTask struct {
	Activity
	message   Id
	operation *Operation
}

type BusinessRuleTask struct {
	Activity
}

type ScriptTask struct {
	Activity
	format string
	script string
}

type UserTask struct {
	Activity
	renderings map[string]string
	owner      string
	priority   int
}

// ------------- Sub-Processes ------------------------------
type AdHocOrdering uint8

const (
	ParallelOrder AdHocOrdering = iota
	SequentalOrder
)

type AdHocSubProc struct {
	Activity
	CompletionCond           *Expression
	Order                    AdHocOrdering
	CancelRemainingInstances bool
}

type GlobalTask struct {
	CallableElement
	resources []ResourceRole
}

type Task interface {
	Exec(ctx context.Context, pi *ProcessInstance) error
}
