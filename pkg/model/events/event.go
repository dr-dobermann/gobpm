package events

import (
	"context"
	"errors"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
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

// NewEvent creates a new Event and returns its pointer.
func newEvent(
	name string,
	props []*data.Property,
	defs []flow.EventDefinition,
	baseOpts ...options.Option,
) (*Event, error) {
	fn, err := flow.NewBaseNode(name, baseOpts...)
	if err != nil {
		return nil, err
	}

	e := Event{
		BaseNode:    *fn,
		properties:  append([]*data.Property{}, props...),
		definitions: append([]flow.EventDefinition{}, defs...),
		triggers:    *set.New[flow.EventTrigger](),
	}

	for _, d := range e.definitions {
		e.triggers.Add(d.Type())
	}

	return &e, nil
}

// clone returns a per-instance copy of the Event: properties, definitions and
// triggers are shared by reference (immutable configuration); the BaseNode shell
// is fresh (empty flows, no container). Execution data lives in the
// per-execution frame, never on the node (ADR-010 §2.4).
func (e *Event) clone() Event {
	return Event{
		BaseNode:    e.CloneShell(),
		properties:  e.properties,
		definitions: e.definitions,
		triggers:    e.triggers,
	}
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

// *****************************************************************************

type catchEvent struct {
	dataOutputs map[string]*data.Parameter
	Event
	outputAssociations []*data.Association
	parallelMultiple   bool
}

// NewCatchEvent creates a new catchEvent and returns its pointer.
func newCatchEvent(
	name string,
	props []*data.Property,
	defs []flow.EventDefinition,
	parallel bool,
	baseOpts ...options.Option,
) (*catchEvent, error) {
	e, err := newEvent(name, props, defs, baseOpts...)
	if err != nil {
		return nil, err
	}

	return &catchEvent{
		Event:              *e,
		outputAssociations: []*data.Association{},
		dataOutputs:        map[string]*data.Parameter{},
		parallelMultiple:   e.triggers.Count() > 1 && parallel,
	}, nil
}

// clone returns a per-instance copy of the catchEvent: the embedded Event is
// cloned (fresh shell + dataPath, shared config), and the data outputs / output
// associations are shared by reference as immutable configuration.
func (ce *catchEvent) clone() catchEvent {
	return catchEvent{
		Event:              ce.Event.clone(),
		dataOutputs:        ce.dataOutputs,
		outputAssociations: ce.outputAssociations,
		parallelMultiple:   ce.parallelMultiple,
	}
}

// IsParallelMultiple returns parallelMultiple settings of the catchEvent.
func (ce catchEvent) IsParallelMultiple() bool {
	return ce.parallelMultiple
}

// ------------------ scope.NodeDataProducer interface --------------------------

// UploadData instantiates the catchEvent's outputs in the execution frame
// (per-execution copies of the output definitions, carrying the values the
// triggering event delivered) and fills all outputAssociations from those
// instances.
func (ce *catchEvent) UploadData(ctx context.Context, f *scope.Frame) error {
	defs := make([]*data.Parameter, 0, len(ce.dataOutputs))
	for _, def := range ce.dataOutputs {
		defs = append(defs, def)
	}

	if err := f.InstantiateOutputs(defs); err != nil {
		return errs.New(
			errs.M("couldn't instantiate outputs of event %q", ce.Name()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	outs := map[string]*data.Parameter{}
	for _, o := range f.Outputs() {
		outs[o.ItemDefinition().ID()] = o
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

			if out.State().Name() != data.ReadyDataState.Name() {
				ee = append(ee,
					errs.New(
						errs.M("output #%s isn't ready"),
						errs.C(errorClass, errs.InvalidState)))

				continue
			}

			if err := oa.UpdateSource(
				ctx, out.ItemDefinition(), data.NoRecalculate); err != nil {
				ee = append(ee,
					errs.New(
						errs.M("couldn't update association #%s source #%s",
							oa.ID(), sid),
						errs.E(err)))
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
	dataInputs map[string]*data.Parameter
	Event
	inputAssociations []*data.Association
}

// NewThrowEvent creates a new throwEvent and returns its pointer.
func newThrowEvent(
	name string,
	props []*data.Property,
	defs []flow.EventDefinition,
	baseOpts ...options.Option,
) (*throwEvent, error) {
	e, err := newEvent(name, props, defs, baseOpts...)
	if err != nil {
		return nil, err
	}

	return &throwEvent{
		Event:             *e,
		inputAssociations: []*data.Association{},
		dataInputs:        map[string]*data.Parameter{},
	}, nil
}

// clone returns a per-instance copy of the throwEvent: the embedded Event is
// cloned (fresh shell + dataPath, shared config), and the data inputs / input
// associations are shared by reference as immutable configuration.
func (te *throwEvent) clone() throwEvent {
	return throwEvent{
		Event:             te.Event.clone(),
		dataInputs:        te.dataInputs,
		inputAssociations: te.inputAssociations,
	}
}

// ---------------- scope.NodeDataConsumer interface ----------------------------

// LoadData instantiates the throwEvent's inputs and properties in the
// execution frame and fills the input instances from the incoming data
// associations.
func (te *throwEvent) LoadData(ctx context.Context, f *scope.Frame) error {
	defs := make([]*data.Parameter, 0, len(te.dataInputs))
	for _, def := range te.dataInputs {
		defs = append(defs, def)
	}

	if err := f.InstantiateInputs(defs); err != nil {
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
	inDefs := make([]*data.Parameter, 0, len(te.dataInputs))
	for _, d := range te.dataInputs {
		inDefs = append(inDefs, d)
	}

	gating := data.RequiredItemIDs(inDefs)

	ee := []error{}

	for _, ia := range te.inputAssociations {
		if !ia.IsReady() {
			// a required input that can't be filled is a fail-fast error —
			// gobpm never waits for data. An optional / while-executing input
			// may stay Unavailable.
			if gating[ia.TargetItemDefID()] {
				ee = append(ee,
					errs.New(
						errs.M("required input of association #%s is unavailable "+
							"(gobpm does not wait for data)", ia.ID()),
						errs.C(errorClass, errs.ConditionFailed)))
			}

			continue
		}

		in, ok := ins[ia.TargetItemDefID()]
		if !ok {
			ee = append(ee,
				errs.New(
					errs.M("node %q[%s] has no input for association #%s",
						te.Name(), te.ID(), ia.ID()),
					errs.C(errorClass, errs.ObjectNotFound)))

			continue
		}

		val, err := ia.Value(ctx)
		if err != nil {
			ee = append(ee,
				errs.New(
					errs.M("couldn't get association #%s value", ia.ID()),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err)))

			continue
		}

		if err := in.Value().Update(ctx, val.Structure().Get(ctx)); err != nil {
			ee = append(ee,
				errs.New(
					errs.M("node %q[%s] input #%s update failed",
						te.Name(), te.ID(), in.ItemDefinition().ID()),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err)))

			continue
		}

		// a DataInput filled by its association becomes available (BPMN
		// §10.4.2) — the state flip targets the frame instance only.
		if err := in.UpdateState(data.ReadyDataState); err != nil {
			ee = append(ee,
				errs.New(
					errs.M("node %q[%s] input #%s state update failed",
						te.Name(), te.ID(), in.ItemDefinition().ID()),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err)))
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
	f *scope.Frame,
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
