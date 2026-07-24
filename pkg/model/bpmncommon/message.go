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
//   - foundation.WithID
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

// msgCloneErr classifies a message-clone item rebuild failure (FIX-026).
func msgCloneErr(msgName string, err error) error {
	return errs.New(
		errs.M("couldn't rebuild cloned message item"),
		errs.C(errorClass, errs.OperationFailed),
		errs.E(err),
		errs.D("message_name", msgName))
}

// Clone returns a per-instance copy of the Message that preserves the message
// id and name but carries a fresh ItemDefinition whose structure is cloned, so
// runtime mutation of the item's value on one instance does not leak into
// another. A Message with a nil item is cloned as-is (fresh shell, no item).
// It returns an error instead of panicking when the item rebuild fails
// (FIX-026 — the Must* twins are for tests/fixtures, not library paths).
func (m *Message) Clone() (*Message, error) {
	var item *data.ItemDefinition

	if m.item != nil {
		var structure data.Value
		if m.item.Structure() != nil {
			structure = m.item.Structure().Clone()
		}

		cloned, err := data.NewItemDefinition(
			structure,
			data.WithKind(m.item.Kind()),
			foundation.WithID(m.item.ID()))
		if err != nil {
			return nil, msgCloneErr(m.name, err)
		}

		item = cloned
	}

	return &Message{
		// Value-copy the base so the clone keeps the id AND the documentation,
		// not just the id (FIX-014 1.9) — mirroring flow.BaseElement.cloneIdentity.
		// docs are immutable after construction (Docs() returns a copy), so
		// sharing the slice header is safe.
		BaseElement: m.BaseElement,
		name:        m.name,
		item:        item,
	}, nil
}

// Name returns Mesaage name.
func (m Message) Name() string {
	return m.name
}

// Item returns ItemDefinition of the Message.
func (m Message) Item() *data.ItemDefinition {
	return m.item
}
