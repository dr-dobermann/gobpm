package model

import "context"

type TaskClass uint8

const (
	Abstract TaskClass = 1 << iota
	Loop
	MultiInstance
	Compensation
	Call
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

type Activity struct {
	FlowNode
	loop        *LoopDef
	defaultFlow Id // that will receive a token when none of the
	// conditionExpressions on other outgoing Sequence Flows evaluate
	// to true. The default Sequence Flow should not have a
	// conditionExpression. Any such Expression SHALL be ignored
	class          TaskClass
	boundaryEvents []*Event
	data           InputOutputSpecification
	// not empty in case the Activity used as CallActivity
	calledElement *CallableElement
	transaction   *Transaction
}

func (a Activity) Class() TaskClass {
	return a.class
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

type Task interface {
	Run(ctx context.Context) error
}
