package model

import (
	"fmt"

	"github.com/google/uuid"
)

type ModelState uint8

const (
	MSCreated ModelState = iota
	MSStarted
	MSFinished
)

type Model struct {
	NamedVersionedElement
	state ModelState
}

func (m Model) State() ModelState {
	return m.state
}

type ModelError struct {
	modelID uuid.UUID
	msg     string
	Err     error
}

func (me ModelError) Error() string {
	return fmt.Sprintf("M[%v]: %s : %s",
		me.modelID, me.msg, me.Err.Error())
}

func NewModelError(mId uuid.UUID, msg string, err error) error {
	return ModelError{mId, msg, err}
}
