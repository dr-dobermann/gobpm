package waiters_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/messagingtest"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// keyed asserts the waiter carries the optional correlation-key seam and
// returns it. The seam is eventproc.KeyedWaiter — declared rather than asserted
// anonymously (FIX-041 §3.1 D4) — and it is optional, so every test that
// exercises a key learned after construction asserts its presence first.
func keyed(t *testing.T, w eventproc.EventWaiter) eventproc.KeyedWaiter {
	t.Helper()

	kw, ok := w.(eventproc.KeyedWaiter)
	require.True(t, ok, "a message waiter must be a KeyedWaiter")

	return kw
}

// join performs both halves of a join the way the EventHub does: the registry
// half, then the broker half once the hub's lock would have been released
// (FIX-041 §3.1 D1). Tests in this file drive the waiter directly, so they must
// drive both halves or they are testing a join no caller performs.
func join(
	t *testing.T, w eventproc.EventWaiter, ep eventproc.EventProcessor,
) error {
	t.Helper()

	if err := w.AddEventProcessor(ep); err != nil {
		return err
	}

	return keyed(t, w).ApplyProcessorKeys(ep)
}

// keyedProcessor is an EventProcessor that answers to correlation keys — the
// shape a parked Multi-Instance iteration presents to the waiter. The mock
// generated for EventProcessor cannot carry CorrelationKeys, which is the
// method the subscription is built from, so the fixture is written by hand.
type keyedProcessor struct {
	id   string
	keys []string
	got  chan flow.EventDefinition
}

func newKeyedProcessor(id string, keys ...string) *keyedProcessor {
	return &keyedProcessor{
		id:   id,
		keys: keys,
		got:  make(chan flow.EventDefinition, 1),
	}
}

func (p *keyedProcessor) ID() string { return p.id }

func (p *keyedProcessor) CorrelationKeys() []string { return p.keys }

func (p *keyedProcessor) ProcessEvent(
	_ context.Context, ed flow.EventDefinition,
) error {
	select {
	case p.got <- ed:
	default:
	}

	return nil
}

var _ eventproc.KeyedProcessor = (*keyedProcessor)(nil)

// TestJoiningProcessorBringsItsKey is #320's regression pin.
//
// The subscription is built from the keys known when Service subscribes. A
// processor that JOINS afterwards — the second iteration of a Multi-Instance
// activity parking at the same catch — used to add itself to the processor
// list and nothing else, so the broker still routed only the first
// iteration's key. Its envelope then matched no subscription and waited in
// the broker's inbox forever: the test hung at its deadline with no data
// race, at any deadline, because nothing was ever going to arrive.
//
// The lazy AddEventKey path cannot cover this — it silently no-ops while the
// waiter is not yet in the hub's map, which is exactly the window in which a
// sibling iteration derives its key.
func TestJoiningProcessorBringsItsKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)
	rt := enginert.Default()

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	first := newKeyedProcessor("iter-0", "a")

	w, err := waiters.NewMessageWaiter(hub, first, eDef, "", rt)
	require.NoError(t, err)

	// the waiter subscribes knowing only "a"
	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	// …and the second iteration parks afterwards, answering to "b"
	second := newKeyedProcessor("iter-1", "b")
	require.NoError(t, join(t, w, second))

	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-B", CorrelationKey: "b"}))

	select {
	case <-second.got:
	case <-time.After(2 * time.Second):
		t.Fatal("the envelope keyed for the joining iteration never " +
			"arrived: its key is missing from the subscription (#320)")
	}
}

// TestKeyAddedBeforeSubscribeSurvives pins the other half. AddKey used to
// return nil when the subscription did not exist yet, on the reasoning that
// the key would be read from the processors at Service time — true only for a
// key the processor already answers to. A key learned by the instance in
// between was dropped with no error and no log.
func TestKeyAddedBeforeSubscribeSurvives(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)
	rt := enginert.Default()

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	// The processor answers to a key of its own, so the subscription is
	// KEYED rather than a wildcard. A keyless processor leaves a wildcard
	// subscription that matches every envelope, which would let this test
	// pass with the key dropped — it did, before this comment existed.
	proc := newKeyedProcessor("iter-0", "own")

	w, err := waiters.NewMessageWaiter(hub, proc, eDef, "", rt)
	require.NoError(t, err)

	require.NoError(t, keyed(t, w).AddKey("late"),
		"a key learned before the subscription exists is accepted")

	// "late" is reachable ONLY if AddKey buffered it: it belongs to no
	// processor, so Service cannot rediscover it from the processor list.

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-L", CorrelationKey: "late"}))

	select {
	case <-proc.got:
	case <-time.After(2 * time.Second):
		t.Fatal("a key added before Service was dropped rather than " +
			"applied when the subscription appeared (#320)")
	}
}

// TestLazyKeyReachesALiveSubscription covers the ordinary half of AddKey: the
// subscription already exists, so the key goes straight to the broker instead
// of being buffered. It is the path an iteration takes when it derives its key
// after its waiter is serviced — the common case, and the one whose failure
// the two tests above were written for.
func TestLazyKeyReachesALiveSubscription(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)
	rt := enginert.Default()

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	// keyed, so the subscription routes by key rather than matching
	// everything — a wildcard would let this pass with the key dropped.
	proc := newKeyedProcessor("iter-0", "own")

	w, err := waiters.NewMessageWaiter(hub, proc, eDef, "", rt)
	require.NoError(t, err)

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	require.NoError(t, keyed(t, w).AddKey("later"))

	require.NoError(t, rt.MessageBroker().Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-X", CorrelationKey: "later"}))

	select {
	case <-proc.got:
	case <-time.After(2 * time.Second):
		t.Fatal("a key added to a live subscription never reached the broker")
	}
}

// TestJoinHalvesAreSeparable pins the split D1 rests on: AddEventProcessor is
// registry work that reaches nothing outside the waiter, and ApplyProcessorKeys
// is the only half that talks to the broker.
//
// This is what lets the EventHub hold its one lock across the first half and
// not the second. While the two were one method, a join reached the host's
// broker from inside eh.m and every registration, unregistration and lookup in
// the engine queued behind it (FIX-038 §1.1, reintroduced by the #320 fix).
// The hub-side timing consequence is pinned by TestJoinDoesNotHoldTheHubLock;
// this test pins the property that makes it possible, which is cheaper to run
// and more precise about which half is at fault when it breaks.
func TestJoinHalvesAreSeparable(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	var reached []string

	broker := &messagingtest.FailingBroker{
		// Set before Service: a subscription copies the hook when it is
		// handed out.
		OnAddKey: func(key string) { reached = append(reached, key) },
	}
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "a"),
		eDef, "", rt)
	require.NoError(t, err)

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	second := newKeyedProcessor("iter-1", "b")

	require.NoError(t, w.AddEventProcessor(second))
	require.Contains(t, w.EventProcessors(), eventproc.EventProcessor(second),
		"the registry half registers")
	require.Empty(t, reached,
		"the registry half must reach no broker: it runs under the hub's "+
			"engine-wide lock, and a host call there stalls the whole engine")

	require.NoError(t, keyed(t, w).ApplyProcessorKeys(second))
	require.Equal(t, []string{"b"}, reached,
		"the foreign half applies the joining processor's key")
	require.Equal(t, []string{"b"}, broker.Subscriptions()[0].Added())
}

// TestApplyProcessorKeysGuardsItsInput covers what the foreign half does with
// input it cannot act on: a nil processor is rejected, and a KEYLESS one — a
// plain track, which is the common case — is a no-op that must not reach the
// broker at all. A waiter subscribed with no keys is a wildcard, so a spurious
// AddKey there would quietly narrow it.
func TestApplyProcessorKeysGuardsItsInput(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	broker := &messagingtest.FailingBroker{}
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "a"),
		eDef, "", rt)
	require.NoError(t, err)

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	kw := keyed(t, w)

	require.Error(t, kw.ApplyProcessorKeys(nil))

	// A processor with no keys of its own: registered, but nothing to apply.
	require.NoError(t, kw.ApplyProcessorKeys(newKeyedProcessor("keyless")))
	require.Empty(t, broker.Subscriptions()[0].Added(),
		"a keyless processor must not touch the subscription")
}

// TestApplyProcessorKeysRejectsAFailedWaiter covers the state a join can arrive
// in through no fault of its own: another registration failed the waiter and
// the hub unmapped it, between this one reading it out of the registry and
// getting here.
//
// Buffering into it would return nil for keys that reach no broker — #320's
// silence, restored by the code that fixes it. So it is refused, loudly.
func TestApplyProcessorKeysRejectsAFailedWaiter(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	broker := &messagingtest.FailingBroker{
		AddKeyErr:   messagingtest.ErrInjected,
		AddKeyAfter: 1, // the first key lands, the second is refused
	}
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "a"),
		eDef, "", rt)
	require.NoError(t, err)

	require.NoError(t, w.Service(ctx))

	// Fail it the ordinary way: a PARTIAL key failure, which is the only kind
	// that costs the waiter its subscription. A first-key refusal turns the
	// join away and leaves the waiter serving.
	require.Error(t, join(t, w, newKeyedProcessor("iter-1", "b", "c")))
	require.Equal(t, eventproc.WSFailed, w.State())

	err = keyed(t, w).ApplyProcessorKeys(newKeyedProcessor("iter-2", "c"))
	require.Error(t, err,
		"a waiter that gave up its subscription must refuse further keys "+
			"rather than buffer them into a waiter nothing will service")
	require.NotErrorIs(t, err, messagingtest.ErrInjected,
		"the refusal is the waiter's own state, not another broker call")
}

// TestStopDoesNotHoldTheWaiterLock pins the waiter-side half of the
// foreignness rule, which the hub-side TestJoinDoesNotHoldTheHubLock does not
// reach.
//
// Stop unsubscribes, and unsubscribing is a call into the host's broker. While
// mw.m was held across it, State, EventProcessors and every delivery snapshot
// queued behind a call this engine cannot hurry. The state change and the
// stopCh close stay under the lock — nothing may observe a half-stopped waiter
// — and only the broker call moves out.
func TestStopDoesNotHoldTheWaiterLock(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	var (
		entered = make(chan struct{})
		release = make(chan struct{})
		first   sync.Once
	)

	// EXACTLY ONE caller takes the blocking branch, and `sync.Once` is what
	// makes that true. Stop unsubscribes, and so does the service goroutine's
	// own teardown, so this hook is entered TWICE and concurrently —
	// FailingSubscription calls it before its lock and on every call, which is
	// its contract (failing_test.go counts the calls).
	//
	// Deciding "am I first?" by reading `entered` and closing it if not —
	// select/default — is check-then-act: both callers can see it open before
	// either close lands, and the second close panics. That is not theoretical;
	// it reddened `test-core` on 2026-08-29, and 200 isolated runs plus 8
	// race-enabled package runs never reproduced it. The window is narrow, not
	// absent.
	broker := &messagingtest.FailingBroker{
		OnUnsubscribe: func() {
			blocking := false

			first.Do(func() {
				blocking = true

				close(entered)
			})

			if blocking {
				<-release // the later teardown passes straight through
			}
		},
	}
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "a"),
		eDef, "", rt)
	require.NoError(t, err)

	require.NoError(t, w.Service(ctx))

	stopped := make(chan error, 1)

	go func() { stopped <- w.Stop() }()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop never reached the broker")
	}

	// The waiter is pinned inside a host call. Reading its state takes mw.m,
	// so it answers only if Stop let the lock go.
	read := make(chan eventproc.EventWaiterState, 1)

	go func() { read <- w.State() }()

	select {
	case st := <-read:
		require.Equal(t, eventproc.WSStopped, st,
			"the state change belongs under the lock, before the host call")
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("State blocked while Stop was inside the broker: the waiter " +
			"is holding mw.m across a host call (FIX-038 §1.1)")
	}

	close(release)
	require.NoError(t, <-stopped)
}

// TestFinishedWaiterRefusesALazyKey is the sibling of
// TestApplyProcessorKeysRejectsAFailedWaiter, and the reason both checks now
// live in one place.
//
// `mw.sub == nil` means two opposite things. Before Service it means "not
// subscribed YET", and a key is buffered for Service to pick up. After a
// teardown — discardSubscription hands the subscription back, and the hub
// unmaps the waiter — it means "not subscribed ANY MORE", and buffering there
// takes a key into a waiter nothing will ever service while returning nil to a
// caller that believes it was accepted. That is #320's silence exactly: the key
// is taken, never routed, and nothing reports it.
//
// It is reachable through EventHub.AddEventKey, which reads the waiter from the
// registry under RLock, releases, and only then calls AddKey.
func TestFinishedWaiterRefusesALazyKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	for _, tc := range []struct {
		name string
		kill func(t *testing.T, w eventproc.EventWaiter, ctx context.Context)
	}{
		{
			name: "failed",
			kill: func(t *testing.T, w eventproc.EventWaiter, _ context.Context) {
				// a PARTIAL key failure is what costs a waiter its subscription
				require.Error(t, join(t, w, newKeyedProcessor("iter-1", "b", "c")))
				require.Equal(t, eventproc.WSFailed, w.State())
			},
		},
		{
			name: "stopped",
			kill: func(t *testing.T, w eventproc.EventWaiter, _ context.Context) {
				require.NoError(t, w.Stop())
				require.Equal(t, eventproc.WSStopped, w.State())
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			eDef := msgEventDef(t)

			hub := mockeventproc.NewMockEventHub(t)
			hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

			broker := &messagingtest.FailingBroker{
				AddKeyErr:   messagingtest.ErrInjected,
				AddKeyAfter: 1,
			}
			rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

			w, err := waiters.NewMessageWaiter(hub,
				newKeyedProcessor("iter-0", "a"), eDef, "", rt)
			require.NoError(t, err)

			require.NoError(t, w.Service(ctx))

			tc.kill(t, w, ctx)

			require.Error(t, keyed(t, w).AddKey("late"),
				"a finished waiter must refuse a lazy key rather than buffer "+
					"it: buffering returns nil to a caller that believes the "+
					"key was accepted, and nothing will ever apply it")
		})
	}
}

// TestKeysReachTheBrokerOnce pins that the waiter sends a correlation key to
// the broker exactly once, however many times it is offered.
//
// Three ways it is offered more than once: two processors answering to the same
// key, one processor repeating a key, and — because AddEventProcessor is
// idempotent while ApplyProcessorKeys is not — the same processor joined twice,
// which used to re-send its whole key-set. membroker hides all three (its
// key-set is a map); a host adapter that turns each key into a queue-level
// binding makes a second binding each time.
func TestKeysReachTheBrokerOnce(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	broker := &messagingtest.FailingBroker{}
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "a"),
		eDef, "", rt)
	require.NoError(t, err)

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	second := newKeyedProcessor("iter-1", "b", "b", "a")

	require.NoError(t, join(t, w, second))
	require.Equal(t, []string{"b"}, broker.Subscriptions()[0].Added(),
		"a key the subscription was CREATED with is not re-sent, and a key "+
			"the processor repeats is sent once")

	// the same processor joined again — the registry half is idempotent, so
	// the broker half must be too
	require.NoError(t, join(t, w, second))
	require.Equal(t, []string{"b"}, broker.Subscriptions()[0].Added(),
		"re-joining a processor must not re-send its key-set")

	require.NoError(t, keyed(t, w).AddKey("b"))
	require.Equal(t, []string{"b"}, broker.Subscriptions()[0].Added(),
		"a lazy key the broker already has is not sent again")
}

// TestPartialKeyFailureDiscardsTheSubscription pins D2: a key the broker
// refuses costs the waiter its whole subscription.
//
// The processor carries TWO keys and the broker accepts one before refusing, so
// the subscription really is left partly keyed — the state the port cannot
// repair, because messaging.Subscription can grow a key-set but not shrink one.
// Leaving it would leave an orphan key on a live subscription, quietly eating
// every message addressed to it: #320's failure mode made permanent.
//
// Asserting the returned error is not enough, and that is the point of this
// test: the pre-fix code returned exactly this error while keeping the
// subscription (FIX-041 §1.3, §4.3).
func TestPartialKeyFailureDiscardsTheSubscription(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	broker := &messagingtest.FailingBroker{
		AddKeyErr:   messagingtest.ErrInjected,
		AddKeyAfter: 1, // the first key lands, the second is refused
	}
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "a"),
		eDef, "", rt)
	require.NoError(t, err)

	// Service subscribes with "a" as a creation key and adds nothing
	// afterwards, so the refusal cannot fire here — it fires on the join.
	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	second := newKeyedProcessor("iter-1", "b", "c")

	err = join(t, w, second)
	require.Error(t, err, "a key the subscription refuses must fail the join")
	require.ErrorIs(t, err, messagingtest.ErrInjected)

	subs := broker.Subscriptions()
	require.Len(t, subs, 1)
	require.Equal(t, []string{"a"}, subs[0].Keys,
		"the subscription was created from the first processor's keys")
	require.Equal(t, []string{"b"}, subs[0].Added(),
		"exactly one key landed before the refusal — the partial state that "+
			"cannot be repaired in place")
	require.True(t, subs[0].Unsubscribed(),
		"a key-set that cannot be repaired is discarded whole: the "+
			"subscription must be gone, not left carrying the orphan key")

	// Wait for the service goroutine before reading the state. Dropping the
	// subscription closes the channel it is selecting on, so it wakes and
	// records an exit state of its own — and a waiter that broke must not end
	// up labelled Stopped, which reads as an orderly shutdown. Waiting also
	// pins that the goroutine exits at all rather than spinning on a closed
	// channel.
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the service goroutine of a failed waiter never exited")
	}

	require.Equal(t, eventproc.WSFailed, w.State(),
		"a waiter that gave up its subscription stays failed: the service "+
			"goroutine woken by its own closed channel must not relabel it "+
			"Stopped, which reports an orderly shutdown for a break")
}

// TestFirstKeyRefusalKeepsTheSubscription is the waiter-side half of
// TestFirstKeyRefusalSparesTheWaiter, and the boundary that keeps D2
// proportionate.
//
// A refusal on the FIRST key of a join applies nothing: the subscription is
// byte-for-byte what it was, and every processor already parked on it is still
// served. Discarding a key-set that was never partly applied would tear down a
// healthy waiter — and every sibling iteration's wait with it — to punish a
// join that failed harmlessly. Only the join is turned away.
func TestFirstKeyRefusalKeepsTheSubscription(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	broker := messagingtest.NewFailingBroker() // every AddKey refused
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "a"),
		eDef, "", rt)
	require.NoError(t, err)

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	err = join(t, w, newKeyedProcessor("iter-1", "b"))
	require.Error(t, err, "a refused key must fail the join")
	require.ErrorIs(t, err, messagingtest.ErrInjected)

	subs := broker.Subscriptions()
	require.Len(t, subs, 1)
	require.False(t, subs[0].Unsubscribed(),
		"nothing was applied, so there is no partial key-set to shed: "+
			"discarding here would strand every processor already parked")
	require.Equal(t, eventproc.WSRunned, w.State(),
		"the waiter keeps serving; only the join failed")
}

// TestKeyRefusedDuringSubscribeFailsTheWaiter covers the last unreachable
// path: a key learned WHILE Subscribe was running, which the waiter buffers
// and applies the moment the subscription appears — and which the broker then
// refuses.
//
// The waiter must end WSFailed with its subscription dropped. It used to
// return the error with mw.sub already set and its state left WSReady: a retry
// would build a second subscription, leak the first, and drop the buffered
// keys permanently.
func TestKeyRefusedDuringSubscribeFailsTheWaiter(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	var w eventproc.EventWaiter

	broker := messagingtest.NewFailingBroker()
	broker.OnSubscribe = func() {
		// Inside Subscribe: the subscription does not exist yet, so the key
		// is buffered rather than applied. This is the window a Multi-
		// Instance iteration derives its key in while a sibling registers
		// (#320) — unreachable from outside, since Subscribe returning is
		// what closes it.
		require.NoError(t, keyed(t, w).AddKey("late"),
			"a key learned during Subscribe is buffered, not refused here")
	}

	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "own"),
		eDef, "", rt)
	require.NoError(t, err)

	err = w.Service(ctx)
	require.Error(t, err, "a buffered key the broker refuses must fail Service")
	require.ErrorIs(t, err, messagingtest.ErrInjected)

	require.Equal(t, eventproc.WSFailed, w.State(),
		"a waiter that could not apply its keys is failed, not ready: a "+
			"retry would leak the subscription it already holds")

	subs := broker.Subscriptions()
	require.Len(t, subs, 1)
	require.True(t, subs[0].Unsubscribed(),
		"the subscription the waiter could not finish keying must be given "+
			"back, not left running behind a failed waiter")
}

// TestJoinBeforeServiceSubscribesEachKeyOnce pins the de-duplication (D3).
//
// A processor that joins before Service has its keys in TWO places — buffered
// by ApplyProcessorKeys, and readable from the processor itself when Service
// snapshots the list — so Subscribe used to receive each of them twice.
// membroker hides this (its key-set is a map), but the port is
// host-implementable and an adapter that turns each key into a queue-level
// registration would make two of them.
//
// The buffering is NOT the bug and must not be removed to fix this: a processor
// joining after Service snapshots its list and before it publishes the
// subscription is in neither, and its key would vanish exactly as in #320.
func TestJoinBeforeServiceSubscribesEachKeyOnce(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	ctx := context.Background()
	eDef := msgEventDef(t)

	hub := mockeventproc.NewMockEventHub(t)
	hub.EXPECT().WaiterFired(eDef.ID()).Return(nil).Maybe()

	broker := &messagingtest.FailingBroker{}
	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.NewMessageWaiter(hub, newKeyedProcessor("iter-0", "a"),
		eDef, "", rt)
	require.NoError(t, err)

	// joins while there is no subscription yet, so its key is buffered
	require.NoError(t, join(t, w, newKeyedProcessor("iter-1", "b")))

	require.NoError(t, w.Service(ctx))

	t.Cleanup(func() { _ = w.Stop() })

	subs := broker.Subscriptions()
	require.Len(t, subs, 1)
	require.ElementsMatch(t, []string{"b", "a"}, subs[0].Keys,
		"each key reaches Subscribe exactly once, however many places the "+
			"waiter learned it from")
}
