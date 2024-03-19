package data

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// *****************************************************************************
//
// Parameter implements both Input and Output classes of BPMNv2.
type ParameterType string

const (
	InputParameter  ParameterType = "INPUT"
	OutputParameter ParameterType = "OUTPUT"
)

// checkParamType test pt on validity and return error on failure.
func checkParamType(pt ParameterType) error {
	if pt != InputParameter && pt != OutputParameter {
		return errs.New(
			errs.M("invalid parameter type: %q", pt),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
}

// not reverses ParameterType.
func not(pt ParameterType) ParameterType {
	if pt == InputParameter {
		return OutputParameter
	}

	return InputParameter
}

// *****************************************************************************
//
// Parameter is type which is used as substitue for DataInput and DataOutput
// BPMN types.
type Parameter struct {
	ItemAwareElement

	name string

	sets map[SetType][]*DataSet
}

// NewParameter creates a new Parameter and returns its pointer on success or
// error on failure.
func NewParameter(name string, iae *ItemAwareElement) (*Parameter, error) {
	name = trim(name)

	if name == "" {
		return nil,
			errs.New(
				errs.M("name shouldn't be empty"),
				errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
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
			sets:             map[SetType][]*DataSet{},
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

func (p *Parameter) Sets(st SetType) map[SetType][]*DataSet {
	res := map[SetType][]*DataSet{}

	for t, ss := range p.sets {
		if t&st == t {
			res[t] = append([]*DataSet{}, ss...)
		}
	}

	return res
}

// addSet adds new DataSet which references onto the Parameter.
func (p *Parameter) addSet(s *DataSet, where SetType) error {
	if err := checkSetType(where, SingleType); err != nil {
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
		p.sets[where] = []*DataSet{s}

		return nil
	}

	if ind := index(s, ss); ind == -1 {
		p.sets[where] = append(ss, s)
	}

	return nil
}

// removeSet removes the DataSet references on the Parameter.
func (p *Parameter) removeSet(s *DataSet, from SetType) error {
	if err := checkSetType(from, SingleType); err != nil {
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

	ind := index(s, ss)
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
// DataSet implements both InputSet and OutputSet of BPMNv2
type DataSet struct {
	foundation.BaseElement

	name string

	values map[SetType][]*Parameter

	// valid keeps validity flag of the DataSet. It could be changed
	// by Validate call.
	// To check validity call IsValid.
	valid bool

	// linkedSets holds Input/Output rule that defines which OutputSet is
	// expected to be created by the Activity when this InputSet became valid.
	// This attribute is paired with the inputSetRefs attribute of OutputSets.
	//
	// Specifies an Input/Output rule that defines which InputSet has to
	// become valid to expect the creation of this OutputSet. This attribute is
	// paired with the outputSetRefs attribute of InputSets.
	//
	// This combination replaces the IORules attribute for Activities in
	// BPMN 1.2.
	linkedSets []*DataSet
}

// NewDataSet creates a new DataSet and returns its pointer on succes or
// error on failure
func NewDataSet(name string, baseOpts ...options.Option) (*DataSet, error) {
	name = trim(name)

	if name == "" {
		return nil,
			errs.New(
				errs.M("name shouldn't be empty"),
				errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &DataSet{
			BaseElement: *be,
			name:        name,
			values:      map[SetType][]*Parameter{},
			linkedSets:  []*DataSet{},
		},
		nil
}

// MustDataSet creates a new DataSet and returns its pointer. On error it
// panics.
func MustDataSet(name string, baseOpts ...options.Option) *DataSet {
	s, err := NewDataSet(name, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return s
}

// Name returns the DataSet name.
func (s *DataSet) Name() string {
	return s.name
}

// Parameters returns parameters of one or a few set types.
// If there is no values of such type it returns error.
func (s *DataSet) Parameters(from SetType) (map[SetType][]*Parameter, error) {
	if err := checkSetType(from, CombinedTypes); err != nil {
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

// AddParameter add non-empyt parameter into the selected DataSet.
// It checks if there is already parameter with equal name but different
// id. In this case error returned.
// If Id and Name of p Parameter equela to a saved one, then no error
// returned.
func (s *DataSet) AddParameter(p *Parameter, where SetType) error {
	if err := checkSetType(where, CombinedTypes); err != nil {
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
		if where&st == st {
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
	}

	return nil
}

// RemoveParameter removes non-empty parameter from the DataSet and
// removes the reference on the DataSet from the Parameter.
// If values of that type isn't existed error returned.
func (s *DataSet) RemoveParameter(p *Parameter, from SetType) error {
	if err := checkSetType(from, CombinedTypes); err != nil {
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

		index := index(p, vv)
		if index != -1 {
			if err := p.removeSet(s, st); err != nil {
				return err
			}

			s.values[st] = append(vv[:index], vv[index+1:]...)
		}
	}

	return nil
}

// Clear removes all parameters from the DataSet.
func (s *DataSet) Clear() error {
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

// Link links the ds DataSet to the s DataSet.
func (s *DataSet) Link(ds *DataSet) error {
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

	if idx := index(ds, s.linkedSets); idx == -1 {
		s.linkedSets = append(s.linkedSets, ds)
	}

	return nil
}

// Unlink removes ds from s linked data sets.
func (s *DataSet) Unlink(ds *DataSet) error {
	if ds == nil {
		return errs.New(
			errs.M("couldn't unlink empty dataset"),
			errs.C(errorClass, errs.InvalidParameter, errs.EmptyNotAllowed))
	}

	idx := index(ds, s.linkedSets)
	if idx == -1 {
		return errs.New(
			errs.M("data set isn't linked"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	s.linkedSets = append(s.linkedSets[:idx], s.linkedSets[idx+1:]...)

	return nil
}

// LinkedSets returns linked to the s data sets.
func (s *DataSet) LinkedSets() []*DataSet {
	return append([]*DataSet{}, s.linkedSets...)
}

// IsValid returns the DataSet's validity flag.
func (s *DataSet) IsValid() bool {
	return s.valid
}

// Validate checks DataSet for validness.
// It uses given readyState DataState to compare with every Parameter state.
// If readyState is nil, then data.ReadyDataState is used (if set).
//
// By default Validate checks only DefaultSet dataSet.
// if executionFinished flag is true, then WhileExecutionSet is also checked.
//
// If the desired SetType parameter set is empty, check is successful.
func (s *DataSet) Validate(
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

	if executionFinished == true {
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
