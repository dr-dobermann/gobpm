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

	monitor *ctr.Monitor
	audit   *ctr.Audit
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
	e := "<nil>"
	if pme.Err != nil {
		e = pme.Err.Error()
	}
	if pme.processID == Id(uuid.Nil) {
		return "P[ <nil> ] " +
			pme.msg + " : " + e
	}
	return "P[" + pme.processID.String() + "] " +
		pme.msg + " : " + e
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
		l.name = "Line " + l.id.String()
	}

	if p.lanes == nil {
		p.lanes = make(map[string]Lane)
	}

	p.lanes[l.name] = l

	return nil
}

func (p *Process) ListLanes() []string {
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
