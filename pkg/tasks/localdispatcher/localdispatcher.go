// Package localdispatcher provides the engine's default WorkerDispatcher
// (ADR-021 §2.4, SRD-036): an in-memory fetch-and-lock job store with per-job
// lock state and a local worker pool. It needs zero extra infrastructure; a
// durable store (ADR-009) and a remote transport (ADR-004) are alternative
// implementations of the same tasks.WorkerDispatcher interface.
package localdispatcher

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock"
	"github.com/dr-dobermann/gobpm/pkg/clock/syscl"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// Dispatcher errors.
var (
	ErrEmptyJobID      = errors.New("localdispatcher: an empty job ID isn't allowed")
	ErrEmptyTopic      = errors.New("localdispatcher: an empty job topic isn't allowed")
	ErrDuplicateJob    = errors.New("localdispatcher: a job with this ID is already queued")
	ErrJobNotFound     = errors.New("localdispatcher: no job with this ID")
	ErrNotLockHolder   = errors.New("localdispatcher: worker isn't the job's lock holder")
	ErrLockExpired     = errors.New("localdispatcher: the job's lock has expired")
	ErrMaxLockExceeded = errors.New("localdispatcher: extension would exceed maxLockDuration")
	ErrNoSink          = errors.New("localdispatcher: no completion sink is bound")
	ErrNilWorker       = errors.New("localdispatcher: a nil worker isn't allowed")
	ErrDuplicateWorker = errors.New("localdispatcher: a worker is already registered for this topic")
)

// defaultMaxLockDuration caps lock extension by default — generous but finite,
// a liveness guard against a worker monopolising a job (ADR-021 §2.4).
const defaultMaxLockDuration = 5 * time.Minute

// WorkerFunc is a local worker's handler: it processes a locked job and returns
// the operation's output (nil if none) or an error. The pool reports Complete
// on success and Fail on error.
type WorkerFunc func(ctx context.Context, job tasks.LockedJob) (*data.ItemDefinition, error)

// jobEntry is a queued job plus its lock state.
type jobEntry struct {
	firstLock time.Time // when this lock was acquired (for the maxLock cap)
	deadline  time.Time // current lock expiry
	job       tasks.Job
	workerID  tasks.WorkerID // "" = unlocked
}

// Dispatcher is the in-memory fetch-and-lock job store (ADR-021 §2.4).
type Dispatcher struct {
	clk     clock.Clock
	sink    tasks.JobCompletionSink
	byID    map[tasks.JobID]*jobEntry
	byTopic map[tasks.Topic][]*jobEntry
	workers map[tasks.Topic]WorkerFunc
	wake    chan struct{}
	maxLock time.Duration
	mu      sync.Mutex
}

// New returns an in-memory dispatcher whose locks are capped at maxLock
// (defaultMaxLockDuration if <= 0). Time comes from clk (a system clock if nil).
func New(clk clock.Clock, maxLock time.Duration) *Dispatcher {
	if clk == nil {
		clk = syscl.New()
	}

	if maxLock <= 0 {
		maxLock = defaultMaxLockDuration
	}

	return &Dispatcher{
		clk:     clk,
		byID:    map[tasks.JobID]*jobEntry{},
		byTopic: map[tasks.Topic][]*jobEntry{},
		workers: map[tasks.Topic]WorkerFunc{},
		wake:    make(chan struct{}, 1),
		maxLock: maxLock,
	}
}

// BindSink sets the completion sink the dispatcher delivers Complete/Fail
// outcomes to. The engine binds itself at startup.
func (d *Dispatcher) BindSink(sink tasks.JobCompletionSink) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.sink = sink
}

// Enqueue adds a job to the queue and wakes any waiting fetcher.
func (d *Dispatcher) Enqueue(_ context.Context, job tasks.Job) error {
	if job.ID == "" {
		return ErrEmptyJobID
	}

	if job.Topic == "" {
		return ErrEmptyTopic
	}

	d.mu.Lock()

	if _, ok := d.byID[job.ID]; ok {
		d.mu.Unlock()

		return ErrDuplicateJob
	}

	e := &jobEntry{job: job}
	d.byID[job.ID] = e
	d.byTopic[job.Topic] = append(d.byTopic[job.Topic], e)
	d.broadcastLocked()

	d.mu.Unlock()

	return nil
}

// FetchAndLock returns and locks the next available job for one of topics,
// blocking until one is available or ctx is done. A job is available if
// unlocked or its lock has expired (worker-crash recovery).
func (d *Dispatcher) FetchAndLock(
	ctx context.Context,
	workerID tasks.WorkerID,
	topics []tasks.Topic,
	lockDuration time.Duration,
) ([]tasks.LockedJob, error) {
	for {
		d.mu.Lock()
		lj, ok := d.lockNext(workerID, topics, lockDuration)
		wake := d.wake
		d.mu.Unlock()

		if ok {
			return []tasks.LockedJob{lj}, nil
		}

		select {
		case <-wake:
			continue // a job was enqueued (broadcast) — re-scan.
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// lockNext locks and returns the first available entry for one of topics.
// Caller holds d.mu.
func (d *Dispatcher) lockNext(
	workerID tasks.WorkerID,
	topics []tasks.Topic,
	lockDuration time.Duration,
) (tasks.LockedJob, bool) {
	now := d.clk.Now()

	for _, topic := range topics {
		for _, e := range d.byTopic[topic] {
			if e.workerID != "" && !now.After(e.deadline) {
				continue // locked and not expired
			}

			e.workerID = workerID
			e.firstLock = now
			e.deadline = now.Add(lockDuration)

			return tasks.LockedJob{
				Job:      e.job,
				WorkerID: workerID,
				Deadline: e.deadline,
			}, true
		}
	}

	return tasks.LockedJob{}, false
}

// ExtendLock extends jobID's lock (held by workerID) by newDuration from now,
// bounded by maxLockDuration measured from the lock's acquisition.
func (d *Dispatcher) ExtendLock(
	_ context.Context,
	jobID tasks.JobID,
	workerID tasks.WorkerID,
	newDuration time.Duration,
) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	e, err := d.heldEntry(jobID, workerID)
	if err != nil {
		return err
	}

	now := d.clk.Now()
	newDeadline := now.Add(newDuration)

	if newDeadline.After(e.firstLock.Add(d.maxLock)) {
		return ErrMaxLockExceeded
	}

	e.deadline = newDeadline

	return nil
}

// Complete reports a successful outcome and removes the job from the store.
func (d *Dispatcher) Complete(
	ctx context.Context,
	jobID tasks.JobID,
	workerID tasks.WorkerID,
	output *data.ItemDefinition,
) error {
	return d.report(ctx, jobID, workerID, tasks.NewWorkerComplete(jobID, output))
}

// Fail reports a technical fault and removes the job from the store.
func (d *Dispatcher) Fail(
	ctx context.Context,
	jobID tasks.JobID,
	workerID tasks.WorkerID,
	cause error,
) error {
	return d.report(ctx, jobID, workerID, tasks.NewWorkerFail(jobID, cause))
}

// report validates the lock, removes the job, and delivers the outcome to the
// bound sink outside the lock (so delivery can't deadlock the store).
func (d *Dispatcher) report(
	ctx context.Context,
	jobID tasks.JobID,
	workerID tasks.WorkerID,
	outcome *tasks.WorkerOutcome,
) error {
	d.mu.Lock()

	e, err := d.heldEntry(jobID, workerID)
	if err != nil {
		d.mu.Unlock()

		return err
	}

	// Check the sink before consuming the job: with no sink the report can't
	// be delivered, so the job must stay in the store (not be lost).
	sink := d.sink
	if sink == nil {
		d.mu.Unlock()

		return ErrNoSink
	}

	d.remove(e)

	d.mu.Unlock()

	return sink.ReportJobCompletion(ctx, outcome)
}

// heldEntry returns the entry for jobID iff workerID holds its unexpired lock.
// Caller holds d.mu.
func (d *Dispatcher) heldEntry(
	jobID tasks.JobID,
	workerID tasks.WorkerID,
) (*jobEntry, error) {
	e, ok := d.byID[jobID]
	if !ok {
		return nil, ErrJobNotFound
	}

	if e.workerID != workerID {
		return nil, ErrNotLockHolder
	}

	if d.clk.Now().After(e.deadline) {
		return nil, ErrLockExpired
	}

	return e, nil
}

// remove deletes e from both indexes. Caller holds d.mu.
func (d *Dispatcher) remove(e *jobEntry) {
	delete(d.byID, e.job.ID)

	q := d.byTopic[e.job.Topic]
	for i, x := range q {
		if x == e {
			d.byTopic[e.job.Topic] = append(q[:i], q[i+1:]...)

			break
		}
	}
}

// RegisterWorker starts an in-process worker for topic: a goroutine that
// fetch-and-locks jobs for topic (until ctx is done), runs fn, and reports
// Complete/Fail. It is the batteries-included local worker (ADR-021 §2.4).
func (d *Dispatcher) RegisterWorker(
	ctx context.Context,
	topic tasks.Topic,
	fn WorkerFunc,
) error {
	if topic == "" {
		return ErrEmptyTopic
	}

	if fn == nil {
		return ErrNilWorker
	}

	d.mu.Lock()

	if _, ok := d.workers[topic]; ok {
		d.mu.Unlock()

		return ErrDuplicateWorker
	}

	d.workers[topic] = fn

	d.mu.Unlock()

	go d.runWorker(ctx, topic, fn)

	return nil
}

// runWorker is a local worker's fetch → run → report loop; it exits when ctx is
// done. workerID is derived from the topic (one local worker per topic).
func (d *Dispatcher) runWorker(
	ctx context.Context,
	topic tasks.Topic,
	fn WorkerFunc,
) {
	workerID := tasks.WorkerID("local:" + string(topic))

	for {
		jobs, err := d.FetchAndLock(ctx, workerID, []tasks.Topic{topic}, d.maxLock)
		if err != nil {
			return // ctx done
		}

		for _, lj := range jobs {
			out, e := fn(ctx, lj)
			if e != nil {
				_ = d.Fail(ctx, lj.ID, workerID, e)

				continue
			}

			_ = d.Complete(ctx, lj.ID, workerID, out)
		}
	}
}

// broadcastLocked wakes all waiting fetchers by closing the current wake channel
// and installing a fresh one. Caller holds d.mu.
func (d *Dispatcher) broadcastLocked() {
	close(d.wake)
	d.wake = make(chan struct{})
}

var (
	_ tasks.WorkerDispatcher = (*Dispatcher)(nil)
	_ tasks.SinkBinder       = (*Dispatcher)(nil)
)
