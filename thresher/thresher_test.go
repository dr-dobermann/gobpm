package thresher

import (
	"context"
	"os"
	"testing"

	"github.com/dr-dobermann/gobpm/model"
	"github.com/google/uuid"
)

func TestTracks(t *testing.T) {
	pi := getTestInstance(nil, t)

	if err := pi.prepare(); err != nil {
		t.Fatal("Couldn't prepare instance for running : ", err)
	}

	if err := pi.prepare(); err == nil {
		t.Fatal("Double preparation of instance")
	}

	if len(pi.tracks) == 0 {
		t.Fatal("No tracks created")
	}

	if len(pi.tracks) != 1 {
		t.Fatal("Invalid tracks count. have ", len(pi.tracks))
	}

	n := pi.tracks[0].currentStep().node
	if n.Name() != "Store Task" {
		t.Fatal("Invalid track Node name ", n.Name())
	}

	if err := pi.tracks[0].tick(context.Background()); err != nil {
		t.Errorf("Couldn't exec Node %s : %v", pi.tracks[0].steps[0].node.Name(), err)
	}

	if len(pi.tracks[0].steps) != 2 {
		t.Error("Invalid steps count")
	}

	if pi.tracks[0].currentStep().node.Name() != "Output Task" {
		t.Error("Invalid second step name")
	}

	if len(pi.tracks) > 1 {
		t.Error("Invalid tracks count")
	}

	if err := pi.tracks[0].tick(context.Background()); err != nil {
		t.Errorf("Couldn't exec Node %s : %v", pi.tracks[0].currentStep().node.Name(), err)
	}

	if pi.tracks[0].state != TsEnded {
		t.Error("Invalid track state")
	}
}

func TestThresher(t *testing.T) {
	thr := GetThreshser()

	if _, err := thr.NewProcessInstance(getTestProcess(t)); err != nil {
		t.Error("couldn't create an Instance of the test process : ", err)
	}

	thr.TurnOn()
}

func getTestInstance(p *model.Process, t *testing.T) *ProcessInstance {
	if p == nil {
		p = getTestProcess(t)
	}

	pi, err := GetThreshser().NewProcessInstance(p)
	if err != nil {
		t.Fatal("couldn't create an instance : ", err)
	}

	return pi
}

func getTestProcess(t *testing.T) *model.Process {
	p := model.NewProcess(model.Id(uuid.Nil), "Test Process", "0.1.0")

	t1 := model.NewStoreTask(p, "Store Task", *model.V("x", model.VtInt, 2))
	t2 := model.NewOutputTask(p, "Output Task", os.Stdout, *model.V("x", model.VtInt, 0))
	if t1 == nil || t2 == nil {
		t.Fatal("Couldn't create tasks for test process")
	}

	if err := p.NewLane("Lane 1"); err != nil {
		t.Fatal("Couldn't add Lane 1 to tosting process : ", err)
	}

	if err := p.AddTask(t1, "Lane 1"); err != nil {
		t.Fatal("Couldn't add Task 1 on Lane 1 : ", err)
	}
	if err := p.AddTask(t2, "Lane 1"); err != nil {
		t.Fatal("Couldn't add Task 2 on Lane 1 : ", err)
	}

	if err := p.LinkNodes(t1, t2, nil); err != nil {
		t.Fatal("couldn't link tasks in test process : ", err)
	}

	return p
}
