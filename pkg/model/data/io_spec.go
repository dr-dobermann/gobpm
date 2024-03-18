package data

import (
	"errors"
	"fmt"
	"strconv"

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
	if err := checkSetType(st, SingleType); err != nil {
		errs.Panic("ivalid set type: " + strconv.Itoa(int(st)))
	}

	return map[SetType]string{
		InvalidSet:        "InvalidSet",
		DefaultSet:        "DefaultSet",
		OptionalSet:       "OptionalSet",
		WhileExecutionSet: "WhileExecutionSet",
	}[st]
}

// allTypes returns all valid set types list.
func allTypes() []SetType {
	return []SetType{DefaultSet, OptionalSet, WhileExecutionSet}
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
	// inputSets []*DataSet

	// A reference to the OutputSets defined by the InputOutputSpecification.
	// Every Data Interface MUST define at least one OutputSet.
	// outputSets []*DataSet

	sets map[ParameterType][]*DataSet

	// An optional reference to the Data Inputs of the InputOutputSpecification.
	// If the InputOutputSpecification defines no Data Input, it means no data
	// is REQUIRED to start the Activity. This is an ordered set.
	// dataInputs []*Parameter

	// An optional reference to the Data Outputs of the
	// InputOutputSpecification. If the InputOutputSpecification defines no Data
	// Output, it means no data is REQUIRED to finish the Activity. This is an
	// ordered set.
	// dataOutputs []*Parameter

	params map[ParameterType][]*Parameter
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
			sets:        map[ParameterType][]*DataSet{},
			params:      map[ParameterType][]*Parameter{},
		},
		nil
}

// Parameters returns all IOSpec parameters of pt type.
func (ios *InputOutputSpecification) Parameters(
	pt ParameterType,
) ([]*Parameter, error) {
	if err := checkParamType(pt); err != nil {
		return nil, err
	}

	pp, ok := ios.params[pt]
	if !ok {
		return []*Parameter{}, nil
	}

	return append([]*Parameter{}, pp...),
		nil
}

// Validate checks all conditions the InputOutputSpecification should
// comply. If all the  condiitons met, no error returned.
//
// An InputSet is a collection of DataInput elements that together define
// a valid set of data inputs for an InputOutputSpecification. An
// InputOutputSpecification MUST have at least one InputSet element.  An
// InputSet MAY reference zero or more DataInput elements. A single
// DataInput MAY be associated with multiple InputSet elements, but it MUST
// always be referenced by at least one InputSet.
//
// An “empty” InputSet, one that references no DataInput elements, signifies
// that the Activity requires no data to start executing (this implies that
// either there are no data inputs or they are referenced by another input
// set).
//
// InputSet elements are contained by InputOutputSpecification elements;
// the order in which these elements are included defines the order in which
// they will be evaluated.
//
// An OutputSet is a collection of DataOutputs elements that together can be
// produced as output from an Activity or Event. An InputOutputSpecification
// element MUST define at least OutputSet element. An OutputSet MAY reference
// zero or more DataOutput elements. A single DataOutput MAY be associated with
// multiple OutputSet elements, but it MUST always be referenced by at least
// one OutputSet.
//
// An “empty” OutputSet, one that is associated with no DataOutput elements,
// signifies that the ACTIVITY produces no data.
func (ios *InputOutputSpecification) Validate() error {
	ee := []error{}

	names := map[ParameterType]struct {
		setName, dataName string
	}{
		InputParameter: {
			setName:  "InputSet",
			dataName: "DataInput",
		},
		OutputParameter: {
			setName:  "OutputSet",
			dataName: "DataOutput",
		}}

	for _, tp := range []ParameterType{InputParameter, OutputParameter} {
		iss, ok := ios.sets[tp]
		if !ok || len(iss) == 0 {
			ee = append(ee,
				fmt.Errorf(
					"the InputOutputSpecification should have at least one %s",
					names[tp].setName))
		} else {
			// take all params
			ipp, ok := ios.params[tp]
			if ok {
				// for every param
				for _, p := range ipp {
					links := 0

					// take all its set
					for _, ss := range p.Sets(AllSets) {
						// for every set
						for _, s := range ss {
							// check if it belongs to the same
							// type as the parameter
							if idx := index(s, ios.sets[tp]); idx != -1 {
								links++
							}
						}
					}

					if links == 0 {
						ee = append(ee,
							fmt.Errorf(
								"the %s %q should be assigned to %s",
								names[tp].dataName, p.name, names[tp].setName))
					}
				}

			}
		}
	}

	if len(ee) > 0 {
		return errors.Join(ee...)
	}

	return nil
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

	if err := checkParamType(pt); err != nil {
		return err
	}

	pp, ok := ios.params[pt]
	if !ok {
		ios.params[pt] = []*Parameter{p}

		return nil
	}

	if idx := index(p, pp); idx == -1 {
		ios.params[pt] = append(pp, p)
	}

	return nil
}

// RemoveParameter removes Parameter p of pt type from all sets
// referenced on it and from IOSpec itself.
func (ios InputOutputSpecification) RemoveParameter(
	p *Parameter,
	pt ParameterType,
) error {
	if p == nil {
		return errs.New(
			errs.M("no parameter"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	if err := checkParamType(pt); err != nil {
		return err
	}

	pp, ok := ios.params[pt]
	if !ok || len(pp) == 0 {
		return errs.New(
			errs.M("data set %q is empty", pt),
			errs.C(errorClass, errs.InvalidParameter))
	}

	idx := index(p, pp)
	if idx == -1 {
		return errs.New(
			errs.M("no parameter %q in data set %q", p.name, pt),
			errs.C(errorClass, errs.InvalidParameter))
	}

	// get all data sets, referenced on the parameter and delete that
	// reference.
	sets := p.Sets(AllSets)
	for st, ss := range sets {
		for _, s := range ss {
			if err := s.RemoveParameter(p, st); err != nil {
				return err
			}
		}
	}

	// remove parameter
	ios.params[pt] = append(pp[:idx], pp[idx+1:]...)

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

	// check param type
	if err := checkParamType(pt); err != nil {
		return err
	}

	// check if required set is existed
	ss, ok := ios.sets[pt]
	if !ok {
		ios.sets[pt] = []*DataSet{s}

		return nil
	}

	// check if s isn't used as opposite sets
	if idx := index(s, ios.sets[not(pt)]); idx != -1 {
		return errs.New(
			errs.M("data set is already used as %s set", not(pt)),
			errs.C(errorClass, errs.InvalidParameter, errs.DuplicateObject))
	}

	// check if s isn't registered yet
	if idx := index(s, ss); idx == -1 {
		ios.sets[pt] = append(ss, s)
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

	// check param type
	if err := checkParamType(pt); err != nil {
		return err
	}

	// check if required set is existed
	ss, ok := ios.sets[pt]
	if !ok || len(ss) == 0 {
		return errs.New(
			errs.M("sets list %q is empty", pt),
			errs.C(errorClass, errs.InvalidParameter))
	}

	idx := index(s, ss)
	if idx == -1 {
		return errs.New(
			errs.M("set isn't existed in %q lists", pt),
			errs.C(errorClass, errs.InvalidParameter))
	}

	// clear all existed references on params
	if err := ss[idx].Clear(); err != nil {
		return err
	}

	// remove set
	ios.sets[pt] = append(ios.sets[pt][:idx], ios.sets[pt][idx+1:]...)

	return nil
}

// DataSets returns data sets of pt type.
func (ios *InputOutputSpecification) DataSets(
	pt ParameterType,
) ([]*DataSet, error) {
	// check param type
	if err := checkParamType(pt); err != nil {
		return nil, err
	}

	ss, ok := ios.sets[pt]
	if !ok {
		return []*DataSet{}, nil
	}

	return append([]*DataSet{}, ss...),
		nil
}
