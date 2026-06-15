package msgflow_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/msgflow"
	"github.com/stretchr/testify/require"
)

// errBroker is a MessageBroker whose Publish always fails — it exercises the
// broker-rejection path of msgflow.Send.
type errBroker struct{}

func (errBroker) Publish(context.Context, messaging.Envelope) error {
	return fmt.Errorf("broker down")
}

func (errBroker) Subscribe(
	context.Context, string, string,
) (<-chan messaging.Envelope, error) {
	return nil, nil
}

// readyParam builds a Ready process datum carrying value under id.
func readyParam(id string, value any) data.Data {
	return data.MustParameter(id,
		data.MustItemAwareElement(
			data.MustItemDefinition(
				values.NewVariable(value),
				foundation.WithID(id)),
			data.ReadyDataState))
}

func TestSend(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	t.Run("happy path: bound payload reaches the broker",
		func(t *testing.T) {
			msg := bpmncommon.MustMessage("order placed",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("order_item")))

			broker := membroker.New()
			ch, err := broker.Subscribe(ctx, "order placed", "")
			require.NoError(t, err)

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				GetDataByID("order_item").
				Return(readyParam("order_item", "ORD-42"), nil)
			re.EXPECT().MessageBroker().Return(broker)

			require.NoError(t, msgflow.Send(ctx, re, msg))

			select {
			case env := <-ch:
				require.Equal(t, "order placed", env.Name)
				require.Equal(t, "ORD-42", env.Payload)
			default:
				t.Fatal("no envelope delivered to the subscriber")
			}
		})

	t.Run("nil RuntimeEnvironment is rejected",
		func(t *testing.T) {
			msg := bpmncommon.MustMessage("ping",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("ping_item")))

			err := msgflow.Send(ctx, nil, msg)
			require.Error(t, err)
			require.Contains(t, err.Error(), "RuntimeEnvironment")
		})

	t.Run("nil Message is rejected",
		func(t *testing.T) {
			re := mockrenv.NewMockRuntimeEnvironment(t)

			err := msgflow.Send(ctx, re, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), "Message")
		})

	t.Run("a scope-bind failure is wrapped",
		func(t *testing.T) {
			msg := bpmncommon.MustMessage("order placed",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("order_item")))

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				GetDataByID("order_item").
				Return(nil, fmt.Errorf("not in scope"))

			err := msgflow.Send(ctx, re, msg)
			require.Error(t, err)
			require.ErrorContains(t, err, "bind message")

			var appErr *errs.ApplicationError
			require.ErrorAs(t, err, &appErr)
		})

	t.Run("a broker rejection is wrapped",
		func(t *testing.T) {
			msg := bpmncommon.MustMessage("order placed",
				data.MustItemDefinition(values.NewVariable(""),
					foundation.WithID("order_item")))

			re := mockrenv.NewMockRuntimeEnvironment(t)
			re.EXPECT().
				GetDataByID("order_item").
				Return(readyParam("order_item", "ORD-42"), nil)
			re.EXPECT().MessageBroker().Return(errBroker{})

			err := msgflow.Send(ctx, re, msg)
			require.Error(t, err)
			require.ErrorContains(t, err, "broker rejected")
		})
}
