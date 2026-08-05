package thresher

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/model/service"

	"github.com/dr-dobermann/gobpm/pkg/auth/allowall"
	"github.com/dr-dobermann/gobpm/pkg/clock/syscl"
	"github.com/dr-dobermann/gobpm/pkg/datastore/memstore"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/observability/memmetrics"
	"github.com/dr-dobermann/gobpm/pkg/observability/noop"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/rules/gorules"
	"github.com/dr-dobermann/gobpm/pkg/script"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
)

func TestConfigSatisfiesEngineRuntime(t *testing.T) {
	c := defaultConfig()

	var er renv.EngineRuntime = &c

	if er.Logger() == nil || er.Tracer() == nil || er.MetricsRecorder() == nil ||
		er.Clock() == nil || er.Repository() == nil || er.MessageBroker() == nil ||
		er.ExpressionEngine() == nil || er.AuthorizationProvider() == nil ||
		er.WorkerDispatcher() == nil || er.RuleEngine() == nil ||
		er.ScriptEngine() == nil || er.DataStores() == nil {
		t.Fatal("thresherConfig does not expose every extension as EngineRuntime")
	}
}

func TestDefaultConfigWiresEveryExtension(t *testing.T) {
	c := defaultConfig()

	if c.logger == nil || c.tracer == nil || c.metrics == nil || c.clock == nil ||
		c.repository == nil || c.msgBroker == nil ||
		c.authz == nil || c.dispatcher == nil || c.ruleEngine == nil ||
		c.dataStores == nil {
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
	az := allowall.New()
	wd := localdispatcher.New(nil, 0)
	rle := gorules.New()
	ds := memstore.New()

	wem, werr := tasks.NewRuleMapper(tasks.Rule{Code: "1", Yield: tasks.Technical{}})
	if werr != nil {
		t.Fatalf("building the error mapper: %v", werr)
	}

	wrp := tasks.NoRetry()
	wtd := tasks.EngineAuthoritative

	for _, o := range []Option{
		WithLogger(lg), WithTracer(tr), WithMetricsRecorder(mr), WithClock(ck),
		WithRepository(rp), WithMessageBroker(mb),
		WithDataStore("test-store", ds),
		WithAuthorizationProvider(az), WithWorkerDispatcher(wd),
		WithRuleEngine(rle),
		WithWorkerErrorMapper(wem), WithWorkerRetryPolicy(wrp),
		WithWorkerTrustDefault(wtd),
	} {
		if err := o(&c); err != nil {
			t.Fatalf("option returned an error: %v", err)
		}
	}

	if c.logger != lg || c.tracer != tr || c.metrics != mr || c.clock != ck ||
		c.repository != rp || c.msgBroker != mb ||
		c.authz != az || c.dispatcher != wd || c.ruleEngine != rle ||
		c.WorkerErrorMapper() != wem || c.WorkerRetryPolicy() != wrp ||
		c.WorkerTrustDefault() != wtd {
		t.Fatal("a WithXxx option did not override its field")
	}

	// WithDataStore registers the store under its ref in the registry.
	if got, err := c.dataStores.Store("test-store"); err != nil || got != ds {
		t.Fatalf("WithDataStore did not register the store: got %v, err %v",
			got, err)
	}
}

func TestLastWriteWins(t *testing.T) {
	c := defaultConfig()
	first := memrepo.New()
	last := memrepo.New()

	_ = WithRepository(first)(&c)
	_ = WithRepository(last)(&c)

	if c.repository != last {
		t.Fatal("last WithRepository should win")
	}
}

func TestScriptEngineWiring(t *testing.T) {
	fake := &fakeScriptEngine{kind: "##A", formats: []string{"text/x-a"}}
	other := &fakeScriptEngine{kind: "##B", formats: []string{"text/x-b"}}

	t.Run("default is the empty ##None registry",
		func(t *testing.T) {
			eng, err := New("se-none")
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			if eng.cfg.scriptRegistry.Type() != script.NoneType {
				t.Fatalf("default script registry = %q, want ##None",
					eng.cfg.scriptRegistry.Type())
			}
		})

	t.Run("WithScriptEngine is repeatable and both register",
		func(t *testing.T) {
			eng, err := New("se-two",
				WithScriptEngine(fake), WithScriptEngine(other))
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			if got := eng.cfg.scriptRegistry.Type(); got != "##A+##B" {
				t.Fatalf("registry kinds = %q, want ##A+##B", got)
			}
		})

	t.Run("a claim conflict fails New loud",
		func(t *testing.T) {
			clash := &fakeScriptEngine{kind: "##C",
				formats: []string{"TEXT/X-A"}}

			_, err := New("se-clash",
				WithScriptEngine(fake), WithScriptEngine(clash))
			if err == nil {
				t.Fatal("New must fail on a duplicate format claim")
			}

			if !strings.Contains(err.Error(), "##A") ||
				!strings.Contains(err.Error(), "##C") {
				t.Fatalf("the conflict error must name both kinds: %v", err)
			}
		})

	t.Run("WithScriptEngine(nil) rejected",
		func(t *testing.T) {
			if _, err := New("se-nil", WithScriptEngine(nil)); err == nil {
				t.Fatal("WithScriptEngine(nil) must be rejected")
			}
		})
}

// fakeScriptEngine is a minimal script.Engine for wiring tests.
type fakeScriptEngine struct {
	kind    string
	formats []string
}

func (f *fakeScriptEngine) Type() string { return f.kind }

func (f *fakeScriptEngine) Formats() []string { return f.formats }

func (f *fakeScriptEngine) Execute(
	_ context.Context, _, _ string, _ service.DataReader,
) (script.Outputs, error) {
	return nil, nil
}

func TestExpressionEngineWiring(t *testing.T) {
	fake := &fakeExprEngine{kind: "##XTest",
		languages: []string{"x-test:expr"}}

	t.Run("zero-config is the goexpr battery",
		func(t *testing.T) {
			eng, err := New("ee-default")
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			if eng.cfg.exprRegistry.Type() != "##GoExpr+##Lite" {
				t.Fatalf("default expression registry = %q, want ##GoExpr+##Lite",
					eng.cfg.exprRegistry.Type())
			}
		})

	t.Run("WithExpressionEngine is repeatable and registers beside the battery",
		func(t *testing.T) {
			eng, err := New("ee-two", WithExpressionEngine(fake))
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			if got := eng.cfg.exprRegistry.Type(); got != "##GoExpr+##Lite+##XTest" {
				t.Fatalf("registry kinds = %q, want ##GoExpr+##Lite+##XTest", got)
			}
		})

	t.Run("a claim on the battery language fails New loud",
		func(t *testing.T) {
			clash := &fakeExprEngine{kind: "##Clash",
				languages: []string{"GOBPM:GOEXPR"}}

			_, err := New("ee-clash", WithExpressionEngine(clash))
			if err == nil {
				t.Fatal("New must fail on a duplicate language claim")
			}

			if !strings.Contains(err.Error(), "##GoExpr") ||
				!strings.Contains(err.Error(), "##Clash") {
				t.Fatalf("the conflict error must name both kinds: %v", err)
			}
		})

	t.Run("WithoutDefaultExpressionEngines empties the registry",
		func(t *testing.T) {
			eng, err := New("ee-none", WithoutDefaultExpressionEngines())
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			if eng.cfg.exprRegistry.Type() != expression.NoneType {
				t.Fatalf("opted-out registry = %q, want ##None",
					eng.cfg.exprRegistry.Type())
			}
		})

	t.Run("WithExpressionEngine(nil) rejected",
		func(t *testing.T) {
			if _, err := New("ee-nil", WithExpressionEngine(nil)); err == nil {
				t.Fatal("WithExpressionEngine(nil) must be rejected")
			}
		})
}

// fakeExprEngine is a minimal expression.Engine for wiring tests.
type fakeExprEngine struct {
	kind      string
	languages []string
}

func (f *fakeExprEngine) Type() string { return f.kind }

func (f *fakeExprEngine) Languages() []string { return f.languages }

func (f *fakeExprEngine) Evaluate(
	_ context.Context, _ data.FormalExpression, _ data.Source,
) (data.Value, error) {
	return values.NewVariable(f.kind), nil
}

func TestNilOptionValueRejected(t *testing.T) {
	c := defaultConfig()
	defaultLogger := c.logger

	// A nil value must be rejected, not silently erase the default.
	if err := WithLogger(nil)(&c); err == nil {
		t.Fatal("WithLogger(nil) should return an error")
	}

	if c.logger != defaultLogger {
		t.Fatal("WithLogger(nil) erased the default instead of rejecting")
	}

	if err := WithRuleEngine(nil)(&c); err == nil {
		t.Fatal("WithRuleEngine(nil) should return an error")
	}

	if c.ruleEngine == nil {
		t.Fatal("WithRuleEngine(nil) erased the default instead of rejecting")
	}

	if err := WithDataStore("bad", nil)(&c); err == nil {
		t.Fatal("WithDataStore(_, nil) should return an error")
	}

	if c.dataStores == nil {
		t.Fatal("WithDataStore(_, nil) erased the registry instead of rejecting")
	}

	// And New surfaces it.
	if _, err := New("x", WithRepository(nil)); err == nil {
		t.Fatal("New with WithRepository(nil) should return an error")
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

func (h *capHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *capHandler) WithGroup(string) slog.Handler            { return h }
func (h *capHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)

	return nil
}

func TestStartupConfigLog(t *testing.T) {
	h := &capHandler{}

	if _, err := New("eng-1", WithLogger(slog.New(h))); err != nil {
		t.Fatalf("New: %v", err)
	}

	// The startup config is printed line by line: the banner, build metadata
	// and one record per resolved module. Collect every message and assert the
	// human-readable lines are present, all at INFO level.
	var msgs []string
	for _, rec := range h.records {
		if rec.Level != slog.LevelInfo {
			t.Fatalf("record %q at %v, want INFO", rec.Message, rec.Level)
		}

		msgs = append(msgs, rec.Message)
	}

	joined := strings.Join(msgs, "\n")

	for _, want := range []string{
		"version:", "last commit:", "thresher id:", "configuration:",
		"repository:", "logger:", "tracer:", "metricsRecorder:", "clock:",
		"messageBroker:", "expressionEngine:", "authorizationProvider:",
		"workerDispatcher:", "ruleEngine:", "scriptEngine",
		"expressionEngine", "exprLanguage gobpm:goexpr: ##GoExpr",
		"exprLanguage gobpm:lite: ##Lite",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("startup log missing line %q (got %v)", want, msgs)
		}
	}
}

// captureStartup constructs an engine with the given options plus a capturing
// Logger and returns the emitted records joined by newline (and their count).
func captureStartup(t *testing.T, opts ...Option) (string, int) {
	t.Helper()

	h := &capHandler{}
	if _, err := New("eng", append(opts, WithLogger(slog.New(h)))...); err != nil {
		t.Fatalf("New: %v", err)
	}

	msgs := make([]string, 0, len(h.records))
	for _, rec := range h.records {
		msgs = append(msgs, rec.Message)
	}

	return strings.Join(msgs, "\n"), len(h.records)
}

// bannerMarkers / configMarkers identify lines unique to each startup block.
var (
	bannerMarkers = []string{"GoBPM —", "version:", "last commit:"}
	configMarkers = []string{
		"thresher id:", "configuration:", "repository:", "workerDispatcher:",
	}
)

func TestWithoutBanner(t *testing.T) {
	joined, n := captureStartup(t, WithoutBanner())
	if n == 0 {
		t.Fatal("WithoutBanner suppressed the whole report, want config block kept")
	}

	for _, m := range bannerMarkers {
		if strings.Contains(joined, m) {
			t.Fatalf("WithoutBanner left banner marker %q (got %q)", m, joined)
		}
	}

	for _, m := range configMarkers {
		if !strings.Contains(joined, m) {
			t.Fatalf("WithoutBanner dropped config marker %q (got %q)", m, joined)
		}
	}

	if !strings.Contains(joined, separator) {
		t.Fatal("WithoutBanner dropped the separator, want it after the config block")
	}
}

func TestWithoutStartupConfig(t *testing.T) {
	joined, n := captureStartup(t, WithoutStartupConfig())
	if n == 0 {
		t.Fatal("WithoutStartupConfig suppressed the whole report, want banner kept")
	}

	for _, m := range configMarkers {
		if strings.Contains(joined, m) {
			t.Fatalf("WithoutStartupConfig left config marker %q (got %q)", m, joined)
		}
	}

	for _, m := range bannerMarkers {
		if !strings.Contains(joined, m) {
			t.Fatalf("WithoutStartupConfig dropped banner marker %q (got %q)", m, joined)
		}
	}

	if !strings.Contains(joined, separator) {
		t.Fatal("WithoutStartupConfig dropped the separator, want it after the banner")
	}
}

func TestQuietStartup(t *testing.T) {
	_, n := captureStartup(t, WithoutBanner(), WithoutStartupConfig())
	if n != 0 {
		t.Fatalf("suppressing both blocks should be fully silent, got %d records", n)
	}
}

func TestWithoutBannerIdempotent(t *testing.T) {
	c := defaultConfig()

	_ = WithoutBanner()(&c)
	_ = WithoutBanner()(&c)

	if !c.suppressBanner || c.suppressStartupConfig {
		t.Fatalf("WithoutBanner not idempotent / leaked: %+v", c)
	}
}

// TestWithWorkerErrorMapperRejectsNil: a nil engine-wide ErrorMapper is rejected.
func TestWithWorkerErrorMapperRejectsNil(t *testing.T) {
	c := defaultConfig()
	if err := WithWorkerErrorMapper(nil)(&c); err == nil {
		t.Fatal("a nil ErrorMapper should be rejected")
	}
}

// TestWithWorkerRetryPolicyRejectsNil: a nil engine-wide RetryPolicy is rejected.
func TestWithWorkerRetryPolicyRejectsNil(t *testing.T) {
	c := defaultConfig()
	if err := WithWorkerRetryPolicy(nil)(&c); err == nil {
		t.Fatal("a nil RetryPolicy should be rejected")
	}
}

// TestWithIncidentRetryPolicy (SRD-079 §3.5): the engine-wide incident retry
// policy sets, exposes through the capability accessor, and rejects nil.
func TestWithIncidentRetryPolicy(t *testing.T) {
	c := defaultConfig()

	p := tasks.NoRetry()
	if err := WithIncidentRetryPolicy(p)(&c); err != nil {
		t.Fatalf("option returned an error: %v", err)
	}

	if c.IncidentRetryPolicy() != p {
		t.Fatal("WithIncidentRetryPolicy did not override its field")
	}

	if err := WithIncidentRetryPolicy(nil)(&c); err == nil {
		t.Fatal("a nil RetryPolicy should be rejected")
	}
}

// TestWithWorkerTrustDefaultRejectsInvalid: an unknown engine-wide trust mode is
// rejected.
func TestWithWorkerTrustDefaultRejectsInvalid(t *testing.T) {
	c := defaultConfig()
	if err := WithWorkerTrustDefault(tasks.TrustMode(99))(&c); err == nil {
		t.Fatal("an invalid trust mode should be rejected")
	}
}

// TestWithLeaseTTL covers the lease-window option (SRD-070 FR-7).
func TestWithLeaseTTL(t *testing.T) {
	if _, err := New("ttl-bad", WithoutBanner(),
		WithLeaseTTL(0)); err == nil {
		t.Fatal("a non-positive lease window must be rejected")
	}

	eng, err := New("ttl-ok", WithoutBanner(),
		WithLeaseTTL(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	if eng.cfg.leaseTTL != 5*time.Second {
		t.Fatalf("leaseTTL = %v, want 5s", eng.cfg.leaseTTL)
	}
}
