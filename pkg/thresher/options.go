package thresher

import (
	"log/slog"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/auth"
	"github.com/dr-dobermann/gobpm/pkg/auth/allowall"
	"github.com/dr-dobermann/gobpm/pkg/clock"
	"github.com/dr-dobermann/gobpm/pkg/clock/syscl"
	"github.com/dr-dobermann/gobpm/pkg/datastore"
	"github.com/dr-dobermann/gobpm/pkg/datastore/memstore"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
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

// thresherConfig holds the resolved engine-level extensions (ADR-002 §4.3).
// EventHub is NOT here — it stays internal and the Thresher builds it itself.
type thresherConfig struct {
	exprEngines       []expression.Engine
	exprRegistry      *expression.Registry
	logger            observability.Logger
	workerErrorMapper tasks.ErrorMapper
	ruleEngine        rules.Engine
	clock             clock.Clock
	repository        repository.Repository
	msgBroker         messaging.MessageBroker
	tracer            observability.Tracer
	dispatcher        tasks.WorkerDispatcher
	reporter          observability.Reporter
	metrics           observability.MetricsRecorder
	dataStores        *memstore.Registry
	// registeredStores is what WithDataStore put in dataStores, kept in
	// registration order. datastore.Registry resolves a ref to a store and
	// does not enumerate, so this is how Shutdown reaches the stores it must
	// stop (SRD-090 §3.2).
	//
	// It is keyed by REF, not a flat slice, because WithDataStore replaces a
	// store when its ref is reused. A slice that only appended would keep the
	// superseded store here, and the engine would start, health-check and
	// stop an object serving no reference — acquiring its connections and
	// letting its health decide the engine's.
	registeredStores    []registeredStore
	authz               auth.AuthorizationProvider
	workerRetryPolicy   tasks.RetryPolicy
	incidentRetryPolicy tasks.RetryPolicy
	taskDist            interactor.TaskDistributor
	scriptRegistry      *script.Registry
	// engineGroup is the configured engine group (SRD-078 FR-2); empty
	// means the solo default (New resolves it to the engine id).
	// groupJoinOnly asserts membership: Run refuses when the group is
	// not established in the repository's registry.
	engineGroup           string
	scriptEngines         []script.Engine
	workerTrustDefault    tasks.TrustMode
	groupJoinOnly         bool
	noDefaultExprEngines  bool
	suppressBanner        bool
	suppressStartupConfig bool
	repoSet               bool
	leaseTTL              time.Duration
	wakeBackoff           time.Duration
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

// WithRepository sets the repository AND arms checkpointing (SRD-070
// FR-7): an explicitly configured repository is meant to hold the state
// of record, so every instance checkpoints into it and Run recovers the
// claimable in-flight instances at start. The zero-config default
// (memrepo, unconfigured) stays volatile — today's behavior, zero
// overhead.
func WithRepository(r repository.Repository) Option {
	return func(c *thresherConfig) error {
		if r == nil {
			return errs.New(
				errs.M("WithRepository: a nil Repository isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.repoSet = true

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

// WithExpressionEngine registers an expression engine (ADR-032 §2.1). The
// option is REPEATABLE — each call registers another engine; the language
// claims fold into the routing registry at New, where a duplicate claim
// fails construction loud. The batteries (goexpr — and lite, when it
// lands) are prepended by default; suppress them with
// WithoutDefaultExpressionEngines. (Pre-1.0 semantic change: the old
// replace-wholesale meaning is retired — registration composes.)
func WithExpressionEngine(e expression.Engine) Option {
	return func(c *thresherConfig) error {
		if e == nil {
			return errs.New(
				errs.M("WithExpressionEngine: a nil expression.Engine isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.exprEngines = append(c.exprEngines, e)

		return nil
	}
}

// WithoutDefaultExpressionEngines starts the expression registry EMPTY:
// no batteries are prepended, every engine registers explicitly — the
// "remove it from the runtime if unused" posture (ADR-032 §2.1). A model
// evaluating an expression whose language nobody claims then fails loud
// with the registered claims listed.
func WithoutDefaultExpressionEngines() Option {
	return func(c *thresherConfig) error {
		c.noDefaultExprEngines = true

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

// WithRuleEngine sets the Business Rule Engine the Business Rule Task
// evaluates its decisions on (default: the in-core gorules decision registry).
func WithRuleEngine(e rules.Engine) Option {
	return func(c *thresherConfig) error {
		if e == nil {
			return errs.New(
				errs.M("WithRuleEngine: a nil rules.Engine isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.ruleEngine = e

		return nil
	}
}

// WithScriptEngine registers a script engine for the Script Task
// (ADR-031 §2.1). The option is REPEATABLE — each call registers another
// engine; the format claims fold into the routing registry at New, where a
// duplicate claim fails construction loud. Default: no engines (the
// "##None" registry — Script Tasks fail with a wire-an-adapter error).
func WithScriptEngine(e script.Engine) Option {
	return func(c *thresherConfig) error {
		if e == nil {
			return errs.New(
				errs.M("WithScriptEngine: a nil script.Engine isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.scriptEngines = append(c.scriptEngines, e)

		return nil
	}
}

// WithDataStore registers the engine-global Data Store a DataStoreReference
// with dataStoreRef=ref reads and writes (BPMN §10.4.1, ADR-030 §2.5). Each
// store outlives every instance and is shared across them; call it once per
// distinct store. A store may be any datastore.DataStore — the in-memory
// memstore, or a durable adapter. Registering an already-used ref replaces it.
func WithDataStore(ref string, store datastore.DataStore) Option {
	return func(c *thresherConfig) error {
		if err := c.dataStores.Register(ref, store); err != nil {
			return err
		}

		// Mirror the registry's replace-by-ref semantics, keeping the original
		// position so the shutdown order does not shift under a reconfiguration.
		for i := range c.registeredStores {
			if c.registeredStores[i].ref == ref {
				c.registeredStores[i].store = store

				return nil
			}
		}

		c.registeredStores = append(c.registeredStores,
			registeredStore{ref: ref, store: store})

		return nil
	}
}

// registeredStore pairs a Data Store with the ref it was registered under, so
// a re-registration of that ref replaces it rather than adding a second entry.
type registeredStore struct {
	store datastore.DataStore
	ref   string
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

// WithWorkerRetryPolicy sets the engine-wide default RetryPolicy applied to a
// worker-dispatched ServiceTask's technical fault when it carries no per-service
// activities.WithRetryPolicy (SRD-038 FR-6, two-level config).
func WithWorkerRetryPolicy(p tasks.RetryPolicy) Option {
	return func(c *thresherConfig) error {
		if p == nil {
			return errs.New(
				errs.M("WithWorkerRetryPolicy: a nil RetryPolicy isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.workerRetryPolicy = p

		return nil
	}
}

// WithIncidentRetryPolicy sets the engine-wide default incident retry policy
// (ADR-036 §2.3, SRD-079 §3.5), applied when a failing activity carries no
// activities.WithIncidentRetryPolicy of its own. Without either, every
// incident waits for an operator — the deliberate conservative default.
func WithIncidentRetryPolicy(p tasks.RetryPolicy) Option {
	return func(c *thresherConfig) error {
		if p == nil {
			return errs.New(
				errs.M("WithIncidentRetryPolicy: a nil RetryPolicy isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		c.incidentRetryPolicy = p

		return nil
	}
}

// WithWorkerTrustDefault sets the engine-wide default trust mode applied to a
// worker-dispatched ServiceTask that carries no per-service activities.WithWorkerTrust
// (SRD-039 M9, two-level config). An invalid mode is rejected.
func WithWorkerTrustDefault(mode tasks.TrustMode) Option {
	return func(c *thresherConfig) error {
		if mode != tasks.WorkerTrusted && mode != tasks.EngineAuthoritative {
			return errs.New(
				errs.M("WithWorkerTrustDefault: unknown trust mode %q", mode.String()),
				errs.C(errorClass, errs.InvalidParameter))
		}

		c.workerTrustDefault = mode

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

// DefaultLeaseTTL is the default instance-ownership lease window
// (ADR-033 §2.8) an armed engine stamps on every checkpoint.
const DefaultLeaseTTL = 30 * time.Second

// DefaultWakeRetryBackoff is how long the engine waits before re-attempting a
// wake that failed (FIX-027 §3.2.3). A held wait is a released instance's only
// way back, so a failed wake keeps its hold and tries again — this is the pause
// between attempts, which is what stops the retry becoming a spin.
//
// Derived from the lease window rather than picked independently: an operator
// who lengthens the lease is describing a slower-moving deployment, and a retry
// cadence that outpaced it would just churn.
const DefaultWakeRetryBackoff = DefaultLeaseTTL / 2

// setEngineGroup validates and stores the group both group options
// share; joinOnly marks the WithExistingEngineGroup assertion.
func setEngineGroup(
	c *thresherConfig, option, name string, joinOnly bool,
) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errs.New(
			errs.M("%s: an empty group name isn't allowed", option),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if c.engineGroup != "" {
		return errs.New(
			errs.M("%s: the engine group is already set to %q",
				option, c.engineGroup),
			errs.C(errorClass, errs.InvalidParameter))
	}

	c.engineGroup = name
	c.groupJoinOnly = joinOnly

	return nil
}

// WithEngineGroup names the engine's group (SRD-078 FR-2, ADR-033 v.3
// §2.8): engines deliberately sharing a group name over one repository
// form a cluster — they recover and claim each other's instances. The
// group is established in the repository's group registry at Run if
// absent. Unset, the engine forms a single-engine group under its own
// engine id — clustering is explicit opt-in, never accidental.
func WithEngineGroup(name string) Option {
	return func(c *thresherConfig) error {
		return setEngineGroup(c, "WithEngineGroup", name, false)
	}
}

// WithExistingEngineGroup joins an ALREADY-established engine group
// (SRD-078 FR-2): at Run the engine asserts the group exists in the
// repository's registry and refuses to start when it does not — the
// typo-guard that keeps a misspelled group name from silently minting
// a fresh partition.
func WithExistingEngineGroup(name string) Option {
	return func(c *thresherConfig) error {
		return setEngineGroup(c, "WithExistingEngineGroup", name, true)
	}
}

// WithLeaseTTL tunes the ownership-lease window (SRD-070 FR-7): how
// long a crashed engine's instances stay unclaimable before recovery
// may take them. Non-positive values are rejected.
func WithLeaseTTL(d time.Duration) Option {
	return func(c *thresherConfig) error {
		if d <= 0 {
			return errs.New(
				errs.M("WithLeaseTTL: the lease window must be positive"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		c.leaseTTL = d

		return nil
	}
}

// WithWakeRetryBackoff sets the pause before a failed wake is re-attempted
// (FIX-027). A dehydrated instance is woken by its hold; when the wake fails —
// an unregistered pinned version, a checkpoint that will not decode — the hold
// is KEPT and retried after this interval, so the instance self-heals once the
// cause clears instead of being stranded until the engine restarts.
//
// Default: DefaultWakeRetryBackoff.
func WithWakeRetryBackoff(d time.Duration) Option {
	return func(c *thresherConfig) error {
		if d <= 0 {
			return errs.New(
				errs.M("WithWakeRetryBackoff: the backoff must be positive"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		c.wakeBackoff = d

		return nil
	}
}
func (c *thresherConfig) MessageBroker() messaging.MessageBroker { return c.msgBroker }
func (c *thresherConfig) ExpressionEngine() expression.Engine    { return c.exprRegistry }
func (c *thresherConfig) RuleEngine() rules.Engine               { return c.ruleEngine }
func (c *thresherConfig) ScriptEngine() script.Engine            { return c.scriptRegistry }
func (c *thresherConfig) DataStores() datastore.Registry         { return c.dataStores }

func (c *thresherConfig) AuthorizationProvider() auth.AuthorizationProvider {
	return c.authz
}

func (c *thresherConfig) WorkerDispatcher() tasks.WorkerDispatcher { return c.dispatcher }

// Reporter returns the engine's observable-event sink (ADR-013 v.2 §2.7).
// Absent an explicit sink, it returns an echo-only producer bound to the
// configured logger — never nil, preserving the visible-by-default posture.
func (c *thresherConfig) Reporter() observability.Reporter {
	if c.reporter != nil {
		return c.reporter
	}

	return observability.NewEchoReporter(c.logger)
}

func (c *thresherConfig) WorkerErrorMapper() tasks.ErrorMapper { return c.workerErrorMapper }

func (c *thresherConfig) WorkerRetryPolicy() tasks.RetryPolicy { return c.workerRetryPolicy }

// IncidentRetryPolicy is the engine-wide incident retry default the instance
// loop reads through a capability assertion (SRD-079 §3.5); nil = operator-only.
func (c *thresherConfig) IncidentRetryPolicy() tasks.RetryPolicy { return c.incidentRetryPolicy }

func (c *thresherConfig) WorkerTrustDefault() tasks.TrustMode { return c.workerTrustDefault }

func (c *thresherConfig) TaskDistributor() interactor.TaskDistributor { return c.taskDist }

var _ renv.EngineRuntime = (*thresherConfig)(nil)

// defaultConfig wires every extension to its bundled core default. A zero-option
// thresher.New produces a fully working engine from this (no NewDefault).
func defaultConfig() thresherConfig {
	return thresherConfig{
		logger:      slog.Default(),
		tracer:      noop.NewTracer(),
		metrics:     memmetrics.New(),
		clock:       syscl.New(),
		repository:  memrepo.New(),
		leaseTTL:    DefaultLeaseTTL,
		wakeBackoff: DefaultWakeRetryBackoff,
		msgBroker:   membroker.New(),
		authz:       allowall.New(),
		dispatcher:  localdispatcher.New(nil, 0),
		ruleEngine:  gorules.New(),
		dataStores:  memstore.NewRegistry(),
		taskDist:    interactor.NopDistributor(),
	}
}
