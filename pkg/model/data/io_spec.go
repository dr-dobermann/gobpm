package data

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// Activities and Processes often need data in order to execute. In addition
// they can produce data during or as a result of execution. Data requirements
// are captured as Data Inputs and InputSets. Data that is produced is captured
// using Data Outputs and OutputSets. These elements are aggregated in a
// InputOutputSpecification class.
// Certain Activities and CallableElements contain a InputOutputSpecification
// element to describe their data requirements. Execution semantics are defined
// for the InputOutputSpecification and they apply the same way to all elements
// that extend it. Not every Activity type defines inputs and outputs, only
// Tasks, CallableElements (Global Tasks and Processes) MAY define their data
// requirements. Embedded Sub-Processes MUST NOT define Data Inputs and Data
// Outputs directly, however they MAY define them indirectly via
// MultiInstanceLoopCharacteristics.
type InputOutputSpecification struct {
	foundation.BaseElement

	// A reference to the InputSets defined by the InputOutputSpecification.
	// Every InputOutputSpecification MUST define at least one InputSet.
	InputSets []*DataSet

	// A reference to the OutputSets defined by the InputOutputSpecification.
	// Every Data Interface MUST define at least one OutputSet.
	OutputSets []*DataSet

	// An optional reference to the Data Inputs of the InputOutputSpecification.
	// If the InputOutputSpecification defines no Data Input, it means no data
	// is REQUIRED to start the Activity. This is an ordered set.
	DataInputs []*Parameter

	// An optional reference to the Data Outputs of the
	// InputOutputSpecification. If the InputOutputSpecification defines no Data
	// Output, it means no data is REQUIRED to finish the Activity. This is an
	// ordered set.
	DataOutputs []*Parameter
}

type SetType uint8

const (
	InvalidSet        SetType = iota
	DefaultSet        SetType = 1 << iota
	OptionalSet       SetType = 1 << iota
	WhileExecutionSet SetType = 1 << iota

	AllSets SetType = DefaultSet | OptionalSet | WhileExecutionSet
)

func (st SetType) String() string {
	return []string{
		"InvalidSet",
		"DefaultSet",
		"OptionalSet",
		"WhileExecutionSet",
	}[st]
}

// checkSetType tests if the st is a proper SetType.
func checkSetType(st SetType) error {
	if st&AllSets != st {
		return fmt.Errorf("invalid DataSet type: %d", st)
	}

	return nil
}

// A Data Input is a declaration that a particular kind of data will be used as
// input of the InputOutputSpecification. There may be multiple Data Inputs
// associated with an InputOutputSpecification.
// The Data Input is an item-aware element. Data Inputs are visually displayed
// on a Process diagram to show the inputs to the top-level Process or to show
// the inputs of a called Process (i.e., one that is referenced by a Call
// Activity, where the Call Activity has been expanded to show the called
// Process within the context of a calling Process).
// type Input struct {
// ItemAwareElement

// A descriptive name for the element.
// name string

// A DataInput is used in one or more InputSets. This attribute is derived
// from the InputSets.
// inputSets []*InputSet

// Each InputSet that uses this DataInput can determine if the Activity can
// start executing with this DataInput state in “unavailable.” This
// attribute lists those InputSets.
// inputSetWithOptional []*InputSet

// Each InputSet that uses this DataInput can determine if the Activity can
// evaluate this DataInput while executing. This attribute lists those
// InputSets.
// inputSetWithWhileExecution []*InputSet

// Defines if the DataInput represents a collection of elements. It is
// needed when no itemDefinition is referenced. If an itemDefinition is
// referenced, then this attribute MUST have the same value as the
// isCollection attribute of the referenced itemDefinition. The default
// value for this attribute is false.
// isCollection bool
// }
