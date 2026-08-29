package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
)

// fanoutProc is a PARALLEL Multi-Instance over a User Task — three approvals
// offered at once, which is the construct ADR-025 §2.15 exists for.
func fanoutProc(t *testing.T, key string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	mi, err := activities.NewMultiInstance(
		activities.WithInputCollection("items", "item"))
	require.NoError(t, err)

	items := data.MustProperty("items",
		data.MustItemDefinition(values.NewArray("a", "b", "c"),
			foundation.WithID(key+"-items")),
		data.ReadyDataState)

	p, err := process.New(key, foundation.WithID(key),
		data.WithProperties(items))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID(key+"-start"))
	require.NoError(t, err)

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("alice"),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams(), activities.WithLoop(mi),
		foundation.WithID(key+"-approve"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ut, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, ut)
	link(t, ut, end)

	return p
}

// TestHumanFanOutIsThreeAddressableTasks (SRD-090.D FR-10, ADR-020 §2.12): a
// parallel Multi-Instance over a User Task announces ONE TASK PER INSTANCE,
// each completable on its own, and the activity finishes only when every one
// of them has actually been done.
//
// This is the measurement the construct was refused over. With the parked-work
// identity held as a single slot on the host track, three instances announced
// ONE task between them: only that one was addressable, the process ran to
// completion, and nobody had approved anything — three approvals modeled, none
// performed. The refusal existed to stop that silent wrong answer; the fix is
// an identity per instance, which is what makes the refusal unnecessary.
func TestHumanFanOutIsThreeAddressableTasks(t *testing.T) {
	repo := memrepo.New()
	dist := &annCollector{}

	p := fanoutProc(t, "fanout-ut")

	th, _, cancel := bootTaskEngine(t, "engine-FAN", repo, dist, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err,
		"a parallel fan-out over human work BUILDS — the refusal it used to "+
			"hit named the shared identity this slice removed")

	distinct := func() []string {
		seen := map[string]bool{}
		out := []string{}

		for _, id := range dist.taskIDs() {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}

		return out
	}

	require.Eventually(t, func() bool { return len(distinct()) == 3 },
		5*time.Second, 10*time.Millisecond,
		"three instances must announce three DISTINCT tasks")

	ids := distinct()

	require.Len(t, dist.taskIDs(), 3,
		"and exactly three announcements — an activity-level task on top of "+
			"the per-instance ones would belong to no execution, so nothing "+
			"could ever complete it")

	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("approved")),
				data.ReadyDataState)),
	}

	// completing TWO must not finish the activity: the third approval is a
	// real piece of outstanding work, and completing without it is the defect.
	for _, id := range ids[:2] {
		require.NoError(t, th.Claim(context.Background(), id,
			utActor{id: "alice"}))
		require.NoError(t, th.Complete(context.Background(), id,
			utActor{id: "alice"}, out))
	}

	require.Never(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), h.ID())

		return ok && rec.Status == repository.StatusCompleted
	}, 300*time.Millisecond, 50*time.Millisecond,
		"two of three approvals is not done — the activity waits for the "+
			"instance still holding work")

	require.NoError(t, th.Claim(context.Background(), ids[2],
		utActor{id: "alice"}))
	require.NoError(t, th.Complete(context.Background(), ids[2],
		utActor{id: "alice"}, out))

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), h.ID())

		return ok && rec.Status == repository.StatusCompleted
	}, 5*time.Second, 10*time.Millisecond,
		"with every instance completed by hand, the activity exits once")
}
