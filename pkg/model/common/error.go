package common

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/identity"
)

type Error struct {
	name string
	code string

	structure ItemDefinition
}

type ModelError struct {
	eID   identity.Id
	eName string
	msg   string
	Err   error
}

func (me ModelError) Error() string {
	return fmt.Sprintf("error on element %s[%v]: %s : %s",
		me.eName, me.eID, me.msg, me.Err.Error())
}

func NewModelError(eName string, eID identity.Id, err error, format string, params ...interface{}) error {
	return ModelError{eID, eName, fmt.Sprintf(format, params...), err}
}

// process model error to keep context of the
// error occured.
// type ProcessModelError struct {
// 	processID identity.Id
// 	msg       striId
// 	Err       error
// }

// func (pme ProcessModelError) Error() string {
// 	return fmt.Sprintf("ERR: PRC[%v] %s: %v",
// 		pme.processID.String(),
// 		pme.msg,
// 		pme.Err)
// }

// func (pme ProcessModelError) Unwrap() error { return pme.Err }

// func NewPMErr(pid identity.Id, err error, format string, params ...interface{}) error {
// 	return ProcessModelError{pid, fmt.Sprintf(format, params...), err}
// }
