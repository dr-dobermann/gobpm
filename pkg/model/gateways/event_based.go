// Package gateways provides BPMN gateway implementations.
package gateways

import (
	"context"
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// EventBasedGateway is a diverging Exclusive deferred choice (BPMN §13.4.4, WCP-16,
// ADR-005 v.4 §2.12). The gate owns the wait: it subscribes to every arm's event and,
// on the first to fire, routes that event into the winning arm and lets the token
// continue down that arm's path; the other subscriptions are dropped. No token ever
// sits on an arm. (Parallel is a start-only instantiation construct, out of scope here.)
type EventBasedGateway struct {
	Gateway
}

// NewEventBasedGateway creates a diverging Exclusive Event-Based gateway. The gate's
// arm well-formedness (BPMN §10.6.6) is checked at registration by Validate.
//
// Available options:
//   - foundation.WithId / foundation.WithDoc
//   - options.WithName
//   - gateways.WithDirection
func NewEventBasedGateway(opts ...options.Option) (*EventBasedGateway, error) {
	g, err := New(opts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("event-based gateway building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &EventBasedGateway{
			Gateway: *g,
		},
		nil
}

// Node returns the gateway as its concrete flow node, so a track reaching it via a
// sequence flow dispatches it as the EventBasedGateway, not the embedded base Gateway.
func (g *EventBasedGateway) Node() flow.Node {
	return g
}

// Clone returns a per-instance copy of the EventBasedGateway: the embedded Gateway is
// cloned (fresh shell, empty flows) and the static allowMixed policy is carried over
// (ADR-009). The gate holds no per-instance arm state in this slice — the winner is
// decided by the runtime as the events fire.
func (g *EventBasedGateway) Clone() flow.Node {
	return &EventBasedGateway{
		Gateway: g.clone(),
	}
}

// arms returns the gateway's arm nodes — the targets of its outgoing flows.
func (g *EventBasedGateway) arms() []flow.Node {
	out := g.Outgoing()
	aa := make([]flow.Node, 0, len(out))

	for _, of := range out {
		aa = append(aa, of.Target().Node())
	}

	return aa
}

// Definitions returns the union of all arms' event definitions, so the existing
// wait-registration path (which loops a node's Definitions()) subscribes the gate to
// every arm's event with the gate as the receiver (ADR-005 v.4 §2.12.1). Implements
// part of flow.EventNode.
func (g *EventBasedGateway) Definitions() []flow.EventDefinition {
	var defs []flow.EventDefinition

	for _, arm := range g.arms() {
		if en, ok := arm.(flow.EventNode); ok {
			defs = append(defs, en.Definitions()...)
		}
	}

	return defs
}

// EventClass reports the gate as an Intermediate catch point: it waits for events
// rather than emitting them. Implements part of flow.EventNode.
func (g *EventBasedGateway) EventClass() flow.EventClass {
	return flow.IntermediateEventClass
}

// ArmFor returns the arm node that owns eDef, scanning each arm's event definitions.
// The runtime calls it to resolve the winning arm and advance the token onto that arm's
// path after the gate routes the fired event (SRD-024 §4.1).
func (g *EventBasedGateway) ArmFor(
	eDef flow.EventDefinition,
) (flow.Node, bool) {
	for _, arm := range g.arms() {
		en, ok := arm.(flow.EventNode)
		if !ok {
			continue
		}

		for _, d := range en.Definitions() {
			if defMatches(d, eDef) {
				return arm, true
			}
		}
	}

	return nil, false
}

// defMatches reports whether the arm's event definition d corresponds to the fired
// event definition. Point-to-point triggers (Message, Timer) deliver the arm's own
// definition, so identity (ID) matches. A Signal is BROADCAST by name — the delivered
// definition is the thrower's, a different object — so signals match by signal name
// (the same key the EventHub broadcast routes on).
func defMatches(d, fired flow.EventDefinition) bool {
	if d.ID() == fired.ID() {
		return true
	}

	ds, dok := d.(*events.SignalEventDefinition)
	fs, fok := fired.(*events.SignalEventDefinition)

	if dok && fok && ds.Signal() != nil && fs.Signal() != nil {
		return ds.Signal().Name() == fs.Signal().Name()
	}

	return false
}

// ProcessEvent routes a fired event into its owning arm: it resolves the arm and
// delegates to that arm's own catch/receive behavior, which binds the payload exactly
// as a standalone catch event or Receive Task would. The runtime then advances the
// track onto the arm's path and drops the other subscriptions (ADR-005 v.4 §2.12.1,
// SRD-024 §4.1). Implements eventproc.EventProcessor.
func (g *EventBasedGateway) ProcessEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	arm, ok := g.ArmFor(eDef)
	if !ok {
		return errs.New(
			errs.M("event-based gateway: no arm owns the fired event"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("gateway_id", g.ID()),
			errs.D("event_definition_id", eDef.ID()))
	}

	ep, ok := arm.(eventproc.EventProcessor)
	if !ok {
		return errs.New(
			errs.M("event-based gateway: arm cannot process events"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("gateway_id", g.ID()),
			errs.D("arm_id", arm.ID()))
	}

	return ep.ProcessEvent(ctx, eDef)
}

// Exec is never reached for an Event-Based gateway: the gate is an
// eventproc.EventProcessor, so the track parks it as an event waiter and resumes it via
// ProcessEvent, routing onto the winning arm — it never forks tokens onto its arms.
// Reaching Exec is a wiring bug, so it fails loudly rather than silently dropping the
// token. Implements exec.NodeExecutor.
func (g *EventBasedGateway) Exec(
	_ context.Context,
	_ renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	return nil,
		errs.New(
			errs.M("event-based gateway must not be executed; "+
				"it waits for events and routes via ProcessEvent"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("gateway_id", g.ID()))
}

// Validate checks the gate's structure at registration, once its flows are linked
// (ADR-005 v.4 §2.12.5). It is invoked by Process.Validate via the per-node validation
// hook. All violations are collected and returned together.
func (g *EventBasedGateway) Validate() error {
	out := g.Outgoing()
	ee := []error{}

	// (a) a deferred choice needs at least two alternatives.
	if len(out) < 2 {
		ee = append(ee, errs.New(
			errs.M("event-based gateway needs at least two arms"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("gateway_id", g.ID()),
			errs.D("arms", len(out))))
	}

	msgCatch, recvTask := false, false

	for _, of := range out {
		mc, rt, armErrs := g.validateArm(of)
		ee = append(ee, armErrs...)
		msgCatch = msgCatch || mc
		recvTask = recvTask || rt
	}

	// (f) a Message intermediate catch event and a Receive Task must not be arms of
	// the same gate — both consume messages, so the routing is ambiguous (BPMN
	// §10.6.6: "If Message Intermediate Events are used ... Receive Tasks MUST NOT be
	// used ... and vice versa"). Timer/Signal catch events mix freely with a Receive
	// Task.
	if msgCatch && recvTask {
		ee = append(ee, errs.New(
			errs.M("event-based gateway: a Message catch event and a Receive Task "+
				"cannot both be arms of one gate (BPMN §10.6.6)"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("gateway_id", g.ID())))
	}

	if len(ee) != 0 {
		return errors.Join(ee...)
	}

	return nil
}

// validateArm checks a single outgoing arm flow and its target node, reporting whether
// the arm is a Message intermediate catch event and whether it is a Receive Task (the
// two that must not coexist, §10.6.6), plus any violations (ADR-005 v.4 §2.12.5 rules
// b–e). It is a helper of Validate.
func (g *EventBasedGateway) validateArm(
	of *flow.SequenceFlow,
) (isMsgCatch, isReceiveTask bool, ee []error) {
	// (d) the branch is chosen by the racing event, never by a condition.
	if of.Condition() != nil {
		ee = append(ee, errs.New(
			errs.M("event-based gateway: an arm flow must not carry a condition"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("gateway_id", g.ID()),
			errs.D("flow_id", of.ID())))
	}

	arm := of.Target().Node()

	// (b) an arm must be a catch-capable event node or Receive Task.
	en, ok := arm.(flow.EventNode)
	if !ok {
		return false, false, append(ee, errs.New(
			errs.M("event-based gateway: an arm must be an intermediate "+
				"catch event or a Receive Task"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("gateway_id", g.ID()),
			errs.D("arm_id", arm.ID())))
	}

	if _, ok := arm.(eventproc.EventProcessor); !ok {
		return false, false, append(ee, errs.New(
			errs.M("event-based gateway: an arm must be able to catch its event"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("gateway_id", g.ID()),
			errs.D("arm_id", arm.ID())))
	}

	msgTrigger := false

	for _, d := range en.Definitions() {
		switch d.Type() {
		case flow.TriggerMessage:
			msgTrigger = true
		case flow.TriggerTimer, flow.TriggerSignal:
			// supported in this slice
		default:
			ee = append(ee, errs.New(
				errs.M("event-based gateway: unsupported arm trigger "+
					"(only Message/Timer/Signal)"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("gateway_id", g.ID()),
				errs.D("arm_id", arm.ID()),
				errs.D("trigger", string(d.Type()))))
		}
	}

	// (c) an arm's only incoming flow is the gate.
	if in := arm.Incoming(); len(in) != 1 {
		ee = append(ee, errs.New(
			errs.M("event-based gateway: an arm must have exactly one "+
				"incoming flow (the gate)"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("gateway_id", g.ID()),
			errs.D("arm_id", arm.ID()),
			errs.D("incoming", len(in))))
	}

	if arm.NodeType() == flow.ActivityNodeType {
		// (e) a Receive-Task arm carries no boundary events.
		if be, ok := arm.(interface {
			BoundaryEvents() []flow.EventNode
		}); ok && len(be.BoundaryEvents()) != 0 {
			ee = append(ee, errs.New(
				errs.M("event-based gateway: a Receive-Task arm must not "+
					"have boundary events"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("gateway_id", g.ID()),
				errs.D("arm_id", arm.ID())))
		}

		return false, true, ee
	}

	return msgTrigger, false, ee
}

// ----------------------------------------------------------------------------

// interface checks
var (
	_ exec.NodeExecutor        = (*EventBasedGateway)(nil)
	_ eventproc.EventProcessor = (*EventBasedGateway)(nil)
	_ flow.EventNode           = (*EventBasedGateway)(nil)
)
