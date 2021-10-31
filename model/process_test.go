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
}

func TestNodes(t *testing.T) {
	p := NewProcess(Id(uuid.Nil), "test", "0.1.0")

	tn1 := "Task1"
	t1 := &StoreTask{
		Activity: Activity{
			FlowNode: FlowNode{
				FlowElement: FlowElement{
					NamedElement: NamedElement{
						BaseElement: BaseElement{
							id: NewID()},
						name: tn1},
					elementType: EtActivity}},
			class:  AcAbstract,
			aType:  AtStoreTask,
			output: []VarDefinition{{"x", VtInt, nil}}},
		vars: []VarDefinition{{"x", VtInt, 2}}}

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
		p.lanes[ln].nodes[0].FloatNode().id != t1.id {
		t.Error("task washn't added to lane " + ln)
	}

	if err = p.AddTask(nil, ""); err == nil {
		t.Error("Nil task added")
	}

	if t1.laneName != p.lanes[ln].name {
		t.Error("Task ", t1.name, " washn't linked to lane ", ln)
	}

	// trying to add a duplicate
	if err = p.AddTask(t1, ln); err == nil {
		t.Error("duplicate task added")
	}

	tn2 := "Task2"
	t2 := &OutputTask{
		Activity: Activity{
			FlowNode: FlowNode{
				FlowElement: FlowElement{
					NamedElement: NamedElement{
						BaseElement: BaseElement{
							id: NewID()},
						name: tn2},
					elementType: EtActivity}},
			aType: AtOutputTask,
			class: AcAbstract,
			input: []VarDefinition{{"x", VtInt, nil}},
		},
		vars: []VarDefinition{{"x", VtInt, nil}}}

	err = p.AddTask(t2, ln)
	if err != nil {
		t.Error("couldn't add t2")
	}

	err = p.LinkNodes(t1, t2, nil)
	if err != nil {
		t.Error("couldn;t link t1 and t2", err)
	}

	if len(p.flows) == 0 {
		t.Fatal("there is no link registered in process")
	}

	if t1.outcoming[0].id != p.flows[0].id ||
		t2.incoming[0].id != p.flows[0].id {
		t.Error("invalid flow between t1 and t2")
	}
}
