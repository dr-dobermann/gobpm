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

	ch, _ := b.Subscribe(ctx, "order", "k1")
	_ = b.Publish(ctx, env("order", "k1"))

	if e, ok := recv(ch); !ok || e.Name != "order" {
		t.Fatalf("live subscriber did not receive: %+v, %v", e, ok)
	}
}

func TestPublishThenSubscribeDrainsInbox(t *testing.T) {
	b := New()
	ctx := context.Background()

	_ = b.Publish(ctx, env("order", "k1")) // no subscriber -> inbox
	ch, _ := b.Subscribe(ctx, "order", "k1")

	if e, ok := recv(ch); !ok || e.CorrelationKey != "k1" {
		t.Fatalf("buffered message not drained on subscribe: %+v, %v", e, ok)
	}
}

func TestWildcardKeyMatches(t *testing.T) {
	b := New()
	ctx := context.Background()

	ch, _ := b.Subscribe(ctx, "order", "") // empty key = wildcard
	_ = b.Publish(ctx, env("order", "anything"))

	if _, ok := recv(ch); !ok {
		t.Fatal("wildcard-key subscription should match any key")
	}
}

func TestNameMismatchGoesToInbox(t *testing.T) {
	b := New()
	ctx := context.Background()

	ship, _ := b.Subscribe(ctx, "shipment", "")
	_ = b.Publish(ctx, env("order", "k")) // name mismatch -> inbox

	if _, ok := recv(ship); ok {
		t.Fatal("shipment subscriber received an order message")
	}

	order, _ := b.Subscribe(ctx, "order", "")
	if _, ok := recv(order); !ok {
		t.Fatal("order message should have been buffered then drained")
	}
}

func TestKeyMismatchNotDelivered(t *testing.T) {
	b := New()
	ctx := context.Background()

	ch, _ := b.Subscribe(ctx, "order", "k1")
	_ = b.Publish(ctx, env("order", "k2")) // key mismatch

	if _, ok := recv(ch); ok {
		t.Fatal("subscriber received a non-matching key")
	}
}

func TestInboxBoundedDropsOldest(t *testing.T) {
	lg := &capLogger{}
	b := New(WithMaxInbox(2), WithLogger(lg))
	ctx := context.Background()

	for i := range 3 { // no subscriber -> all to inbox; cap 2 drops oldest
		_ = b.Publish(ctx, env("m", strconv.Itoa(i)))
	}

	ch, _ := b.Subscribe(ctx, "m", "")
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

	ch, _ := b.Subscribe(ctx, "m", "")
	if got := drain(ch); len(got) != subBuffer {
		t.Fatalf("drained %d, want %d (channel buffer)", len(got), subBuffer)
	}

	// the overflow that didn't fit the channel stayed in the inbox.
	ch2, _ := b.Subscribe(ctx, "m", "")
	if got := drain(ch2); len(got) != 2 {
		t.Fatalf("remaining inbox = %d, want 2", len(got))
	}
}

func TestPublishFallsToInboxWhenSubscriberFull(t *testing.T) {
	b := New()
	ctx := context.Background()

	ch, _ := b.Subscribe(ctx, "m", "")

	for range subBuffer { // fill the subscriber's channel buffer
		_ = b.Publish(ctx, env("m", "x"))
	}

	// one more match: the subscriber's channel is full -> falls to the inbox.
	_ = b.Publish(ctx, env("m", "overflow"))

	if got := drain(ch); len(got) != subBuffer {
		t.Fatalf("subscriber drained %d, want %d", len(got), subBuffer)
	}

	ch2, _ := b.Subscribe(ctx, "m", "")
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

	ch, _ := b.Subscribe(ctx, "m", "")
	if got := drain(ch); len(got) != 5 {
		t.Fatalf("inbox kept %d, want 5 (cap disabled)", len(got))
	}
}
