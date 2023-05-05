package common

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/matryer/is"
)

func TestLane(t *testing.T) {

	is := is.New(t)

	l := NewLane("test-lane")
	is.True(l != nil)

	n := FlowNode{
		FlowElement: *NewElement(identity.NewID(), "test_node", EtActivity),
		incoming:    []*SequenceFlow{},
		outcoming:   []*SequenceFlow{},
	}

	l.AddNode(&n)
	nn, err := l.GetNode(n.ID())
	is.NoErr(err)
	is.Equal(nn.ID(), n.ID())

	_, err = l.GetNode(identity.NewID())
	is.True(err != nil)

	is.Equal(len(l.GetAllNodes()), 1)

	err = l.RemoveNode(n.ID())
	is.NoErr(err)

	err = l.RemoveNode(n.ID())
	is.True(err != nil)

	is.Equal(len(l.GetAllNodes()), 0)
}

func TestLaneSet(t *testing.T) {
	is := is.New(t)

	ls := NewLaneSet("testLS")
	is.True(ls != nil)

	lNames := []string{"Lane1", "Lane2", "Lane3"}
	lanes := []*Lane{}
	for _, ln := range lNames {
		l := NewLane(ln)
		is.True(l != nil)
		lanes = append(lanes, l)
	}

	ls.AddLanes(lanes[:2]...)
	is.Equal(len(ls.lanes), 2)

	is.NoErr(ls.RemoveLane(lanes[0].ID()))
	is.True(ls.RemoveLane(lanes[0].ID()) != nil)
	_, err := ls.GetLane(lanes[1].ID())
	is.NoErr(err)
	_, err = ls.GetLane(lanes[0].ID())
	is.True(err != nil)

	is.NoErr(lanes[2].AddLaneSet(ls))
	is.True(lanes[2].AddLaneSet(ls) != nil)
}
