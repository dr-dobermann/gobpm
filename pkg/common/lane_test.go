package common

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/matryer/is"
)

func TestLane(t *testing.T) {

	is := is.New(t)

	l := NewLane(identity.EmptyID(), "test-lane")
	is.True(l != nil)
	is.Equal(l.SourceID(), identity.EmptyID())

	is.Equal(l.ID(), l.copy().SourceID())

	n := FlowNode{
		FlowElement: *NewElement(identity.EmptyID(), "test_node", EtActivity),
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
	lanes := map[identity.Id]*Lane{}
	for _, ln := range lNames {
		l := NewLane(identity.EmptyID(), ln)
		is.True(l != nil)
		lanes[l.ID()] = l
	}

	lids := []identity.Id{}
	for k, l := range lanes {
		lids = append(lids, k)
		if len(lids) < 3 {
			ls.AddLanes(l)
		}
	}

	is.Equal(len(ls.lanes), 2)
	for _, l := range ls.GetAllLanes(false) {
		is.Equal(l.ID(), lanes[l.ID()].ID())
	}

	is.NoErr(ls.RemoveLane(lids[0]))
	is.True(ls.RemoveLane(lids[0]) != nil)
	_, err := ls.GetLane(lids[1])
	is.NoErr(err)
	_, err = ls.GetLane(lids[0])
	is.True(err != nil)

	is.NoErr(lanes[lids[2]].AddLaneSet(ls))
	is.True(lanes[lids[2]].AddLaneSet(ls) != nil)

	is.True(lanes[lids[1]].AddLaneSet(ls) != nil)

	lsc := ls.Copy()
	is.Equal(len(ls.lanes), len(lsc.lanes))

}
