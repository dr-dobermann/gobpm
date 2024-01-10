package common

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/msgmarsh"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model"
	"github.com/dr-dobermann/gobpm/pkg/model/dataprovider"
	"github.com/google/uuid"
)

type Message struct {
	FlowElement
	//flow  Id
	//event Id // Message event processor

	item dataprovider.DataItem
}

func NewMessage(mn string, item dataprovider.DataItem) (*Message, error) {

	mn = strings.Trim(mn, " ")
	if mn == "" {
		return nil, errors.New("couldn't create message with no name")
	}

	return &Message{
		FlowElement: *NewElement(identity.NewID(), mn, EtMessage),
		item:        item.Copy(),
	}, nil
}

// GetItem returns a copy of message internal item.
func (m Message) GetItem() dataprovider.DataItem {

	return m.item.Copy()
}

// UpdateItem takes a copy of nItem and updates internal itme
// with the copy.
func (m *Message) UpdateItem(nItem dataprovider.DataItem) {

	m.item = nItem.Copy()
}

func (m Message) MarshalJSON() (bdata []byte, e error) {

	mm := msgmarsh.MsgMarsh{
		ID:   m.ID().String(),
		Name: m.Name(),
		Item: m.item.GetValue(),
	}

	return json.Marshal(&mm)
}

func (m *Message) UnmarshalJSON(b []byte) error {

	var mm msgmarsh.MsgMarsh

	err := json.Unmarshal(b, &mm)
	if err != nil {
		return model.NewModelError(m.name, m.ID(),
			err, "couldn't unmarshal Message")
	}

	m.SetName(mm.Name)
	id, err := uuid.Parse(mm.ID)
	if err != nil {
		return model.NewModelError(m.name, m.ID(),
			err, "couldn't parse Message id")
	}

	if err := m.item.UpdateValue(mm.Item); err != nil {
		return model.NewModelError(m.name, m.ID(),
			err, "couldn't unmarshal Message payload")
	}

	m.SetNewID(identity.Id(id))
	m.SetType(EtMessage)

	return nil
}
