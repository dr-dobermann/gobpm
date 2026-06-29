package events

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Signal represents a signal event.
type Signal struct {
	structure *data.ItemDefinition
	name      string
	foundation.BaseElement
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

// SignalEventDefinition represents a signal event definition.
type SignalEventDefinition struct {
	signal *Signal
	definition
}

// Compile-time conformance (FIX-011): GetItemsList must override the embedded
// definition's empty implementation; a misspelling would silently report no
// items.
var _ flow.EventDefinition = (*SignalEventDefinition)(nil)

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
// If there is an error occurred, then panic fired.
func MustSignalEventDefinition(
	signal *Signal,
	baseOpts ...options.Option,
) *SignalEventDefinition {
	sed, err := NewSignalEventDefinition(signal, baseOpts...)
	if err != nil {
		errs.Panic(err)
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
// data.ItemDefinition with iDefID Id.
func (sed *SignalEventDefinition) CheckItemDefinition(iDefID string) bool {
	if sed.signal.structure == nil {
		return false
	}

	return sed.signal.structure.ID() == iDefID
}

// GetItemsList returns a list of data.ItemDefinition the EventDefinition
// is based on. It overrides the embedded definition's empty implementation so a
// signal's payload is reported (the method was previously misspelled
// GetItemList and never overrode flow.EventDefinition — FIX-011).
// If EventDefinition isn't based on any data.ItemDefinition, empty list
// will be returned.
func (sed *SignalEventDefinition) GetItemsList() []*data.ItemDefinition {
	idd := make([]*data.ItemDefinition, 0, 1)

	if sed.signal.structure == nil {
		return idd
	}

	return append(idd, sed.signal.structure)
}

// -----------------------------------------------------------------------------
