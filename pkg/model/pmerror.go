package model

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/identity"
)

// process model error to keep context of the
// error occured.
type ProcessModelError struct {
	processID identity.Id
	msg       string
	Err       error
}

func (pme ProcessModelError) Error() string {
	return fmt.Sprintf("ERR: PRC[%v] %s: %v",
		pme.processID.String(),
		pme.msg,
		pme.Err)
}

func (pme ProcessModelError) Unwrap() error { return pme.Err }

func NewPMErr(pid identity.Id, err error, format string, params ...interface{}) error {
	return ProcessModelError{pid, fmt.Sprintf(format, params...), err}
}
