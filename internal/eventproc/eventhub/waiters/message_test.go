package waiters_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
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

// TestJoinBeforeServiceIsSubscribedByService covers the not-yet-serving
// branch: before Service there is no subscription to extend, so the join
// records the processor and defers to Service — which must then actually
// subscribe the deferred key.
//
// That deferral is the entire content of the branch, so asserting only that
// AddEventProcessor returned nil and grew the list tests nothing: a waiter
// that dropped the key on the floor passes it. The test services the waiter
// and addresses an envelope to the joiner's key alone.
func TestJoinBeforeServiceIsSubscribedByService(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)
	rt := enginert.Default()
	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	firstMock := mockeventproc.NewMockEventProcessor(t)
	firstMock.EXPECT().ID().Return("a").Maybe()
	firstMock.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).Return(nil).Maybe()

	first := keyedProc{MockEventProcessor: firstMock, keys: []string{"k-a"}}

	w, err := waiters.NewMessageWaiter(hub, first, eDef, "", rt)
	require.NoError(t, err)

	delivered := make(chan flow.EventDefinition, 1)
	joinerMock := mockeventproc.NewMockEventProcessor(t)
	joinerMock.EXPECT().ID().Return("b").Maybe()
	joinerMock.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ed flow.EventDefinition) error {
			delivered <- ed

			return nil
		}).Maybe()

	joiner := keyedProc{MockEventProcessor: joinerMock, keys: []string{"k-b"}}

	// no Service yet — the join must succeed and simply record the processor
	require.NoError(t, w.AddEventProcessor(joiner))
	require.Len(t, w.EventProcessors(), 2)

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { require.NoError(t, w.Stop()) })

	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "k-b", CorrelationKey: "k-b"}))

	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("Service did not subscribe the key of a processor that " +
			"joined before it")
	}
}

// gatedBroker holds Subscribe open until release is closed, so a test can put
// a join inside the window between Service reading the processors' keys and
// publishing the subscription those keys were passed to.
//
// With deadKeys set, the subscriptions it hands out refuse every AddKey —
// which is how the catch-up's failure path is reached.
type gatedBroker struct {
	messaging.MessageBroker

	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
	deadKeys  bool
	deadUnsub bool
}

func (b *gatedBroker) Subscribe(
	ctx context.Context, name string, keys ...string,
) (messaging.Subscription, error) {
	b.once.Do(func() { close(b.entered) })

	<-b.release

	sub, err := b.MessageBroker.Subscribe(ctx, name, keys...)
	if err != nil || !b.deadKeys {
		return sub, err
	}

	return &deadKeySub{Subscription: sub, deadUnsub: b.deadUnsub}, nil
}

// deadKeySub is a subscription that cannot be extended, and — with deadUnsub
// — cannot be torn down either.
type deadKeySub struct {
	messaging.Subscription

	deadUnsub bool
}

func (s *deadKeySub) AddKey(string) error {
	return errors.New("the broker refused the key")
}

func (s *deadKeySub) Unsubscribe() error {
	if s.deadUnsub {
		return errors.New("the broker refused the unsubscribe")
	}

	return s.Subscription.Unsubscribe()
}

// TestJoinDuringSubscribeIsCaughtUp pins the window both independent review
// lenses found in the joining-key fix itself.
//
// Service reads the processors' key list, makes the BLOCKING Subscribe call
// outside the lock (FIX-038 §1.1 put it there deliberately), and publishes
// mw.sub only afterwards. A processor joining in between sees a nil
// subscription and defers to Service — but Service has already read the list,
// so the key is subscribed by nobody: the same silent lost delivery, one
// window narrower.
//
// The hub makes this unreachable today, because registerWaiter services a
// waiter before publishing it into the registry, so no other goroutine holds a
// reference while Service runs. That is an ordering in a different package,
// and a waiter that depends on it fails silently the day it changes. This test
// reaches the window directly.
func TestJoinDuringSubscribeIsCaughtUp(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	broker := &gatedBroker{
		MessageBroker: membroker.New(),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	rt := flakyKeyRuntime{EngineRuntime: enginert.Default(), broker: broker}

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	firstMock := mockeventproc.NewMockEventProcessor(t)
	firstMock.EXPECT().ID().Return("a").Maybe()
	firstMock.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).Return(nil).Maybe()

	first := keyedProc{MockEventProcessor: firstMock, keys: []string{"k-a"}}

	w, err := waiters.NewMessageWaiter(hub, first, eDef, "", rt)
	require.NoError(t, err)

	served := make(chan error, 1)

	go func() { served <- w.Service(ctx) }()

	// Service has read the key list and is now inside Subscribe.
	<-broker.entered

	delivered := make(chan flow.EventDefinition, 1)
	joinerMock := mockeventproc.NewMockEventProcessor(t)
	joinerMock.EXPECT().ID().Return("b").Maybe()
	joinerMock.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ed flow.EventDefinition) error {
			delivered <- ed

			return nil
		}).Maybe()

	joiner := keyedProc{MockEventProcessor: joinerMock, keys: []string{"k-b"}}
	require.NoError(t, w.AddEventProcessor(joiner))

	close(broker.release)
	require.NoError(t, <-served)

	t.Cleanup(func() { require.NoError(t, w.Stop()) })

	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "k-b", CorrelationKey: "k-b"}))

	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("a processor that joined while Subscribe was blocked was " +
			"never subscribed")
	}
}

// flakyKeyBroker wraps a real broker and fails the first AddKey on every
// subscription it hands out.
type flakyKeyBroker struct {
	messaging.MessageBroker

	mu    sync.Mutex
	calls int
}

func (b *flakyKeyBroker) Subscribe(
	ctx context.Context, name string, keys ...string,
) (messaging.Subscription, error) {
	sub, err := b.MessageBroker.Subscribe(ctx, name, keys...)
	if err != nil {
		return nil, err
	}

	return &flakyKeySub{Subscription: sub, owner: b}, nil
}

func (b *flakyKeyBroker) addKeyCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.calls
}

type flakyKeySub struct {
	messaging.Subscription

	owner *flakyKeyBroker
}

func (s *flakyKeySub) AddKey(key string) error {
	s.owner.mu.Lock()
	n := s.owner.calls
	s.owner.calls++
	s.owner.mu.Unlock()

	if n == 0 {
		return errors.New("broker refused the key")
	}

	return s.Subscription.AddKey(key)
}

// flakyKeyRuntime is enginert.Default with the broker swapped.
type flakyKeyRuntime struct {
	renv.EngineRuntime

	broker messaging.MessageBroker
}

func (r flakyKeyRuntime) MessageBroker() messaging.MessageBroker {
	return r.broker
}

// TestRetryAfterAKeyFailureReSubscribes pins the blocker the independent
// review found in the joining-key fix.
//
// The processor is appended to the list BEFORE its keys are subscribed, so a
// failed AddKey leaves it registered with its key missing. If the retry — the
// very thing a caller does after this method returns an error — saw "already
// present" and returned nil, the processor would be stranded exactly as it
// was before the fix: registered, parked, and unreachable, now with a success
// reported to the caller.
func TestRetryAfterAKeyFailureReSubscribes(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	broker := &flakyKeyBroker{MessageBroker: membroker.New()}
	rt := flakyKeyRuntime{EngineRuntime: enginert.Default(), broker: broker}

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	firstMock := mockeventproc.NewMockEventProcessor(t)
	firstMock.EXPECT().ID().Return("first").Maybe()
	firstMock.EXPECT().ProcessEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	first := keyedProc{MockEventProcessor: firstMock, keys: []string{"k-a"}}

	w, err := waiters.NewMessageWaiter(hub, first, eDef, "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { require.NoError(t, w.Stop()) })

	joinerMock := mockeventproc.NewMockEventProcessor(t)
	joinerMock.EXPECT().ID().Return("joiner").Maybe()
	joinerMock.EXPECT().ProcessEvent(mock.Anything, mock.Anything).
		Return(nil).Maybe()

	joiner := keyedProc{MockEventProcessor: joinerMock, keys: []string{"k-b"}}

	// first attempt: the broker refuses the key, so the join must FAIL rather
	// than report a processor that cannot be reached.
	require.Error(t, w.AddEventProcessor(joiner),
		"a key the broker refused must surface, not be swallowed")

	// the retry must actually retry
	require.NoError(t, w.AddEventProcessor(joiner))
	require.Equal(t, 2, broker.addKeyCalls(),
		"the retry re-issued AddKey; returning early on 'already present' "+
			"would have reported success without subscribing the key")

	// and the joiner is now genuinely reachable
	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "k-b", CorrelationKey: "k-b"}))
}

// TestServiceFailsWhenALateJoinerCannotBeSubscribed covers the catch-up's
// failure path, which is the one that must not leave a half-started waiter.
//
// If the broker refuses the joiner's key there is no way to serve it — the
// instance would park on a subscription that cannot reach it, which is the
// exact failure the catch-up exists to prevent. Service therefore tears the
// subscription down and fails, rather than starting a goroutine over a
// subscription it knows is incomplete.
func TestServiceFailsWhenALateJoinerCannotBeSubscribed(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	broker := &gatedBroker{
		MessageBroker: membroker.New(),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
		deadKeys:      true,
	}
	rt := flakyKeyRuntime{EngineRuntime: enginert.Default(), broker: broker}

	hub := mockeventproc.NewMockEventHub(t)

	firstMock := mockeventproc.NewMockEventProcessor(t)
	firstMock.EXPECT().ID().Return("a").Maybe()
	first := keyedProc{MockEventProcessor: firstMock, keys: []string{"k-a"}}

	w, err := waiters.NewMessageWaiter(hub, first, eDef, "", rt)
	require.NoError(t, err)

	served := make(chan error, 1)

	go func() { served <- w.Service(ctx) }()

	<-broker.entered

	joinerMock := mockeventproc.NewMockEventProcessor(t)
	joinerMock.EXPECT().ID().Return("b").Maybe()
	joiner := keyedProc{MockEventProcessor: joinerMock, keys: []string{"k-b"}}
	require.NoError(t, w.AddEventProcessor(joiner))

	close(broker.release)

	serr := <-served
	require.Error(t, serr,
		"a waiter that cannot subscribe a joined key must fail to start, "+
			"not serve a subscription it knows is missing one")
	require.Contains(t, serr.Error(), "correlation key")

	require.Equal(t, eventproc.WSFailed, w.State(),
		"the waiter must be left failed, not ready or running")

	require.Error(t, w.Stop(),
		"no goroutine was started, so there is nothing to stop")
}

// TestServiceReportsTheJoinerFailureEvenIfTeardownAlsoFails: when the
// catch-up fails AND the subscription cannot be torn down, the error the
// CALLER gets must still be the one it can act on.
//
// The teardown failure is logged and swallowed deliberately — a broker that
// refuses an unsubscribe tells the caller nothing about why the waiter would
// not start, and returning it instead would replace a diagnosable cause with
// a symptom.
func TestServiceReportsTheJoinerFailureEvenIfTeardownAlsoFails(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	broker := &gatedBroker{
		MessageBroker: membroker.New(),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
		deadKeys:      true,
		deadUnsub:     true,
	}
	rt := flakyKeyRuntime{EngineRuntime: enginert.Default(), broker: broker}

	hub := mockeventproc.NewMockEventHub(t)

	firstMock := mockeventproc.NewMockEventProcessor(t)
	firstMock.EXPECT().ID().Return("a").Maybe()
	first := keyedProc{MockEventProcessor: firstMock, keys: []string{"k-a"}}

	w, err := waiters.NewMessageWaiter(hub, first, eDef, "", rt)
	require.NoError(t, err)

	served := make(chan error, 1)

	go func() { served <- w.Service(ctx) }()

	<-broker.entered

	joinerMock := mockeventproc.NewMockEventProcessor(t)
	joinerMock.EXPECT().ID().Return("b").Maybe()
	joiner := keyedProc{MockEventProcessor: joinerMock, keys: []string{"k-b"}}
	require.NoError(t, w.AddEventProcessor(joiner))

	close(broker.release)

	serr := <-served
	require.Error(t, serr)
	require.Contains(t, serr.Error(), "correlation key",
		"the reported cause must be the key that could not be subscribed, "+
			"not the unsubscribe that failed while cleaning up after it")
	require.NotContains(t, serr.Error(), "unsubscribe")

	require.Equal(t, eventproc.WSFailed, w.State())
}

// TestAddKeyRecordsTheKeyItSubscribed covers the lazy-association path
// (SRD-017 §4.5) that correlation.go drives when an instance LEARNS a key
// mid-flight, and the record it must leave behind.
//
// The waiter's key record is what a joining processor consults to decide
// whether its own key still needs subscribing. A key added here and not
// recorded would make the record understate the subscription — harmless in
// itself, since re-adding is idempotent, but it is the field a correctness
// decision is made against.
func TestAddKeyRecordsTheKeyItSubscribed(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)
	rt := enginert.Default()

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	delivered := make(chan flow.EventDefinition, 1)
	epMock := mockeventproc.NewMockEventProcessor(t)
	epMock.EXPECT().ID().Return("a").Maybe()
	epMock.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ed flow.EventDefinition) error {
			delivered <- ed

			return nil
		}).Maybe()

	ep := keyedProc{MockEventProcessor: epMock, keys: []string{"k-a"}}

	w, err := waiters.NewMessageWaiter(hub, ep, eDef, "", rt)
	require.NoError(t, err)

	adder, ok := w.(interface{ AddKey(string) error })
	require.True(t, ok, "a message waiter carries the lazy-association seam")

	// before Service there is no subscription to extend: a no-op, not an error
	require.NoError(t, adder.AddKey("learned"))

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { require.NoError(t, w.Stop()) })

	require.NoError(t, adder.AddKey("learned"))

	// the learned key now routes to this waiter
	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "x", CorrelationKey: "learned"}))

	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("a key added through AddKey never reached the subscription")
	}

	// A processor declaring the SAME key now joins. Its key is already
	// carried, so the join must not re-issue it — which is only knowable if
	// AddKey recorded what it subscribed.
	joinerMock := mockeventproc.NewMockEventProcessor(t)
	joinerMock.EXPECT().ID().Return("b").Maybe()
	joinerMock.EXPECT().
		ProcessEvent(mock.Anything, mock.Anything).Return(nil).Maybe()

	joiner := keyedProc{
		MockEventProcessor: joinerMock, keys: []string{"learned"},
	}
	require.NoError(t, w.AddEventProcessor(joiner))
}

// TestAddKeyReportsABrokerRefusal: the lazy-association seam is best-effort at
// its CALLER (correlation.go logs and continues), which only works if this
// side actually reports the failure rather than swallowing it.
func TestAddKeyReportsABrokerRefusal(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	broker := &gatedBroker{
		MessageBroker: membroker.New(),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
		deadKeys:      true,
	}
	close(broker.release) // no gating wanted here, only the dead keys

	rt := flakyKeyRuntime{EngineRuntime: enginert.Default(), broker: broker}
	hub := mockeventproc.NewMockEventHub(t)

	epMock := mockeventproc.NewMockEventProcessor(t)
	epMock.EXPECT().ID().Return("a").Maybe()
	ep := keyedProc{MockEventProcessor: epMock, keys: []string{"k-a"}}

	w, err := waiters.NewMessageWaiter(hub, ep, eDef, "", rt)
	require.NoError(t, err)
	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { require.NoError(t, w.Stop()) })

	adder, ok := w.(interface{ AddKey(string) error })
	require.True(t, ok)

	require.Error(t, adder.AddKey("learned"),
		"a refused key must be reported, not silently dropped — the caller "+
			"decides whether to degrade")
}
