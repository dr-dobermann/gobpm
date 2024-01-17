package errs

import (
	"errors"
	"fmt"
)

func OperationError(msg string, err error) error {
	return fmt.Errorf("%s: %w", msg, err)
}

var (
	ErrObjectCreation = errors.New("couldn't create object")
	ErrDuplicateID    = errors.New("object ID already exists")
	ErrEmptyObject    = errors.New("no object(nil reference)")
	ErrNotFound       = errors.New("object is not found")
)

func OperationFailed(err error, details string) error {
	return fmt.Errorf("%w: %s", err, details)
}
