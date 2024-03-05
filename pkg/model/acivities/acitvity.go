package acivities

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// The Activity class is the abstract super class for all concrete Activity
// types.
type Activity struct {
	flow.Element

	// A flag that identifies whether this Activity is intended for the
	// purposes of compensation.
	// If false, then this Activity executes as a result of normal execution
	// flow.
	// If true, this Activity is only activated when a Compensation Event is
	// detected and initiated under Compensation Event visibility scope
	isForCompensation bool

	// An Activity MAY be performed once or MAY be repeated. If repeated,
	// the Activity MUST have loopCharacteristics that define the repetition
	// criteria (if the isExecutable attribute of the Process is set to true).
	loopCharacteristics LoopCharacteristics

	// Defines the resource that will perform or will be responsible for the
	// Activity. The resource, e.g., a performer, can be specified in the form
	// of a specific individual, a group, an organization role or position, or
	// an organization.
	resources []ResourceRole

	// The Sequence Flow that will receive a token when none of the
	// conditionExpressions on other outgoing Sequence Flows evaluate to true.
	// The default Sequence Flow should not have a conditionExpression. Any
	// such Expression SHALL be ignored.
	defaultFlow *flow.SequenceFlow

	// Modeler-defined properties MAY be added to an Activity. These
	// properties are contained within the Activity.
	properties []data.Property

	// The default value is 1. The value MUST NOT be less than 1. This attribute
	// defines the number of tokens that MUST arrive before the Activity can
	// begin.
	// Note that any value for the attribute that is greater than 1 is an
	// advanced type of modeling and should be used with caution.
	startQuantity int

	// The default value is 1. The value MUST NOT be less than 1. This attribute
	// defines the number of tokens that MUST be generated from the Activity.
	// This number of tokens will be sent done any outgoing Sequence Flow
	// (assuming any Sequence Flow conditions are satisfied).
	// Note that any value for the attribute that is greater than 1 is an
	// advanced type of modeling and should be used with caution.
	completionQuantity int

	// The InputOutputSpecification defines the inputs and outputs and the
	// InputSets and OutputSets for the Activity. See page 210 for more
	// information on the InputOutputSpecification.
	ioSpec data.InputOutputSpecification

	// This references the Intermediate Events that are attached to the
	// boundary of the Activity.
	boundaryEvents []flow.EventNode

	// An optional reference to the DataInputAssociations.
	// A DataInputAssociation defines how the DataInput of the Activity’s
	// InputOutputSpecification will be populated.
	dataInputAssociations []*data.Association

	// An optional reference to the DataOutputAssociations.
	dataOutputAssociations []*data.Association
}
