package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// Depending on the type of the Event there are different strategies to forward
// the trigger to catching Events: publication, direct resolution, propagation,
// cancellations, and compensations.
//
// With publication a trigger MAY be received by any catching Events in any scope
// of the system where the trigger is published. Events for which publication is
// used are grouped to Conversations. Published Events MAY participate in several
// Conversations. Messages are triggers, which are generated outside of the Pool
// they are published in. They typically describe B2B communication between
// different Processes in different Pools. When Messages need to reach a specific
// Process instance, correlation is used to identify the particular instance.
// Signals are triggers generated in the Pool they are published. They are
// typically used for broadcast communication within and across Processes, across
// Pools, and between Process diagrams.
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
type Event struct {
	flow.Node

	// Modeler-defined properties MAY be added to an Event. These properties are
	// contained within the Event.
	Properties []data.Property
}

type CatchEvent struct {
	Event

	// References the reusable EventDefinitions that are triggers expected for a
	// catch Event. Reusable EventDefinitions are defined as top-level elements.
	// These EventDefinitions can be shared by different catch and throw Events.
	//   • If there is no EventDefinition defined, then this is considered a
	//     catch None Event and the Event will not have an internal marker.
	//   • If there is more than one EventDefinition defined, this is
	//     considered a Catch Multiple Event and the Event will have the pentagon
	//     internal marker (see Figure 10.90).
	// This is an ordered set.
	EventDefitionsRefs []*EventDefition

	// Defines the event EventDefinitions that are triggers expected for a catch
	// Event. These EventDefinitions are only valid inside the current Event.
	//   • If there is no EventDefinition defined, then this is considered a
	//     catch None Event and the Event will not have an internal marker.
	//   • If there is more than one EventDefinition defined, this is
	//     considered a catch Multiple Event and the Event will have the
	//     pentagon internal marker.
	// This is an ordered set.
	EventDefitions []*EventDefition

	// The Data Associations of the catch Event. The dataOutputAssociation of a
	// catch Event is used to assign data from the Event to a data element that
	// is in the scope of the Event.
	// For a catch Multiple Event, multiple Data Associations might be REQUIRED,
	// depending on the individual triggers of the Event.
	OutputAssociations []*data.Association

	// The Data Outputs for the catch Event. This is an ordered set.
	DataOutputs []*data.Output

	// The OutputSet for the catch Event.
	OutputSet *data.OutputSet

	// This attribute is only relevant when the catch Event has more than
	// EventDefinition (Multiple). If this value is true, then all of the types
	// of triggers that are listed in the catch Event MUST be triggered before
	// the Process is instantiated.
	ParallelMultiple bool
}

type ThrowEvent struct {
	Event

	// References the reusable EventDefinitions that are triggers expected for a
	// catch Event. Reusable EventDefinitions are defined as top-level elements.
	// These EventDefinitions can be shared by different catch and throw Events.
	//   • If there is no EventDefinition defined, then this is considered a
	//     catch None Event and the Event will not have an internal marker.
	//   • If there is more than one EventDefinition defined, this is
	//     considered a Catch Multiple Event and the Event will have the pentagon
	//     internal marker (see Figure 10.90).
	// This is an ordered set.
	EventDefitionsRefs []*EventDefition

	// Defines the event EventDefinitions that are triggers expected for a catch
	// Event. These EventDefinitions are only valid inside the current Event.
	//   • If there is no EventDefinition defined, then this is considered a
	//     catch None Event and the Event will not have an internal marker.
	//   • If there is more than one EventDefinition defined, this is
	//     considered a catch Multiple Event and the Event will have the
	//     pentagon internal marker.
	// This is an ordered set.
	EventDefitions []*EventDefition

	// The Data Associations of the throw Event. The dataInputAssociation of a
	// throw Event is responsible for the assignment of a data element that is
	// in scope of the Event to the Event data.
	// For a throw Multiple Event, multiple Data Associations might be REQUIRED,
	// depending on the individual results of the Event.
	InputAssociations []*data.Association

	// The Data Inputs for the throw Event. This is an ordered set.
	DataInputs []*data.Input

	// The InputSet for the throw Event.
	OutputSets []*data.OutputSet
}

type EventDefition struct{}
