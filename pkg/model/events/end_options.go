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
		props      []data.Property
		baseOpts   []options.Option
		defs       []Definition
		dataInputs map[string]*data.Input
		inputSet   *data.InputSet
	}
)

// Apply implements options.Option interface for endOption.
func (eo endOption) Apply(cfg options.Configurator) error {
	if ec, ok := cfg.(*endConfig); ok {
		return eo(ec)
	}

	return &errs.ApplicationError{
		Message: "not an endConfig",
		Classes: []string{
			errorClass,
			errs.TypeCastingError,
		},
		Details: map[string]string{
			"cast_from": reflect.TypeOf(cfg).Name(),
		},
	}
}

// endEvent builds an EndEvent from the endConfig.
func (ec *endConfig) endEvent() (*EndEvent, error) {
	const inputSetName = "endEventInput"

	te, err := newThrowEvent(
		ec.name,
		ec.props,
		ec.defs,
		ec.baseOpts...)
	if err != nil {
		return nil, err
	}

	// create and fill input set
	if len(ec.dataInputs) > 0 {
		te.inputSet, err = data.NewInputSet(inputSetName)
		if err != nil {
			return nil,
				&errs.ApplicationError{
					Err:     err,
					Message: "input set creation failed for end event",
					Classes: []string{
						errorClass,
						errs.BulidingFailed,
					},
				}
		}

		te.dataInputs = ec.dataInputs

		for _, di := range te.dataInputs {
			te.inputSet.AddInput(di, data.DefaultSet)
		}
	}

	return &EndEvent{
		throwEvent: *te,
	}, nil
}

// eventType implements eventConfig interface.
func (ec *endConfig) eventType() eventConfigType {
	return throwEventConfig
}

// addProperty implements properyAdder for the endConfig.
func (ec *endConfig) addProperty(prop *data.Property) {
	ec.props = append(ec.props, *prop)
}

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

// WithTerminateTrigger adds TerminateEventDefinition into the endConfig.
func WithTerminateTrigger(
	ted *TerminateEventDefinition,
) options.Option {
	f := func(cfg *endConfig) error {
		if ted == nil {
			return &errs.ApplicationError{
				Message: "empty terminate definition isn't allowed",
				Classes: []string{
					errorClass,
					errs.InvalidParameter,
				},
			}
		}

		cfg.defs = append(cfg.defs, ted)

		return nil
	}

	return endOption(f)
}

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

		di, err := data.NewInput(
			data.NewItemAwareElement(id, &ds),
			fmt.Sprintf("message %q(%s) input",
				med.Message().Name(),
				med.Message().Id()))
		if err != nil {
			return &errs.ApplicationError{
				Err:     err,
				Message: "couldn't create DataInput from Message",
				Classes: []string{
					errorClass,
					errs.BulidingFailed},
				Details: map[string]string{
					"msg_name": med.Message().Name()},
			}
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