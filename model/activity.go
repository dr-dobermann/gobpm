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

	// temporary tasks until DMN realization
	AtStoreTask
	AtCalculateTask
	AtOutputTask
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

func (a Activity) ActivityType() ActivityType {
	return a.aType
}

func (a *Activity) FloatNode() *FlowNode {
	return &a.FlowNode
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

// ------------ Task interfaces ------------------------------------------------
type Task interface {
	Node
	Exec(ctx context.Context, pi *ProcessInstance) error
}

// ------------ Special tasks --------------------------------------------------
// Those tasks are introduced until DMN-engine built
// As soon as DMN will be realized those task will become unneccessary
// -----------------------------------------------------------------------------

// StoreTask stores a bunch of variables into local VarStore of process instance
type StoreTask struct {
	Activity
	vars []VarDefinition
}

func (st *StoreTask) Exec(ctx context.Context, pi *ProcessInstance) error {

	return nil
}

// Calc function provides generic interface to custom fuctions which
// could be expressed out as equation
//               out = F(in)
// where in and out variables mapped to the variables from the Process
// Instance local VarStorage. For a success mapping the variables
// should have identical names and types.
// In case user needs a constant in equation such as x**2,
// variable doesn't have a name, only type and value
// If output variable doesn't have correlated variable in the
// local VarStore, a new variable would be created
type CalcFunc func(
	ctx context.Context,
	pi *ProcessInstance,
	in []VarDefinition,
	out []VarDefinition) error

type CalculateTask struct {
	Activity
	funcs []CalcFunc
}

func (ct *CalculateTask) Exec(ctx context.Context, pi *ProcessInstance) error {

	return nil
}

type OutputTask struct {
	Activity
	vars []VarDefinition
}

func (ot *OutputTask) Exec(ctx context.Context, pi *ProcessInstance) error {
	return nil
}

func (ot *OutputTask) FloatNode() *FlowNode {
	return &ot.FlowNode
}
