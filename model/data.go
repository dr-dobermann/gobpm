<<<<<<< HEAD
package model

type DataState int8

const (
	Unavailable DataState = iota
	Available
)
=======

package model

type DataState uint8
>>>>>>> cd1bb6ab4d496deef6cc2b2baa563bdcafa033d0

type ItemAvareElement struct {
	state DataState
	items []ItemDefinition
}

type DataInput struct {
	ItemAvareElement
<<<<<<< HEAD
	name         string
=======
	name string
>>>>>>> cd1bb6ab4d496deef6cc2b2baa563bdcafa033d0
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
<<<<<<< HEAD
	name         string
	dataInputRef *DataInput
	diItems      []uint
=======
	name string
	dataInputRef *DataInput
	diItems []uint
>>>>>>> cd1bb6ab4d496deef6cc2b2baa563bdcafa033d0
}

type DataOutput struct {
	ItemAvareElement
<<<<<<< HEAD
	name         string
	isCollection bool
	outputSets   []*OutputSet
=======
	name string
	isCollection bool
	outputSets []*OutputSet
>>>>>>> cd1bb6ab4d496deef6cc2b2baa563bdcafa033d0
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
<<<<<<< HEAD
	name       string
	dataOutRef *DataOutput
	doItems    []uint
}

type InputOutputSpecification struct {
	BaseElement
	dataInputs []DataInput
	dataOutput []DataOutput
}
=======
	name string
	dataOutRef *DataOutput
	doItems []uint
}

type InputOutputSpecification {
	BaseElement
	dataInputs []DataInput
	dataOutput []DataOutput
}
>>>>>>> cd1bb6ab4d496deef6cc2b2baa563bdcafa033d0
