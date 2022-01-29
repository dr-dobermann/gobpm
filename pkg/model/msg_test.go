package model

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	mid "github.com/dr-dobermann/gobpm/pkg/identity"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
	"github.com/matryer/is"
)

func TestMessage(t *testing.T) {
	var (
		m   *Message
		err error
	)

	p := NewProcess(mid.NewID(), "test_process", "v0.1.0")
	if p == nil {
		panic("couldn't create a proccess")
	}

	mn := "test_msg"
	m, err = p.AddMessage(mn,
		Incoming,
		[]MessageVariable{
			{*vars.V("x", vars.Int, nil), false},
			{*vars.V("y", vars.Int, nil), false},
		}...)

	if m == nil || err != nil {
		t.Fatal("Couldn't add message to the process : ", err)
	}

	if m != nil && (m.Name() != mn || m.State() != Created) {
		t.Error("invalid message attributes. Expected name : ",
			mn, ", state: 0, got name: ", m.Name(), ", state: ", m.State())
	}

	// empty message name
	if _, err := p.AddMessage("", Incoming, []MessageVariable{}...); err == nil {
		t.Error("Registered message with an empty name")
	}

	// add duplicate
	md, err := p.AddMessage(mn,
		Incoming,
		[]MessageVariable{}...)

	if md != nil || err == nil {
		t.Fatal("Duplicate were added")
	}

	// add message with an empty variables list
	if _, err := p.AddMessage("empty_var_list",
		Incoming,
		[]MessageVariable{}...); err == nil {

		t.Error("Message with an empty variables list added")
	}

	// add message with duplicate variables
	if _, err := p.AddMessage("duplicate_variables",
		Incoming,
		[]MessageVariable{
			{*vars.V("x", vars.Int, nil), false},
			{*vars.V("x", vars.Int, nil), false},
		}...); err == nil {

		t.Error("Added message with duplicate by name variables")
	}

	// add message with empty variable name
	if _, err := p.AddMessage("msg_with_empty_var_name",
		Incoming,
		[]MessageVariable{
			{*vars.V("x", vars.Int, nil), false},
			{*vars.V("", vars.Int, nil), false},
		}...); err == nil {

		t.Error("Added message with an empty variable name")
	}

}

func TestMsgMarshalling(t *testing.T) {
	is := is.New(t)

	bd, err := time.Parse(time.RFC3339, "1973-02-23T05:15:00+06:00")
	is.NoErr(err)

	testVars := map[string]*vars.Variable{
		"iVar": vars.V("iVar", vars.Int, 10),
		"bVar": vars.V("bVar", vars.Bool, true),
		"sVar": vars.V("sVar", vars.String, "Hello Dober!"),
		"fVar": vars.V("fVar", vars.Float, 48.9),
		"tVal": vars.V("tVal", vars.Time, bd)}

	msg := new(Message)
	msg.name = "TestVar"
	msg.elementType = EtMessage
	msg.SetNewID(mid.NewID())
	msg.direction = Outgoing
	msg.vList = make(map[string]MessageVariable)

	for _, v := range testVars {
		msg.vList[v.Name()] = MessageVariable{
			Variable: *v,
			optional: false,
		}
	}

	buf, err := json.Marshal(msg)
	is.NoErr(err)
	fmt.Println(string(buf))

	var uMsg Message

	err = json.Unmarshal(buf, &uMsg)
	is.NoErr(err)

	if msg.name != uMsg.name ||
		msg.direction != uMsg.direction ||
		msg.elementType != uMsg.elementType {

		t.Fatalf("message header is not the same:\n%v\n%v\n",
			msg, uMsg)
	}

	for mn, mv := range uMsg.vList {
		tv, ok := testVars[mn]
		if !ok {
			t.Fatalf("variable %s is not found\n", mn)
		}

		if mv.Variable.I != tv.I ||
			mv.Variable.B != tv.B ||
			mv.Variable.S != tv.S ||
			mv.Variable.F != tv.F ||
			!mv.Variable.T.Equal(tv.T) {

			t.Fatalf("vairable %s has different values: want %v\n, got %v\n",
				mn, tv, mv)
		}
	}
}
