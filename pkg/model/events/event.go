package events

import (
	"errors"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
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

// Events that catch a trigger. All Start Events and some Intermediate Events
// are catching Events.
type Event struct {
	foundation.BaseElement
	flow.FlowNode
	flow.FlowElement

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

	// dataPaht holds Event's data path in runtime's Scope.
	dataPath exec.DataPath

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
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	e := Event{
		BaseElement: *be,
		FlowNode:    *flow.NewFlowNode(),
		FlowElement: *flow.NewFlowElement(name),
		properties:  append([]*data.Property{}, props...),
		definitions: append([]flow.EventDefinition{}, defs...),
		triggers:    *set.New[flow.EventTrigger](),
	}

	for _, d := range e.definitions {
		e.triggers.Add(d.Type())
	}

	return &e, nil
}

// Properties returns a copy of the Event properties.
func (e Event) Properties() []*data.Property {
	return append([]*data.Property{}, e.properties...)
}

// Definiitons returns a list of event definitions.
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

// NodeType implements flow.FlowNode interface for the Event.
func (e Event) NodeType() flow.NodeType {
	return flow.EventNodeType
}

// DataPath returns the Event's data path in runtime Scope.
func (e *Event) DataPath() exec.DataPath {
	return e.dataPath
}

// getEventData returns a list of its Properties as list of data.Data.
func (e *Event) getEventData() []data.Data {
	pcnt := len(e.properties)
	dd := make([]data.Data, pcnt)
	for i, p := range e.properties {
		dd[i] = p
	}

	return dd
}

// *****************************************************************************

type catchEvent struct {
	Event

	// The Data Associations of the catch Event. The dataOutputAssociation of a
	// catch Event is used to assign data from the Event to a data element that
	// is in the scope of the Event.
	// For a catch Multiple Event, multiple Data Associations might be REQUIRED,
	// depending on the individual triggers of the Event.
	outputAssociations []*data.Association

	// The Data Outputs for the catch Event. This is an ordered set.
	// dataOutputs are indexed by Ids of ItemDefinitions from eventDefinition
	// bodies.
	dataOutputs map[string]*data.Parameter

	// The outputSet for the catch Event.
	outputSet *data.Set

	// This attribute is only relevant when the catch Event has more than one
	// EventDefinition (Multiple). If this value is true, then all of the types
	// of triggers that are listed in the catch Event MUST be triggered before
	// the Process is instantiated.
	parallelMultiple bool
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

// IsParallelMultiple returns parallelMultiple settings of the catchEvent.
func (ce catchEvent) IsParallelMultiple() bool {
	return ce.parallelMultiple
}

// fillOutput puts all data in ReadyState (those that were filled with
// incoming flow.EventDefinition or data.Association) to outgoing
// data.Association.
func (ce catchEvent) fillOutput() error {
	ee := []error{}

	for _, o := range ce.dataOutputs {
		if o.State().Name() != data.ReadyDataState.Name() {
			continue
		}

		for _, a := range ce.outputAssociations {
			if !a.HasSourceWith(o.Subject().Id()) {
				continue
			}

			if err := a.Update(o.Subject()); err != nil {
				ee = append(ee, err)
			}
		}
	}

	if len(ee) != 0 {
		return errs.New(
			errs.M("errors on sneding evnet's %q[%s] data associations"),
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
	Event

	// The Data Associations of the throw Event. The dataInputAssociation of a
	// throw Event is responsible for the assignment of a data element that is
	// in scope of the Event to the Event data.
	// For a throw Multiple Event, multiple Data Associations might be REQUIRED,
	// depending on the individual results of the Event.
	inputAssociations []*data.Association

	// The Data Inputs for the throw Event. This is an ordered set.
	// dataInputs are indexed by Ids of ItemDefinitions.
	dataInputs map[string]*data.Parameter

	// The InputSet for the throw Event.
	inputSet *data.Set
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

// fillInputs loads all its inputs data.Parameter from active data.Associations.
func (te *throwEvent) fillInputs() error {
	if te.dataPath == exec.EmptyDataPath {
		return errs.New(
			errs.M("data path isn't set for throwEvent",
				te.Name(), te.Id()))
	}

	ee := []error{}

	for _, ia := range te.inputAssociations {
		if in, ok := te.dataInputs[ia.Target.Subject().Id()]; ok {
			if err := in.Value().Update(ia.Target.Value().Get()); err != nil {
				ee = append(ee, err)
			}
		}
	}

	if len(ee) != 0 {
		return errs.New(
			errs.M("data.Associations load failed"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.E(errors.Join(ee...)))
	}

	return nil
}

// emitEvent tries to evmit single event based on flow.EventDefinition ed.
// On failure it returns error.
func (te *throwEvent) emitEvent(
	s exec.Scope,
	eProd exec.EventProducer,
	ed flow.EventDefinition,
) error {
	// get all dataItems the ed depends on
	idd := []data.Data{}
	for _, it := range ed.GetItemsList() {
		// for every dataitem check its presence in inputs of the event
		if inp, ok := te.dataInputs[it.Id()]; ok {
			idd = append(idd, inp)

			continue
		}

		// or in the runtime Scope
		d, err := s.GetDataById(te.dataPath, it.Id())
		if err != nil {
			return errs.New(
				errs.M("couldn't find ItemDefinition #%s", it.Id()),
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
					ed.Type(), ed.Id()),
				errs.E(err))
		}
	}

	return eProd.EmitEvents(ced)
}
