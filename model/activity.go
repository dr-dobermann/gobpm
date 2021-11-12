package model

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
	Invoke(t TaskDefinition) error
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

	// temporary tasks until DMN realization
	AtStoreTask
	AtCalculateTask
	AtOutputTask
)

func (at ActivityType) String() string {
	return []string{
		"GenericTask",
		"UserTask",
		"RecieveTask",
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
}

func (a Activity) Class() ActivityClass {
	return a.class
}

func (a Activity) TaskType() ActivityType {
	return a.aType
}

func (a Activity) DefaultFlowId() Id {
	return a.defaultFlow
}

// ------------------ Standard tasks -------------------------------------------
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

// ------------- Ad-Hoc Sub-Processes -----------------------------------------
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

// ------------ Global task ----------------------------------------------------
type GlobalTask struct {
	CallableElement
	resources []ResourceRole
}

// ------------ TaskDefinition interfaces --------------------------------------
type TaskDefinition interface {
	Node
	TaskType() ActivityType
	GetTaskDefStr() interface{}
	DefaultFlowId() Id
	// Copy returns a copy of the Task with a new Id
	Copy(snapshot *Process) TaskDefinition
}
