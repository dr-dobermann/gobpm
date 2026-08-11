package messagingtest

import (
	"context"
	"errors"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
)

// ErrInjected is returned by every injected failure in this package, so a test
// can assert that the error it sees is the one it asked for rather than a real
// broker fault that happens to look similar.
var ErrInjected = errors.New("messagingtest: injected failure")

// FailingBroker is a messaging.MessageBroker whose operations fail on demand.
//
// It exists because the real broker cannot be made to fail: engine paths that
// react to a broker refusing a key — a joining subscriber whose key cannot be
// applied, a key learned while a subscription was being created — are
// unreachable through membroker, and were therefore untested until a lost
// correlation key reached production behavior (#320).
//
// It is deliberately NOT a full broker: it routes nothing and delivers
// nothing. Tests that need real delivery use membroker; tests that need a
// refusal use this. Mixing the two concerns into one double would make every
// caller carry the configuration of the half it does not use.
//
// CONFIGURATION IS NOT CONCURRENCY-SAFE. Every exported field below is read on
// the calling component's goroutine, when it calls the broker — set them all
// before handing the broker to whatever will use it, and do not change them
// afterwards. There are deliberately no setters: a test that needs to change a
// broker's behavior mid-run builds a second broker, and mutex-guarded knobs
// would buy an API nobody needs at the cost of hiding that fact.
type FailingBroker struct {
	// SubscribeErr, when set, makes Subscribe fail.
	SubscribeErr error
	// AddKeyErr, when set, makes AddKey fail on every subscription this
	// broker hands out.
	AddKeyErr error
	// OnSubscribe, when set, runs INSIDE Subscribe, before the subscription
	// is handed back. It opens the window a real broker's Subscribe occupies
	// — the window in which a correlation key can be learned with no
	// subscription to apply it to, which is where #320's key was lost. A
	// caller cannot reach that window from outside, because Subscribe
	// returning is what closes it.
	OnSubscribe func()
	// OnAddKey, when set, runs INSIDE AddKey on every subscription this broker
	// hands out, before the key is recorded or refused. A test that blocks in
	// it holds the caller inside the broker call for as long as it likes,
	// which is how a timing property — "the engine is not holding its lock
	// while it talks to me" — becomes assertable: the real broker returns too
	// fast to observe, and sleeping instead only makes the assertion flaky.
	OnAddKey func(key string)
	// OnUnsubscribe, when set, runs INSIDE Unsubscribe on every subscription
	// this broker hands out, before the teardown. Same purpose as OnAddKey: a
	// test that blocks in it holds the caller inside the broker call, which is
	// how "the engine is not holding its lock while it talks to me" becomes
	// assertable for a teardown path.
	OnUnsubscribe func()
	subs          []*FailingSubscription
	// AddKeyAfter lets the first N AddKey calls succeed before AddKeyErr
	// starts being returned — for the path where a subscription is built
	// with some keys and then refuses a later one. Ignored when AddKeyErr
	// is nil.
	AddKeyAfter int
	mu          sync.Mutex
}

// NewFailingBroker builds a broker whose AddKey always fails. The zero value
// of FailingBroker fails nothing, which is the right default for a test that
// only wants to observe calls.
func NewFailingBroker() *FailingBroker {
	return &FailingBroker{AddKeyErr: ErrInjected}
}

// Publish accepts and discards: this broker routes nothing (see the type
// comment). It never fails, so a test cannot mistake a publish error for the
// refusal it is exercising.
func (b *FailingBroker) Publish(_ context.Context, _ messaging.Envelope) error {
	return nil
}

// Subscribe hands out a subscription that fails as configured.
func (b *FailingBroker) Subscribe(
	_ context.Context, name string, keys ...string,
) (messaging.Subscription, error) {
	if b.SubscribeErr != nil {
		return nil, b.SubscribeErr
	}

	if b.OnSubscribe != nil {
		b.OnSubscribe()
	}

	s := &FailingSubscription{
		Name:     name,
		Keys:     append([]string(nil), keys...),
		ch:       make(chan messaging.Envelope),
		addErr:   b.AddKeyErr,
		addAfter: b.AddKeyAfter,
		onAddKey: b.OnAddKey,
		onUnsub:  b.OnUnsubscribe,
	}

	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()

	return s, nil
}

// Subscriptions returns the subscriptions handed out so far, so a test can
// assert which keys a component subscribed with.
func (b *FailingBroker) Subscriptions() []*FailingSubscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	return append([]*FailingSubscription(nil), b.subs...)
}

// FailingSubscription records the keys it was created with and the keys added
// afterwards, and refuses AddKey as its broker was configured to.
type FailingSubscription struct {
	// Name and Keys are the subscription's creation parameters, exposed so a
	// test can assert what a component subscribed with — the question at the
	// heart of #320.
	Name string
	Keys []string

	ch       chan messaging.Envelope
	addErr   error
	onAddKey func(key string)
	onUnsub  func()
	added    []string
	addAfter int
	closed   bool
	mu       sync.Mutex
}

// C is the (never-delivering) envelope channel.
func (s *FailingSubscription) C() <-chan messaging.Envelope { return s.ch }

// AddKey records the key, or fails as configured.
func (s *FailingSubscription) AddKey(key string) error {
	// Before the lock: a test may block here for as long as it likes, and a
	// blocked hook must not also freeze Added() or Unsubscribe() — the very
	// calls the test uses to observe what it is blocking.
	if s.onAddKey != nil {
		s.onAddKey(key)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.addErr != nil && len(s.added) >= s.addAfter {
		return s.addErr
	}

	s.added = append(s.added, key)

	return nil
}

// Added returns the keys accepted after creation.
func (s *FailingSubscription) Added() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.added...)
}

// Unsubscribed reports whether the subscription has been torn down. It is the
// observable behind "the engine gave the subscription back instead of leaving
// it partly keyed" — an assertion on the error alone would pass for code that
// reports the failure and keeps the subscription, which is the bug (FIX-041
// §4.3).
func (s *FailingSubscription) Unsubscribed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.closed
}

// Unsubscribe closes the channel once; a second call is a no-op, as the
// port requires.
func (s *FailingSubscription) Unsubscribe() error {
	// Before the lock, for the same reason as AddKey's hook: a test may block
	// here, and a blocked hook must not freeze Unsubscribed() — the call the
	// test uses to observe what it is blocking.
	if s.onUnsub != nil {
		s.onUnsub()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.closed {
		close(s.ch)
		s.closed = true
	}

	return nil
}

// interface checks
var (
	_ messaging.MessageBroker = (*FailingBroker)(nil)
	_ messaging.Subscription  = (*FailingSubscription)(nil)
)
