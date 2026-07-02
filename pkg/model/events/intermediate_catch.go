package events

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

// intermediateCatchTriggers are the event triggers an IntermediateCatchEvent
// may wait for. Message is the SRD-014 deliverable; the others work through the
// same flow.EventNode/waiter path.
var intermediateCatchTriggers = set.New[flow.EventTrigger](
	flow.TriggerConditional,
	flow.TriggerMessage,
	flow.TriggerSignal,
	flow.TriggerTimer,
)

// IntermediateCatchEvent is a mid-flow catch event: it waits for its event
// definition (via the EventHub waiter the track registers) and, on arrival,
// binds any payload into scope before emitting its outgoing flows. For a
// message trigger it is the event-shaped peer of a ReceiveTask (ADR-014 v.1).
type IntermediateCatchEvent struct {
	catchEvent
}

// NewIntermediateCatchEvent builds an intermediate catch event waiting for def.
// A nil def, or a trigger not allowed for an intermediate catch, is rejected.
// For a message definition the message item's payload output is registered, so
// the arrived payload binds into scope on resume.
func NewIntermediateCatchEvent(
	name string,
	def flow.EventDefinition,
	baseOpts ...options.Option,
) (*IntermediateCatchEvent, error) {
	if def == nil {
		return nil,
			errs.New(
				errs.M("an event definition is required for the "+
					"IntermediateCatchEvent"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if !intermediateCatchTriggers.Has(def.Type()) {
		return nil,
			errs.New(
				errs.M("%q trigger isn't allowed for an IntermediateCatchEvent",
					def.Type()),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("event_trigger", string(def.Type())))
	}

	ce, err := newCatchEvent(name, nil,
		[]flow.EventDefinition{def}, false, baseOpts...)
	if err != nil {
		return nil, err
	}

	if med, ok := def.(*MessageEventDefinition); ok {
		ce.addMessagePayloadOutput(med)
	}

	return &IntermediateCatchEvent{catchEvent: *ce}, nil
}

// Node returns the event as a flow.Node.
func (ice *IntermediateCatchEvent) Node() flow.Node {
	return ice
}

// Clone returns a per-instance copy. The captured payload is per-instance
// runtime state and is not carried over.
func (ice *IntermediateCatchEvent) Clone() (flow.Node, error) {
	ce, err := ice.clone()
	if err != nil {
		return nil, err
	}

	return &IntermediateCatchEvent{catchEvent: ce}, nil
}

// EventClass classifies the event as intermediate (a mid-flow wait).
func (ice *IntermediateCatchEvent) EventClass() flow.EventClass {
	return flow.IntermediateEventClass
}

// AcceptIncomingFlow accepts the event's incoming sequence flow.
// Implements flow.SequenceTarget.
func (ice *IntermediateCatchEvent) AcceptIncomingFlow(_ *flow.SequenceFlow) error {
	return nil
}

// SupportOutgoingFlow accepts the event's outgoing sequence flow.
// Implements flow.SequenceSource.
func (ice *IntermediateCatchEvent) SupportOutgoingFlow(_ *flow.SequenceFlow) error {
	return nil
}

// Exec completes the event after it resumed (the payload was captured by
// ProcessEvent and is bound into scope by the inherited UploadData), emitting
// the outgoing sequence flows. Implements exec.NodeExecutor.
func (ice *IntermediateCatchEvent) Exec(
	_ context.Context,
	_ renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	return append([]*flow.SequenceFlow{}, ice.Outgoing()...), nil
}

var (
	_ flow.SequenceSource      = (*IntermediateCatchEvent)(nil)
	_ flow.SequenceTarget      = (*IntermediateCatchEvent)(nil)
	_ flow.Node                = (*IntermediateCatchEvent)(nil)
	_ flow.EventNode           = (*IntermediateCatchEvent)(nil)
	_ exec.NodeExecutor        = (*IntermediateCatchEvent)(nil)
	_ exec.NodeDataProducer    = (*IntermediateCatchEvent)(nil)
	_ eventproc.EventProcessor = (*IntermediateCatchEvent)(nil)
)
