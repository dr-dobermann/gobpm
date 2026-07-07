// Package tasks defines the engine's external-worker execution contract: an
// asynchronous fetch-and-lock job queue (ADR-021 §2.4, SRD-036). The engine
// Enqueues a job and parks the ServiceTask; a worker FetchAndLocks it, executes,
// and reports (Complete/Fail); the report re-enters the instance loop as a
// WorkerOutcome and resumes the parked track. The in-process default lives in
// the localdispatcher sibling subpackage; a remote adapter (HTTP/gRPC) is a
// future extension (ADR-004).
package tasks

import (
	"context"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// Named identifiers keep the extendable interface mixing-proof at compile time:
// a Topic can't be passed where a JobID is expected, and vice versa.
type (
	// JobID identifies one execution of a worker-dispatched ServiceTask; a
	// worker treats it as an idempotency key. It embeds the owning instance's id
	// (see MakeJobID) so a completion routes back to that instance without a
	// separate registry (SRD-036 §4.5).
	JobID string
	// Topic is a job's type/fetch key — a worker fetches by topic, and it
	// equals a ServiceTask's WithWorker topic.
	Topic string
	// WorkerID identifies the worker holding a job's lock.
	WorkerID string
)

// errorClass tags errors raised by the tasks package (ErrorMapper, options).
const errorClass = "TASKS"

// jobIDSep separates the owning instance id from the unique suffix inside a
// JobID. A worker treats the whole JobID as opaque; the engine splits on the
// first separator to route a completion (InstanceID). It is a character the
// default id generator (UUID) never emits, so the split is unambiguous.
const jobIDSep = "|"

// MakeJobID composes a JobID for a worker-dispatched ServiceTask on instanceID,
// embedding that id so ReportJobCompletion routes the outcome back to the owning
// instance without a registry (SRD-036 §4.5). The suffix is a fresh unique id,
// so two jobs on the same instance never collide.
func MakeJobID(instanceID string) JobID {
	return JobID(instanceID + jobIDSep + foundation.GenerateID())
}

// InstanceID returns the owning instance id embedded in the JobID (the segment
// before the first separator), or the whole string if it carries none.
func (j JobID) InstanceID() string {
	instanceID, _, _ := strings.Cut(string(j), jobIDSep)

	return instanceID
}

// Policy is the per-service execution bundle shipped to a WorkerTrusted worker
// (SRD-038): output mapping, error mapping, and retry policy. It is nil under
// EngineAuthoritative and throughout M2/M3 — a placeholder here that SRD-037/038
// fill in.
type Policy struct{}

// Job is the unit the engine Enqueues. Input is the single bound input-message
// item (nil if the operation has no inMessage), per the operation contract.
type Job struct {
	Input  *data.ItemDefinition
	Policy *Policy
	ID     JobID
	Topic  Topic
}

// LockedJob is a Job a worker received from FetchAndLock, together with its
// lock: WorkerID holds it until Deadline, extendable via ExtendLock.
type LockedJob struct {
	Deadline time.Time
	Job
	WorkerID WorkerID
}

// WorkerDispatcher is an asynchronous fetch-and-lock job queue (ADR-021 §2.4).
// Enqueue is engine-facing; FetchAndLock / ExtendLock and the four terminal
// reports (Complete / ReportBpmnError / ReportStatus / Fail) are worker-facing.
// The reports mirror the four outcome kinds (SRD-037 §2.6): a worker either
// self-classifies (ReportBpmnError / ReportStatus) or reports a raw Fault the
// engine ErrorMapper classifies.
type WorkerDispatcher interface {
	// Enqueue adds a job to the queue (non-blocking); the engine then parks the
	// ServiceTask.
	Enqueue(ctx context.Context, job Job) error

	// FetchAndLock returns and locks (for lockDuration, to workerID) the next
	// available jobs for the given topics, blocking until at least one is
	// available or ctx is done.
	FetchAndLock(
		ctx context.Context,
		workerID WorkerID,
		topics []Topic,
		lockDuration time.Duration,
	) ([]LockedJob, error)

	// ExtendLock extends the lock on jobID (held by workerID) by newDuration
	// from now. Holder-only; bounded by the configured maxLockDuration.
	ExtendLock(
		ctx context.Context,
		jobID JobID,
		workerID WorkerID,
		newDuration time.Duration,
	) error

	// Complete reports a successful outcome: output is the operation's result
	// item (nil if the operation has no outMessage).
	Complete(
		ctx context.Context,
		jobID JobID,
		workerID WorkerID,
		output *data.ItemDefinition,
	) error

	// ReportBpmnError reports a worker-declared Business Error (Camunda
	// handleBpmnError): the engine raises code (message is an optional
	// diagnostic), caught by a matching Error boundary event (interrupting).
	ReportBpmnError(
		ctx context.Context,
		jobID JobID,
		workerID WorkerID,
		code, message string,
	) error

	// ReportStatus reports a worker-declared Business Status: the engine writes
	// value to the ServiceTask's WithStatus variable and the task completes.
	ReportStatus(
		ctx context.Context,
		jobID JobID,
		workerID WorkerID,
		value data.Value,
	) error

	// Fail reports a raw fault the engine ErrorMapper classifies (§2.6). A
	// pure-technical fault carries only Fault.Cause (empty code, nil body) and
	// falls through to the default technical outcome.
	Fail(ctx context.Context, jobID JobID, workerID WorkerID, fault Fault) error
}

// JobCompletionSink routes a worker's terminal report to the owning instance.
// The engine implements it; a dispatcher calls it from Complete/Fail. Keeping
// this separate from WorkerDispatcher keeps the queue decoupled from instance
// internals (SRD-036 §4.1).
type JobCompletionSink interface {
	ReportJobCompletion(ctx context.Context, outcome *WorkerOutcome) error
}

// SinkBinder is an optional dispatcher capability: the engine binds its
// completion sink at startup so the dispatcher can deliver outcomes back. A
// dispatcher that reaches the engine another way (e.g. a remote adapter) need
// not implement it.
type SinkBinder interface {
	BindSink(sink JobCompletionSink)
}

// LoggerBinder is an optional dispatcher capability: the engine binds its
// configured logger (from the runtime config) at startup, so a dispatcher's own
// lifecycle logging uses the embedder's logger rather than a private default. A
// dispatcher that manages its own logging need not implement it.
type LoggerBinder interface {
	BindLogger(logger observability.Logger)
}

// ExternalWorker is implemented by a node whose work is dispatched to an
// external worker rather than run in-process. The instance loop diverts a node
// whose WorkerTopic reports ok == true to the wait-node park path (it enqueues a
// job and resumes on the worker's report); ok == false runs the node in-process
// as usual.
type ExternalWorker interface {
	// WorkerTopic reports the external-worker topic and whether the node is
	// worker-dispatched.
	WorkerTopic() (topic Topic, ok bool)

	// BindJobInput binds the node's operation input message from r (without
	// executing it) — the payload the engine puts in the enqueued Job.Input.
	BindJobInput(
		ctx context.Context, r service.DataReader,
	) (*data.ItemDefinition, error)
}
