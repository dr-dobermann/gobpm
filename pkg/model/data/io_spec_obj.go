package data

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/helpers"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// *****************************************************************************
//
// Parameter implements bothelpers.Input and Output classes of BPMNv2.
type Direction string

const (
	Input  Direction = "INPUT"
	Output Direction = "OUTPUT"
)

// checkParamType test pt on validity and return error on failure.
func (dir Direction) Validate() error {
	if dir != Input && dir != Output {
		return errs.New(
			errs.M("invalid direction: %q", dir),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
}

// not reverses Direction.
func Opposite(dir Direction) Direction {
	if dir == Input {
		return Output
	}

	return Input
}

// *****************************************************************************
//
// Parameter is type which is used as substitute for DataInput and DataOutput
// BPMN types.
type Parameter struct {
	ItemAwareElement

	name string

	sets map[SetType][]*Set
}

// NewParameter creates a new Parameter and returns its pointer on success or
// error on failure.
func NewParameter(name string, iae *ItemAwareElement) (*Parameter, error) {
	name = helpers.Strim(name)

	if err := helpers.CheckStr(
		name,
		"name shouldn't be empty",
		errorClass,
	); err != nil {
		return nil, err
	}

	if iae == nil {
		return nil,
			errs.New(
				errs.M("ItemAwareElement should be provided"),
				errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	return &Parameter{
			ItemAwareElement: *iae,
			name:             name,
			sets:             map[SetType][]*Set{},
		},
		nil
}

// MustParameter creates a new Parameter and returns its pointer or panics on
// failure.
func MustParameter(name string, iae *ItemAwareElement) *Parameter {
	p, err := NewParameter(name, iae)
	if err != nil {
		errs.Panic(err.Error())
	}

	return p
}

// Name returns the Parameter's name.
func (p *Parameter) Name() string {
	return p.name
}

func (p *Parameter) Sets(st SetType) map[SetType][]*Set {
	res := map[SetType][]*Set{}

	for t, ss := range p.sets {
		if t&st == t {
			res[t] = append([]*Set{}, ss...)
		}
	}

	return res
}

// addSet adds new Set which references onto the Parameter.
func (p *Parameter) addSet(s *Set, where SetType) error {
	if err := where.Validate(SingleType); err != nil {
		return errs.New(
			errs.M("invalid data set type (%d)", where),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if s == nil {
		return errs.New(
			errs.M("data set should be provided"),
			errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	ss, ok := p.sets[where]
	if !ok {
		p.sets[where] = []*Set{s}

		return nil
	}

	if ind := helpers.Index(s, ss); ind == -1 {
		p.sets[where] = append(ss, s)
	}

	return nil
}

// removeSet removes the Set references on the Parameter.
func (p *Parameter) removeSet(s *Set, from SetType) error {
	if err := from.Validate(SingleType); err != nil {
		return errs.New(
			errs.M("invalid data set type (%d)", from),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if s == nil {
		return errs.New(
			errs.M("data set should be provided"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	ss, ok := p.sets[from]
	if !ok || ss == nil {
		return errs.New(
			errs.M("parameter doesn't belong to data set"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("parameter_name", p.name),
			errs.D("set_type", from.String()),
			errs.D("set_name", s.name))
	}

	ind := helpers.Index(s, ss)
	if ind == -1 {
		return errs.New(
			errs.M("parameter %q doesn't belong to data set %q",
				p.name, s.name),
			errs.C(errorClass, errs.ConditionFailed))
	}

	p.sets[from] = append(ss[:ind], ss[ind+1:]...)

	return nil
}

// *****************************************************************************
//
// Set implements bothelpers.InputSet and OutputSet of BPMNv2
type Set struct {
	foundation.BaseElement

	name string

	values map[SetType][]*Parameter

	// valid keeps validity flag of the Set. It could be changed
	// by Validate call.
	// To check validity call IsValid.
	valid bool

	// linkedSets holds Input/Output rule that defines which OutputSet is
	// expected to be created by the Activity when this InputSet became valid.
	// This attribute is paired with the inputSetRefs attribute of OutputSets.
	//
	// Specifies an Input/Output rule that defines whichelpers.InputSet has to
	// become valid to expect the creation of this OutputSet. This attribute is
	// paired with the outputSetRefs attribute of InputSets.
	//
	// This combination replaces the IORules attribute for Activities in
	// BPMN 1.2.
	linkedSets []*Set
}

// NewSet creates a new Set and returns its pointer on succes or
// error on failure
func NewSet(name string, baseOpts ...options.Option) (*Set, error) {
	name = helpers.Strim(name)

	if err := helpers.CheckStr(
		name,
		"name shouldn't be empty",
		errorClass,
	); err != nil {
		return nil, err
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &Set{
			BaseElement: *be,
			name:        name,
			values:      map[SetType][]*Parameter{},
			linkedSets:  []*Set{},
		},
		nil
}

// MustSet creates a new Set and returns its pointer. On error it
// panics.
func MustSet(name string, baseOpts ...options.Option) *Set {
	s, err := NewSet(name, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return s
}

// Name returns the Set name.
func (s *Set) Name() string {
	return s.name
}

// Parameters returns parameters of one or a few set types.
// If there is no values of such type it returns error.
func (s *Set) Parameters(from SetType) (map[SetType][]*Parameter, error) {
	if err := from.Validate(CombinedTypes); err != nil {
		return nil,
			errs.New(
				errs.M("invalid data set type (%d)", from),
				errs.C(errorClass, errs.InvalidParameter))
	}

	res := map[SetType][]*Parameter{}

	for _, st := range allTypes() {
		if st&from == st {
			ds, ok := s.values[st]
			if ok {
				res[st] = append([]*Parameter{}, ds...)
			}
		}
	}

	return res, nil
}

// AddParameter add non-empyt parameter into the selected Set.
// It checks if there is already parameter with equal name but different
// id. In this case error returned.
// If Id and Name of p Parameter equela to a saved one, then no error
// returned.
func (s *Set) AddParameter(p *Parameter, where SetType) error {
	if err := where.Validate(CombinedTypes); err != nil {
		return errs.New(
			errs.M("invalid data set type/types combination (%d)", where),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if p == nil {
		return errs.New(
			errs.M("parameter should be provided"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	for _, st := range allTypes() {
		if where&st != st {
			continue
		}

		vv, ok := s.values[st]
		if !ok {
			vv = []*Parameter{}
		}

		for _, v := range vv {
			if v.name == p.name {
				if v.Id() != p.Id() {
					return errs.New(
						errs.M("data set already has parameter with the name %q",
							v.name),
						errs.C(errorClass, errs.InvalidParameter,
							errs.DuplicateObject))
				}

				return nil
			}
		}

		if err := p.addSet(s, st); err != nil {
			return err
		}

		s.values[st] = append(vv, p)
	}

	return nil
}

// RemoveParameter removes non-empty parameter from the Set and
// removes the reference on the Set from the Parameter.
// If values of that type isn't existed error returned.
func (s *Set) RemoveParameter(p *Parameter, from SetType) error {
	if err := from.Validate(CombinedTypes); err != nil {
		return errs.New(
			errs.M("invalid data set type (%d)", from),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if p == nil {
		return errs.New(
			errs.M("parameter should be provided"),
			errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	for _, st := range allTypes() {
		vv, ok := s.values[st]
		if !ok || len(vv) == 0 {
			continue
		}

		index := helpers.Index(p, vv)
		if index != -1 {
			if err := p.removeSet(s, st); err != nil {
				return err
			}

			s.values[st] = append(vv[:index], vv[index+1:]...)
		}
	}

	return nil
}

// Clear removes all parameters from the Set.
func (s *Set) Clear() error {
	for st, paramList := range s.values {
		// make a copy of parameters and delete them all
		pp := append([]*Parameter{}, paramList...)
		for _, p := range pp {
			if err := s.RemoveParameter(p, st); err != nil {
				return err
			}
		}
	}

	return nil
}

// Link links the ds Set to the s Set.
func (s *Set) Link(ds *Set) error {
	if ds == nil {
		return errs.New(
			errs.M("couldn't link empty dataset"),
			errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	if ds == s {
		return errs.New(
			errs.M("couldn't link to itself"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if idx := helpers.Index(ds, s.linkedSets); idx == -1 {
		s.linkedSets = append(s.linkedSets, ds)
	}

	return nil
}

// Unlink removes ds from s linked data sets.
func (s *Set) Unlink(ds *Set) error {
	if ds == nil {
		return errs.New(
			errs.M("couldn't unlink empty dataset"),
			errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	idx := helpers.Index(ds, s.linkedSets)
	if idx == -1 {
		return errs.New(
			errs.M("data set isn't linked"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	s.linkedSets = append(s.linkedSets[:idx], s.linkedSets[idx+1:]...)

	return nil
}

// LinkedSets returns linked to the s data sets.
func (s *Set) LinkedSets() []*Set {
	return append([]*Set{}, s.linkedSets...)
}

// IsValid returns the Set's validity flag.
func (s *Set) IsValid() bool {
	return s.valid
}

// Validate checks Set for validness.
// It uses given readyState DataState to compare with every Parameter state.
// If readyState is nil, then data.ReadyDataState is used (if set).
//
// By default Validate checks only DefaultSet dataSet.
// if executionFinished flag is true, then WhileExecutionSet is also checked.
//
// If the desired SetType parameter set is empty, check is successful.
func (s *Set) Validate(
	readyState *DataState,
	executionFinished bool,
) error {
	rs := readyState

	s.valid = false

	if readyState == nil {
		rs = ReadyDataState
		if rs == nil {
			return errs.New(
				errs.M("default ready state isn't initialized "+
					"(use data.CreateDefaultStates to initialize)"),
				errs.C(errorClass, errs.InvalidParameter))
		}
	}

	if _, ok := s.values[DefaultSet]; ok {
		if err := checkParamsState(
			rs,
			s.values[DefaultSet],
			DefaultSet); err != nil {
			return err
		}
	}

	if executionFinished {
		if _, ok := s.values[WhileExecutionSet]; ok {
			if err := checkParamsState(
				rs,
				s.values[WhileExecutionSet],
				WhileExecutionSet); err != nil {
				return err
			}
		}
	}

	s.valid = true

	return nil
}

// checkParamState checks set of parameter on rs DataState equality.
// If any parameter DataSate is differs from rs, then error returned.
func checkParamsState(rs *DataState, pp []*Parameter, sType SetType) error {
	for _, p := range pp {
		if p.dataState.Id() != rs.Id() {
			return errs.New(
				errs.M("parameter has not desired state"),
				errs.C(errorClass, errs.ConditionFailed),
				errs.D("parameter_name", p.name),
				errs.D("data_set", sType.String()),
				errs.D("requested_state", rs.name),
				errs.D("parameter_state", p.State().name))
		}
	}

	return nil
}

// Interfaces check for Parameter
var _ Data = (*Parameter)(nil)
