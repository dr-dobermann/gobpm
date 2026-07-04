package events

import (
	"errors"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	startOption func(*startConfig) error

	startConfig struct {
		props          map[string]*data.Property
		dataOutputs    map[string]*data.Parameter
		correlationKey *bpmncommon.CorrelationKey
		name           string
		baseOpts       []options.Option
		defs           []flow.EventDefinition
		parallel       bool
		interrurpting  bool
	}
)

// WithCorrelationKey declares the CorrelationKey an instantiating message start
// event correlates on (ADR-016 v.1 §2.2): the engine derives an incoming
// message's key from it to decide create-or-route-or-join. A nil key is
// rejected.
func WithCorrelationKey(key *bpmncommon.CorrelationKey) options.Option {
	return startOption(func(sc *startConfig) error {
		if key == nil {
			return errs.New(
				errs.M("WithCorrelationKey: a nil CorrelationKey isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		sc.correlationKey = key

		return nil
	})
}

// eventType implements eventConfig interface
func (sc *startConfig) eventType() eventConfigType {
	return catchEventConfig
}

// ------------------- options.Option interface --------------------------------

// Option marks startOption as an options.Option; newStartEvent applies it by
// calling the func directly after its type-switch matches.
func (startOption) Option() {}

// ------------------ data.PropertyConfigurator interface ----------------------

// AddProperty adds non-empty property in startConfig.
// if there aleready exists the property with same id it will be overwritten.
func (sc *startConfig) AddProperty(prop *data.Property) error {
	if prop == nil {
		return errs.New(
			errs.M("property couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	sc.props[prop.ID()] = prop

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
	ce, err := newCatchEvent(
		sc.name,
		map2slice(sc.props),
		sc.defs,
		sc.parallel,
		sc.baseOpts...)
	if err != nil {
		return nil, err
	}

	if len(sc.dataOutputs) > 0 {
		ce.dataOutputs = sc.dataOutputs
	}

	return &StartEvent{
		catchEvent:     *ce,
		correlationKey: sc.correlationKey,
		interrupting:   sc.interrurpting,
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
func (sc *startConfig) setCompensation(
	ced *CompensationEventDefinition,
) error {
	sc.defs = append(sc.defs, ced)

	return nil
}

// setCondition implements conditionAdder interface.
func (sc *startConfig) setCondition(ced *ConditionalEventDefinition) error {
	sc.defs = append(sc.defs, ced)

	return nil
}

// setError implements errorAdder interface.
func (sc *startConfig) setError(
	eed *ErrorEventDefinition,
) error {
	sc.defs = append(sc.defs, eed)

	return nil
}

// setEscalation implements escalationAdder interface.
func (sc *startConfig) setEscalation(
	eed *EscalationEventDefinition,
) error {
	sc.defs = append(sc.defs, eed)

	return nil
}

// setMessage implements messageAdder interface.
func (sc *startConfig) setMessage(med *MessageEventDefinition) error {
	sc.defs = append(sc.defs, med)

	if id := med.Message().Item(); id != nil {
		ds := data.ReadyDataState
		if id.Structure() == nil {
			ds = data.UndefinedSrcState
		}

		iae, err := data.NewItemAwareElement(id, ds)
		if err != nil {
			return err
		}

		do, err := data.NewParameter(
			fmt.Sprintf("message %q(%s) output",
				med.Message().Name(),
				med.Message().ID()),
			iae)
		if err != nil {
			return err
		}

		sc.dataOutputs[id.ID()] = do
	}

	return nil
}

// setSignal implements signalAdder interface.
func (sc *startConfig) setSignal(sed *SignalEventDefinition) error {
	sc.defs = append(sc.defs, sed)

	return nil
}

// setTimer implements timerAdder interface.
func (sc *startConfig) setTimer(ted *TimerEventDefinition) error {
	sc.defs = append(sc.defs, ted)

	return nil
}

// setCancel implements cancelAdder solely to REJECT a Cancel trigger: Cancel is
// an End Event trigger (Transaction abort), never a Start Event trigger.
// Implementing the interface turns the attempt into a clear INVALID_PARAMETER
// error at configuration building instead of the leaky "cfg doesn't implement
// cancelAdder" type-cast error (mirrors endConfig's catch-trigger rejections).
func (sc *startConfig) setCancel(_ *CancelEventDefinition) error {
	return errs.New(
		errs.M("a Start Event cannot carry a Cancel trigger; "+
			"Cancel is an End Event trigger (Transaction abort)"),
		errs.C(errorClass, errs.InvalidParameter))
}
