package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// TestInstancesFilter verifies the InstanceFilter views and the Forget sweep
// over the completed set (FR-7a, FR-7).
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
		th.Instances(thresher.InstancesAll))
	require.Equal(t, []string{doneH.ID()}, th.Instances(thresher.InstancesCompleted))
	require.Equal(t, []string{running.ID()}, th.Instances(thresher.InstancesRunning))

	// Sweep the finished instances.
	require.NoError(t, th.Forget(th.Instances(thresher.InstancesCompleted)...))
	_, ok := th.Instance(doneH.ID())
	require.False(t, ok)
	require.Equal(t, []string{running.ID()}, th.Instances(thresher.InstancesAll))
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
	require.Len(t, th.Instances(thresher.InstancesAll), 2)

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
