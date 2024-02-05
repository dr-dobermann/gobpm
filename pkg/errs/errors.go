package errs

import (
	"fmt"
)

const (
	InvalidObject  = "INVALID_OBJECT"
	NilDereference = "NIL_DEREF"
	ObjectBuliding = "OBJ_BUILDING"
)

type ApplicationError struct {
	Err     error
	Message string
	Classes []string
	Details map[string]string
}

func (ap *ApplicationError) Error() string {
	return fmt.Sprintf("%v: %s[%s]: %v",
		ap.Classes, ap.Message, ap.Details, ap.Err)
}
