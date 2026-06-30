// Package localdispatcher provides the engine's default WorkerDispatcher: an
// in-process executor that runs each job's registered handler under a bounded
// worker pool (a counting semaphore), so it cannot spawn unbounded goroutines
// (the bounded-in-memory-defaults principle, ADR-002 §4.2).
package localdispatcher

import (
	"context"
	"errors"
	"runtime"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// ErrNoHandler is returned when dispatching a job whose type has no handler.
var ErrNoHandler = errors.New("localdispatcher: no handler registered for job type")

// ErrDuplicateHandler is returned when registering a second handler for a type.
var ErrDuplicateHandler = errors.New("localdispatcher: handler already registered for job type")

// ErrNilHandler is returned when registering a nil handler — rejected at the
// boundary so the failure surfaces at Register, not as a deferred nil-call
// panic inside Dispatch.
var ErrNilHandler = errors.New("localdispatcher: a nil handler isn't allowed")

// ErrEmptyJobType is returned when registering with an empty job type.
var ErrEmptyJobType = errors.New("localdispatcher: an empty job type isn't allowed")

// Dispatcher is an in-process tasks.WorkerDispatcher with bounded concurrency.
type Dispatcher struct {
	handlers map[string]tasks.Handler
	sem      chan struct{}
	mu       sync.RWMutex
}

// New returns a Dispatcher whose worker pool allows poolSize concurrent jobs
// (runtime.NumCPU() if poolSize <= 0).
func New(poolSize int) *Dispatcher {
	if poolSize <= 0 {
		poolSize = runtime.NumCPU()
	}

	return &Dispatcher{
		handlers: map[string]tasks.Handler{},
		sem:      make(chan struct{}, poolSize),
	}
}

// Register associates a handler with a job type, rejecting an empty job type,
// a nil handler, and duplicates.
func (d *Dispatcher) Register(jobType string, h tasks.Handler) error {
	if jobType == "" {
		return ErrEmptyJobType
	}

	if h == nil {
		return ErrNilHandler
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.handlers[jobType]; ok {
		return ErrDuplicateHandler
	}

	d.handlers[jobType] = h

	return nil
}

// Dispatch runs the job's handler under the worker pool, blocking for a slot
// (or until ctx is done). It returns ErrNoHandler if the job type is unknown.
func (d *Dispatcher) Dispatch(ctx context.Context, job tasks.Job) (any, error) {
	d.mu.RLock()
	h, ok := d.handlers[job.Type]
	d.mu.RUnlock()

	if !ok {
		return nil, ErrNoHandler
	}

	select {
	case d.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	defer func() { <-d.sem }()

	return h(ctx, job)
}

var _ tasks.WorkerDispatcher = (*Dispatcher)(nil)
