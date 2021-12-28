package model

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

var ErrSnapshotChange = errors.New("couldn't change process snapshot data")

type ProcessDataType uint8

const (
	PdtModel    ProcessDataType = iota
	PdtSnapshot                 // TODO: Add restiction on updating snapshot
)

type Process struct {
	FlowElement
	version string

	// supportedBy []string // processes supported this one
	lanes map[string]*Lane

	// processes tasks
	tasks []TaskModel

	// processes gateways
	// gates []Gateways

	// processes events
	// events []Events

	flows []*SequenceFlow

	// the type of process data. could be a real Model or
	// Snapshot of the model.
	// Snapshot is used as real-time model for process
	// execution
	dataType ProcessDataType

	// process messages
	messages []*Message
}

// returns a version of the process.
func (p Process) Version() string {
	return p.version
}

// GetNodes returns a list of Nodes in the Process p.
// Only Task, Gateway or Event could be returned.
// If et is EtUnspecified then all three types of Nodes
// will be returned in the same list.
// If et is differs from EtAcitvity, EtGateway, EtEvent,
// the panic will be fired
func (p *Process) GetNodes(et FlowElementType) ([]Node, error) {

	nn := []Node{}

	switch et {
	// return all nodes
	case EtUnspecified:
		for _, t := range p.tasks {
			nn = append(nn, t)
		}

	case EtActivity:
		for _, t := range p.tasks {
			nn = append(nn, t)
		}

	case EtGateway:

	case EtEvent:

	default:
		return nil,
			NewPMErr(p.id, nil, "wrong element type for node [%s]",
				et.String())
	}

	return nn, nil
}

// returns a task by its ID.
//
// if there is no such task, nil will be returned.
func (p *Process) GetTask(tid Id) TaskModel {
	for _, t := range p.tasks {
		if t.ID() == tid {
			return t
		}
	}

	return nil
}

// GetMessage returns a *Message from the Process p.
// If there is no Message with name mn, the error will be returned
func (p *Process) GetMessage(mn string) (*Message, error) {
	for _, m := range p.messages {
		if m.name == mn {
			return m, nil
		}
	}

	return nil,
		NewPMErr(
			p.id,
			nil, "couldn't find message '%s' in process",
			mn)
}

// creates a copy from a model process.
// copy could not be made from a copy (snapshot) of the process.
func (p Process) Copy() (*Process, error) {
	if p.dataType == PdtSnapshot {
		return nil, NewPMErr(p.id, nil, "couldn't make a copy of snapshot")
	}

	pc := Process{
		FlowElement: p.FlowElement,
		lanes:       make(map[string]*Lane),
		tasks:       make([]TaskModel, 0),
		flows:       make([]*SequenceFlow, 0)}

	// copy lanes
	for l := range p.lanes {
		pc.NewLane(l)
	}

	// tm used as a mapper from source process task id to
	// a copy process task id in LinkNodes calls below
	tm := make(map[Id]TaskModel)

	// copy tasks and place them on lanes
	for _, ot := range p.tasks {
		t := ot.Copy(&pc)

		tm[ot.ID()] = t

		pc.AddTask(t, ot.LaneName())
	}

	// copy sequence flows
	for _, of := range p.flows {
		var e *Expression

		if of.expr != nil {
			e = of.expr.Copy()
		}

		if err := pc.LinkNodes(
			tm[of.sourceRef.ID()],
			tm[of.targetRef.ID()], e); err != nil {

			return nil,
				NewPMErr(p.id, err, "couldn't link nodes in snapshot")
		}
	}

	pc.dataType = PdtSnapshot

	return &pc, nil
}

// creates a new process
func NewProcess(pid Id, nm string, ver string) *Process {
	if pid == Id(uuid.Nil) {
		pid = NewID()
	}

	nm = strings.Trim(nm, " ")
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
		tasks:    []TaskModel{},
		flows:    []*SequenceFlow{},
		messages: make([]*Message, 0),
		lanes:    make(map[string]*Lane)}
}

// adds new lane to the process
func (p *Process) NewLane(nm string) error {
	if p.dataType == PdtSnapshot {
		return ErrSnapshotChange
	}

	nm = strings.Trim(nm, " ")
	if len(nm) > 0 {
		if _, ok := p.lanes[nm]; ok {
			return NewPMErr(p.id, nil,
				"Lane '%s' already exists", nm)
		}
	}

	l := new(Lane)
	l.id = NewID()
	l.name = nm
	l.elementType = EtLane
	l.process = p
	l.nodes = make([]Node, 0)

	if len(nm) == 0 {
		l.name = "Lane " + l.id.String()
	}

	p.lanes[l.name] = l

	return nil
}

// returns a slice of process lanes
func (p *Process) Lanes() []string {
	ln := []string{}

	for l := range p.lanes {
		ln = append(ln, l)
	}

	return ln
}

// remove lane from the process
func (p *Process) RemoveLane(ln string) error {
	if p.dataType == PdtSnapshot {
		return ErrSnapshotChange
	}

	ln = strings.Trim(ln, " ")

	l, ok := p.lanes[ln]
	if l == nil || !ok {
		return NewPMErr(p.id, nil, "lane '%s' isn't found", ln)
	}

	if len(p.lanes[ln].nodes) > 0 {
		return NewPMErr(p.id, nil,
			"couldn't remove non-empty lane '%s'", ln)
	}

	delete(p.lanes, ln)

	return nil
}

// register a single non-empty, non-duplicating message
// in the process.
func (p *Process) AddMessage(mn string,
	dir MessageFlowDirection, vars ...MessageVariable) (*Message, error) {

	if p.dataType == PdtSnapshot {
		return nil, ErrSnapshotChange
	}

	var m *Message

	mn = strings.Trim(mn, " ")
	if len(mn) == 0 {
		return nil, NewPMErr(p.id, nil,
			"couldn't register meassage with an empty name")
	}

	for _, m = range p.messages {
		if m.name == mn {
			return nil, NewPMErr(p.id, nil,
				"message '%s' already exists", mn)
		}
	}

	if ms, err := newMessage(mn, dir, vars...); err != nil {
		return nil,
			NewPMErr(p.id, err,
				"couldn't register message '%s' to process", mn)
	} else {
		m = ms

		p.messages = append(p.messages, m)
	}

	return m, nil
}

// AddTask adds a new task into the Process Model into lane named ln.
// If t is nil, or ln is the wrong lane name the error would be
// returned.
func (p *Process) AddTask(t TaskModel, ln string) error {
	if p.dataType == PdtSnapshot {
		return ErrSnapshotChange
	}

	if t == nil {
		return NewPMErr(p.id, nil,
			"сouldn't add nil task or task with an empty name")
	}

	l, ok := p.lanes[ln]
	if !ok {
		return NewPMErr(p.id, nil, "lane '%s' is not found", ln)
	}

	for _, pt := range p.tasks {
		if pt.ID() == t.ID() {
			return NewPMErr(p.id, nil, "task '%s' "+
				"already exists in the process", t.Name())
		}
	}

	if err := t.Check(); err != nil {
		return NewPMErr(p.id, err,
			"task %s doesn't pass self-check", t.Name())
	}

	p.tasks = append(p.tasks, t)
	l.addNode(t)

	return t.PutOnLane(l)
}

// links two Nodes by one SequenceFlow.
//
// expr added as Expression on SequenceFlow
func (p *Process) LinkNodes(src Node, trg Node, expr *Expression) error {
	if p.dataType == PdtSnapshot {
		return ErrSnapshotChange
	}

	if src == nil || trg == nil {
		return NewPMErr(p.id, nil,
			"trying to link nil-nodes. src: %v, dest: %v", src, trg)
	}

	if src.ProcessID() != p.id ||
		trg.ProcessID() != p.id {
		return NewPMErr(p.id, nil,
			"nodes isnt't binded to process src.pID[%v], trg.pID[%v]",
			src.ProcessID(), trg.ProcessID())
	}

	sf := &SequenceFlow{
		FlowElement: FlowElement{
			NamedElement: NamedElement{
				BaseElement: BaseElement{
					id: NewID()}}},
		process:   p,
		expr:      expr,
		sourceRef: src,
		targetRef: trg}

	if err := src.ConnectFlow(sf, SeSource); err != nil {
		return NewPMErr(p.id, err,
			"couldn't connect sequence flow [%v] to node '%s' as source",
			sf.ID(), src.Name())
	}

	if err := trg.ConnectFlow(sf, SeTarget); err != nil {
		return NewPMErr(p.id, err,
			"couldn't connect sequence flow [%v] to node '%s' as target",
			sf.ID(), trg.Name())
	}

	p.flows = append(p.flows, sf)

	return nil
}
