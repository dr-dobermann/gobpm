// Package renv provides runtime environment interfaces and implementations.
package renv

import (
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/interactor"
	"github.com/dr-dobermann/gobpm/internal/scope"
	engrenv "github.com/dr-dobermann/gobpm/pkg/renv"
)

// RuntimeEnvironment keeps current runtime environment for the running Instance
// and its tracks. It is the instance-level context: the engine-level extension
// set (the embedded EngineRuntime) plus instance-local state. It stays internal
// because it exposes internal-only types (scope, EventProducer, Registrator);
// only the embedded EngineRuntime is public (pkg/renv).
type RuntimeEnvironment interface {
	engrenv.EngineRuntime

	scope.Scope

	// InstanceID returns the process instance ID.
	InstanceID() string

	// EventProducer returns the EventProducer of the runtime.
	EventProducer() eventproc.EventProducer

	RenderRegistrator() interactor.Registrator
}
