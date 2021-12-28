package model

import (
	"errors"
	"fmt"
	"strings"
)

// type MessageFlow struct {
// 	FlowElement
// 	startRef Id
// 	endRef   Id
// 	message  Id
// }

type MessageFlowDirection uint8

const (
	Incoming MessageFlowDirection = 1 << iota
	Outgoing
	Bidirectional = Incoming | Outgoing
)

type MessageState uint8

const (
	Created  MessageState = 0x00
	Recieved MessageState = 0x01
	Sent     MessageState = 0x02
)

type MessageVariable struct {
	Variable
	optional bool
}

type Message struct {
	FlowElement
	//flow  Id
	//event Id // Message event processor

	direction MessageFlowDirection
	mstate    MessageState
	vList     map[string]MessageVariable
}

func (m Message) State() MessageState {
	return m.mstate
}

// GetVariables returns a list of variables, defined for the Message m.
// if nonOptionalOnly is true, then only variables with optional == false
// will be returned.
func (m Message) GetVariables(nonOptionalOnly bool) []Variable {
	vv := []Variable{}

	for _, mv := range m.vList {
		if nonOptionalOnly && mv.optional {
			continue
		}
		vv = append(vv, mv.Variable)
	}

	return vv
}

func newMessage(
	mn string,
	dir MessageFlowDirection,
	vars ...MessageVariable) (*Message, error) {

	mn = strings.Trim(mn, " ")
	if mn == "" {
		return nil, errors.New("couldn't create message with no name")
	}

	vl := map[string]MessageVariable{}

	if len(vars) == 0 {
		return nil,
			fmt.Errorf(
				"couldn't create message '%s' with an empty variable list",
				mn)
	}

	for i, v := range vars {
		v.name = strings.Trim(v.name, " ")
		if len(v.name) == 0 {
			return nil,
				fmt.Errorf(
					"trying create a message %s with non-named variable #%d",
					mn, i)
		}

		if _, ok := vl[v.name]; ok {
			return nil,
				fmt.Errorf(
					"variable %s already exists in the message %s",
					v.name, mn)
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
