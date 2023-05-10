package common

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/identity"
)

type Lane struct {
	NamedElement

	nodes map[identity.Id]*FlowNode

	childLaneSet *LaneSet

	// id of the copyed lane
	// for the new one it's EmpytID()
	sourceLaneID identity.Id
}

func NewLane(id identity.Id, name string) *Lane {
	return &Lane{
		NamedElement: *NewNamedElement(id, name),
		nodes:        map[identity.Id]*FlowNode{},
		childLaneSet: nil,
		sourceLaneID: identity.EmptyID(),
	}
}

func (l *Lane) SourceID() identity.Id {
	return l.sourceLaneID
}

// AddNode adds node on the lane if it doesn't have
// child LaneSet
func (l *Lane) AddNode(fn *FlowNode) {
	if l.childLaneSet != nil {
		panic(fmt.Sprintf("lane %s[%v] couldn't add node since it has a child lineSet",
			l.name, l.ID()))
	}

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
		return NewModelError(l.name, l.ID(),
			nil, "there is no node %v on lane", id)
	}

	delete(l.nodes, id)

	return nil
}

// AddLaneSet adds child LaneSet on lane
// if it doesn't have another child LaneSet
// or nodes.
func (l *Lane) AddLaneSet(ls *LaneSet) error {
	if l.childLaneSet != nil {
		return NewModelError(l.name, l.ID(),
			nil, "there is already child LaneSet on lane")
	}

	if len(l.nodes) > 0 {
		return NewModelError(l.name, l.ID(),
			nil, "there are %d nodes on lane", len(l.nodes))
	}

	// check for recursion
	if ls.hasLane(l.ID()) {
		return NewModelError(l.name, l.ID(), nil,
			"cyclic line -> lineSet %s[%v]", ls.name, ls.ID())
	}

	l.childLaneSet = ls
	ls.parentLane = l

	return nil
}

// copy creates a line copy with child LaneSets but
// with no nodes. It gives new ID's and keeps names.
func (l *Lane) copy() *Lane {
	cl := Lane{
		NamedElement: *NewNamedElement(identity.EmptyID(), l.name),
		nodes:        map[identity.Id]*FlowNode{},
		childLaneSet: nil,
		sourceLaneID: l.ID(),
	}

	if l.childLaneSet != nil {
		cls := l.childLaneSet.Copy()
		cl.AddLaneSet(cls)
	}

	return &cl
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

func (ls *LaneSet) GetAllLanes(recursive bool) []*Lane {
	ll := []*Lane{}

	for _, l := range ls.lanes {
		ll = append(ll, l)
		if recursive && l.childLaneSet != nil {
			ll = append(ll, l.childLaneSet.GetAllLanes(recursive)...)
		}
	}

	return ll
}

func (ls *LaneSet) hasLane(id identity.Id) bool {
	for _, l := range ls.lanes {
		if l.ID() == id {
			return true
		}
		if l.childLaneSet != nil {
			if l.childLaneSet.hasLane(id) {
				return true
			}
		}
	}

	return false
}

func (ls *LaneSet) Copy() *LaneSet {
	c := LaneSet{
		NamedElement: *NewNamedElement(identity.EmptyID(), ls.name),
		lanes:        map[identity.Id]*Lane{},
		parentLane:   nil,
	}

	for _, l := range ls.lanes {
		cl := l.copy()
		c.lanes[cl.ID()] = cl
	}

	return &c
}
