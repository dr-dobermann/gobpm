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

	// variables should be in the instance's VarStore
	// _before_ Task starts execution
	input []Variable
	// variables should be in the instance's VarStore
	// _after_ Tasks finishes execution
	output []Variable
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

// ------------ Special tasks --------------------------------------------------
// Those tasks are introduced until DMN-engine built
// As soon as DMN will be realized those task will become unneccessary
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
			class:  AcAbstract,
			aType:  AtStoreTask,
			output: []Variable{}},
		Vars: []Variable{}}
	st.output = append(st.output, vl...)
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
	funcs []CalcFunc
}

type OutputTask struct {
	Activity
	Vars []Variable
}

func (ot *OutputTask) GetTaskDefStr() interface{} {
	return ot
}

func NewOutputTask(p *Process, n string, vl ...Variable) *OutputTask {

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
			class: AcAbstract,
			input: []Variable{},
		},
		Vars: []Variable{}}
	ot.input = append(ot.input, vl...)
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
