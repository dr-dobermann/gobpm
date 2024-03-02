package data

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

const (
	DSUndefined   = "UNDEFINED_DATA_STATE"
	DSUnavailable = "UNAVAILABLE_DATA_STATE"
	DSReady       = "READY_DATA_STATE"
)

var (
	UndefinedDataState = DataState{
		BaseElement: *foundation.MustBaseElement(
			foundation.WithId(DSUndefined)),
		name: "undefined",
	}

	UnavailableDataState = DataState{
		BaseElement: *foundation.MustBaseElement(
			foundation.WithId(DSUnavailable)),
		name: "unavailable",
	}

	ReadyDataState = DataState{
		BaseElement: *foundation.MustBaseElement(
			foundation.WithId("DSReady")),
		name: "ready",
	}
)

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
		return nil, &errs.ApplicationError{
			Message: "data state shouldn't be empty",
			Classes: []string{
				errorClass,
				errs.InvalidParameter,
			},
		}
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

// MustDataState tries to create a DataState and returns it. In case of
// error it panics.
func MustDataState(name string, baseOpts ...options.Option) *DataState {
	ds, err := NewDataState(name, baseOpts...)
	if err != nil {
		panic(err)
	}

	return ds
}

// Name returns the DataState name.
func (ds *DataState) Name() string {
	return ds.name
}
