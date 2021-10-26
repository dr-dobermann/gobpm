package model

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestProcessCreation(t *testing.T) {
	id := uuid.New()
	nm := "Testing process"
	ver := "0.1.0"
	p := NewProcess(Id(id), nm, ver)

	if p.ID() != Id(id) {
		t.Errorf("Invalid process id: got %v, expected %v", p.ID(), id)
	}

	if p.Name() != nm {
		t.Errorf("Invalid process name: got %v, expected %v", p.Name(), nm)
	}

	if p.Version() != ver {
		t.Errorf("Invalid process version: got %v, expected %v", p.Version(), ver)
	}

	p = NewProcess(Id(uuid.Nil), "", "")

	if p.ID() == Id(uuid.Nil) {
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
	err := NewProcessModelError(Id(uuid.Nil), "test", nil)
	_, ok := err.(ProcessModelError)
	if !ok {
		t.Errorf("NewProcessModelError doesnt't create and ProcessModelError. Got %T", err)
	}

	if err.Error() != "P[ <nil> ] test" {
		t.Error("Invalid ProcessModelError return ", err.Error())
	}

	id := Id(uuid.New())
	err = NewProcessModelError(id, "test", fmt.Errorf("test"))

	want := "P[" + id.String() + "] test : test"
	if err.Error() != want {
		t.Error("Invalid ProcessModelError return ", err.Error())
	}

}

func TestProcessLanes(t *testing.T) {
	p := NewProcess(Id(uuid.New()), "Testing process", "0.1.0")

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
			if l == r.name {
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
	if len(res) != 1 || res[0].name != ln[1] {
		t.Error("couldn't remove lane. Got lane's names ", res)
	}
}

func TestNodesList(t *testing.T) {
	// p := NewProcess(Id(uuid.Nil), "test", "0.1.0")

	// n := NewOutputTask("")

	// err := p.AddNode(n Node)
	// if err != nil {
	// 	t.Error("Couldn't add node ")
	// }
}
