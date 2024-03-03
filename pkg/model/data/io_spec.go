package data

import (
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
	InputSets []InputSet

	// A reference to the OutputSets defined by the InputOutputSpecification.
	// Every Data Interface MUST define at least one OutputSet.
	OutputSets []OutputSet

	// An optional reference to the Data Inputs of the InputOutputSpecification.
	// If the InputOutputSpecification defines no Data Input, it means no data
	// is REQUIRED to start the Activity. This is an ordered set.
	DataInputs []Input

	// An optional reference to the Data Outputs of the
	// InputOutputSpecification. If the InputOutputSpecification defines no Data
	// Output, it means no data is REQUIRED to finish the Activity. This is an
	// ordered set.
	DataOutputs []Output
}

type SetType uint8

const (
	DefaultSet        SetType = 1 << (iota + 1)
	OptionalSet       SetType = 1 << (iota + 1)
	WhileExecutionSet SetType = 1 << (iota + 1)

	AllSets SetType = DefaultSet | OptionalSet | WhileExecutionSet
)
