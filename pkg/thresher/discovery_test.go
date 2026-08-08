package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// TestInstancesFilter verifies the lifecycle-stage views and the
// Forget sweep over the settled set (SRD-019 FR-7a/FR-7; the name is
// kept to honor the frozen doc's canary — the enum it referenced was
// replaced by InstanceQuery in SRD-084).
func TestInstancesFilter(t *testing.T) {
	bp := blockingProcess(t, "disc-run") // stays Running until cancelled
	lp := linearProcess(t, "disc-done", 0)

	th, cancel := runEngine(t, bp)
	defer cancel()
	_, err := th.RegisterProcess(lp)
	require.NoError(t, err)

	running, err := th.StartLatest(bp.ID())
	require.NoError(t, err)
	doneH, err := th.StartLatest(lp.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	st, err := doneH.WaitCompletion(ctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, st)

	time.Sleep(150 * time.Millisecond) // running one reaches its blocking op

	require.ElementsMatch(t, []string{running.ID(), doneH.ID()},
		instanceIDs(t, th, thresher.InstanceQuery{}))
	require.Equal(t, []string{doneH.ID()}, instanceIDs(t, th, thresher.InstanceQuery{Stage: thresher.StageSettled}))
	require.Equal(t, []string{running.ID()}, instanceIDs(t, th, thresher.InstanceQuery{Stage: thresher.StageRunning}))

	// Sweep the finished instances.
	require.NoError(t, th.Forget(instanceIDs(t, th, thresher.InstanceQuery{Stage: thresher.StageSettled})...))
	_, ok := th.Instance(doneH.ID())
	require.False(t, ok)
	require.Equal(t, []string{running.ID()}, instanceIDs(t, th, thresher.InstanceQuery{}))
}

// TestForget verifies batch all-or-nothing release: unknown or still-live ids
// error and remove nothing; terminal ids are removed (FR-7).
func TestForget(t *testing.T) {
	bp := blockingProcess(t, "forget-run")
	lp := linearProcess(t, "forget-done", 0)

	th, cancel := runEngine(t, bp)
	defer cancel()
	_, err := th.RegisterProcess(lp)
	require.NoError(t, err)

	running, err := th.StartLatest(bp.ID())
	require.NoError(t, err)
	doneH, err := th.StartLatest(lp.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	_, err = doneH.WaitCompletion(ctx)
	require.NoError(t, err)
	time.Sleep(150 * time.Millisecond)

	// Unknown id: error, nothing removed.
	require.Error(t, th.Forget("no-such"))
	require.Len(t, instanceIDs(t, th, thresher.InstanceQuery{}), 2)

	// Batch with a still-live id: all-or-nothing — none removed, even the
	// completed one in the same call.
	require.Error(t, th.Forget(doneH.ID(), running.ID()))
	_, ok := th.Instance(doneH.ID())
	require.True(t, ok, "all-or-nothing: the completed id must remain after a failed batch")

	// Terminal id alone: removed.
	require.NoError(t, th.Forget(doneH.ID()))
	_, ok = th.Instance(doneH.ID())
	require.False(t, ok)
}

// TestStarters verifies event-start registrations are listed (FR-7b); a
// none-start process registers no starter.
func TestStarters(t *testing.T) {
	done := make(chan string, 1)
	proc := orderConversationProcess(t, done) // message-start "order placed"

	th, cancel := runEngine(t, proc)
	defer cancel()

	starters := th.Starters()
	require.Len(t, starters, 1)
	require.Equal(t, proc.ID(), starters[0].ProcessID)
	require.Equal(t, "start", starters[0].StartNode)
	require.Equal(t, "order placed", starters[0].Trigger)

	// A none-start process adds no starter.
	_, err := th.RegisterProcess(blockingProcess(t, "no-starter"))
	require.NoError(t, err)
	require.Len(t, th.Starters(), 1)
}

// TestUnregisterProcessWithLiveInstance verifies UnregisterVersion succeeds with
// a live instance, which keeps running (SRD-031.A FR-8; the name predates the
// UnregisterProcess→UnregisterVersion split and is kept to honor SRD-019's
// frozen reference).
func TestUnregisterProcessWithLiveInstance(t *testing.T) {
	bp := blockingProcess(t, "unreg-live")

	th, err := thresher.New("test-unreg-live")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	reg, err := th.RegisterProcess(bp)
	require.NoError(t, err)

	h, err := th.StartProcess(reg)
	require.NoError(t, err)
	time.Sleep(150 * time.Millisecond)

	require.NoError(t, th.UnregisterVersion(reg))

	// The live instance is unaffected: still Active and looked-up-able.
	require.Equal(t, thresher.StateActive, h.State())
	_, ok := th.Instance(h.ID())
	require.True(t, ok)

	// The definition is gone — a new start is rejected.
	_, err = th.StartLatest(bp.ID())
	require.Error(t, err)
}

// instanceIDs lists the ids matching q, failing the test on a refused
// query — the migration shim every discovery consumer in the suite
// shares (SRD-084 FR-4).
func instanceIDs(
	t *testing.T, th *thresher.Thresher, q thresher.InstanceQuery,
) []string {
	t.Helper()

	ids, err := th.Instances(q)
	require.NoError(t, err)

	return ids
}

// TestInstanceQueryComposition is SRD-084 T-1/T-2/T-4/T-5: against a
// mixed registry — a parked caller, its call child, a running root and
// a settled root — every axis filters alone and composed, a
// contradictory query answers with an honest empty set, and each
// handle names its process.
func TestInstanceQueryComposition(t *testing.T) {
	repo := memrepo.New()

	var gate atomic.Int32

	callee := parkingCallee(t, "dq-ee", &gate)
	parent := callerOf(t, "dq-er", callee)

	th, _, cancel := bootCallEngine(t, "dq-engine", repo,
		time.Minute, parent, callee)
	defer cancel()

	ph, err := th.StartLatest(parent.ID())
	require.NoError(t, err)

	var childID string

	require.Eventually(t, func() bool {
		kids := instanceIDs(t, th,
			thresher.InstanceQuery{Kind: thresher.KindChildren})
		if len(kids) != 1 {
			return false
		}

		childID = kids[0]

		return true
	}, 3*time.Second, 5*time.Millisecond, "the call child must appear")

	bp := blockingProcess(t, "dq-run")
	_, err = th.RegisterProcess(bp)
	require.NoError(t, err)
	runH, err := th.StartLatest(bp.ID())
	require.NoError(t, err)

	lp := linearProcess(t, "dq-done", 0)
	_, err = th.RegisterProcess(lp)
	require.NoError(t, err)
	doneH, err := th.StartLatest(lp.ID())
	require.NoError(t, err)

	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	_, err = doneH.WaitCompletion(ctx)
	require.NoError(t, err)
	time.Sleep(150 * time.Millisecond)

	// T-1 — the zero value lists everything.
	require.ElementsMatch(t,
		[]string{ph.ID(), childID, runH.ID(), doneH.ID()},
		instanceIDs(t, th, thresher.InstanceQuery{}))

	// T-2 — single axes.
	require.ElementsMatch(t, []string{ph.ID(), runH.ID(), doneH.ID()},
		instanceIDs(t, th,
			thresher.InstanceQuery{Kind: thresher.KindRoots}))
	require.Equal(t, []string{childID},
		instanceIDs(t, th,
			thresher.InstanceQuery{Kind: thresher.KindChildren}))
	require.Equal(t, []string{doneH.ID()},
		instanceIDs(t, th,
			thresher.InstanceQuery{Stage: thresher.StageSettled}))

	// T-2 — composed: the running children of THIS parent; the running
	// instances of one process.
	require.Equal(t, []string{childID},
		instanceIDs(t, th, thresher.InstanceQuery{
			Stage:    thresher.StageRunning,
			ParentID: ph.ID(),
		}))
	require.Equal(t, []string{childID},
		instanceIDs(t, th,
			thresher.InstanceQuery{ProcessID: callee.ID()}))
	require.Equal(t, []string{runH.ID()},
		instanceIDs(t, th, thresher.InstanceQuery{
			ProcessID: bp.ID(),
			Stage:     thresher.StageRunning,
		}))

	// T-4 — contradictory but well-formed: an empty set, no error.
	require.Empty(t, instanceIDs(t, th, thresher.InstanceQuery{
		Kind:     thresher.KindRoots,
		ParentID: ph.ID(),
	}))

	// T-5 — every handle names its process.
	ch, ok := th.Instance(childID)
	require.True(t, ok)
	require.Equal(t, callee.ID(), ch.ProcessID())
	require.Equal(t, parent.ID(), ph.ProcessID())
	require.Equal(t, ph.ID(), ch.ParentID())
}

// TestInstanceQueryValidation is SRD-084 T-3: an out-of-range axis is
// a self-identifying refusal — the retired enum's silent include-all
// must not survive the migration.
func TestInstanceQueryValidation(t *testing.T) {
	th, err := thresher.New("dq-validate")
	require.NoError(t, err)

	_, err = th.Instances(thresher.InstanceQuery{Kind: 99})
	require.ErrorContains(t, err, "unknown Kind")

	_, err = th.Instances(thresher.InstanceQuery{Stage: 99})
	require.ErrorContains(t, err, "unknown Stage")
}
