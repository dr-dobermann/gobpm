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
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeProducer is a minimal msgflow.MessageProducer.
type fakeProducer struct{ msg *bpmncommon.Message }

func (f fakeProducer) MessageToSend() *bpmncommon.Message { return f.msg }

func seamMessage(t *testing.T) *bpmncommon.Message {
	t.Helper()

	return bpmncommon.MustMessage("order placed",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("order_item")))
}

func TestPublish(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("drives the producer's message to the broker",
		func(t *testing.T) {
			broker := membroker.New()
			ch, err := broker.Subscribe(ctx, "order placed", "")
			require.NoError(t, err)

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				GetDataByID("order_item").
				Return(readyParam("order_item", "ORD-1"), nil)
			re.EXPECT().MessageBroker().Return(broker)

			require.NoError(t, msgflow.Publish(ctx, re,
				fakeProducer{msg: seamMessage(t)}))

			select {
			case env := <-ch:
				require.Equal(t, "ORD-1", env.Payload)
			default:
				t.Fatal("no envelope delivered")
			}
		})

	t.Run("nil producer is rejected",
		func(t *testing.T) {
			err := msgflow.Publish(ctx,
				mockrenv.NewMockRuntimeEnvironment(t), nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "MessageProducer")
		})
}

func TestCaptureItem(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("returns the payload item of a message definition",
		func(t *testing.T) {
			med := events.MustMessageEventDefinition(seamMessage(t), nil)
			item := msgflow.CaptureItem(med)
			require.NotNil(t, item)
			require.Equal(t, "order_item", item.ID())
		})

	t.Run("nil for a definition without items",
		func(t *testing.T) {
			sig := events.MustSignalEventDefinition(&events.Signal{})
			require.Nil(t, msgflow.CaptureItem(sig))
		})

	t.Run("nil for a nil definition",
		func(t *testing.T) {
			require.Nil(t, msgflow.CaptureItem(nil))
		})
}

func TestBind(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	item := data.MustItemDefinition(values.NewVariable("ORD-2"),
		foundation.WithID("order_in"))

	t.Run("binds the item as a Ready datum",
		func(t *testing.T) {
			var put data.Data

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				Put(mock.Anything).
				RunAndReturn(func(dd ...data.Data) error {
					put = dd[0]

					return nil
				})

			require.NoError(t, msgflow.Bind(ctx, re, item))
			require.Equal(t, "order_in", put.ItemDefinition().ID())
			require.Equal(t, "ORD-2", put.Value().Get(ctx))
		})

	t.Run("a nil item is a no-op",
		func(t *testing.T) {
			require.NoError(t, msgflow.Bind(ctx,
				mockrenv.NewMockRuntimeEnvironment(t), nil))
		})

	t.Run("nil runtime environment is rejected",
		func(t *testing.T) {
			require.Error(t, msgflow.Bind(ctx, nil, item))
		})

	t.Run("a Put failure is wrapped",
		func(t *testing.T) {
			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				Put(mock.Anything).
				Return(fmt.Errorf("commit failed"))

			err := msgflow.Bind(ctx, re, item)
			require.Error(t, err)
			require.ErrorContains(t, err, "bind message item")
		})
}
