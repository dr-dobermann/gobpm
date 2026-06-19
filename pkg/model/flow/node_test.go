package flow_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/stretchr/testify/require"
)

// TestBaseNodeFlowOrder guards FIX-005: Incoming()/Outgoing() return flows in
// declaration order (not the randomized map order they used to), so the gateway
// first-true / subset rules are deterministic (BPMN §13.4.2).
func TestBaseNodeFlowOrder(t *testing.T) {
	gate := func() *gateways.ParallelGateway {
		g, err := gateways.NewParallelGateway()
		require.NoError(t, err)

		return g
	}

	end := func(name string) *events.EndEvent {
		e, err := events.NewEndEvent(name)
		require.NoError(t, err)

		return e
	}

	t.Run("outgoing in declaration order, stable across calls",
		func(t *testing.T) {
			src := gate()

			f1, err := flow.Link(src, end("e1"))
			require.NoError(t, err)
			f2, err := flow.Link(src, end("e2"))
			require.NoError(t, err)
			f3, err := flow.Link(src, end("e3"))
			require.NoError(t, err)

			require.Equal(t, []*flow.SequenceFlow{f1, f2, f3}, src.Outgoing())
			require.Equal(t, src.Outgoing(), src.Outgoing())
		})

	t.Run("incoming in declaration order",
		func(t *testing.T) {
			trg := gate()

			f1, err := flow.Link(gate(), trg)
			require.NoError(t, err)
			f2, err := flow.Link(gate(), trg)
			require.NoError(t, err)
			f3, err := flow.Link(gate(), trg)
			require.NoError(t, err)

			require.Equal(t, []*flow.SequenceFlow{f1, f2, f3}, trg.Incoming())
		})

	t.Run("re-adding a flow keeps a single ordered entry",
		func(t *testing.T) {
			src := gate()

			f1, err := flow.Link(src, end("e1"))
			require.NoError(t, err)

			// re-adding the same flow id must not duplicate it in the order.
			require.NoError(t, src.AddFlow(f1, data.Output))
			require.Equal(t, []*flow.SequenceFlow{f1}, src.Outgoing())
		})

	t.Run("rejects a nil flow and an invalid direction",
		func(t *testing.T) {
			src := gate()

			require.Error(t, src.AddFlow(nil, data.Output))

			f1, err := flow.Link(src, end("e1"))
			require.NoError(t, err)
			require.Error(t, src.AddFlow(f1, data.Direction("bogus")))
		})
}
