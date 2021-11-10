package thresher

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/model"
	"github.com/google/uuid"
)

func TestThresherCommons(t *testing.T) {
	states := []string{"Created", "Started", "AwaitsService",
		"AwaitsMessage", "Merged", "Ended", "Error"}

	for i := 0; i < 7; i++ {
		if TrackState(i).String() != states[i] {
			t.Errorf("invalid track state name for %d", i)
		}
	}

}

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

	if pi.tracks[0].node.Name() != "Store Task" {
		t.Fatal("Invalid track Node name ", pi.tracks[0].node.Name())
	}

	if err := pi.tracks[0].tick(context.Background()); err != nil {
		t.Errorf("Couldn't exec Node %s : %v", pi.tracks[0].node.Name(), err)
	}
}

func getTestInstance(p *model.Process, t *testing.T) *ProcessInstance {
	if p == nil {
		p = getTestProcess(t)
	}

	pi, err := GetThreshser().NewProcessInstance(p)
	if err != nil {
		t.Fatal("couldn't create an instance : ", err)
	}

	if pi == nil {
		t.Fatal("got nil instance")
	}

	return pi
}

func getTestProcess(t *testing.T) *model.Process {
	p := model.NewProcess(model.Id(uuid.Nil), "Test Process", "0.1.0")

	t1 := model.NewStoreTask(p, "Store Task", *model.V("x", model.VtInt, 2))
	// t1 := NewStoreTaskExecutor(model.NewStoreTask(p, "Store Task", *model.V("x", model.VtInt, 2)))
	t2 := model.NewOutputTask(p, "Output Task", *model.V("x", model.VtInt, 0))
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
