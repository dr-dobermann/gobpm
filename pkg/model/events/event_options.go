package events

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const (
	catchEventConfig eventConfigType = "catchEvent"
	throwEventConfig eventConfigType = "throwEvent"
)

// Common event configuration interface and function.
type (
	eventConfigType string

	// Specialized interface for event configuration
	eventConfig interface {
		options.Configurator

		eventType() eventConfigType
	}

	// option for event configuring.
	eventOption func(cfg eventConfig) error

	// propertyAdder is an configuration interface which
	// adds single property to the event configureation.
	propertyAdder interface {
		eventConfig

		addProperty(prop *data.Property)
	}

	messageAdder interface {
		eventConfig

		setMessage(med *MessageEventDefinition) error
	}

	timerAdder interface {
		eventConfig

		setTimer(ted *TimerEventDefinition) error
	}

	conditionAdder interface {
		eventConfig

		setCondiiton(ced *ConditionalEventDefinition) error
	}

	signalAdder interface {
		eventConfig

		setSignal(sed *SignalEventDefinition) error
	}
)

// Apply implements options.Option interface for the eventOption.
func (eo eventOption) Apply(cfg options.Configurator) error {
	if ec, ok := cfg.(eventConfig); ok {
		return eo(ec)
	}

	return &errs.ApplicationError{
		Message: "cfg doens't implement eventConfig interface",
		Classes: []string{
			errorClass,
			errs.TypeCastingError,
		},
		Details: map[string]string{
			"cfg_type": reflect.TypeOf(cfg).String(),
		},
	}

}

// WithProperty add one property to startConfig.
func WithProperty(prop *data.Property) eventOption {
	f := func(cfg eventConfig) error {
		if pa, ok := cfg.(propertyAdder); ok {
			pa.addProperty(prop)

			return nil
		}

		return &errs.ApplicationError{
			Message: "cfg doens't implement propertyAdder interface",
			Classes: []string{
				errorClass,
				errs.TypeCastingError,
			},
			Details: map[string]string{
				"cfg_type": reflect.TypeOf(cfg).String(),
			},
		}
	}

	return eventOption(f)
}

// WithMessageTrigger adds a MessageEventDefinition into eventConfig.
func WithMessageTrigger(
	med *MessageEventDefinition,
) eventOption {
	f := func(cfg eventConfig) error {
		if med == nil {
			return &errs.ApplicationError{
				Message: "empty message definition isn't allowed",
				Classes: []string{
					errorClass,
					errs.InvalidParameter,
				},
			}
		}

		if ma, ok := cfg.(messageAdder); ok {
			return ma.setMessage(med)
		}

		return &errs.ApplicationError{
			Message: "cfg doens't implement messageAdder interface",
			Classes: []string{
				errorClass,
				errs.TypeCastingError,
			},
			Details: map[string]string{
				"cfg_type": reflect.TypeOf(cfg).String(),
			},
		}
	}

	return eventOption(f)
}

// WithTimerTrigger adds a TimerEventDefinition into eventConfig.
func WithTimerTrigger(ted *TimerEventDefinition) eventOption {
	f := func(cfg eventConfig) error {
		if ted == nil {
			return &errs.ApplicationError{
				Message: "empty timer definition isn't allowed",
				Classes: []string{
					errorClass,
					errs.InvalidParameter,
				},
			}
		}

		if ta, ok := cfg.(timerAdder); ok {
			return ta.setTimer(ted)
		}

		return &errs.ApplicationError{
			Message: "cfg doens't implement timerAdder interface",
			Classes: []string{
				errorClass,
				errs.TypeCastingError,
			},
			Details: map[string]string{
				"cfg_type": reflect.TypeOf(cfg).String(),
			},
		}
	}

	return eventOption(f)
}

// WithConditionalTrigger adds a ConditionalEventDefinition into eventConfig.
func WithConditionalTrigger(ced *ConditionalEventDefinition) eventOption {
	f := func(cfg eventConfig) error {
		if ced == nil {
			return &errs.ApplicationError{
				Message: "empty conditional definition isn't allowed",
				Classes: []string{
					errorClass,
					errs.InvalidParameter,
				},
			}
		}

		if ca, ok := cfg.(conditionAdder); ok {
			return ca.setCondiiton(ced)
		}

		return &errs.ApplicationError{
			Message: "cfg doens't implement conditionAdder interface",
			Classes: []string{
				errorClass,
				errs.TypeCastingError,
			},
			Details: map[string]string{
				"cfg_type": reflect.TypeOf(cfg).String(),
			},
		}
	}

	return eventOption(f)
}

// WithSignalTrigger adds a SignalEventDefinition into eventConfig.
func WithSignalTrigger(sed *SignalEventDefinition) eventOption {
	f := func(cfg eventConfig) error {
		if sed == nil {
			return &errs.ApplicationError{
				Message: "empty signal definition isn't allowed",
				Classes: []string{
					errorClass,
					errs.InvalidParameter,
				},
			}
		}

		if sa, ok := cfg.(signalAdder); ok {
			return sa.setSignal(sed)
		}

		return &errs.ApplicationError{
			Message: "cfg doens't implement signalAdder interface",
			Classes: []string{
				errorClass,
				errs.TypeCastingError,
			},
			Details: map[string]string{
				"cfg_type": reflect.TypeOf(cfg).String(),
			},
		}
	}

	return eventOption(f)
}
