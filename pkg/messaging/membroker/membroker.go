// Package membroker provides the engine's default MessageBroker: an in-memory
// inbox + correlation router. Undelivered envelopes are buffered in a bounded
// inbox that drops the oldest and warns once past the cap, so uncorrelated
// messages cannot grow unbounded (the bounded-in-memory-defaults principle,
// ADR-002 §4.2).
//
// Delivery is most-specific (SRD-017, ADR-016 §2.3): a keyed subscription — one
// whose key-set contains the message's correlation key — is preferred over a
// wildcard subscription, so a follow-up message routes to the conversation that
// owns it rather than to the engine-level instance-starter. A subscription's
// key-set can grow at runtime via AddKey (lazy secondary-key association).
package membroker

import (
	"context"
	"log/slog"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

const (
	// DefaultMaxInbox is the default cap on buffered undelivered envelopes.
	DefaultMaxInbox = 1024
	// subBuffer is the per-subscription channel buffer.
	subBuffer = 16

	errorClass = "MEMBROKER_ERROR"
)

// Broker is an in-memory messaging.MessageBroker.
type Broker struct {
	logger   observability.Logger
	inbox    []messaging.Envelope
	subs     []*subscription
	maxInbox int
	mu       sync.Mutex
	warnOnce sync.Once
}

// subscription is a live registration. An empty keys set is a wildcard that
// matches any correlation key for the name; a non-empty set matches only a
// message whose CorrelationKey is in it.
type subscription struct {
	ch   chan messaging.Envelope
	keys map[string]struct{}
	name string
}

// keyed reports whether s restricts delivery to its key-set (vs wildcard).
func (s *subscription) keyed() bool { return len(s.keys) > 0 }

// matches reports whether s should receive e: same name, and either a wildcard
// subscription or a key-set containing e's (non-empty) correlation key.
func (s *subscription) matches(e messaging.Envelope) bool {
	if s.name != e.Name {
		return false
	}

	if !s.keyed() {
		return true
	}

	if e.CorrelationKey == "" {
		return false
	}

	_, ok := s.keys[e.CorrelationKey]

	return ok
}

// brokerSub is the messaging.Subscription handle bound to a broker and its
// subscription.
type brokerSub struct {
	b   *Broker
	sub *subscription
}

// C returns the channel of envelopes matching the subscription.
func (h brokerSub) C() <-chan messaging.Envelope { return h.sub.ch }

// AddKey adds key to the subscription's key-set and drains any already-buffered
// envelopes that now match (lazy secondary-key association, SRD-017). An empty
// key is rejected.
func (h brokerSub) AddKey(key string) error {
	if key == "" {
		return errs.New(
			errs.M("membroker.AddKey: an empty correlation key isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	h.b.mu.Lock()
	defer h.b.mu.Unlock()

	h.sub.keys[key] = struct{}{}
	h.b.drainInboxLocked(h.sub)

	return nil
}

// Option configures a Broker.
type Option func(*Broker)

// WithMaxInbox sets the cap on buffered undelivered envelopes; n <= 0 disables it.
func WithMaxInbox(n int) Option { return func(b *Broker) { b.maxInbox = n } }

// WithLogger sets the logger used for the inbox-eviction warning.
func WithLogger(l observability.Logger) Option { return func(b *Broker) { b.logger = l } }

// New returns an in-memory Broker with the default inbox cap and slog.Default()
// logger, overridden by opts.
func New(opts ...Option) *Broker {
	b := &Broker{
		logger:   slog.Default(),
		maxInbox: DefaultMaxInbox,
	}

	for _, o := range opts {
		o(b)
	}

	return b
}

// Publish delivers the message most-specifically: to a keyed subscriber whose
// key-set contains the message key if one exists, else to a wildcard subscriber,
// else it is buffered in the bounded inbox. A message claimed by a keyed
// subscriber whose channel is momentarily full is buffered (for that
// subscriber's later drain), never handed to a wildcard subscriber.
func (b *Broker) Publish(_ context.Context, msg messaging.Envelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	keyedMatched := false

	for _, s := range b.subs {
		if !s.keyed() || !s.matches(msg) {
			continue
		}

		keyedMatched = true

		if trySend(s.ch, msg) {
			return nil
		}
	}

	if keyedMatched {
		b.bufferLocked(msg)

		return nil
	}

	for _, s := range b.subs {
		if s.keyed() || !s.matches(msg) {
			continue
		}

		if trySend(s.ch, msg) {
			return nil
		}
	}

	b.bufferLocked(msg)

	return nil
}

// Subscribe registers interest in messages named name. With no keys (or only
// empty keys) the subscription is a wildcard; otherwise it matches a message
// whose CorrelationKey is in the key-set. Already-buffered matches are drained
// to the new subscription first.
func (b *Broker) Subscribe(
	_ context.Context, name string, keys ...string,
) (messaging.Subscription, error) {
	sub := &subscription{
		ch:   make(chan messaging.Envelope, subBuffer),
		keys: keySet(keys),
		name: name,
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.drainInboxLocked(sub)
	b.subs = append(b.subs, sub)

	return brokerSub{b: b, sub: sub}, nil
}

// drainInboxLocked delivers buffered envelopes matching sub to its channel,
// keeping the rest in the inbox. Caller holds mu.
func (b *Broker) drainInboxLocked(sub *subscription) {
	kept := b.inbox[:0]

	for _, e := range b.inbox {
		if sub.matches(e) && trySend(sub.ch, e) {
			continue
		}

		kept = append(kept, e)
	}

	b.inbox = kept
}

// bufferLocked appends to the bounded inbox, evicting the oldest past the cap.
// Caller holds mu.
func (b *Broker) bufferLocked(msg messaging.Envelope) {
	b.inbox = append(b.inbox, msg)
	b.evictInboxLocked()
}

// evictInboxLocked drops oldest buffered envelopes past the cap. Caller holds mu.
func (b *Broker) evictInboxLocked() {
	if b.maxInbox <= 0 {
		return
	}

	for len(b.inbox) > b.maxInbox {
		b.inbox = b.inbox[1:]

		b.warnOnce.Do(func() {
			b.logger.Warn("membroker: inbox cap reached, dropping oldest",
				"cap", b.maxInbox)
		})
	}
}

// trySend delivers e to ch without blocking; it reports whether it succeeded.
func trySend(ch chan messaging.Envelope, e messaging.Envelope) bool {
	select {
	case ch <- e:
		return true
	default:
		return false
	}
}

// keySet builds a key-set from keys, dropping empty strings; an all-empty or
// zero input yields an empty set (a wildcard).
func keySet(keys []string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))

	for _, k := range keys {
		if k != "" {
			set[k] = struct{}{}
		}
	}

	return set
}

var (
	_ messaging.MessageBroker = (*Broker)(nil)
	_ messaging.Subscription  = brokerSub{}
)
