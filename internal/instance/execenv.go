package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/renv"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// execEnv is the per-execution runtime environment the track hands a node
// (ADR-010 §2.4): engine services and instance identity are delegated to the
// Instance; the data surface is backed by the execution's own frame.
type execEnv struct {
	*Instance

	frame *scope.Frame
}

// newExecEnv builds the environment of one node execution.
func newExecEnv(inst *Instance, f *scope.Frame) *execEnv {
	return &execEnv{
		Instance: inst,
		frame:    f,
	}
}

// GetData resolves name frame-first, then through the container scopes.
func (e *execEnv) GetData(name string) (data.Data, error) {
	return e.frame.GetData(name)
}

// GetDataByID resolves an ItemDefinition id frame-first, then through the
// container scopes.
func (e *execEnv) GetDataByID(id string) (data.Data, error) {
	return e.frame.GetDataByID(id)
}

// GetSources lists the named data sources reachable through the environment.
func (e *execEnv) GetSources() []string {
	return e.frame.GetSources()
}

// List enumerates variable names at the default scope (empty path) or a named
// source.
func (e *execEnv) List(path string) ([]string, error) {
	return e.frame.List(path)
}

// Put stores node-produced values in the execution's frame.
func (e *execEnv) Put(dd ...data.Data) error {
	return e.frame.Put(dd...)
}

// Find implements data.Source: expression evaluation reads variables with
// the execution's resolution (frame-first, container walk-up).
func (e *execEnv) Find(_ context.Context, name string) (data.Data, error) {
	return e.frame.GetData(name)
}

// interface check
var _ renv.RuntimeEnvironment = (*execEnv)(nil)
