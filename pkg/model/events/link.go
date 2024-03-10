package events

// A Link Event is a mechanism for connecting two sections of a Process. Link
// Events can be used to create looping situations or to avoid long Sequence
// Flow lines. The use of Link Events is limited to a single Process level
// (i.e., they cannot link a parent Process with a Sub-Process).
//
// Paired Link Events can also be used as “Off-Page Connectors” for printing
// a Process across multiple pages. They can also be used as generic “Go To”
// objects within the Process level. There can be multiple source Link Events,
// but there can only be one target Link Event. When used to “catch” from the
// source Link, the Event marker will be unfilled. When used to “throw” to the
// target Link, the Event marker will be filled.
type LinkEventDefinition struct {
	definition

	// If the trigger is a Link, then the name MUST be entered.
	Name string

	// Used to reference the corresponding 'catch' or 'target'
	// LinkEventDefinition, when this LinkEventDefinition represents a 'throw'
	// or 'source' LinkEventDefinition.
	Sources []*LinkEventDefinition

	// Used to reference the corresponding 'throw' or 'source'
	// LinkEventDefinition, when this LinkEventDefinition represents a 'catch'
	// or 'target' LinkEventDefinition.
	Target *LinkEventDefinition
}
