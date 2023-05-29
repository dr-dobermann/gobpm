package errs

import "errors"

var (
	DummyFuncImplementation = errors.New("stub function implementation")

	NotImplementedYet = errors.New("not implemented yet")
)
