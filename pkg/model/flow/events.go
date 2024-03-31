package flow

import (
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type EventClass string

const (
	StartEventClass        EventClass = "Start"
	IntermediateEventClass EventClass = "Intermediate"
	EndEventClass          EventClass = "End"
)

type EventTrigger string

// Multiple and ParallelMultiple have no direct trigger since they are
// calculated based on event definitions.
// As well, None trigger also isn't existed since it appears with empty
// Definitions list.
const (
	// Common Start and End events triggers
	// TriggerNone    Trigger = "None"
	TriggerMessage EventTrigger = "Message"
	TriggerSignal  EventTrigger = "Signal"
	// TriggerMultiple Trigger = "Multiple"

	// Only Start events triggers
	TriggerTimer       EventTrigger = "Timer"
	TriggerConditional EventTrigger = "Conditional"
	// TriggerParallelMultiple Trigger = "ParallelMultiple"

	// Only End events triggers
	TriggerError        EventTrigger = "Error"
	TriggerEscalation   EventTrigger = "Escalation"
	TriggerCancel       EventTrigger = "Cancel"
	TriggerCompensation EventTrigger = "Compensation"
	TriggerTerminate    EventTrigger = "Terminate"

	// Only Intermediate events triggers
	TriggerLink EventTrigger = "Link"
)

type EventDefinition interface {
	foundation.Identifyer
	foundation.Documentator

	Type() EventTrigger
}

type EventNode interface {
	Node

	Definitions() []EventDefinition
	EventType() string
	EventClass() EventClass
}
