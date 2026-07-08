// Package enginert provides a concrete renv.EngineRuntime assembled from the
// bundled default extensions. It exists for engine-internal use and tests that
// need a working EngineRuntime without the full Thresher assembly (the Thresher
// itself builds its EngineRuntime from its configured options). Override setters
// let tests inject a specific extension (e.g. a fake clock or a spy expression
// engine).
package enginert

import (
	"log/slog"

	"github.com/dr-dobermann/gobpm/pkg/auth"
	"github.com/dr-dobermann/gobpm/pkg/auth/allowall"
	"github.com/dr-dobermann/gobpm/pkg/clock"
	"github.com/dr-dobermann/gobpm/pkg/clock/syscl"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/observability/memmetrics"
	"github.com/dr-dobermann/gobpm/pkg/observability/noop"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/tasks/localdispatcher"
)

// Runtime is a concrete renv.EngineRuntime backed by the bundled defaults.
type Runtime struct {
	logger     observability.Logger
	tracer     observability.Tracer
	metrics    observability.MetricsRecorder
	clk        clock.Clock
	repo       repository.Repository
	broker     messaging.MessageBroker
	expr       expression.Engine
	authz      auth.AuthorizationProvider
	dispatcher tasks.WorkerDispatcher
	// workerErrorMapper is the engine-wide default ErrorMapper (SRD-037 FR-3);
	// nil by default (a per-service WithErrorMapper overrides it).
	workerErrorMapper tasks.ErrorMapper
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
		expr:       goexpr.New(),
		authz:      allowall.New(),
		dispatcher: localdispatcher.New(nil, 0),
	}
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

// WithExpressionEngine overrides the expression engine and returns the Runtime. A
// nil engine is ignored (the bundled default is kept).
func (r *Runtime) WithExpressionEngine(e expression.Engine) *Runtime {
	if e != nil {
		r.expr = e
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

// ExpressionEngine returns the configured expression engine.
func (r *Runtime) ExpressionEngine() expression.Engine { return r.expr }

// AuthorizationProvider returns the configured authorization provider.
func (r *Runtime) AuthorizationProvider() auth.AuthorizationProvider { return r.authz }

// WorkerDispatcher returns the configured worker dispatcher.
func (r *Runtime) WorkerDispatcher() tasks.WorkerDispatcher { return r.dispatcher }

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

var _ renv.EngineRuntime = (*Runtime)(nil)
