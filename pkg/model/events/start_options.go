package events

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	startOption func(*startConfig) error

	startConfig struct {
		name          string
		props         []data.Property
		parallel      bool
		interrurpting bool
		baseOpts      []foundation.BaseOption
		defs          []Definition
		defRefs       []Definition
	}
)

// Apply implements options.Option interface for startOption.
func (so startOption) Apply(cfg any) error {
	if sc, ok := cfg.(*startConfig); ok {
		return so(sc)
	}

	return &errs.ApplicationError{
		Message: "not an startConfig",
		Classes: []string{
			eventErrorClass,
			errs.TypeCastingError,
		},
		Details: map[string]string{
			"cast_from": reflect.TypeOf(cfg).String(),
		},
	}
}

// startEvent creates a new StartEvent from startConfig.
func (sc *startConfig) startEvent() (*StartEvent, error) {
	ce, err := newCatchEvent(
		sc.name,
		sc.props,
		sc.defRefs,
		sc.defs,
		sc.parallel,
		sc.baseOpts...)
	if err != nil {
		return nil, err
	}

	return &StartEvent{
		catchEvent:    *ce,
		interrrupting: sc.interrurpting,
	}, nil
}

// validate checks if startConfig is consistent.
func (sc *startConfig) validate() error {
	ers := []error{}

	// check event definitions to comply with StartEvent triggers.
	for _, d := range append(sc.defRefs, sc.defs...) {
		if !startTriggers.Has(d.Type()) {
			ers = append(ers, fmt.Errorf("%q trigger isn't allowed for StartEvent", d.Type()))
		}
	}

	if len(ers) > 0 {
		return errors.Join(ers...)
	}

	return nil
}

// WithProperty add one property to startConfig.
func WithProperty(prop data.Property) options.Option {
	f := func(cfg *startConfig) error {
		cfg.props = append(cfg.props, prop)

		return nil
	}

	return startOption(f)
}

// WithParallel sets parallel flag in startConfig.
func WithParallel(para bool) options.Option {
	f := func(cfg *startConfig) error {
		cfg.parallel = para

		return nil
	}

	return startOption(f)
}

// WithInterrupting sets interrurpting flag in startConfig.
func WithInterrupting(inter bool) options.Option {
	f := func(cfg *startConfig) error {
		cfg.interrurpting = inter

		return nil
	}

	return startOption(f)
}

// WithEventDefinition adds a Definition into startConfig.
func WithEventDefinition(d Definition) options.Option {
	f := func(cfg *startConfig) error {
		if d == nil {
			return &errs.ApplicationError{
				Message: "empty definition isn't allowed",
				Classes: []string{
					eventErrorClass,
					errs.InvalidParameter,
				},
			}
		}

		cfg.defs = append(cfg.defs, d)

		return nil
	}

	return startOption(f)
}

// WithEventDefinitionRef adds a Defintion referenct to startConfig.
func WithEventDefinitionRef(d Definition) options.Option {
	f := func(cfg *startConfig) error {
		if d == nil {
			return &errs.ApplicationError{
				Message: "empty definition reference isn't allowed",
				Classes: []string{
					eventErrorClass,
					errs.InvalidParameter,
				},
			}
		}

		cfg.defRefs = append(cfg.defRefs, d)

		return nil
	}

	return startOption(f)
}
