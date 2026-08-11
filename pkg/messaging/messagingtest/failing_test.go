package messagingtest

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// The double's own tests. A test double that lies is worse than no double:
// every assertion written against it inherits the lie, and the engine tests
// that use FailingBroker assert on what it recorded (#320 turned on which keys
// reached the broker). So its recording and its refusals are pinned here.

func TestFailingBrokerRecordsWhatItWasAskedFor(t *testing.T) {
	b := &FailingBroker{}

	sub, err := b.Subscribe(context.Background(), "order placed", "a", "b")
	if err != nil {
		t.Fatalf("a broker with no injected failure must subscribe: %v", err)
	}

	subs := b.Subscriptions()
	if len(subs) != 1 || subs[0] != sub {
		t.Fatalf("Subscriptions must return the subscription handed out, got %v",
			subs)
	}

	if subs[0].Name != "order placed" {
		t.Errorf("subscription name = %q, want %q", subs[0].Name, "order placed")
	}

	if got := subs[0].Keys; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("subscription created with %v, want [a b]", got)
	}

	// The creation keys are a copy: a caller that reuses its slice must not
	// be able to rewrite what the double recorded.
	keys := []string{"x"}

	if _, err = b.Subscribe(context.Background(), "second", keys...); err != nil {
		t.Fatalf("second Subscribe: %v", err)
	}

	keys[0] = "mutated"

	if got := b.Subscriptions()[1].Keys[0]; got != "x" {
		t.Errorf("recorded creation key = %q, want it unaffected by the "+
			"caller's slice", got)
	}
}

func TestFailingBrokerPublishesNothing(t *testing.T) {
	b := &FailingBroker{}

	sub, err := b.Subscribe(context.Background(), "order placed", "a")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Publish must not fail — a test exercising a REFUSED KEY would
	// otherwise be unable to tell that error from the one it injected.
	if err = b.Publish(context.Background(), env("order placed", "a")); err != nil {
		t.Fatalf("Publish must accept and discard, got %v", err)
	}

	select {
	case got, ok := <-sub.C():
		t.Fatalf("this broker routes nothing; got %v (open=%v)", got, ok)
	default:
	}
}

func TestFailingBrokerRefusesSubscribe(t *testing.T) {
	b := &FailingBroker{SubscribeErr: ErrInjected}

	sub, err := b.Subscribe(context.Background(), "order placed")
	if !errors.Is(err, ErrInjected) {
		t.Fatalf("Subscribe error = %v, want the injected one", err)
	}

	if sub != nil {
		t.Errorf("a refused Subscribe must hand out no subscription, got %v", sub)
	}

	if got := b.Subscriptions(); len(got) != 0 {
		t.Errorf("a refused Subscribe must record nothing, got %v", got)
	}
}

func TestFailingSubscriptionRefusesKeys(t *testing.T) {
	tests := []struct {
		name     string
		broker   *FailingBroker
		keys     []string
		wantErrs []bool
		wantAdd  []string
	}{
		{
			name:     "every key refused",
			broker:   NewFailingBroker(),
			keys:     []string{"a", "b"},
			wantErrs: []bool{true, true},
			wantAdd:  nil,
		},
		{
			name:     "refusal starts after the allowance",
			broker:   &FailingBroker{AddKeyErr: ErrInjected, AddKeyAfter: 1},
			keys:     []string{"a", "b"},
			wantErrs: []bool{false, true},
			wantAdd:  []string{"a"},
		},
		{
			name:     "nothing injected, every key accepted",
			broker:   &FailingBroker{},
			keys:     []string{"a", "b"},
			wantErrs: []bool{false, false},
			wantAdd:  []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub, err := tt.broker.Subscribe(context.Background(), "order placed")
			if err != nil {
				t.Fatalf("Subscribe: %v", err)
			}

			for i, k := range tt.keys {
				err = sub.AddKey(k)
				if got := err != nil; got != tt.wantErrs[i] {
					t.Fatalf("AddKey(%q) error = %v, want error = %v",
						k, err, tt.wantErrs[i])
				}

				if err != nil && !errors.Is(err, ErrInjected) {
					t.Errorf("AddKey(%q) = %v, want the injected error", k, err)
				}
			}

			added := tt.broker.Subscriptions()[0].Added()
			if len(added) != len(tt.wantAdd) {
				t.Fatalf("Added() = %v, want %v", added, tt.wantAdd)
			}

			for i, k := range tt.wantAdd {
				if added[i] != k {
					t.Errorf("Added()[%d] = %q, want %q", i, added[i], k)
				}
			}
		})
	}
}

func TestFailingSubscriptionUnsubscribeIsIdempotent(t *testing.T) {
	b := &FailingBroker{}

	sub, err := b.Subscribe(context.Background(), "order placed")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err = sub.Unsubscribe(); err != nil {
		t.Fatalf("first Unsubscribe: %v", err)
	}

	if _, open := <-sub.C(); open {
		t.Error("Unsubscribe must close the envelope channel")
	}

	// The port allows a second Unsubscribe, and the waiter under test makes
	// one on every teardown path — a double that panicked on it would fail
	// tests for a reason that has nothing to do with what they assert.
	if err = sub.Unsubscribe(); err != nil {
		t.Fatalf("second Unsubscribe: %v", err)
	}
}

func TestFailingSubscriptionRunsTheUnsubscribeHook(t *testing.T) {
	var seen int

	b := &FailingBroker{OnUnsubscribe: func() { seen++ }}

	sub, err := b.Subscribe(context.Background(), "order placed")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err = sub.Unsubscribe(); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	// The hook runs on the second, no-op teardown too: a test that blocks in it
	// to hold the caller inside the broker must not have that window depend on
	// whether the subscription happens to be closed already.
	if err = sub.Unsubscribe(); err != nil {
		t.Fatalf("second Unsubscribe: %v", err)
	}

	if seen != 2 {
		t.Errorf("hook ran %d time(s), want 2", seen)
	}
}

func TestFailingSubscriptionReportsUnsubscribed(t *testing.T) {
	b := &FailingBroker{}

	sub, err := b.Subscribe(context.Background(), "order placed")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	fs, ok := sub.(*FailingSubscription)
	if !ok {
		t.Fatalf("Subscribe returned %T, want *FailingSubscription", sub)
	}

	if fs.Unsubscribed() {
		t.Error("a fresh subscription must not report itself unsubscribed")
	}

	if err = fs.Unsubscribe(); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	if !fs.Unsubscribed() {
		t.Error("Unsubscribed must report the teardown that happened")
	}
}

func TestFailingSubscriptionRunsTheAddKeyHook(t *testing.T) {
	var seen []string

	b := &FailingBroker{
		AddKeyErr:   ErrInjected,
		AddKeyAfter: 1,
		OnAddKey:    func(key string) { seen = append(seen, key) },
	}

	sub, err := b.Subscribe(context.Background(), "order placed")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err = sub.AddKey("first"); err != nil {
		t.Fatalf("AddKey(first): %v", err)
	}

	// The hook runs on the REFUSED call too: a test that blocks in it to hold
	// the caller inside the broker must not have that window depend on whether
	// the key is about to be accepted.
	if err = sub.AddKey("second"); !errors.Is(err, ErrInjected) {
		t.Fatalf("AddKey(second) = %v, want ErrInjected", err)
	}

	want := []string{"first", "second"}
	if !slices.Equal(seen, want) {
		t.Errorf("hook saw %v, want %v", seen, want)
	}
}

func TestFailingBrokerOpensTheSubscribeWindow(t *testing.T) {
	var during []string

	b := &FailingBroker{}
	b.OnSubscribe = func() {
		during = append(during, "inside")

		if got := b.Subscriptions(); len(got) != 0 {
			t.Errorf("the hook must run BEFORE the subscription exists, "+
				"got %v", got)
		}
	}

	if _, err := b.Subscribe(context.Background(), "order placed"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if len(during) != 1 {
		t.Fatalf("the hook ran %d time(s), want exactly once", len(during))
	}
}
