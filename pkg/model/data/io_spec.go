package data

import (
	"errors"
	"fmt"
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// InputOutputSpecification represents BPMN input/output specification for activities and processes.
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
	params map[Direction][]*Parameter
	foundation.BaseElement
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

// Validate performs a structural check of the InputOutputSpecification: within
// each direction no two parameters share a name. Parameters are non-nil by
// construction (AddParameter rejects nil).
//
// With the single-set model (ADR-011 v.2) the input set is the input parameter
// list and the output set is the output parameter list — there are no separate
// sets to cross-check. Input availability and required-output production are
// runtime concerns, gated when the activity starts and completes, not here.
func (ios *InputOutputSpecification) Validate() error {
	ee := []error{}

	for _, dir := range []Direction{Input, Output} {
		seen := map[string]bool{}

		for _, p := range ios.params[dir] {
			if seen[p.name] {
				ee = append(ee,
					fmt.Errorf("duplicate %s parameter name %q", dir, p.name))

				continue
			}

			seen[p.name] = true
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

	if idx := slices.Index(pp, p); idx == -1 {
		ios.params[dir] = append(pp, p)
	}

	return nil
}

// RemoveParameter removes Parameter p of dir type from every set of that
// direction that references it and from the IOSpec itself.
//
// The receiver is a pointer: the method mutates ios.params, so a value
// receiver would drop the removal on a copy and silently keep the parameter.
func (ios *InputOutputSpecification) RemoveParameter(
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

	idx := slices.Index(pp, p)
	if idx == -1 {
		return errs.New(
			errs.M("no parameter %q in data set %q", p.name, dir),
			errs.C(errorClass, errs.InvalidParameter))
	}

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
		if iosP.ID() == p.ID() {
			return true
		}
	}

	return false
}

// InputSet returns the activity's input set — its list of input parameters
// (ADR-011 v.2: the single InputSet is the input parameter list, no Set type).
// The returned slice is a copy.
func (ios *InputOutputSpecification) InputSet() []*Parameter {
	return append([]*Parameter{}, ios.params[Input]...)
}

// OutputSet returns the activity's output set — its list of output parameters
// (ADR-011 v.2: the single OutputSet is the output parameter list, no Set type).
// The returned slice is a copy.
func (ios *InputOutputSpecification) OutputSet() []*Parameter {
	return append([]*Parameter{}, ios.params[Output]...)
}
