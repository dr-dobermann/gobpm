package model

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/identity"
)

type ModelError struct {
	eID   identity.Id
	eName string
	msg   string
	Err   error
}

func (me ModelError) Error() string {

	if me.Err == nil {
		return fmt.Sprintf("error on element %s[%v]: %s",
			me.eName, me.eID, me.msg)
	}

	return fmt.Sprintf("error on element %s[%v]: %s : %s",
		me.eName, me.eID, me.msg, me.Err.Error())
}

func NewModelError(eName string, eID identity.Id, err error,
	format string, params ...interface{}) error {

	return ModelError{eID, eName, fmt.Sprintf(format, params...), err}
}
