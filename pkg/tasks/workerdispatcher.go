// Package tasks defines the engine's task-routing extensions. This file holds
// the WorkerDispatcher contract (dispatch eligible Tasks to workers per
// SAD-001 §13.2); the TaskDistributor interface (UserTask routing to humans)
// joins this package in M3. The in-process WorkerDispatcher default lives in
// the localdispatcher sibling subpackage. Remote/distributed dispatch is owned
// by a dedicated adapter ADR (ADR-001 v.4 §9); this is the minimal skeleton
// contract.
package tasks

import "context"

// Job is a unit of work dispatched to a worker.
type Job struct {
	// Input is the job's input data, opaque to the dispatcher.
	Input any
	// Type is the job type/topic a handler registers against.
	Type string
	// ID identifies this job instance.
	ID string
}

// Handler executes a job and returns its output (or an error).
type Handler func(ctx context.Context, job Job) (any, error)

// WorkerDispatcher routes jobs to workers for execution.
type WorkerDispatcher interface {
	// Register associates a handler with a job type.
	Register(jobType string, h Handler) error
	// Dispatch executes the job (via its registered handler in the in-process
	// default) and returns the result.
	Dispatch(ctx context.Context, job Job) (any, error)
}
