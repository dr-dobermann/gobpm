package flow

import (
	"errors"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	// SequenceSource implemented by the Nodes which could be a source of the sequence
	// flow.
	SequenceSource interface {
		FlowNode

		SuportOutgoingFlow(sf *SequenceFlow) error

		Link(
			trg SequenceTarget,
			flowOptions ...options.Option,
		) (*SequenceFlow, error)
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

// ------------------------- FlowElement interface -----------------------------

// ElementType returns element type of the SequenceFlow.
func (f *SequenceFlow) ElementType() ElementType {
	return SequenceFlowElement
}

// GetElement returns underlaying Element.
func (f *SequenceFlow) GetElement() *Element {
	return &f.Element
}

// -----------------------------------------------------------------------------

// newSequenceFlow creates a new SequenceFlow which connects src and trg
// FlowNodes. On success it returns the new SequenceFlow pointer.
// In case of failrue it returns an error.
func NewSequenceFlow(
	src SequenceSource,
	trg SequenceTarget,
	opts ...options.Option,
) (*SequenceFlow, error) {
	fc := sflowConfig{
		name:     "",
		src:      src,
		trg:      trg,
		baseOpts: []options.Option{},
	}

	ee := []error{}

	for _, opt := range opts {
		switch o := opt.(type) {
		case options.NameOption, sflowOption:
			if err := o.Apply(&fc); err != nil {
				ee = append(ee, err)
			}

		case foundation.BaseOption:
			fc.baseOpts = append(fc.baseOpts, o)

		default:
			ee = append(ee,
				errs.New(
					errs.M("invalid option for SequenceFlow: %s",
						reflect.TypeOf(o).String()),
					errs.C(errorClass, errs.TypeCastingError)))
		}
	}

	if len(ee) != 0 {
		return nil, errors.Join(ee...)
	}

	sf, err := fc.newSequenceFlow()
	if err != nil {
		return nil, err
	}

	if err := connect(sf, src, trg); err != nil {
		return nil, err
	}

	return sf, nil
}

// checkConnections tests if it possible to connect src with trg via sf.
// If any error found, then error returned.
func checkConnections(
	sf *SequenceFlow,
	src SequenceSource,
	trg SequenceTarget,
) error {
	if err := sf.Validate(); err != nil {
		return err
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

// connect check connection and, on succes, connects src with trg through sf.
func connect(sf *SequenceFlow, src SequenceSource, trg SequenceTarget) error {
	if err := checkConnections(sf, src, trg); err != nil {
		return err
	}

	// join source and targed with flow
	if err := src.GetNode().addFlow(sf, data.Output); err != nil {
		return err
	}

	if err := trg.GetNode().addFlow(sf, data.Input); err != nil {
		if errd := src.GetNode().removeFlow(sf, data.Output); errd != nil {
			return errors.Join(errd, err)
		}

		return err
	}

	return nil
}

// Validate checks if the sequence flow and its ends belongs to the same
// container.
func (f *SequenceFlow) Validate() error {
	// sequence, source and target should belong to the same container
	// or has no container for all of them.
	cntr := f.container

	// ignore empty flow container if its not set yet.
	if cntr == nil {
		cntr = f.source.GetNode().container
	}

	if (cntr != nil &&
		(f.source.GetNode().container != cntr ||
			f.target.GetNode().container != cntr)) ||
		(cntr == nil &&
			(f.source.GetNode().container != nil ||
				f.target.GetNode().container != nil)) {
		return errs.New(
			errs.M("sequence flow, source and target should belong to the "+
				"same or nil container"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("flow_container", getContainerId(cntr)),
			errs.D("source_container",
				getContainerId(f.source.GetNode().container)),
			errs.D("target_container",
				getContainerId(f.target.GetNode().container)))
	}

	return nil
}

// getContainerId returns the container id if its not nil.
func getContainerId(c *ElementsContainer) string {
	if c == nil {
		return "<nil>"
	}

	return c.Id()
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
