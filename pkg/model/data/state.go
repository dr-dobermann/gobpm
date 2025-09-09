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
	// UndefinedDataState represents the undefined data state.
	UndefinedDataState *DataState
	// UnavailableDataState represents the unavailable data state.
	UnavailableDataState *DataState
	// ReadyDataState represents the ready data state.
	ReadyDataState *DataState
)

// DataState represents a BPMN data state element.
// Data Object elements can optionally reference a DataState element, which is
// the state of the data contained in the Data Object. The definition of these
// states, e.g., possible values and any specific semantic are out of scope of
// this International Standard. Therefore, BPMN adopters can use the State
// element and the BPMN extensibility capabilities to define their states.
type DataState struct {
	foundation.BaseElement

	name string
}

// NewDataState creates a new DataState and returns its pointer on success
// or error on failure.
func NewDataState(
	name string,
	baseOpts ...options.Option,
) (*DataState, error) {
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

	return &DataState{
		BaseElement: *be,
		name:        name,
	}, nil
}

// MustDataState tries to create DataState and returns its pointer on success or
// panics on failure.
func MustDataState(
	name string,
	baseOpts ...options.Option,
) *DataState {
	ds, err := NewDataState(name, baseOpts...)
	if err != nil {
		errs.Panic("DataState creation failed: " + err.Error())
	}

	return ds
}

// Name returns the DataState name.
func (ds DataState) Name() string {
	return ds.name
}

// CreateDefaultStates creates default DataStates if need be.
func CreateDefaultStates() error {
	// do nothing if values already set
	if UndefinedDataState != nil &&
		UnavailableDataState != nil &&
		ReadyDataState != nil {
		return nil
	}

	dss := map[string]*DataState{
		StateUndefined:   nil,
		StateUnavailable: nil,
		StateReady:       nil}

	for sn := range dss {
		ds, err := NewDataState(sn)
		if err != nil {
			return err
		}

		dss[sn] = ds
	}

	UndefinedDataState = dss[StateUndefined]
	UnavailableDataState = dss[StateUnavailable]
	ReadyDataState = dss[StateReady]

	return nil
}
