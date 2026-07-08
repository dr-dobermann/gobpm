package instance

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/stretchr/testify/require"
)

// capDispatcher is a WorkerDispatcher spy: Enqueue records the enqueued jobs (so a
// test can read the minted JobID and drive completion via Instance.ReportJobCompletion
// directly), and enqErr — when set — makes Enqueue fail so the onJobWaiting fault
// path is exercised. The worker-facing methods are unused by the instance-level
// tests (they call ReportJobCompletion, not the dispatcher's own report path).
type capDispatcher struct {
	enqErr error
	mu     sync.Mutex
	jobs   []tasks.Job
}

func (d *capDispatcher) Enqueue(_ context.Context, job tasks.Job) error {
	if d.enqErr != nil {
		return d.enqErr
	}

	d.mu.Lock()
	d.jobs = append(d.jobs, job)
	d.mu.Unlock()

	return nil
}

func (d *capDispatcher) FetchAndLock(
	context.Context, tasks.WorkerID, []tasks.Topic, time.Duration,
) ([]tasks.LockedJob, error) {
	return nil, nil
}

func (d *capDispatcher) ExtendLock(
	context.Context, tasks.JobID, tasks.WorkerID, time.Duration,
) error {
	return nil
}

func (d *capDispatcher) Complete(
	context.Context, tasks.JobID, tasks.WorkerID, *data.ItemDefinition,
) error {
	return nil
}

func (d *capDispatcher) ReportBpmnError(
	context.Context, tasks.JobID, tasks.WorkerID, string, string,
) error {
	return nil
}

func (d *capDispatcher) ReportStatus(
	context.Context, tasks.JobID, tasks.WorkerID, data.Value,
) error {
	return nil
}

func (d *capDispatcher) Fail(
	context.Context, tasks.JobID, tasks.WorkerID, tasks.Fault,
) error {
	return nil
}

// lastJob returns the most recently enqueued job, or false if none.
func (d *capDispatcher) lastJob() (tasks.Job, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.jobs) == 0 {
		return tasks.Job{}, false
	}

	return d.jobs[len(d.jobs)-1], true
}

// spyImpl is a service Implementor that records whether it ran and always errors.
// A worker-dispatched ServiceTask must NOT run its in-process executor (the worker
// is the executor), so ran must stay false on the worker path.
type spyImpl struct {
	ran atomic.Bool
}

func (s *spyImpl) Type() string { return "##spy" }

func (s *spyImpl) ErrorClasses() []string { return nil }

func (s *spyImpl) Execute(
	context.Context, *data.ItemDefinition,
) (*data.ItemDefinition, error) {
	s.ran.Store(true)

	return nil, errors.New("in-process executor must not run on the worker path")
}

// serviceTaskWorkerInst builds and runs an instance of start → ServiceTask(op,
// WithWorker("topic-x")) → end backed by disp, so the ServiceTask parks as an
// external-worker wait node and enqueues a job on disp.
func serviceTaskWorkerInst(
	t *testing.T,
	disp tasks.WorkerDispatcher,
	op service.Operation,
) (*Instance, context.CancelFunc) {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("st-worker")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	st, err := activities.NewServiceTask("svc", op,
		activities.WithWorker("topic-x"), activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, st, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, st)
	require.NoError(t, err)
	_, err = flow.Link(st, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	rt := enginert.Default().WithWorkerDispatcher(disp)
	inst, err := New(s, scope.EmptyDataPath, rt,
		mockeventproc.NewMockEventProducer(t), &failDist{})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, inst.Run(ctx))

	return inst, cancel
}

// waitForJob blocks until disp has captured an enqueued job and returns it.
func waitForJob(t *testing.T, disp *capDispatcher) tasks.Job {
	t.Helper()

	var job tasks.Job

	require.Eventually(t, func() bool {
		j, ok := disp.lastJob()
		job = j

		return ok
	}, 2*time.Second, 5*time.Millisecond)

	return job
}

// TestServiceTaskWorkerParksAndEnqueues covers the park path (SRD-036 FR-1/FR-3):
// a WithWorker ServiceTask parks instead of running in-process, and the loop
// enqueues a job on the task's topic whose id embeds the owning instance so a
// completion routes back.
func TestServiceTaskWorkerParksAndEnqueues(t *testing.T) {
	disp := &capDispatcher{}
	inst, cancel := serviceTaskWorkerInst(t, disp,
		service.MustOperation("op", nil, nil, nil))
	defer cancel()

	job := waitForJob(t, disp)

	require.Equal(t, tasks.Topic("topic-x"), job.Topic)
	require.Equal(t, inst.ID(), job.ID.InstanceID(),
		"the job id embeds its owning instance for routing")
	require.Equal(t, Active, inst.State(), "the task stays parked, not completed")
}

// TestServiceTaskWorkerCompleteResumesAndBinds covers the resume path (SRD-036
// FR-4/FR-5): a worker's Complete outcome resumes the parked track, binds the
// output, and advances the token to the end event.
func TestServiceTaskWorkerCompleteResumesAndBinds(t *testing.T) {
	disp := &capDispatcher{}
	inst, cancel := serviceTaskWorkerInst(t, disp,
		service.MustOperation("op", nil, nil, nil))
	defer cancel()

	job := waitForJob(t, disp)

	output := data.MustItemDefinition(values.NewVariable("worker-result"))
	require.NoError(t, inst.ReportJobCompletion(context.Background(),
		tasks.NewWorkerComplete(job.ID, output)))

	require.Eventually(t, func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)
}

// TestServiceTaskWorkerFailFaults covers the fault path (SRD-036 FR-6): a worker's
// Fail outcome faults the parked task, terminating the instance with the cause.
func TestServiceTaskWorkerFailFaults(t *testing.T) {
	disp := &capDispatcher{}
	inst, cancel := serviceTaskWorkerInst(t, disp,
		service.MustOperation("op", nil, nil, nil))
	defer cancel()

	job := waitForJob(t, disp)

	require.NoError(t, inst.ReportJobCompletion(context.Background(),
		tasks.NewWorkerFault(job.ID, tasks.Fault{Cause: errors.New("boom")})))

	require.Eventually(t, func() bool { return inst.State() == Terminated },
		2*time.Second, 5*time.Millisecond)
	require.ErrorContains(t, inst.LastErr(), "worker reported a technical fault")
}

// TestServiceTaskWorkerExecutorIgnored covers §2.5: the operation's in-process
// executor is never run on the worker path — the worker is the executor. The spy
// executor would error if run; the job completes via the worker outcome instead.
func TestServiceTaskWorkerExecutorIgnored(t *testing.T) {
	spy := &spyImpl{}
	disp := &capDispatcher{}
	inst, cancel := serviceTaskWorkerInst(t, disp,
		service.MustOperation("op", nil, nil, spy))
	defer cancel()

	job := waitForJob(t, disp)

	require.NoError(t, inst.ReportJobCompletion(context.Background(),
		tasks.NewWorkerComplete(job.ID, nil)))

	require.Eventually(t, func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)
	require.False(t, spy.ran.Load(), "the in-process executor must not run")
}

// TestReportJobCompletionRoutesToParkedTrack covers the sink guards and the
// job→track resolution (SRD-036 §4.5): a nil outcome and an unknown job are
// rejected, a known job resumes its track, and once the loop is gone a report is
// refused.
func TestReportJobCompletionRoutesToParkedTrack(t *testing.T) {
	disp := &capDispatcher{}
	inst, cancel := serviceTaskWorkerInst(t, disp,
		service.MustOperation("op", nil, nil, nil))
	defer cancel()

	job := waitForJob(t, disp)
	ctx := context.Background()

	// a nil outcome is rejected before the loop.
	require.Error(t, inst.ReportJobCompletion(ctx, nil))

	// an unknown job id is rejected at the loop (not in the registry).
	require.Error(t, inst.ReportJobCompletion(ctx,
		tasks.NewWorkerComplete(tasks.MakeJobID(inst.ID()), nil)))

	// the real job resumes the parked track to completion.
	require.NoError(t, inst.ReportJobCompletion(ctx,
		tasks.NewWorkerComplete(job.ID, nil)))

	require.Eventually(t, func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)

	// once the loop is gone, a report is refused (loopDone path).
	<-inst.Done()
	require.Error(t, inst.ReportJobCompletion(context.Background(),
		tasks.NewWorkerComplete(job.ID, nil)))
}

// TestServiceTaskWorkerEnqueueFailureFaults covers the onJobWaiting fault branch:
// when the dispatcher's Enqueue fails, the parked track is resumed with a fault so
// the instance surfaces it instead of parking forever (SRD-036 §4.3).
func TestServiceTaskWorkerEnqueueFailureFaults(t *testing.T) {
	disp := &capDispatcher{enqErr: errors.New("enqueue boom")}
	inst, cancel := serviceTaskWorkerInst(t, disp,
		service.MustOperation("op", nil, nil, nil))
	defer cancel()

	require.Eventually(t, func() bool { return inst.State() == Terminated },
		2*time.Second, 5*time.Millisecond)
	require.ErrorContains(t, inst.LastErr(), "worker reported a technical fault")
}

// guardedWorkerInstance builds start → host(ServiceTask, WithWorker) → normalEnd
// with an interrupting signal boundary host → excEnd, backed by disp and ep. The
// worker task parks (enqueuing a job); the boundary can then cancel it.
func guardedWorkerInstance(
	t *testing.T,
	ep *recordingProducer,
	disp tasks.WorkerDispatcher,
) (*Instance, flow.EventDefinition) {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("st-worker-guarded")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	host, err := activities.NewServiceTask("host",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("topic-x"), activities.WithoutParams())
	require.NoError(t, err)

	normalEnd, err := events.NewEndEvent("normal-end")
	require.NoError(t, err)

	sig, err := events.NewSignal("sigW", nil)
	require.NoError(t, err)

	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	be, err := events.NewBoundaryEvent("bndW", host, def, true)
	require.NoError(t, err)

	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, host, normalEnd, be, excEnd} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, host)
	require.NoError(t, err)
	_, err = flow.Link(host, normalEnd)
	require.NoError(t, err)
	_, err = flow.Link(be, excEnd)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	rt := enginert.Default().WithWorkerDispatcher(disp)
	inst, err := New(s, scope.EmptyDataPath, rt, ep, &failDist{})
	require.NoError(t, err)

	return inst, def
}

// TestServiceTaskWorkerBoundaryInterruptDropsJob covers the interrupting-boundary
// interaction with a parked worker task (SRD-036 × SRD-029): a boundary firing
// while the ServiceTask waits on its worker cancels the parked track, drops its
// job from the registry (cleanupJob), and the instance continues on the exception
// flow — the worker's later report finds no track.
func TestServiceTaskWorkerBoundaryInterruptDropsJob(t *testing.T) {
	ep := &recordingProducer{}
	disp := &capDispatcher{}
	inst, sigDef := guardedWorkerInstance(t, ep, disp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, inst.Run(ctx))

	// the task parked (job enqueued) and the boundary armed over it.
	job := waitForJob(t, disp)
	require.Eventually(t, func() bool { return ep.capturedWatch() != nil },
		2*time.Second, 5*time.Millisecond)

	// fire the boundary as the hub would — it cancels the parked worker task.
	require.NoError(t, ep.capturedWatch().ProcessEvent(ctx, sigDef))

	require.Eventually(t, func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)

	// the worker's late report finds no parked track (its job was dropped).
	require.Error(t, inst.ReportJobCompletion(context.Background(),
		tasks.NewWorkerComplete(job.ID, nil)))
}

// TestReportJobCompletionCanceledContext covers the request-send guard: a
// canceled context is honored before the (un-drained) loop accepts the report.
// The instance is built but NOT run, so nothing drains jobReq and the canceled
// context wins the send select deterministically.
func TestReportJobCompletionCanceledContext(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("st-norun")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), &failDist{})
	require.NoError(t, err)

	cctx, cc := context.WithCancel(context.Background())
	cc()

	require.Error(t, inst.ReportJobCompletion(cctx,
		tasks.NewWorkerComplete(tasks.MakeJobID(inst.ID()), nil)))
}

// TestServiceTaskWorkerBindInputFailureFaults covers the enqueueJob input-bind
// fault branch: an operation whose input message references an item absent from
// process scope fails to bind, faulting the task before any job is enqueued.
func TestServiceTaskWorkerBindInputFailureFaults(t *testing.T) {
	disp := &capDispatcher{}
	// the inMessage's item is not a process property, so BindInputOnly can't
	// resolve it from scope and returns an error.
	op := service.MustOperation("op",
		bpmncommon.MustMessage("in", data.MustItemDefinition(values.NewVariable(1))),
		nil, nil)
	inst, cancel := serviceTaskWorkerInst(t, disp, op)
	defer cancel()

	require.Eventually(t, func() bool { return inst.State() == Terminated },
		2*time.Second, 5*time.Millisecond)
	require.ErrorContains(t, inst.LastErr(), "worker reported a technical fault")
	_, enqueued := disp.lastJob()
	require.False(t, enqueued, "no job is enqueued when input binding fails")
}

// errorGuardedWorkerInstance builds start → host(ServiceTask, WithWorker) →
// normalEnd with an interrupting Error boundary (errorRef boundaryCode) →
// excEnd, backed by disp. A worker Business Error matching boundaryCode is caught
// by the boundary (SRD-037 FR-4).
func errorGuardedWorkerInstance(
	t *testing.T,
	disp tasks.WorkerDispatcher,
	boundaryCode string,
) (inst *Instance, normalEndID, excEndID string) {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("st-worker-err")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	host, err := activities.NewServiceTask("host",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("topic-x"), activities.WithoutParams())
	require.NoError(t, err)

	normalEnd, err := events.NewEndEvent("normal-end")
	require.NoError(t, err)

	bpErr, err := bpmncommon.NewError("boundary-error", boundaryCode, nil)
	require.NoError(t, err)

	eed, err := events.NewErrorEventDefinition(bpErr)
	require.NoError(t, err)

	be, err := events.NewBoundaryEvent("err-bnd", host, eed, true)
	require.NoError(t, err)

	excEnd, err := events.NewEndEvent("exc-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, host, normalEnd, be, excEnd} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, host)
	require.NoError(t, err)
	_, err = flow.Link(host, normalEnd)
	require.NoError(t, err)
	_, err = flow.Link(be, excEnd)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	rt := enginert.Default().WithWorkerDispatcher(disp)
	inst, err = New(s, scope.EmptyDataPath, rt,
		mockeventproc.NewMockEventProducer(t), &failDist{})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	return inst, normalEnd.ID(), excEnd.ID()
}

// TestWorkerReportBpmnErrorRaisesBoundary covers FR-2/FR-4: a worker's Business
// Error matching an Error boundary interrupts the task and runs the exception flow.
func TestWorkerReportBpmnErrorRaisesBoundary(t *testing.T) {
	disp := &capDispatcher{}
	inst, _, excEndID := errorGuardedWorkerInstance(t, disp, "ResourceConflict")

	job := waitForJob(t, disp)

	require.NoError(t, inst.ReportJobCompletion(context.Background(),
		tasks.NewWorkerBpmnError(job.ID, "ResourceConflict", "conflict")))

	require.Eventually(t, func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)
	require.True(t, reachedNode(inst, excEndID), "the exception flow ran")
}

// TestWorkerBpmnErrorUnmatchedFaultsInstance covers FR-4: a Business Error with no
// matching boundary faults the instance (not silently completed).
func TestWorkerBpmnErrorUnmatchedFaultsInstance(t *testing.T) {
	disp := &capDispatcher{}
	inst, _, excEndID := errorGuardedWorkerInstance(t, disp, "ResourceConflict")

	job := waitForJob(t, disp)

	require.NoError(t, inst.ReportJobCompletion(context.Background(),
		tasks.NewWorkerBpmnError(job.ID, "OtherCode", "x")))

	require.Eventually(t, func() bool { return inst.State() == Terminated },
		2*time.Second, 5*time.Millisecond)
	require.False(t, reachedNode(inst, excEndID),
		"no exception flow runs on a no-match")
}

// TestWorkerReportStatusCompletes covers FR-2/FR-5 end-to-end: a worker Business
// Status writes the WithStatus variable into the real instance scope and the task
// completes normally (proving re.Find/Put for a free-named var).
func TestWorkerReportStatusCompletes(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("st-worker-status")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	host, err := activities.NewServiceTask("host",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("topic-x"),
		activities.WithStatus("orderStatus", false),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, host, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, host)
	require.NoError(t, err)
	_, err = flow.Link(host, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	disp := &capDispatcher{}
	rt := enginert.Default().WithWorkerDispatcher(disp)
	inst, err := New(s, scope.EmptyDataPath, rt,
		mockeventproc.NewMockEventProducer(t), &failDist{})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, inst.Run(ctx))

	job := waitForJob(t, disp)

	require.NoError(t, inst.ReportJobCompletion(context.Background(),
		tasks.NewWorkerStatus(job.ID, values.NewVariable("NOT_FOUND"))))

	require.Eventually(t, func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)
}
