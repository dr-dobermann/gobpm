package bpmncommon

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Message represents a BPMN message element.
type Message struct {
	item *data.ItemDefinition
	name string
	foundation.BaseElement
}

// NewMessage creates a new Message object and returns its pointer on succes or
// error on failure.
//
// Available options:
//
//   - foundation.WithId
//   - foundation.WithDoc
func NewMessage(
	name string,
	item *data.ItemDefinition,
	baseOpts ...options.Option,
) (*Message, error) {
	name = strings.Trim(name, " ")

	if name == "" {
		return nil,
			errs.New(
				errs.M("message should have non-empty name"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	if item == nil {
		return nil,
			errs.New(
				errs.M("empty item definition isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
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
		errs.Panic(err)
	}

	return m
}

// Clone returns a per-instance copy of the Message that preserves the message
// id and name but carries a fresh ItemDefinition whose structure is cloned, so
// runtime mutation of the item's value on one instance does not leak into
// another. A Message with a nil item is cloned as-is (fresh shell, no item).
//
// Cloning a valid Message cannot produce an invalid one — item id, kind and the
// cloned structure all originate from an already-valid item — so the helper uses
// the Must form, mirroring data.Value.Clone, which likewise does not error.
func (m *Message) Clone() *Message {
	var item *data.ItemDefinition

	if m.item != nil {
		var structure data.Value
		if m.item.Structure() != nil {
			structure = m.item.Structure().Clone()
		}

		item = data.MustItemDefinition(
			structure,
			data.WithKind(m.item.Kind()),
			foundation.WithID(m.item.ID()))
	}

	return &Message{
		BaseElement: *foundation.MustBaseElement(foundation.WithID(m.ID())),
		name:        m.name,
		item:        item,
	}
}

// Name returns Mesaage name.
func (m Message) Name() string {
	return m.name
}

// Item returns ItemDefinition of the Message.
func (m Message) Item() *data.ItemDefinition {
	return m.item
}
