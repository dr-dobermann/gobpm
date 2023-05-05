package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/msgmarsh"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/google/uuid"
)

type Message[T any] struct {
	FlowElement
	//flow  Id
	//event Id // Message event processor

	item T
}

func (m Message[T]) GetItem() T {

	return m.item
}

func (m *Message[T]) UpdateItem(nItem T) {
	m.item = nItem
}

func (m Message[T]) MarshalJSON() (bdata []byte, e error) {

	mm := msgmarsh.MsgMarsh[T]{
		ID:   m.ID().String(),
		Name: m.Name(),
		Item: m.item,
	}

	return json.Marshal(&mm)
}

func (m *Message[T]) UnmarshalJSON(b []byte) error {
	var mm msgmarsh.MsgMarsh[T]

	err := json.Unmarshal(b, &mm)
	if err != nil {
		return fmt.Errorf("couldn't unmarshal transported msg: %v", err)
	}

	m.SetName(mm.Name)
	id, err := uuid.Parse(mm.ID)
	if err != nil {
		return fmt.Errorf("couldn't parse message id: %v", err)
	}

	m.SetNewID(identity.Id(id))
	m.SetType(EtMessage)
	m.item = mm.Item

	return nil
}

func NewMessage[T any](
	mn string,
	it T) (*Message[T], error) {

	mn = strings.Trim(mn, " ")
	if mn == "" {
		return nil, errors.New("couldn't create message with no name")
	}

	return &Message[T]{
		FlowElement: *NewElement(identity.NewID(), mn, EtMessage),
		item:        it,
	}, nil
}
