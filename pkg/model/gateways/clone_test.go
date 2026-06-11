package gateways_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/stretchr/testify/require"
)

// TestExclusiveGatewayClone verifies that ExclusiveGateway.Clone shares
// configuration (direction, default flow) by reference, starts with a fresh
// scope, empty flows and no container.
func TestExclusiveGatewayClone(t *testing.T) {
	eg, err := gateways.NewExclusiveGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

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

	// fresh scope: Find before any Exec reports the unset scope on the clone.
	_, err = clone.Find(context.Background(), "anything")
	require.Error(t, err)
}
