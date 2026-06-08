// Package membroker provides the engine's default MessageBroker: an in-memory
// inbox + correlation router. Undelivered envelopes are buffered in a bounded
// inbox that drops the oldest and warns once past the cap, so uncorrelated
// messages cannot grow unbounded (the bounded-in-memory-defaults principle,
// ADR-002 §4.2).
package membroker

import (
	"context"
	"log/slog"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

const (
	// DefaultMaxInbox is the default cap on buffered undelivered envelopes.
	DefaultMaxInbox = 1024
	// subBuffer is the per-subscription channel buffer.
	subBuffer = 16
)

// Broker is an in-memory messaging.MessageBroker.
type Broker struct {
	logger   observability.Logger
	inbox    []messaging.Envelope
	subs     []subscription
	maxInbox int
	mu       sync.Mutex
	warnOnce sync.Once
}

type subscription struct {
	ch   chan messaging.Envelope
	name string
	key  string
}

func (s subscription) matches(e messaging.Envelope) bool {
	return s.name == e.Name && (s.key == "" || s.key == e.CorrelationKey)
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

// Publish delivers the message to a matching subscriber, or buffers it in the
// bounded inbox when none is ready.
func (b *Broker) Publish(_ context.Context, msg messaging.Envelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, s := range b.subs {
		if s.matches(msg) {
			select {
			case s.ch <- msg:
				return nil
			default:
			}
		}
	}

	b.inbox = append(b.inbox, msg)
	b.evictInboxLocked()

	return nil
}

// Subscribe returns a channel of envelopes matching name and correlationKey,
// draining any already-buffered matches first.
func (b *Broker) Subscribe(
	_ context.Context, name, correlationKey string,
) (<-chan messaging.Envelope, error) {
	ch := make(chan messaging.Envelope, subBuffer)
	sub := subscription{ch: ch, name: name, key: correlationKey}

	b.mu.Lock()
	defer b.mu.Unlock()

	kept := b.inbox[:0]
	for _, e := range b.inbox {
		if sub.matches(e) {
			select {
			case ch <- e:
				continue
			default:
			}
		}

		kept = append(kept, e)
	}

	b.inbox = kept
	b.subs = append(b.subs, sub)

	return ch, nil
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

var _ messaging.MessageBroker = (*Broker)(nil)
