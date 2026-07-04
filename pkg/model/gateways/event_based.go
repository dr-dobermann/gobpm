// Package gateways provides BPMN gateway implementations.
package gateways

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// EventBasedGateway is a diverging Event-Based deferred choice (BPMN §13.4.4, WCP-16,
// ADR-005 v.4 §2.12). Mid-flow it owns the wait: it subscribes to every arm's event and,
// on the first to fire, routes that event into the winning arm; the other subscriptions
// are dropped, and no token ever sits on an arm. With WithInstantiate it is a process
// **start** instantiator (§2.12.4): Exclusive — each arm event creates a new instance;
// Parallel — the first creates one instance, which completes only once all arms fired.
type EventBasedGateway struct {
	corrKey *bpmncommon.CorrelationKey
	Gateway
	instantiate bool
	gwType      EventGatewayType
}

// EventGatewayType selects an instantiating gate's start policy (ADR-005 v.4 §2.12.4).
// It is meaningful only with WithInstantiate; a non-instantiating (mid-flow) gate is
// always Exclusive (BPMN §10.6.6).
type EventGatewayType uint8

const (
	// ExclusiveEvents — the first event wins (mid-flow), or each event starts its own
	// instance (start). The default.
	ExclusiveEvents EventGatewayType = iota
	// ParallelEvents — start-only: the first event creates one instance, which completes
	// only once every arm has fired.
	ParallelEvents
)

// eventBasedConfig collects EventBasedGateway-specific construction options.
type eventBasedConfig struct {
	corrKey     *bpmncommon.CorrelationKey
	instantiate bool
	gwType      EventGatewayType
}

// Validate implements options.Configurator. Cross-option consistency (Parallel requires
// instantiate) and start well-formedness are enforced at registration by
// EventBasedGateway.Validate, against the linked flows.
func (c *eventBasedConfig) Validate() error {
	return nil
}

// EventBasedOption configures an EventBasedGateway at construction.
type EventBasedOption func(*eventBasedConfig) error

// Apply implements options.Option against the eventBasedConfig.
func (o EventBasedOption) Apply(cfg options.Configurator) error {
	if ec, ok := cfg.(*eventBasedConfig); ok {
		return o(ec)
	}

	return errs.New(
		errs.M("cfg isn't an eventBasedConfig"),
		errs.C(errorClass, errs.InvalidParameter, errs.TypeCastingError))
}

// WithInstantiate marks the gate a process-start instantiator (no incoming flow): an
// event fired at one of its arms starts a process instance (ADR-005 v.4 §2.12.4, BPMN
// §10.5.6 / §13.2).
func WithInstantiate() EventBasedOption {
	return func(ec *eventBasedConfig) error {
		ec.instantiate = true

		return nil
	}
}

// WithEventGatewayType sets the start policy (default ExclusiveEvents). ParallelEvents is
// start-only — it requires WithInstantiate (checked at registration).
func WithEventGatewayType(t EventGatewayType) EventBasedOption {
	return func(ec *eventBasedConfig) error {
		ec.gwType = t

		return nil
	}
}

// WithCorrelationKey declares the gate's CorrelationKey — one key whose
// CorrelationProperty carries a per-arm-message retrieval expression, so the starter can
// derive the same conversation key from whichever arm fires first and route the
// remaining arms to that instance (Parallel-start, ADR-016 §2.3; BPMN §8.4.2: the gate's
// message triggers "share the same correlation information"). nil is rejected.
func WithCorrelationKey(key *bpmncommon.CorrelationKey) EventBasedOption {
	return func(ec *eventBasedConfig) error {
		if key == nil {
			return errs.New(
				errs.M("WithCorrelationKey: a nil CorrelationKey isn't allowed"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		ec.corrKey = key

		return nil
	}
}

// NewEventBasedGateway creates an Event-Based gateway. Mid-flow (default) it is the
// Exclusive deferred choice; WithInstantiate makes it a start instantiator and
// WithEventGatewayType picks Exclusive/Parallel. Arm + start well-formedness (BPMN
// §10.6.6 / §10.5.6) is checked at registration by Validate.
//
// Available options:
//   - foundation.WithID / foundation.WithDoc
//   - options.WithName
//   - gateways.WithDirection
//   - gateways.WithInstantiate
//   - gateways.WithEventGatewayType
func NewEventBasedGateway(opts ...options.Option) (*EventBasedGateway, error) {
	ec := eventBasedConfig{}
	baseOpts := make([]options.Option, 0, len(opts))
	ee := []error{}

	for _, opt := range opts {
		if eo, ok := opt.(EventBasedOption); ok {
			if err := eo.Apply(&ec); err != nil {
				ee = append(ee, err)
			}

			continue
		}

		baseOpts = append(baseOpts, opt)
	}

	if len(ee) != 0 {
		return nil,
			errs.New(
				errs.M("event-based gateway building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(errors.Join(ee...)))
	}

	g, err := New(baseOpts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("event-based gateway building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &EventBasedGateway{
			Gateway:     *g,
			instantiate: ec.instantiate,
			gwType:      ec.gwType,
			corrKey:     ec.corrKey,
		},
		nil
}

// Instantiate reports whether the gate is a process-start instantiator (WithInstantiate).
func (g *EventBasedGateway) Instantiate() bool {
	return g.instantiate
}

// EventGatewayType returns the gate's start policy (ExclusiveEvents by default).
func (g *EventBasedGateway) EventGatewayType() EventGatewayType {
	return g.gwType
}

// ParallelStart reports whether the gate is a Parallel-start instantiator — the first
// arm event creates one instance, the other arms re-arm as in-instance receivers, and
// the instance completes only once every arm has fired (ADR-005 v.4 §2.12.4). The
// runtime detects it structurally to seed the born instance accordingly (SRD-025 M3).
func (g *EventBasedGateway) ParallelStart() bool {
	return g.instantiate && g.gwType == ParallelEvents
}

// CorrelationKey returns the gate's declared CorrelationKey (nil if none). Read
// structurally by the thresher's starter to derive the conversation key from a fired
// arm's message (SRD-025 §4.3), mirroring StartEvent.CorrelationKey().
func (g *EventBasedGateway) CorrelationKey() *bpmncommon.CorrelationKey {
	return g.corrKey
}

// Node returns the gateway as its concrete flow node, so a track reaching it via a
// sequence flow dispatches it as the EventBasedGateway, not the embedded base Gateway.
func (g *EventBasedGateway) Node() flow.Node {
	return g
}

// Clone returns a per-instance copy of the EventBasedGateway: the embedded Gateway is
// cloned (fresh shell, empty flows) and the static instantiate/gwType policy is carried
// over (ADR-009). The gate holds no per-instance arm state at the model layer — the
// winner (mid-flow) / completion gate (Parallel-start) is decided by the runtime.
func (g *EventBasedGateway) Clone() (flow.Node, error) {
	return &EventBasedGateway{
		Gateway:     g.clone(),
		instantiate: g.instantiate,
		gwType:      g.gwType,
		corrKey:     g.corrKey,
	}, nil
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
// event definition. In-instance (mid-flow) a fired Message/Timer is the arm's own
// (cloned) definition, so identity (ID) matches. Two cases break identity and need a
// name fallback: a Parallel-start gate resolves its firing arm from the starter's
// SNAPSHOT definition against the instance's CLONED arms (SRD-025 §4.3) — different
// objects, same message — so Message arms also match by message name; and a Signal is
// BROADCAST by name (the delivered definition is the thrower's, a different object), so
// signals match by signal name (the same key the EventHub broadcast routes on).
func defMatches(d, fired flow.EventDefinition) bool {
	if d.ID() == fired.ID() {
		return true
	}

	if dm, dok := d.(*events.MessageEventDefinition); dok {
		fm, fok := fired.(*events.MessageEventDefinition)
		if fok && dm.Message() != nil && fm.Message() != nil {
			return dm.Message().Name() == fm.Message().Name()
		}
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
			errs.D("arms", strconv.Itoa(len(out)))))
	}

	msgCatch, recvTask := false, false

	for _, of := range out {
		mc, rt, armErrs := g.validateArm(of)
		ee = append(ee, armErrs...)
		msgCatch = msgCatch || mc
		recvTask = recvTask || rt
	}

	// (h)+(i) the gate-level start rules (the per-arm (g) rule is in validateArm).
	ee = append(ee, g.validateStartGate()...)

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
			errs.D("incoming", strconv.Itoa(len(in)))))
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

	// (g) at start a non-message arm (a valid Timer/Signal catch — msgTrigger false, no
	// prior errors) cannot instantiate: every start arm must be a Message catch event or
	// a Receive Task (BPMN §10.6.6 / §13.2).
	if g.instantiate && !msgTrigger && len(ee) == 0 {
		ee = append(ee, errs.New(
			errs.M("event-based gateway: at start every arm must be message-based "+
				"(a Message catch event or a Receive Task)"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("gateway_id", g.ID()),
			errs.D("arm_id", arm.ID())))
	}

	return msgTrigger, false, ee
}

// validateStartGate checks the gate-level start rules (BPMN §10.5.6 / §10.6.6): an
// instantiating gate has no incoming flow, and ParallelEvents is start-only — a
// non-instantiating gate must be Exclusive. A helper of Validate.
func (g *EventBasedGateway) validateStartGate() []error {
	var ee []error

	if g.instantiate && len(g.Incoming()) != 0 {
		ee = append(ee, errs.New(
			errs.M("event-based gateway: an instantiating gate must have no "+
				"incoming flow"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("gateway_id", g.ID()),
			errs.D("incoming", strconv.Itoa(len(g.Incoming())))))
	}

	if g.gwType == ParallelEvents && !g.instantiate {
		ee = append(ee, errs.New(
			errs.M("event-based gateway: ParallelEvents requires WithInstantiate "+
				"(a non-instantiating gate must be Exclusive, BPMN §10.6.6)"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("gateway_id", g.ID())))
	}

	ee = append(ee, g.validateParallelStartCorrelation()...)

	return ee
}

// validateParallelStartCorrelation enforces ADR-005 v.4 §2.12.5 rule 7 for a
// Parallel-start gate (SRD-033): the gate must declare a CorrelationKey and
// every arm's message must cover it — each key property declares a retrieval
// expression for that message — so all arm messages correlate to the one
// created instance (BPMN §10.6.6: "the Messages that trigger the Events of the
// Gateway configuration MUST share the same correlation information"). Without
// this a keyless (or uncovered) arm message hits the engine's empty-key
// create branch and every arm spawns its own stuck instance.
func (g *EventBasedGateway) validateParallelStartCorrelation() []error {
	if !g.instantiate || g.gwType != ParallelEvents {
		return nil
	}

	if g.corrKey == nil {
		return []error{errs.New(
			errs.M("event-based gateway: a Parallel-start gate must declare a "+
				"CorrelationKey — its arm messages MUST share the same "+
				"correlation information (BPMN §10.6.6)"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("gateway_id", g.ID()))}
	}

	ee := []error{}

	for _, of := range g.Outgoing() {
		arm := of.Target().Node()

		msg := armMessage(arm)
		if msg == nil {
			// a non-message arm is already rejected at start by the
			// message-based rule; nothing to cover here.
			continue
		}

		if missing := msgflow.MissingKeyProperties(g.corrKey, msg); len(missing) != 0 {
			ee = append(ee, errs.New(
				errs.M("event-based gateway: arm's message doesn't cover the "+
					"gate's CorrelationKey — the gate's messages MUST share "+
					"the same correlation information (BPMN §10.6.6)"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("gateway_id", g.ID()),
				errs.D("arm_id", arm.ID()),
				errs.D("message", msg.Name()),
				errs.D("missing_properties", strings.Join(missing, ", "))))
		}
	}

	return ee
}

// armMessage resolves the message an arm consumes: a Receive-Task arm exposes
// Message() itself (asserted structurally — gateways doesn't import
// activities); a message-catch arm carries it in its MessageEventDefinition.
// A non-message arm yields nil.
func armMessage(arm flow.Node) *bpmncommon.Message {
	if mp, ok := arm.(interface{ Message() *bpmncommon.Message }); ok {
		return mp.Message()
	}

	en, ok := arm.(flow.EventNode)
	if !ok {
		return nil
	}

	for _, d := range en.Definitions() {
		if med, ok := d.(*events.MessageEventDefinition); ok {
			return med.Message()
		}
	}

	return nil
}

// ----------------------------------------------------------------------------

// interface checks
var (
	_ exec.NodeExecutor        = (*EventBasedGateway)(nil)
	_ eventproc.EventProcessor = (*EventBasedGateway)(nil)
	_ flow.EventNode           = (*EventBasedGateway)(nil)
)
