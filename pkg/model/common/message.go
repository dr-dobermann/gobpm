package common

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type Message struct {
	foundation.BaseElement

	// Name is a text description of the Message.
	name string

	// An ItemDefinition is used to define the “payload” of the Message.
	item *data.ItemDefinition
}

// NewMessage creates a new Message object and returns its pointer on succes or
// error on failure.
func NewMessage(
	name string,
	item *data.ItemDefinition,
	baseOpts ...options.Option,
) (*Message, error) {
	name = strings.Trim(name, " ")

	if name == "" {
		return nil,
			&errs.ApplicationError{
				Message: "message should have non-empty name",
				Classes: []string{
					errorClass,
					errs.InvalidParameter,
				},
				Details: map[string]string{},
			}
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			&errs.ApplicationError{
				Err:     err,
				Message: "message creation error",
				Classes: []string{
					errorClass,
					errs.BulidingFailed,
				},
			}
	}

	return &Message{
		BaseElement: *be,
		name:        name,
		item:        item,
	}, nil
}

// MustMessage tries to create a Message and returns it pointer on success or
// panics on failure.
func MustMessage(
	name string,
	item *data.ItemDefinition,
	baseOpts ...options.Option,
) *Message {
	m, err := NewMessage(name, item, baseOpts...)
	if err != nil {
		panic(err.Error())
	}

	return m
}

// Name returns Mesaage name.
func (m Message) Name() string {
	return m.name
}

func (m *Message) Item() *data.ItemDefinition {
	return m.item
}
