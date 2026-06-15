package waiters_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// msgEventDef builds a "order placed" MessageEventDefinition carrying an item.
func msgEventDef(t *testing.T) *events.MessageEventDefinition {
	t.Helper()

	return events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_item"))),
		nil)
}

// brokerRT overrides the message broker of an embedded EngineRuntime so a test
// can drive the subscription channel (closed/erroring) deterministically.
type brokerRT struct {
	renv.EngineRuntime
	broker messaging.MessageBroker
}

func (b brokerRT) MessageBroker() messaging.MessageBroker { return b.broker }

// closedChBroker returns an already-closed subscription channel.
type closedChBroker struct{}

func (closedChBroker) Publish(context.Context, messaging.Envelope) error { return nil }

func (closedChBroker) Subscribe(
	context.Context, string, string,
) (<-chan messaging.Envelope, error) {
	ch := make(chan messaging.Envelope)
	close(ch)

	return ch, nil
}

// errSubBroker fails on Subscribe.
type errSubBroker struct{}

func (errSubBroker) Publish(context.Context, messaging.Envelope) error { return nil }

func (errSubBroker) Subscribe(
	context.Context, string, string,
) (<-chan messaging.Envelope, error) {
	return nil, fmt.Errorf("broker down")
}

func TestNewMessageWaiterErrors(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	hub := mockeventproc.NewMockEventHub(t)

	_, err := waiters.NewMessageWaiter(nil, nil, nil, "", nil)
	require.Error(t, err)

	_, err = waiters.NewMessageWaiter(hub, ep,
		events.MustSignalEventDefinition(&events.Signal{}), "",
		enginert.Default())
	require.Error(t, err)
}

func TestMessageWaiterCreate(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	hub := mockeventproc.NewMockEventHub(t)
	eDef := msgEventDef(t)

	w, err := waiters.CreateWaiter(hub, ep, eDef, enginert.Default())
	require.NoError(t, err)
	require.Equal(t, eventproc.WSReady, w.State())
	require.NotEmpty(t, w.ID())
	require.Contains(t, w.EventProcessors(), ep)
	require.Equal(t, eDef, w.EventDefinition())

	// not running yet → Stop fails, Process is unsupported.
	require.Error(t, w.Stop())
	require.Error(t, w.Process(eDef))
}

func TestMessageWaiterProcessors(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	ep2 := mockeventproc.NewMockEventProcessor(t)
	ep2.EXPECT().ID().Return("ep2").Maybe()
	hub := mockeventproc.NewMockEventHub(t)

	w, err := waiters.NewMessageWaiter(hub, ep, msgEventDef(t), "",
		enginert.Default())
	require.NoError(t, err)

	require.Error(t, w.AddEventProcessor(nil))
	require.NoError(t, w.AddEventProcessor(ep2))
	require.NoError(t, w.AddEventProcessor(ep2)) // idempotent
	require.Len(t, w.EventProcessors(), 2)

	require.Error(t, w.RemoveEventProcessor(nil))
	require.NoError(t, w.RemoveEventProcessor(ep2))
	require.Error(t, w.RemoveEventProcessor(ep2)) // already gone
	require.Len(t, w.EventProcessors(), 1)
}

func TestMessageWaiterDelivery(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	rt := enginert.Default()
	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().RemoveWaiter(eDef.ID()).Return(nil).Once()

	done := make(chan flow.EventDefinition, 1)
	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ed flow.EventDefinition) error {
			done <- ed

			return nil
		})

	w, err := waiters.NewMessageWaiter(hub, ep, eDef, "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(ctx))

	require.NoError(t, rt.MessageBroker().Publish(ctx,
		messaging.Envelope{Name: "order placed", Payload: "ORD-9"}))

	select {
	case ed := <-done:
		items := ed.GetItemsList()
		require.Len(t, items, 1)
		require.Equal(t, "ORD-9", items[0].Structure().Get(ctx))
	case <-time.After(2 * time.Second):
		t.Fatal("message was not delivered to the processor")
	}

	require.Eventually(t, func() bool {
		return w.State() == eventproc.WSEnded
	}, time.Second, 10*time.Millisecond)
}

func TestMessageWaiterProcessEventError(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	rt := enginert.Default()
	hub := mockeventproc.NewMockEventHub(t)

	released := make(chan struct{})
	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, flow.EventDefinition) error {
			close(released)

			return fmt.Errorf("processing failed")
		})

	w, err := waiters.NewMessageWaiter(hub, ep, msgEventDef(t), "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(ctx))

	require.NoError(t, rt.MessageBroker().Publish(ctx,
		messaging.Envelope{Name: "order placed", Payload: "x"}))

	<-released
	require.Eventually(t, func() bool {
		return w.State() == eventproc.WSFailed
	}, time.Second, 10*time.Millisecond)
}

func TestMessageWaiterStop(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	hub := mockeventproc.NewMockEventHub(t)

	w, err := waiters.NewMessageWaiter(hub, ep, msgEventDef(t), "",
		enginert.Default())
	require.NoError(t, err)

	require.NoError(t, w.Service(context.Background()))
	require.Error(t, w.Service(context.Background())) // already running
	require.NoError(t, w.Stop())
	require.Equal(t, eventproc.WSStopped, w.State())
}

func TestMessageWaiterContextCancel(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	hub := mockeventproc.NewMockEventHub(t)

	w, err := waiters.NewMessageWaiter(hub, ep, msgEventDef(t), "",
		enginert.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, w.Service(ctx))
	cancel()

	require.Eventually(t, func() bool {
		return w.State() == eventproc.WSStopped
	}, time.Second, 10*time.Millisecond)
}

func TestMessageWaiterClosedChannel(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	hub := mockeventproc.NewMockEventHub(t)

	rt := brokerRT{EngineRuntime: enginert.Default(), broker: closedChBroker{}}

	w, err := waiters.NewMessageWaiter(hub, ep, msgEventDef(t), "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(context.Background()))

	require.Eventually(t, func() bool {
		return w.State() == eventproc.WSStopped
	}, time.Second, 10*time.Millisecond)
}

func TestMessageWaiterSubscribeError(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	hub := mockeventproc.NewMockEventHub(t)

	rt := brokerRT{EngineRuntime: enginert.Default(), broker: errSubBroker{}}

	w, err := waiters.NewMessageWaiter(hub, ep, msgEventDef(t), "", rt)
	require.NoError(t, err)

	require.Error(t, w.Service(context.Background()))
	require.Equal(t, eventproc.WSFailed, w.State())
}
