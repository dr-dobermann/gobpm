package errs

import "errors"

var (
	ErrNoLogger      = errors.New("no logger")
	ErrAlreadyRunned = errors.New("already runned")

	ErrNotImplementedYet = errors.New("not implemented yet")
)
