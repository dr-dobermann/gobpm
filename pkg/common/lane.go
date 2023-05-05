package common

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/identity"
)

type Lane struct {
	NamedElement

	nodes map[identity.Id]*FlowNode

	childLaneSet *LaneSet
}

func NewLane(name string) *Lane {
	return &Lane{
		NamedElement: *NewNamedElement(identity.EmptyID(), name),
		nodes:        map[identity.Id]*FlowNode{},
		childLaneSet: nil,
	}
}

func (l *Lane) AddNode(fn *FlowNode) {
	l.nodes[fn.ID()] = fn
}

func (l *Lane) GetNode(id identity.Id) (*FlowNode, error) {
	fn, ok := l.nodes[id]
	if !ok {
		return nil, fmt.Errorf("there is no FlowNode %v on lane %s[%v]",
			id, l.name, l.ID())
	}

	return fn, nil
}

func (l *Lane) GetAllNodes() []*FlowNode {
	fnn := []*FlowNode{}

	for _, fn := range l.nodes {
		fnn = append(fnn, fn)
	}

	return fnn
}

func (l *Lane) RemoveNode(id identity.Id) error {
	if _, ok := l.nodes[id]; !ok {
		return fmt.Errorf("there is no node %v on lane %s[%v]",
			id, l.name, l.ID())
	}

	delete(l.nodes, id)

	return nil
}

func (l *Lane) AddLaneSet(ls *LaneSet) error {
	if l.childLaneSet != nil {
		return fmt.Errorf("there is already child LaneSet on lane %s[%v]",
			l.name, l.ID())
	}

	l.childLaneSet = ls
	ls.parentLane = l

	return nil
}

// ==============================================================================
type LaneSet struct {
	NamedElement

	lanes map[identity.Id]*Lane

	parentLane *Lane
}

func NewLaneSet(name string) *LaneSet {
	return &LaneSet{
		NamedElement: *NewNamedElement(identity.EmptyID(), name),
		lanes:        map[identity.Id]*Lane{},
		parentLane:   nil,
	}
}

func (ls *LaneSet) AddLanes(ll ...*Lane) {
	for _, l := range ll {
		ls.lanes[l.ID()] = l
	}
}

func (ls *LaneSet) RemoveLane(id identity.Id) error {
	_, ok := ls.lanes[id]
	if !ok {
		return fmt.Errorf("there is no lane %v on lane set %s[%v]",
			id, ls.name, ls.ID())
	}

	delete(ls.lanes, id)

	return nil
}

func (ls *LaneSet) GetLane(id identity.Id) (*Lane, error) {
	l, ok := ls.lanes[id]
	if !ok {
		return nil,
			fmt.Errorf("there is no lane %v on lane set %s[%v]",
				id, ls.name, ls.ID())
	}

	return l, nil
}
