package events

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/observability"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/dataflow"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

// Depending on the type of the Event there are different strategies to forward
// the trigger to catching Events: publication, direct resolution, propagation,
// cancellations, and compensations.
//
// With publication a trigger MAY be received by any catching Events in any
// scope of the system where the trigger is published. Events for which
// publication is used are grouped to Conversations. Published Events MAY
// participate in several Conversations. Messages are triggers, which are
// generated outside of the Pool they are published in. They typically describe
// B2B communication between different Processes in different Pools. When
// Messages need to reach a specific Process instance, correlation is used to
// identify the particular instance.  Signals are triggers generated in the
// Pool they are published. They are typically used for broadcast communication
// within and across Processes, across Pools, and between Process diagrams.
// Timer and Conditional triggers are implicitly thrown. When they are activated
// they wait for a time based or status based condition respectively to trigger
// the catch Event.
// A trigger that is propagated is forwarded from the location where the Event
// has been thrown to the innermost enclosing scope instance (e.g., Process
// level) which has an attached Event being able to catch the trigger. Error
// triggers are critical and suspend execution at the location of throwing.
// Escalations are non critical and execution continues at the location of
// throwing. If no catching Event is found for an error or escalation trigger,
// this trigger is unresolved.
//
// Termination, compensation, and cancellation are directed towards a Process or
// a specific Activity instance.
// Termination indicates that all Activities in the Process or Activity should
// be immediately ended. This includes all instances of multi-instances. It is
// ended without compensation or Event handling.
//
// Compensation of a successfully completed Activity triggers its compensation
// handler. The compensation handler is either user defined or implicit. The
// implicit compensation handler of a Sub Process calls all compensation handlers
// of its enclosed Activities in the reverse order of Sequence Flow dependencies.
// If compensation is invoked for an Activity that has not yet completed, or has
// not completed successfully, nothing happens (in particular, no error is
// raised).
//
// Cancellation will terminate all running Activities and compensate all
// successfully completed Activities in the Sub-Process it is applied to. If the
// Sub-Process is a Transaction, the Transaction is rolled back.
//
// Data Modeling and Events
//
// Some Events (like the Message, Escalation, Error, Signal, and Multiple Event)
// have the capability to carry data.
// Data Association is used to push data from a Catch Event to a data element.
// For such Events, the following constraints apply:
//   - If the Event is associated with multiple EventDefinitions, there MUST be
//     one Data Input (in the case of throw Events) or one Data Output (in the
//     case of catch Events) for each EventDefinition. The order of the
//     EventDefinitions and the order of the Data Inputs/Outputs determine which
//     Data Input/Output corresponds with which EventDefinition.
//   - For each EventDefinition and Data Input/Output pair, if the Data
//     Input/Output is present, it MUST have an ItemDefinition equivalent to the
//     one defined by the Message, Escalation, Error, or Signal on the associated
//     EventDefinition. In the case of a throw Event, if the Data Input is not
//     present, the Message, Escalation, Error, or Signal will not be populated
//     with data. In the case of a catch Event, if the Data Output is not
//     present, the payload within the Message, Escalation, Error, or Signal
//     will not flow out of the Event and into the Process.
//
// The execution behavior is then as follows:
//
//   - For throw Events: When the Event is activated, the data in the Data Input
//     is assigned automatically to the Message, Escalation, Error, or Signal
//     referenced by the corresponding EventDefinition.
//   - For catch Events: When the trigger of the Event occurs (for example, the
//     Message is received), the data is assigned automatically to the Data
//     Output that corresponds to the EventDefinition that described that
//     trigger.

// *****************************************************************************

// Event catches a trigger. All Start Events and some Intermediate Events
// are catching Events.
type Event struct {
	flow.BaseNode

	// Modeler-defined properties MAY be added to an Event. These properties are
	// contained within the Event.
	properties []*data.Property

	// DEV_NOTE: There is no difference for the developer where this definition
	// are helded since either type of definition are external for the event.
	// Moreover, it is impossible to keep order of definition between two
	// similar slices.
	//
	// References the reusable EventDefinitions that are triggers expected.
	// Reusable EventDefinitions are defined as top-level elements.
	// These EventDefinitions can be shared by different catch and throw Events.
	//   • If there is no EventDefinition defined, then this is considered a
	//     catch None Event and the Event will not have an internal marker.
	//   • If there is more than one EventDefinition defined, this is
	//     considered a Catch Multiple Event.
	// This is an ordered set.
	// defitionsRefs []flow.EventDefiniion

	// Defines the event EventDefinitions that are triggers expected.
	// These EventDefinitions are only valid inside the current Event.
	//   • If there is no EventDefinition defined, then this is considered a
	//     catch None Event.
	//   • If there is more than one EventDefinition defined, this is
	//     considered a catch Multiple Event.
	// This is an ordered set.
	definitions []flow.EventDefinition

	// triggers holds information about TriggerTypes of the Event.
	triggers set.Set[flow.EventTrigger]
}

// propCollector gathers the properties supplied via data.PropertyOption during
// event construction: it is the data.PropertyAdder those options target, so an
// event constructor can accept data.WithProperties in its base options (FIX-018).
// Its collected set is copied into the Event; the collector itself is discarded.
type propCollector struct {
	props []*data.Property
}

// AddProperty appends a non-nil property to the collector.
func (pc *propCollector) AddProperty(p *data.Property) error {
	if p == nil {
		return errs.New(
			errs.M("AddProperty: a nil property isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	pc.props = append(pc.props, p)

	return nil
}

// Validate satisfies options.Configurator (a collector has nothing to validate).
func (pc *propCollector) Validate() error { return nil }

// NewEvent creates a new Event and returns its pointer. Properties may be
// supplied positionally (props) or as data.PropertyOption base options
// (FIX-018): the latter are separated from the node options, applied to a
// collector, and merged into the event's property set. The remaining options
// configure the BaseNode.
func newEvent(
	name string,
	props []*data.Property,
	defs []flow.EventDefinition,
	baseOpts ...options.Option,
) (*Event, error) {
	nodeOpts := make([]options.Option, 0, len(baseOpts))
	pc := propCollector{}

	for _, o := range baseOpts {
		if po, ok := o.(data.PropertyOption); ok {
			if err := po(&pc); err != nil {
				return nil, errs.New(
					errs.M("newEvent: couldn't apply a property option to "+
						"event %q", name),
					errs.C(errorClass, errs.BulidingFailed),
					errs.E(err))
			}

			continue
		}

		nodeOpts = append(nodeOpts, o)
	}

	fn, err := flow.NewBaseNode(name, nodeOpts...)
	if err != nil {
		return nil, err
	}

	allProps := append(append([]*data.Property{}, props...), pc.props...)

	e := Event{
		BaseNode:    *fn,
		properties:  allProps,
		definitions: append([]flow.EventDefinition{}, defs...),
		triggers:    *set.New[flow.EventTrigger](),
	}

	for _, d := range e.definitions {
		e.triggers.Add(d.Type())
	}

	return &e, nil
}

// clone returns a per-instance copy of the Event: the properties are deep-copied
// so the clone owns private Property objects — a later edit to the source
// process can't leak into a registered snapshot or a running instance, and a
// value-less property is rejected here (FIX-017). Definitions get per-instance
// identity where supported (cloneDefsForInstance); triggers are shared by
// reference; the BaseNode shell is fresh (empty flows, no container). Execution
// data lives in the per-execution frame, never on the node (ADR-010 §2.4).
func (e *Event) clone() (Event, error) {
	props, err := data.CloneProperties(e.properties)
	if err != nil {
		return Event{}, err
	}

	return Event{
		BaseNode:    e.CloneShell(),
		properties:  props,
		definitions: cloneDefsForInstance(e.definitions),
		triggers:    e.triggers,
	}, nil
}

// cloneDefsForInstance gives every event definition that supports per-instance
// identity (currently the message definition — SRD-017 §4.3) a fresh-id copy so
// concurrent instances waiting on the same trigger register distinct EventHub
// waiters instead of sharing one and all firing on a single point-to-point
// message. Definitions without the capability are shared by reference as before.
func cloneDefsForInstance(defs []flow.EventDefinition) []flow.EventDefinition {
	out := make([]flow.EventDefinition, len(defs))

	for i, d := range defs {
		if c, ok := d.(interface {
			CloneForInstance() flow.EventDefinition
		}); ok {
			out[i] = c.CloneForInstance()

			continue
		}

		out[i] = d
	}

	return out
}

// Properties returns a copy of the Event properties.
func (e Event) Properties() []*data.Property {
	return append([]*data.Property{}, e.properties...)
}

// Definitions returns a list of event definitions.
func (e Event) Definitions() []flow.EventDefinition {
	return append([]flow.EventDefinition{}, e.definitions...)
}

// Triggers returns the Event triggers.
func (e Event) Triggers() []flow.EventTrigger {
	return e.triggers.All()
}

// HasTrigger checks if event has Trigger t in it.
func (e Event) HasTrigger(t flow.EventTrigger) bool {
	return e.triggers.Has(t)
}

// NodeType implements flow.BaseNode interface for the Event.
func (e Event) NodeType() flow.NodeType {
	return flow.EventNodeType
}

// owner labels the event in the shared copy path's errors.
func (e Event) owner() string {
	return fmt.Sprintf("event %q[%s]", e.Name(), e.ID())
}

// *****************************************************************************

type catchEvent struct {
	// dataOutputs are the parameters the triggering element's payload lands
	// in, one per item-bearing definition IN DEFINITION ORDER (p217) — the
	// event's association sources (§10.4.2). Immutable configuration,
	// shared by reference across per-instance clones.
	dataOutputs []*data.Parameter
	// iterCorr is the declared iteration-correlation pair (SRD-085
	// FR-3, ADR-006 v.5 §2.9.3): immutable configuration, shared by
	// reference across per-instance clones; nil when the catch declares
	// none.
	iterCorr *iterationCorrelation
	Event
	outputAssociations []*data.Association
	parallelMultiple   bool
}

// iterationCorrelation pairs a DECLARED process-level correlation key
// (whose payload-side retrieval the model already carries) with the
// subscription-side expression a registering execution evaluates over
// its own scope (SRD-085 FR-3): the two halves of one value, matched
// at delivery.
type iterationCorrelation struct {
	expr    data.FormalExpression
	keyName string
}

// CatchOption configures a catch event at construction (SRD-085).
type CatchOption func(*catchEvent) error

// Option marks CatchOption as an options.Option.
func (CatchOption) Option() {}

// WithIterationCorrelation declares how a concurrently-waiting
// execution of this catch (a parallel-MI iteration) is addressed by an
// arriving message (ADR-006 v.5 §2.9.3): keyName names a declared
// process CorrelationKey — its retrieval expressions derive the
// envelope-side value — and expr, evaluated at registration over the
// registering execution's scope (where the iteration's split item is
// bound), produces the subscription-side value. Equal values route the
// delivery to exactly that execution.
func WithIterationCorrelation(
	keyName string, expr data.FormalExpression,
) CatchOption {
	return CatchOption(func(ce *catchEvent) error {
		if strings.TrimSpace(keyName) == "" {
			return errs.New(
				errs.M("WithIterationCorrelation: an empty correlation "+
					"key name isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		if expr == nil {
			return errs.New(
				errs.M("WithIterationCorrelation: a nil expression "+
					"isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		ce.iterCorr = &iterationCorrelation{keyName: keyName, expr: expr}

		return nil
	})
}

// IterationCorrelation returns the declared iteration-correlation pair,
// or ("", nil) when the catch declares none — the capability the
// registering execution probes (SRD-085 FR-3).
func (ce *catchEvent) IterationCorrelation() (string, data.FormalExpression) {
	if ce.iterCorr == nil {
		return "", nil
	}

	return ce.iterCorr.keyName, ce.iterCorr.expr
}

// ProcessEvent is the node's delivery notification (implements
// eventproc.EventProcessor for every catch event). Since SRD-085 the
// payload does NOT land here: a node is a runtime-immutable definition
// shared by every execution of its instance (parallel MI iterations
// included), so the delivery's item is captured by the RECEIVING
// execution and carried to UploadData in its frame (ADR-006 v.5
// §2.9.1). Nothing is left to do at this seam.
func (ce *catchEvent) ProcessEvent(
	_ context.Context,
	_ flow.EventDefinition,
) error {
	return nil
}

// msgOutputErr classifies a message payload output build failure (FIX-026).
func msgOutputErr(med *MessageEventDefinition, err error) error {
	return errs.New(
		errs.M("couldn't build message payload output"),
		errs.C(errorClass, errs.OperationFailed),
		errs.E(err),
		errs.D(observability.AttrMessageName, med.Message().Name()))
}

// NewCatchEvent creates a new catchEvent and returns its pointer.
func newCatchEvent(
	name string,
	props []*data.Property,
	defs []flow.EventDefinition,
	parallel bool,
	baseOpts ...options.Option,
) (*catchEvent, error) {
	// catch-level options apply to the built catchEvent; everything
	// else rides down to newEvent (the propCollector discipline).
	catchOpts := make([]CatchOption, 0, len(baseOpts))
	eventOpts := make([]options.Option, 0, len(baseOpts))

	for _, o := range baseOpts {
		if co, ok := o.(CatchOption); ok {
			catchOpts = append(catchOpts, co)

			continue
		}

		eventOpts = append(eventOpts, o)
	}

	e, err := newEvent(name, props, defs, eventOpts...)
	if err != nil {
		return nil, err
	}

	// the payload parameters first — one per item-bearing definition, in
	// order — so a catch option that declares them explicitly has the
	// definitions to pair with (p217).
	outputs, err := autoParameters(defs, data.Output)
	if err != nil {
		return nil, err
	}

	ce := &catchEvent{
		Event:              *e,
		outputAssociations: []*data.Association{},
		dataOutputs:        outputs,
		parallelMultiple:   e.triggers.Count() > 1 && parallel,
	}

	for _, co := range catchOpts {
		if err := co(ce); err != nil {
			return nil, errs.New(
				errs.M("newCatchEvent: couldn't apply a catch option to "+
					"event %q", name),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}
	}

	return ce, nil
}

// clone returns a per-instance copy of the catchEvent: the embedded Event is
// cloned (fresh shell + dataPath, shared config), and the data outputs / output
// associations are shared by reference as immutable configuration.
func (ce *catchEvent) clone() (catchEvent, error) {
	e, err := ce.Event.clone()
	if err != nil {
		return catchEvent{}, err
	}

	return catchEvent{
		Event:              e,
		dataOutputs:        ce.dataOutputs,
		outputAssociations: ce.outputAssociations,
		iterCorr:           ce.iterCorr,
		parallelMultiple:   ce.parallelMultiple,
	}, nil
}

// IsParallelMultiple returns parallelMultiple settings of the catchEvent.
func (ce catchEvent) IsParallelMultiple() bool {
	return ce.parallelMultiple
}

// ------------------ exec.NodeDataProducer interface --------------------------

// UploadData instantiates the catchEvent's outputs in the execution frame
// (per-execution copies of the output definitions, carrying the values the
// triggering event delivered) and fills all outputAssociations from those
// instances.
func (ce *catchEvent) UploadData(ctx context.Context, f exec.Frame) error {
	if err := f.InstantiateOutputs(ce.dataOutputs); err != nil {
		return errs.New(
			errs.M("couldn't instantiate outputs of event %q", ce.Name()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	if err := f.LoadProperties(ce.properties); err != nil {
		return errs.New(
			errs.M("couldn't load properties of event %q", ce.Name()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	// THIS delivery's payload rides the frame (ADR-006 v.5 §2.9.1,
	// SRD-085 FR-1): the receiving execution captured it from the fired
	// definition and staged it here — never node state, so concurrent
	// deliveries into sibling executions cannot cross payloads.
	received := f.Received()

	outs := map[string]*data.Parameter{}
	for _, o := range f.Outputs() {
		id := o.ItemDefinition().ID()

		// WS-C3 (ADR-014 v.1): a catch event that received a message binds the
		// runtime payload into the matching output, overriding the static
		// value, so it commits to scope and flows through the associations. A
		// catch with no captured payload keeps the static-output path.
		if received != nil && id == received.ID() {
			if err := o.ItemDefinition().Structure().
				Update(ctx, received.Structure().Get(ctx)); err != nil {
				return errs.New(
					errs.M("couldn't bind received payload for event %q",
						ce.Name()),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err))
			}
		}

		outs[id] = o
	}

	ee := []error{}

	for _, oa := range ce.outputAssociations {
		for _, sid := range oa.SourcesIDs() {
			out, ok := outs[sid]
			if !ok {
				ee = append(ee,
					errs.New(
						errs.M("no output for association #%s source #%s",
							oa.ID(), sid),
						errs.C(errorClass, errs.ObjectNotFound)))

				continue
			}

			// the shared copy path (SRD-094 FR-3): the association is read
			// for its target name, THIS instance's datum is updated; an
			// output not Ready — a payload that did not arrive — simply
			// does not flow (ADR-011 §2.5)
			if err := dataflow.PushOutput(ctx, f, oa, out, ce.owner()); err != nil {
				ee = append(ee, err)
			}
		}
	}

	if len(ee) != 0 {
		return errs.New(
			errs.M("data.Associations upload failed"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.E(errors.Join(ee...)))
	}

	return nil
}

// *****************************************************************************

// ThrowEvents are the events that throws a Result. All End Events and some
// Intermediate Events are throwing Events that MAY eventually be caught by
// another Event. Typically the trigger carries information out of the scope
// where the throw Event occurred into the scope of the catching Events. The
// throwing of a trigger MAY be either implicit as defined by this standard or
// an extension to it or explicit by a throw Event.
type throwEvent struct {
	// dataInputs are the parameters the input associations fill when the
	// event fires and the thrown element is copied from, one per
	// item-bearing definition IN DEFINITION ORDER (p217) — the event's
	// association targets (§10.4.2). Immutable configuration, shared by
	// reference across per-instance clones.
	dataInputs []*data.Parameter
	// autoInputs are the item ids of the inputs newThrowEvent declared from
	// the definitions and no option re-declared. An auto input is a slot
	// for an association to fill: it is instantiated in the frame only when
	// one targets it, so an untargeted one never shadows the scope datum
	// the thrown element binds from by item id (SRD-094 FR-2).
	autoInputs map[string]bool
	Event
	inputAssociations []*data.Association
}

// NewThrowEvent creates a new throwEvent and returns its pointer. Throw-level
// options apply to the built throwEvent after its payload parameters exist;
// everything else rides down to newEvent.
func newThrowEvent(
	name string,
	props []*data.Property,
	defs []flow.EventDefinition,
	baseOpts ...options.Option,
) (*throwEvent, error) {
	throwOpts := make([]ThrowOption, 0, len(baseOpts))
	eventOpts := make([]options.Option, 0, len(baseOpts))

	for _, o := range baseOpts {
		if to, ok := o.(ThrowOption); ok {
			throwOpts = append(throwOpts, to)

			continue
		}

		eventOpts = append(eventOpts, o)
	}

	e, err := newEvent(name, props, defs, eventOpts...)
	if err != nil {
		return nil, err
	}

	inputs, err := autoParameters(defs, data.Input)
	if err != nil {
		return nil, err
	}

	auto := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		auto[in.ItemDefinition().ID()] = true
	}

	te := &throwEvent{
		Event:             *e,
		inputAssociations: []*data.Association{},
		dataInputs:        inputs,
		autoInputs:        auto,
	}

	for _, to := range throwOpts {
		if err := to(te); err != nil {
			return nil, errs.New(
				errs.M("newThrowEvent: couldn't apply a throw option to "+
					"event %q", name),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}
	}

	return te, nil
}

// clone returns a per-instance copy of the throwEvent: the embedded Event is
// cloned (fresh shell + dataPath, shared config), and the data inputs / input
// associations are shared by reference as immutable configuration.
func (te *throwEvent) clone() (throwEvent, error) {
	e, err := te.Event.clone()
	if err != nil {
		return throwEvent{}, err
	}

	return throwEvent{
		Event:             e,
		dataInputs:        te.dataInputs,
		autoInputs:        te.autoInputs,
		inputAssociations: te.inputAssociations,
	}, nil
}

// activeInputs are the inputs an execution instantiates: every declared
// one, and an auto-declared one only when an input association targets it
// — an untargeted auto input is not in the frame, so the thrown element
// binds from the scope by item id exactly as it did before the event
// could carry data.
func (te *throwEvent) activeInputs() []*data.Parameter {
	targeted := make(map[string]bool, len(te.inputAssociations))
	for _, ia := range te.inputAssociations {
		targeted[ia.TargetItemDefID()] = true
	}

	active := make([]*data.Parameter, 0, len(te.dataInputs))

	for _, in := range te.dataInputs {
		if id := in.ItemDefinition().ID(); !te.autoInputs[id] || targeted[id] {
			active = append(active, in)
		}
	}

	return active
}

// ---------------- exec.NodeDataConsumer interface ----------------------------

// LoadData instantiates the throwEvent's inputs and properties in the
// execution frame and fills the input instances from the incoming data
// associations.
func (te *throwEvent) LoadData(ctx context.Context, f exec.Frame) error {
	active := te.activeInputs()

	if err := f.InstantiateInputs(active); err != nil {
		return errs.New(
			errs.M("couldn't instantiate inputs of event %q", te.Name()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	if err := f.LoadProperties(te.properties); err != nil {
		return errs.New(
			errs.M("couldn't load properties of event %q", te.Name()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	ins := map[string]*data.Parameter{}
	for _, in := range f.Inputs() {
		ins[in.ItemDefinition().ID()] = in
	}

	// an input gates the event firing unless it is optional or while-executing
	// (ADR-011 v.2 §2.2-§2.3); events never wait on data.
	gating := data.RequiredItemIDs(active)

	ee := []error{}

	for _, ia := range te.inputAssociations {
		in, ok := ins[ia.TargetItemDefID()]
		if !ok {
			ee = append(ee,
				errs.New(
					errs.M("node %q[%s] has no input for association #%s",
						te.Name(), te.ID(), ia.ID()),
					errs.C(errorClass, errs.ObjectNotFound)))

			continue
		}

		// the shared copy path (SRD-094 FR-3): the association is read for
		// its source name, THIS instance's datum fills the frame input; a
		// required input that can't be filled fails fast — gobpm never
		// waits for data — an optional one may stay Unavailable
		if err := dataflow.FillInput(
			ctx, f, ia, in, gating, te.owner()); err != nil {
			ee = append(ee, err)
		}
	}

	// the start-gate: every required input must now be available — also catches
	// a required input with no association to fill it.
	ee = append(ee, missingRequiredInputs(f, gating, te.Name())...)

	if len(ee) != 0 {
		return errs.New(
			errs.M("node %q[%s] associations data load failed", te.Name(), te.ID()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(errors.Join(ee...)))
	}

	return nil
}

// missingRequiredInputs returns an error for each required input (per gating)
// whose frame instance is not Ready — the start-gate that never waits for data
// (ADR-011 v.2 §2.3). Optional / while-executing inputs are skipped.
func missingRequiredInputs(
	f exec.Frame,
	gating map[string]bool,
	eventName string,
) []error {
	ee := []error{}

	for _, in := range f.Inputs() {
		if !gating[in.ItemDefinition().ID()] {
			continue
		}

		if in.State().Name() != data.ReadyDataState.Name() {
			ee = append(ee,
				errs.New(
					errs.M("required input %q of event %q is unavailable "+
						"(gobpm does not wait for data)", in.Name(), eventName),
					errs.C(errorClass, errs.ConditionFailed)))
		}
	}

	return ee
}

// emitEvent tries to evmit single event based on flow.EventDefinition ed.
// On failure it returns error.
//
// The data items the definition depends on resolve through the execution
// environment: the frame's input instances (loaded by LoadData) first, then
// the container scopes.
// emitDefinition routes one event definition: a message definition is published
// to the broker (the producer choreography, ADR-014 v.1 §2.2), any other kind
// propagates through the internal event bus. Shared by EndEvent and
// IntermediateThrowEvent.
func (te *throwEvent) emitDefinition(
	ctx context.Context,
	re renv.RuntimeEnvironment,
	ed flow.EventDefinition,
) error {
	if med, ok := ed.(*MessageEventDefinition); ok {
		// Throw-event correlation keys are not yet wired (ADR-016 phase-2c);
		// the throw publishes name-keyed only. The payload is what the
		// execution resolves for the message item — the input an association
		// filled (frame-first), else the scope datum of that id; with
		// nothing to bind, the message goes with its own item value: "sent
		// without payload data" (§10.4.1 p216, the engine's reading for a
		// throw — SRD-094 FR-2).
		return msgflow.SendResolved(ctx, re, med.Message(), nil)
	}

	if eed, ok := ed.(*EscalationEventDefinition); ok {
		// An escalation is not a broker broadcast: it climbs the throwing
		// execution's scope chain to the innermost matching catcher (BPMN
		// §10.5.6, ADR-006 §2.6, SRD-058 FR-1). The runtime resolves it — the
		// throwing token continues (Intermediate Throw) or ends normally (End
		// Event); it is never routed through the event hub.
		re.Escalate(eed.Escalation().Code())

		return nil
	}

	if ced, ok := ed.(*CompensationEventDefinition); ok {
		// Compensation resolves directly against the engine's completion
		// ledger (BPMN §13.5.5, ADR-026, SRD-059 FR-5) — never the hub. A
		// wait-for-completion definition is owned entirely by the PARK path
		// (the track parked on this throw before Exec and the runtime already
		// swept; see CompensationWaitRef), so it is a no-op here; a
		// fire-and-forget one triggers inline and the token continues.
		if ced.WaitForCompletion() {
			return nil
		}

		re.Compensate(compensationRefOf(ced), false)

		return nil
	}

	return te.emitEvent(re, re.EventProducer(), ed)
}

// compensationRefOf extracts the definition's activityRef id ("" = the
// default target context — compensate the enclosing scope, §13.5.5).
func compensationRefOf(ced *CompensationEventDefinition) string {
	if a := ced.Activity(); a != nil {
		return a.ID()
	}

	return ""
}

// CompensationWaitRef reports whether this throw carries a wait-for-completion
// Compensation definition, and its activityRef ("" = scope-wide). The engine
// treats such a throw as a WAIT NODE (SRD-059 FR-5): it parks the track,
// triggers the sweep, and resumes the token when every invoked handler
// completes — so Exec (emitDefinition) deliberately no-ops on the definition.
func (te *throwEvent) CompensationWaitRef() (string, bool) {
	for _, ed := range te.definitions {
		if ced, ok := ed.(*CompensationEventDefinition); ok &&
			ced.WaitForCompletion() {
			return compensationRefOf(ced), true
		}
	}

	return "", false
}

// ProcessEvent accepts the compensation-completion delivery that resumes a
// track parked on a wait-for-completion Compensation throw (SRD-059 FR-5):
// the engine loop is the only producer for a parked throw — the delivery
// itself IS the completion signal, so nothing binds here (the SubProcess
// ProcessEvent precedent). Implements eventproc's EventProcessor surface.
func (te *throwEvent) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	if eDef == nil {
		return errs.New(
			errs.M("a nil event definition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return nil
}

func (te *throwEvent) emitEvent(
	re renv.RuntimeEnvironment,
	eProd eventproc.EventProducer,
	ed flow.EventDefinition,
) error {
	// get all dataItems the ed depends on
	idd := []data.Data{}
	for _, it := range ed.GetItemsList() {
		d, err := re.GetDataByID(it.ID())
		if err != nil {
			return errs.New(
				errs.M("couldn't find ItemDefinition #%s", it.ID()),
				errs.E(err))
		}

		if d.State().Name() != data.ReadyDataState.Name() {
			return errs.New(
				errs.M("data %q isn't ready in Scope", d.Name()))
		}

		idd = append(idd, d)
	}

	// if dataitem is ready, compose new eventDefinition with new dataItem value
	// and emit newly created event to EventProducer.
	ced := ed
	if c, ok := ed.(flow.EventDefCloner); ok {
		var err error
		if ced, err = c.CloneEventDefinition(idd); err != nil {
			return errs.New(
				errs.M("couldn't clone EventDefinition %q[%s]",
					ed.Type(), ed.ID()),
				errs.E(err))
		}
	}

	return eProd.PropagateEvent(context.Background(), ced)
}
