package events

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

var startTriggers = set.New[Trigger](
	TriggerCompensation, // only for in-line Sub-Processes
	TriggerConditional,
	TriggerError,      // only for in-line Sub-Processes
	TriggerEscalation, // only for in-line Sub-Processes
	TriggerMessage,
	TriggerSignal,
	TriggerTimer,
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
// or error on failure.
func NewStartEvent(
	name string,
	startEventOptions ...options.Option,
) (*StartEvent, error) {
	sc := startConfig{
		name:          name,
		props:         map[string]*data.Property{},
		parallel:      false,
		interrurpting: false,
		baseOpts:      []options.Option{},
		defs:          []Definition{},
		dataOutputs:   make(map[string]*data.Parameter),
	}

	ee := []error{}

	for _, opt := range startEventOptions {
		switch so := opt.(type) {
		case foundation.BaseOption:
			sc.baseOpts = append(sc.baseOpts, opt)

		case startOption, eventOption, data.PropertyOption:
			if err := so.Apply(&sc); err != nil {
				ee = append(ee, err)
			}

		default:
			ee = append(ee, fmt.Errorf("innapropriate option type: %s",
				reflect.TypeOf(so).Name()))
		}
	}

	if err := sc.Validate(); err != nil {
		ee = append(ee, err)
	}

	if len(ee) > 0 {
		return nil, errors.Join(ee...)
	}

	return sc.startEvent()
}

// ------------------ flow.Source interface ------------------------------------
//
// SuportOutgoingFlow checks if it allowed to source sf from the StartEvent
func (se *StartEvent) SuportOutgoingFlow(sf *flow.SequenceFlow) error {
	// StartEvent don't restricted any source sequence flow from it
	return nil
}

// Link creates a new SequenceFlow between se and trg.
func (se *StartEvent) Link(
	trg flow.SequenceTarget,
	options ...options.Option,
) (*flow.SequenceFlow, error) {
	return flow.NewSequenceFlow(se, trg, options...)
}

// -----------------------------------------------------------------------------
// IsInterrupting returns interrupting setting of the StartEvent.
func (se StartEvent) IsInterrupting() bool {
	return se.interrrupting
}

// EventType impments flow.Event interface for the StartEvent.
func (se StartEvent) EventType() string {
	return "StartEvent"
}
