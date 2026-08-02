package lanes

import (
	"slices"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Lane is one partition within a LaneSet (BPMN 2.0.2 Table 10.135).
//
// It is carried and never executed. The engine reads a Lane in exactly one
// place — registration-time validation — and nowhere else.
type Lane struct {
	// partitionElement says what this lane partitions by; partitionElementRef is
	// the same thing named by reference. Both are carried VERBATIM and never
	// interpreted: the standard frames them as what "a BPMN compliant tool can
	// determine the FlowElements" with, and this engine determines nothing,
	// because flowNodeRefs states the membership outright.
	partitionElement foundation.Identifyer

	// childLaneSet is the nested partitioning, if any — lanes nest arbitrarily.
	childLaneSet *LaneSet

	// flowNodeRefs are the container's nodes placed on this lane, via Place.
	// One-directional by design: a lane knows its elements, an element never
	// knows its lane.
	flowNodeRefs []flow.Node

	name                string
	partitionElementRef string

	foundation.BaseElement
}

// NewLane creates a Lane. Every parameter after name is optional — pass nil and
// "" for a plain named lane, which is the common case; place its elements
// afterwards with Place.
//
// An empty name is ACCEPTED: BPMN gives Lane.name cardinality 0..1, so refusing
// one would reject a conformant model. That differs deliberately from the
// engine's flow-node constructors, which require a name because they address
// something at run time.
func NewLane(
	name string,
	partitionElement foundation.Identifyer,
	partitionElementRef string,
	childLaneSet *LaneSet,
	baseOpts ...options.Option,
) (*Lane, error) {
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("Lane %q creation failed", name),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &Lane{
			BaseElement:         *be,
			name:                strings.TrimSpace(name),
			partitionElement:    partitionElement,
			partitionElementRef: strings.TrimSpace(partitionElementRef),
			childLaneSet:        childLaneSet,
			flowNodeRefs:        []flow.Node{},
		},
		nil
}

// MustLane creates a Lane or panics — for tests and examples.
func MustLane(
	name string,
	partitionElement foundation.Identifyer,
	partitionElementRef string,
	childLaneSet *LaneSet,
	baseOpts ...options.Option,
) *Lane {
	l, err := NewLane(
		name, partitionElement, partitionElementRef, childLaneSet, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return l
}

// Place puts nodes on the lane.
//
// This is the only way membership is established. A lane is a modeling overlay
// laid over elements: nothing is added to flow.Node, and no element can be asked
// which lane it is on.
//
// Variadic, so a single element or a whole group goes on in one call, and it may
// be called repeatedly while a model is assembled. A nil node is refused. A node
// already on this lane is skipped rather than duplicated, so the call is safe to
// repeat — placing the same group twice is not an error, it is a no-op.
//
// Placement does not check that the nodes belong to the lane's container: a lane
// is typically built before it is added to one. That check runs at registration
// (ValidateLaneSets).
func (l *Lane) Place(nodes ...flow.Node) error {
	placed := make([]flow.Node, 0, len(nodes))

	for _, n := range nodes {
		if n == nil {
			return errs.New(
				errs.M("Lane.Place: a nil node isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed),
				errs.D("lane", l.name))
		}

		if l.holds(n.ID()) {
			continue
		}

		placed = append(placed, n)
	}

	l.flowNodeRefs = append(l.flowNodeRefs, placed...)

	return nil
}

// holds reports whether a node with this id is already on the lane.
func (l *Lane) holds(id string) bool {
	return slices.ContainsFunc(
		l.flowNodeRefs,
		func(n flow.Node) bool { return n.ID() == id })
}

// Name returns the lane's name, which may be empty.
func (l *Lane) Name() string {
	return l.name
}

// PartitionElement returns what the lane partitions by, or nil.
func (l *Lane) PartitionElement() foundation.Identifyer {
	return l.partitionElement
}

// PartitionElementRef returns the referenced partition element's id, or "".
func (l *Lane) PartitionElementRef() string {
	return l.partitionElementRef
}

// ChildLaneSet returns the nested lane set, or nil.
func (l *Lane) ChildLaneSet() *LaneSet {
	return l.childLaneSet
}

// FlowNodes returns a copy of the nodes placed on the lane.
func (l *Lane) FlowNodes() []flow.Node {
	return slices.Clone(l.flowNodeRefs)
}
