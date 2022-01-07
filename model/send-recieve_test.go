package model

import (
	"os"
	"testing"

	"github.com/matryer/is"
)

var test_queue = "test_queue"

func TestSendProcess(t *testing.T) {
	p := getSendProcess(t)
	if p == nil {
		t.Fatal("Couldn't create send process")
	}

	if l := len(p.tasks); l != 2 {
		t.Fatal("Invalid send process tasks count ", l)
	}
}

func TestReceiveProcess(t *testing.T) {
	p := getRecieveProcess(t)
	if p == nil {
		t.Fatal("Couldn't create receive process")
	}
}

func getSendProcess(t *testing.T) *Process {
	is := is.New(t)

	p := NewProcess(NewID(), "Test Send Process", "0.1.0")

	str := NewStoreTask(p, "Store X", *V("x", VtInt, 10))
	snd := NewSendTask(p, "Send X", "letter_X", test_queue)
	if str == nil || snd == nil {
		t.Error("Couldn't create store|send tasks")
		return nil
	}

	if err := p.NewLane("Sender"); err != nil {
		t.Error("Couldn't add a Lane Sender", err)
		return nil
	}

	if _, err := p.AddMessage("letter_X",
		Outgoing, MessageVariable{*V("x", VtInt, 0), false}); err != nil {
		t.Error("Couldn't add outgoing message letter_X : ", err)
		return nil
	}

	if len(p.messages) != 1 {
		t.Error("Message definition wasn't added")
		return nil
	}

	m, err := p.GetMessage("letter_X")
	if err != nil {
		t.Error("Couldn't retrieve the message letter_X from the process")
		return nil
	}

	if m == nil {
		t.Error("Message is empty")
		return nil
	}

	vv := m.GetVariables(AllVariables)
	if len(vv) != 1 {
		t.Error("Invalid variables count", len(vv))
		return nil
	}

	if vv[0].name != "x" {
		t.Error("Invalid variable name", vv[0].name)
		return nil
	}

	if _, err := p.AddMessage("letter_Y",
		Outgoing, MessageVariable{*V("y", VtInt, 0), true}); err != nil {
		t.Error("Couldn't add outgoing message letter_X : ", err)
		return nil
	}

	my, err := p.GetMessage("letter_Y")
	is.NoErr(err)
	if my == nil {
		t.Error("Couldn't retrieve message letter_Y")
		return nil
	}

	vmy := my.GetVariables(OnlyNonOptional)
	if len(vmy) > 0 {
		t.Error("Invalid letter_Y non-optional variables count", len(vmy))
		return nil
	}

	if err := p.AddTask(snd, "Sender"); err != nil {
		t.Error("Couldn't add Send X on Sender : ", err)
		return nil
	}

	if err := p.AddTask(str, "Sender"); err != nil {
		t.Error("Couldn't add Store X on Sender : ", err)
		return nil
	}

	if err := p.LinkNodes(str, snd, nil); err != nil {
		t.Error("Couldn't link Store and Send tasks : ", err)
		return nil
	}

	return p
}

func getRecieveProcess(t *testing.T) *Process {
	p := NewProcess(NewID(), "Test Receive Process", "0.1.0")

	rcv := NewReceiveTask(p, "Receive X", "letter_X", test_queue)
	out := NewOutputTask(p, "Print X", OutputDescr{nil, os.Stdout}, *V("x", VtInt, 0))
	if out == nil || rcv == nil {
		t.Error("Couldn't create receive or output task")
		return nil
	}

	if err := p.NewLane("Receiver"); err != nil {
		t.Error("Couldn't create lane Receiver :", err)
		return nil
	}

	if _, err := p.AddMessage("letter_X",
		Bidirectional, MessageVariable{*V("x", VtInt, 0), false}); err != nil {

		t.Error("Couldn't add message letter_X", err)
		return nil
	}

	if p.AddTask(rcv, "Receiver") != nil && p.AddTask(out, "Receiver") != nil {
		t.Error("Couldn't add tasks to receive process")
		return nil
	}

	if err := p.LinkNodes(rcv, out, nil); err != nil {
		t.Error("Couldn't link receive and output tasks : ", err)
		return nil
	}

	return p
}
