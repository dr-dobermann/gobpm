package errs

import (
	"errors"
	"fmt"
)

var (
	ErrObjectCreation = errors.New("couldn't create object")
	ErrDuplicateID    = errors.New("object ID already exists")
	ErrEmptyObject    = errors.New("no object(nil reference)")
	ErrNotFound       = errors.New("object is not found")
)

const (
	ClassInvalidObject = "INVALID_OBJECT"
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

// func OperationFailed(err error, msg, details string) error {
// 	return fmt.Errorf("%s [%s]: %w", msg, details, err)
// }
