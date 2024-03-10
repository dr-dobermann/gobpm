package flow

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	// Sourcer implemented by the Nodes which could be a source of the sequence
	// flow.
	Sourcer interface {
		FlowNode

		AddOutgoing(sf *SequenceFlow) error
	}

	// Targeter impmemented by the Nodes which accepts incomng sequence flows.
	Targeter interface {
		FlowNode

		AddIncoming(sf *SequenceFlow) error
	}
)

// A Sequence Flow is used to show the order of Flow Elements in a Process or
// a Choreography. Each Sequence Flow has only one source and only one target.
// The source and target MUST be from the set of the following Flow Elements:
// Events (Start, Intermediate, and End), Activities (Task and Sub-Process;
// for Processes), Choreography Activities (Choreography Task and
// Sub-Choreography; for Choreographies), and Gateways.
type SequenceFlow struct {
	Element

	// The FlowNode that the Sequence Flow is connecting from.
	// For a Process: Of the types of FlowNode, only Activities,
	// Gateways, and Events can be the source. However, Activities that are
	// Event Sub-Processes are not allowed to be a source.
	// For a Choreography: Of the types of FlowNode, only Choreography
	// Activities, Gateways, and Events can be the source.
	source Sourcer

	// The FlowNode that the Sequence Flow is connecting to.
	// For a Process: Of the types of FlowNode, only Activities, Gateways,
	// and Events can be the target. However, Activities that are Event
	// Sub-Processes are not allowed to be a target.
	// For a Choreography: Of the types of FlowNode, only Choreography
	// Activities, Gateways, and Events can be the target.
	target Targeter

	// An optional boolean Expression that acts as a gating condition. A
	// token will only be placed on this Sequence Flow if this
	// conditionExpression evaluates to true.
	conditionExpression *data.Expression

	// An optional boolean value specifying whether Activities or Choreography
	// Activities not in the model containing the Sequence Flow can occur
	// between the elements connected by the Sequence Flow. If the value is
	// true, they MAY NOT occur. If the value is false, they MAY occur. Also
	// see the isClosed attribute on Process, Choreography, and Collaboration.
	// When the attribute has no value, the default semantics depends on the
	// kind of model containing Sequence Flows:
	//   • For non-executable Processes (public Processes and non-executable
	//     private Processes) and Choreographies no value has the same semantics
	//     as if the value were false.
	//   • For an executable Processes no value has the same semantics as if
	//     the value were true.
	//   • For executable Processes, the attribute MUST NOT be false.
	isImmediate bool
}

// NewSequenceFlow creates a new SequenceFlow which connects src and trg
// FlowNodes. On success it returns the new SequenceFlow pointer.
// In case of failrue it returns error.
func NewSequenceFlow(
	name string,
	src Sourcer,
	trg Targeter,
	condition *data.Expression,
	immediate bool,
	baseOpts ...options.Option,
) (*SequenceFlow, error) {
	if src == nil || trg == nil {
		return nil,
			&errs.ApplicationError{
				Message: "sourca and target FlowNodes couldn't be empty",
				Classes: []string{
					errorClass,
					errs.InvalidParameter}}
	}

	e, err := NewElement(name, baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "couldn't create FlowElement for SequenceFlow",
				Classes: []string{
					errorClass,
					errs.BulidingFailed}}
	}

	sf := SequenceFlow{
		Element:             *e,
		source:              src,
		target:              trg,
		conditionExpression: condition,
		isImmediate:         immediate,
	}

	if err := src.AddOutgoing(&sf); err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "couldn't connect SequenceFlow to source",
				Classes: []string{
					errorClass,
					errs.BulidingFailed}}
	}

	if err := trg.AddIncoming(&sf); err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "couldn't connect SequenceFlow to target",
				Classes: []string{
					errorClass,
					errs.BulidingFailed}}
	}

	src.GetNode().addOutgoing(&sf)
	trg.GetNode().addIncoming(&sf)

	return &sf, nil
}

// Source returns the Source of the SequenceFlow.
func (sf *SequenceFlow) Source() Sourcer {
	return sf.source
}

// Target returns the Target of the SequenceFlow.
func (sf *SequenceFlow) Target() Targeter {
	return sf.target
}

// Condition returns the condition expression  of the SequenceFlow.
func (sf *SequenceFlow) Condition() *data.Expression {
	return sf.conditionExpression
}

// IsImmediate returns the SequenceFlow's immediate setting.
func (sf *SequenceFlow) IsImmediate() bool {
	return sf.isImmediate
}
