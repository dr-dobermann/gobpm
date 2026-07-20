package renv

import (
	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// RuntimeEnvironment is the environment a node executes against. It is built
// PER EXECUTION by the track (ADR-010 §2.4, SRD-007 FR-3): the engine-level
// extension set and instance identity are shared, while the data surface is
// backed by the execution's own frame — reads resolve frame-first and walk the
// instance's container scopes; results go to the frame and reach the container
// scope only at the frame commit.
//
// It is the public, runtime-facing peer of the read-only service.DataReader
// (ADR-012 §2.3): it embeds the read half and adds write (Put), identity, the
// event producer, and the interaction registrator. The implementation lives in
// internal/instance; pkg/model depends only on this interface.
type RuntimeEnvironment interface {
	EngineRuntime

	// DataReader is the read half — GetData / GetDataByID / GetSources / List
	// (shared with the Go-operation reader, SRD-011).
	service.DataReader

	// Source lets expression evaluation (sequence-flow conditions, gateway
	// decisions) read variables through the environment.
	data.Source

	// InstanceID returns the process instance ID.
	InstanceID() string

	// EventProducer returns the EventProducer of the runtime.
	EventProducer() eventproc.EventProducer

	// Put stores node-produced values in the execution's frame; they reach the
	// container scope at the frame commit.
	Put(dd ...data.Data) error

	// Terminate abnormally ends the whole process instance (a Terminate End
	// Event, BPMN §13.5.6): it cancels the instance context, so every track
	// observes Done() and exits canceled and the instance settles Terminated.
	// Idempotent.
	Terminate()

	// Escalate raises a non-critical escalation with the given code (an
	// Escalation Intermediate Throw or End Event, BPMN §10.5.6, ADR-006 §2.6):
	// the engine walks the throwing execution's scope chain to the innermost
	// matching Escalation catcher (a boundary or an event-sub-process start).
	// Unlike Terminate the throwing token is NOT torn down — it continues (throw)
	// or ends normally (end) — and an unresolved escalation is logged, never a
	// fault (SRD-058 FR-1/FR-4). An empty code catches at a catch-all handler.
	Escalate(code string)
}
