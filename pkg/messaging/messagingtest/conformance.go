// Package messagingtest publishes the MessageBroker conformance suite
// (ADR-003 §4.2): every broker — the in-memory default and any adapter over a
// real queue — proves the same routing contract by calling Conformance from a
// one-line test.
//
// The suite covers only what the MessageBroker interface promises: name and
// key matching, most-specific delivery, buffering of undelivered messages,
// lazy secondary keys through AddKey, and Unsubscribe. It deliberately does
// NOT cover inbox capacity or eviction order — the interface calls the inbox
// "bounded, in the default", making the bound a policy of membroker rather
// than a promise of the port, and a suite that asserted it would reject a
// correct adapter over an unbounded queue.
package messagingtest

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
)

// deliveryWait bounds a wait for a message that MUST arrive. It is a
// hang-breaker, not a latency assertion — an adapter over a real queue may
// deliver asynchronously, and no part of this contract promises speed — so it
// is generous and costs nothing when delivery is prompt.
//
// It is a var rather than a const only so this package's own negative tests
// can shrink it: they drive assertions against a broker that never delivers,
// and each would otherwise wait out the full hang-breaker to prove a failure
// it already knows how to force. Nothing outside this package can reach it.
var deliveryWait = 5 * time.Second

// silenceWait is how long a message that must NOT arrive is given to arrive
// anyway. Unlike deliveryWait it cannot be generous: the whole assertion is
// that nothing comes, so the test pays this in full every time it runs. It is
// therefore a genuine trade-off — long enough to catch a broker that
// mis-routes promptly, short enough to keep the suite usable.
var silenceWait = 150 * time.Millisecond

// tb is the slice of *testing.T the individual contract assertions use. It
// exists so the suite's OWN failure branches can be driven in-process by a
// recording fake: those branches only run against a broken implementation, and
// an assertion that is never executed is an assertion nobody has checked —
// an inverted comparison would silently pass every adapter it was meant to
// reject.
//
// Conformance still takes a real *testing.T, because subtests need one.
type tb interface {
	Helper()
	Cleanup(func())
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Skip(args ...any)
}

// Waits returns the suite's current time bounds, so a caller can widen them
// for a backend this suite's defaults would falsely reject.
//
// The defaults suit an in-process implementation. An adapter over a remote
// queue does not: a broker with a 20-second long-poll, or one behind a network
// hop slower than the absence windows, is CORRECT and would fail here for
// reasons that have nothing to do with the contract. The suite is published
// for exactly those adapters (NFR-3), so it must not hardcode the assumption
// that delivery is instant.
func Waits() WaitConfig { return currentWaits() }

// SetWaits widens or narrows the suite's time bounds for the current test
// binary, returning a function that restores them.
//
//	defer messagingtest.SetWaits(messagingtest.WaitConfig{
//		Delivery: 30 * time.Second, Silence: time.Second,
//	})()
//
// It is process-global and NOT safe to call from a parallel test — the suite
// reads these while running. Set it once, before Conformance.
func SetWaits(w WaitConfig) func() { return applyWaits(w) }

// WaitConfig is the suite's time bounds. A zero field keeps the current value.
type WaitConfig struct {
	// Delivery bounds a message that MUST arrive. A hang-breaker: raise it for
	// a slow backend; it costs nothing when delivery is prompt.
	Delivery time.Duration

	// Silence is how long a message that must NOT arrive is given to arrive
	// anyway. Every such assertion pays it in full, so it trades runtime
	// against the chance of missing a broker that mis-routes slowly.
	Silence time.Duration
}

// Factory builds a fresh, empty MessageBroker under test. It is called once
// per subtest, so implementations must return isolated brokers (for a shared
// backend: a wiped namespace or a unique topic prefix).
type Factory func(t *testing.T) messaging.MessageBroker

// Conformance runs the full MessageBroker contract against factory-built
// brokers. Adapter tests are one-liners:
//
//	func TestConformance(t *testing.T) {
//		messagingtest.Conformance(t, func(t *testing.T) messaging.MessageBroker {
//			return membroker.New()
//		})
//	}
func Conformance(t *testing.T, factory Factory) {
	t.Helper()

	if factory == nil {
		t.Fatal("Conformance: a nil Factory isn't allowed")
	}

	for name, test := range conformanceTests {
		t.Run(name, func(t *testing.T) { test(t, factory(t)) })
	}
}

// conformanceTests is the contract as a declarative table.
var conformanceTests = map[string]func(tb, messaging.MessageBroker){
	"SubscribeThenPublishDelivers": testSubscribeThenPublishDelivers,
	"PublishThenSubscribeDrains":   testPublishThenSubscribeDrains,
	"WildcardMatchesAnyKey":        testWildcardMatchesAnyKey,
	"KeyedMatchesOnlyItsKeys":      testKeyedMatchesOnlyItsKeys,
	"MultiKeySetMatchesAny":        testMultiKeySetMatchesAny,
	"NameMismatchNotDelivered":     testNameMismatchNotDelivered,
	"KeyedBeatsWildcard":           testKeyedBeatsWildcard,
	"KeylessGoesToWildcard":        testKeylessGoesToWildcard,
	"PointToPointSingleDelivery":   testPointToPointSingleDelivery,
	"AddKeyExtendsAndDrains":       testAddKeyExtendsAndDrains,
	"AddKeyEmptyRejected":          testAddKeyEmptyRejected,
	"UnsubscribeStopsDelivery":     testUnsubscribeStopsDelivery,
	"UnsubscribeIsIdempotent":      testUnsubscribeIsIdempotent,
}

// env builds an envelope whose payload identifies its own name and key, so a
// mis-routed delivery names itself in the failure.
func env(name, key string) messaging.Envelope {
	return messaging.Envelope{
		Payload: name + "/" + key, Name: name, CorrelationKey: key,
	}
}

// subscribe registers a subscription, failing the test on error, and
// unsubscribes at cleanup so one subtest's subscriber cannot claim the next
// one's messages on a shared backend.
func subscribe(
	t tb, b messaging.MessageBroker, name string, keys ...string,
) messaging.Subscription {
	t.Helper()

	s, err := b.Subscribe(context.Background(), name, keys...)
	if err != nil {
		t.Fatalf("Subscribe(%q, %v): %v", name, keys, err)
	}

	//nolint:errcheck // best-effort cleanup: the subtest's assertions are done
	t.Cleanup(func() { _ = s.Unsubscribe() })

	return s
}

// publish submits msg, failing the test on error.
func publish(t tb, b messaging.MessageBroker, msg messaging.Envelope) {
	t.Helper()

	if err := b.Publish(context.Background(), msg); err != nil {
		t.Fatalf("Publish(%q/%q): %v", msg.Name, msg.CorrelationKey, err)
	}
}

// expectEnvelope waits for one delivery and checks its name and key.
func expectEnvelope(
	t tb, s messaging.Subscription, name, key string,
) messaging.Envelope {
	t.Helper()

	select {
	case e := <-s.C():
		if e.Name != name || e.CorrelationKey != key {
			t.Fatalf("delivered %q/%q, want %q/%q",
				e.Name, e.CorrelationKey, name, key)
		}

		return e

	case <-time.After(deliveryWait):
		t.Fatalf("no delivery of %q/%q within %v", name, key, deliveryWait)
	}

	return messaging.Envelope{}
}

// expectSilence checks that nothing reaches s within silenceWait.
func expectSilence(t tb, s messaging.Subscription, why string) {
	t.Helper()

	select {
	case e := <-s.C():
		t.Fatalf("%s: unexpected delivery of %q/%q",
			why, e.Name, e.CorrelationKey)

	case <-time.After(silenceWait):
	}
}

func testSubscribeThenPublishDelivers(
	t tb, b messaging.MessageBroker,
) {
	s := subscribe(t, b, "order", "k1")

	publish(t, b, env("order", "k1"))
	expectEnvelope(t, s, "order", "k1")
}

// testPublishThenSubscribeDrains: a message with no subscriber is buffered and
// reaches the subscription that appears afterwards. Without this, every
// subscribe-before-publish race in the engine would silently lose a message.
func testPublishThenSubscribeDrains(t tb, b messaging.MessageBroker) {
	publish(t, b, env("order", "k1"))

	s := subscribe(t, b, "order", "k1")
	expectEnvelope(t, s, "order", "k1")
}

func testWildcardMatchesAnyKey(t tb, b messaging.MessageBroker) {
	s := subscribe(t, b, "order")

	publish(t, b, env("order", "whatever"))
	expectEnvelope(t, s, "order", "whatever")
}

func testKeyedMatchesOnlyItsKeys(t tb, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1")

	publish(t, b, env("order", "k2"))
	expectSilence(t, s, "a keyed subscription must not receive another key")
}

func testMultiKeySetMatchesAny(t tb, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1", "k2")

	publish(t, b, env("order", "k2"))
	expectEnvelope(t, s, "order", "k2")

	publish(t, b, env("order", "k1"))
	expectEnvelope(t, s, "order", "k1")
}

func testNameMismatchNotDelivered(t tb, b messaging.MessageBroker) {
	s := subscribe(t, b, "order")

	publish(t, b, env("invoice", "k1"))
	expectSilence(t, s, "a subscription must not receive another message name")
}

// testKeyedBeatsWildcard pins the most-specific rule the MessageBroker doc
// states outright: with both a keyed and a wildcard subscription live for one
// name, the keyed one takes the message.
func testKeyedBeatsWildcard(t tb, b messaging.MessageBroker) {
	wild := subscribe(t, b, "order")
	keyed := subscribe(t, b, "order", "k1")

	publish(t, b, env("order", "k1"))

	expectEnvelope(t, keyed, "order", "k1")
	expectSilence(t, wild, "the wildcard must not also receive a keyed message")
}

func testKeylessGoesToWildcard(t tb, b messaging.MessageBroker) {
	wild := subscribe(t, b, "order")
	keyed := subscribe(t, b, "order", "k1")

	publish(t, b, env("order", ""))

	expectEnvelope(t, wild, "order", "")
	expectSilence(t, keyed, "a keyless message must not reach a keyed subscriber")
}

// testPointToPointSingleDelivery: a message is consumed once, not fanned out.
// Two equally-specific subscribers means exactly one delivery in total —
// otherwise two instances would each act on the same incoming message.
func testPointToPointSingleDelivery(t tb, b messaging.MessageBroker) {
	s1 := subscribe(t, b, "order", "k1")
	s2 := subscribe(t, b, "order", "k1")

	publish(t, b, env("order", "k1"))

	// Both subscribers are watched CONCURRENTLY, for the whole delivery
	// window, then the window is allowed to close. Polling them in turn was
	// wrong in both directions: a broker slower than silenceWait scored 0 on
	// the first and 1 on the second and PASSED as if correct, and a broker
	// that fanned out slowly scored 1 the same way — so the assertion could
	// neither fail a fan-out nor pass a slow-but-correct broker for the right
	// reason.
	var delivered atomic.Int32

	var wg sync.WaitGroup

	for _, s := range []messaging.Subscription{s1, s2} {
		wg.Add(1)

		go func(sub messaging.Subscription) {
			defer wg.Done()

			select {
			case <-sub.C():
				delivered.Add(1)
			case <-time.After(deliveryWait):
			}
		}(s)
	}

	// One delivery is expected, so one wait ends early and the other burns the
	// full window. Wait for both: returning at the first would report a
	// fan-out that is still in flight as a single delivery.
	wg.Wait()

	if got := delivered.Load(); got != 1 {
		t.Fatalf("one message reached %d subscribers, want exactly 1", got)
	}
}

// testAddKeyExtendsAndDrains covers the lazy secondary key: a conversation
// that learns a key becomes reachable by it, INCLUDING for a message that was
// already buffered when the key was unknown.
func testAddKeyExtendsAndDrains(t tb, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1")

	// buffered while nothing claims k2
	publish(t, b, env("order", "k2"))
	expectSilence(t, s, "k2 is not yet in the key-set")

	if err := s.AddKey("k2"); err != nil {
		t.Fatalf("AddKey(k2): %v", err)
	}

	expectEnvelope(t, s, "order", "k2")
}

func testAddKeyEmptyRejected(t tb, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1")

	if err := s.AddKey(""); err == nil {
		t.Fatal("AddKey(\"\") must be rejected — an empty key is the wildcard, " +
			"so accepting it would silently widen the subscription")
	}
}

func testUnsubscribeStopsDelivery(t tb, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1")

	if err := s.Unsubscribe(); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	publish(t, b, env("order", "k1"))
	expectSilence(t, s, "an unsubscribed subscription must receive nothing")
}

func testUnsubscribeIsIdempotent(t tb, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1")

	if err := s.Unsubscribe(); err != nil {
		t.Fatalf("first Unsubscribe: %v", err)
	}

	if err := s.Unsubscribe(); err != nil {
		t.Fatalf("second Unsubscribe must be a no-op, got: %v", err)
	}
}

// currentWaits and applyWaits keep the tunables in one place, so the exported
// surface stays a value type and the package variables stay unexported.
func currentWaits() WaitConfig {
	return WaitConfig{Delivery: deliveryWait, Silence: silenceWait}
}

func applyWaits(w WaitConfig) func() {
	prev := currentWaits()

	if w.Delivery > 0 {
		deliveryWait = w.Delivery
	}

	if w.Silence > 0 {
		silenceWait = w.Silence
	}

	return func() { deliveryWait, silenceWait = prev.Delivery, prev.Silence }
}
