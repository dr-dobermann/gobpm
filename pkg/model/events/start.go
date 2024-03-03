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

var startTriggers = set.New[Trigger](
	TriggerMessage,
	TriggerTimer,
	TriggerConditional,
	TriggerSignal,
)

type StartEvent struct {
	catchEvent

	// This attribute only applies to Start Events of Event Sub-Processes; it is
	// ignored for other Start Events. This attribute denotes whether the
	// Sub-Process encompassing the Event Sub-Process should be canceled or not,
	// If the encompassing Sub-Process is not canceled, multiple instances of
	// the Event Sub-Process can run concurrently. This attribute cannot be
	// applied to Error Events (where it’s always true), or Compensation Events
	// (where it doesn’t apply).
	interrrupting bool
}

// NewStartEvent creates a new StartEvent and returns its pointer on success
// or error if any.
func NewStartEvent(
	name string,
	startEventOptions ...options.Option,
) (*StartEvent, error) {
	sc := startConfig{
		name:          name,
		props:         []data.Property{},
		parallel:      false,
		interrurpting: false,
		baseOpts:      []options.Option{},
		defs:          []Definition{},
		dataOutputs:   make(map[string]*data.Output),
	}

	ee := []error{}

	for _, opt := range startEventOptions {
		switch so := opt.(type) {
		case foundation.BaseOption:
			sc.baseOpts = append(sc.baseOpts, opt)

		case startOption:
			if err := so.Apply(&sc); err != nil {
				ee = append(ee, err)
			}

		default:
			ee = append(ee, fmt.Errorf("innapropriate option type: %s",
				reflect.TypeOf(so).Name()))
		}

	}

	if err := sc.validate(); err != nil {
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

	return sc.startEvent()
}

// IsInterrupting returns interrupting setting of the StartEvent.
func (se *StartEvent) IsInterrupting() bool {
	return se.interrrupting
}
