package model

type TaskClass uint8

const (
<<<<<<< HEAD
	Abstract TaskClass = 1 << iota
	Loop
	MultiInstance
	Compensation
	Call
=======
	Normal TaskClass = iota
	Loop
	MultiInstance
	Compensation
>>>>>>> cd1bb6ab4d496deef6cc2b2baa563bdcafa033d0
)

type LoopDef struct {
	loopVar   Variable
	condition *Expression
	iteration *Expression
}

type ParameterBinding struct {
	BaseElement
	parRef id
	expr   *Expression
}

type ResourceRole struct {
	BaseElement
	assignExpr *Expression // should return Users or Groups resources
	bindings   []*ParameterBinding
}

type Activity struct {
	FlowElement
	forCompensation bool
	loop            *LoopDef
	defaultFlow     id // that will receive a token when none of the
	// conditionExpressions on other outgoing Sequence Flows evaluate
	// to true. The default Sequence Flow should not have a
	// conditionExpression. Any such Expression SHALL be ignored
	boundaryEvents []*Event
	data           InputOutputSpecification
	startQuantity  uint8
	complQuantity  uint8
}

<<<<<<< HEAD
type CallActivity struct {
	Activity
	calledElementRef *CallableElement
}

=======
>>>>>>> cd1bb6ab4d496deef6cc2b2baa563bdcafa033d0
type ServiceTask struct {
	Activity
	// could be "##unspecified", "##WebService" or
	// URI or coordination protocl
	implementation string
	operation      *Operation // invoked operation
}

type SendTask struct {
	Activity
	implementation string
	message        id
	operation      *Operation
}

type ReceiveTask struct {
	Activity
	implementation string
	message        id
	operation      *Operation
}

type BusinessRuleTask struct {
	Activity
	implementation string
}

type ScriptTask struct {
	Activity
	format string
	script string
}

type UserTask struct {
	Activity
	implementation string
	renderings     map[string]string
	owner          string
	priority       int
}
<<<<<<< HEAD

// ------------- Sub-Processes ------------------------------

type TransactionalSubProc struct {
	Activity
	// TODO: should be changed to method used to commit or cancel
	// Transaction.
	method string
}

type AdHocOrdering uint8

const (
	ParallelOrder AdHocOrdering = iota
	SequentalOrder
)

type AdHocSubProc struct {
	Activity
	completionCond           *Expression
	order                    AdHocOrdering
	cancelRemainingInstances bool
}
=======
>>>>>>> cd1bb6ab4d496deef6cc2b2baa563bdcafa033d0
