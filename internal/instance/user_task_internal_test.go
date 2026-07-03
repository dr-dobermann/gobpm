package instance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// failDist captures the announced task id and returns errors from both calls, so
// the addTask/withdrawTask distributor-error log branches are exercised (the
// errors are non-fatal — the task still parks and completes).
type failDist struct {
	mu sync.Mutex
	id string
}

func (c *failDist) Distribute(_ context.Context, task interactor.TaskInfo) error {
	c.mu.Lock()
	c.id = task.TaskID
	c.mu.Unlock()

	return errors.New("distribute boom")
}

func (c *failDist) Withdraw(context.Context, string) error {
	return errors.New("withdraw boom")
}

func (c *failDist) taskID() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.id
}

// userTaskInst builds a running instance of start → UserTask(candidateUsers=alice,
// required output "result") → end, with a capturing (error-returning) distributor.
func userTaskInst(t *testing.T) (*Instance, *failDist, context.CancelFunc) {
	t.Helper()
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("ut-inst")
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	form, err := data.NewProperty("FORM_ID",
		data.MustItemDefinition(values.NewVariable("form-1")),
		data.ReadyDataState)
	require.NoError(t, err)

	ut, err := activities.NewUserTask("t",
		activities.WithCandidateUsers("alice"),
		activities.WithOutput("result", "string", true),
		data.WithProperties(form),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ut, end} {
		require.NoError(t, p.Add(e))
	}

	_, err = flow.Link(start, ut)
	require.NoError(t, err)
	_, err = flow.Link(ut, end)
	require.NoError(t, err)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	dist := &failDist{}
	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		mockeventproc.NewMockEventProducer(t), dist)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, inst.Run(ctx))

	return inst, dist, cancel
}

// TestInstanceTakeCompleteBranches drives Take/Complete directly against a running
// instance to exercise the argument guards, unknown-task routing, authorization,
// output validation, a valid completion, and the stopped-instance refusal.
func TestInstanceTakeCompleteBranches(t *testing.T) {
	inst, dist, cancel := userTaskInst(t)
	defer cancel()

	require.Eventually(t, func() bool { return dist.taskID() != "" },
		2*time.Second, 5*time.Millisecond)
	taskID := dist.taskID()

	ctx := context.Background()
	alice := stubActor{id: "alice"}

	// argument guards.
	_, err := inst.Take(ctx, "", alice)
	require.Error(t, err)
	require.Error(t, inst.Complete(ctx, "id", nil, nil))

	// unknown task id at the loop.
	_, err = inst.Take(ctx, "bogus", alice)
	require.Error(t, err)

	// unauthorized actor denied; a candidate gets the view.
	_, err = inst.Take(ctx, taskID, stubActor{id: "bob"})
	require.Error(t, err)

	view, err := inst.Take(ctx, taskID, alice)
	require.NoError(t, err)
	require.Equal(t, taskID, view.TaskID)
	require.NotEmpty(t, view.Data) // the FORM_ID property travels in the view

	// invalid outputs (required "result" missing) — non-terminal.
	require.Error(t, inst.Complete(ctx, taskID, alice, nil))

	// a valid completion resumes the token to the end event.
	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("ok")),
				data.ReadyDataState)),
	}
	require.NoError(t, inst.Complete(ctx, taskID, alice, out))

	require.Eventually(t, func() bool { return inst.State() == Completed },
		2*time.Second, 5*time.Millisecond)

	// once the loop is gone, a request is refused (loopDone path).
	<-inst.Done()
	_, err = inst.Take(context.Background(), taskID, alice)
	require.Error(t, err)
}

// TestInstanceCancelWhileParked covers task withdrawal on instance teardown
// (stopAll → withdrawAllTasks) while a UserTask is parked.
func TestInstanceCancelWhileParked(t *testing.T) {
	inst, dist, cancel := userTaskInst(t)

	require.Eventually(t, func() bool { return dist.taskID() != "" },
		2*time.Second, 5*time.Millisecond)

	cancel() // cancel the instance while the task is parked

	require.Eventually(t, func() bool { return inst.State() == Terminated },
		2*time.Second, 5*time.Millisecond)
}

// TestInstanceTakeCanceledContext covers the request-send guard: a canceled
// context is honored before the (un-drained) loop accepts the request. The
// instance is built but NOT run, so nothing drains taskReq and the canceled
// context wins the send select deterministically.
func TestInstanceTakeCanceledContext(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("ut-norun")
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

	_, err = inst.Take(cctx, "x", stubActor{id: "a"})
	require.Error(t, err)
}
