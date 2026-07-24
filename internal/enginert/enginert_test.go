package enginert

import (
	"log/slog"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/rules/gorules"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
)

func TestDefaultPopulatesEveryExtension(t *testing.T) {
	r := Default()

	if r.Logger() == nil || r.Tracer() == nil || r.MetricsRecorder() == nil ||
		r.Clock() == nil || r.Repository() == nil || r.MessageBroker() == nil ||
		r.ExpressionEngine() == nil || r.AuthorizationProvider() == nil ||
		r.WorkerDispatcher() == nil || r.RuleEngine() == nil {
		t.Fatal("Default left an extension nil")
	}
}

// TestDefaultRuntimeWorkerDispatcherIsJobStore covers SRD-036 FR-3: the default
// runtime's WorkerDispatcher is the reworked localdispatcher job store, and
// WithWorkerDispatcher injects an alternative.
func TestDefaultRuntimeWorkerDispatcherIsJobStore(t *testing.T) {
	if _, ok := Default().WorkerDispatcher().(*localdispatcher.Dispatcher); !ok {
		t.Fatal("default WorkerDispatcher is not the localdispatcher job store")
	}

	custom := localdispatcher.New(nil, 0)
	if Default().WithWorkerDispatcher(custom).WorkerDispatcher() != custom {
		t.Fatal("WithWorkerDispatcher was not applied")
	}
}

func TestOverrides(t *testing.T) {
	c := clocktest.New(time.Unix(0, 0))
	e := goexpr.New()
	re := gorules.New()

	l := slog.Default()

	r := Default().WithClock(c).WithExpressionEngine(e).WithLogger(l).
		WithRuleEngine(re)

	if r.Clock() != c {
		t.Fatal("WithClock was not applied")
	}

	if r.ExpressionEngine() != e {
		t.Fatal("WithExpressionEngine was not applied")
	}

	if r.Logger() != l {
		t.Fatal("WithLogger was not applied")
	}

	if r.RuleEngine() != re {
		t.Fatal("WithRuleEngine was not applied")
	}

	if Default().WithRuleEngine(nil).RuleEngine() == nil {
		t.Fatal("a nil rule engine should be ignored (default kept)")
	}
}

// TestWithWorkerErrorMapper covers SRD-037 FR-3: the engine-wide default
// ErrorMapper is nil by default, set by WithWorkerErrorMapper, nil ignored.
func TestWithWorkerErrorMapper(t *testing.T) {
	m, err := tasks.NewRuleMapper(tasks.Rule{Code: "1", Yield: tasks.Technical{}})
	if err != nil {
		t.Fatal(err)
	}

	if Default().WorkerErrorMapper() != nil {
		t.Fatal("the default worker error mapper should be nil")
	}

	if Default().WithWorkerErrorMapper(m).WorkerErrorMapper() != m {
		t.Fatal("WithWorkerErrorMapper was not applied")
	}

	if Default().WithWorkerErrorMapper(nil).WorkerErrorMapper() != nil {
		t.Fatal("a nil mapper should be ignored (default kept)")
	}
}

// TestWithWorkerRetryPolicy covers SRD-038 FR-6: the engine-wide default
// RetryPolicy is nil by default, set by WithWorkerRetryPolicy, nil ignored.
func TestWithWorkerRetryPolicy(t *testing.T) {
	p := tasks.NoRetry()

	if Default().WorkerRetryPolicy() != nil {
		t.Fatal("the default worker retry policy should be nil")
	}

	if Default().WithWorkerRetryPolicy(p).WorkerRetryPolicy() != p {
		t.Fatal("WithWorkerRetryPolicy was not applied")
	}

	if Default().WithWorkerRetryPolicy(nil).WorkerRetryPolicy() != nil {
		t.Fatal("a nil policy should be ignored (default kept)")
	}
}

// TestWithWorkerTrustDefault covers SRD-039 M9: the engine-wide default trust
// mode is unset (zero) by default and set by WithWorkerTrustDefault.
func TestWithWorkerTrustDefault(t *testing.T) {
	var unset tasks.TrustMode

	if Default().WorkerTrustDefault() != unset {
		t.Fatal("the default trust mode should be unset")
	}

	got := Default().
		WithWorkerTrustDefault(tasks.EngineAuthoritative).WorkerTrustDefault()
	if got != tasks.EngineAuthoritative {
		t.Fatal("WithWorkerTrustDefault was not applied")
	}
}
