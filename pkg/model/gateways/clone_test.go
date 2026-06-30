package gateways_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/stretchr/testify/require"
)

// TestUpdateDefaultFlowStoresMember covers FIX-014 1.5: UpdateDefaultFlow stores
// the gateway's own outgoing-flow object, not the caller's pointer, so the
// pointer-identity routing (`of == g.defaultFlow`) recognises the default even
// when the caller passes a different object carrying the same id.
func TestUpdateDefaultFlowStoresMember(t *testing.T) {
	g, err := gateways.New(gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	nodes := getDummyNodes(4)
	_, err = flow.Link(nodes[0], g)
	require.NoError(t, err)
	_, err = flow.Link(g, nodes[1])
	require.NoError(t, err)
	df, err := flow.Link(g, nodes[2]) // df is one of g's outgoing members
	require.NoError(t, err)

	// a distinct flow object carrying the SAME id as the member df, not in g.
	foreign, err := flow.Link(nodes[2], nodes[3], foundation.WithID(df.ID()))
	require.NoError(t, err)
	require.NotSame(t, df, foreign)
	require.Equal(t, df.ID(), foreign.ID())

	require.NoError(t, g.UpdateDefaultFlow(foreign))

	require.Same(t, df, g.DefaultFlow(),
		"the stored default must be the gateway's member, not the caller's pointer")
	require.NotSame(t, foreign, g.DefaultFlow())

	// a conditioned outgoing flow may not be the default.
	cond, err := flow.Link(g, nodes[3],
		flow.WithCondition(boolCond(t, func(x int) bool { return x == 1 })))
	require.NoError(t, err)
	require.Error(t, g.UpdateDefaultFlow(cond))
}

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
