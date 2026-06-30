package instance

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/stretchr/testify/require"
)

// TestUnregisterEventNonEventNodeError covers FIX-014 1.6: the defensive branch
// for a node that is not a flow.EventNode now interpolates the node name and id
// (no %!q(MISSING)/%!s(MISSING)) and carries the error class. A gateway is a
// flow.Node but not a flow.EventNode, so it drives this branch; the error path
// reads only n.Name()/n.ID(), so a zero-value track is sufficient.
func TestUnregisterEventNonEventNodeError(t *testing.T) {
	g, err := gateways.NewParallelGateway(
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)

	var tr track

	err = tr.unregisterEvent(g)
	require.Error(t, err)

	msg := err.Error()
	require.Contains(t, msg, g.ID())
	require.NotContains(t, msg, "MISSING",
		"format verbs must receive their arguments")
}
