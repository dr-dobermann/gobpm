package process_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/internal/request"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/matryer/is"
)

func TestProcessCreation(t *testing.T) {
	is := is.New(t)

	id := identity.NewID()
	nm := "Testing process"

	p := process.New(id, nm)
	is.True(p != nil)
	is.Equal(p.ID(), id)
	is.Equal(p.Name(), nm)

	p = process.New(identity.EmptyID(), "")
	is.True(p.ID() != identity.EmptyID())
	is.Equal(p.Name(), "Process #"+p.ID().String())
}

func TestAddNodes(t *testing.T) {
	is := is.New(t)

	p := process.New(identity.EmptyID(), "test-process")
	is.True(p != nil)

	na := common.FlowNode{
		FlowElement: *common.NewElement(identity.EmptyID(), "test-node-activity", common.EtActivity),
	}
	is.True(p.AddNode(&na, "__default") != nil)
	err := p.AddNode(&na, "")
	is.NoErr(err)

	ne := common.FlowNode{
		FlowElement: *common.NewElement(identity.EmptyID(), "test-node-event", common.EtEvent),
	}

	_, err = p.GetNodes(common.EtDataAssociation)
	is.True(err != nil)

	is.True(p.AddNode(&common.FlowNode{
		FlowElement: *common.NewElement(identity.EmptyID(), "wrong-element", common.EtLane),
	}, "") != nil)

	nn, err := p.GetNodes(common.EtActivity)
	is.NoErr(err)
	is.True(nn != nil)
	is.Equal(len(nn), 1)
	is.Equal(nn[0].ID(), na.ID())

	p.AddNode(&ne, "")
	nn1, err := p.GetNodes(common.EtUnspecified)
	is.NoErr(err)
	is.True(nn1 != nil)
	is.Equal(len(nn1), 2)

	// check for ne presence
	nodeFound := false
	for _, fn := range nn1 {
		if fn.ID() == ne.ID() {
			nodeFound = true
			break
		}
	}
	is.True(nodeFound)

}

func TestConnectNodes(t *testing.T) {

	is := is.New(t)

	nnames := []string{"node1", "node2"}
	fnn := []common.Node{}
	p := process.New(identity.EmptyID(), "test-process")

	for _, name := range nnames {
		n := &common.FlowNode{
			FlowElement: *common.NewElement(identity.EmptyID(), name,
				common.EtActivity),
		}
		fnn = append(fnn, n)
		is.NoErr(p.AddNode(n, "My Lane"))
	}

	is.NoErr(p.ConnectNodes(fnn[0], fnn[1], "test-flow", nil))
	is.True(p.ConnectNodes(fnn[0], fnn[1], "test-flow1", nil) != nil)
	is.NoErr(p.ConnectNodes(fnn[1], fnn[0], "reverse-test-flow", nil))

}

func TestMessages(t *testing.T) {

	is := is.New(t)

	p := process.New(identity.EmptyID(), "test-process")

	r := request.New(10, "tenth request")
	m, err := common.NewMessage("test-msg", r)
	is.NoErr(err)

	is.NoErr(p.RegisterMessage(m))
	is.True(p.RegisterMessage(m) != nil)

	ms, err := p.GetMessage("test-msg")
	is.NoErr(err)

	_, err = p.GetMessage("invalid-msg-name")
	is.True(err != nil)

	is.Equal(m.Name(), ms.Name())

	checkReq := request.New(0, "trash")
	checkReq.UpdateValue(m.GetItem().GetValue())
	is.Equal(r.ID(), checkReq.ID())
	is.Equal(r.Descr(), checkReq.Descr())

	is.NoErr(p.RemoveMessage("test-msg"))
	is.True(p.RemoveMessage("test-msg") != nil)
}
