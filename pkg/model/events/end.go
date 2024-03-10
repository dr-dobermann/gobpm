package events

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

var endTriggers = set.New[Trigger](
	TriggerCancel,
	TriggerCompensation,
	TriggerError,
	TriggerEscalation,
	TriggerMessage,
	TriggerSignal,
	TriggerTerminate,
)

type EndEvent struct {
	throwEvent
}

// NewEndEvent creates a new EndEvent and returns its pointer on success
// or error on failure.
func NewEndEvent(
	name string,
	endEventOptions ...options.Option,
) (*EndEvent, error) {
	ec := endConfig{
		name:       name,
		props:      []data.Property{},
		baseOpts:   []options.Option{},
		defs:       []Definition{},
		dataInputs: map[string]*data.Input{},
		inputSet:   &data.InputSet{},
	}

	ee := []error{}

	for _, opt := range endEventOptions {
		switch so := opt.(type) {
		case foundation.BaseOption:
			ec.baseOpts = append(ec.baseOpts, opt)

		case endOption:
			if err := so.Apply(&ec); err != nil {
				ee = append(ee, err)
			}

		case eventOption:
			if err := so.Apply(&ec); err != nil {
				ee = append(ee, err)
			}

		default:
			ee = append(ee, fmt.Errorf("innapropriate option type: %s",
				reflect.TypeOf(so).Name()))
		}

	}

	if err := ec.Validate(); err != nil {
		ee = append(ee, err)
	}

	if len(ee) > 0 {
		return nil,
			&errs.ApplicationError{
				Err:     errors.Join(ee...),
				Message: "start event configuration errors",
				Classes: []string{
					errorClass,
				},
			}
	}

	return ec.endEvent()
}

// EventType impments flow.Event interface for the EndEvent.
func (ee EndEvent) EventType() string {
	return "EndEvent"
}
