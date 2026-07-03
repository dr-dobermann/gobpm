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
	// eventConfig is the configuration an EventOption applies to. It carries a
	// setXxx sink for every trigger definition an option can add. Both
	// startConfig and endConfig implement all of them — each ADDS the triggers
	// valid for its event kind and REJECTS the rest with a clear error (e.g. a
	// Start Event rejects Cancel, an End Event rejects Conditional/Timer) — so a
	// WithXxxTrigger option can call the matching sink directly, without a
	// runtime type assertion or an unreachable "unsupported config" fallback.
	// Any new eventConfig implementation must therefore decide, per trigger,
	// whether to add or reject it — enforced at compile time.
	eventConfig interface {
		options.Configurator

		eventType() eventConfigType

		setCancel(ced *CancelEventDefinition) error
		setCompensation(ced *CompensationEventDefinition) error
		setCondition(ced *ConditionalEventDefinition) error
		setError(eed *ErrorEventDefinition) error
		setEscalation(eed *EscalationEventDefinition) error
		setMessage(med *MessageEventDefinition) error
		setSignal(sed *SignalEventDefinition) error
		setTimer(ted *TimerEventDefinition) error
	}

	// EventOption is a function type for configuring events.
	EventOption func(cfg eventConfig) error
)

// Apply implements options.Option interface for the EventOption.
func (eo EventOption) Apply(cfg options.Configurator) error {
	if ec, ok := cfg.(eventConfig); ok {
		return eo(ec)
	}

	return errs.New(
		errs.M("cfg isn't eventConfig: %s", reflect.TypeOf(cfg).String()),
		errs.C(errorClass, errs.TypeCastingError))
}

// WithCancelTrigger adds a CancelEventDefinition into eventConfig.
func WithCancelTrigger(ced *CancelEventDefinition) EventOption {
	return EventOption(func(cfg eventConfig) error {
		if err := checkNil(ced,
			"empty cancel event definition isn't allowed"); err != nil {
			return err
		}

		return cfg.setCancel(ced)
	})
}

// WithCompensationTrigger adds a CompensationEventDefinition into eventConfig.
func WithCompensationTrigger(ced *CompensationEventDefinition) EventOption {
	return EventOption(func(cfg eventConfig) error {
		if err := checkNil(ced,
			"empty compensation event definition isn't allowed"); err != nil {
			return err
		}

		return cfg.setCompensation(ced)
	})
}

// WithConditionalTrigger adds a ConditionalEventDefinition into eventConfig.
func WithConditionalTrigger(ced *ConditionalEventDefinition) EventOption {
	return EventOption(func(cfg eventConfig) error {
		if err := checkNil(ced,
			"empty conditional event definition isn't allowed"); err != nil {
			return err
		}

		return cfg.setCondition(ced)
	})
}

// WithErrorTrigger adds ErrorEventDefinition into the eventConfig.
func WithErrorTrigger(eed *ErrorEventDefinition) EventOption {
	return EventOption(func(cfg eventConfig) error {
		if err := checkNil(eed,
			"empty error definition isn't allowed"); err != nil {
			return err
		}

		return cfg.setError(eed)
	})
}

// WithEscalationTrigger adds EscalationEventDefinition into the eventConfig.
func WithEscalationTrigger(eed *EscalationEventDefinition) EventOption {
	return EventOption(func(cfg eventConfig) error {
		if err := checkNil(eed,
			"empty escalation definition isn't allowed"); err != nil {
			return err
		}

		return cfg.setEscalation(eed)
	})
}

// WithMessageTrigger adds a MessageEventDefinition into eventConfig.
func WithMessageTrigger(
	med *MessageEventDefinition,
) EventOption {
	return EventOption(func(cfg eventConfig) error {
		if err := checkNil(med,
			"empty message definition isn't allowed"); err != nil {
			return err
		}

		return cfg.setMessage(med)
	})
}

// WithSignalTrigger adds a SignalEventDefinition into eventConfig.
func WithSignalTrigger(sed *SignalEventDefinition) EventOption {
	return EventOption(func(cfg eventConfig) error {
		if err := checkNil(sed,
			"empty signal event definition isn't allowed"); err != nil {
			return err
		}

		return cfg.setSignal(sed)
	})
}

// WithTimerTrigger adds a TimerEventDefinition into eventConfig.
func WithTimerTrigger(ted *TimerEventDefinition) EventOption {
	return EventOption(func(cfg eventConfig) error {
		if err := checkNil(ted,
			"empty timer event definition isn't allowed"); err != nil {
			return err
		}

		return cfg.setTimer(ted)
	})
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
