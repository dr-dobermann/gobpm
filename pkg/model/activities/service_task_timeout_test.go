package activities_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockrenv"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// sleepingOp returns a Go operation that sleeps for d — ignoring its context,
// the worst case WithTimeout must survive — and then returns (nil, nil).
func sleepingOp(t *testing.T, d time.Duration) service.Operation {
	t.Helper()

	op, err := gooper.New(
		"sleeper",
		func(
			_ context.Context, _ service.DataReader, _ *data.ItemDefinition,
		) (*data.ItemDefinition, error) {
			time.Sleep(d)

			return nil, nil
		})
	require.NoError(t, err)

	return op
}

// TestServiceTaskWithTimeoutCompletes: an operation that finishes before the
// timeout takes the done branch and completes normally (FR-1, FR-2).
func TestServiceTaskWithTimeoutCompletes(t *testing.T) {
	st, err := activities.NewServiceTask("fast", sleepingOp(t, 0),
		activities.WithoutParams(), activities.WithTimeout(time.Second))
	require.NoError(t, err)

	flows, err := st.Exec(context.Background(),
		mockrenv.NewMockRuntimeEnvironment(t))
	require.NoError(t, err)
	require.Empty(t, flows)
}

// TestServiceTaskWithTimeoutTimesOut: an operation that overruns the timeout
// faults the task with a self-identifying error whose message carries the
// "goroutine may still be running" nuance — folded in from the former Warn, so
// the failure is reported once (ADR-022 v.1 §2.1), by the error at its fault
// boundary, not logged-and-returned (FR-2, NFR-1).
func TestServiceTaskWithTimeoutTimesOut(t *testing.T) {
	st, err := activities.NewServiceTask("slow",
		sleepingOp(t, 200*time.Millisecond),
		activities.WithoutParams(), activities.WithTimeout(15*time.Millisecond))
	require.NoError(t, err)

	_, err = st.Exec(context.Background(),
		mockrenv.NewMockRuntimeEnvironment(t))
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out")
	require.Contains(t, err.Error(), "may still be running",
		"the former Warn's nuance folds into the returned error")
	require.Contains(t, err.Error(), "slow", "the error self-identifies the task")
}

// TestServiceTaskWithTimeoutCtxCancel: cancelling the context (a boundary
// interrupt / instance abort) unblocks the wait even while the operation runs;
// Exec returns the context error (FR-2).
func TestServiceTaskWithTimeoutCtxCancel(t *testing.T) {
	st, err := activities.NewServiceTask("cancelled",
		sleepingOp(t, 500*time.Millisecond),
		activities.WithoutParams(), activities.WithTimeout(5*time.Second))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	_, err = st.Exec(ctx, mockrenv.NewMockRuntimeEnvironment(t))
	require.ErrorIs(t, err, context.Canceled)
}

// TestServiceTaskWithTimeoutZeroIsUnbounded: WithTimeout(0) means no bound —
// the operation runs synchronously, exactly as without the option (FR-3).
func TestServiceTaskWithTimeoutZeroIsUnbounded(t *testing.T) {
	st, err := activities.NewServiceTask("sync", sleepingOp(t, 0),
		activities.WithoutParams(), activities.WithTimeout(0))
	require.NoError(t, err)

	flows, err := st.Exec(context.Background(),
		mockrenv.NewMockRuntimeEnvironment(t))
	require.NoError(t, err)
	require.Empty(t, flows)
}

// TestServiceTaskWithTimeoutLeakedGoroutineDropped: an operation that returns
// AFTER the timeout fired has its late result dropped by the buffered done
// channel — no panic, no race (NFR-1). Run under -race.
func TestServiceTaskWithTimeoutLeakedGoroutineDropped(t *testing.T) {
	st, err := activities.NewServiceTask("leaky",
		sleepingOp(t, 60*time.Millisecond),
		activities.WithoutParams(), activities.WithTimeout(15*time.Millisecond))
	require.NoError(t, err)

	_, err = st.Exec(context.Background(),
		mockrenv.NewMockRuntimeEnvironment(t))
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out")

	// let the leaked goroutine finish its buffered send and exit, so -race
	// observes the drop cleanly.
	time.Sleep(90 * time.Millisecond)
}
