package gateways_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/stretchr/testify/require"
)

// TestMustUpdateDefaultFlow verifies the panicking wrapper around
// UpdateDefaultFlow: it sets the default flow for a valid outgoing edge and
// panics when the flow is not one of the gateway's outgoing flows.
func TestMustUpdateDefaultFlow(t *testing.T) {
	t.Run("success sets default flow",
		func(t *testing.T) {
			g, err := gateways.New(gateways.WithDirection(gateways.Diverging))
			require.NoError(t, err)

			nodes := getDummyNodes(3)
			_, err = flow.Link(nodes[0], g)
			require.NoError(t, err)
			_, err = flow.Link(g, nodes[1])
			require.NoError(t, err)
			df, err := flow.Link(g, nodes[2])
			require.NoError(t, err)

			g.MustUpdateDefaultFlow(df)
			require.Same(t, df, g.DefaultFlow())
		})

	t.Run("foreign flow panics",
		func(t *testing.T) {
			g, err := gateways.New(gateways.WithDirection(gateways.Diverging))
			require.NoError(t, err)

			nodes := getDummyNodes(4)
			_, err = flow.Link(nodes[0], g)
			require.NoError(t, err)
			_, err = flow.Link(g, nodes[1])
			require.NoError(t, err)
			_, err = flow.Link(g, nodes[2])
			require.NoError(t, err)

			// a flow between two other nodes — not one of g's outgoing flows.
			foreign, err := flow.Link(nodes[2], nodes[3])
			require.NoError(t, err)

			require.Panics(t, func() {
				g.MustUpdateDefaultFlow(foreign)
			})
		})
}

// TestExclusiveGatewayClone verifies that ExclusiveGateway.Clone shares
// configuration (direction, default flow) by reference, starts with a fresh
// scope, empty flows and no container.
func TestExclusiveGatewayClone(t *testing.T) {
	eg, err := gateways.NewExclusiveGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	// Node() returns the concrete gateway (so flow-dispatch finds the executor).
	concrete, ok := eg.Node().(*gateways.ExclusiveGateway)
	require.True(t, ok)
	require.Same(t, eg, concrete)

	// wire an incoming flow and two outgoing flows; mark one as default.
	nodes := getDummyNodes(3)
	_, err = flow.Link(nodes[0], eg)
	require.NoError(t, err)
	_, err = flow.Link(eg, nodes[1])
	require.NoError(t, err)
	df, err := flow.Link(eg, nodes[2])
	require.NoError(t, err)
	require.NoError(t, eg.UpdateDefaultFlow(df))

	require.NotEmpty(t, eg.Outgoing())
	require.NotEmpty(t, eg.Incoming())

	clone, ok := eg.Clone().(*gateways.ExclusiveGateway)
	require.True(t, ok)

	// independent object, same id.
	require.NotSame(t, eg, clone)
	require.Equal(t, eg.ID(), clone.ID())

	// configuration shared by reference.
	require.Equal(t, eg.Direction(), clone.Direction())
	require.Same(t, eg.DefaultFlow(), clone.DefaultFlow())

	// flows empty, no container.
	require.Empty(t, clone.Outgoing())
	require.Empty(t, clone.Incoming())
	require.Nil(t, clone.Container())
}
