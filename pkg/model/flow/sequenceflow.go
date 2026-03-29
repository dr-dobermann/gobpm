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
		Node

		SupportOutgoingFlow(sf *SequenceFlow) error
	}

	// SequenceTarget impmemented by the Nodes which accepts incomng sequence flows.
	SequenceTarget interface {
		Node

		AcceptIncomingFlow(sf *SequenceFlow) error
	}
)

// SequenceFlow is used to show the order of Flow Elements in a Process or
// a Choreography. Each Sequence Flow has only one source and only one target.
// The source and target MUST be from the set of the following Flow Elements:
// Events (Start, Intermediate, and End), Activities (Task and Sub-Process;
// for Processes), Choreography Activities (Choreography Task and
// Sub-Choreography; for Choreographies), and Gateways.
type SequenceFlow struct {
	source              SequenceSource
	target              SequenceTarget
	conditionExpression data.FormalExpression
	BaseElement
}

// Link creates a new sequence flow between two Nodes.
// if source node is in a Container, Link also adds created sequence flow
// inte the same Containier.
// Possible options are:
//   - foundation.WithId
//   - foundation.WithDoc
//   - options.WithName
//   - flow.WithCondition
func Link(
	src SequenceSource,
	trg SequenceTarget,
	flowOptions ...options.Option,
) (*SequenceFlow, error) {
	if src == nil {
		return nil,
			errs.New(
				errs.M("flow source couldn't be empty"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if trg == nil {
		return nil,
			errs.New(
				errs.M("flow target shouldn't be empty"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return newSequenceFlow(src, trg, flowOptions...)
}

// newSequenceFlow creates a new SequenceFlow which connects src and trg
// BaseNodes. On success it returns the new SequenceFlow pointer.
// In case of failrue it returns an error.
func newSequenceFlow(
	src SequenceSource,
	trg SequenceTarget,
	opts ...options.Option,
) (*SequenceFlow, error) {
	fc := sflowConfig{
		src:               src,
		trg:               trg,
		putInSrcContainer: true,
		baseOpts:          []options.Option{},
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

	if fc.putInSrcContainer {
		if fc.src.Container() != nil {
			if err := fc.src.Container().Add(sf); err != nil {
				return nil, err
			}
		}
	}

	return sf, nil
}

// checkConnections tests if it possible to connect src with trg via sf.
func checkConnections(
	sf *SequenceFlow,
	src SequenceSource,
	trg SequenceTarget,
) error {
	if err := sf.Validate(); err != nil {
		return err
	}

	// check possibility to use sf as source of the flow
	if err := src.SupportOutgoingFlow(sf); err != nil {
		return err
	}

	// check possibility to use trg as target of the sf flow.
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
	if err := src.AddFlow(sf, data.Output); err != nil {
		return err
	}

	if err := trg.AddFlow(sf, data.Input); err != nil {
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
		cntr = f.source.Container()
	}

	if (cntr != nil &&
		(f.source.Container() != cntr ||
			f.target.Container() != cntr)) ||
		(cntr == nil &&
			(f.source.Container() != nil ||
				f.target.Container() != nil)) {
		return errs.New(
			errs.M("sequence flow, source and target should belong to the "+
				"same or nil container"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.D("flow_container", getContainerID(cntr)),
			errs.D("source_container",
				getContainerID(f.source.Container())),
			errs.D("target_container",
				getContainerID(f.target.Container())))
	}

	return nil
}

// getContainerId returns the container id if its not nil.
func getContainerID(c Container) string {
	if c == nil {
		return "<nil>"
	}

	return c.ID()
}

// Source returns the Source of the SequenceFlow.
func (f *SequenceFlow) Source() SequenceSource {
	return f.source
}

// Target returns the Target of the SequenceFlow.
func (f *SequenceFlow) Target() SequenceTarget {
	return f.target
}

// Condition returns the condition expression  of the SequenceFlow.
func (f *SequenceFlow) Condition() data.FormalExpression {
	return f.conditionExpression
}

// ------------------------- Element interface ---------------------------------

// EType returns element type of the SequenceFlow.
func (f *SequenceFlow) EType() ElementType {
	return SequenceBaseElement
}

// -----------------------------------------------------------------------------

// interfaces check
var (
	_ Element = (*SequenceFlow)(nil)
)
