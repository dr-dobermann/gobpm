package model

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
	BaseElement
	//parRef Id
	//expr   *Expression
}

type ResourceRole struct {
	BaseElement
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
	defaultFlow Id // that will receive a token when none of the
	// conditionExpressions on other outgoing Sequence Flows evaluate
	// to true. The default Sequence Flow should not have a
	// conditionExpression. Any such Expression SHALL be ignored
	class ActivityClass
	aType ActivityType
	//boundaryEvents []*Event
	//data           InputOutputSpecification
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

func (a Activity) DefaultFlowId() Id {
	return a.defaultFlow
}

func (a Activity) Check() error {

	return nil
}

// ------------ TaskModel interfaces --------------------------------------
type TaskModel interface {
	Node

	// returns task type
	TaskType() ActivityType

	DefaultFlowId() Id

	// Copy returns a copy of the Task with a new Id
	// To prevent errors of duplication flows
	// 'THE COPIED NODE SHOULD HAVE _EMPTY_ INCOMING AND OUTCOMING FLOWS
	Copy(snapshot *Process) TaskModel

	// Check makes a test if it possible to use the Task in the process.
	// It called in process.AddTask
	Check() error
}
