// Package renv provides runtime environment interfaces and implementations.
package renv

import (
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/interactor"
	"github.com/dr-dobermann/gobpm/internal/scope"
)

// RuntimeEnvironment keeps current runtime environment for the running Instance
// and its tracks
type RuntimeEnvironment interface {
	scope.Scope

	// InstanceID returns the process instance ID.
	InstanceID() string

	// EventProducer returns the EventProducer of the runtime.
	EventProducer() eventproc.EventProducer

	RenderRegistrator() interactor.Registrator
}
