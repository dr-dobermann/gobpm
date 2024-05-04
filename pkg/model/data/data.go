package data

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// common error class for data package errors.
const errorClass = "DATA_ERRORS"

// Data is implemented by elements which holds data: Property, Parameter and
// DataObjects.
type Data interface {
	foundation.BaseObject
	foundation.Namer

	// Value returns the Value of the object.
	Value() Value

	// State returns current DataState of the object.
	State() DataState
}

// DataSource is implemented by objects which keeps any number of Data:
// some EventDefinitions.
type DataSource interface {
	// GetData returns all Data objects of the DataSource.
	GetData(ctx context.Context) ([]Data, error)
}

// DataHolder is implemented by object, which depends or control some
// Data (eg. some EventDefinitions, ...)
type DataHolder interface {
	// GetData returns a list of Data the objects controls.
	GetData() []Data
}
