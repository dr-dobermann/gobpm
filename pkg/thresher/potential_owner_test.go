package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// roleEngine resolves the "x-role:expr" language to a fixed identifier list —
// one user and one group, so both halves of the undiscriminated role match are
// exercised end to end.
type roleEngine struct{}

func (roleEngine) Type() string { return "##XRole" }

func (roleEngine) Languages() []string { return []string{"x-role:expr"} }

func (roleEngine) Evaluate(
	_ context.Context, _ data.FormalExpression, _ data.Source,
) (data.Value, error) {
	return values.NewVariable([]string{"alice", "reviewers"}), nil
}

// potentialOwnerProcess builds a one-UserTask process whose ONLY authorization
// source is a BPMN PotentialOwner — no triad member at all. Before SRD-075 this
// process was open to every actor, because the role was carried and never
// consulted.
func potentialOwnerProcess(t *testing.T, id string) *process.Process {
	t.Helper()

	proc, err := process.New(id)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	expr, err := data.NewTextExpression("x-role:expr", "owners",
		data.WithResultType("string"))
	require.NoError(t, err)

	ae, err := hi.NewResourceAssignmentExpression(expr)
	require.NoError(t, err)

	owners, err := hi.NewPotentialOwner("owners", nil, ae, nil)
	require.NoError(t, err)

	ut, err := activities.NewUserTask("approve",
		activities.WithRoles(owners),
		activities.WithOutput("result", "string", true),
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

// TestPotentialOwnerAuthorizes — SRD-075 T-16: BPMN's own vocabulary decides who
// may act. A UserTask declaring only a PotentialOwner authorizes its members and
// refuses everyone else, through the standard's name for the concept rather than
// the Camunda triad (ADR-020 v.3 §2.5.4).
func TestPotentialOwnerAuthorizes(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	cap := &captureDist{}
	proc := potentialOwnerProcess(t, "ut-potential-owner")

	th, err := thresher.New("test-potential-owner",
		thresher.WithTaskDistributor(cap),
		thresher.WithExpressionEngine(roleEngine{}))
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

	stranger := utActor{id: "mallory"}
	byUserID := utActor{id: "alice"}
	byGroup := utActor{id: "bob", groups: []string{"reviewers"}}

	// An actor the role does not name is refused — the task is NOT open.
	_, err = th.Take(ctx, taskID, stranger)
	require.Error(t, err,
		"a declared PotentialOwner must close the task to everyone else")
	require.Error(t, th.Claim(ctx, taskID, stranger))

	// A role identifier naming the user authorizes.
	_, err = th.Take(ctx, taskID, byUserID)
	require.NoError(t, err)

	// A role identifier naming one of the actor's groups authorizes too: the
	// standard's expressions return "Users or Groups" and mark neither.
	_, err = th.Take(ctx, taskID, byGroup)
	require.NoError(t, err)

	// A potential owner "becomes the actual owner … by explicitly claiming it".
	require.NoError(t, th.Claim(ctx, taskID, byGroup))

	output := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("ok")),
				data.ReadyDataState)),
	}

	// Strict completion still holds: only the actual owner may write.
	require.Error(t, th.Complete(ctx, taskID, byUserID, output))
	require.NoError(t, th.Complete(ctx, taskID, byGroup, output))

	wctx, wc := context.WithTimeout(context.Background(), 3*time.Second)
	defer wc()

	_, err = h.WaitCompletion(wctx)
	require.NoError(t, err)
}
