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

// chanSub adapts a channel to messaging.Subscription for the broker fakes.
type chanSub struct{ ch <-chan messaging.Envelope }

func (s chanSub) C() <-chan messaging.Envelope { return s.ch }
func (chanSub) AddKey(string) error            { return nil }
func (chanSub) Unsubscribe() error             { return nil }

// closedChBroker returns an already-closed subscription channel.
type closedChBroker struct{}

func (closedChBroker) Publish(context.Context, messaging.Envelope) error { return nil }

func (closedChBroker) Subscribe(
	context.Context, string, ...string,
) (messaging.Subscription, error) {
	ch := make(chan messaging.Envelope)
	close(ch)

	return chanSub{ch: ch}, nil
}

// errSubBroker fails on Subscribe.
type errSubBroker struct{}

func (errSubBroker) Publish(context.Context, messaging.Envelope) error { return nil }

func (errSubBroker) Subscribe(
	context.Context, string, ...string,
) (messaging.Subscription, error) {
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
	// Identity is compared by ID (waiters.sameProcessor), so every processor
	// already on the list is asked for its own — including the one the waiter
	// was built with.
	ep.EXPECT().ID().Return("ep1").Maybe()
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
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Once()

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

	// The waiter is a pure forwarder (ADR-017 v.1 §2): it does not self-terminate
	// on a fire. With a mock processor that never unregisters, it stays Runned —
	// in production the hub removes it when the receiving track consumes and
	// unregisters. Stop ends the service goroutine and closes Done (SRD-019).
	require.Equal(t, eventproc.WSRunned, w.State())
	require.NoError(t, w.Stop())

	select {
	case <-w.Done():
	case <-time.After(time.Second):
		t.Fatal("message waiter Done did not close after Stop")
	}
}

// keyedProc is an EventProcessor that declares correlation keys for its
// subscription (the SRD-017 §4.3 declared-filter), like the in-instance track.
type keyedProc struct {
	*mockeventproc.MockEventProcessor
	keys []string
}

func (k keyedProc) CorrelationKeys() []string { return k.keys }

// TestMessageWaiterKeyedDelivery verifies that a waiter whose processor declares
// correlation keys subscribes keyed: a non-matching key is not delivered, the
// matching key wakes it (SRD-017 §4.3 / FR-2).
func TestMessageWaiterKeyedDelivery(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	rt := enginert.Default()
	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Once()

	done := make(chan flow.EventDefinition, 1)
	mep := mockeventproc.NewMockEventProcessor(t)
	mep.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ed flow.EventDefinition) error {
			done <- ed

			return nil
		})

	ep := keyedProc{MockEventProcessor: mep, keys: []string{"ORD-1"}}

	w, err := waiters.NewMessageWaiter(hub, ep, eDef, "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(ctx))

	// a non-matching key must not wake this keyed receiver.
	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-2", CorrelationKey: "ORD-2"}))

	select {
	case <-done:
		t.Fatal("keyed receiver woke on a non-matching key")
	case <-time.After(200 * time.Millisecond):
	}

	// the matching key wakes it.
	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-1", CorrelationKey: "ORD-1"}))

	select {
	case ed := <-done:
		require.Equal(t, "ORD-1", ed.GetItemsList()[0].Structure().Get(ctx))
	case <-time.After(2 * time.Second):
		t.Fatal("keyed receiver did not wake on the matching key")
	}
}

// TestMessageWaiterPersistent exercises the forward-every-message lifecycle
// (SRD-015 FR-1): the waiter fires for every matching message and stays Runned —
// it never reaches a terminal state on its own, so the hub keeps it (here the
// mock processor never unregisters). Each fire still reports to the hub via
// WaiterFired. Correlation mismatches are dropped by the receiving loop, not the
// waiter (ADR-017 v.1 §2), so the waiter forwards unconditionally.
func TestMessageWaiterPersistent(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	rt := enginert.Default()
	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil)

	const fires = 3

	got := make(chan flow.EventDefinition, fires)
	ep := mockeventproc.NewMockEventProcessor(t)
	ep.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ed flow.EventDefinition) error {
			got <- ed

			return nil
		})

	w, err := waiters.NewMessageWaiter(hub, ep, eDef, "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(ctx))

	for i := range fires {
		require.NoError(t, rt.MessageBroker().Publish(ctx,
			messaging.Envelope{Name: "order placed", Payload: i}))

		select {
		case ed := <-got:
			items := ed.GetItemsList()
			require.Len(t, items, 1)
			require.Equal(t, i, items[0].Structure().Get(ctx))
		case <-time.After(2 * time.Second):
			t.Fatalf("message %d was not delivered to the processor", i)
		}
	}

	// A persistent waiter never goes terminal on its own — it stays Runned
	// until stopped. The hub keeps it (WaiterFired removes only terminal ones).
	require.Equal(t, eventproc.WSRunned, w.State())
	require.NoError(t, w.Stop())
}

func TestMessageWaiterProcessEventError(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()

	rt := enginert.Default()
	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(mock.Anything).Return(nil).Maybe()

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

// TestCreatePersistentWaiter covers the instance-starter builder (SRD-015 M2):
// a message trigger yields a persistent (Ready) waiter; a non-message trigger
// and nil dependencies are rejected.
func TestCreatePersistentWaiter(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	hub := mockeventproc.NewMockEventHub(t)

	w, err := waiters.CreatePersistentWaiter(hub, ep, msgEventDef(t),
		enginert.Default())
	require.NoError(t, err)
	require.Equal(t, eventproc.WSReady, w.State())
	require.NotEmpty(t, w.ID())

	// signal trigger now backs a persistent signal waiter (SRD-026 §3.2).
	sw, err := waiters.CreatePersistentWaiter(hub, ep, signalEDef(t, "GO"),
		enginert.Default())
	require.NoError(t, err)
	require.NotNil(t, sw)

	// a non-message, non-signal trigger is still rejected.
	_, err = waiters.CreatePersistentWaiter(hub, ep, timerEDef(t),
		enginert.Default())
	require.Error(t, err)

	// nil dependencies rejected.
	_, err = waiters.CreatePersistentWaiter(nil, ep, msgEventDef(t),
		enginert.Default())
	require.Error(t, err)

	_, err = waiters.CreatePersistentWaiter(hub, nil, msgEventDef(t),
		enginert.Default())
	require.Error(t, err)

	_, err = waiters.CreatePersistentWaiter(hub, ep, nil, enginert.Default())
	require.Error(t, err)
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

// errUnsubSub never closes its channel and always fails to unsubscribe.
type errUnsubSub struct{ ch <-chan messaging.Envelope }

func (s errUnsubSub) C() <-chan messaging.Envelope { return s.ch }
func (errUnsubSub) AddKey(string) error            { return nil }
func (errUnsubSub) Unsubscribe() error             { return fmt.Errorf("unsubscribe failed") }

// errUnsubBroker hands out subscriptions whose Unsubscribe always errors.
type errUnsubBroker struct{}

func (errUnsubBroker) Publish(context.Context, messaging.Envelope) error { return nil }

func (errUnsubBroker) Subscribe(
	context.Context, string, ...string,
) (messaging.Subscription, error) {
	return errUnsubSub{ch: make(chan messaging.Envelope)}, nil
}

// TestMessageWaiterUnsubscribeErrorIsLogged drives a subscription that fails to
// unsubscribe: Stop still succeeds (the failure is logged, not propagated) and
// the service goroutine still exits. It exercises both unsubscribe-failure
// branches — the synchronous one in Stop and the deferred one on goroutine exit.
func TestMessageWaiterUnsubscribeErrorIsLogged(t *testing.T) {
	ep := mockeventproc.NewMockEventProcessor(t)
	hub := mockeventproc.NewMockEventHub(t)

	rt := brokerRT{EngineRuntime: enginert.Default(), broker: errUnsubBroker{}}

	w, err := waiters.NewMessageWaiter(hub, ep, msgEventDef(t), "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(context.Background()))

	require.NoError(t, w.Stop())

	select {
	case <-w.Done():
	case <-time.After(time.Second):
		t.Fatal("Done did not close after Stop despite the unsubscribe error")
	}
}

// TestJoiningProcessorExtendsTheSubscription is the regression pin for a lost
// message delivery in parallel multi-instance routing.
//
// subscriptionKeys() is read ONCE, by Service. A processor that joins an
// already-serving waiter — which is what every iteration after the first does
// at a shared catch node — therefore contributed its correlation key to
// nothing: the instance parked on a subscription that did not carry its key,
// and an envelope addressed to it matched no subscription and was buffered
// forever. Not misrouted; silently never delivered.
//
// The end-to-end test (TestIterationCorrelatedRouting) only caught this when
// the second registration happened to land after Service, which is why it
// read as an intermittent flake for weeks. This pins it deterministically:
// join AFTER Service, then address an envelope to the joiner alone.
func TestJoiningProcessorExtendsTheSubscription(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	rt := enginert.Default()
	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	// the FIRST processor: it builds and services the waiter, so the
	// subscription is created carrying only its key.
	firstDone := make(chan flow.EventDefinition, 1)
	firstMock := mockeventproc.NewMockEventProcessor(t)
	firstMock.EXPECT().ID().Return("iter-a-proc").Maybe()
	firstMock.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ed flow.EventDefinition) error {
			firstDone <- ed

			return nil
		}).Maybe()

	first := keyedProc{MockEventProcessor: firstMock, keys: []string{"iter-a"}}

	w, err := waiters.NewMessageWaiter(hub, first, eDef, "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { require.NoError(t, w.Stop()) })

	// the SECOND processor joins the SERVING waiter — the ordering the bug
	// depended on.
	joinerDone := make(chan flow.EventDefinition, 1)
	joinerMock := mockeventproc.NewMockEventProcessor(t)
	joinerMock.EXPECT().ID().Return("iter-b-proc").Maybe()
	joinerMock.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ed flow.EventDefinition) error {
			joinerDone <- ed

			return nil
		}).Maybe()

	joiner := keyedProc{MockEventProcessor: joinerMock, keys: []string{"iter-b"}}
	require.NoError(t, w.AddEventProcessor(joiner))

	// An envelope addressed to the JOINER's key only. Before the fix this
	// matched no subscription and sat in the broker's inbox.
	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "iter-b", CorrelationKey: "iter-b"}))

	select {
	case <-joinerDone:
	case <-firstDone:
		// forwarding is coarse — either processor receiving proves the
		// envelope reached the waiter, which is the delivery being pinned.
	case <-time.After(2 * time.Second):
		t.Fatal("an envelope addressed to a JOINING processor's correlation " +
			"key reached the waiter not at all: joining added the processor " +
			"but not its key to the live subscription")
	}
}

// TestJoiningProcessorWithoutKeysIsAccepted covers the branch a joining
// processor that declares NO correlation keys takes: an instance-starter
// wants the wildcard the subscription already has, so there is nothing to
// add and joining must not fail.
func TestJoiningProcessorWithoutKeysIsAccepted(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	rt := enginert.Default()
	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	firstMock := mockeventproc.NewMockEventProcessor(t)
	firstMock.EXPECT().ID().Return("keyed").Maybe()
	firstMock.EXPECT().ProcessEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	first := keyedProc{MockEventProcessor: firstMock, keys: []string{"k1"}}

	w, err := waiters.NewMessageWaiter(hub, first, eDef, "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { require.NoError(t, w.Stop()) })

	// a keyless processor — the instance-starter shape
	plain := mockeventproc.NewMockEventProcessor(t)
	plain.EXPECT().ID().Return("starter").Maybe()
	plain.EXPECT().ProcessEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	require.NoError(t, w.AddEventProcessor(plain),
		"a processor declaring no keys must join without error")
	require.Len(t, w.EventProcessors(), 2)

	// joining twice is idempotent and must not re-add keys either
	require.NoError(t, w.AddEventProcessor(plain))
	require.Len(t, w.EventProcessors(), 2)
}

// TestJoinBeforeServiceAddsNoKeys covers the not-yet-serving branch: before
// Service there is no subscription to extend, and Service will read the
// joined processor's keys itself.
func TestJoinBeforeServiceAddsNoKeys(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	eDef := msgEventDef(t)
	rt := enginert.Default()
	hub := mockeventproc.NewMockEventHub(t)

	firstMock := mockeventproc.NewMockEventProcessor(t)
	firstMock.EXPECT().ID().Return("a").Maybe()
	first := keyedProc{MockEventProcessor: firstMock, keys: []string{"k-a"}}

	w, err := waiters.NewMessageWaiter(hub, first, eDef, "", rt)
	require.NoError(t, err)

	joinerMock := mockeventproc.NewMockEventProcessor(t)
	joinerMock.EXPECT().ID().Return("b").Maybe()
	joiner := keyedProc{MockEventProcessor: joinerMock, keys: []string{"k-b"}}

	// no Service yet — the join must succeed and simply record the processor
	require.NoError(t, w.AddEventProcessor(joiner))
	require.Len(t, w.EventProcessors(), 2)
}
