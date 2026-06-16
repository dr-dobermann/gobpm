package activities_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

// sendReadyParam builds a Ready process datum carrying value under id.
func sendReadyParam(id string, value any) data.Data {
	return data.MustParameter(id,
		data.MustItemAwareElement(
			data.MustItemDefinition(
				values.NewVariable(value),
				foundation.WithID(id)),
			data.ReadyDataState))
}

func sendMessage(t *testing.T) *bpmncommon.Message {
	t.Helper()

	return bpmncommon.MustMessage("order placed",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("order_item")))
}

func TestNewSendTask(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("happy path",
		func(t *testing.T) {
			msg := sendMessage(t)

			st, err := activities.NewSendTask("notify", msg,
				activities.WithoutParams())
			require.NoError(t, err)
			require.Equal(t, flow.SendTask, st.TaskType())
			require.Equal(t, st, st.Node())
			require.Equal(t, msg, st.Message())
			require.Empty(t, st.Implementation())
		})

	t.Run("empty name is rejected",
		func(t *testing.T) {
			_, err := activities.NewSendTask("  ", sendMessage(t),
				activities.WithoutParams())
			require.Error(t, err)
		})

	t.Run("nil message is rejected",
		func(t *testing.T) {
			_, err := activities.NewSendTask("notify", nil,
				activities.WithoutParams())
			require.Error(t, err)
		})

	t.Run("an invalid task option is rejected",
		func(t *testing.T) {
			_, err := activities.NewSendTask("notify", sendMessage(t),
				events.WithParallel())
			require.Error(t, err)
		})
}

func TestSendTaskClone(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	st, err := activities.NewSendTask("notify", sendMessage(t),
		activities.WithoutParams())
	require.NoError(t, err)

	cl, ok := st.Clone().(*activities.SendTask)
	require.True(t, ok)
	require.Equal(t, "order placed", cl.Message().Name())
	require.NotSame(t, st.Message(), cl.Message())
}

func TestSendTaskExec(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("happy path: the message reaches the broker",
		func(t *testing.T) {
			st, err := activities.NewSendTask("notify", sendMessage(t),
				activities.WithoutParams())
			require.NoError(t, err)

			broker := membroker.New()
			ch, err := broker.Subscribe(ctx, "order placed", "")
			require.NoError(t, err)

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				GetDataByID("order_item").
				Return(sendReadyParam("order_item", "ORD-7"), nil)
			re.EXPECT().MessageBroker().Return(broker)

			flows, err := st.Exec(ctx, re)
			require.NoError(t, err)
			require.Empty(t, flows)

			select {
			case env := <-ch:
				require.Equal(t, "order placed", env.Name)
				require.Equal(t, "ORD-7", env.Payload)
			default:
				t.Fatal("no envelope delivered to the subscriber")
			}
		})

	t.Run("nil runtime environment is rejected",
		func(t *testing.T) {
			st, err := activities.NewSendTask("notify", sendMessage(t),
				activities.WithoutParams())
			require.NoError(t, err)

			_, err = st.Exec(ctx, nil)
			require.Error(t, err)
		})

	t.Run("a publication failure is wrapped",
		func(t *testing.T) {
			st, err := activities.NewSendTask("notify", sendMessage(t),
				activities.WithoutParams())
			require.NoError(t, err)

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				GetDataByID("order_item").
				Return(nil, fmt.Errorf("not in scope"))

			_, err = st.Exec(ctx, re)
			require.Error(t, err)
			require.ErrorContains(t, err, "publication failed")
		})
}

// TestSendTaskCorrelationKey covers WithCorrelationKey on a SendTask (SRD-015
// M5b-producer / ADR-016 §2.2): the option sets the key, the accessor returns
// it, and it survives Clone. The default is nil.
func TestSendTaskCorrelationKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	msg := bpmncommon.MustMessage("order placed",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("order_item")))

	plain, err := activities.NewSendTask("send", msg, activities.WithoutParams())
	require.NoError(t, err)
	require.Nil(t, plain.CorrelationKey())

	key := &bpmncommon.CorrelationKey{Name: "orderKey"}

	st, err := activities.NewSendTask("send", msg,
		activities.WithoutParams(), activities.WithCorrelationKey(key))
	require.NoError(t, err)
	require.Same(t, key, st.CorrelationKey())

	cl, ok := st.Clone().(*activities.SendTask)
	require.True(t, ok)
	require.Same(t, key, cl.CorrelationKey())
}
