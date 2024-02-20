package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// *****************************************************************************
type Signal struct {
	foundation.BaseElement

	name string

	structure *data.ItemDefinition
}

// NewSignal creates a new signal and returns its pointer.
func NewSignal(
	name string,
	str *data.ItemDefinition,
	baseOpts ...foundation.BaseOption,
) *Signal {

	return &Signal{
		BaseElement: *foundation.MustBaseElement(baseOpts...),
		name:        name,
		structure:   str,
	}
}

// *****************************************************************************
type SignalEventDefinition struct {
	definition

	// If the trigger is a Signal, then a Signal is provided.
	signal *Signal
}

// Type implements the Definition interface.
func (*SignalEventDefinition) Type() Trigger {

	return TriggerSignal
}

// NewSignalEventDefinition creates a new SignalEventDefinition with given
// signal. If signal is empty, then error returned.
func NewSignalEventDefinition(
	signal *Signal,
	baseOpts ...foundation.BaseOption,
) (*SignalEventDefinition, error) {
	if signal == nil {
		return nil,
			&errs.ApplicationError{
				Message: "signal isn't provided",
				Classes: []string{
					eventErrorClass,
					errs.InvalidParameter},
			}
	}

	return &SignalEventDefinition{
		definition: *newDefinition(baseOpts...),
		signal:     signal,
	}, nil
}

// MustSignalEventDefinition tries to create a new SignalEventDefinition.
// If there is an error occured, then panic fired.
func MustSignalEventDefinition(
	signal *Signal,
	baseOpts ...foundation.BaseOption,
) *SignalEventDefinition {
	sed, err := NewSignalEventDefinition(signal, baseOpts...)
	if err != nil {
		panic(err.Error())
	}

	return sed
}
