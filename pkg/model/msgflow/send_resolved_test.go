package msgflow_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/stretchr/testify/require"
)

// TestSendResolved — SRD-094 FR-2's engine note: a throw's message carries
// what the execution resolves for its item, and its own item value when
// nothing Ready is there to bind; the nil guards hold; a message without an
// item goes with a nil payload.
func TestSendResolved(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	msg := bpmncommon.MustMessage("order placed",
		data.MustItemDefinition(values.NewVariable("static"),
			foundation.WithID("order_item")))

	publish := func(t *testing.T, re *mockrenv.MockRuntimeEnvironment,
		m *bpmncommon.Message) any {
		t.Helper()

		broker := membroker.New()
		sub, err := broker.Subscribe(ctx, m.Name())
		require.NoError(t, err)

		re.EXPECT().MessageBroker().Return(broker)
		require.NoError(t, msgflow.SendResolved(ctx, re, m, nil))

		select {
		case env := <-sub.C():
			return env.Payload
		default:
			t.Fatal("no envelope delivered")

			return nil
		}
	}

	t.Run("nil guards", func(t *testing.T) {
		require.ErrorContains(t, msgflow.SendResolved(ctx, nil, msg, nil),
			"nil RuntimeEnvironment")
		require.ErrorContains(t, msgflow.SendResolved(ctx,
			mockrenv.NewMockRuntimeEnvironment(t), nil, nil), "nil Message")
	})

	t.Run("a Ready datum of the item id is the payload", func(t *testing.T) {
		re := mockrenv.NewMockRuntimeEnvironment(t)
		re.EXPECT().GetDataByID("order_item").
			Return(readyParam("order_item", "ORD-42"), nil)

		require.Equal(t, "ORD-42", publish(t, re, msg))
	})

	t.Run("nothing to bind: the item's own value", func(t *testing.T) {
		re := mockrenv.NewMockRuntimeEnvironment(t)
		re.EXPECT().GetDataByID("order_item").
			Return(nil, fmt.Errorf("not in scope"))

		require.Equal(t, "static", publish(t, re, msg))
	})

	t.Run("a datum that is not Ready is not bound", func(t *testing.T) {
		unavailable := data.MustParameter("order_item",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("later"),
					foundation.WithID("order_item")),
				data.UnavailableDataState))

		re := mockrenv.NewMockRuntimeEnvironment(t)
		re.EXPECT().GetDataByID("order_item").Return(unavailable, nil)

		require.Equal(t, "static", publish(t, re, msg))
	})
}
