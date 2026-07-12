// Package renv defines the public runtime-environment contracts: EngineRuntime
// — the engine/server-level set of resolved extensions (the wired services) the
// Thresher owns and shares with the things that run BPMN — and the per-execution
// RuntimeEnvironment a node executes against (ADR-012 §2.3). Both expose only
// public types, so pkg/model implements/consumes them without importing
// internal; the implementations live in internal/instance (ADR-002 §4.3).
package renv

import (
	"github.com/dr-dobermann/gobpm/pkg/auth"
	"github.com/dr-dobermann/gobpm/pkg/clock"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// EngineRuntime exposes the engine's resolved extension set. The Thresher
// implements it (from its assembled config) and injects it into instances and
// the EventHub. Adapters receive it via the optional RuntimeAware hook (deferred
// — ADR-002 §3.5/§8.3).
type EngineRuntime interface {
	Logger() observability.Logger
	Tracer() observability.Tracer
	MetricsRecorder() observability.MetricsRecorder
	Clock() clock.Clock
	Repository() repository.Repository
	MessageBroker() messaging.MessageBroker
	ExpressionEngine() expression.Engine
	AuthorizationProvider() auth.AuthorizationProvider
	WorkerDispatcher() tasks.WorkerDispatcher

	// Reporter returns the engine's observable-event sink (ADR-013 v.2
	// §2.7): the single producer that writes the operator-log echo and fans
	// every Fact out to the registered observers. Never nil — absent an
	// explicit sink, an echo-only producer bound to Logger() is returned, so
	// the visible-by-default posture holds and node emitters can always emit.
	Reporter() observability.Reporter

	// WorkerErrorMapper is the engine-wide default ErrorMapper applied to a
	// worker-dispatched ServiceTask's raw fault when the task carries no
	// per-service WithErrorMapper (SRD-037 FR-3, two-level config). nil = no
	// default (a raw fault falls through to the default technical outcome).
	WorkerErrorMapper() tasks.ErrorMapper

	// WorkerRetryPolicy is the engine-wide default RetryPolicy applied to a
	// worker-dispatched ServiceTask's technical fault when the task carries no
	// per-service WithRetryPolicy (SRD-038 FR-6, two-level config). nil = fall
	// back to tasks.DefaultRetryPolicy at resolution.
	WorkerRetryPolicy() tasks.RetryPolicy

	// WorkerTrustDefault is the engine-wide default trust mode applied to a
	// worker-dispatched ServiceTask that carries no per-service WithWorkerTrust
	// (SRD-039 M9, two-level config). The zero value (trustUnset) resolves to
	// WorkerTrusted (the ADR-021 default) at enqueue.
	WorkerTrustDefault() tasks.TrustMode
}
