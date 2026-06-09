package thresher

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

// thresherConfig holds the resolved engine-level extensions (ADR-002 §4.3).
// EventHub is NOT here — it stays internal and the Thresher builds it itself.
type thresherConfig struct {
	logger     observability.Logger
	tracer     observability.Tracer
	metrics    observability.MetricsRecorder
	clock      clock.Clock
	repository repository.Repository
	msgBroker  messaging.MessageBroker
	exprEngine expression.Engine
	authz      auth.AuthorizationProvider
	dispatcher tasks.WorkerDispatcher
}

// Option overrides one engine-level extension at thresher.New.
type Option func(*thresherConfig)

// WithLogger sets the structured logger (default: slog.Default()).
func WithLogger(l observability.Logger) Option {
	return func(c *thresherConfig) { c.logger = l }
}

// WithTracer sets the tracer (default: no-op).
func WithTracer(t observability.Tracer) Option {
	return func(c *thresherConfig) { c.tracer = t }
}

// WithMetricsRecorder sets the metrics recorder (default: in-memory registry).
func WithMetricsRecorder(m observability.MetricsRecorder) Option {
	return func(c *thresherConfig) { c.metrics = m }
}

// WithClock sets the clock (default: system wall clock).
func WithClock(ck clock.Clock) Option {
	return func(c *thresherConfig) { c.clock = ck }
}

// WithRepository sets the repository (default: in-memory, non-durable).
func WithRepository(r repository.Repository) Option {
	return func(c *thresherConfig) { c.repository = r }
}

// WithMessageBroker sets the message broker (default: in-memory inbox).
func WithMessageBroker(b messaging.MessageBroker) Option {
	return func(c *thresherConfig) { c.msgBroker = b }
}

// WithExpressionEngine sets the expression engine (default: Go-native).
func WithExpressionEngine(e expression.Engine) Option {
	return func(c *thresherConfig) { c.exprEngine = e }
}

// WithAuthorizationProvider sets the authorization provider (default: allow-all).
func WithAuthorizationProvider(a auth.AuthorizationProvider) Option {
	return func(c *thresherConfig) { c.authz = a }
}

// WithWorkerDispatcher sets the worker dispatcher (default: in-process).
func WithWorkerDispatcher(d tasks.WorkerDispatcher) Option {
	return func(c *thresherConfig) { c.dispatcher = d }
}

// thresherConfig is the engine's resolved EngineRuntime (renv.EngineRuntime):
// the Thresher shares it with instances and the EventHub so node executors and
// event waiters reach the wired extensions.

func (c *thresherConfig) Logger() observability.Logger          { return c.logger }
func (c *thresherConfig) Tracer() observability.Tracer          { return c.tracer }
func (c *thresherConfig) MetricsRecorder() observability.MetricsRecorder { return c.metrics }
func (c *thresherConfig) Clock() clock.Clock                    { return c.clock }
func (c *thresherConfig) Repository() repository.Repository     { return c.repository }
func (c *thresherConfig) MessageBroker() messaging.MessageBroker { return c.msgBroker }
func (c *thresherConfig) ExpressionEngine() expression.Engine   { return c.exprEngine }

func (c *thresherConfig) AuthorizationProvider() auth.AuthorizationProvider {
	return c.authz
}

func (c *thresherConfig) WorkerDispatcher() tasks.WorkerDispatcher { return c.dispatcher }

var _ renv.EngineRuntime = (*thresherConfig)(nil)

// defaultConfig wires every extension to its bundled core default. A zero-option
// thresher.New produces a fully working engine from this (no NewDefault).
func defaultConfig() thresherConfig {
	return thresherConfig{
		logger:     slog.Default(),
		tracer:     noop.NewTracer(),
		metrics:    memmetrics.New(),
		clock:      syscl.New(),
		repository: memrepo.New(),
		msgBroker:  membroker.New(),
		exprEngine: goexpr.New(),
		authz:      allowall.New(),
		dispatcher: localdispatcher.New(0),
	}
}
