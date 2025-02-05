package data

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const (
	StateUndefined   = "UNDEFINED_DATA"
	StateUnavailable = "UNAVAILABLE_DATA"
	StateReady       = "READY_DATA_STATE"
)

// Default DataStates. Initialized by calling CreateDefaultStates.
var UndefinedDataState, UnavailableDataState, ReadyDataState *DataState

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
		panic("DataState creation failed: " + err.Error())
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
		StateReady:       nil,
	}

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
