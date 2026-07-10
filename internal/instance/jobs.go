package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
)

// jobRequest is a worker's terminal report (Complete/Fail, carried as a
// WorkerOutcome) handed to the instance loop so the job→track resolution and the
// resume run on the single-writer goroutine, mirroring taskRequest (SRD-036
// §4.5). The caller blocks on reply for the synchronous verdict.
type jobRequest struct {
	outcome *tasks.WorkerOutcome
	reply   chan error
}

// ReportJobCompletion delivers a worker's terminal outcome for a parked
// worker-dispatched ServiceTask to the instance loop, which resolves the job to
// its parked track and resumes it (binding the output, or faulting on a Fail
// outcome). It implements tasks.JobCompletionSink for one instance; the engine
// routes an outcome to the owning instance by the instance id embedded in the
// job id, then calls this (SRD-036 §4.5). A nil outcome is rejected.
func (inst *Instance) ReportJobCompletion(
	ctx context.Context,
	outcome *tasks.WorkerOutcome,
) error {
	if outcome == nil {
		return errs.New(
			errs.M("ReportJobCompletion: a nil outcome isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	reply := make(chan error, 1)

	select {
	case inst.jobReq <- jobRequest{outcome: outcome, reply: reply}:
	case <-inst.loopDone:
		return errs.New(
			errs.M("instance %q is not running", inst.ID()),
			errs.C(errorClass, errs.InvalidState))
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-reply:
		return err
	case <-inst.loopDone:
		return errs.New(
			errs.M("instance %q stopped before job reply", inst.ID()),
			errs.C(errorClass, errs.InvalidState))
	case <-ctx.Done():
		return ctx.Err()
	}
}

// handleJobCompletion resolves the outcome's job to its parked track and resumes
// it by delivering the WorkerOutcome to the track's evtCh, mirroring completeTask
// (SRD-036 §4.5). Runs on the loop goroutine.
func (ls *loopState) handleJobCompletion(req jobRequest) {
	jobID := req.outcome.JobID()

	tr, ok := ls.jobs[jobID]
	if !ok {
		req.reply <- errs.New(
			errs.M("job %q not found or already completed", string(jobID)),
			errs.C(errorClass, errs.ObjectNotFound))

		return
	}

	// A job in the registry is always still parked: onJobWaiting adds it to both
	// jobs and waiting, handleJobCompletion removes it from both — all on this loop
	// goroutine. So flip it out and deliver on its own evtCh, where the loop is the
	// sole sender and it is parked-and-undelivered (SRD-027). The track wakes,
	// ProcessEvent stashes the outcome, and Exec binds the output (or faults on a
	// Fail outcome). SRD-036 §4.4.
	ls.flipNotParked(tr)
	delete(ls.jobs, jobID)
	tr.evtCh <- req.outcome

	req.reply <- nil
}

// onJobWaiting binds the worker-dispatched ServiceTask's input from process
// scope, enqueues a job for it on the WorkerDispatcher, and records the parked
// track so the worker's report can resume it — all on the loop goroutine, unless
// the instance is shutting down (SRD-036 §4.3). Binding on the loop goroutine (not
// the parked track's) keeps scope access single-writer, mirroring authorizeTask.
func (ls *loopState) onJobWaiting(ctx context.Context, ev trackEvent) {
	if ls.stopping {
		return
	}

	jobID := tasks.JobID(ev.taskID)

	// ev.node is always a ServiceTask marked WithWorker (only parkServiceTask emits
	// evJobWaiting), and ServiceTask implements ExternalWorker, so this cannot fail.
	ew, _ := ev.node.(tasks.ExternalWorker)

	if err := ls.inst.enqueueJob(ctx, ev, ew, jobID); err != nil {
		// binding or enqueue failed — resume the parked track with a fault so the
		// instance surfaces it instead of parking forever with no job. The track was
		// never registered (below), so deliver straight to its buffered evtCh where
		// the loop is the sole sender; the track wakes and Exec faults. SRD-036 §4.3.
		ev.track.evtCh <- tasks.NewWorkerFault(jobID, tasks.Fault{Cause: err})

		return
	}

	// Enqueued — record the track as parked-and-undelivered and in the job registry
	// so the worker's report (via jobReq) resumes it. The report is serviced on this
	// same loop goroutine (jobReq), strictly after this returns, so registering last
	// cannot race a fast worker (SRD-036 §4.5).
	ls.waiting[ev.track.ID()] = struct{}{}
	ls.jobs[jobID] = ev.track
}

// enqueueJob opens a transient root frame for the parked ServiceTask, binds its
// operation input message from process scope, and enqueues the resulting job. The
// frame is opened and discarded here (loop goroutine) so the bind stays
// single-writer with the rest of scope access (SRD-036 §4.3).
func (inst *Instance) enqueueJob(
	ctx context.Context,
	ev trackEvent,
	ew tasks.ExternalWorker,
	jobID tasks.JobID,
) error {
	frame, err := inst.sc.openFrame(ev.track.ID(), ev.node.ID())
	if err != nil {
		return err
	}
	defer frame.Discard()

	input, err := ew.BindJobInput(ctx, newExecEnv(inst, frame))
	if err != nil {
		return err
	}

	topic, _ := ew.WorkerTopic()

	return inst.WorkerDispatcher().Enqueue(ctx, tasks.Job{
		ID:     jobID,
		Topic:  topic,
		Input:  input,
		Policy: inst.resolveWorkerPolicy(ew),
	})
}

// resolveWorkerPolicy resolves the enqueued job's outcome policy two-level
// (SRD-038 §3.6): the node's per-service config (tasks.WorkerConfig) over the
// engine-wide defaults. The ErrorMapper may stay nil (a raw fault then falls
// through to the default technical outcome); the RetryPolicy is always non-nil
// (tasks.DefaultRetryPolicy when neither level sets one). The dispatcher uses
// both to classify and retry a raw fault engine-side.
func (inst *Instance) resolveWorkerPolicy(ew tasks.ExternalWorker) *tasks.Policy {
	var ps tasks.Policy

	if wc, ok := ew.(tasks.WorkerConfig); ok {
		ps, _ = wc.WorkerConfig()
	}

	em := ps.ErrorMapper
	if em == nil {
		em = inst.WorkerErrorMapper()
	}

	rp := ps.RetryPolicy
	if rp == nil {
		rp = inst.WorkerRetryPolicy()
	}

	if rp == nil {
		rp = tasks.DefaultRetryPolicy()
	}

	// Trust resolves two-level: per-service over engine-wide over WorkerTrusted
	// (the ADR-021 default) — Resolve maps an unset mode to its fallback (SRD-039
	// M9). OutputMapping is per-service only (node-specific shaping — no
	// engine-wide default); ship it so the policy owner maps the completion.
	trust := ps.Trust.Resolve(
		inst.WorkerTrustDefault().Resolve(tasks.WorkerTrusted))

	return &tasks.Policy{
		ErrorMapper:   em,
		RetryPolicy:   rp,
		OutputMapping: ps.OutputMapping,
		Trust:         trust,
	}
}

// cleanupJob drops any job owned by a track that ended without completing it
// (canceled by an interrupting boundary or instance terminate). The enqueued job
// is left for the dispatcher to expire — the engine has no withdraw yet — so a
// late worker report finds no track and is dropped (SRD-036).
func (ls *loopState) cleanupJob(tr *track) {
	for id, t := range ls.jobs {
		if t == tr {
			delete(ls.jobs, id)
		}
	}
}
