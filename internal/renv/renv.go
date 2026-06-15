// Package renv provides runtime environment interfaces and implementations.
package renv

import (
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	engrenv "github.com/dr-dobermann/gobpm/pkg/renv"
)

// RuntimeEnvironment is the environment a node executes against. It is built
// PER EXECUTION by the track (ADR-010 §2.4, SRD-007 FR-3): the engine-level
// extension set and instance identity are shared, while the data surface is
// backed by the execution's own Frame — reads resolve frame-first and walk
// the instance's container scopes; results go to the frame and reach the
// container scope only at the frame commit.
//
// It also serves as the data.Source for expression evaluation (conditions
// read variables through the environment). It stays internal because it
// exposes internal-only types (EventProducer, Registrator); only the
// embedded EngineRuntime is public (pkg/renv).
type RuntimeEnvironment interface {
	engrenv.EngineRuntime

	// Source lets expression evaluation (sequence-flow conditions, gateway
	// decisions) read variables through the environment.
	data.Source

	// InstanceID returns the process instance ID.
	InstanceID() string

	// EventProducer returns the EventProducer of the runtime.
	EventProducer() eventproc.EventProducer

	// RenderRegistrator returns the user-interaction render registrator.
	RenderRegistrator() interactor.Registrator

	// GetData returns the data named name, resolving frame-first and then
	// walking the container scopes from the execution's attachment point.
	GetData(name string) (data.Data, error)

	// GetDataByID returns the data whose ItemDefinition id is id, with the
	// same frame-first resolution.
	GetDataByID(id string) (data.Data, error)

	// GetSources lists the named data sources reachable through the
	// environment (the default scope is addressed by plain name, not listed).
	GetSources() []string

	// List enumerates variable names: an empty path lists the default-scope
	// names the execution sees; a source segment returns that source's names.
	List(path string) ([]string, error)

	// Put stores node-produced values in the execution's frame; they reach
	// the container scope at the frame commit.
	Put(dd ...data.Data) error
}
