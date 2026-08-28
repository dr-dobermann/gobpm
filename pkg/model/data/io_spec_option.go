package data

import (
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// IOSpecAdder is what a callable's config implements to take its declared
// I/O parameters — the PropertyAdder pattern one type over (SRD-093 §4.1).
//
// It deliberately does NOT embed options.Configurator: nothing here calls
// Validate, so requiring it would force every container config to carry a
// stub.
type IOSpecAdder interface {
	AddIOParameters(dir Direction, params ...*Parameter) error
}

// IOSpecOption declares a callable's I/O contract (ADR-040 §2.1). The
// dispatching constructor applies it by calling the func with a config
// that implements IOSpecAdder.
type IOSpecOption func(cfg IOSpecAdder) error

// Option marks IOSpecOption as an options.Option.
func (IOSpecOption) Option() {}

// WithInputs declares a callable's input parameters — its single input set
// (ADR-040 §2.1). Accumulates across calls; a nil parameter is refused.
func WithInputs(params ...*Parameter) IOSpecOption {
	return withIOParams(Input, params)
}

// WithOutputs declares a callable's output parameters — its single output
// set (ADR-040 §2.1). Accumulates across calls; a nil parameter is refused.
func WithOutputs(params ...*Parameter) IOSpecOption {
	return withIOParams(Output, params)
}

// withIOParams is the one implementation behind the direction-named pair.
func withIOParams(dir Direction, params []*Parameter) IOSpecOption {
	return func(cfg IOSpecAdder) error {
		for i, p := range params {
			if p == nil {
				return errs.New(
					errs.M("%s: a nil parameter isn't allowed", optionName(dir)),
					errs.C(errorClass, errs.EmptyNotAllowed),
					errs.D("index", strconv.Itoa(i)))
			}
		}

		return cfg.AddIOParameters(dir, params...)
	}
}

// optionNames is the self-identifying prefix each direction's option
// reports (the data-over-code rule for a fixed mapping).
var optionNames = map[Direction]string{
	Input:  "WithInputs",
	Output: "WithOutputs",
}

// optionName names the option a direction belongs to.
func optionName(dir Direction) string {
	return optionNames[dir]
}
