package membroker

import (
	"context"
	"strconv"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
)

type capLogger struct{ warns int }

func (l *capLogger) Debug(string, ...any) {}
func (l *capLogger) Info(string, ...any)  {}
func (l *capLogger) Warn(string, ...any)  { l.warns++ }
func (l *capLogger) Error(string, ...any) {}

func env(name, key string) messaging.Envelope {
	return messaging.Envelope{Payload: name + key, Name: name, CorrelationKey: key}
}

// subscribe registers a subscription and fails the test on error.
func subscribe(
	t *testing.T, b *Broker, name string, keys ...string,
) messaging.Subscription {
	t.Helper()

	s, err := b.Subscribe(context.Background(), name, keys...)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	return s
}

func recv(ch <-chan messaging.Envelope) (messaging.Envelope, bool) {
	select {
	case e := <-ch:
		return e, true
	default:
		return messaging.Envelope{}, false
	}
}

func drain(ch <-chan messaging.Envelope) []messaging.Envelope {
	var out []messaging.Envelope

	for {
		e, ok := recv(ch)
		if !ok {
			return out
		}

		out = append(out, e)
	}
}

func TestSubscribeThenPublishDelivers(t *testing.T) {
	b := New()
	ctx := context.Background()

	ch := subscribe(t, b, "order", "k1").C()
	_ = b.Publish(ctx, env("order", "k1"))

	if e, ok := recv(ch); !ok || e.Name != "order" {
		t.Fatalf("live subscriber did not receive: %+v, %v", e, ok)
	}
}

func TestPublishThenSubscribeDrainsInbox(t *testing.T) {
	b := New()
	ctx := context.Background()

	_ = b.Publish(ctx, env("order", "k1")) // no subscriber -> inbox
	ch := subscribe(t, b, "order", "k1").C()

	if e, ok := recv(ch); !ok || e.CorrelationKey != "k1" {
		t.Fatalf("buffered message not drained on subscribe: %+v, %v", e, ok)
	}
}

func TestWildcardKeyMatches(t *testing.T) {
	b := New()
	ctx := context.Background()

	ch := subscribe(t, b, "order").C() // no keys = wildcard
	_ = b.Publish(ctx, env("order", "anything"))

	if _, ok := recv(ch); !ok {
		t.Fatal("wildcard subscription should match any key")
	}
}

func TestNameMismatchGoesToInbox(t *testing.T) {
	b := New()
	ctx := context.Background()

	ship := subscribe(t, b, "shipment").C()
	_ = b.Publish(ctx, env("order", "k")) // name mismatch -> inbox

	if _, ok := recv(ship); ok {
		t.Fatal("shipment subscriber received an order message")
	}

	order := subscribe(t, b, "order").C()
	if _, ok := recv(order); !ok {
		t.Fatal("order message should have been buffered then drained")
	}
}

func TestKeyMismatchNotDelivered(t *testing.T) {
	b := New()
	ctx := context.Background()

	ch := subscribe(t, b, "order", "k1").C()
	_ = b.Publish(ctx, env("order", "k2")) // key mismatch

	if _, ok := recv(ch); ok {
		t.Fatal("subscriber received a non-matching key")
	}
}

func TestKeyedBeatsWildcard(t *testing.T) {
	b := New()
	ctx := context.Background()

	wild := subscribe(t, b, "order").C()
	keyed := subscribe(t, b, "order", "k1").C()

	_ = b.Publish(ctx, env("order", "k1"))

	if _, ok := recv(keyed); !ok {
		t.Fatal("keyed subscriber should win the most-specific match")
	}

	if _, ok := recv(wild); ok {
		t.Fatal("wildcard subscriber must not receive a message claimed by a keyed one")
	}
}

func TestKeyedFullClaimsBuffersNotWildcard(t *testing.T) {
	b := New()
	ctx := context.Background()

	keyed := subscribe(t, b, "order", "k1")
	for range subBuffer { // fill the keyed subscriber's channel
		_ = b.Publish(ctx, env("order", "k1"))
	}

	wild := subscribe(t, b, "order").C()

	// one more for the keyed key: its channel is full, so it is buffered
	// (claimed by that conversation), never handed to the wildcard subscriber.
	_ = b.Publish(ctx, env("order", "k1"))

	if _, ok := recv(wild); ok {
		t.Fatal("wildcard received a message claimed by a full keyed subscriber")
	}

	// a fresh keyed subscriber drains the buffered, claimed message.
	again := subscribe(t, b, "order", "k1").C()
	if got := drain(again); len(got) != 1 {
		t.Fatalf("claimed message not buffered for the keyed key: got %d, want 1", len(got))
	}

	_ = keyed // keep the first subscriber referenced
}

func TestNoKeyMessageGoesToWildcard(t *testing.T) {
	b := New()
	ctx := context.Background()

	keyed := subscribe(t, b, "order", "k1").C()
	wild := subscribe(t, b, "order").C()

	_ = b.Publish(ctx, env("order", "")) // no key

	if _, ok := recv(wild); !ok {
		t.Fatal("a no-key message should go to the wildcard subscriber")
	}

	if _, ok := recv(keyed); ok {
		t.Fatal("a no-key message must not reach a keyed subscriber")
	}
}

func TestMultiKeySetMatchesAny(t *testing.T) {
	b := New()
	ctx := context.Background()

	ch := subscribe(t, b, "order", "k1", "k2").C()
	_ = b.Publish(ctx, env("order", "k1"))
	_ = b.Publish(ctx, env("order", "k2"))

	if got := drain(ch); len(got) != 2 {
		t.Fatalf("multi-key subscriber received %d, want 2", len(got))
	}
}

func TestAddKeyExtendsAndDrains(t *testing.T) {
	b := New()
	ctx := context.Background()

	s := subscribe(t, b, "order", "k1")
	_ = b.Publish(ctx, env("order", "k2")) // not yet matched -> inbox

	if _, ok := recv(s.C()); ok {
		t.Fatal("k2 message delivered before the key was associated")
	}

	if err := s.AddKey("k2"); err != nil {
		t.Fatalf("AddKey: %v", err)
	}

	if e, ok := recv(s.C()); !ok || e.CorrelationKey != "k2" {
		t.Fatalf("buffered k2 message not drained after AddKey: %+v, %v", e, ok)
	}
}

func TestAddKeyEmptyRejected(t *testing.T) {
	b := New()

	if err := subscribe(t, b, "order", "k1").AddKey(""); err == nil {
		t.Fatal("AddKey(\"\") should be rejected")
	}
}

func TestPointToPointSingleDelivery(t *testing.T) {
	b := New()
	ctx := context.Background()

	a := subscribe(t, b, "order").C()
	c := subscribe(t, b, "order").C()

	_ = b.Publish(ctx, env("order", "x"))

	if got := len(drain(a)) + len(drain(c)); got != 1 {
		t.Fatalf("point-to-point delivery sent to %d subscribers, want 1", got)
	}
}

func TestInboxBoundedDropsOldest(t *testing.T) {
	lg := &capLogger{}
	b := New(WithMaxInbox(2), WithLogger(lg))
	ctx := context.Background()

	for i := range 3 { // no subscriber -> all to inbox; cap 2 drops oldest
		_ = b.Publish(ctx, env("m", strconv.Itoa(i)))
	}

	ch := subscribe(t, b, "m").C()
	if got := drain(ch); len(got) != 2 {
		t.Fatalf("inbox kept %d, want 2", len(got))
	}

	if lg.warns != 1 {
		t.Fatalf("warns = %d, want exactly 1", lg.warns)
	}
}

func TestSubscribeDrainStopsWhenChannelFull(t *testing.T) {
	b := New() // default inbox cap is large enough to hold the overflow
	ctx := context.Background()

	total := subBuffer + 2
	for i := range total { // no subscriber yet -> all buffered in the inbox
		_ = b.Publish(ctx, env("m", strconv.Itoa(i)))
	}

	ch := subscribe(t, b, "m").C()
	if got := drain(ch); len(got) != subBuffer {
		t.Fatalf("drained %d, want %d (channel buffer)", len(got), subBuffer)
	}

	// the overflow that didn't fit the channel stayed in the inbox.
	ch2 := subscribe(t, b, "m").C()
	if got := drain(ch2); len(got) != 2 {
		t.Fatalf("remaining inbox = %d, want 2", len(got))
	}
}

func TestPublishFallsToInboxWhenSubscriberFull(t *testing.T) {
	b := New()
	ctx := context.Background()

	ch := subscribe(t, b, "m").C()

	for range subBuffer { // fill the subscriber's channel buffer
		_ = b.Publish(ctx, env("m", "x"))
	}

	// one more match: the subscriber's channel is full -> falls to the inbox.
	_ = b.Publish(ctx, env("m", "overflow"))

	if got := drain(ch); len(got) != subBuffer {
		t.Fatalf("subscriber drained %d, want %d", len(got), subBuffer)
	}

	ch2 := subscribe(t, b, "m").C()
	if got := drain(ch2); len(got) != 1 {
		t.Fatalf("inbox overflow = %d, want 1", len(got))
	}
}

func TestMaxInboxDisabled(t *testing.T) {
	b := New(WithMaxInbox(0))
	ctx := context.Background()

	for i := range 5 {
		_ = b.Publish(ctx, env("m", strconv.Itoa(i)))
	}

	ch := subscribe(t, b, "m").C()
	if got := drain(ch); len(got) != 5 {
		t.Fatalf("inbox kept %d, want 5 (cap disabled)", len(got))
	}
}

// TestUnsubscribeStopsDelivery verifies that after Unsubscribe the broker no
// longer routes to the dropped subscription: a later publish must reach a fresh
// subscriber on the same name, not get swallowed into the dead one's channel.
// This backs the EventHub teardown a superseded process version relies on.
func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := New()
	ctx := context.Background()

	first := subscribe(t, b, "order") // wildcard
	if err := first.Unsubscribe(); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}

	// the published message must not land in the dropped subscription's channel.
	_ = b.Publish(ctx, env("order", "k"))
	if e, ok := recv(first.C()); ok {
		t.Fatalf("dropped subscription still received: %+v", e)
	}

	// a fresh subscriber drains it from the inbox — it was buffered, not stolen.
	second := subscribe(t, b, "order")
	if e, ok := recv(second.C()); !ok || e.Name != "order" {
		t.Fatalf("replacement subscriber did not receive: %+v, %v", e, ok)
	}
}

// TestUnsubscribeIsIdempotent verifies a second Unsubscribe (the synchronous Stop
// path plus the service goroutine's deferred call both fire) is a harmless no-op.
func TestUnsubscribeIsIdempotent(t *testing.T) {
	b := New()

	s := subscribe(t, b, "order")
	if err := s.Unsubscribe(); err != nil {
		t.Fatalf("first unsubscribe: %v", err)
	}

	if err := s.Unsubscribe(); err != nil {
		t.Fatalf("second unsubscribe must be a no-op: %v", err)
	}
}
