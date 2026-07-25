// Package enginert provides a concrete renv.EngineRuntime assembled from the
// bundled default extensions. It exists for engine-internal use and tests that
// need a working EngineRuntime without the full Thresher assembly (the Thresher
// itself builds its EngineRuntime from its configured options). Override setters
// let tests inject a specific extension (e.g. a fake clock or a spy expression
// engine).
package enginert

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"log/slog"

	"github.com/dr-dobermann/gobpm/pkg/auth"
	"github.com/dr-dobermann/gobpm/pkg/auth/allowall"
	"github.com/dr-dobermann/gobpm/pkg/clock"
	"github.com/dr-dobermann/gobpm/pkg/clock/syscl"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/observability/memmetrics"
	"github.com/dr-dobermann/gobpm/pkg/observability/noop"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/rules"
	"github.com/dr-dobermann/gobpm/pkg/rules/gorules"
	"github.com/dr-dobermann/gobpm/pkg/script"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
)

// Runtime is a concrete renv.EngineRuntime backed by the bundled defaults.
type Runtime struct {
	exprReg            *expression.Registry
	ruleEng            rules.Engine
	scriptReg          *script.Registry
	tracer             observability.Tracer
	metrics            observability.MetricsRecorder
	clk                clock.Clock
	repo               repository.Repository
	broker             messaging.MessageBroker
	logger             observability.Logger
	authz              auth.AuthorizationProvider
	dispatcher         tasks.WorkerDispatcher
	workerErrorMapper  tasks.ErrorMapper
	workerRetryPolicy  tasks.RetryPolicy
	reporter           observability.Reporter
	workerTrustDefault tasks.TrustMode
}

// Default returns a Runtime with every extension set to its bundled default.
func Default() *Runtime {
	return &Runtime{
		logger:     slog.Default(),
		tracer:     noop.NewTracer(),
		metrics:    memmetrics.New(),
		clk:        syscl.New(),
		repo:       memrepo.New(),
		broker:     membroker.New(),
		exprReg:    defaultExprRegistry(),
		authz:      allowall.New(),
		dispatcher: localdispatcher.New(nil, 0),
		ruleEng:    gorules.New(),
		scriptReg:  emptyScriptRegistry(),
	}
}

// defaultExprRegistry builds the batteries default (goexpr + lite);
// NewRegistry over the batteries cannot fail.
func defaultExprRegistry() *expression.Registry {
	reg, err := expression.NewRegistry(goexpr.New(), lite.New())
	if err != nil {
		errs.Panic(err)

		return nil
	}

	return reg
}

// emptyScriptRegistry builds the ##None default; NewRegistry with no
// engines cannot fail.
func emptyScriptRegistry() *script.Registry {
	reg, err := script.NewRegistry()
	if err != nil {
		errs.Panic(err)

		return nil
	}

	return reg
}

// The override setters below all guard against a nil argument by keeping the
// bundled default rather than erasing it. These are fluent (return *Runtime for
// chaining), so they cannot report an error; a silently-erased default would
// surface only later as a nil-deref far from the call (the FIX-020 bug class — a
// setter must not let bad input replace a working default). The public option API
// (thresher.WithXxx) rejects a nil with an explicit error instead.

// WithClock overrides the clock and returns the Runtime for chaining. A nil clock
// is ignored (the bundled default is kept).
func (r *Runtime) WithClock(c clock.Clock) *Runtime {
	if c != nil {
		r.clk = c
	}

	return r
}

// WithExpressionRegistry overrides the expression-engine registry and
// returns the Runtime. A nil registry is ignored (the goexpr default is
// kept). The caller builds the registry with expression.NewRegistry — the
// single conflict authority, where duplicate language claims fail loud.
func (r *Runtime) WithExpressionRegistry(reg *expression.Registry) *Runtime {
	if reg != nil {
		r.exprReg = reg
	}

	return r
}

// WithRuleEngine overrides the Business Rule Engine and returns the Runtime. A
// nil engine is ignored (the bundled default is kept).
func (r *Runtime) WithRuleEngine(e rules.Engine) *Runtime {
	if e != nil {
		r.ruleEng = e
	}

	return r
}

// WithScriptRegistry overrides the script-engine registry and returns the
// Runtime. A nil registry is ignored (the empty default is kept). The
// caller builds the registry with script.NewRegistry — the single
// conflict authority, where duplicate format claims fail loud.
func (r *Runtime) WithScriptRegistry(reg *script.Registry) *Runtime {
	if reg != nil {
		r.scriptReg = reg
	}

	return r
}

// WithLogger overrides the logger and returns the Runtime. A nil logger is ignored
// (the bundled default is kept).
func (r *Runtime) WithLogger(l observability.Logger) *Runtime {
	if l != nil {
		r.logger = l
	}

	return r
}

// WithWorkerDispatcher overrides the worker dispatcher and returns the Runtime. A
// nil dispatcher is ignored (the bundled default is kept).
func (r *Runtime) WithWorkerDispatcher(d tasks.WorkerDispatcher) *Runtime {
	if d != nil {
		r.dispatcher = d
	}

	return r
}

// Logger returns the configured logger.
func (r *Runtime) Logger() observability.Logger { return r.logger }

// Tracer returns the configured tracer.
func (r *Runtime) Tracer() observability.Tracer { return r.tracer }

// MetricsRecorder returns the configured metrics recorder.
func (r *Runtime) MetricsRecorder() observability.MetricsRecorder { return r.metrics }

// Clock returns the configured clock.
func (r *Runtime) Clock() clock.Clock { return r.clk }

// Repository returns the configured repository.
func (r *Runtime) Repository() repository.Repository { return r.repo }

// MessageBroker returns the configured message broker.
func (r *Runtime) MessageBroker() messaging.MessageBroker { return r.broker }

// ExpressionEngine returns the configured expression routing surface.
func (r *Runtime) ExpressionEngine() expression.Engine { return r.exprReg }

// RuleEngine returns the configured Business Rule Engine.
func (r *Runtime) RuleEngine() rules.Engine { return r.ruleEng }

// ScriptEngine returns the configured script-engine routing surface.
func (r *Runtime) ScriptEngine() script.Engine { return r.scriptReg }

// AuthorizationProvider returns the configured authorization provider.
func (r *Runtime) AuthorizationProvider() auth.AuthorizationProvider { return r.authz }

// WorkerDispatcher returns the configured worker dispatcher.
func (r *Runtime) WorkerDispatcher() tasks.WorkerDispatcher { return r.dispatcher }

// Reporter returns the engine's observable-event sink (ADR-013 v.2 §2.7).
// Absent an explicit sink, it returns an echo-only producer bound to the current
// logger, so the result is never nil and the visible-by-default posture holds.
func (r *Runtime) Reporter() observability.Reporter {
	if r.reporter != nil {
		return r.reporter
	}

	return observability.NewEchoReporter(r.logger)
}

// WithReporter overrides the observable-event sink and returns the Runtime
// for chaining. A nil sink is ignored (the echo-only default is kept).
func (r *Runtime) WithReporter(s observability.Reporter) *Runtime {
	if s != nil {
		r.reporter = s
	}

	return r
}

// WorkerErrorMapper returns the engine-wide default ErrorMapper (nil = none).
func (r *Runtime) WorkerErrorMapper() tasks.ErrorMapper { return r.workerErrorMapper }

// WithWorkerErrorMapper overrides the engine-wide default ErrorMapper and returns
// the Runtime. A nil mapper is ignored (the current default is kept).
func (r *Runtime) WithWorkerErrorMapper(m tasks.ErrorMapper) *Runtime {
	if m != nil {
		r.workerErrorMapper = m
	}

	return r
}

// WorkerRetryPolicy returns the engine-wide default RetryPolicy (nil = fall back
// to tasks.DefaultRetryPolicy at resolution).
func (r *Runtime) WorkerRetryPolicy() tasks.RetryPolicy { return r.workerRetryPolicy }

// WithWorkerRetryPolicy overrides the engine-wide default RetryPolicy and returns
// the Runtime. A nil policy is ignored (the current default is kept).
func (r *Runtime) WithWorkerRetryPolicy(p tasks.RetryPolicy) *Runtime {
	if p != nil {
		r.workerRetryPolicy = p
	}

	return r
}

// WorkerTrustDefault returns the engine-wide default trust mode (the zero value
// resolves to WorkerTrusted at enqueue).
func (r *Runtime) WorkerTrustDefault() tasks.TrustMode { return r.workerTrustDefault }

// WithWorkerTrustDefault overrides the engine-wide default trust mode and returns
// the Runtime.
func (r *Runtime) WithWorkerTrustDefault(m tasks.TrustMode) *Runtime {
	r.workerTrustDefault = m

	return r
}

var _ renv.EngineRuntime = (*Runtime)(nil)
