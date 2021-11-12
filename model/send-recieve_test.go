package model

import "testing"

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

	p := NewProcess(NewID(), "Test Send Process", "0.1.0")

	str := NewStoreTask(p, "Store X", *V("x", VtInt, 10))
	snd := NewSendTask(p, "Send X", "letter_X")
	if str == nil || snd == nil {
		t.Error("Couldn't create store|send tasks")
		return nil
	}

	if err := p.NewLane("Sender"); err != nil {
		t.Error("Couldn't add a Lane Sender", err)
		return nil
	}

	if _, err := p.AddMessage("letter_X",
		MfdOutgoing, MessageVariable{*V("x", VtInt, 0), false}); err != nil {
		t.Error("Couldn't add outgoing message letter_X : ", err)
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

	rcv := NewReceiveTask(p, "Receive X", "letter_X")
	out := NewOutputTask(p, "Print X", *V("x", VtInt, 0))
	if out == nil || rcv == nil {
		t.Error("Couldn't create receive or output task")
		return nil
	}

	if err := p.NewLane("Receiver"); err != nil {
		t.Error("Couldn't create lane Receiver :", err)
		return nil
	}

	if _, err := p.AddMessage("letter_X",
		MfdBidirectional, MessageVariable{*V("x", VtInt, 0), false}); err != nil {

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
