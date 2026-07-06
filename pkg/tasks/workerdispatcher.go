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
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// Named identifiers keep the extendable interface mixing-proof at compile time:
// a Topic can't be passed where a JobID is expected, and vice versa.
type (
	// JobID identifies one execution of a worker-dispatched ServiceTask
	// (instance+track+node); a worker treats it as an idempotency key.
	JobID string
	// Topic is a job's type/fetch key — a worker fetches by topic, and it
	// equals a ServiceTask's WithWorker topic.
	Topic string
	// WorkerID identifies the worker holding a job's lock.
	WorkerID string
)

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
// Enqueue is engine-facing; FetchAndLock / ExtendLock / Complete / Fail are
// worker-facing. Report methods for the classified outcomes (business status,
// BPMN error) join in SRD-037.
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

	// Fail reports a technical fault; cause identifies it.
	Fail(ctx context.Context, jobID JobID, workerID WorkerID, cause error) error
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
