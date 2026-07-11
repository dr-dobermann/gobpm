package instance

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// capHandler captures emitted slog records for level/message/attr assertions
// (FIX-022 §4.2 — the ADR-022 boundary logs must actually appear, at the right
// level and key vocabulary).
type capHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *capHandler) WithGroup(string) slog.Handler            { return h }

func (h *capHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())

	return nil
}

// attr returns the string value of the named attribute on the first record at
// level whose message contains msg, or ok=false if no such record exists.
func (h *capHandler) attr(level slog.Level, msg, key string) (val string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, r := range h.records {
		if r.Level != level || r.Message != msg {
			continue
		}

		r.Attrs(func(a slog.Attr) bool {
			if a.Key == key {
				val = a.Value.String()
			}

			return true
		})

		return val, true
	}

	return "", false
}

// TestSpawnForksFaultRoutesThroughFail (FIX-022 §4.1.3, E3 + activation C):
// a fork target that cannot be built faults the instance THROUGH fail() — the
// single fault boundary — so LastErr is set and one Error record appears under
// the canonical instance_id key, instead of the error being stored silently.
// Since SRD-041 the record is the producer's echo of the InstanceState/Failed
// event (fail() emits rather than logging directly); it still echoes at Error
// with instance_id.
func TestSpawnForksFaultRoutesThroughFail(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	h := &capHandler{}
	inst, err := New(buildForkSnapshot(t), scope.EmptyDataPath,
		enginert.Default().WithLogger(slog.New(h)),
		mockeventproc.NewMockEventProducer(t), nil)
	require.NoError(t, err)

	inst.tracks = map[string]*track{}

	start, err := events.NewStartEvent("spawn-src")
	require.NoError(t, err)
	bn, err := flow.NewBaseNode("plain")
	require.NoError(t, err)
	fBad, err := flow.Link(start, plainNode{bn}) // target lacks NodeExecutor
	require.NoError(t, err)

	ls := newLoopState(inst)
	ls.spawnForks(t.Context(), trackEvent{flows: []*flow.SequenceFlow{fBad}})

	require.True(t, ls.stopping, "a build fault stops the instance")
	require.Error(t, inst.LastErr(), "the fault is recorded")

	errAttr, ok := h.attr(slog.LevelError, "InstanceState Failed", "instance_id")
	require.True(t, ok,
		"E3: a fork-build fault must echo via fail() at Error, not store lastErr silently")
	require.Equal(t, inst.ID(), errAttr, "the record carries the canonical instance_id")
}
