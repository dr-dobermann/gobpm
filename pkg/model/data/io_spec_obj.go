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
			&errs.ApplicationError{
				Message: "name shouldn't be empty",
				Classes: []string{
					errorClass,
					errs.EmptyNotAllowed,
					errs.InvalidParameter}}
	}

	if iae == nil {
		return nil,
			&errs.ApplicationError{
				Message: "ItemAvareElement should be provided for data input",
				Classes: []string{
					errorClass,
					errs.EmptyNotAllowed,
					errs.InvalidParameter}}
	}

	return &Parameter{
			ItemAwareElement: *iae,
			name:             name,
			sets:             map[SetType][]*DataSet{}},
		nil
}

// MustParameter creates a new Parameter and returns its pointer or panics on
// failure.
func MustParameter(name string, iae *ItemAwareElement) *Parameter {
	p, err := NewParameter(name, iae)
	if err != nil {
		panic(err.Error())
	}

	return p
}

// Name returns the Parameter's name.
func (p *Parameter) Name() string {
	return p.name
}

// addSet adds new DataSet which references onto the Parameter.
func (p *Parameter) addSet(s *DataSet, where SetType) error {
	if err := checkSetType(where); err != nil {
		return &errs.ApplicationError{
			Err:     err,
			Message: "Invalid DataSet type",
			Classes: []string{
				errorClass,
				errs.InvalidParameter}}
	}

	if s == nil {
		return &errs.ApplicationError{
			Message: "dataSet should be provided",
			Classes: []string{
				errorClass,
				errs.InvalidParameter,
				errs.EmptyNotAllowed}}
	}

	ss, ok := p.sets[where]
	if !ok {
		ss = []*DataSet{s}
		p.sets[where] = ss

		return nil
	}

	if ind := index[*DataSet](s, ss); ind == -1 {
		ss = append(ss, s)
	}

	return nil
}

// removeSet removes the DataSet references on the Parameter.
func (p *Parameter) removeSet(s *DataSet, from SetType) error {
	if err := checkSetType(from); err != nil {
		return &errs.ApplicationError{
			Err:     err,
			Message: "Invalid DataSet type",
			Classes: []string{
				errorClass,
				errs.InvalidParameter}}
	}

	if s == nil {
		return &errs.ApplicationError{
			Message: "dataSet should be provided",
			Classes: []string{
				errorClass,
				errs.InvalidParameter,
				errs.EmptyNotAllowed}}
	}

	ss, ok := p.sets[from]
	if !ok {
		return &errs.ApplicationError{
			Message: "parameter does't referenced by dataSet",
			Classes: []string{
				errorClass,
				errs.InvalidParameter},
			Details: map[string]string{
				"parameter_name": p.name,
				"set_type":       from.String(),
				"set_name":       s.name}}
	}

	if ind := index[*DataSet](s, ss); ind != -1 {
		ss = append(ss[:ind], ss[ind+1:]...)
	}

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
			&errs.ApplicationError{
				Message: "name shouldn't be empty",
				Classes: []string{
					errorClass,
					errs.EmptyNotAllowed,
					errs.InvalidParameter}}
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &DataSet{
			BaseElement: *be,
			name:        name,
			values:      map[SetType][]*Parameter{},
			linkedSets:  []*DataSet{}},
		nil
}

// AddParameter add non-empyt parameter into the selected DataSet.
// It checks if there is already parameter with equal name but different
// id.
func (s *DataSet) AddParameter(p *Parameter, where SetType) error {
	if err := checkSetType(where); err != nil {
		return &errs.ApplicationError{
			Message: "Invalid DataSet type",
			Classes: []string{
				errorClass,
				errs.InvalidParameter}}
	}

	if p == nil {
		return &errs.ApplicationError{
			Message: "parameter couldn't be nil",
			Classes: []string{
				errorClass,
				errs.InvalidParameter,
				errs.EmptyNotAllowed}}
	}

	if s.values[where] == nil {
		s.values[where] = []*Parameter{}
	}

	vv, ok := s.values[where]
	if !ok {
		if err := p.addSet(s, where); err != nil {
			return err
		}
		vv = []*Parameter{p}
		s.values[where] = vv

		return nil
	}

	for _, v := range vv {
		if v.name == p.name {
			if v.Id() != p.Id() {
				return &errs.ApplicationError{
					Message: "data set already has parameter with the name",
					Classes: []string{
						errorClass,
						errs.InvalidParameter,
						errs.DuplicateObject},
					Details: map[string]string{
						"name": v.name}}
			}

			return nil
		}
	}

	if err := p.addSet(s, where); err != nil {
		return err
	}
	s.values[where] = append(vv, p)

	return nil
}

// RemoveParameter removes non-empty parameter from the DataSet and
// removes the reference on the DataSet from the Parameter.
func (s *DataSet) RemoveParameter(p *Parameter, from SetType) error {
	if err := checkSetType(from); err != nil {
		return &errs.ApplicationError{
			Message: "Invalid DataSet type",
			Classes: []string{
				errorClass,
				errs.InvalidParameter}}
	}

	if p == nil {
		return &errs.ApplicationError{
			Message: "parameter couldn't be nil",
			Classes: []string{
				errorClass,
				errs.InvalidParameter,
				errs.EmptyNotAllowed}}
	}

	if s.values[from] == nil {
		return errs.New(
			errs.M("data set is empty"),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("data_set_type", from.String()))
	}

	vv, ok := s.values[from]
	if !ok {
		return &errs.ApplicationError{
			Message: "couldn't find selected set type",
			Classes: []string{
				errorClass,
				errs.InvalidParameter},
			Details: map[string]string{
				"set_type": from.String()}}
	}

	index := index(p, vv)
	if index == -1 {
		return errs.New(
			errs.M("parameter isn't exists in selected data set"),
			errs.C(errorClass, errs.InvalidObject),
			errs.D("param_name", p.name),
			errs.D("data_set_type", from.String()))
	}

	if err := p.removeSet(s, from); err != nil {
		return err
	}
	s.values[from] = append(vv[:index], vv[index+1:]...)

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
	executionFinished bool) error {
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

	if s.values[DefaultSet] != nil {
		if err := checkParamsState(
			rs,
			s.values[DefaultSet],
			DefaultSet); err != nil {
			return err
		}
	}

	if executionFinished == true {
		if s.values[WhileExecutionSet] != nil {
			return checkParamsState(
				rs,
				s.values[WhileExecutionSet],
				WhileExecutionSet)
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
