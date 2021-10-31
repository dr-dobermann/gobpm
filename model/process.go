package model

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/ctr"
	"github.com/google/uuid"
)

type Lane struct {
	FlowElement
	process *Process
	nodes   []Node
}

func (l *Lane) addNode(n Node) error {

	for _, ln := range l.nodes {
		if ln.FloatNode().id == n.FloatNode().id {
			return NewProcessModelError(l.process.id,
				"Node "+n.FloatNode().name+"already exists on lane "+l.name,
				nil)
		}
	}

	l.nodes = append(l.nodes, n)

	return nil
}

type ProcessInstance struct {
	vs VarStore
}

type Process struct {
	FlowElement
	version     string
	supportedBy []string // processes supported this one
	lanes       map[string]*Lane
	tasks       []Task
	flows       []*SequenceFlow

	messages []*Message

	monitor *ctr.Monitor
	audit   *ctr.Audit
}

func NewProcess(pid Id, nm string, ver string) *Process {
	if pid == Id(uuid.Nil) {
		pid = Id(uuid.New())
	}

	if len(nm) == 0 {
		nm = "Process #" + pid.String()
	}

	if len(ver) == 0 {
		ver = "0.1.0"
	}

	return &Process{FlowElement: FlowElement{
		NamedElement: NamedElement{
			BaseElement: BaseElement{
				id: pid},
			name: nm},
		elementType: EtProcess},
		version:  ver,
		tasks:    []Task{},
		flows:    []*SequenceFlow{},
		messages: make([]*Message, 0),
		lanes:    make(map[string]*Lane)}
}

func (p Process) Version() string {
	return p.version
}

type ProcessModelError struct {
	processID Id
	msg       string
	Err       error
}

func (pme ProcessModelError) Error() string {
	e := ""
	if pme.Err != nil {
		e = " : " + pme.Err.Error()
	}
	if pme.processID == Id(uuid.Nil) {
		return "P[ <nil> ] " +
			pme.msg + e
	}
	return "P[" + pme.processID.String() + "] " +
		pme.msg + e
}

func (pme ProcessModelError) Unwrap() error { return pme.Err }

func NewProcessModelError(pid Id, m string, err error) error {
	return ProcessModelError{pid, m, err}
}

func (p *Process) NewLane(nm string) error {
	if len(nm) > 0 {
		if _, ok := p.lanes[nm]; ok {
			return NewProcessModelError(p.id,
				"Lane ["+nm+"] already exists", nil)
		}
	}

	l := Lane{FlowElement: FlowElement{
		NamedElement: NamedElement{
			BaseElement: BaseElement{
				id: NewID()},
			name: nm},
		elementType: EtLane},
		process: p,
		nodes:   []Node{}}

	if len(nm) == 0 {
		l.name = "Lane " + l.id.String()
	}

	p.lanes[l.name] = &l

	return nil
}

func (p *Process) Lanes() []string {
	ln := []string{}

	for l := range p.lanes {
		ln = append(ln, l)
	}

	return ln
}

func (p *Process) RemoveLane(ln string) error {
	l, ok := p.lanes[ln]
	if l == nil || !ok {
		return NewProcessModelError(p.id, "lane ["+ln+"] isn't found", nil)
	}

	if len(p.lanes[ln].nodes) > 0 {
		return NewProcessModelError(p.id,
			"couldn't remove non-empty lane ["+ln+"]", nil)
	}

	delete(p.lanes, ln)

	return nil
}

func (p *Process) AddMessage(mn string,
	dir MessageFlowDirection, vars ...MessageVariable) (*Message, error) {

	var m *Message

	if len(mn) == 0 {
		return nil, NewProcessModelError(p.id, "couldn't register meassage with an empty name", nil)
	}

	for _, m = range p.messages {
		if m.name == mn {
			return nil, NewProcessModelError(p.id, "message "+mn+" already exists", nil)
		}
	}

	if ms, err := newMessage(p, mn, dir, vars...); err != nil {
		return nil, NewProcessModelError(p.id, "couldn't register message "+mn+" to process", err)
	} else {
		m = ms
		p.messages = append(p.messages, m)
	}

	return m, nil
}

// AddTask adds a new task into the Process Model into lane named ln.
// If t is nil, or ln is the wrong lane name the error would be
// returned.
func (p *Process) AddTask(t Task, ln string) error {
	if t == nil {
		return NewProcessModelError(p.id,
			"сouldn't add nil task or task with an empty name", nil)
	}

	l, ok := p.lanes[ln]
	if !ok {
		return NewProcessModelError(p.id, "cannot find lane "+ln, nil)
	}

	for _, pt := range p.tasks {
		if pt.FloatNode().id == t.FloatNode().id {
			return NewProcessModelError(p.id, "task "+t.FloatNode().name+
				" already exists in the process", nil)
		}
	}
	p.tasks = append(p.tasks, t)
	l.addNode(t)

	t.BindToProcess(p, l.name)

	return nil
}

func (p *Process) LinkNodes(src Node, trg Node, sExpr *Expression) error {

	if src == nil || trg == nil {
		return NewProcessModelError(p.id,
			fmt.Sprintf("trying to link nil-nodes. src: %v, dest: %v", src, trg),
			nil)

	}

	if src.FloatNode().process == nil ||
		src.FloatNode().process.id != p.id {
		return NewProcessModelError(p.id,
			fmt.Sprintf("src isnt't binded to process (%v)",
				src.FloatNode().process),
			nil)
	}

	if trg.FloatNode().process == nil ||
		trg.FloatNode().process.id != p.id {
		return NewProcessModelError(p.id,
			fmt.Sprintf("src isnt't binded to process (%v)",
				trg.FloatNode().process),
			nil)
	}

	sf := &SequenceFlow{
		FlowElement: FlowElement{
			NamedElement: NamedElement{
				BaseElement: BaseElement{
					id: NewID()}}},
		process:   p,
		expr:      sExpr,
		sourceRef: src,
		targetRef: trg}
	p.flows = append(p.flows, sf)

	src.ConnectFlow(sf, SeSource)
	trg.ConnectFlow(sf, SeTarget)

	return nil
}
