package gateways_test

import (
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

// ============================================================================

type dummyNode struct {
	foundation.BaseElement
	flow.FlowNode
	flow.FlowElement
}

func newDummyNode(name string) *dummyNode {
	return &dummyNode{
		BaseElement: *foundation.MustBaseElement(),
		FlowNode:    *flow.NewFlowNode(),
		FlowElement: *flow.NewFlowElement(name),
	}
}

func (n *dummyNode) Node() flow.Node {
	return n
}

func (n *dummyNode) NodeType() flow.NodeType {
	return flow.NodeType("DummyNode")
}

func (n *dummyNode) SupportOutgoingFlow(_ *flow.SequenceFlow) error {
	return nil
}

func (n *dummyNode) AcceptIncomingFlow(_ *flow.SequenceFlow) error {
	return nil
}

func getDummyNodes(n int) []*dummyNode {
	nodes := []*dummyNode{}
	for i := 0; i < n; i++ {
		nodes = append(nodes, newDummyNode(fmt.Sprintf("dummy_node #%d", i)))
	}

	return nodes
}

// interfaces check
var (
	_ flow.SequenceSource = (*dummyNode)(nil)
	_ flow.SequenceTarget = (*dummyNode)(nil)
)

// ============================================================================

func TestDirection(t *testing.T) {
	// invalid cases
	for _, ec := range []string{
		"",
		"invalid_direction",
	} {
		require.Error(t, gateways.GDirection(ec).Validate())
	}

	dir := gateways.Unspecified

	require.NoError(t, dir.Validate())
}

func TestNewGateway(t *testing.T) {
	// invalid options
	_, err := gateways.New(activities.WithCompensation())
	require.Error(t, err)

	// valid options
	g, err := gateways.New(foundation.WithId("gate #1"), options.WithName("my gate"))
	require.NoError(t, err)
	require.Equal(t, "gate #1", g.Id())
	require.Equal(t, "my gate", g.Name())
	require.Equal(t, gateways.Unspecified, g.Direction())

	require.Equal(t, g, g.Node())
	require.Equal(t, flow.GatewayNodeType, g.NodeType())

	// with new direction
	g, err = gateways.New(gateways.WithDirection(gateways.Mixed))
	require.NoError(t, err)
	require.Equal(t, gateways.Mixed, g.Direction())
}

func TestGatewayFlows(t *testing.T) {
	t.Run(
		"diverging gateway",
		func(t *testing.T) {
			nodes := getDummyNodes(3)
			require.Len(t, nodes, 3)

			g, err := gateways.New(gateways.WithDirection(gateways.Diverging))
			require.NoError(t, err)

			// incoming flows
			// first node should link without problem
			_, err = flow.Link(nodes[0], g)
			require.NoError(t, err)

			// second node shouldn't be linked
			_, err = flow.Link(nodes[1], g)
			require.Error(t, err)

			// outgoing flows
			// empty outgoing flows should fail
			require.Error(t, g.TestFlows())

			// single outgoing flow is ok
			_, err = flow.Link(g, nodes[1])
			require.NoError(t, err)

			// multiple outgoing flow is ok
			_, err = flow.Link(g, nodes[2])
			require.NoError(t, err)

			require.NoError(t, g.TestFlows())
		})
}
