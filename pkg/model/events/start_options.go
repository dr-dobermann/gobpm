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
		props         map[string]*data.Property
		parallel      bool
		interrurpting bool
		baseOpts      []options.Option
		defs          []Definition
		dataOutputs   map[string]*data.Parameter
		outputSet     *data.Set
	}
)

// eventType implements eventConfig interface
func (sc *startConfig) eventType() eventConfigType {
	return catchEventConfig
}

// ------------------- options.Option interface --------------------------------

// Apply implements options.Option interface for startOption.
func (so startOption) Apply(cfg options.Configurator) error {
	if sc, ok := cfg.(*startConfig); ok {
		return so(sc)
	}

	return errs.New(
		errs.M("not an startConfig: %s", reflect.TypeOf(cfg).String()),
		errs.C(errorClass, errs.TypeCastingError))
}

// ------------------ data.PropertyConfigurator interface ----------------------

// AddProperty adds non-empty property in startConfig.
// if there aleready exists the property with same id it will be overwritten.
func (sc *startConfig) AddProperty(prop *data.Property) error {
	if prop == nil {
		return errs.New(
			errs.M("property couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	sc.props[prop.Id()] = prop

	return nil
}

// ------------------------ options.Configurator interface ---------------------

// validate checks if startConfig is consistent.
func (sc *startConfig) Validate() error {
	ers := []error{}

	// check event definitions to comply with StartEvent triggers.
	for _, d := range sc.defs {
		if !startTriggers.Has(d.Type()) {
			ers = append(ers,
				fmt.Errorf("%q trigger isn't allowed for StartEvent",
					d.Type()))
		}
	}

	if len(ers) > 0 {
		return errors.Join(ers...)
	}

	return nil
}

// -----------------------------------------------------------------------------

// startEvent creates a new StartEvent from startConfig.
func (sc *startConfig) startEvent() (*StartEvent, error) {
	const outputSetName = "startEventOutput"

	ce, err := newCatchEvent(
		sc.name,
		map2slice(sc.props),
		sc.defs,
		sc.parallel,
		sc.baseOpts...)
	if err != nil {
		return nil, err
	}

	// create and fill output set
	if len(sc.dataOutputs) > 0 {
		ce.outputSet, err = data.NewSet(outputSetName)
		if err != nil {
			return nil, err
		}

		ce.dataOutputs = sc.dataOutputs

		for _, do := range ce.dataOutputs {
			if err := ce.outputSet.AddParameter(
				do, data.DefaultSet); err != nil {
				return nil, err
			}
		}
	}

	return &StartEvent{
		catchEvent:    *ce,
		interrrupting: sc.interrurpting,
	}, nil
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

// ----------------- eventOptions ----------------------------------------------

// setCompensation implements compensationAdder interface.
func (cfg *startConfig) setCompensation(
	ced *CompensationEventDefinition,
) error {
	cfg.defs = append(cfg.defs, ced)

	return nil
}

// setCondition implements conditionAdder interface.
func (cfg *startConfig) setCondiiton(ced *ConditionalEventDefinition) error {
	cfg.defs = append(cfg.defs, ced)

	return nil
}

// setError implements errorAdder interface.
func (cfg *startConfig) setError(
	eed *ErrorEventDefinition,
) error {
	cfg.defs = append(cfg.defs, eed)

	return nil
}

// setEscalation implements escalationAdder interface.
func (cfg *startConfig) setEscalation(
	eed *EscalationEventDefinition,
) error {
	cfg.defs = append(cfg.defs, eed)

	return nil
}

// setMessage implements messageAdder interface.
func (cfg *startConfig) setMessage(med *MessageEventDefinition) error {
	cfg.defs = append(cfg.defs, med)

	if id := med.Message().Item(); id != nil {
		ds := data.ReadyDataState
		if id.Structure() == nil {
			ds = data.UndefinedDataState
		}

		iae, err := data.NewItemAwareElement(id, ds)
		if err != nil {
			return err
		}

		do, err := data.NewParameter(
			fmt.Sprintf("message %q(%s) output",
				med.Message().Name(),
				med.Message().Id()),
			iae)
		if err != nil {
			return err
		}

		cfg.dataOutputs[id.Id()] = do
	}

	return nil
}

// setSignal implements signalAdder interface.
func (cfg *startConfig) setSignal(sed *SignalEventDefinition) error {
	cfg.defs = append(cfg.defs, sed)

	return nil
}

// setTimer implements timerAdder interface.
func (cfg *startConfig) setTimer(ted *TimerEventDefinition) error {
	cfg.defs = append(cfg.defs, ted)

	return nil
}
