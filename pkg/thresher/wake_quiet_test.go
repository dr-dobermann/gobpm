package thresher_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// errSink captures ERROR records so a test can assert a path stayed quiet.
type errSink struct {
	mu   sync.Mutex
	msgs []string
}

func (s *errSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *errSink) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		s.mu.Lock()
		s.msgs = append(s.msgs, r.Message)
		s.mu.Unlock()
	}

	return nil
}

func (s *errSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *errSink) WithGroup(string) slog.Handler      { return s }

func (s *errSink) errors() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.msgs...)
}

// TestMessageWakeReportsNoFailure covers SRD-071 T-12 (FR-3b): a successful
// message wake logs nothing at ERROR.
//
// The wake runs SYNCHRONOUSLY inside the holder's own ProcessEvent, and it
// releases the holder's registrations — which unregisters and Stops the very
// hub waiter that is mid-delivery. That waiter then reports its fire, finds
// itself absent from the registry, and before M8 treated the miss as the
// hub-state divergence it normally is: every successful wake ended with
// "message waiter terminally failed". The waiter was correctly stopped; only
// its exit was mislabelled, and an ERROR on a fully successful path teaches an
// operator to ignore the channel where real divergence would appear.
func TestMessageWakeReportsNoFailure(t *testing.T) {
	repo := memrepo.New()
	broker := membroker.New()
	got := make(chan string, 2)
	sink := &errSink{}

	p := msgWaitProcess(t, "quiet-msg", got)

	th, err := thresher.New("engine-quiet",
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithMessageBroker(broker),
		thresher.WithLogger(slog.New(sink)),
		thresher.WithLeaseTTL(time.Minute))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)

	t.Cleanup(sub.Cancel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)
	require.NoError(t, th.Run(ctx))

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "order placed", Payload: "ORD-1", CorrelationKey: "ORD-1"}))

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond,
		"an instance parked on a message catch must dehydrate")

	require.NoError(t, broker.Publish(ctx, messaging.Envelope{
		Name: "payment received", Payload: "PAID-1", CorrelationKey: "ORD-1"}))

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseHydrated)
	}, 3*time.Second, 10*time.Millisecond, "the message must wake it")

	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("the woken flow never passed its wait")
	}

	// give the stopped waiter's goroutine time to reach its exit — the ERROR,
	// when it happened, was logged there and not on the wake path.
	require.Never(t, func() bool { return len(sink.errors()) > 0 },
		500*time.Millisecond, 25*time.Millisecond)

	for _, m := range sink.errors() {
		require.NotContains(t, strings.ToLower(m), "terminally failed",
			"a successful wake must not report a waiter failure")
	}
}
