package errs

import (
	"fmt"
)

const (
	InvalidObjectError       = "INVALID_OBJECT_ERROR"
	NilReferenceError        = "NIL_REF_ERROR"
	ClassObjectBulidingError = "OBJ_BUILDING_ERROR"
)

type ApplicationError struct {
	Err     error
	Message string
	Class   string
	Details map[string]string
}

func (ap *ApplicationError) Error() string {
	return fmt.Sprintf("%s: %s[%s]: %v",
		ap.Class, ap.Message, ap.Details, ap.Err)
}
