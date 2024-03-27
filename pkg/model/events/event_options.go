package events

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type eventConfigType string

const (
	catchEventConfig eventConfigType = "catchEvent"
	throwEventConfig eventConfigType = "throwEvent"
)

// Common event configuration interface and function.
type (
	// Specialized interface for event configuration
	eventConfig interface {
		options.Configurator

		eventType() eventConfigType
	}

	// option for event configuring.
	eventOption func(cfg eventConfig) error

	// condigionAdder adds ConditionalEventDefinition into the event
	// configuration.
	// Used by Start and Intermediate Events.
	conditionAdder interface {
		eventConfig

		setCondiiton(ced *ConditionalEventDefinition) error
	}

	// cancelAdder adds CancelEventDefinition into the event
	// configuration.
	// Used by Intermediate(boundary only) and End Events.
	cancelAdder interface {
		eventConfig

		setCancel(ced *CancelEventDefinition) error
	}

	// compensationAdder adds CompensationEventDefinition into the event
	// configuration.
	// Used by Intermediate, Start(in-line Sub-Process only) and End Events.
	compensationAdder interface {
		eventConfig

		setCompensation(sed *CompensationEventDefinition) error
	}

	// escalationAdder adds EscalationEventDefinition into the event
	// configuration.
	// Used by Intermediate, Start(in-line Sub-Process only) and End Events.
	escalationAdder interface {
		eventConfig

		setEscalation(sed *EscalationEventDefinition) error
	}

	// errorAdder adds ErrorEventDefinition into the event configuration.
	// Used by End, Starti(only in in-line Sub-Process) and
	// Intermediate(boundary only) Events.
	errorAdder interface {
		eventConfig

		setError(eed *ErrorEventDefinition) error
	}

	// messageAdder adds MessageEventDefinition into the
	// event configureation and sets dataInput or dataOutput depending from
	// event type.
	// Used by all Events.
	messageAdder interface {
		eventConfig

		setMessage(med *MessageEventDefinition) error
	}

	// signalAdder adds SignalEventDefinition into the event configuration.
	// Used by All Events.
	signalAdder interface {
		eventConfig

		setSignal(sed *SignalEventDefinition) error
	}

	// timerAdder adds TimerEventDefinition into the event configuration.
	// Used by Start and Intermediate Events.
	timerAdder interface {
		eventConfig

		setTimer(ted *TimerEventDefinition) error
	}
)

// Apply implements options.Option interface for the eventOption.
func (eo eventOption) Apply(cfg options.Configurator) error {
	if ec, ok := cfg.(eventConfig); ok {
		return eo(ec)
	}

	return errs.New(
		errs.M("cfg isn't eventConfig: %s", reflect.TypeOf(cfg).String()),
		errs.C(errorClass, errs.TypeCastingError))
}

// WithCancelTrigger adds a CancelEventDefinition into eventConfig.
func WithCancelTrigger(ced *CancelEventDefinition) eventOption {
	f := func(cfg eventConfig) error {
		if err := checkNil(ced,
			"empty cancel event definition isn't allowed"); err != nil {
			return err
		}

		if ca, ok := cfg.(cancelAdder); ok {
			return ca.setCancel(ced)
		}

		return errs.New(
			errs.M("cfg doens't implement cancelAdder interface: %s",
				reflect.TypeOf(cfg).String()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return eventOption(f)
}

// WithCompensationTrigger adds a CompensationEventDefinition into eventConfig.
func WithCompensationTrigger(ced *CompensationEventDefinition) eventOption {
	f := func(cfg eventConfig) error {
		if err := checkNil(ced,
			"empty compensation event definition isn't allowed"); err != nil {
			return err
		}

		if ca, ok := cfg.(compensationAdder); ok {
			return ca.setCompensation(ced)
		}

		return errs.New(
			errs.M("cfg doens't implement compensationAdder interface: %s",
				reflect.TypeOf(cfg).String()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return eventOption(f)
}

// WithConditionalTrigger adds a ConditionalEventDefinition into eventConfig.
func WithConditionalTrigger(ced *ConditionalEventDefinition) eventOption {
	f := func(cfg eventConfig) error {
		if err := checkNil(ced,
			"empty conditional event definition isn't allowed"); err != nil {
			return err
		}

		if ca, ok := cfg.(conditionAdder); ok {
			return ca.setCondiiton(ced)
		}

		return errs.New(
			errs.M("cfg doens't implement conditionAdder interface: %s",
				reflect.TypeOf(cfg).String()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return eventOption(f)
}

// WithErrorTrigger adds ErrorEventDefinition into the eventConfig.
func WithErrorTrigger(eed *ErrorEventDefinition) eventOption {
	f := func(cfg eventConfig) error {
		if err := checkNil(eed,
			"empty error definition isn't allowed"); err != nil {
			return err
		}

		if ea, ok := cfg.(errorAdder); ok {
			return ea.setError(eed)
		}

		return errs.New(
			errs.M("cfg doens't implement errorAdder interface: %s",
				reflect.TypeOf(cfg).String()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return eventOption(f)
}

// WithEscalationTrigger adds EscalationEventDefinition into the eventConfig.
func WithEscalationTrigger(eed *EscalationEventDefinition) eventOption {
	f := func(cfg eventConfig) error {
		if err := checkNil(eed,
			"empty escalation definition isn't allowed"); err != nil {
			return err
		}

		if ea, ok := cfg.(escalationAdder); ok {
			return ea.setEscalation(eed)
		}

		return errs.New(
			errs.M("cfg doens't implement escalationAdder interface: %s",
				reflect.TypeOf(cfg).String()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return eventOption(f)
}

// WithMessageTrigger adds a MessageEventDefinition into eventConfig.
func WithMessageTrigger(
	med *MessageEventDefinition,
) eventOption {
	f := func(cfg eventConfig) error {
		if err := checkNil(med,
			"empty message definition isn't allowed"); err != nil {
			return err
		}

		if ma, ok := cfg.(messageAdder); ok {
			return ma.setMessage(med)
		}

		return errs.New(
			errs.M("cfg doens't implement messageAdder interface: %s",
				reflect.TypeOf(cfg).String()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return eventOption(f)
}

// WithSignalTrigger adds a SignalEventDefinition into eventConfig.
func WithSignalTrigger(sed *SignalEventDefinition) eventOption {
	f := func(cfg eventConfig) error {
		if err := checkNil(sed,
			"empty signal event definition isn't allowed"); err != nil {
			return err
		}

		if sa, ok := cfg.(signalAdder); ok {
			return sa.setSignal(sed)
		}

		return errs.New(
			errs.M("cfg doens't implement signalAdder interface: %s",
				reflect.TypeOf(cfg).String()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return eventOption(f)
}

// WithTimerTrigger adds a TimerEventDefinition into eventConfig.
func WithTimerTrigger(ted *TimerEventDefinition) eventOption {
	f := func(cfg eventConfig) error {
		if err := checkNil(ted,
			"empty timer event definition isn't allowed"); err != nil {
			return err
		}

		if ta, ok := cfg.(timerAdder); ok {
			return ta.setTimer(ted)
		}

		return errs.New(
			errs.M("cfg doens't implement timerAdder interface: %s",
				reflect.TypeOf(cfg).String()),
			errs.C(errorClass, errs.TypeCastingError))
	}

	return eventOption(f)
}

// checkNil checks if v is a nil, and if so, returns error with errMsg.
func checkNil[T any](v *T, errMsg string) error {
	if v == nil {
		return errs.New(
			errs.M(errMsg),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return nil
}
