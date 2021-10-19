package model

import (
	"testing"

	"github.com/google/uuid"
)

func TestProcessModelError(t *testing.T) {
	err := NewProcessModelError(Id(uuid.Nil), "test", nil)
	_, ok := err.(ProcessModelError)
	if !ok {
		t.Errorf("NewProcessModelError doesnt't create and ProcessModelError. Got %T", err)
	}
}
func TestProcessLanes(t *testing.T) {
	p := Process{FlowElementsContainer: FlowElementsContainer{
		FlowElement: FlowElement{
			NamedElement: NamedElement{
				BaseElement: BaseElement{id: Id(uuid.New())},
				name:        "testProcess"}}}}

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
	res := p.ListLanes()
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
	res = p.ListLanes()
	if len(res) != 1 || res[0] != ln[1] {
		t.Error("couldn't remove lane. Got lane's names ", res)
	}
}
