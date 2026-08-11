package waiters_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// keyProc is an EventProcessor that declares correlation keys — the structural
// shape the waiter reads to build and grow its broker subscription (SRD-017
// §4.3).
type keyProc struct {
	id   string
	keys []string
}

func (p *keyProc) ID() string { return p.id }

func (*keyProc) ProcessEvent(context.Context, flow.EventDefinition) error {
	return nil
}

func (p *keyProc) CorrelationKeys() []string { return p.keys }

// recSub records every key offered to it, and can refuse the one named in
// refuse — so a test can tell "the waiter added nothing" from "the broker took
// it", and drive the failing arm.
type recSub struct {
	ch     chan messaging.Envelope
	refuse string
	added  []string
	m      sync.Mutex
}

func (s *recSub) C() <-chan messaging.Envelope { return s.ch }

func (s *recSub) AddKey(key string) error {
	s.m.Lock()
	defer s.m.Unlock()

	if key == s.refuse {
		return fmt.Errorf("broker refused %q", key)
	}

	s.added = append(s.added, key)

	return nil
}

func (*recSub) Unsubscribe() error { return nil }

// keys returns the keys offered so far.
func (s *recSub) keys() []string {
	s.m.Lock()
	defer s.m.Unlock()

	return append([]string(nil), s.added...)
}

// recBroker hands out one recSub and remembers the key-set Subscribe was given.
type recBroker struct {
	sub        *recSub
	subscribed []string
}

func newRecBroker(refuse string) *recBroker {
	return &recBroker{sub: &recSub{
		ch:     make(chan messaging.Envelope),
		refuse: refuse,
	}}
}

func (recBroker) Publish(context.Context, messaging.Envelope) error { return nil }

func (b *recBroker) Subscribe(
	_ context.Context, _ string, keys ...string,
) (messaging.Subscription, error) {
	b.subscribed = keys

	return b.sub, nil
}

// keySyncer is the capability the EventHub reaches for once a waiter becomes
// reachable through its registry.
type keySyncer interface{ SyncKeys() error }

// servicedWaiter builds a message waiter over broker, services it with ep, and
// stops it with the test.
func servicedWaiter(
	t *testing.T, broker messaging.MessageBroker, ep eventproc.EventProcessor,
) eventproc.EventWaiter {
	t.Helper()

	rt := brokerRT{EngineRuntime: enginert.Default(), broker: broker}

	w, err := waiters.CreateWaiter(
		mockeventproc.NewMockEventHub(t), ep, msgEventDef(t), rt)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, w.Service(ctx))

	return w
}

// TestMessageWaiterSyncKeys covers the re-read that closes the install window
// (SRD-090.A M2c): between a waiter subscribing and the hub installing it, a
// key declared by a concurrent sibling reaches neither Subscribe's snapshot nor
// AddEventKey, which cannot find an uninstalled waiter. SyncKeys is what the
// hub calls to pick those up — and what must NOT narrow a wildcard.
func TestMessageWaiterSyncKeys(t *testing.T) {
	t.Run("before Service there is no subscription to grow", func(t *testing.T) {
		w, err := waiters.NewMessageWaiter(mockeventproc.NewMockEventHub(t),
			&keyProc{id: "p", keys: []string{"a"}}, msgEventDef(t), "",
			enginert.Default())
		require.NoError(t, err)

		require.NoError(t, w.(keySyncer).SyncKeys())

		// the single-key path says the same: the waiter picks the value up
		// from the grown key-set when it does subscribe.
		require.NoError(t, w.(interface{ AddKey(string) error }).AddKey("a"))
	})

	t.Run("a keyed subscription gains what was declared since",
		func(t *testing.T) {
			broker := newRecBroker("")
			w := servicedWaiter(t, broker, &keyProc{id: "1", keys: []string{"a"}})

			require.Equal(t, []string{"a"}, broker.subscribed)

			// the sibling the install window belongs to: its processor joins
			// after Subscribe read the keys.
			require.NoError(t, w.AddEventProcessor(
				&keyProc{id: "2", keys: []string{"b"}}))
			require.NoError(t, w.(keySyncer).SyncKeys())

			require.Contains(t, broker.sub.keys(), "b",
				"the key declared after Subscribe must reach the subscription")
		})

	t.Run("a live subscription takes a single key", func(t *testing.T) {
		broker := newRecBroker("")
		w := servicedWaiter(t, broker, &keyProc{id: "1", keys: []string{"a"}})

		require.NoError(t,
			w.(interface{ AddKey(string) error }).AddKey("late"))

		require.Equal(t, []string{"late"}, broker.sub.keys(),
			"the lazy-association path must reach the live subscription")
	})

	t.Run("a wildcard subscription is left alone", func(t *testing.T) {
		broker := newRecBroker("")
		w := servicedWaiter(t, broker, &keyProc{id: "1"}) // declares nothing

		require.Empty(t, broker.subscribed)

		require.NoError(t, w.AddEventProcessor(
			&keyProc{id: "2", keys: []string{"b"}}))
		require.NoError(t, w.(keySyncer).SyncKeys())

		require.Empty(t, broker.sub.keys(),
			"keying a wildcard would cut off the processor that asked for "+
				"every message of this name")
	})

	t.Run("an empty key is never offered to the broker", func(t *testing.T) {
		broker := newRecBroker("")
		w := servicedWaiter(t, broker,
			&keyProc{id: "1", keys: []string{"a", ""}})

		require.NoError(t, w.(keySyncer).SyncKeys())

		require.Equal(t, []string{"a"}, broker.sub.keys(),
			"an empty key is the wildcard, not a key")
	})

	t.Run("a refused key surfaces", func(t *testing.T) {
		broker := newRecBroker("a")
		w := servicedWaiter(t, broker, &keyProc{id: "1", keys: []string{"a"}})

		require.ErrorContains(t, w.(keySyncer).SyncKeys(), "broker refused")
	})
}
