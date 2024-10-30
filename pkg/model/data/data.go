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

	// ItemDefinition returns the Data's underlaying ItemDefinition.
	ItemDefinition() *ItemDefinition
}

// Source is implemented by objects which store Data.
type Source interface {
	// Get returns Data object named name.
	Find(ctx context.Context, name string) (Data, error)
}
