package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// execEnv is the per-execution runtime environment the track hands a node
// (ADR-010 §2.4): engine services and instance identity are delegated to the
// Instance; the data surface is backed by the execution's own frame.
type execEnv struct {
	*Instance

	frame *scope.Frame
	// track is the executing track (nil for transient evaluation frames):
	// the scoped Terminate routes by ITS scope path (SRD-049 FR-11).
	track *track
}

// newExecEnv builds the environment of one node execution. t may be nil
// for transient (non-track) frames — Terminate then falls back to the
// instance-level semantics.
func newExecEnv(inst *Instance, f *scope.Frame, t *track) *execEnv {
	return &execEnv{
		Instance: inst,
		frame:    f,
		track:    t,
	}
}

// Terminate overrides the instance-level Terminate with the scoped
// semantics (BPMN §13.5.6, ADR-023 §2.5): a Terminate End Event reached
// INSIDE a sub-process terminates only its enclosing scope — the loop
// discards that scope's tokens and the parent continues; at the root (or
// from a track-less frame) it keeps the whole-instance behavior.
func (e *execEnv) Terminate() {
	if e.track == nil || e.track.scopePath == e.sc.root {
		e.Instance.Terminate()

		return
	}

	e.emit(trackEvent{kind: evScopeTerminate, track: e.track})
}

// Escalate raises a non-critical escalation on the executing track (an
// Escalation Intermediate Throw or End Event, BPMN §10.5.6, ADR-006 §2.6): it
// hands the loop an evEscalate carrying the escalation code, and the loop walks
// the track's scope chain to the innermost matching catcher (SRD-058 FR-2).
// Unlike Terminate the throwing token is not torn down — the loop only resolves
// (or logs) the escalation; the token continues or ends on its own. A track-less
// frame has no scope to escalate from, so it is a no-op.
func (e *execEnv) Escalate(code string) {
	if e.track == nil {
		return
	}

	e.emit(trackEvent{kind: evEscalate, track: e.track, escCode: code})
}

// Compensate triggers compensation of completed work (a Compensation throw,
// BPMN §13.5.5, ADR-026, SRD-059 FR-5): it hands the loop an evCompensate
// carrying the target ref ("" = the enclosing scope, reverse completion
// order) and the wait flag; the loop resolves it directly against the
// completion ledger. Used by the fire-and-forget path (wait=false) — a
// wait-for-completion throw parks instead (parkCompensationThrow) and its
// evCompensate is emitted by the park. A track-less frame has no scope to
// compensate from, so it is a no-op.
func (e *execEnv) Compensate(activityRef string, wait bool) {
	if e.track == nil {
		return
	}

	e.emit(trackEvent{
		kind:     evCompensate,
		track:    e.track,
		compRef:  activityRef,
		compWait: wait,
	})
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
