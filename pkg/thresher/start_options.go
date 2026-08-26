package thresher

import (
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
)

// startConfig collects what a Start* call supplies for the launch.
type startConfig struct {
	rootData []data.Data
}

// StartOption configures one launch of a process instance (SRD-093 FR-6).
type StartOption func(*startConfig) error

// WithStartInputs supplies the launch's input values by name — the host's
// start request (ADR-040 §2.2). The engine binds them through the process's
// declared inputs, refusing the launch when a required input is missing or a
// datum names no declared input; a contract-less process takes them as they
// are. "Start" says which moment: data.WithInputs DECLARES a slot, this
// SUPPLIES its value. A nil datum is refused.
func WithStartInputs(dd ...data.Data) StartOption {
	return func(sc *startConfig) error {
		for i, d := range dd {
			if d == nil {
				return errs.New(
					errs.M("WithStartInputs: a nil datum isn't allowed"),
					errs.C(errorClass, errs.EmptyNotAllowed),
					errs.D("index", strconv.Itoa(i)))
			}
		}

		sc.rootData = append(sc.rootData, dd...)

		return nil
	}
}

// WithStartInput is the one-value convenience of WithStartInputs: a name
// and a Go value, lifted through values.NewVariable and delivered Ready.
func WithStartInput(name string, value any) StartOption {
	return func(sc *startConfig) error {
		p, err := data.ReadyValueParameter(name, values.NewVariable(value))
		if err != nil {
			return errs.New(
				errs.M("WithStartInput: input %q can't be built", name),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
		}

		sc.rootData = append(sc.rootData, p)

		return nil
	}
}

// applyStartOptions folds the options of one Start* call, naming the caller
// in a failure.
func applyStartOptions(fn string, opts []StartOption) ([]data.Data, error) {
	var sc startConfig

	for _, o := range opts {
		if o == nil {
			return nil, errs.New(
				errs.M("%s: a nil StartOption isn't allowed", fn),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		if err := o(&sc); err != nil {
			return nil, err
		}
	}

	return sc.rootData, nil
}
