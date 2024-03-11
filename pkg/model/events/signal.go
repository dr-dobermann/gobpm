package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
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
	baseOpts ...options.Option,
) (*Signal, error) {
	name = trim(name)

	if err := checkStr(name, "name should be provided fro Signal"); err != nil {
		return nil, err
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "signal building error",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}

	return &Signal{
		BaseElement: *be,
		name:        name,
		structure:   str,
	}, nil
}

// Name returns the Signal's name.
func (s *Signal) Name() string {
	return s.name
}

// Item returns the Signal's internal structure.
func (s *Signal) Item() *data.ItemDefinition {
	return s.structure
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
	baseOpts ...options.Option,
) (*SignalEventDefinition, error) {
	if signal == nil {
		return nil,
			&errs.ApplicationError{
				Message: "signal isn't provided",
				Classes: []string{
					errorClass,
					errs.InvalidParameter},
			}
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "signal event definition building error",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}

	return &SignalEventDefinition{
		definition: *d,
		signal:     signal,
	}, nil
}

// MustSignalEventDefinition tries to create a new SignalEventDefinition.
// If there is an error occured, then panic fired.
func MustSignalEventDefinition(
	signal *Signal,
	baseOpts ...options.Option,
) *SignalEventDefinition {
	sed, err := NewSignalEventDefinition(signal, baseOpts...)
	if err != nil {
		panic(err.Error())
	}

	return sed
}

// Signal returns the base signal of the SignalEventDefinition.
func (sed *SignalEventDefinition) Signal() *Signal {
	return sed.signal
}
