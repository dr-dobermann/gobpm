package model

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

// SendTask represent the Task that sends the message outside the process.
type SendTask struct {
	Activity
	// message name in process
	mName string
}

//-----------------------------------------------------------------------------

type ReceiveTask struct {
	Activity
	mName string
}

//-----------------------------------------------------------------------------

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

// ------------ Special tasks --------------------------------------------------
// Those tasks are introduced until DMN-engine be built
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
			class: AcAbstract,
			aType: AtStoreTask},
		Vars: []Variable{}}
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
			class: AcAbstract},
		Vars: []Variable{}}
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
