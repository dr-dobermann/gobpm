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
	id, name string,
	str *data.ItemDefinition,
	docs ...*foundation.Documentation,
) *Signal {

	return &Signal{
		BaseElement: *foundation.NewBaseElement(id, docs...),
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
	id string,
	signal *Signal,
	docs ...*foundation.Documentation,
) (*SignalEventDefinition, error) {
	if signal == nil {
		return nil,
			&errs.ApplicationError{
				Message: "signal isn't provided",
				Classes: []string{
					eventErrorClass,
					errs.InvalidParameter},
				Details: map[string]string{
					"definition_id": id,
				},
			}
	}

	return &SignalEventDefinition{
		definition: *newDefinition(id, docs...),
		signal:     signal,
	}, nil
}

// MustSignalEventDefinition tries to create a new SignalEventDefinition.
// If there is an error occured, then panic fired.
func MustSignalEventDefinition(
	id string,
	signal *Signal,
	docs ...*foundation.Documentation,
) *SignalEventDefinition {
	sed, err := NewSignalEventDefinition(id, signal, docs...)
	if err != nil {
		panic(err.Error())
	}

	return sed
}
