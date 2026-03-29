package flow

import (
	"errors"
	"fmt"

	"golang.org/x/exp/maps"

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
}

// ============================================================================
//                             BaseNode
// ============================================================================

// BaseNode provides base functionality of all Nodes as sequence flows holder.
type BaseNode struct {
	flows map[data.Direction]map[string]*SequenceFlow
	BaseElement
}

// NewBaseNode creates a new BaseNode object.
//
// Available options:
//   - foundation.WithId
//   - foundation.WithDoc
func NewBaseNode(name string, baseOpts ...options.Option) (*BaseNode, error) {
	fe, err := NewBaseElement(name, baseOpts...)
	if err != nil {
		return nil,
			fmt.Errorf("BaseElement building failed: %w", err)
	}

	return &BaseNode{
			BaseElement: *fe,
			flows:       map[data.Direction]map[string]*SequenceFlow{},
		},
		nil
}

// --------------------- Node interface ----------------------------------------

// Incoming returns all the BaseNode's incoming flows.
func (fn *BaseNode) Incoming() []*SequenceFlow {
	return maps.Values(fn.flows[data.Input])
}

// Outgoing returns all the BaseNodes outgoing flows.
func (fn *BaseNode) Outgoing() []*SequenceFlow {
	return maps.Values(fn.flows[data.Output])
}

// AddFlow adds new SequenceFlow to the BaseNode n.
func (fn *BaseNode) AddFlow(sf *SequenceFlow, dir data.Direction) error {
	if sf == nil {
		return errs.New(
			errs.M("couldn't add empty sequence flow"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := dir.Validate(); err != nil {
		return err
	}

	if _, ok := fn.flows[dir]; !ok {
		fn.flows[dir] = map[string]*SequenceFlow{}
	}

	fn.flows[dir][sf.ID()] = sf

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
