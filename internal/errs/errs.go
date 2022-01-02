package errs

import "errors"

var (
	ErrNoLogger      = errors.New("no logger")
	ErrAlreadyRunned = errors.New("already runned")
	ErrNotRunned     = errors.New("not runned")

	ErrNoTracks     = errors.New("instance has no tracks to run")
	ErrInvalidTrack = errors.New("invalid track id or empty track object")

	ErrNotImplementedYet = errors.New("not implemented yet")
)
