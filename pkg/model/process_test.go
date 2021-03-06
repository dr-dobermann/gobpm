package model

import (
	"fmt"
	"os"
	"testing"

	mid "github.com/dr-dobermann/gobpm/pkg/identity"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
	"github.com/matryer/is"
)

func TestProcessCreation(t *testing.T) {
	id := mid.NewID()
	nm := "Testing process"
	ver := "0.1.0"
	p := NewProcess(id, nm, ver)

	if p.ID() != id {
		t.Errorf("Invalid process id: got %v, expected %v", p.ID(), id)
	}

	if p.Name() != nm {
		t.Errorf("Invalid process name: got %v, expected %v", p.Name(), nm)
	}

	if p.Version() != ver {
		t.Errorf("Invalid process version: got %v, expected %v", p.Version(), ver)
	}

	p = NewProcess(mid.EmptyID(), "", "")

	if p.ID() == mid.EmptyID() {
		t.Error("Invalid process id autogeneration")
	}

	if p.Name() != "Process #"+p.ID().String() {
		t.Error("Invalid process name autogeneration ", p.Name())
	}

	if p.Version() != ver {
		t.Error("Invalid process version autogeneration ", p.Version())
	}
}

func TestProcessModelError(t *testing.T) {
	err := NewPMErr(mid.EmptyID(), nil, "test %s", "error")
	_, ok := err.(ProcessModelError)
	if !ok {
		t.Errorf("NewPMErr doesnt't create and ProcessModelError. Got %T", err)
	}

	id := mid.NewID()
	err = NewPMErr(id, fmt.Errorf("test"), "test")

	want := "ERR: PRC[" + id.String() + "] test: test"
	if err.Error() != want {
		t.Error("Invalid ProcessModelError got [",
			err.Error(), "] want [", want, "]")
	}

}

func TestProcessLanes(t *testing.T) {
	p := NewProcess(mid.NewID(), "Testing process", "0.1.0")

	var err error

	// adding lanes
	ln := []string{"first", "second"}
	for _, l := range ln {
		err = p.NewLane(l)
		if err != nil {
			t.Error("Couldn't add lane ", l, " : ", err)
		}
	}

	err = p.NewLane(ln[0])
	if err == nil {
		t.Error("Lane ", ln[0], " added twice")
	}
	if _, ok := err.(ProcessModelError); !ok {
		t.Errorf("Wrong error type returned: %T, %v", err, err)
	}

	// listing lanes
	res := p.Lanes()
	if len(res) != 2 {
		t.Error("Invalid lanes count. Expected 2, got ", len(res))
	}

	n := 2
	for _, l := range ln {
		for _, r := range res {
			if l == r {
				n--
				break
			}
		}
	}
	if n != 0 {
		t.Error("Some lane isn't found. Expected ", ln, " got ", res)
	}

	// removing lanes
	err = p.RemoveLane("third")
	if err == nil {
		t.Error("could remove unexistance lane")
	}

	err = p.RemoveLane(ln[0])
	if err != nil {
		t.Error("couldn't remove lane ", ln[0], " : ", err)
	}
	res = p.Lanes()
	if len(res) != 1 || res[0] != ln[1] {
		t.Error("couldn't remove lane. Got lane's names ", res)
	}

	// generate new name
	err = p.NewLane("")
	if err != nil {
		t.Error("couldn't add lane with an empty name", err)
	}

}

func TestNodes(t *testing.T) {
	is := is.New(t)

	p := NewProcess(mid.EmptyID(), "test", "0.1.0")

	tn1 := "Task1"
	t1 := NewStoreTask(p, tn1, *vars.V("x", vars.Int, nil))

	ln := "Lane 1"
	err := p.NewLane(ln)
	if err != nil {
		t.Error("couldn't add lane "+ln+" ; ", err)
	}

	err = p.AddTask(t1, ln)
	if err != nil {
		t.Error("couldn't add task1", err)
	}

	if len(p.tasks) != 1 {
		t.Error("task wasn't added to process")
	}

	if len(p.lanes[ln].nodes) == 0 ||
		p.lanes[ln].nodes[0].ID() != t1.ID() {
		t.Error("task washn't added to lane " + ln)
	}

	if err = p.AddTask(nil, ""); err == nil {
		t.Error("Nil task added")
	}

	if t1.lane.name != p.lanes[ln].name {
		t.Error("Task ", t1.name, " washn't linked to lane ", ln)
	}

	// trying to add a duplicate
	if err = p.AddTask(t1, ln); err == nil {
		t.Error("duplicate task added")
	}

	// adding task on wrong lane
	if err = p.AddTask(t1, "wrong_lane"); err == nil {
		t.Error("using wrong lane")
	}

	// trying remove non-empty lane
	if err = p.RemoveLane(ln); err == nil {
		t.Error("non-empty lane removed")
	}

	tn2 := "Task2"
	t2 := NewOutputTask(p, tn2, OutputDescr{nil, os.Stdout}, *vars.V("x", vars.Int, nil))

	err = p.AddTask(t2, ln)
	if err != nil {
		t.Error("couldn't add t2")
	}

	// nil-task linking
	if err = p.LinkNodes(nil, t2, nil); err == nil {
		t.Error("nil-task linked one to another")
	}

	err = p.LinkNodes(t1, t2, nil)
	if err != nil {
		t.Error("couldn't link t1 and t2", err)
	}

	if len(p.flows) == 0 {
		t.Fatal("there is no link registered in process")
	}

	is.True(len(t1.outcoming) > 0 && len(t2.incoming) > 0)

	if t1.outcoming[0].ID() != p.flows[0].ID() ||
		t2.incoming[0].ID() != p.flows[0].ID() {
		t.Error("invalid flow between t1 and t2")
	}
}

func TestProcessSnapshot(t *testing.T) {
	is := is.New(t)

	p := createTestProcess(t)

	sn, err := p.Copy()
	is.NoErr(err)

	if len(p.lanes) != len(sn.lanes) {
		t.Error("different lanes number in snapshot")
	}

	// check lanes and tasks
	for ln, ls := range p.lanes {
		lt, ok := sn.lanes[ln]
		if !ok {
			t.Fatal("Lane " + ln + " isn't found in snapshot")
		}
		for i, n := range ls.nodes {
			if n.Name() != lt.nodes[i].Name() {
				t.Errorf("Node %d (%s) has different name (%s) in snapshot",
					i, n.Name(), lt.nodes[i].Name())
			}

			if n.Type() != lt.nodes[i].Type() {
				t.Errorf("Node %d (%s) has different type (%d instead of %d) in snapshot",
					i, n.Name(), n.Type(),
					lt.nodes[i].Type())

			}

			// sadly it's impossible to test equelity of incoming and
			// outcoming node's flows without adding only-for-test methods.
			// To prevent errors of duplication flows
			// 'THE COPIED NODE SHOULD HAVE _EMPTY_ INCOMING AND OUTCOMING FLOWS

		}
	}

	// check flows
	if len(p.flows) != len(sn.flows) {
		t.Fatalf("number of flows are different %d instead %d",
			len(sn.flows), len(p.flows))
	}
	for i, osf := range p.flows {
		if osf.sourceRef.Name() != sn.flows[i].sourceRef.Name() {
			t.Errorf("source differs on flow %d: exp. %s, got %s",
				i, osf.sourceRef.Name(), sn.flows[i].sourceRef.Name())
		}
		if osf.targetRef.Name() != sn.flows[i].targetRef.Name() {
			t.Errorf("target differs on flow %d: exp. %s, got %s",
				i, osf.targetRef.Name(), sn.flows[i].targetRef.Name())
		}
	}
}

func createTestProcess(t *testing.T) *Process {
	p := NewProcess(mid.EmptyID(), "test", "0.1.0")

	t1 := NewStoreTask(p, "Task 1", *vars.V("x", vars.Int, 2))
	t2 := NewOutputTask(p, "Task 2", OutputDescr{nil, os.Stdout}, *vars.V("x", vars.Int, 0))
	if t1 == nil || t2 == nil {
		t.Fatal("couldn't create tasks for test process")
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
