package events

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	startOption func(*startConfig) error

	startConfig struct {
		name          string
		props         []data.Property
		parallel      bool
		interrurpting bool
		baseOpts      []options.Option
		defs          []Definition
		dataOutputs   map[string]*data.Output
		outputSet     *data.OutputSet
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
			errorClass,
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
	for _, d := range append(sc.defs, sc.defs...) {
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
func WithParallel() options.Option {
	f := func(cfg *startConfig) error {
		cfg.parallel = true

		return nil
	}

	return startOption(f)
}

// WithInterrupting sets interrurpting flag in startConfig.
func WithInterrupting() options.Option {
	f := func(cfg *startConfig) error {
		cfg.interrurpting = true

		return nil
	}

	return startOption(f)
}

// WithMessageTrigger adds a MessageEventDefinition into startConfig.
// If reference is true, then Definition will be added to defintionRef list or
// to definition otherwise.
func WithMessageTrigger(
	med *MessageEventDefinition,
) options.Option {
	f := func(cfg *startConfig) error {
		cfg.defs = append(cfg.defs, med)

		if id := med.Message().Item(); id != nil {
			ds := data.ReadyDataState
			if id.Structure() == nil {
				ds = data.UndefinedDataState
			}

			do, err := data.NewOutput(
				data.NewItemAwareElement(id, &ds),
				fmt.Sprintf("message %q(%s) output",
					med.Message().Name(),
					med.Message().Id()))
			if err != nil {
				return &errs.ApplicationError{
					Err:     err,
					Message: "couldn't create DataOutput",
					Classes: []string{
						errorClass,
						errs.BulidingFailed,
					},
				}
			}

			cfg.dataOutputs[id.Id()] = do
		}

		return nil
	}

	return startOption(f)
}

// WithTimerTrigger adds a TimerEventDefinition into startConfig.
// If reference is true, then Definition will be added to defintionRef list or
// to definition otherwise.
func WithTimerTrigger(ted TimerEventDefinition) options.Option {
	f := func(cfg *startConfig) error {
		cfg.defs = append(cfg.defs, &ted)

		return nil
	}

	return startOption(f)
}

// WithConditionalTrigger adds a ConditionalEventDefinition into startConfig.
// If reference is true, then Definition will be added to defintionRef list or
// to definition otherwise.
func WithConditionalTrigger(ced ConditionalEventDefinition) options.Option {
	f := func(cfg *startConfig) error {
		cfg.defs = append(cfg.defs, &ced)

		return nil
	}

	return startOption(f)
}

// WithSignalTrigger adds a SignalEventDefinition into startConfig.
// If reference is true, then Definition will be added to defintionRef list or
// to definition otherwise.
func WithSignalTrigger(sed SignalEventDefinition) options.Option {
	f := func(cfg *startConfig) error {
		cfg.defs = append(cfg.defs, &sed)

		return nil
	}

	return startOption(f)
}
