package model

import (
	"github.com/dr-dobermann/gobpm/ctr"
	"github.com/google/uuid"
)

type Lane struct {
	NamedElement
	elements []*FlowElement
}

type Process struct {
	FlowElementsContainer
	version     string
	supportedBy []string // processes supported this one
	lanes       map[string]Lane
	nodes       []Node
	flows       []*SequenceFlow

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
		containers: make([]*FlowElementsContainer, 0),
		elements:   make([]*FlowElement, 0)},
		version: ver,
		lanes:   make(map[string]Lane),
		nodes:   []Node{},
		flows:   []*SequenceFlow{}}
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
	if _, ok := p.lanes[nm]; ok {
		return NewProcessModelError(p.id,
			"Lane ["+nm+"] already exists", nil)
	}

	l := Lane{NamedElement: NamedElement{
		BaseElement: BaseElement{id: Id(uuid.New()),
			Documentation: Documentation{"", ""}},
		name: nm}}

	if len(l.name) == 0 {
		l.name = "Lane " + l.id.String()
	}

	p.lanes[l.name] = l

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

	if len(p.lanes[ln].elements) > 0 {
		return NewProcessModelError(p.id,
			"couldn't remove non-empty lane ["+ln+"]", nil)
	}

	delete(p.lanes, ln)

	return nil
}
