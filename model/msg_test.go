package model

import "testing"

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
		MfdIncoming,
		[]MessageVariable{
			{*V("x", VtInt, nil), false},
			{*V("y", VtInt, nil), false},
		}...)

	if m == nil || err != nil {
		t.Fatal("Couldn't add message to the process : ", err)
	}

	if m != nil && (m.Name() != mn || m.State() != MsCreated) {
		t.Error("invalid message attributes. Expected name : ",
			mn, ", state: 0, got name: ", m.Name(), ", state: ", m.State())
	}

	// empty message name
	if _, err := p.AddMessage("", MfdIncoming, []MessageVariable{}...); err == nil {
		t.Error("Registered message with an empty name")
	}

	// add duplicate
	md, err := p.AddMessage(mn,
		MfdIncoming,
		[]MessageVariable{}...)

	if md != nil || err == nil {
		t.Fatal("Duplicate were added")
	}

	// add message with an empty variables list
	if _, err := p.AddMessage("empty_var_list",
		MfdIncoming,
		[]MessageVariable{}...); err == nil {

		t.Error("Message with an empty variables list added")
	}

	// add message with duplicate variables
	if _, err := p.AddMessage("duplicate_variables",
		MfdIncoming,
		[]MessageVariable{
			{*V("x", VtInt, nil), false},
			{*V("x", VtInt, nil), false},
		}...); err == nil {

		t.Error("Added message with duplicate by name variables")
	}

	// add message with empty variable name
	if _, err := p.AddMessage("msg_with_empty_var_name",
		MfdIncoming,
		[]MessageVariable{
			{*V("x", VtInt, nil), false},
			{*V("", VtInt, nil), false},
		}...); err == nil {

		t.Error("Added message with an empty variable name")
	}

}
