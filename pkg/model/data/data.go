package data

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

// common error class for data package errors.
const errorClass = "DATA_ERRORS"

type Data interface {
	foundation.BaseObject
	foundation.Namer

	Value() Value
	State() DataState
}
