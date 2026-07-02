package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

// boundaryTriggers are the event triggers a boundary event may carry in 0.1.0
// (SRD-029 FR-2). Timer/Message/Signal may be interrupting or non-interrupting;
// Error is always interrupting (BPMN §10.5.6). Conditional/Escalation/Cancel/
// Compensation/Multiple are deferred (ADR-018 v.1 §2.7).
var boundaryTriggers = set.New[flow.EventTrigger](
	flow.TriggerTimer,
	flow.TriggerMessage,
	flow.TriggerSignal,
	flow.TriggerError,
)

// boundaryHost is the activity-side capability BoundTo needs: it both lists the
// boundaries already attached (for the multiplicity check) and accepts a new
// one. The concrete activity (and every task that embeds it) satisfies it; the
// narrow interface keeps the attachment logic here without widening
// flow.ActivityNode.
type boundaryHost interface {
	flow.ActivityNode

	BoundaryEvents() []flow.EventNode
	AddBoundaryEvent(flow.BoundaryEvent) error
}

// BoundaryEvent is a catch event attached to an activity that fires while the
// activity executes: an interrupting boundary (cancelActivity) terminates the
// guarded activity and routes a token onto its outgoing (exception) flow; a
// non-interrupting one spawns a parallel token while the activity runs on
// (ADR-018 v.1 §2.2-§2.3). It is one type parameterized by its trigger
// definition — the trigger behavior lives in the EventDefinition, not in a
// type hierarchy (SRD-029 §4.1).
type BoundaryEvent struct {
	attachedTo flow.ActivityNode
	catchEvent
	cancelActivity bool
}

// NewBoundaryEvent builds a boundary event and attaches it to host. It
// validates every parameter (SRD-029 FR-2; the validate-all-public-params
// rule): a non-nil host and definition, a trigger allowed on a boundary, and —
// since an Error boundary is always interrupting (BPMN §10.5.6) — it rejects a
// non-interrupting Error boundary. For a message trigger the payload output is
// registered so an arrived payload can bind into scope on resume.
func NewBoundaryEvent(
	name string,
	host flow.ActivityNode,
	def flow.EventDefinition,
	cancelActivity bool,
	baseOpts ...options.Option,
) (*BoundaryEvent, error) {
	if host == nil {
		return nil,
			errs.New(
				errs.M("NewBoundaryEvent: a nil host activity isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if def == nil {
		return nil,
			errs.New(
				errs.M("NewBoundaryEvent: a nil event definition isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if !boundaryTriggers.Has(def.Type()) {
		return nil,
			errs.New(
				errs.M("NewBoundaryEvent: %q trigger isn't allowed for a "+
					"boundary event", def.Type()),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("event_trigger", string(def.Type())))
	}

	if def.Type() == flow.TriggerError && !cancelActivity {
		return nil,
			errs.New(
				errs.M("NewBoundaryEvent: an Error boundary is always "+
					"interrupting; cancelActivity=false isn't allowed"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	ce, err := newCatchEvent(name, nil,
		[]flow.EventDefinition{def}, false, baseOpts...)
	if err != nil {
		return nil, err
	}

	if med, ok := def.(*MessageEventDefinition); ok {
		ce.addMessagePayloadOutput(med)
	}

	b := &BoundaryEvent{
		catchEvent:     *ce,
		cancelActivity: cancelActivity,
	}

	if err := b.BoundTo(host); err != nil {
		return nil, err
	}

	return b, nil
}

// BoundTo attaches the boundary event to host, enforcing multiplicity: at most
// one interrupting handler per Event Declaration on a given activity
// (ADR-018 v.1 §2.5). Non-interrupting handlers are unbounded. Implements
// flow.BoundaryEvent.
func (b *BoundaryEvent) BoundTo(host flow.ActivityNode) error {
	if host == nil {
		return errs.New(
			errs.M("BoundaryEvent.BoundTo: a nil host activity isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	h, ok := host.(boundaryHost)
	if !ok {
		return errs.New(
			errs.M("BoundaryEvent.BoundTo: host %q doesn't support boundary "+
				"attachment", host.Name()),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if b.cancelActivity {
		key := declarationKey(b)
		for _, ex := range h.BoundaryEvents() {
			be, ok := ex.(flow.BoundaryEvent)
			if !ok || !be.CancelActivity() {
				continue
			}

			if declarationKey(be) == key {
				return errs.New(
					errs.M("BoundaryEvent.BoundTo: an interrupting boundary "+
						"for this declaration is already attached to %q",
						host.Name()),
					errs.C(errorClass, errs.InvalidParameter),
					errs.D("declaration", key))
			}
		}
	}

	b.attachedTo = host

	return h.AddBoundaryEvent(b)
}

// declarationKey identifies the Event Declaration a boundary catches, for the
// multiplicity rule. The key is the trigger plus the EventDefinition identity,
// so two boundaries on distinct declarations (e.g. different errorRef, modeled
// as distinct definitions) are both allowed while a re-attachment of the same
// declaration is rejected.
func declarationKey(be flow.BoundaryEvent) string {
	defs := be.Definitions()
	if len(defs) == 0 {
		return ""
	}

	d := defs[0]

	return string(d.Type()) + ":" + d.ID()
}

// AttachedTo returns the activity the boundary event is bound to (nil until
// BoundTo succeeds).
func (b *BoundaryEvent) AttachedTo() flow.ActivityNode {
	return b.attachedTo
}

// CancelActivity reports whether the boundary interrupts its host
// (interrupting) or fires in parallel (non-interrupting). Implements
// flow.BoundaryEvent.
func (b *BoundaryEvent) CancelActivity() bool {
	return b.cancelActivity
}

// EventClass classifies the event as a boundary event (a catch over the host
// activity's execution window).
func (b *BoundaryEvent) EventClass() flow.EventClass {
	return flow.BoundaryEventClass
}

// Node returns the event as a flow.Node.
func (b *BoundaryEvent) Node() flow.Node {
	return b
}

// Clone returns a per-instance copy. The host back-reference is shared and
// rewired to the cloned activity at instance build; the captured payload is
// per-instance runtime state and is not carried over.
func (b *BoundaryEvent) Clone() (flow.Node, error) {
	ce, err := b.clone()
	if err != nil {
		return nil, err
	}

	return &BoundaryEvent{
		catchEvent:     ce,
		attachedTo:     b.attachedTo,
		cancelActivity: b.cancelActivity,
	}, nil
}

// AcceptIncomingFlow rejects an incoming sequence flow: a boundary event is
// attached to an activity, it is never the target of a sequence flow.
// Implements flow.SequenceTarget.
func (b *BoundaryEvent) AcceptIncomingFlow(_ *flow.SequenceFlow) error {
	return errs.New(
		errs.M("a boundary event accepts no incoming sequence flow"),
		errs.C(errorClass, errs.InvalidParameter))
}

// SupportOutgoingFlow accepts the boundary's outgoing (exception / parallel)
// sequence flow. Implements flow.SequenceSource.
func (b *BoundaryEvent) SupportOutgoingFlow(_ *flow.SequenceFlow) error {
	return nil
}

var (
	_ flow.Node           = (*BoundaryEvent)(nil)
	_ flow.EventNode      = (*BoundaryEvent)(nil)
	_ flow.BoundaryEvent  = (*BoundaryEvent)(nil)
	_ flow.SequenceSource = (*BoundaryEvent)(nil)
	_ flow.SequenceTarget = (*BoundaryEvent)(nil)
)
