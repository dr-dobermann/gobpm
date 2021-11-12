package model

import (
	"fmt"

	"github.com/google/uuid"
)

type MessageFlow struct {
	FlowElement
	startRef Id
	endRef   Id
	message  Id
}

type MessageFlowDirection uint8

const (
	MfdIncoming MessageFlowDirection = 1 << iota
	MfdOutgoing
	MfdBidirectional = MfdIncoming | MfdOutgoing
)

type MessageState uint8

const (
	MsCreated  MessageState = 0x00
	MsRecieved              = 0x01
	MsSent                  = 0x02
)

type MessageVariable struct {
	Variable
	optional bool
}

type Message struct {
	FlowElement
	flow  Id
	event Id // Message event processor

	direction MessageFlowDirection
	mstate    MessageState
	vList     map[string]MessageVariable
}

func (m Message) State() MessageState {
	return m.mstate
}

func newMessage(mn string, dir MessageFlowDirection, vars ...MessageVariable) (*Message, error) {
	vl := map[string]MessageVariable{}

	if len(vars) == 0 {
		return nil, NewProcessModelError(Id(uuid.Nil),
			"couldn't create message "+mn+" with an empty variable list", nil)
	}

	for i, v := range vars {
		if len(v.name) == 0 {
			return nil, NewProcessModelError(Id(uuid.Nil),
				fmt.Sprintf("trying create a message %s with non-named variable (%d)", mn, i),
				nil)
		}

		for _, iv := range vl {
			if iv.name == v.name {
				return nil, NewProcessModelError(Id(uuid.Nil), "variable "+v.name+" already exists in the message "+mn, nil)
			}
		}

		vl[v.name] = v
	}

	return &Message{
		FlowElement: FlowElement{
			NamedElement: NamedElement{
				BaseElement: BaseElement{
					id: NewID()},
				name: mn},
			elementType: EtMessage},
		direction: dir,
		vList:     vl}, nil
}
