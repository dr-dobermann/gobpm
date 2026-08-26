package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// TestDehydrationUserTaskWake covers SRD-071 T-6 (FR-8): an instance parked on
// a human task dehydrates — the task keeps living in the distributor's inbox,
// which is precisely why the instance need not — and a Complete on that task
// hydrates the instance and completes it. The task id the human holds survives
// the round trip: the wake re-parks under the RECORDED id, never a fresh one.
func TestDehydrationUserTaskWake(t *testing.T) {
	repo := memrepo.New()
	dist := &annCollector{}

	p := utProc(t, "dehy-ut-wake")

	th, fw, cancel := bootTaskEngine(t, "engine-UT", repo, dist, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return dist.count() == 1 },
		3*time.Second, 10*time.Millisecond,
		"the parked task must be announced")

	announced := dist.taskIDs()
	require.Len(t, announced, 1)

	taskID := announced[0]

	// idle on a held human-task wait → the instance releases its goroutines.
	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond,
		"an instance parked on a human task must dehydrate")

	// the human acts on the task exactly as before — unaware the instance
	// released its goroutines. Take hydrates it and answers.
	view, err := th.Take(context.Background(), taskID, utActor{id: "operator"})
	require.NoError(t, err, "Take must hydrate the instance and answer")
	require.Equal(t, taskID, view.TaskID,
		"the task keeps the id the human is holding")

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseHydrated)
	}, 3*time.Second, 10*time.Millisecond,
		"the task action must be an observable wake")

	// the re-park announced the SAME id, never a fresh one.
	for _, id := range dist.taskIDs() {
		require.Equal(t, taskID, id,
			"a rehydrated task must not change the id a human holds")
	}

	// completing it drives the instance to the end.
	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("approved")),
				data.ReadyDataState)),
	}

	// Claim before completing — strict completion (ADR-020 v.2 §2.4.1). Note the
	// instance is ALREADY hydrated here, by the Take above; the no-hydration
	// property of a claim is asserted in TestDehydrationUserTaskCompleteWithoutTake,
	// where the instance is still released.
	require.NoError(t, th.Claim(context.Background(), taskID,
		utActor{id: "operator"}))

	require.NoError(t, th.Complete(context.Background(), taskID,
		utActor{id: "operator"}, out))

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), h.ID())

		return ok && rec.Status == repository.StatusCompleted
	}, 3*time.Second, 10*time.Millisecond,
		"the woken instance must complete")
}

// TestDehydrationUserTaskCompleteWithoutTake covers the same wake reached
// straight from Complete — a human acting on a task they took long before the
// instance released its goroutines.
func TestDehydrationUserTaskCompleteWithoutTake(t *testing.T) {
	repo := memrepo.New()
	dist := &annCollector{}

	p := utProc(t, "dehy-ut-complete")

	th, fw, cancel := bootTaskEngine(t, "engine-UTC", repo, dist, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return dist.count() == 1 },
		3*time.Second, 10*time.Millisecond)

	taskID := dist.taskIDs()[0]

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond)

	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("ok")),
				data.ReadyDataState)),
	}

	// Claiming a RELEASED instance's task must not wake it: ownership lives beside
	// the task, not inside the instance, so the operation is a registry mutation
	// (ADR-020 v.2 §2.1.1, SRD-073 NFR-2). This is the property that justifies
	// serving ownership at the engine level at all.
	require.NoError(t, th.Claim(context.Background(), taskID,
		utActor{id: "operator"}))

	require.False(t,
		fw.saw(observability.KindInstanceState, observability.PhaseHydrated),
		"claiming must not hydrate a released instance")

	require.NoError(t, th.Complete(context.Background(), taskID,
		utActor{id: "operator"}, out),
		"Complete must hydrate the instance and resume the task")

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), h.ID())

		return ok && rec.Status == repository.StatusCompleted
	}, 3*time.Second, 10*time.Millisecond)
}

// TestDehydrationUnknownTaskIsLoud: an action on a task the engine does not
// know stays a classified error — the wake path never invents an instance.
func TestDehydrationUnknownTaskIsLoud(t *testing.T) {
	th, _, cancel := bootTaskEngine(t, "engine-UTX", memrepo.New(),
		&annCollector{}, utProc(t, "dehy-ut-unknown"))
	defer cancel()

	_, err := th.Take(context.Background(), "no-such-task",
		utActor{id: "operator"})
	require.Error(t, err)

	require.Error(t, th.Complete(context.Background(), "no-such-task",
		utActor{id: "operator"}, nil))
}

// TestDehydrationKeepsOwnershipAcrossAHydration: a claim taken while the
// instance is released survives the hydration that a later Take causes.
//
// The natural flow for a task offered to several candidates is claim → open the
// form → submit: claiming first is what stops two candidates working it in
// parallel (ADR-020 §2.4.1). Across a dehydration that flow used to fail. Waking
// the instance re-parks the wait and re-announces the task under its recorded id
// (§2.1), and the announcement overwrote the whole engine-level record — so the
// Take that opened the form silently discarded the claim, the holder's own
// Complete came back TASK_UNCLAIMED, and a second candidate could claim the task
// out from under them. Exclusivity lapsed on exactly the long-lived tasks it
// exists for.
func TestDehydrationKeepsOwnershipAcrossAHydration(t *testing.T) {
	repo := memrepo.New()
	dist := &annCollector{}

	p := twoCandidateProc(t, "dehy-ut-own")

	th, fw, cancel := bootTaskEngine(t, "engine-UTO", repo, dist, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return dist.count() == 1 },
		3*time.Second, 10*time.Millisecond,
		"the parked task must be announced")

	taskID := dist.taskIDs()[0]

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 3*time.Second, 10*time.Millisecond,
		"an instance parked on a human task must dehydrate")

	// alice takes the task off the pool while the instance is still released —
	// no hydration, the record lives beside the task (§2.1.1).
	require.NoError(t, th.Claim(context.Background(), taskID,
		utActor{id: "alice"}))

	// opening the form hydrates the instance, which re-announces the task.
	_, err = th.Take(context.Background(), taskID, utActor{id: "alice"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseHydrated)
	}, 3*time.Second, 10*time.Millisecond,
		"the Take must have woken the instance — otherwise this test proves "+
			"nothing about what the wake does to the record")

	// the hold is still alice's: bob is eligible and still refused.
	require.Error(t, th.Claim(context.Background(), taskID,
		utActor{id: "bob"}),
		"an eligible candidate must not take a held task, hydration or not")

	out := []data.Data{
		data.MustParameter("result",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable("approved")),
				data.ReadyDataState)),
	}

	require.NoError(t, th.Complete(context.Background(), taskID,
		utActor{id: "alice"}, out),
		"the holder completes her own task after opening it")

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), h.ID())

		return ok && rec.Status == repository.StatusCompleted
	}, 3*time.Second, 10*time.Millisecond,
		"the completed task drives the woken instance to the end")
}

// twoCandidateProc is utProc with TWO candidate users, so a task is born unowned
// and a second eligible actor exists to test exclusivity against.
func twoCandidateProc(t *testing.T, key string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", foundation.WithID(key+"-start"))
	require.NoError(t, err)

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("alice", "bob"),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams(),
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

// bootTaskEngine boots a checkpoint-armed engine on a controlled clock with the
// given task distributor.
func bootTaskEngine(
	t *testing.T, name string, repo repository.Repository,
	dist *annCollector, p *process.Process,
) (*thresher.Thresher, *factWatch, context.CancelFunc) {
	t.Helper()

	th, err := thresher.New(name,
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithTaskDistributor(dist),
		thresher.WithClock(clocktest.New(dehydrationEpoch)),
		thresher.WithLeaseTTL(time.Minute))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)
	t.Cleanup(sub.Cancel)

	ctx, cancel := context.WithCancel(context.Background())

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)
	require.NoError(t, th.Run(ctx))

	return th, fw, cancel
}
