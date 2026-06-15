package events

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// TestCatchEventProcessEventCaptures verifies the catch-side capture (SRD-014):
// ProcessEvent stores a fired message definition's payload item (bound into
// scope on resume by an IntermediateCatchEvent), and captures nothing for a
// payload-less trigger. It is an internal test because `received` is unexported.
func TestCatchEventProcessEventCaptures(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("captures a fired message payload",
		func(t *testing.T) {
			se, err := NewStartEvent("start")
			require.NoError(t, err)

			fired := MustMessageEventDefinition(
				bpmncommon.MustMessage("order placed",
					data.MustItemDefinition(values.NewVariable("ORD-9"),
						foundation.WithID("order_item"))),
				nil)

			require.NoError(t, se.ProcessEvent(ctx, fired))
			require.NotNil(t, se.received)
			require.Equal(t, "order_item", se.received.ID())
			require.Equal(t, "ORD-9", se.received.Structure().Get(ctx))
		})

	t.Run("captures nothing for a payload-less trigger",
		func(t *testing.T) {
			se, err := NewStartEvent("start")
			require.NoError(t, err)

			require.NoError(t, se.ProcessEvent(ctx,
				MustSignalEventDefinition(&Signal{})))
			require.Nil(t, se.received)
		})
}
