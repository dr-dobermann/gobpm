package model

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/matryer/is"
)

func TestMessage(t *testing.T) {
	var (
		m   *Message
		err error
	)

	p := NewProcess(NewID(), "test_process", "v0.1.0")
	if p == nil {
		panic("couldn't create a proccess")
	}

	mn := "test_msg"
	m, err = p.AddMessage(mn,
		Incoming,
		[]MessageVariable{
			{*V("x", VtInt, nil), false},
			{*V("y", VtInt, nil), false},
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
			{*V("x", VtInt, nil), false},
			{*V("x", VtInt, nil), false},
		}...); err == nil {

		t.Error("Added message with duplicate by name variables")
	}

	// add message with empty variable name
	if _, err := p.AddMessage("msg_with_empty_var_name",
		Incoming,
		[]MessageVariable{
			{*V("x", VtInt, nil), false},
			{*V("", VtInt, nil), false},
		}...); err == nil {

		t.Error("Added message with an empty variable name")
	}

}

func TestMsgMarshalling(t *testing.T) {
	is := is.New(t)

	bd, err := time.Parse(time.RFC3339, "1973-02-23T05:15:00+06:00")
	is.NoErr(err)

	testVars := map[string]*Variable{
		"iVar": V("iVar", VtInt, 10),
		"bVar": V("bVar", VtBool, true),
		"sVar": V("sVar", VtString, "Hello Dober!"),
		"fVar": V("fVar", VtFloat, 48.9),
		"tVal": V("tVal", VtTime, bd)}

	msg := new(Message)
	msg.name = "TestVar"
	msg.elementType = EtMessage
	msg.id = NewID()
	msg.direction = Outgoing
	msg.vList = make(map[string]MessageVariable)

	for _, v := range testVars {
		msg.vList[v.name] = MessageVariable{
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

		if mv.Variable.i != tv.i ||
			mv.Variable.b != tv.b ||
			mv.Variable.s != tv.s ||
			mv.Variable.f != tv.f ||
			!mv.Variable.t.Equal(tv.t) {

			t.Fatalf("vairable %s has different values: want %v\n, got %v\n",
				mn, tv, mv)
		}
	}
}
