package thresher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// captureDist is a TaskDistributor that records the announced TaskInfo so a test
// learns the engine-minted task id.
type captureDist struct {
	mu   sync.Mutex
	info interactor.TaskInfo
}

func (c *captureDist) Distribute(_ context.Context, task interactor.TaskInfo) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.info = task

	return nil
}

func (c *captureDist) Withdraw(context.Context, string) error { return nil }

func (c *captureDist) taskID() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.info.TaskID
}

// utActor is a test hi.Actor.
type utActor struct {
	id     string
	groups []string
}

func (a utActor) UserID() string   { return a.id }
func (a utActor) Groups() []string { return a.groups }

// userTaskProcess builds start → UserTask(candidateUsers=alice, required output
// "result") → end.
func userTaskProcess(t *testing.T, id string) *process.Process {
	t.Helper()

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	form, err := data.NewProperty("FORM_ID",
		data.MustItemDefinition(values.NewVariable("approval-form")),
		data.ReadyDataState)
	require.NoError(t, err)

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("alice"),
		activities.WithOutput("result", "string", true),
		data.WithProperties(form),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ut, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, ut)
	link(t, ut, end)

	return proc
}

// TestUserTaskParkTakeComplete drives a UserTask end-to-end through the engine:
// it parks and is announced; Take/Complete authorize; a valid completion resumes
// the token to the end event (SRD-034 FR-1/6/7).
func TestUserTaskParkTakeComplete(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	cap := &captureDist{}
	proc := userTaskProcess(t, "ut-e2e")

	th, err := thresher.New("test-ut", thresher.WithTaskDistributor(cap))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	// The UserTask parks and is announced to the distributor.
	require.Eventually(t, func() bool { return cap.taskID() != "" },
		2*time.Second, 10*time.Millisecond)
	taskID := cap.taskID()

	alice := utActor{id: "alice"}
	bob := utActor{id: "bob"}

	// Take: an unauthorized actor is denied (no data); a candidate gets the view.
	_, err = th.Take(ctx, taskID, bob)
	require.Error(t, err)

	view, err := th.Take(ctx, taskID, alice)
	require.NoError(t, err)
	require.Equal(t, taskID, view.TaskID)
	// the FORM_ID property travels in the view's self-describing data.
	require.NotEmpty(t, view.Data)
	require.Equal(t, "FORM_ID", view.Data[0].Name())

	output := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("ok")),
				data.ReadyDataState)),
	}

	// Complete failures are non-terminal — the task stays parked for a retry.
	require.Error(t, th.Complete(ctx, taskID, bob, output)) // unauthorized
	require.Error(t, th.Complete(ctx, taskID, alice, nil))  // required output missing

	// An authorized, valid completion resumes the token to the end event.
	require.NoError(t, th.Complete(ctx, taskID, alice, output))

	wctx, wc := context.WithTimeout(context.Background(), 3*time.Second)
	defer wc()
	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)

	// The task is gone after completion.
	_, err = th.Take(ctx, taskID, alice)
	require.Error(t, err)
}

// TestUserTaskCancelWhileParked verifies that canceling an instance with a parked
// UserTask terminates it and withdraws the task (SRD-034 NFR-2).
func TestUserTaskCancelWhileParked(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	cap := &captureDist{}
	proc := userTaskProcess(t, "ut-cancel")

	th, err := thresher.New("test-ut-cancel",
		thresher.WithTaskDistributor(cap))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return cap.taskID() != "" },
		2*time.Second, 10*time.Millisecond)
	taskID := cap.taskID()

	cctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	state, err := h.Cancel(cctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateTerminated, state)

	// the parked task was withdrawn — no longer completable.
	require.Error(t,
		th.Complete(context.Background(), taskID, utActor{id: "alice"}, nil))
}

// TestThresherTakeCompleteUnknownTask covers routing of an unknown task id.
func TestThresherTakeCompleteUnknownTask(t *testing.T) {
	th, err := thresher.New("test-ut-unknown")
	require.NoError(t, err)

	_, err = th.Take(context.Background(), "no-such", utActor{id: "x"})
	require.Error(t, err)

	require.Error(t,
		th.Complete(context.Background(), "no-such", utActor{id: "x"}, nil))
}
