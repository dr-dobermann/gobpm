package model

import (
	"fmt"
)

// process model error to keep context of the
// error occured.
type ProcessModelError struct {
	processID Id
	msg       string
	Err       error
}

func (pme ProcessModelError) Error() string {
	return fmt.Sprintf("ERR: Process[%v]  %s: %v",
		pme.processID.String(),
		pme.msg,
		pme.Err)
}

func (pme ProcessModelError) Unwrap() error { return pme.Err }

func NewPMErr(pid Id, err error, format string, params ...interface{}) error {
	return ProcessModelError{pid, fmt.Sprintf(format, params...), err}
}
