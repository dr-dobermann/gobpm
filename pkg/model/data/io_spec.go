package data

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// *****************************************************************************
//
// SetType uses to set DataSet
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

// *****************************************************************************
//
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
	inputSets []*DataSet

	// A reference to the OutputSets defined by the InputOutputSpecification.
	// Every Data Interface MUST define at least one OutputSet.
	outputSets []*DataSet

	// An optional reference to the Data Inputs of the InputOutputSpecification.
	// If the InputOutputSpecification defines no Data Input, it means no data
	// is REQUIRED to start the Activity. This is an ordered set.
	dataInputs []*Parameter

	// An optional reference to the Data Outputs of the
	// InputOutputSpecification. If the InputOutputSpecification defines no Data
	// Output, it means no data is REQUIRED to finish the Activity. This is an
	// ordered set.
	dataOutputs []*Parameter
}

// NewIOSpec creates a new InputOutputSpecification and returns its pointer on
// success or error on failure.
func NewIOSpec(baseOpts ...options.Option) (*InputOutputSpecification, error) {
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &InputOutputSpecification{
			BaseElement: *be,
			inputSets:   []*DataSet{},
			outputSets:  []*DataSet{},
			dataInputs:  []*Parameter{},
			dataOutputs: []*Parameter{},
		},
		nil
}

// AddParameter add input or output non-empty parameter ot the
// InptutOutputSpecification.
// It returns error on failure
func (ios *InputOutputSpecification) AddParameter(
	p *Parameter,
	pt ParameterType,
	where SetType,
) error {
	if p == nil {
		return errs.New(
			errs.M("no parameter"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	return nil
}
