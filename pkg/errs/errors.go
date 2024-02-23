package errs

import (
	"fmt"
)

const (
	InvalidObject    = "INVALID_OBJECT"
	NilDereference   = "NIL_DEREFERENCE"
	BulidingFailed   = "BUILDING_FAILED"
	InvalidParameter = "INVALID_PARAMETER"

	TypeCastingError = "InvalidTypeCasting"

	OutOfRangeError      = "OutOfRange"
	EmptyCollectionError = "CollectionIsEmpty"
)

type ApplicationError struct {
	Err     error
	Message string
	Classes []string
	Details map[string]string
}

func (ap *ApplicationError) Error() string {
	return fmt.Sprintf("%v: %q (%s): %v",
		ap.Classes, ap.Message, ap.Details, ap.Err)
}
