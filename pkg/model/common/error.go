package common

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model"
)

type Error struct {
	name  string
	descr string

	data *ItemDefinition
}

func NewError(name, descr string, data *ItemDefinition) (*Error, error) {

	name, descr = strings.Trim(name, " "), strings.Trim(descr, " ")
	if name == "" {
		return nil, model.NewModelError("invalid-name", identity.EmptyID(), nil,
			"couldn't create unnamed Error")
	}

	return &Error{
		name:  name,
		descr: descr,
		data:  data,
	}, nil
}

func MustError(name, descr string, data *ItemDefinition) *Error {

	e, err := NewError(name, descr, data)
	if err != nil {
		panic(err.Error())
	}

	return e
}

func (e Error) Name() string {

	return e.name
}

func (e Error) Descr() string {

	return e.descr
}

func (e *Error) Data() *ItemDefinition {

	return e.data
}

func (e *Error) SetData(data *ItemDefinition) {
	if data == nil {
		return
	}

	e.data = data
}
