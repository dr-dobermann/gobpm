package errs

import "errors"

var (
	ErrNoLogger      = errors.New("no logger")
	ErrAlreadyRunned = errors.New("already runned")
	ErrNotRunned     = errors.New("not runned")

	ErrDummyFuncImplementation = errors.New("stub function implementation")

	ErrNoTracks     = errors.New("instance has no tracks to run")
	ErrInvalidTrack = errors.New("invalid track id or empty track object")

	ErrEmptyVarStore = errors.New("VarStore is empty")
	ErrNotCalculated = errors.New("expression is not calculated")
	ErrNoVariable    = errors.New("no variable or empty variable given")

	ErrIsNotACollection = errors.New("DataAccessor is not a collection")

	ErrNotImplementedYet = errors.New("not implemented yet")
)
