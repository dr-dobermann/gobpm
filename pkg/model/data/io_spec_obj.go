package data

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// Direction represents input/output direction for BPMN parameters.
type Direction string

const (
	// Input represents input direction for parameters.
	Input Direction = "INPUT"
	// Output represents output direction for parameters.
	Output Direction = "OUTPUT"
)

// Validate test pt on validity and return error on failure.
func (dir Direction) Validate() error {
	if dir != Input && dir != Output {
		return errs.New(
			errs.M("invalid direction: %q", dir),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
}

// Opposite reverses Direction.
func Opposite(dir Direction) Direction {
	if dir == Input {
		return Output
	}

	return Input
}

// Parameter is type which is used as substitute for DataInput and DataOutput
// BPMN types.
//
// A Parameter carries its own role within the activity's single input/output
// set (ADR-011 v.2): it is required unless flagged optional, and whileExecuting
// marks the niche parameters the standard evaluates during execution rather than
// at start/completion. The InputOutputSpecification owns its parameters directly;
// there is no separate Set type.
type Parameter struct {
	name string
	ItemAwareElement
	optional       bool
	whileExecuting bool
}

// ParameterOption configures a Parameter at construction. Options are
// self-naming closures (Optional, WhileExecuting); applied in order. They set
// flags and cannot fail, so they carry no error.
type ParameterOption func(p *Parameter)

// Optional marks the Parameter as optional: a required input must be available
// when the activity starts, an optional one may be absent (ADR-011 v.2 §2.2).
func Optional() ParameterOption {
	return func(p *Parameter) {
		p.optional = true
	}
}

// WhileExecuting marks the Parameter as evaluated while the activity executes
// rather than gating its start/completion (the standard's
// whileExecutingInputRefs / whileExecutingOutputRefs). Its runtime evaluation is
// not yet implemented; the flag only records the intent.
func WhileExecuting() ParameterOption {
	return func(p *Parameter) {
		p.whileExecuting = true
	}
}

// NewParameter creates a new Parameter and returns its pointer on success or
// error on failure. By default a Parameter is required and not while-executing;
// pass Optional() / WhileExecuting() to change that.
func NewParameter(
	name string,
	iae *ItemAwareElement,
	opts ...ParameterOption,
) (*Parameter, error) {
	name = strings.TrimSpace(name)

	if err := errs.CheckStr(
		name,
		"name shouldn't be empty",
		errorClass,
	); err != nil {
		return nil, err
	}

	if err := CheckName(name, errorClass); err != nil {
		return nil, err
	}

	if iae == nil {
		return nil,
			errs.New(
				errs.M("ItemAwareElement should be provided"),
				errs.C(errorClass, errs.EmptyNotAllowed, errs.InvalidParameter))
	}

	p := &Parameter{
		ItemAwareElement: *iae,
		name:             name,
	}

	for _, opt := range opts {
		opt(p)
	}

	return p, nil
}

// MustParameter creates a new Parameter and returns its pointer or panics on
// failure.
func MustParameter(
	name string,
	iae *ItemAwareElement,
	opts ...ParameterOption,
) *Parameter {
	p, err := NewParameter(name, iae, opts...)
	if err != nil {
		errs.Panic(err.Error())
	}

	return p
}

// Name returns the Parameter's name.
func (p *Parameter) Name() string {
	return p.name
}

// IsOptional reports whether the Parameter is optional (may be absent when the
// activity starts/completes). The default is required.
func (p *Parameter) IsOptional() bool {
	return p.optional
}

// IsWhileExecuting reports whether the Parameter is evaluated while the activity
// executes rather than gating its start/completion.
func (p *Parameter) IsWhileExecuting() bool {
	return p.whileExecuting
}

// RequiredItemIDs returns the set of ItemDefinition ids of the parameters that
// gate an activity's start or completion — those that are neither optional nor
// while-executing (ADR-011 v.2 §2.2). It is keyed by ItemDefinition id, the form
// the execution frame instances and data associations match on, so a runtime
// availability gate can look a parameter up by the id it already has in hand.
func RequiredItemIDs(defs []*Parameter) map[string]bool {
	req := map[string]bool{}

	for _, d := range defs {
		if !d.IsOptional() && !d.IsWhileExecuting() {
			req[d.ItemDefinition().ID()] = true
		}
	}

	return req
}

// Interfaces check for Parameter
var _ Data = (*Parameter)(nil)
