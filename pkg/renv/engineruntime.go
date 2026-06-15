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
}
