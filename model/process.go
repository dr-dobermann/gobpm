package model

import (
	"github.com/dr-dobermann/gobpm/ctr"
	"github.com/google/uuid"
)

type Lane struct {
	FlowElementsContainer
}

type Process struct {
	FlowElementsContainer
	version     string
	supportedBy []string // processes supported this one
	nodes       []Node
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

	return &Process{FlowElementsContainer: FlowElementsContainer{
		FlowElement: FlowElement{
			NamedElement: NamedElement{
				BaseElement: BaseElement{
					id:            pid,
					Documentation: Documentation{"", ""}},
				name: nm},
			elementType: EtProcess},
		containers: make([]interface{}, 0),
		elements:   make([]interface{}, 0)},
		version:  ver,
		nodes:    []Node{},
		flows:    []*SequenceFlow{},
		messages: make([]*Message, 0)}
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
	if len(nm) == 0 {
		return NewProcessModelError(p.id, "couldn't add non-named lane", nil)
	}

	if _, ok := p.lanes[nm]; ok {
		return NewProcessModelError(p.id,
			"Lane ["+nm+"] already exists", nil)
	}

	l := Lane{FlowElementsContainer: FlowElementsContainer{
		FlowElement: FlowElement{
			NamedElement: NamedElement{
				BaseElement: BaseElement{
					id: NewID()},
				name: nm},
			container: p,
		},
	}}

	if len(l.name) == 0 {
		l.name = "Lane " + l.id.String()
	}

	p.lanes[l.name] = l
	if err := p.FlowElementsContainer.InsertElement(&l.FlowElement); err != nil {
		return NewProcessModelError(p.id,
			"couldn't add lane "+nm+" as process elelment", err)
	}

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
	if _, ok := p.lanes[ln]; !ok {
		return NewProcessModelError(p.id, "lane ["+ln+"] isn't found", nil)
	}

	if len(p.Elements(EtLane)) > 0 {
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
		p.FlowElementsContainer.elements = append(p.FlowElementsContainer.elements, &m.FlowElement)
	}

	return m, nil
}
