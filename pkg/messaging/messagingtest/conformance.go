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
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
)

// deliveryWait bounds a wait for a message that MUST arrive. It is a
// hang-breaker, not a latency assertion — an adapter over a real queue may
// deliver asynchronously, and no part of this contract promises speed — so it
// is generous and costs nothing when delivery is prompt.
const deliveryWait = 5 * time.Second

// silenceWait is how long a message that must NOT arrive is given to arrive
// anyway. Unlike deliveryWait it cannot be generous: the whole assertion is
// that nothing comes, so the test pays this in full every time it runs. It is
// therefore a genuine trade-off — long enough to catch a broker that
// mis-routes promptly, short enough to keep the suite usable.
const silenceWait = 150 * time.Millisecond

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
var conformanceTests = map[string]func(*testing.T, messaging.MessageBroker){
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
	t *testing.T, b messaging.MessageBroker, name string, keys ...string,
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
func publish(t *testing.T, b messaging.MessageBroker, msg messaging.Envelope) {
	t.Helper()

	if err := b.Publish(context.Background(), msg); err != nil {
		t.Fatalf("Publish(%q/%q): %v", msg.Name, msg.CorrelationKey, err)
	}
}

// expectEnvelope waits for one delivery and checks its name and key.
func expectEnvelope(
	t *testing.T, s messaging.Subscription, name, key string,
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
func expectSilence(t *testing.T, s messaging.Subscription, why string) {
	t.Helper()

	select {
	case e := <-s.C():
		t.Fatalf("%s: unexpected delivery of %q/%q",
			why, e.Name, e.CorrelationKey)

	case <-time.After(silenceWait):
	}
}

func testSubscribeThenPublishDelivers(
	t *testing.T, b messaging.MessageBroker,
) {
	s := subscribe(t, b, "order", "k1")

	publish(t, b, env("order", "k1"))
	expectEnvelope(t, s, "order", "k1")
}

// testPublishThenSubscribeDrains: a message with no subscriber is buffered and
// reaches the subscription that appears afterwards. Without this, every
// subscribe-before-publish race in the engine would silently lose a message.
func testPublishThenSubscribeDrains(t *testing.T, b messaging.MessageBroker) {
	publish(t, b, env("order", "k1"))

	s := subscribe(t, b, "order", "k1")
	expectEnvelope(t, s, "order", "k1")
}

func testWildcardMatchesAnyKey(t *testing.T, b messaging.MessageBroker) {
	s := subscribe(t, b, "order")

	publish(t, b, env("order", "whatever"))
	expectEnvelope(t, s, "order", "whatever")
}

func testKeyedMatchesOnlyItsKeys(t *testing.T, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1")

	publish(t, b, env("order", "k2"))
	expectSilence(t, s, "a keyed subscription must not receive another key")
}

func testMultiKeySetMatchesAny(t *testing.T, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1", "k2")

	publish(t, b, env("order", "k2"))
	expectEnvelope(t, s, "order", "k2")

	publish(t, b, env("order", "k1"))
	expectEnvelope(t, s, "order", "k1")
}

func testNameMismatchNotDelivered(t *testing.T, b messaging.MessageBroker) {
	s := subscribe(t, b, "order")

	publish(t, b, env("invoice", "k1"))
	expectSilence(t, s, "a subscription must not receive another message name")
}

// testKeyedBeatsWildcard pins the most-specific rule the MessageBroker doc
// states outright: with both a keyed and a wildcard subscription live for one
// name, the keyed one takes the message.
func testKeyedBeatsWildcard(t *testing.T, b messaging.MessageBroker) {
	wild := subscribe(t, b, "order")
	keyed := subscribe(t, b, "order", "k1")

	publish(t, b, env("order", "k1"))

	expectEnvelope(t, keyed, "order", "k1")
	expectSilence(t, wild, "the wildcard must not also receive a keyed message")
}

func testKeylessGoesToWildcard(t *testing.T, b messaging.MessageBroker) {
	wild := subscribe(t, b, "order")
	keyed := subscribe(t, b, "order", "k1")

	publish(t, b, env("order", ""))

	expectEnvelope(t, wild, "order", "")
	expectSilence(t, keyed, "a keyless message must not reach a keyed subscriber")
}

// testPointToPointSingleDelivery: a message is consumed once, not fanned out.
// Two equally-specific subscribers means exactly one delivery in total —
// otherwise two instances would each act on the same incoming message.
func testPointToPointSingleDelivery(t *testing.T, b messaging.MessageBroker) {
	s1 := subscribe(t, b, "order", "k1")
	s2 := subscribe(t, b, "order", "k1")

	publish(t, b, env("order", "k1"))

	delivered := 0

	for _, s := range []messaging.Subscription{s1, s2} {
		select {
		case <-s.C():
			delivered++

		case <-time.After(silenceWait):
		}
	}

	if delivered != 1 {
		t.Fatalf("one message reached %d subscribers, want exactly 1", delivered)
	}
}

// testAddKeyExtendsAndDrains covers the lazy secondary key: a conversation
// that learns a key becomes reachable by it, INCLUDING for a message that was
// already buffered when the key was unknown.
func testAddKeyExtendsAndDrains(t *testing.T, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1")

	// buffered while nothing claims k2
	publish(t, b, env("order", "k2"))
	expectSilence(t, s, "k2 is not yet in the key-set")

	if err := s.AddKey("k2"); err != nil {
		t.Fatalf("AddKey(k2): %v", err)
	}

	expectEnvelope(t, s, "order", "k2")
}

func testAddKeyEmptyRejected(t *testing.T, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1")

	if err := s.AddKey(""); err == nil {
		t.Fatal("AddKey(\"\") must be rejected — an empty key is the wildcard, " +
			"so accepting it would silently widen the subscription")
	}
}

func testUnsubscribeStopsDelivery(t *testing.T, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1")

	if err := s.Unsubscribe(); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	publish(t, b, env("order", "k1"))
	expectSilence(t, s, "an unsubscribed subscription must receive nothing")
}

func testUnsubscribeIsIdempotent(t *testing.T, b messaging.MessageBroker) {
	s := subscribe(t, b, "order", "k1")

	if err := s.Unsubscribe(); err != nil {
		t.Fatalf("first Unsubscribe: %v", err)
	}

	if err := s.Unsubscribe(); err != nil {
		t.Fatalf("second Unsubscribe must be a no-op, got: %v", err)
	}
}
