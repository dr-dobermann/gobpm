package flow

import (
	"errors"
	"fmt"
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// NodeType represents different types of BPMN nodes.
type NodeType string

const (
	// InvalidNodeType represents an invalid node type.
	InvalidNodeType NodeType = "INVALID_NODE_TYPE"
	// ActivityNodeType represents an activity node type.
	ActivityNodeType NodeType = "Activity"
	// EventNodeType represents an event node type.
	EventNodeType NodeType = "Event"
	// GatewayNodeType represents a gateway node type.
	GatewayNodeType NodeType = "Gateway"
)

// Validate checks if nt has NodeType value.
func (nt NodeType) Validate() error {
	if nt != ActivityNodeType &&
		nt != EventNodeType &&
		nt != GatewayNodeType {
		return errs.New(
			errs.M("invalid NodeType: %q", nt),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return nil
}

// ValidateNodeTypes checks list of NodeTypes on validity.
func ValidateNodeTypes(types ...NodeType) error {
	ee := []error{}

	for _, t := range types {
		if err := t.Validate(); err != nil {
			ee = append(ee, err)
		}
	}

	if len(ee) != 0 {
		return errors.Join(ee...)
	}

	return nil
}

// Node element is used to provide a single element as the source and
// target Sequence Flow associations instead of the individual associations of
// the elements that can connect to Sequence Flows.
// Only the Gateway, Activity, Choreography Activity, and Event elements can
// connect to Sequence Flows and thus, these elements are the only ones that
// are sub-classes of BaseNode.
type Node interface {
	Element

	// This attribute identifies the incoming Sequence Flow of the BaseNode.
	// incoming map[string]*SequenceFlow

	// This attribute identifies the outgoing Sequence Flow of the BaseNode.
	// This is an ordered collection.
	// outgoing map[string]*SequenceFlow

	// flows holds both incoming and outgoing flows of the Node.
	Incoming() []*SequenceFlow
	Outgoing() []*SequenceFlow

	AddFlow(*SequenceFlow, data.Direction) error

	NodeType() NodeType

	// Node returns underlying node object.
	Node() Node

	// Clone returns a per-instance copy of the Node: immutable configuration is
	// shared by reference, per-instance runtime state is fresh, the flow
	// collections are empty (rewired between clones afterwards) and the clone
	// carries no container back-reference. Clone returns an error because
	// copying a node's properties can fail — a value-less property is
	// unclonable (FIX-017); a node without properties never errors.
	Clone() (Node, error)
}

// ============================================================================
//                             BaseNode
// ============================================================================

// BaseNode provides base functionality of all Nodes as sequence flows holder.
//
// flows are held per direction in a declaration-ordered slice, so
// Incoming()/Outgoing() return them in the order they were added — the gateway
// routing rules (Exclusive first-true §2.8, Inclusive subset §2.9) depend on that
// stable order (BPMN §13.4.2).
type BaseNode struct {
	flows map[data.Direction][]*SequenceFlow
	BaseElement
}

// NewBaseNode creates a new BaseNode object.
//
// Available options:
//   - foundation.WithID
//   - foundation.WithDoc
func NewBaseNode(name string, baseOpts ...options.Option) (*BaseNode, error) {
	fe, err := NewBaseElement(name, baseOpts...)
	if err != nil {
		return nil,
			fmt.Errorf("BaseElement building failed: %w", err)
	}

	return &BaseNode{
			BaseElement: *fe,
			flows:       map[data.Direction][]*SequenceFlow{},
		},
		nil
}

// CloneShell returns a new BaseNode that copies the identity (id/name/docs) of
// fn through the embedded BaseElement but starts with a fresh empty flow map and
// no container back-reference. It is the per-instance shell every concrete node
// type builds its clone on top of. flows is unexported, so this helper lives in
// package flow.
func (fn *BaseNode) CloneShell() BaseNode {
	return BaseNode{
		BaseElement: fn.cloneIdentity(),
		flows:       map[data.Direction][]*SequenceFlow{},
	}
}

// --------------------- Node interface ----------------------------------------

// Incoming returns the BaseNode's incoming flows in declaration order.
func (fn *BaseNode) Incoming() []*SequenceFlow {
	return fn.ordered(data.Input)
}

// Outgoing returns the BaseNode's outgoing flows in declaration order.
func (fn *BaseNode) Outgoing() []*SequenceFlow {
	return fn.ordered(data.Output)
}

// ordered returns a copy of the direction's flows, already in the order they
// were added — the gateway first-true/subset rules rely on this being stable
// (BPMN §13.4.2). The copy keeps the internal slice unexposed.
func (fn *BaseNode) ordered(dir data.Direction) []*SequenceFlow {
	return slices.Clone(fn.flows[dir])
}

// AddFlow adds a SequenceFlow to the BaseNode in direction dir, appending it in
// declaration order. Re-adding the same flow id overwrites in place (keeping its
// position) rather than duplicating it.
func (fn *BaseNode) AddFlow(sf *SequenceFlow, dir data.Direction) error {
	if sf == nil {
		return errs.New(
			errs.M("couldn't add empty sequence flow"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := dir.Validate(); err != nil {
		return err
	}

	for i, f := range fn.flows[dir] {
		if f.ID() == sf.ID() {
			fn.flows[dir][i] = sf

			return nil
		}
	}

	fn.flows[dir] = append(fn.flows[dir], sf)

	return nil
}

// Node returns underlaying node structure.
func (fn *BaseNode) Node() Node {
	errs.Panic("don't use Node from generic BaseNode")

	return nil
}

// NodeType returns the Node's type.
func (fn *BaseNode) NodeType() NodeType {
	errs.Panic("don't use NodeType from generic BaseNode")

	return InvalidNodeType
}

// Clone panics for the generic BaseNode: each concrete node type implements its
// own Clone. The shell-clone helper CloneShell is used by those implementations.
func (fn *BaseNode) Clone() (Node, error) {
	errs.Panic("don't use Clone from generic BaseNode")

	return nil, nil
}

// ----------------- Element interface -----------------------------------------

// EType returns ElementType of the BaseNode.
func (fn *BaseNode) EType() ElementType {
	return NodeElement
}

// -----------------------------------------------------------------------------
// interfaces check

var (
	_ Node    = (*BaseNode)(nil)
	_ Element = (*BaseElement)(nil)
)
