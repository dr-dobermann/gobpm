package flow

import (
	"errors"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	// SequenceSource implemented by the Nodes which could be a source of the sequence
	// flow.
	SequenceSource interface {
		FlowNode

		SuportOutgoingFlow(sf *SequenceFlow) error
	}

	// SequenceTarget impmemented by the Nodes which accepts incomng sequence flows.
	SequenceTarget interface {
		FlowNode

		AcceptIncomingFlow(sf *SequenceFlow) error
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
	source SequenceSource

	// The FlowNode that the Sequence Flow is connecting to.
	// For a Process: Of the types of FlowNode, only Activities, Gateways,
	// and Events can be the target. However, Activities that are Event
	// Sub-Processes are not allowed to be a target.
	// For a Choreography: Of the types of FlowNode, only Choreography
	// Activities, Gateways, and Events can be the target.
	target SequenceTarget

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
	//
	// DEV_NOTE: Since goBpm implements only EXECUTABLE processes, this
	// attribute always SHOULD be true.
	// isImmediate bool
}

// NewSequenceFlow creates a new SequenceFlow which connects src and trg
// FlowNodes. On success it returns the new SequenceFlow pointer.
// In case of failrue it returns error.
func NewSequenceFlow(
	name string,
	src SequenceSource,
	trg SequenceTarget,
	condition *data.Expression,
	baseOpts ...options.Option,
) (*SequenceFlow, error) {
	if src == nil || trg == nil {
		return nil,
			errs.New(
				errs.M("sourca and target FlowNodes couldn't be empty"),
				errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	e, err := NewElement(name, baseOpts...)
	if err != nil {
		return nil, err
	}

	sf := SequenceFlow{
		Element:             *e,
		source:              src,
		target:              trg,
		conditionExpression: condition,
	}

	if err := checkConnections(&sf, src, trg); err != nil {
		return nil, err
	}

	// join source and targed with flow
	if err := src.GetNode().addFlow(&sf, data.Output); err != nil {
		return nil, err
	}

	if err := trg.GetNode().addFlow(&sf, data.Input); err != nil {
		if errd := src.GetNode().removeFlow(&sf, data.Output); errd != nil {
			return nil, errors.Join(errd, err)
		}

		return nil, err
	}

	return &sf, nil
}

// checkConnections tests if it possible to connect src with trg via sf.
// If any error found, then error returned.
func checkConnections(sf *SequenceFlow, src SequenceSource, trg SequenceTarget) error {
	// sequence, source and target should belong to the same container
	// or has no container for all of them.
	if (sf.container != nil &&
		(src.GetNode().container != sf.container ||
			trg.GetNode().container != sf.container)) ||
		(sf.container == nil &&
			(src.GetNode().container != nil ||
				trg.GetNode().container != nil)) {
		return errs.New(
			errs.M("sequence flow, source and target should belong to the "+
				"same or nil container"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("flow_container", fmt.Sprint(sf.container)),
			errs.D("source_container", fmt.Sprint(src.GetNode().container)),
			errs.D("target_container", fmt.Sprint(trg.GetNode().container)))
	}

	// check possibility to use sf on source and target ends of the flow
	if err := src.SuportOutgoingFlow(sf); err != nil {
		return err
	}

	if err := trg.AcceptIncomingFlow(sf); err != nil {
		return err
	}

	return nil
}

// Source returns the Source of the SequenceFlow.
func (sf *SequenceFlow) Source() SequenceSource {
	return sf.source
}

// Target returns the Target of the SequenceFlow.
func (sf *SequenceFlow) Target() SequenceTarget {
	return sf.target
}

// Condition returns the condition expression  of the SequenceFlow.
func (sf *SequenceFlow) Condition() *data.Expression {
	return sf.conditionExpression
}
