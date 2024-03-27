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
	endOption func(*endConfig) error

	endConfig struct {
		name       string
		props      map[string]*data.Property
		baseOpts   []options.Option
		defs       []Definition
		dataInputs map[string]*data.Parameter
		inputSet   *data.Set
	}
)

// -------------- eventConfig interface ----------------------------------------

// eventType implements eventConfig interface.
func (ec *endConfig) eventType() eventConfigType {
	return throwEventConfig
}

// -------------------- options.Option interface -------------------------------

// Apply implements options.Option interface for endOption.
func (eo endOption) Apply(cfg options.Configurator) error {
	if ec, ok := cfg.(*endConfig); ok {
		return eo(ec)
	}

	return errs.New(
		errs.M("not an endConfig: %s", reflect.TypeOf(cfg).String()),
		errs.C(errorClass, errs.TypeCastingError))
}

// ------------------------ options.Configurator interface ---------------------

// Validate checks if endConfig is consistent and implements
// options.Confugurator interface.
func (ec *endConfig) Validate() error {
	ers := []error{}

	// check event definitions to comply with StartEvent triggers.
	for _, d := range ec.defs {
		if !endTriggers.Has(d.Type()) {
			ers = append(ers,
				fmt.Errorf("%q trigger isn't allowed for EndEvent",
					d.Type()))
		}
	}

	if len(ers) > 0 {
		return errors.Join(ers...)
	}

	return nil
}

// ---------------- data.PropertyConfigurator interface ------------------------

// AddProperty adds non-empty property in startConfig.
// if there aleready exists the property with same id it will be overwritten.
func (ec *endConfig) AddProperty(prop *data.Property) error {
	if prop == nil {
		return errs.New(
			errs.M("property couldn't be empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	ec.props[prop.Id()] = prop

	return nil
}

// -----------------------------------------------------------------------------

// endEvent builds an EndEvent from the endConfig.
func (ec *endConfig) endEvent() (*EndEvent, error) {
	const inputSetName = "endEventInput"

	te, err := newThrowEvent(
		ec.name,
		map2slice(ec.props),
		ec.defs,
		ec.baseOpts...)
	if err != nil {
		return nil, err
	}

	// create and fill input set
	if len(ec.dataInputs) > 0 {
		te.inputSet, err = data.NewSet(inputSetName)
		if err != nil {
			return nil, err
		}

		te.dataInputs = ec.dataInputs

		for _, di := range te.dataInputs {
			if err := te.inputSet.AddParameter(di, data.DefaultSet); err != nil {
				return nil, err
			}
		}
	}

	return &EndEvent{
		throwEvent: *te,
	}, nil
}

// WithTerminateTrigger adds TerminateEventDefinition into the endConfig.
func WithTerminateTrigger(
	ted *TerminateEventDefinition,
) options.Option {
	f := func(cfg *endConfig) error {
		if ted == nil {
			return errs.New(
				errs.M("empty terminate definition isn't allowed"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		cfg.defs = append(cfg.defs, ted)

		return nil
	}

	return endOption(f)
}

// --------------------- eventOptions ------------------------------------------

// setCancel implements cancelAdder interface.
func (ec *endConfig) setCancel(ced *CancelEventDefinition) error {
	ec.defs = append(ec.defs, ced)

	return nil
}

// setCompensation implements compensationAdder interface.
func (ec *endConfig) setCompensation(ced *CompensationEventDefinition) error {
	ec.defs = append(ec.defs, ced)

	return nil
}

// setError implements errorAdder interface.
func (ec *endConfig) setError(eed *ErrorEventDefinition) error {
	ec.defs = append(ec.defs, eed)

	return nil
}

// setEscalation implements escalationAdder interface.
func (ec *endConfig) setEscalation(eed *EscalationEventDefinition) error {
	ec.defs = append(ec.defs, eed)

	return nil
}

// setMessage implements messageAdder for the endConfig.
func (ec *endConfig) setMessage(med *MessageEventDefinition) error {
	ec.defs = append(ec.defs, med)

	if id := med.Message().Item(); id != nil {
		ds := data.ReadyDataState
		if id.Structure() == nil {
			ds = data.UndefinedDataState
		}

		iae, err := data.NewItemAwareElement(id, ds)
		if err != nil {
			return err
		}

		di, err := data.NewParameter(
			fmt.Sprintf("message %q(%s) input",
				med.Message().Name(),
				med.Message().Id()),
			iae)
		if err != nil {
			return err
		}

		ec.dataInputs[id.Id()] = di
	}

	return nil
}

// setSignal implements signalAdder interface.
func (ec *endConfig) setSignal(sed *SignalEventDefinition) error {
	ec.defs = append(ec.defs, sed)

	return nil
}
