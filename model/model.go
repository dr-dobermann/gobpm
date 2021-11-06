package model

import (
	"fmt"
)

type ModelState uint8

const (
	MSCreated ModelState = iota
	MSStarted
	MSFinished
)

type ModelError struct {
	msg string
	Err error
}

func (me ModelError) Error() string {
	return fmt.Sprintf("ME: %s : %s",
		me.msg, me.Err.Error())
}

func NewModelError(msg string, err error) error {
	return ModelError{msg, err}
}
