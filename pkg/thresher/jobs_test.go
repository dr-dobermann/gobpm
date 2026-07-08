package thresher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// capDispatcher captures the jobs enqueued by the engine so a test can read the
// minted JobID and drive completion through the Thresher's routing sink. The
// worker-facing methods are unused here.
type capDispatcher struct {
	mu   sync.Mutex
	jobs []tasks.Job
}

func (d *capDispatcher) Enqueue(_ context.Context, job tasks.Job) error {
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

func (d *capDispatcher) lastJob() (tasks.Job, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.jobs) == 0 {
		return tasks.Job{}, false
	}

	return d.jobs[len(d.jobs)-1], true
}

// TestThresherReportJobCompletionRoutes covers the engine-level routing (SRD-036
// §4.5): a worker's outcome routes to the owning instance by the id embedded in
// the job id and resumes it, while a nil outcome and an unknown instance are
// rejected.
func TestThresherReportJobCompletionRoutes(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	disp := &capDispatcher{}
	th, err := thresher.New("job-routing", thresher.WithWorkerDispatcher(disp))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	proc, err := process.New("worker-proc")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	st, err := activities.NewServiceTask("svc",
		service.MustOperation("op", nil, nil, nil),
		activities.WithWorker("topic-x"), activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, st, end} {
		require.NoError(t, proc.Add(e))
	}

	_, err = flow.Link(start, st)
	require.NoError(t, err)
	_, err = flow.Link(st, end)
	require.NoError(t, err)

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	var job tasks.Job

	require.Eventually(t, func() bool {
		j, ok := disp.lastJob()
		job = j

		return ok
	}, 2*time.Second, 5*time.Millisecond)

	// the job routes back to the launched instance.
	require.Equal(t, h.ID(), job.ID.InstanceID())

	// a nil outcome is rejected before routing.
	require.Error(t, th.ReportJobCompletion(ctx, nil))

	// an outcome for an unknown instance is rejected.
	require.Error(t, th.ReportJobCompletion(ctx,
		tasks.NewWorkerComplete(tasks.MakeJobID("bogus-instance"), nil)))

	// the real job resumes the instance to completion.
	require.NoError(t, th.ReportJobCompletion(ctx,
		tasks.NewWorkerComplete(job.ID, nil)))

	state, werr := h.WaitCompletion(ctx)
	require.NoError(t, werr)
	require.Equal(t, thresher.StateCompleted, state)
}
