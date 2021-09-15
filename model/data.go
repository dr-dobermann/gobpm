
package model

type DataState uint8

type ItemAvareElement struct {
	state DataState
	items []ItemDefinition
}

type DataInput struct {
	ItemAvareElement
	name string
	isCollection bool
	// A DataInput is used in one or more InputSets
	inputSets []*InputSet
	// Each InputSet that uses this DataInput can determine if the Activity
	// can start executing with this DataInput state in “unavailable.” This attribute
	// lists those InputSets
	optionalSets []*InputSet
	// Each InputSet that uses this DataInput can determine if the Activity
	// can evaluate this DataInput while executing. This attribute lists those
	// InputSets.
	evaluatingSets []*InputSet
}

type InputSet struct {
	BaseElement
	name string
	dataInputRef *DataInput
	diItems []uint
}

type DataOutput struct {
	ItemAvareElement
	name string
	isCollection bool
	outputSets []*OutputSet
	// Each OutputSet that uses this DataOutput can determine if the
	// Activity can complete executing without producing this DataInput.
	// This attribute lists those OutputSets
	optionalSets []*OutputSet
	// Each OutputSet that uses this DataInput can determine if the
	// Activity can produce this DataOutput while executing.
	evaluatedSets []*OutputSet
}

type OutputSet struct {
	BaseElement
	name string
	dataOutRef *DataOutput
	doItems []uint
}

type InputOutputSpecification {
	BaseElement
	dataInputs []DataInput
	dataOutput []DataOutput
}