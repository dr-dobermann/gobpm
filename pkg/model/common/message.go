package common

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type Message struct {
	foundation.BaseElement

	// Name is a text description of the Message.
	name string

	// An ItemDefinition is used to define the “payload” of the Message.
	Item *data.ItemDefinition
}

// NewMessage creates a new Message object and returns its pointer.
func NewMessage(
	name string,
	item *data.ItemDefinition,
	baseOpts ...foundation.BaseOption,
) *Message {
	name = strings.Trim(name, " ")

	return &Message{
		BaseElement: *foundation.MustBaseElement(baseOpts...),
		name:        name,
		Item:        item,
	}
}

// Name returns Mesaage name.
func (m Message) Name() string {
	return m.name
}
