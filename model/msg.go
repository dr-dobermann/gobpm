package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/internal/msgmarsh"
	"github.com/google/uuid"
)

// type MessageFlow struct {
// 	FlowElement
// 	startRef Id
// 	endRef   Id
// 	message  Id
// }

const (
	OnlyNonOptional = true
	AllVariables    = false

	Optional = true
	Required = false
)

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

func (mv MessageVariable) IsOptional() bool {
	return mv.optional
}

func NewMVar(v *Variable, optional bool) *MessageVariable {
	return &MessageVariable{Variable: *v, optional: optional}
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

func (m Message) Direction() MessageFlowDirection {
	return m.direction
}

func (m Message) GetVar(name string) (MessageVariable, bool) {
	mv, ok := m.vList[name]
	return mv, ok
}

// GetVariables returns a list of variables, defined for the Message m.
// if nonOptionalOnly is true, then only variables with optional == false
// will be returned.
// []bool slice returns a optional characteristic of the according variable.
func (m Message) GetVariables(nonOptionalOnly bool) []MessageVariable {
	vv := []MessageVariable{}

	for _, mv := range m.vList {
		if nonOptionalOnly && mv.optional {
			continue
		}
		vv = append(vv, mv)
	}

	return vv
}

func (m Message) MarshalJSON() (bdata []byte, e error) {

	mm := msgmarsh.MsgMarsh{
		ID:        m.id.String(),
		Name:      m.name,
		Direction: uint8(m.direction)}

	for _, v := range m.vList {
		mm.Variables = append(mm.Variables, struct {
			Optional bool              `json:"optional"`
			Variable msgmarsh.VarMarsh `json:"variable"`
		}{v.optional, msgmarsh.VarMarsh{
			Name:      v.name,
			Type:      uint8(v.vtype),
			Precision: v.prec,
			Value: struct {
				Int    int64     `json:"int"`
				Bool   bool      `json:"bool"`
				String string    `json:"string"`
				Float  float64   `json:"float"`
				Time   time.Time `json:"time"`
			}{v.variableValues.i, v.variableValues.b,
				v.variableValues.s, v.variableValues.f, v.variableValues.t},
		}})
	}

	return json.Marshal(&mm)
}

func (m *Message) UnmarshalJSON(b []byte) error {
	var mm msgmarsh.MsgMarsh

	err := json.Unmarshal(b, &mm)
	if err != nil {
		return fmt.Errorf("couldn't unmarshal transported msg: %v", err)
	}

	m.name = mm.Name
	m.id = Id(uuid.MustParse(mm.ID))
	m.mstate = Created
	m.elementType = EtMessage
	m.direction = MessageFlowDirection(mm.Direction)
	if m.vList == nil {
		m.vList = make(map[string]MessageVariable)
	}

	for _, v := range mm.Variables {
		var nv *Variable

		switch VarType(v.Variable.Type) {
		case VtInt:
			nv = V(
				v.Variable.Name,
				VarType(v.Variable.Type),
				v.Variable.Value.Int)

		case VtBool:
			nv = V(
				v.Variable.Name,
				VarType(v.Variable.Type),
				v.Variable.Value.Bool)

		case VtString:
			nv = V(
				v.Variable.Name,
				VarType(v.Variable.Type),
				v.Variable.Value.String)

		case VtFloat:
			nv = V(
				v.Variable.Name,
				VarType(v.Variable.Type),
				v.Variable.Value.Float)

		case VtTime:
			nv = V(
				v.Variable.Name,
				VarType(v.Variable.Type),
				v.Variable.Value.Time)

		}

		nv.SetPrecision(v.Variable.Precision)

		m.vList[nv.name] = MessageVariable{
			Variable: *nv,
			optional: v.Optional,
		}
	}

	return nil
}

func NewMessage(
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
