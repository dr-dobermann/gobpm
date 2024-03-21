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
// SetType uses to set Set
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
	if err := st.Validate(SingleType); err != nil {
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
func (st SetType) Validate(single bool) error {
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
	// inputSets []*Set

	// A reference to the OutputSets defined by the InputOutputSpecification.
	// Every Data Interface MUST define at least one OutputSet.
	// outputSets []*Set

	sets map[Direction][]*Set

	// An optional reference to the Data Inputs of the InputOutputSpecification.
	// If the InputOutputSpecification defines no Data Input, it means no data
	// is REQUIRED to start the Activity. This is an ordered set.
	// dataInputs []*Parameter

	// An optional reference to the Data Outputs of the
	// InputOutputSpecification. If the InputOutputSpecification defines no Data
	// Output, it means no data is REQUIRED to finish the Activity. This is an
	// ordered set.
	// dataOutputs []*Parameter

	params map[Direction][]*Parameter
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
			sets:        map[Direction][]*Set{},
			params:      map[Direction][]*Parameter{},
		},
		nil
}

// Parameters returns all IOSpec parameters of dir type.
func (ios *InputOutputSpecification) Parameters(
	dir Direction,
) ([]*Parameter, error) {
	if err := dir.Validate(); err != nil {
		return nil, err
	}

	pp, ok := ios.params[dir]
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

	names := map[Direction]struct {
		setName, dataName string
	}{
		Input: {
			setName:  "InputSet",
			dataName: "DataInput",
		},
		Output: {
			setName:  "OutputSet",
			dataName: "DataOutput",
		},
	}

	for _, tp := range []Direction{Input, Output} {
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
	dir Direction,
) error {
	if p == nil {
		return errs.New(
			errs.M("no parameter"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	if err := dir.Validate(); err != nil {
		return err
	}

	pp, ok := ios.params[dir]
	if !ok {
		ios.params[dir] = []*Parameter{p}

		return nil
	}

	if idx := index(p, pp); idx == -1 {
		ios.params[dir] = append(pp, p)
	}

	return nil
}

// RemoveParameter removes Parameter p of dir type from all sets
// referenced on it and from IOSpec itself.
func (ios InputOutputSpecification) RemoveParameter(
	p *Parameter,
	dir Direction,
) error {
	if p == nil {
		return errs.New(
			errs.M("no parameter"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	if err := dir.Validate(); err != nil {
		return err
	}

	pp, ok := ios.params[dir]
	if !ok || len(pp) == 0 {
		return errs.New(
			errs.M("data set %q is empty", dir),
			errs.C(errorClass, errs.InvalidParameter))
	}

	idx := index(p, pp)
	if idx == -1 {
		return errs.New(
			errs.M("no parameter %q in data set %q", p.name, dir),
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
	ios.params[dir] = append(pp[:idx], pp[idx+1:]...)

	return nil
}

// HasParameter checks if the IOSpec has selected parameter.
func (ios *InputOutputSpecification) HasParameter(
	p *Parameter, d Direction,
) bool {
	if p == nil {
		return false
	}

	if err := d.Validate(); err != nil {
		return false
	}

	for _, iosP := range ios.params[d] {
		if iosP.Id() == p.Id() {
			return true
		}
	}

	return false
}

// AddSet adds single data set into InputOutputSpecification and check
// if it is already exist in selected by dir list of data sets.
// Set could be set only as input or output.
func (ios *InputOutputSpecification) AddSet(
	s *Set,
	dir Direction,
) error {
	if s == nil {
		return errs.New(
			errs.M("no data set"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	// check param type
	if err := dir.Validate(); err != nil {
		return err
	}

	// check if required set is existed
	ss, ok := ios.sets[dir]
	if !ok {
		ios.sets[dir] = []*Set{s}

		return nil
	}

	// check if s isn't used as opposite sets
	if idx := index(s, ios.sets[Opposite(dir)]); idx != -1 {
		return errs.New(
			errs.M("data set is already used as %s set", Opposite(dir)),
			errs.C(errorClass, errs.InvalidParameter, errs.DuplicateObject))
	}

	// check if s isn't registered yet
	if idx := index(s, ss); idx == -1 {
		ios.sets[dir] = append(ss, s)
	}

	return nil
}

// RemoveSet removes non-empty data set and clears all references on it
// from parameters.
func (ios *InputOutputSpecification) RemoveSet(
	s *Set,
	dir Direction,
) error {
	if s == nil {
		return errs.New(
			errs.M("no data set"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	// check param type
	if err := dir.Validate(); err != nil {
		return err
	}

	// check if required set is existed
	ss, ok := ios.sets[dir]
	if !ok || len(ss) == 0 {
		return errs.New(
			errs.M("sets list %q is empty", dir),
			errs.C(errorClass, errs.InvalidParameter))
	}

	idx := index(s, ss)
	if idx == -1 {
		return errs.New(
			errs.M("set isn't existed in %q lists", dir),
			errs.C(errorClass, errs.InvalidParameter))
	}

	// clear all existed references on params
	if err := ss[idx].Clear(); err != nil {
		return err
	}

	// remove set
	ios.sets[dir] = append(ios.sets[dir][:idx], ios.sets[dir][idx+1:]...)

	return nil
}

// Sets returns data sets of dir type.
func (ios *InputOutputSpecification) Sets(
	dir Direction,
) ([]*Set, error) {
	// check param type
	if err := dir.Validate(); err != nil {
		return nil, err
	}

	ss, ok := ios.sets[dir]
	if !ok {
		return []*Set{}, nil
	}

	return append([]*Set{}, ss...),
		nil
}
