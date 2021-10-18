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

type GlobalTask struct {
	CallableElement
	resources []ResourceRole
}

func (p *Process) NewLine(nm string) error {
	if _, ok := p.lanes[nm]; ok {
		return NewModelError(uuid.Nil,
			"Lane ["+nm+"] already exists", nil)
	}

	l := Lane{NamedElement: NamedElement{
		BaseElement: BaseElement{id: Id(uuid.New()),
			Documentation: Documentation{"", ""}},
		name: nm}}

	if len(l.name) == 0 {
		l.name = "Line " + l.id.String()
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
		return NewModelError(uuid.Nil, "lane ["+ln+"] isn't found", nil)
	}

	if len(p.lanes[ln].elements) > 0 {
		return NewModelError(uuid.Nil,
			"couldn't remove non-empty lane ["+ln+"]", nil)
	}

	delete(p.lanes, ln)

	return nil
}
