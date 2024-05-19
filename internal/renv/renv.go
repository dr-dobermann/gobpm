package renv

import (
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/scope"
)

// RuntimeEnvironment keeps current runtime environment for the running Instance
// and its tracks
type RuntimeEnvironment interface {
	scope.Scope

	// InstanceId returns the process instance Id.
	InstanceId() string

	// EventProducer returns the EventProducer of the runtime.
	EventProducer() eventproc.EventProducer

	// Scope returns the Scope of the runtime.
	Scope() scope.Scope
}
