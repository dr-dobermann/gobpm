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

	SingleType    = true
	CombinedTypes = false
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
// If single is true, then checkSetType test st against single
// SetType and fails on combined states.
func checkSetType(st SetType, single bool) error {
	if st&AllSets != st {
		return fmt.Errorf("invalid data set type or types combination: %d", st)
	}

	if single &&
		st != DefaultSet &&
		st != OptionalSet &&
		st != WhileExecutionSet {
		return fmt.Errorf("invalid single data set type %d", st)
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
) error {
	if p == nil {
		return errs.New(
			errs.M("no parameter"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	dd := ios.dataInputs
	if pt == OutputParameter {
		dd = ios.dataOutputs
	}

	if idx := index(p, dd); idx != -1 {
		return nil
	}

	switch pt {
	case InputParameter:
		ios.dataInputs = append(dd, p)

	case OutputParameter:
		ios.dataOutputs = append(dd, p)
	}

	return nil
}

// AddDataSet adds single data set into InputOutputSpecification and check
// if it is already exist in selected by pt list of data sets.
// DataSet could be set only as input or output.
func (ios *InputOutputSpecification) AddDataSet(
	s *DataSet,
	pt ParameterType,
) error {
	if s == nil {
		return errs.New(
			errs.M("no data set"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	ss := ios.inputSets
	if pt == OutputParameter {
		ss = ios.outputSets
	}

	if idx := index(s, ss); idx != -1 {
		return nil
	}

	switch pt {
	case InputParameter:
		if idx := index(s, ios.outputSets); idx != -1 {
			return errs.New(
				errs.M("data set is already used as output data set"),
				errs.C(errorClass, errs.InvalidParameter, errs.DuplicateObject))
		}

		ios.inputSets = append(ss, s)

	case OutputParameter:
		if idx := index(s, ios.inputSets); idx != -1 {
			return errs.New(
				errs.M("data set is already used as input data set"),
				errs.C(errorClass, errs.InvalidParameter, errs.DuplicateObject))
		}

		ios.outputSets = append(ss, s)
	}

	return nil
}

// RemoveDataSet removes non-empty data set and clears all references on it
// from parameters.
func (ios *InputOutputSpecification) RemoveDataSet(
	s *DataSet,
	pt ParameterType,
) error {
	if s == nil {
		return errs.New(
			errs.M("no data set"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	ss := ios.inputSets

	if pt == OutputParameter {
		ss = ios.outputSets
	}

	if idx := index(s, ss); idx == -1 {
		return errs.New(
			errs.M("data set %q isn't found", s.name),
			errs.C(errorClass, errs.InvalidParameter, errs.ObjectNotFound))
	}

	return nil
}

// GetDataSets returns data sets of selected type.
func (ios *InputOutputSpecification) GetDataSets(pt ParameterType) []*DataSet {
	if pt == InputParameter {
		return append([]*DataSet{}, ios.inputSets...)
	}

	return append([]*DataSet{}, ios.outputSets...)
}
