package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/internal/msgmarsh"
	"github.com/dr-dobermann/gobpm/pkg/common"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	mid "github.com/dr-dobermann/gobpm/pkg/identity"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
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
	vars.Variable
	optional bool
}

func (mv MessageVariable) IsOptional() bool {
	return mv.optional
}

func NewMVar(v *vars.Variable, optional bool) *MessageVariable {
	return &MessageVariable{Variable: *v, optional: optional}
}

type Message struct {
	common.FlowElement
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

// Getv.Variables returns a list of variables, defined for the Message m.
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
		ID:        m.ID().String(),
		Name:      m.Name(),
		Direction: uint8(m.direction)}

	for _, v := range m.vList {
		rv := v.Values

		mm.Variables = append(mm.Variables, struct {
			Optional bool              `json:"optional"`
			Variable msgmarsh.VarMarsh `json:"variable"`
		}{v.optional, msgmarsh.VarMarsh{
			Name:      v.Name(),
			Type:      uint8(v.Type()),
			Precision: v.Precision(),
			Value: struct {
				Int    int64     `json:"int"`
				Bool   bool      `json:"bool"`
				String string    `json:"string"`
				Float  float64   `json:"float"`
				Time   time.Time `json:"time"`
			}{rv.I, rv.B, rv.S, rv.F, rv.T},
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

	m.SetName(mm.Name)
	m.SetNewID(mid.Id(uuid.MustParse(mm.ID)))
	m.mstate = Created
	m.SetType(common.EtMessage)
	m.direction = MessageFlowDirection(mm.Direction)
	if m.vList == nil {
		m.vList = make(map[string]MessageVariable)
	}

	for _, v := range mm.Variables {
		var nv *vars.Variable

		switch vars.Type(v.Variable.Type) {
		case vars.Int:
			nv = vars.V(
				v.Variable.Name,
				vars.Type(v.Variable.Type),
				v.Variable.Value.Int)

		case vars.Bool:
			nv = vars.V(
				v.Variable.Name,
				vars.Type(v.Variable.Type),
				v.Variable.Value.Bool)

		case vars.String:
			nv = vars.V(
				v.Variable.Name,
				vars.Type(v.Variable.Type),
				v.Variable.Value.String)

		case vars.Float:
			nv = vars.V(
				v.Variable.Name,
				vars.Type(v.Variable.Type),
				v.Variable.Value.Float)

		case vars.Time:
			nv = vars.V(
				v.Variable.Name,
				vars.Type(v.Variable.Type),
				v.Variable.Value.Time)

		}

		nv.SetPrecision(v.Variable.Precision)

		m.vList[nv.Name()] = MessageVariable{
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
		if len(strings.Trim(v.Name(), " ")) == 0 {
			return nil,
				fmt.Errorf(
					"trying create a message %s with non-named variable #%d",
					mn, i)
		}

		if _, ok := vl[v.Name()]; ok {
			return nil,
				fmt.Errorf(
					"variable %s already exists in the message %s",
					v.Name(), mn)
		}

		vl[v.Name()] = v
	}

	return &Message{
		FlowElement: *common.NewElement(identity.NewID(), mn, common.EtMessage),
		direction:   dir,
		vList:       vl}, nil
}
