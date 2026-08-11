package messagingtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
)

// shrinkWaits cuts the suite's hang-breakers for the negative tests. A broker
// that is KNOWN never to deliver does not need five seconds to prove it, and
// these cases would otherwise dominate the package's runtime on every gate run.
//
// It goes through the exported SetWaits rather than assigning the package
// variables directly, so the negative tests exercise the same knob an adapter
// author uses — a tunable nothing in the repo calls is a tunable nobody has
// checked. SetWaits is process-global and its restore is registered with
// t.Cleanup, which is why no test in this package may call t.Parallel.
func shrinkWaits(t *testing.T) {
	t.Helper()

	t.Cleanup(SetWaits(WaitConfig{
		Delivery: 100 * time.Millisecond,
		Silence:  20 * time.Millisecond,
	}))
}

// fakeTB records what an assertion did instead of failing the real test.
type fakeTB struct {
	msg      string
	cleanups []func()
	failed   bool
	skipped  bool
}

// fakeAbort is the sentinel a fakeTB panics with, so drive can tell an
// intentional abort from a genuine panic and re-raise the latter.
type fakeAbort struct{}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }

func (f *fakeTB) Fatal(args ...any) {
	f.failed, f.msg = true, fmt.Sprint(args...)

	panic(fakeAbort{})
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed, f.msg = true, fmt.Sprintf(format, args...)

	panic(fakeAbort{})
}

func (f *fakeTB) Skip(args ...any) {
	f.skipped, f.msg = true, fmt.Sprint(args...)

	panic(fakeAbort{})
}

// drive runs one contract assertion against b and reports what it did.
func drive(
	test func(tb, messaging.MessageBroker), b messaging.MessageBroker,
) *fakeTB {
	f := &fakeTB{}

	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(fakeAbort); !ok {
					panic(r)
				}
			}
		}()

		test(f, b)
	}()

	for _, fn := range f.cleanups {
		fn()
	}

	return f
}

// silentBroker accepts every publish and delivers nothing.
type silentBroker struct{}

func (silentBroker) Publish(context.Context, messaging.Envelope) error {
	return nil
}

func (silentBroker) Subscribe(
	context.Context, string, ...string,
) (messaging.Subscription, error) {
	return deadSub{}, nil
}

type deadSub struct{}

func (deadSub) C() <-chan messaging.Envelope { return nil }
func (deadSub) AddKey(string) error          { return nil }
func (deadSub) Unsubscribe() error           { return nil }

// refusingBroker cannot subscribe at all.
type refusingBroker struct{ silentBroker }

func (refusingBroker) Subscribe(
	context.Context, string, ...string,
) (messaging.Subscription, error) {
	return nil, errors.New("subscribe refused")
}

// failingPublishBroker cannot publish.
type failingPublishBroker struct{ silentBroker }

func (failingPublishBroker) Publish(
	context.Context, messaging.Envelope,
) error {
	return errors.New("publish refused")
}

// shoutingBroker fans every message to every subscriber, ignoring names, keys
// and specificity — the opposite failure from silence, and the one the
// negative-assertion subtests exist to catch.
type shoutingBroker struct{ subs []chan messaging.Envelope }

func (b *shoutingBroker) Publish(
	_ context.Context, msg messaging.Envelope,
) error {
	for _, ch := range b.subs {
		select {
		case ch <- msg:
		default:
		}
	}

	return nil
}

func (b *shoutingBroker) Subscribe(
	context.Context, string, ...string,
) (messaging.Subscription, error) {
	ch := make(chan messaging.Envelope, 8)
	b.subs = append(b.subs, ch)

	return shoutingSub{ch: ch}, nil
}

type shoutingSub struct{ ch chan messaging.Envelope }

func (s shoutingSub) C() <-chan messaging.Envelope { return s.ch }
func (s shoutingSub) AddKey(string) error          { return nil }
func (s shoutingSub) Unsubscribe() error           { return nil }

// TestAssertionsRejectBrokenBrokers drives each contract assertion against a
// broker that violates it (SRD-088 T-9, in-process half). Splitting it this
// way is what proves each assertion does its own job: a suite-level check
// cannot distinguish twelve working assertions from one working assertion and
// eleven inverted ones.
func TestAssertionsRejectBrokenBrokers(t *testing.T) {
	shrinkWaits(t)

	for name, tc := range map[string]struct {
		test   func(tb, messaging.MessageBroker)
		broker func() messaging.MessageBroker
		want   string
	}{
		"delivery never arrives": {
			test:   testSubscribeThenPublishDelivers,
			broker: func() messaging.MessageBroker { return silentBroker{} },
			want:   "no delivery",
		},
		"a buffered message is lost": {
			test:   testPublishThenSubscribeDrains,
			broker: func() messaging.MessageBroker { return silentBroker{} },
			want:   "no delivery",
		},
		"a wildcard receives nothing": {
			test:   testWildcardMatchesAnyKey,
			broker: func() messaging.MessageBroker { return silentBroker{} },
			want:   "no delivery",
		},
		"AddKey does not drain": {
			test:   testAddKeyExtendsAndDrains,
			broker: func() messaging.MessageBroker { return silentBroker{} },
			want:   "no delivery",
		},
		"subscribe fails": {
			test:   testSubscribeThenPublishDelivers,
			broker: func() messaging.MessageBroker { return refusingBroker{} },
			want:   "Subscribe",
		},
		"publish fails": {
			test:   testSubscribeThenPublishDelivers,
			broker: func() messaging.MessageBroker { return failingPublishBroker{} },
			want:   "Publish",
		},
		"a keyed subscription receives a foreign key": {
			test:   testKeyedMatchesOnlyItsKeys,
			broker: func() messaging.MessageBroker { return &shoutingBroker{} },
			want:   "unexpected delivery",
		},
		"a subscription receives a foreign name": {
			test:   testNameMismatchNotDelivered,
			broker: func() messaging.MessageBroker { return &shoutingBroker{} },
			want:   "unexpected delivery",
		},
		"the wildcard steals a keyed message": {
			test:   testKeyedBeatsWildcard,
			broker: func() messaging.MessageBroker { return &shoutingBroker{} },
			want:   "unexpected delivery",
		},
		"one message reaches every subscriber": {
			test:   testPointToPointSingleDelivery,
			broker: func() messaging.MessageBroker { return &shoutingBroker{} },
			want:   "want exactly 1",
		},
		"AddKey accepts the empty key": {
			test:   testAddKeyEmptyRejected,
			broker: func() messaging.MessageBroker { return silentBroker{} },
			want:   "must be rejected",
		},
		"unsubscribe does not stop delivery": {
			test:   testUnsubscribeStopsDelivery,
			broker: func() messaging.MessageBroker { return &shoutingBroker{} },
			want:   "unexpected delivery",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := drive(tc.test, tc.broker())

			if !got.failed {
				t.Fatalf("the assertion PASSED a broker where %s", name)
			}

			if !strings.Contains(got.msg, tc.want) {
				t.Fatalf("failed with %q, which does not mention %q",
					got.msg, tc.want)
			}
		})
	}
}

// TestWaitsAreTunable covers the knob published for out-of-repo adapters
// (NFR-3): an adapter over a slow backend must be able to widen the bounds
// this suite's in-process defaults assume.
//
// It is tested rather than merely offered because an untested tunable is the
// same trap as an untested assertion — it would be discovered broken by the
// adapter author it exists for, at the moment they need it.
func TestWaitsAreTunable(t *testing.T) {
	before := Waits()

	restore := SetWaits(WaitConfig{
		Delivery: 42 * time.Second,
		Silence:  7 * time.Second,
	})

	got := Waits()
	if got.Delivery != 42*time.Second || got.Silence != 7*time.Second {
		t.Fatalf("SetWaits did not take effect: %+v", got)
	}

	restore()

	if back := Waits(); back != before {
		t.Fatalf("restore left %+v, want %+v", back, before)
	}

	// A zero field keeps the current value, so a caller may widen one bound
	// without having to restate the others.
	defer SetWaits(WaitConfig{Delivery: 9 * time.Second})()

	if partial := Waits(); partial.Silence != before.Silence {
		t.Fatalf("a zero field overwrote Silence: %+v", partial)
	}
}
