package events

import (
	"context"
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

// intermediateThrowTriggers are the event triggers an IntermediateThrowEvent
// may emit. Message is the SRD-014 deliverable (published to the broker); the
// others propagate through the internal event bus.
var intermediateThrowTriggers = set.New(
	flow.TriggerCompensation,
	flow.TriggerEscalation,
	flow.TriggerMessage,
	flow.TriggerSignal,
	flow.TriggerLink,
)

// IntermediateThrowEvent is a mid-flow throw event: on execution it emits its
// event definition (publishing a message to the broker, or propagating any
// other kind internally) and continues. For a message trigger it is the
// event-shaped peer of a SendTask (ADR-014 v.1).
type IntermediateThrowEvent struct {
	// linkTarget is the resolved target catch node for a Link throw, set by the
	// graph wiring (flow.resolveLinkEdges) at snapshot build / per-instance
	// clone. A Link throw's Exec redirects the token to its outgoing flows
	// (ADR-006 v.4 §2.8); nil for every non-Link throw.
	linkTarget flow.Node

	throwEvent
}

// NewIntermediateThrowEvent builds an intermediate throw event for def. A nil
// def, or a trigger not allowed for an intermediate throw, is rejected.
func NewIntermediateThrowEvent(
	name string,
	def flow.EventDefinition,
	baseOpts ...options.Option,
) (*IntermediateThrowEvent, error) {
	if def == nil {
		return nil,
			errs.New(
				errs.M("an event definition is required for the "+
					"IntermediateThrowEvent"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if !intermediateThrowTriggers.Has(def.Type()) {
		return nil,
			errs.New(
				errs.M("%q trigger isn't allowed for an IntermediateThrowEvent",
					def.Type()),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("event_trigger", string(def.Type())))
	}

	te, err := newThrowEvent(name, nil, []flow.EventDefinition{def}, baseOpts...)
	if err != nil {
		return nil, err
	}

	return &IntermediateThrowEvent{throwEvent: *te}, nil
}

// MessageToSend returns the message when the event throws a message, or nil for
// a non-message throw. Implements msgflow.MessageProducer.
func (ite *IntermediateThrowEvent) MessageToSend() *bpmncommon.Message {
	for _, d := range ite.definitions {
		if med, ok := d.(*MessageEventDefinition); ok {
			return med.Message()
		}
	}

	return nil
}

// Node returns the event as a flow.Node.
func (ite *IntermediateThrowEvent) Node() flow.Node {
	return ite
}

// LinkName returns the Link pairing name when this throw carries a Link
// definition, or "" otherwise. Implements flow.LinkEventNode.
func (ite *IntermediateThrowEvent) LinkName() string {
	name, _ := linkDefName(ite)

	return name
}

// IsLinkSource reports that an intermediate throw is a Link source (the redirect
// origin). Implements flow.LinkEventNode.
func (*IntermediateThrowEvent) IsLinkSource() bool { return true }

// SetLinkTarget records the resolved target catch node — set by the graph
// wiring (flow.resolveLinkEdges). Implements flow.LinkSource.
func (ite *IntermediateThrowEvent) SetLinkTarget(target flow.Node) {
	ite.linkTarget = target
}

// Clone returns a per-instance copy of the event.
func (ite *IntermediateThrowEvent) Clone() (flow.Node, error) {
	te, err := ite.clone()
	if err != nil {
		return nil, err
	}

	return &IntermediateThrowEvent{throwEvent: te}, nil
}

// EventClass classifies the event as intermediate.
func (ite *IntermediateThrowEvent) EventClass() flow.EventClass {
	return flow.IntermediateEventClass
}

// AcceptIncomingFlow accepts the event's incoming sequence flow.
// Implements flow.SequenceTarget.
func (ite *IntermediateThrowEvent) AcceptIncomingFlow(_ *flow.SequenceFlow) error {
	return nil
}

// SupportOutgoingFlow accepts the event's outgoing sequence flow.
// Implements flow.SequenceSource.
func (ite *IntermediateThrowEvent) SupportOutgoingFlow(_ *flow.SequenceFlow) error {
	return nil
}

// Exec emits the event's definition(s) — a message to the broker, any other
// kind through the internal event bus — then returns the outgoing sequence
// flows. Implements exec.NodeExecutor.
func (ite *IntermediateThrowEvent) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	// A Link throw redirects, it does not emit (ADR-006 v.4 §2.8): hand the
	// token to the paired target catch's outgoing flows (resolved statically by
	// the graph wiring). No hub event, and the throw has no outgoing flow of
	// its own — the target catch is bypassed as a pure flow label.
	if name, ok := linkDefName(ite); ok {
		if ite.linkTarget == nil {
			return nil, errs.New(
				errs.M("IntermediateThrowEvent %q[%s]: Link %q has no resolved "+
					"target catch", ite.Name(), ite.ID(), name),
				errs.C(errorClass, errs.InvalidObject))
		}

		return append([]*flow.SequenceFlow{}, ite.linkTarget.Outgoing()...), nil
	}

	ers := []error{}

	for _, ed := range ite.definitions {
		if err := ite.emitDefinition(ctx, re, ed); err != nil {
			ers = append(ers, err)
		}
	}

	if len(ers) != 0 {
		return nil,
			errs.New(
				errs.M("event emitting failed for IntermediateThrowEvent %q[%s]",
					ite.Name(), ite.ID()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(errors.Join(ers...)))
	}

	return append([]*flow.SequenceFlow{}, ite.Outgoing()...), nil
}

var (
	_ flow.SequenceSource     = (*IntermediateThrowEvent)(nil)
	_ flow.SequenceTarget     = (*IntermediateThrowEvent)(nil)
	_ flow.Node               = (*IntermediateThrowEvent)(nil)
	_ flow.EventNode          = (*IntermediateThrowEvent)(nil)
	_ flow.LinkSource         = (*IntermediateThrowEvent)(nil)
	_ exec.NodeExecutor       = (*IntermediateThrowEvent)(nil)
	_ msgflow.MessageProducer = (*IntermediateThrowEvent)(nil)
)
