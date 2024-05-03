package data

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

// Data Associations are used to move data between Data Objects, Properties, and
// inputs and outputs of Activities, Processes, and GlobalTasks. Tokens do not
// flow along a Data Association, and as a result they have no direct effect on
// the flow of the Process. The purpose of retrieving data from Data Objects or
// Process Data Inputs is to fill the Activities inputs and later push the
// output values from the execution of the Activity back into Data Objects or
// Process Data Outputs.
//
// The core concepts of a DataAssociation are that they have sources, a target,
// and an optional transformation.
// When a data association is “executed,” data is copied to the target. What is
// copied depends if there is a transformation defined or not.
// If there is no transformation defined or referenced, then only one source
// MUST be defined, and the contents of this source will be copied into the
// target.
//
// If there is a transformation defined or referenced, then this transformation
// Expression will be evaluated and the result of the evaluation is copied into
// the target. There can be zero (0) to many sources defined in this case, but
// there is no requirement that these sources are used inside the Expression.
// In any case, sources are used to define if the data association can be
// “executed,” if any of the sources is in the state of “unavailable,” then the
// data association cannot be executed, and the Activity or Event where the data
// association is defined MUST wait until this condition is met.
// Data Associations are always contained within another element that defines
// when these data associations are going to be executed. Activities define two
// sets of data associations, while Events define only one.
// For Events, there is only one set, but they are used differently for catch or
// throw Events. For a catch Event, data associations are used to push data from
// the Message received into Data Objects and properties. For a throw Event, data
// associations are used to fill the Message that is being thrown.
// As DataAssociations are used in different stages of the Process and Activity
// lifecycle, the possible sources and targets vary according to that stage. This
// defines the scope of possible elements that can be referenced as source and
// target. For example: when an Activity starts executing, the scope of valid
// targets include the Activity data inputs, while at the end of the Activity
// execution, the scope of valid sources include Activity data outputs.
type Association struct {
	foundation.BaseElement

	// Specifies an optional transformation Expression. The actual scope of
	// accessible data for that Expression is defined by the source and target of
	// the specific Data Association types.
	Transformation *Expression

	// Specifies one or more data elements Assignments. By using an Assignment,
	// single data structure elements can be assigned from the source structure
	// to the target structure.
	Assignments []*Assignment

	// Identifies the source of the Data Association. The source MUST be an
	// ItemAwareElement.
	Source []*ItemAwareElement

	// Identifies the target of the Data Association. The target MUST be an
	// ItemAwareElement.
	Target *ItemAwareElement
}

// The Assignment class is used to specify a simple mapping of data elements
// using a specified Expression language.
// The default Expression language for all Expressions is specified in the
// Definitions element, using the expressionLanguage attribute. It can also be
// overridden on each individual Assignment using the same attribute.
type Assignment struct {
	foundation.BaseElement

	// The Expression that evaluates the source of the Assignment.
	From *Expression

	// The Expression that defines the actual Assignment operation and the
	// target data element.
	To *Expression
}

// Update updates the Target of the Association a with new value from
// ItemDefintion iDef.
func (a *Association) Update(iDef *ItemDefinition) error {
	panic("not implemented yet")

	// return nil
}
