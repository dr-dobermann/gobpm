package activities

import (
	"context"
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// SubProcess is the embedded Sub-Process (ADR-023 §2.2): an activity in
// its parent's graph AND the container of its own inner graph. Its inner
// flow is seeded when the host token arrives (the §2.3 validated shapes:
// a unique None Start Event, or every flow-less inner activity/gateway);
// it completes when its scope drains (BPMN §13.3.4). Inner elements are
// added exactly like a process's (Add + flow.Link — the same-container
// rule confines inner flows to this container).
type SubProcess struct {
	flow.ElementsContainer
	activity
	// triggered marks an Event Sub-Process (triggeredByEvent, BPMN §13.5.4):
	// a scope-armed handler entered by its triggered start, not a token
	// (ADR-023 v.2 §2.10, SRD-052). A plain (embedded) Sub-Process leaves it
	// false and keeps the §2.3 None-start / flow-less shape.
	triggered bool
}

// NewSubProcess creates an empty embedded Sub-Process. Inner elements are
// added afterwards via Add; the shape rules are enforced by Validate at
// process validation (registration), not at construction — a container is
// legitimately built element by element. WithTriggeredByEvent makes it an
// Event Sub-Process instead.
func NewSubProcess(
	name string,
	opts ...options.Option,
) (*SubProcess, error) {
	cfg := subProcessConfig{}
	actOpts := make([]options.Option, 0, len(opts))

	for _, o := range opts {
		switch opt := o.(type) {
		case SubProcessOption:
			if err := opt(&cfg); err != nil {
				return nil, err
			}

		default:
			actOpts = append(actOpts, o)
		}
	}

	a, err := newActivity(name, actOpts...)
	if err != nil {
		return nil, err
	}

	return &SubProcess{
		ElementsContainer: flow.NewElementsContainer(),
		activity:          *a,
		triggered:         cfg.triggered,
	}, nil
}

// IsEventSubProcess reports whether this Sub-Process is an Event Sub-Process
// (triggeredByEvent) — a scope-armed handler, not a flow-reached activity
// (ADR-023 v.2 §2.10). The runtime uses it to skip the handler from entry
// seeding and arm it instead (SRD-052).
func (sp *SubProcess) IsEventSubProcess() bool {
	return sp.triggered
}

// ActivityType returns the SubProcess activity type.
func (sp *SubProcess) ActivityType() flow.ActivityType {
	return flow.SubProcessActivity
}

// Node returns the SubProcess itself — the concrete-type override every
// node provides (the embedded activity base would otherwise surface,
// stripping the container and executor capabilities from flow targets).
func (sp *SubProcess) Node() flow.Node {
	return sp
}

// Add adds a flow element into the Sub-Process's inner graph, binding it
// to the Sub-Process as its container (flow.Container).
func (sp *SubProcess) Add(e flow.Element) error {
	return sp.AddElement(sp, e)
}

// Remove removes a flow element from the Sub-Process's inner graph
// (flow.Container).
func (sp *SubProcess) Remove(e flow.Element) error {
	return sp.RemoveElement(e)
}

// Validate checks the Sub-Process's inner graph — the ADR-023 §2.3 shape
// rules realized at the process-validation seam (the per-node hook of
// Process.Validate calls it; an enclosing SubProcess recurses into it the
// same way):
//
//   - every inner flow's endpoints are inner nodes;
//   - exactly one None Start Event, XOR no start event with at least one
//     flow-less inner activity/gateway (the two normative §13.3.4 shapes);
//   - a triggered start, multiple starts, a mixed shape, or an empty
//     container are classified errors;
//   - an inner boundary event's host is an inner node;
//   - inner nodes' own Validate hooks run (a nested SubProcess validates
//     its body — recursion).
func (sp *SubProcess) Validate() error {
	ee := []error{}

	if err := sp.ValidateFlows(); err != nil {
		ee = append(ee, err)
	}

	noneStarts, triggeredStarts, flowless, nonIntrErr := sp.classifyEntries(&ee)

	if sp.triggered {
		sp.validateEventSubShape(&ee, noneStarts, triggeredStarts, nonIntrErr)
	} else {
		sp.validateEmbeddedShape(&ee, noneStarts, triggeredStarts, flowless)
	}

	if len(ee) > 0 {
		return errors.Join(ee...)
	}

	return nil
}

// validateEmbeddedShape enforces the embedded Sub-Process entry shape (ADR-023
// §2.3, BPMN §13.3.4): a unique None Start Event XOR at least one flow-less
// inner node; a triggered start is not allowed (it belongs to an event
// sub-process or a top-level process).
func (sp *SubProcess) validateEmbeddedShape(
	ee *[]error, noneStarts, triggeredStarts, flowless int,
) {
	switch {
	case triggeredStarts > 0:
		*ee = append(*ee, sp.shapeErr(
			"a triggered Start Event isn't allowed in an embedded "+
				"Sub-Process (BPMN §13.3.4; event sub-processes and "+
				"top-level processes own triggered starts)"))

	case noneStarts > 1:
		*ee = append(*ee, sp.shapeErr(
			"an embedded Sub-Process has at most a unique None Start "+
				"Event (BPMN §13.3.4)"))

	case noneStarts == 1 && flowless > 0:
		*ee = append(*ee, sp.shapeErr(
			"a None Start Event and flow-less inner nodes can't be "+
				"mixed — the §13.3.4 shapes are exclusive alternatives"))

	case noneStarts == 0 && flowless == 0:
		*ee = append(*ee, sp.shapeErr(
			"an embedded Sub-Process needs an entry: one None Start "+
				"Event or at least one flow-less activity/gateway"))
	}
}

// validateEventSubShape enforces the Event Sub-Process entry shape (ADR-023
// v.2 §2.10, BPMN §13.5.4/§10.5.2): exactly one triggered Start Event, no None
// start. Both interrupting and non-interrupting triggered starts are accepted
// (SRD-052 interrupting + SRD-053 non-interrupting); the only trigger-mode
// restriction is that an Error start must be interrupting (BPMN §10.5.6).
func (sp *SubProcess) validateEventSubShape(
	ee *[]error, noneStarts, triggeredStarts, nonIntrError int,
) {
	switch {
	case triggeredStarts != 1:
		*ee = append(*ee, sp.shapeErr(
			"an event sub-process needs exactly one triggered Start Event "+
				"(BPMN §10.5.2), got %d", triggeredStarts))

	case noneStarts > 0:
		*ee = append(*ee, sp.shapeErr(
			"an event sub-process has no None Start Event — it is entered "+
				"by its triggered start (BPMN §13.5.4)"))

	case nonIntrError > 0:
		*ee = append(*ee, sp.shapeErr(
			"an Error event sub-process start must be interrupting "+
				"(BPMN §10.5.6); a non-interrupting Error start is invalid"))
	}
}

// classifyEntries scans the inner nodes, counting the entry-shape
// participants, checking inner boundary hosting, and running the inner
// per-node Validate hooks; violations append to ee.
func (sp *SubProcess) classifyEntries(
	ee *[]error,
) (noneStarts, triggeredStarts, flowless, nonInterruptingError int) {
	for _, n := range sp.Nodes() {
		switch {
		case isEventSubProcess(n):
			// An inner Event Sub-Process is a scope-armed handler, not an
			// entry (ADR-023 v.2 §2.10): it is skipped from the entry shape
			// (so it never counts as a flow-less seed and breaks the parent's
			// §13.3.4 shape) and armed at runtime instead. Its own inner
			// graph is still validated by the per-node hook below.

		case isBoundaryEvent(n):
			// A boundary event has no incoming flow by nature — it is not
			// an entry. Its host must be an inner node.
			be := n.(flow.BoundaryEvent)
			if !sp.contains(be.AttachedTo()) {
				*ee = append(*ee, sp.shapeErr(
					"boundary event %q is attached to a node outside the "+
						"Sub-Process", n.ID()))
			}

		case isStartEvent(n):
			en := n.(flow.EventNode)
			defs := en.Definitions()
			if len(defs) == 0 {
				noneStarts++
			} else {
				triggeredStarts++
				// A non-interrupting triggered start is allowed (SRD-053)
				// EXCEPT an Error start, which is always interrupting (BPMN
				// §10.5.6) — only that case is still rejected.
				if si, ok := n.(interface{ IsInterrupting() bool }); ok &&
					!si.IsInterrupting() && defs[0].Type() == flow.TriggerError {
					nonInterruptingError++
				}
			}

		case len(n.Incoming()) == 0 && n.NodeType() != flow.EventNodeType:
			// activities and gateways without incoming flows are the
			// no-start entry shape (§13.3.4); events are not.
			flowless++
		}

		if v, ok := n.(interface{ Validate() error }); ok {
			if err := v.Validate(); err != nil {
				*ee = append(*ee, err)
			}
		}
	}

	return noneStarts, triggeredStarts, flowless, nonInterruptingError
}

// isEventSubProcess reports whether n is an Event Sub-Process (a scope-armed
// handler, skipped from entry seeding — ADR-023 v.2 §2.10).
func isEventSubProcess(n flow.Node) bool {
	h, ok := n.(interface{ IsEventSubProcess() bool })

	return ok && h.IsEventSubProcess()
}

// isBoundaryEvent reports whether n is a boundary event.
func isBoundaryEvent(n flow.Node) bool {
	_, ok := n.(flow.BoundaryEvent)

	return ok
}

// isStartEvent reports whether n is a Start Event node.
func isStartEvent(n flow.Node) bool {
	en, ok := n.(flow.EventNode)

	return ok && en.EventClass() == flow.StartEventClass
}

// contains reports whether node is one of the Sub-Process's inner nodes.
func (sp *SubProcess) contains(node flow.Node) bool {
	if node == nil {
		return false
	}

	for _, n := range sp.Nodes() {
		if n.ID() == node.ID() {
			return true
		}
	}

	return false
}

// shapeErr builds one classified shape-validation error naming the
// Sub-Process.
func (sp *SubProcess) shapeErr(format string, args ...any) error {
	return errs.New(
		errs.M(format, args...),
		errs.C(errorClass, errs.InvalidObject),
		errs.D("subprocess_id", sp.ID()),
		errs.D("subprocess_name", sp.Name()))
}

// Clone implements flow.Node: the activity base clones per the shared
// contract (config by reference, per-instance state fresh, the host's own
// boundary events left for the enclosing graph's rebind), and the inner
// graph deep-clones through the container core — every inner node via its
// own Clone (a nested Sub-Process recurses), inner flows relinked, inner
// defaults remapped and inner boundaries rebound between the clones
// (flow.WireClonedGraph, one wiring implementation for every level).
func (sp *SubProcess) Clone() (flow.Node, error) {
	a, err := sp.clone()
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't clone sub-process %q", sp.ID()),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	inner, err := sp.CloneGraph()
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't clone the inner graph of sub-process %q",
				sp.ID()),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return &SubProcess{
		ElementsContainer: inner,
		activity:          a,
		triggered:         sp.triggered,
	}, nil
}

// ProcessEvent accepts the scope-completion delivery that resumes the
// parked host track (SRD-049 FR-9): the engine loop is the only producer
// for a composite — the delivery itself IS the completion signal, so
// nothing binds here. Implements eventproc's EventProcessor surface the
// track's deliver dispatches to.
func (sp *SubProcess) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	if eDef == nil {
		return errs.New(
			errs.M("a nil event definition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("subprocess_id", sp.ID()))
	}

	return nil
}

// Exec runs after the scope drained and the host resumed (SRD-049 FR-9):
// the inner work is done, so the composite's execution is exactly the
// standard activity completion — select the outgoing flows (conditional /
// default rules included).
func (sp *SubProcess) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	return sp.selectOutgoing(ctx, re)
}

// interface checks
var (
	_ flow.Container    = (*SubProcess)(nil)
	_ flow.ActivityNode = (*SubProcess)(nil)
)
