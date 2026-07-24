// Package localdispatcher provides the engine's default WorkerDispatcher
// (ADR-021 §2.4, SRD-036): an in-memory fetch-and-lock job store with per-job
// lock state and a local worker pool. It needs zero extra infrastructure; a
// durable store (ADR-009) and a remote transport (ADR-004) are alternative
// implementations of the same tasks.WorkerDispatcher interface.
package localdispatcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock"
	"github.com/dr-dobermann/gobpm/pkg/clock/syscl"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/observability"
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
	notBefore time.Time // > now while gated for a retry backoff (zero = available)
	job       tasks.Job
	workerID  tasks.WorkerID // "" = unlocked
	attempt   int            // executions run so far (retry count = attempt-1)
}

// Dispatcher is the in-memory fetch-and-lock job store (ADR-021 §2.4).
type Dispatcher struct {
	clk        clock.Clock
	sink       tasks.JobCompletionSink
	logger     observability.Logger
	reporter   observability.Reporter // the engine's observation seam (SRD-041)
	exprEngine expression.Engine      // classifies a raw fault's ErrorMapper (SRD-038)
	byID       map[tasks.JobID]*jobEntry
	byTopic    map[tasks.Topic][]*jobEntry
	workers    map[tasks.Topic]WorkerFunc
	wake       chan struct{}
	maxLock    time.Duration
	mu         sync.Mutex
	// reporterBound is true once the engine installs its shared Reporter via
	// BindReporter; while false the reporter is the echo-only default and
	// BindLogger rebuilds it so job echoes follow the bound logger.
	reporterBound bool
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
		clk: clk,
		// Observability is visible by default (project policy: accidental silence
		// is the worse bug); slog.Default() satisfies observability.Logger.
		logger: slog.Default(),
		// The single-non-nil-Reporter invariant (ADR-013 v.2 §2.7a): the
		// dispatcher always holds a Reporter. Default is echo-only; the engine
		// overrides it with the shared producer via BindReporter at startup.
		reporter: observability.NewEchoReporter(slog.Default()),
		byID:     map[tasks.JobID]*jobEntry{},
		byTopic:  map[tasks.Topic][]*jobEntry{},
		workers:  map[tasks.Topic]WorkerFunc{},
		wake:     make(chan struct{}, 1),
		maxLock:  maxLock,
	}
}

// BindSink sets the completion sink the dispatcher delivers Complete/Fail
// outcomes to. The engine binds itself at startup.
func (d *Dispatcher) BindSink(sink tasks.JobCompletionSink) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.sink = sink
}

// BindLogger sets the dispatcher's logger from the engine's runtime config at
// startup (tasks.LoggerBinder), so the pool's lifecycle logs use the embedder's
// configured logger. A nil logger is ignored — the slog.Default() set in New is
// kept, never erased (logging is on by default; FIX-020 class).
func (d *Dispatcher) BindLogger(logger observability.Logger) {
	if logger == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.logger = logger

	// While no richer Reporter is installed, the reporter is the echo-only
	// default — rebuild it so job-lifecycle echoes use the embedder's logger, not
	// the slog.Default() captured in New (the non-nil-Reporter invariant holds
	// throughout). Once BindReporter installs the engine producer, that owns the
	// echo and this leaves it alone.
	if !d.reporterBound {
		d.reporter = observability.NewEchoReporter(logger)
	}
}

// BindReporter installs the engine's shared Reporter at startup
// (tasks.ReporterBinder, ADR-013 v.2 §3.2), so the dispatcher's JobState facts
// land on the one engine seam (echo + observers). A nil reporter is ignored —
// the echo-only default set in New is kept (the non-nil-Reporter invariant; a
// standalone dispatcher still echoes its job lifecycle).
func (d *Dispatcher) BindReporter(reporter observability.Reporter) {
	if reporter == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.reporter = reporter
	d.reporterBound = true
}

// reportFact announces a JobState fact through the dispatcher's Reporter (the
// seam echoes it at the §2.6 level and fans it out). It reads d.reporter under
// d.mu, so callers already holding d.mu use reportFactLocked. Job-lifecycle facts
// are the catalog record — the emitter never also Logger()s them (ADR-013 v.2
// §2.7a). (Distinct from report(), which delivers a completion outcome to the
// JobCompletionSink.)
func (d *Dispatcher) reportFact(f observability.Fact) {
	d.mu.Lock()
	reporter := d.reporter
	d.mu.Unlock()

	reporter.Report(f)
}

// reportFactLocked is reportFact for callers already holding d.mu.
func (d *Dispatcher) reportFactLocked(f observability.Fact) {
	d.reporter.Report(f)
}

// jobFact builds a JobState fact for phase with the given details.
func jobFact(phase observability.Phase, details map[string]string) observability.Fact {
	return observability.Fact{
		Kind:    observability.KindJobState,
		Phase:   phase,
		Details: details,
	}
}

// BindExpressionEngine sets the engine the dispatcher uses to run a Job's
// ErrorMapper when it classifies a raw fault engine-side (EngineAuthoritative,
// SRD-038). Bound at startup (tasks.ExpressionEngineBinder). A nil engine is
// ignored — a dispatcher with no bound engine treats every raw fault as the
// default technical outcome (no mapper can run).
func (d *Dispatcher) BindExpressionEngine(ee expression.Engine) {
	if ee == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.exprEngine = ee
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

	d.reportFact(jobFact(observability.PhaseEnqueued, map[string]string{
		observability.AttrJobID: string(job.ID),
		observability.AttrTopic: string(job.Topic),
	}))

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
		lj, gate, ok := d.lockNext(workerID, topics, lockDuration)
		wake := d.wake
		now := d.clk.Now()
		d.mu.Unlock()

		if ok {
			d.reportFact(jobFact(observability.PhaseLocked, map[string]string{
				observability.AttrWorkerID: string(workerID),
				observability.AttrJobID:    string(lj.ID),
				observability.AttrTopic:    string(lj.Topic),
			}))

			return []tasks.LockedJob{lj}, nil
		}

		// Nothing available now; if a retry backoff gate is pending, also wake at
		// the nearest one (a nil timer channel never fires — the wake-only case).
		var timer <-chan time.Time
		if !gate.IsZero() {
			timer = d.clk.After(gate.Sub(now))
		}

		select {
		case <-wake:
			continue // a job was enqueued (broadcast) — re-scan.
		case <-timer:
			continue // a retry backoff gate elapsed — re-scan.
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// lockNext locks and returns the first available entry for one of topics. When
// nothing is available it also returns the earliest future retry-backoff gate
// (zero if none) so the caller can wake for it. Caller holds d.mu.
func (d *Dispatcher) lockNext(
	workerID tasks.WorkerID,
	topics []tasks.Topic,
	lockDuration time.Duration,
) (tasks.LockedJob, time.Time, bool) {
	now := d.clk.Now()

	var gate time.Time

	for _, topic := range topics {
		for _, e := range d.byTopic[topic] {
			if e.workerID != "" && !now.After(e.deadline) {
				continue // locked and not expired
			}

			if !e.notBefore.IsZero() && now.Before(e.notBefore) {
				if gate.IsZero() || e.notBefore.Before(gate) {
					gate = e.notBefore // track the nearest backoff gate
				}

				continue // gated for a retry backoff
			}

			if e.workerID != "" {
				// reaching here with a holder means the lock expired (the guard
				// above continued otherwise) — reclaim it for crash recovery. A
				// worker that missed its deadline is degradation someone should
				// see, not mere flow tracing (ADR-022 v.1 §2.4): LockReclaimed
				// echoes at Warn (the level table). Under d.mu → reportFactLocked.
				d.reportFactLocked(jobFact(observability.PhaseLockReclaimed,
					map[string]string{
						observability.AttrJobID:    string(e.job.ID),
						observability.AttrWorkerID: string(e.workerID),
					}))
			}

			e.workerID = workerID
			e.firstLock = now
			e.deadline = now.Add(lockDuration)
			e.notBefore = time.Time{} // consumed — the gate is now handed out

			return tasks.LockedJob{
				Job:      e.job,
				WorkerID: workerID,
				Deadline: e.deadline,
			}, time.Time{}, true
		}
	}

	return tasks.LockedJob{}, gate, false
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

	d.logger.Debug("job lock extended",
		"job_id", string(jobID), "worker_id", string(workerID),
		"deadline", newDeadline)

	return nil
}

// Complete reports a successful outcome and removes the job from the store. Under
// EngineAuthoritative the dispatcher owns output mapping (SRD-039 §3.4): it shapes
// the worker's raw output via the job's Policy.OutputMapping (over the bound
// expression engine) into the final committed data before delivering, so the
// track only commits. A required output path the body doesn't satisfy is a
// contract violation — reported as a terminal technical fault, not retried.
func (d *Dispatcher) Complete(
	ctx context.Context,
	jobID tasks.JobID,
	workerID tasks.WorkerID,
	output *data.ItemDefinition,
) error {
	d.mu.Lock()

	e, err := d.heldEntry(jobID, workerID)
	if err != nil {
		d.mu.Unlock()

		return err
	}

	policy := e.job.Policy
	ee := d.exprEngine
	d.mu.Unlock()

	res, mapErr := mapOutput(ctx, ee, policy, output)
	if mapErr != nil {
		return d.report(ctx, jobID, workerID,
			tasks.NewWorkerFault(jobID, tasks.Fault{Cause: mapErr}))
	}

	return d.report(ctx, jobID, workerID, tasks.NewWorkerComplete(jobID, res))
}

// mapOutput shapes a worker's raw completion output into the final committed data
// (SRD-039 §3.4): a nil output commits nothing; a job with Policy.OutputMapping
// runs ApplyOutputMapping (a required path the body doesn't satisfy errors); an
// unmapped output is committed directly (the M3 direct-reconciliation default).
func mapOutput(
	ctx context.Context,
	ee expression.Engine,
	policy *tasks.Policy,
	output *data.ItemDefinition,
) ([]data.Data, error) {
	if output == nil {
		return nil, nil
	}

	if policy != nil && len(policy.OutputMapping) > 0 && ee != nil {
		return tasks.ApplyOutputMapping(ctx, ee, policy.OutputMapping, output)
	}

	iae, err := data.NewItemAwareElement(output, data.ReadyDataState)
	if err != nil {
		return nil, fmt.Errorf(
			"localdispatcher: couldn't wrap job output %q: %w",
			output.ID(), err)
	}

	datum, err := data.NewParameter(output.ID(), iae)
	if err != nil {
		return nil, fmt.Errorf(
			"localdispatcher: couldn't build job output datum %q: %w",
			output.ID(), err)
	}

	return []data.Data{datum}, nil
}

// ReportBpmnError reports a worker-declared Business Error and removes the job.
func (d *Dispatcher) ReportBpmnError(
	ctx context.Context,
	jobID tasks.JobID,
	workerID tasks.WorkerID,
	code, message string,
) error {
	return d.report(ctx, jobID, workerID,
		tasks.NewWorkerBpmnError(jobID, code, message))
}

// ReportStatus reports a worker-declared Business Status and removes the job.
func (d *Dispatcher) ReportStatus(
	ctx context.Context,
	jobID tasks.JobID,
	workerID tasks.WorkerID,
	value data.Value,
) error {
	return d.report(ctx, jobID, workerID, tasks.NewWorkerStatus(jobID, value))
}

// Fail reports a raw fault. The dispatcher classifies it engine-side via the
// job's Policy.ErrorMapper (EngineAuthoritative, SRD-038 §3.4): a Business Error
// or Status is a terminal verdict delivered to the sink; a Technical fault is
// run through the Policy.RetryPolicy — a retry re-arms the job for a later
// re-fetch (no delivery, the track stays parked), an exhausted policy delivers
// the terminal fault. Classification runs outside d.mu while the reporting
// worker still holds the job's (unexpired) lock, so no concurrent fetch can take
// it; the retry decision re-acquires d.mu and re-validates the entry.
func (d *Dispatcher) Fail(
	ctx context.Context,
	jobID tasks.JobID,
	workerID tasks.WorkerID,
	fault tasks.Fault,
) error {
	d.mu.Lock()

	e, err := d.heldEntry(jobID, workerID)
	if err != nil {
		d.mu.Unlock()

		return err
	}

	policy := e.job.Policy
	ee := d.exprEngine
	logger := d.logger
	d.mu.Unlock()

	outcome := classify(ctx, logger, jobID, policy, ee, fault)

	// A business verdict (BpmnError / Status) is never retried — deliver it.
	if outcome.Kind() != tasks.OutcomeFault {
		return d.report(ctx, jobID, workerID, outcome)
	}

	// Technical fault — consult the retry policy (SRD-038 §3.4, FR-7/FR-8).
	return d.retryOrExhaust(ctx, jobID, workerID, policy, fault)
}

// retryOrExhaust applies the job's RetryPolicy to a technical fault: while it
// retries, the entry is re-armed (unlocked, gated by a notBefore backoff, and
// its attempt count incremented) for a later re-fetch, with no delivery so the
// track stays parked; once exhausted (or with no policy) the terminal technical
// fault is delivered. Re-validates the entry under d.mu before acting.
func (d *Dispatcher) retryOrExhaust(
	ctx context.Context,
	jobID tasks.JobID,
	workerID tasks.WorkerID,
	policy *tasks.Policy,
	fault tasks.Fault,
) error {
	d.mu.Lock()

	e, err := d.heldEntry(jobID, workerID)
	if err != nil {
		d.mu.Unlock()

		return err
	}

	attempt := e.attempt + 1

	if backoff, retry := retryDecision(policy, attempt, fault.Cause); retry {
		e.workerID = ""
		e.attempt = attempt
		e.notBefore = d.clk.Now().Add(backoff)
		d.broadcastLocked()
		d.mu.Unlock()

		d.reportFact(jobFact(observability.PhaseRetryScheduled, map[string]string{
			observability.AttrJobID:    string(jobID),
			observability.AttrAttempts: strconv.Itoa(attempt),
			observability.AttrBackoff:  backoff.String(),
		}))

		return nil
	}

	topic := e.job.Topic
	d.mu.Unlock()

	// RetriesExhausted echoes at Warn (the level table) — a job giving up is
	// degradation an operator should see (ADR-022 v.1 §2.4).
	d.reportFact(jobFact(observability.PhaseRetriesExhausted, map[string]string{
		observability.AttrJobID:    string(jobID),
		observability.AttrTopic:    string(topic),
		observability.AttrAttempts: strconv.Itoa(attempt),
	}))

	return d.report(ctx, jobID, workerID, tasks.NewWorkerFault(jobID, fault))
}

// retryDecision consults policy's RetryPolicy for the just-failed attempt; a nil
// policy or RetryPolicy is a no-retry (terminal on first technical fault).
func retryDecision(
	policy *tasks.Policy, attempt int, cause error,
) (time.Duration, bool) {
	if policy == nil || policy.RetryPolicy == nil {
		return 0, false
	}

	return policy.RetryPolicy.Retry(attempt, cause)
}

// classify runs the job's ErrorMapper over a raw fault and returns the mapped
// outcome (SRD-038 §3.4): a Business Error / Status verdict, or a technical
// OutcomeFault. A nil policy / nil mapper / nil engine, or a mapper error, falls
// through to the technical outcome. The caller retries a technical outcome (via
// the RetryPolicy) before it becomes terminal; a verdict is delivered as-is.
func classify(
	ctx context.Context,
	logger observability.Logger,
	jobID tasks.JobID,
	policy *tasks.Policy,
	ee expression.Engine,
	fault tasks.Fault,
) *tasks.WorkerOutcome {
	if policy == nil || policy.ErrorMapper == nil || ee == nil {
		return tasks.NewWorkerFault(jobID, fault)
	}

	mapped, err := policy.ErrorMapper.Classify(ctx, ee, fault)
	if err != nil {
		logger.Warn("error-mapping failed; treating as a technical fault",
			"job_id", string(jobID), "error", err.Error())

		return tasks.NewWorkerFault(jobID, fault)
	}

	switch o := mapped.(type) {
	case tasks.BpmnError:
		return tasks.NewWorkerBpmnError(jobID, o.Code, o.Message)

	case tasks.Status:
		return tasks.NewWorkerStatus(jobID, o.Value)

	default: // tasks.Technical (sealed interface — the only remaining kind)
		return tasks.NewWorkerFault(jobID, fault)
	}
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

	// The terminal JobState: the outcome kind maps to the phase (Completed for a
	// success, BusinessError for a worker-declared BPMN error / status,
	// TechnicalFault for a raw fault). One emission at the single delivery point
	// (SRD-041 §3.4).
	d.reportFact(jobFact(outcomePhase[outcome.Kind()], map[string]string{
		observability.AttrJobID:    string(jobID),
		observability.AttrWorkerID: string(workerID),
	}))

	return sink.ReportJobCompletion(ctx, outcome)
}

// outcomePhase maps a worker outcome kind to its terminal JobState phase — a data
// table, not a switch.
var outcomePhase = map[tasks.OutcomeKind]observability.Phase{
	tasks.OutcomeComplete:  observability.PhaseCompleted,
	tasks.OutcomeBpmnError: observability.PhaseBusinessError,
	tasks.OutcomeStatus:    observability.PhaseBusinessError,
	tasks.OutcomeFault:     observability.PhaseTechnicalFault,
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

	logger := d.logger
	d.mu.Unlock()

	logger.Info("registered local worker", "topic", string(topic))

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
			// FetchAndLock returns a non-nil error ONLY on ctx cancellation
			// (its sole error path) — the worker-pool shutdown signal, an
			// expected exit with nothing to surface (ADR-022 v.1 §2.3). If a
			// future change adds another error mode here, revisit to log it.
			return
		}

		for _, lj := range jobs {
			// WorkerTrusted: the worker runs the policy in-process and reports a
			// verdict (SRD-039 M10). Otherwise (EngineAuthoritative / no policy) the
			// worker returns raw and the dispatcher owns the policy.
			if lj.Policy != nil && lj.Policy.Trust == tasks.WorkerTrusted {
				d.runTrusted(ctx, lj, workerID, fn)

				continue
			}

			out, e := fn(ctx, lj)
			if e != nil {
				// a pooled worker's plain error is a raw technical fault (no
				// code/body → the ErrorMapper falls through to default technical).
				if rerr := d.Fail(ctx, lj.ID, workerID,
					tasks.Fault{Cause: e}); rerr != nil {
					d.logger.Warn("local worker failed to report a job fault",
						"topic", topic, "job_id", string(lj.ID),
						"fault", e.Error(), "report_error", rerr.Error())
				}

				continue
			}

			if rerr := d.Complete(ctx, lj.ID, workerID, out); rerr != nil {
				d.logger.Warn("local worker failed to report a job completion",
					"topic", topic, "job_id", string(lj.ID),
					"error", rerr.Error())
			}
		}
	}
}

// runTrusted runs a WorkerTrusted job's policy in-process (SRD-039 M10, FR-7): it
// calls fn, maps a success, honors a *WorkerError's self-classification (or the
// fallback ErrorMapper for a plain error), and retries a technical fault within
// the held lock window — reporting only a final verdict, which the dispatcher
// forwards via report (no re-classify / re-map / retry). The worker owns retry
// accounting; the whole sequence runs under the single maxLock lease it holds.
func (d *Dispatcher) runTrusted(
	ctx context.Context,
	lj tasks.LockedJob,
	workerID tasks.WorkerID,
	fn WorkerFunc,
) {
	for attempt := 1; ; attempt++ {
		out, err := fn(ctx, lj)

		// Success — the worker maps its output, then reports a completion.
		if err == nil {
			res, mapErr := mapOutput(ctx, d.exprEngine, lj.Policy, out)
			if mapErr != nil {
				d.reportTrusted(ctx, lj.ID, workerID,
					tasks.NewWorkerFault(lj.ID, tasks.Fault{Cause: mapErr}))

				return
			}

			d.reportTrusted(ctx, lj.ID, workerID,
				tasks.NewWorkerComplete(lj.ID, res))

			return
		}

		// A rich *WorkerError self-classifies a business outcome directly.
		var we *tasks.WorkerError
		if errors.As(err, &we) {
			if verdict, ok := businessVerdict(lj.ID, we); ok {
				d.reportTrusted(ctx, lj.ID, workerID, verdict)

				return
			}
		}

		// Technical (a plain error or a technical *WorkerError) — the fallback
		// ErrorMapper may still reclassify it as a business outcome.
		outcome := classify(ctx, d.logger, lj.ID, lj.Policy, d.exprEngine,
			tasks.Fault{Cause: err})
		if outcome.Kind() != tasks.OutcomeFault {
			d.reportTrusted(ctx, lj.ID, workerID, outcome)

			return
		}

		// Still technical — retry in-process, but only if the backoff fits inside
		// the held lock window; otherwise report the terminal fault (exhausted),
		// never letting the lock lapse into a re-fetch (NFR-2).
		backoff, retry := retryDecision(lj.Policy, attempt, err)
		if !retry || !d.clk.Now().Add(backoff).Before(lj.Deadline) {
			d.reportTrusted(ctx, lj.ID, workerID, outcome)

			return
		}

		select {
		case <-d.clk.After(backoff):
		case <-ctx.Done():
			return
		}
	}
}

// businessVerdict turns a *WorkerError's self-classification into a business
// outcome (BpmnError over Status); ok == false for a technical-only WorkerError.
func businessVerdict(
	jobID tasks.JobID, we *tasks.WorkerError,
) (*tasks.WorkerOutcome, bool) {
	switch {
	case we.BpmnErrorCode != "":
		return tasks.NewWorkerBpmnError(jobID, we.BpmnErrorCode, we.Message), true

	case we.Status != nil:
		return tasks.NewWorkerStatus(jobID, we.Status), true

	default:
		return nil, false
	}
}

// reportTrusted delivers a WorkerTrusted verdict through the sink (removing the
// job), logging a delivery failure like the raw-report path.
func (d *Dispatcher) reportTrusted(
	ctx context.Context,
	jobID tasks.JobID,
	workerID tasks.WorkerID,
	outcome *tasks.WorkerOutcome,
) {
	if err := d.report(ctx, jobID, workerID, outcome); err != nil {
		d.logger.Warn("local trusted worker failed to report a verdict",
			"job_id", string(jobID), "kind", outcome.Kind().String(),
			"error", err.Error())
	}
}

// broadcastLocked wakes all waiting fetchers by closing the current wake channel
// and installing a fresh one. Caller holds d.mu.
func (d *Dispatcher) broadcastLocked() {
	close(d.wake)
	d.wake = make(chan struct{})
}

var (
	_ tasks.WorkerDispatcher       = (*Dispatcher)(nil)
	_ tasks.SinkBinder             = (*Dispatcher)(nil)
	_ tasks.LoggerBinder           = (*Dispatcher)(nil)
	_ tasks.ExpressionEngineBinder = (*Dispatcher)(nil)
	_ tasks.ReporterBinder         = (*Dispatcher)(nil)
)
