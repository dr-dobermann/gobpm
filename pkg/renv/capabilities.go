package renv

import "context"

// ClusterAware is the optional capability an extension declares to
// state whether it may back a multi-engine deployment (ADR-002 §8.3,
// SRD-078 FR-3). The engine and its tooling read the declaration; the
// runtime-level cluster-mode enforcement stays with the Distribution &
// Scale work — this is the vocabulary, satisfied structurally (an
// adapter needs no import to implement it).
type ClusterAware interface {
	// ClusterCompatibility reports whether the extension is safe to
	// share between engines, with a short human-readable reason for the
	// verdict (e.g. "in-memory; state is not shared across nodes").
	ClusterCompatibility() (bool, string)
}

// Migrator is the optional capability a storage-backed extension
// implements to prepare its own objects in the shared, user-owned
// backend (ADR-033 §2.7 — "each module prepares its own objects",
// SRD-078 FR-3). thresher.Run invokes it on the wired Repository
// before the engine group is established and recovery runs; an error
// aborts the start loud — a half-created schema must never look like
// an empty store.
type Migrator interface {
	// Migrate creates or upgrades the extension's storage objects,
	// idempotently: a second run over an up-to-date backend is a no-op.
	Migrate(ctx context.Context) error
}

// Starter is the optional capability an adapter implements to learn that the
// engine is starting, before it accepts any work. The engine detects it by type
// assertion on each wired seam (ADR-002 v.2 §8.3) — an adapter that does not
// implement it is simply not asked, which is not an error.
//
// A returned error ABORTS the start: an extension that cannot start is not a
// degraded mode, and an engine that ran on top of one would fail later, further
// from the cause.
type Starter interface {
	Start(ctx context.Context) error
}

// Stopper is the optional capability an adapter implements to release what it
// holds — a connection pool, a subscription, a goroutine. The engine calls it
// during Shutdown, after the work that depends on that seam has drained.
//
// It MUST be idempotent: a second call is a no-op returning nil. The engine
// stops what it holds, while a host that constructed the adapter (or a server
// that started it before the engine existed, ADR-004 v.1 §4.3) may stop it too.
// Idempotency is what makes that overlap safe rather than a double release.
type Stopper interface {
	Stop(ctx context.Context) error
}

// HealthChecker is the optional capability an adapter implements to answer, on
// demand, whether it is presently usable — a pool that can still reach its
// database, a broker whose connection is live.
//
// It is a PULL, and that is why it exists alongside the observation stream
// rather than duplicating it: facts and metrics report what has already
// happened, and a readiness probe needs to know the state right now. A host
// aggregates the engine's answer through Thresher.HealthCheck.
type HealthChecker interface {
	HealthCheck(ctx context.Context) error
}

// RuntimeAware is the optional capability an adapter implements to receive the
// engine's resolved services (ADR-002 v.2 §8.3 Pattern C). The engine calls it
// once during New, after every option has been applied — an adapter handed a
// half-built runtime would silently default a dependency to nil.
//
// It is also how an adapter publishes its own operational statistics and inner
// state: rt.MetricsRecorder(), rt.Tracer() and rt.Logger() are the same path
// every engine component emits through, so an adapter's pool exhaustion or
// retry count lands beside the engine's own facts rather than in a private
// channel nobody is watching.
type RuntimeAware interface {
	UseRuntime(rt EngineRuntime)
}
