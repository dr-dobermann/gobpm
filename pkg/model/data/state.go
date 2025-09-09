package data

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const (
	// StateUndefined represents undefined data state.
	StateUndefined = "UNDEFINED_DATA"
	// StateUnavailable represents unavailable data state.
	StateUnavailable = "UNAVAILABLE_DATA"
	// StateReady represents ready data state.
	StateReady = "READY_DATA_STATE"
)

var (
	// UndefinedSrcState represents the undefined data state.
	UndefinedSrcState *SrcState
	// UnavailableDataState represents the unavailable data state.
	UnavailableDataState *SrcState
	// ReadyDataState represents the ready data state.
	ReadyDataState *SrcState
)

// SrcState represents a BPMN data state element.
// Data Object elements can optionally reference a SrcState element, which is
// the state of the data contained in the Data Object. The definition of these
// states, e.g., possible values and any specific semantic are out of scope of
// this International Standard. Therefore, BPMN adopters can use the State
// element and the BPMN extensibility capabilities to define their states.
type SrcState struct {
	foundation.BaseElement

	name string
}

// NewSrcState creates a new SrcState and returns its pointer on success
// or error on failure.
func NewSrcState(
	name string,
	baseOpts ...options.Option,
) (*SrcState, error) {
	name = strings.Trim(name, " ")
	if name == "" {
		return nil,
			errs.New(
				errs.M("data state should have non-empty name"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &SrcState{
		BaseElement: *be,
		name:        name,
	}, nil
}

// MustSrcState tries to create SrcState and returns its pointer on success or
// panics on failure.
func MustSrcState(
	name string,
	baseOpts ...options.Option,
) *SrcState {
	ds, err := NewSrcState(name, baseOpts...)
	if err != nil {
		errs.Panic("SrcState creation failed: " + err.Error())
	}

	return ds
}

// Name returns the SrcState name.
func (ds SrcState) Name() string {
	return ds.name
}

// CreateDefaultStates creates default SrcStates if need be.
func CreateDefaultStates() error {
	// do nothing if values already set
	if UndefinedSrcState != nil &&
		UnavailableDataState != nil &&
		ReadyDataState != nil {
		return nil
	}

	dss := map[string]*SrcState{
		StateUndefined:   nil,
		StateUnavailable: nil,
		StateReady:       nil}

	for sn := range dss {
		ds, err := NewSrcState(sn)
		if err != nil {
			return err
		}

		dss[sn] = ds
	}

	UndefinedSrcState = dss[StateUndefined]
	UnavailableDataState = dss[StateUnavailable]
	ReadyDataState = dss[StateReady]

	return nil
}
