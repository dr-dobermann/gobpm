package thresher_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
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

// approvalProcess models the pattern completedBy exists for (ADR-020 v.2 §1.4): a
// task is performed, and a LATER node routes on who performed it. The second user
// task's assignee is an expression over `approve_completedBy` — the record the engine
// committed when the first task completed, read after that task is gone.
//
// It returns the two user-task node ids, because a task id is generated per TRACK
// and reused for each task that track parks on, so sequential tasks share one id and
// must be told apart by node.
func approvalProcess(t *testing.T, id string) (*process.Process, string, string) {
	t.Helper()

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	approve, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("alice", "bob"),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams())
	require.NoError(t, err)

	// Assigned to whoever completed "approve", read out of the engine-published
	// performer register under the reserved read-only RUNTIME subtree.
	performer, err := data.NewTextExpression("x-performer:expr",
		"approve", data.WithResultType("string"))
	require.NoError(t, err)

	countersign, err := activities.NewUserTask("countersign",
		activities.WithAssigneeExpr(performer),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams())
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, approve, countersign, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, approve)
	link(t, approve, countersign)
	link(t, countersign, end)

	return proc, approve.ID(), countersign.ID()
}

// performerEngine resolves the "x-performer:expr" language by looking the expression's
// body up as a NODE NAME in the RUNTIME performer register — enough to prove the
// engine-published record is visible to a later node's expression.
type performerEngine struct{}

func (performerEngine) Type() string { return "##XPerformer" }

func (performerEngine) Languages() []string { return []string{"x-performer:expr"} }

func (performerEngine) Evaluate(
	ctx context.Context, expr data.FormalExpression, src data.Source,
) (data.Value, error) {
	// A path-qualified name selects a named source: RUNTIME serves the engine's
	// read-only runtime variables, of which COMPLETED_BY is the performer register.
	reg, err := src.Find(ctx, "RUNTIME/COMPLETED_BY")
	if err != nil {
		return nil, err
	}

	m, ok := reg.Value().Get(ctx).(map[string]string)
	if !ok {
		return nil, errors.New("the performer register is not a string map")
	}

	who, ok := m[expr.(data.BodyHolder).Body()]
	if !ok {
		return nil, errors.New("no performer recorded for that node")
	}

	return values.NewVariable(who), nil
}

// seqDist records each announcement by node id, so a test can wait for a SPECIFIC
// task in a process that parks several in sequence.
type seqDist struct {
	byNode map[string]string
	mu     sync.Mutex
}

func (d *seqDist) Distribute(_ context.Context, task interactor.TaskInfo) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.byNode == nil {
		d.byNode = map[string]string{}
	}

	d.byNode[task.NodeID] = task.TaskID

	return nil
}

func (d *seqDist) Withdraw(context.Context, string) error { return nil }

// await blocks until a task is announced for nodeID and returns its task id.
func (d *seqDist) await(t *testing.T, nodeID string) string {
	t.Helper()

	var id string

	require.Eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()

		id = d.byNode[nodeID]

		return id != ""
	}, 3*time.Second, 10*time.Millisecond,
		"no task announced for node %q", nodeID)

	return id
}

func stringOutput(t *testing.T, name, v string) []data.Data {
	t.Helper()

	return []data.Data{
		data.MustParameter(name,
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(v)),
				data.ReadyDataState)),
	}
}

// TestCompletedByRoutesTheNextTask is the end-to-end proof of ADR-020 v.2 §2.4.2
// (SRD-073 V10, V15, V16): completion records WHO performed the work, at the instance
// root so the record outlives the task, and a later node routes on it.
//
// It also pins the reassignment case: the record names whoever actually FINISHED the
// task, not whoever was first assigned to it.
func TestCompletedByRoutesTheNextTask(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	seq := &seqDist{}
	proc, approveID, countersignID := approvalProcess(t, "ut-approval")

	th, err := thresher.New("test-completedby",
		thresher.WithTaskDistributor(seq),
		thresher.WithExpressionEngine(performerEngine{}))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	// alice claims the first task, but it is REASSIGNED to bob, who finishes it.
	firstID := seq.await(t, approveID)
	require.NoError(t, th.Claim(ctx, firstID, utActor{id: "alice"}))
	require.NoError(t, th.Reassign(ctx, firstID, "bob"))
	require.NoError(t, th.Complete(ctx, firstID, utActor{id: "bob"},
		stringOutput(t, "result", "ok")))

	// The countersign task's assignee expression read approve_completedBy — bob, the
	// actual finisher, NOT alice who merely held it first. bob is therefore its born
	// owner (§2.5.3) and alice cannot complete it.
	secondID := seq.await(t, countersignID)

	require.Error(t, th.Complete(ctx, secondID, utActor{id: "alice"},
		stringOutput(t, "result", "ok")),
		"the countersign task must be routed to the actual performer")

	require.NoError(t, th.Complete(ctx, secondID, utActor{id: "bob"},
		stringOutput(t, "result", "ok")))

	wctx, wc := context.WithTimeout(context.Background(), 3*time.Second)
	defer wc()
	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)
}

// TestCompletionRefusalsAreDistinguishable covers FR-8's remainder: an embedder can
// tell an authorization failure from an unclaimed task from someone else's hold,
// without parsing messages.
func TestCompletionRefusalsAreDistinguishable(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// A process whose task admits BOTH actors, so the ownership refusal is
	// reachable: with a single candidate, an eligibility failure would mask it.
	seq := &seqDist{}
	proc, approveID, _ := approvalProcess(t, "ut-refusals")

	th, err := thresher.New("test-refusals",
		thresher.WithTaskDistributor(seq),
		thresher.WithExpressionEngine(performerEngine{}))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	_, err = th.StartLatest(proc.ID())
	require.NoError(t, err)

	taskID := seq.await(t, approveID)
	out := stringOutput(t, "result", "ok")

	// Unclaimed: strict completion refuses even an eligible candidate, and says so.
	requireClass(t, th.Complete(ctx, taskID, utActor{id: "alice"}, out),
		thresher.TaskUnclaimedClass)

	// Ineligible: an authorization failure, distinct from either ownership refusal.
	err = th.Complete(ctx, taskID, utActor{id: "carol"}, out)
	requireClass(t, err, errs.ConditionFailed)
	requireNotClass(t, err, thresher.TaskUnclaimedClass)
	requireNotClass(t, err, thresher.TaskNotOwnerClass)

	// Held by someone else: distinct again from "unclaimed".
	require.NoError(t, th.Claim(ctx, taskID, utActor{id: "alice"}))
	requireClass(t, th.Complete(ctx, taskID, utActor{id: "bob"}, out),
		thresher.TaskNotOwnerClass)
}

// requireClass asserts err carries class.
func requireClass(t *testing.T, err error, class string) {
	t.Helper()

	var aerr *errs.ApplicationError
	require.ErrorAs(t, err, &aerr)
	require.Contains(t, aerr.Classes, class)
}

// requireNotClass asserts err does NOT carry class.
func requireNotClass(t *testing.T, err error, class string) {
	t.Helper()

	var aerr *errs.ApplicationError
	require.ErrorAs(t, err, &aerr)
	require.NotContains(t, aerr.Classes, class)
}
