package thresher

import (
	"log/slog"

	"github.com/dr-dobermann/gobpm/pkg/auth"
	"github.com/dr-dobermann/gobpm/pkg/auth/allowall"
	"github.com/dr-dobermann/gobpm/pkg/clock"
	"github.com/dr-dobermann/gobpm/pkg/clock/syscl"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
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
	// workerErrorMapper is the engine-wide default ErrorMapper (SRD-037 FR-3);
	// nil by default, overridden per-service by activities.WithErrorMapper.
	workerErrorMapper tasks.ErrorMapper
	taskDist          interactor.TaskDistributor

	// Startup-report suppression (ADR-002 v.2 §4.4.1). Both default to false —
	// the report is visible by default; each flag opts its block out.
	suppressBanner        bool
	suppressStartupConfig bool
}

// Option overrides one engine-level extension at thresher.New. An Option may
// fail — a nil value is rejected (it would silently erase the default) with an
// error that names the offending extension; New returns the first such error.
type Option func(*thresherConfig) error

// WithLogger sets the structured logger (default: slog.Default()).
func WithLogger(l observability.Logger) Option {
	return func(c *thresherConfig) error {
		if l == nil {
			return errs.New(
				errs.M("WithLogger: a nil Logger isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.logger = l

		return nil
	}
}

// WithTracer sets the tracer (default: no-op).
func WithTracer(t observability.Tracer) Option {
	return func(c *thresherConfig) error {
		if t == nil {
			return errs.New(
				errs.M("WithTracer: a nil Tracer isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.tracer = t

		return nil
	}
}

// WithMetricsRecorder sets the metrics recorder (default: in-memory registry).
func WithMetricsRecorder(m observability.MetricsRecorder) Option {
	return func(c *thresherConfig) error {
		if m == nil {
			return errs.New(
				errs.M("WithMetricsRecorder: a nil MetricsRecorder isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.metrics = m

		return nil
	}
}

// WithClock sets the clock (default: system wall clock).
func WithClock(ck clock.Clock) Option {
	return func(c *thresherConfig) error {
		if ck == nil {
			return errs.New(
				errs.M("WithClock: a nil Clock isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.clock = ck

		return nil
	}
}

// WithRepository sets the repository (default: in-memory, non-durable).
func WithRepository(r repository.Repository) Option {
	return func(c *thresherConfig) error {
		if r == nil {
			return errs.New(
				errs.M("WithRepository: a nil Repository isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.repository = r

		return nil
	}
}

// WithMessageBroker sets the message broker (default: in-memory inbox).
func WithMessageBroker(b messaging.MessageBroker) Option {
	return func(c *thresherConfig) error {
		if b == nil {
			return errs.New(
				errs.M("WithMessageBroker: a nil MessageBroker isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.msgBroker = b

		return nil
	}
}

// WithTaskDistributor sets the human-task distributor boundary — the embedder's
// surface for announcing/retracting parked UserTasks (ADR-020 §2.2). Default: a
// no-op distributor (tasks still park and are completable by id).
func WithTaskDistributor(d interactor.TaskDistributor) Option {
	return func(c *thresherConfig) error {
		if d == nil {
			return errs.New(
				errs.M("WithTaskDistributor: a nil TaskDistributor isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.taskDist = d

		return nil
	}
}

// WithExpressionEngine sets the expression engine (default: Go-native).
func WithExpressionEngine(e expression.Engine) Option {
	return func(c *thresherConfig) error {
		if e == nil {
			return errs.New(
				errs.M("WithExpressionEngine: a nil expression.Engine isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.exprEngine = e

		return nil
	}
}

// WithAuthorizationProvider sets the authorization provider (default: allow-all).
func WithAuthorizationProvider(a auth.AuthorizationProvider) Option {
	return func(c *thresherConfig) error {
		if a == nil {
			return errs.New(
				errs.M("WithAuthorizationProvider: a nil AuthorizationProvider isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.authz = a

		return nil
	}
}

// WithWorkerDispatcher sets the worker dispatcher (default: in-process).
func WithWorkerDispatcher(d tasks.WorkerDispatcher) Option {
	return func(c *thresherConfig) error {
		if d == nil {
			return errs.New(
				errs.M("WithWorkerDispatcher: a nil WorkerDispatcher isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.dispatcher = d

		return nil
	}
}

// WithWorkerErrorMapper sets the engine-wide default ErrorMapper applied to a
// worker-dispatched ServiceTask's raw fault when it carries no per-service
// activities.WithErrorMapper (SRD-037 FR-3, two-level config).
func WithWorkerErrorMapper(m tasks.ErrorMapper) Option {
	return func(c *thresherConfig) error {
		if m == nil {
			return errs.New(
				errs.M("WithWorkerErrorMapper: a nil ErrorMapper isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.workerErrorMapper = m

		return nil
	}
}

// WithoutBanner suppresses the startup banner block — the ASCII wordmark, the
// product tagline, and the version / last-commit lines (ADR-002 v.2 §4.4.1).
// The configuration dump still prints unless WithoutStartupConfig is also given.
func WithoutBanner() Option {
	return func(c *thresherConfig) error {
		c.suppressBanner = true

		return nil
	}
}

// WithoutStartupConfig suppresses the startup configuration dump — the thresher
// id, the "configuration:" header, and the per-extension lines (ADR-002 v.2
// §4.4.1). The banner still prints unless WithoutBanner is also given.
func WithoutStartupConfig() Option {
	return func(c *thresherConfig) error {
		c.suppressStartupConfig = true

		return nil
	}
}

// thresherConfig is the engine's resolved EngineRuntime (renv.EngineRuntime):
// the Thresher shares it with instances and the EventHub so node executors and
// event waiters reach the wired extensions.

func (c *thresherConfig) Logger() observability.Logger                   { return c.logger }
func (c *thresherConfig) Tracer() observability.Tracer                   { return c.tracer }
func (c *thresherConfig) MetricsRecorder() observability.MetricsRecorder { return c.metrics }
func (c *thresherConfig) Clock() clock.Clock                             { return c.clock }
func (c *thresherConfig) Repository() repository.Repository              { return c.repository }
func (c *thresherConfig) MessageBroker() messaging.MessageBroker         { return c.msgBroker }
func (c *thresherConfig) ExpressionEngine() expression.Engine            { return c.exprEngine }

func (c *thresherConfig) AuthorizationProvider() auth.AuthorizationProvider {
	return c.authz
}

func (c *thresherConfig) WorkerDispatcher() tasks.WorkerDispatcher { return c.dispatcher }

func (c *thresherConfig) WorkerErrorMapper() tasks.ErrorMapper { return c.workerErrorMapper }

func (c *thresherConfig) TaskDistributor() interactor.TaskDistributor { return c.taskDist }

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
		dispatcher: localdispatcher.New(nil, 0),
		taskDist:   interactor.NopDistributor(),
	}
}
