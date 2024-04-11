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

var endTriggers = set.New[flow.EventTrigger](
	flow.TriggerCancel,
	flow.TriggerCompensation,
	flow.TriggerError,
	flow.TriggerEscalation,
	flow.TriggerMessage,
	flow.TriggerSignal,
	flow.TriggerTerminate,
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
		props:      map[string]*data.Property{},
		baseOpts:   []options.Option{},
		defs:       []flow.EventDefinition{},
		dataInputs: map[string]*data.Parameter{},
		inputSet:   &data.Set{},
	}

	ee := []error{}

	for _, opt := range endEventOptions {
		switch so := opt.(type) {
		case foundation.BaseOption:
			ec.baseOpts = append(ec.baseOpts, opt)

		case endOption, eventOption, data.PropertyOption:
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
		return nil, errors.Join(ee...)
	}

	return ec.endEvent()
}

// ------------------ flow.Node interface --------------------------------------

func (ee *EndEvent) Node() flow.Node {
	return ee
}

// ------------------ flow.EventNode interface ---------------------------------

func (ee *EndEvent) EventClass() flow.EventClass {
	return flow.EndEventClass
}

// ------------------ flow.SequenceTarget interface ----------------------------

// AcceptIncomingFlow checks if the EndEvent accepts incoming sequence flow sf.
func (ee *EndEvent) AcceptIncomingFlow(sf *flow.SequenceFlow) error {
	// EndEvent has no restrictions on incoming sequence flows
	return nil
}
