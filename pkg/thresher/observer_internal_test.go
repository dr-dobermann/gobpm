package thresher

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/stretchr/testify/require"
)

// capturedRecord is one log record flattened to what the assertions need.
type capturedRecord struct {
	attrs map[string]string
	msg   string
	level slog.Level
}

// logCapture is a slog.Handler that records everything, including Debug — the
// engine's default handler drops Debug, and the flood half of the policy is
// only observable there.
type logCapture struct {
	recs []capturedRecord
	mu   sync.Mutex
}

func (h *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *logCapture) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *logCapture) WithGroup(string) slog.Handler { return h }

func (h *logCapture) Handle(_ context.Context, r slog.Record) error {
	rec := capturedRecord{
		msg:   r.Message,
		level: r.Level,
		attrs: make(map[string]string, r.NumAttrs()),
	}

	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.String()

		return true
	})

	h.mu.Lock()
	defer h.mu.Unlock()

	h.recs = append(h.recs, rec)

	return nil
}

// observerPanics returns the captured observer-panic records at one level.
func (h *logCapture) observerPanics(lvl slog.Level) []capturedRecord {
	h.mu.Lock()
	defer h.mu.Unlock()

	var out []capturedRecord

	for _, r := range h.recs {
		if r.level == lvl && strings.HasPrefix(r.msg, "observer panicked") {
			out = append(out, r)
		}
	}

	return out
}

// newCapture builds a logger that records every level.
func newCapture() (*logCapture, observability.Logger) {
	h := &logCapture{}

	return h, slog.New(h)
}

// boomObserver panics on every Fact with a recognisable value.
type boomObserver struct{}

func (boomObserver) OnFact(observability.Fact) { panic("boom") }

// quietObserver never panics.
type quietObserver struct{ calls atomic.Uint64 }

func (q *quietObserver) OnFact(observability.Fact) { q.calls.Add(1) }

// TestDeliverReturnsRecoveredValue: deliver hands the recovered value back
// instead of discarding it (FIX-035 §3.2.1), and captures a stack only when
// asked — the property that keeps a flooding observer off the debug.Stack cost.
func TestDeliverReturnsRecoveredValue(t *testing.T) {
	t.Run("panic with stack requested", func(t *testing.T) {
		r, stack := deliver(boomObserver{}, observability.Fact{}, true)

		require.Equal(t, "boom", r, "the recovered value reaches the caller")
		require.NotEmpty(t, stack, "a stack is captured when asked")
		require.Contains(t, string(stack), "boomObserver",
			"the stack names the panicking observer's own frame")
	})

	t.Run("panic without stack requested", func(t *testing.T) {
		r, stack := deliver(boomObserver{}, observability.Fact{}, false)

		require.Equal(t, "boom", r)
		require.Nil(t, stack, "no stack is formatted when it was not asked for")
	})

	t.Run("no panic", func(t *testing.T) {
		q := &quietObserver{}

		r, stack := deliver(q, observability.Fact{}, true)

		require.Nil(t, r, "a nil recovered value is an unambiguous no-panic")
		require.Nil(t, stack)
		require.Equal(t, uint64(1), q.calls.Load(), "the observer still ran")
	})
}

// TestDeliverObservedFirstWarnsThenDebugs: the flood policy (FIX-035 §3.1.D).
// A broken observer panics on EVERY Fact, so the loud record is bounded to one
// per subscription — that bound is what keeps the per-event record at Debug,
// as ADR-022 v.1 §2.4's hot-path corollary requires — while the counter stays
// authoritative for how often it actually happened.
func TestDeliverObservedFirstWarnsThenDebugs(t *testing.T) {
	const deliveries = 50

	cap, log := newCapture()

	var panicked atomic.Uint64

	for range deliveries {
		deliverObserved(log, boomObserver{}, observability.Fact{}, &panicked)
	}

	warns := cap.observerPanics(slog.LevelWarn)
	debugs := cap.observerPanics(slog.LevelDebug)

	require.Len(t, warns, 1,
		"exactly one Warn per subscription no matter how many panics")
	require.Len(t, debugs, deliveries-1,
		"every later panic is still recorded, at Debug")
	require.Equal(t, uint64(deliveries), panicked.Load(),
		"the counter — not the log — is the authority on the count")

	require.Equal(t, "thresher.boomObserver",
		warns[0].attrs[observability.AttrObserverType],
		"the record names the observer's concrete type")
	require.Equal(t, "boom", warns[0].attrs[observability.AttrError],
		"the recovered value travels under the canonical error key")
	require.NotEmpty(t, warns[0].attrs["stack"],
		"the loud record carries the stack")
	require.Empty(t, debugs[0].attrs["stack"],
		"the quiet records do not pay for a stack")
}

// TestDeliverObservedQuietObserverIsSilent: a healthy observer produces no
// record and no count — the policy must not make working code noisy.
func TestDeliverObservedQuietObserverIsSilent(t *testing.T) {
	cap, log := newCapture()

	var panicked atomic.Uint64

	q := &quietObserver{}
	for range 10 {
		deliverObserved(log, q, observability.Fact{}, &panicked)
	}

	require.Zero(t, panicked.Load())
	require.Empty(t, cap.observerPanics(slog.LevelWarn))
	require.Empty(t, cap.observerPanics(slog.LevelDebug))
	require.Equal(t, uint64(10), q.calls.Load())
}
