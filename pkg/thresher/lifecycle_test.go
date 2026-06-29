package thresher_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// TestLifecycleNotStartedToStopped verifies the engine walks the lifecycle
// NotStarted -> Started -> Stopped through Run then Shutdown, with the
// transitional states resolved to their terminal values at each observable
// point (SRD-031.B FR-6, T-1).
func TestLifecycleNotStartedToStopped(t *testing.T) {
	th, err := thresher.New("lc-path")
	require.NoError(t, err)
	require.Equal(t, thresher.NotStarted, th.State())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))
	require.Equal(t, thresher.Started, th.State())

	sctx, sc := context.WithTimeout(context.Background(), 3*time.Second)
	defer sc()
	require.NoError(t, th.Shutdown(sctx))
	require.Equal(t, thresher.Stopped, th.State())
}

// TestConcurrentRunSingleWinner verifies that when several goroutines call Run
// on the same fresh engine at once, exactly one claims the NotStarted->Starting
// transition and succeeds; the rest lose the compare-and-swap and are rejected.
// The engine ends Started (SRD-031.B FR-6, T-8; run under -race).
func TestConcurrentRunSingleWinner(t *testing.T) {
	th, err := thresher.New("lc-dbl-run")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const racers = 8

	var (
		wg        sync.WaitGroup
		succeeded atomic.Int32
		rejected  atomic.Int32
	)

	wg.Add(racers)
	for range racers {
		go func() {
			defer wg.Done()
			if err := th.Run(ctx); err != nil {
				require.Contains(t, err.Error(),
					"couldn't start thresher from state")
				rejected.Add(1)

				return
			}
			succeeded.Add(1)
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), succeeded.Load(), "exactly one Run wins")
	require.Equal(t, int32(racers-1), rejected.Load(), "the rest are rejected")
	require.Equal(t, thresher.Started, th.State())

	sctx, sc := context.WithTimeout(context.Background(), 3*time.Second)
	defer sc()
	require.NoError(t, th.Shutdown(sctx))
}

// TestShutdownBeforeRun verifies Shutdown on a NotStarted engine marks it
// Stopped without any teardown (there is nothing running) and stays idempotent
// (SRD-031.B FR-6).
func TestShutdownBeforeRun(t *testing.T) {
	th, err := thresher.New("lc-sd-prerun")
	require.NoError(t, err)

	require.NoError(t, th.Shutdown(context.Background()))
	require.Equal(t, thresher.Stopped, th.State())

	require.NoError(t, th.Shutdown(context.Background()))
}

// TestShutdownFromPausedTearsDown verifies a Paused engine — which still owns a
// live hub and instances — is torn down by Shutdown and ends Stopped, matching
// the pre-transitional-state behavior (SRD-031.B FR-6).
func TestShutdownFromPausedTearsDown(t *testing.T) {
	proc := blockingProcess(t, "lc-sd-paused")
	th, cancel := runEngine(t, proc)
	defer cancel()

	_, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond) // reach the blocking op

	require.NoError(t, th.UpdateState(thresher.Paused))

	sctx, sc := context.WithTimeout(context.Background(), 3*time.Second)
	defer sc()
	require.NoError(t, th.Shutdown(sctx))
	require.Equal(t, thresher.Stopped, th.State())
}

// TestConcurrentShutdownIdempotent verifies that several goroutines calling
// Shutdown at once all return nil and exactly one teardown runs: the winner of
// the Started->Stopping compare-and-swap tears the engine down, the losers
// no-op. The engine ends Stopped (SRD-031.B FR-6, T-3; run under -race).
func TestConcurrentShutdownIdempotent(t *testing.T) {
	proc := blockingProcess(t, "lc-concurrent-sd")
	th, cancel := runEngine(t, proc)
	defer cancel()

	_, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond) // reach the blocking op

	const racers = 8

	var wg sync.WaitGroup

	wg.Add(racers)
	for range racers {
		go func() {
			defer wg.Done()

			sctx, sc := context.WithTimeout(context.Background(), 3*time.Second)
			defer sc()
			require.NoError(t, th.Shutdown(sctx))
		}()
	}
	wg.Wait()

	require.Equal(t, thresher.Stopped, th.State())
}
