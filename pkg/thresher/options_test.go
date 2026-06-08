package thresher

import (
	"context"
	"log/slog"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/auth/allowall"
	"github.com/dr-dobermann/gobpm/pkg/clock/syscl"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/observability/memmetrics"
	"github.com/dr-dobermann/gobpm/pkg/observability/noop"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
)

func TestDefaultConfigWiresEveryExtension(t *testing.T) {
	c := defaultConfig()

	if c.logger == nil || c.tracer == nil || c.metrics == nil || c.clock == nil ||
		c.repository == nil || c.msgBroker == nil || c.exprEngine == nil ||
		c.authz == nil || c.dispatcher == nil {
		t.Fatalf("defaultConfig left an extension nil: %+v", c)
	}
}

func TestEveryOptionOverridesItsDefault(t *testing.T) {
	c := defaultConfig()

	lg := slog.Default()
	tr := noop.NewTracer()
	mr := memmetrics.New()
	ck := syscl.New()
	rp := memrepo.New()
	mb := membroker.New()
	ee := goexpr.New()
	az := allowall.New()
	wd := localdispatcher.New(0)

	for _, o := range []Option{
		WithLogger(lg), WithTracer(tr), WithMetricsRecorder(mr), WithClock(ck),
		WithRepository(rp), WithMessageBroker(mb), WithExpressionEngine(ee),
		WithAuthorizationProvider(az), WithWorkerDispatcher(wd),
	} {
		o(&c)
	}

	if c.logger != lg || c.tracer != tr || c.metrics != mr || c.clock != ck ||
		c.repository != rp || c.msgBroker != mb || c.exprEngine != ee ||
		c.authz != az || c.dispatcher != wd {
		t.Fatal("a WithXxx option did not override its field")
	}
}

func TestLastWriteWins(t *testing.T) {
	c := defaultConfig()
	first := memrepo.New()
	last := memrepo.New()

	WithRepository(first)(&c)
	WithRepository(last)(&c)

	if c.repository != last {
		t.Fatal("last WithRepository should win")
	}
}

func TestZeroOptionNewWorks(t *testing.T) {
	eng, err := New("zero-opt")
	if err != nil || eng == nil {
		t.Fatalf("New with no options = %v, %v", eng, err)
	}
}

// capHandler captures emitted slog records.
type capHandler struct{ records []slog.Record }

func (h *capHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (h *capHandler) WithAttrs([]slog.Attr) slog.Handler        { return h }
func (h *capHandler) WithGroup(string) slog.Handler             { return h }
func (h *capHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)

	return nil
}

func TestStartupConfigLog(t *testing.T) {
	h := &capHandler{}

	if _, err := New("eng-1", WithLogger(slog.New(h))); err != nil {
		t.Fatalf("New: %v", err)
	}

	if len(h.records) != 1 {
		t.Fatalf("records = %d, want exactly 1", len(h.records))
	}

	rec := h.records[0]
	if rec.Message != "thresher.starting" || rec.Level != slog.LevelInfo {
		t.Fatalf("record = %q @ %v, want \"thresher.starting\" @ INFO", rec.Message, rec.Level)
	}

	keys := map[string]bool{}
	rec.Attrs(func(a slog.Attr) bool {
		keys[a.Key] = true

		return true
	})

	for _, want := range []string{
		"id", "repository", "logger", "tracer", "metricsRecorder", "clock",
		"messageBroker", "expressionEngine", "authorizationProvider",
		"workerDispatcher",
	} {
		if !keys[want] {
			t.Fatalf("startup log missing attr %q (got %v)", want, keys)
		}
	}
}
