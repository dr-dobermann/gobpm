package events

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
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
	name = strings.TrimSpace(name)

	if err := errs.CheckStr(
		name,
		"name should be provided fro Signal",
		errorClass,
	); err != nil {
		return nil, err
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
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

// NewSignalEventDefinition creates a new SignalEventDefinition with given
// signal. If signal is empty, then error returned.
func NewSignalEventDefinition(
	signal *Signal,
	baseOpts ...options.Option,
) (*SignalEventDefinition, error) {
	if signal == nil {
		return nil,
			errs.New(
				errs.M("signal isn't provided"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
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
		panic(err)
	}

	return sed
}

// Signal returns the base signal of the SignalEventDefinition.
func (sed *SignalEventDefinition) Signal() *Signal {
	return sed.signal
}

// ---------------- flow.EventDefinition interface -----------------------------

// Type returns the SignalEventDefinition's flow.EventTrigger.
func (*SignalEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerSignal
}

// CheckItemDefinition check if definition is related with
// data.ItemDefinition with iDefId Id.
func (sed *SignalEventDefinition) CheckItemDefinition(iDefId string) bool {
	if sed.signal.structure == nil {
		return false
	}

	return sed.signal.structure.Id() == iDefId
}

// GetItemList returns a list of data.ItemDefinition the EventDefinition
// is based on.
// If EventDefiniton isn't based on any data.ItemDefiniton, empty list
// wil be returned.
func (sed *SignalEventDefinition) GetItemList() []*data.ItemDefinition {
	idd := []*data.ItemDefinition{}

	if sed.signal.structure == nil {
		return idd
	}

	return append(idd, sed.signal.structure)
}

// -----------------------------------------------------------------------------
