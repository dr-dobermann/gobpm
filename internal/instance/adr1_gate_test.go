package instance

import (
	"context"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// TestStateString verifies every instance lifecycle state renders its own name
// (guards the reconciled vocabulary and the former "FInished" typo).
func TestStateString(t *testing.T) {
	cases := map[State]string{
		Created:     "Created",
		Active:      "Active",
		Completed:   "Completed",
		Terminating: "Terminating",
		Terminated:  "Terminated",
	}

	for st, want := range cases {
		require.Equalf(t, want, st.String(), "State(%d).String()", uint32(st))
	}
}

// TestTerminatedOnPreCanceledContext drives the cancellation-terminal branch
// deterministically: a context canceled before Run() means the loop's first
// select sees ctx.Done() before any track has emitted, so the instance stops
// every track and settles in Terminated (not Completed).
func TestTerminatedOnPreCanceledContext(t *testing.T) {
	_ = data.CreateDefaultStates()

	s := buildForkSnapshot(t)
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	leak := assertNoGoroutineLeak(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before the loop starts

	require.NoError(t, inst.Run(ctx))

	require.Eventually(t,
		func() bool { return inst.State() == Terminated },
		time.Second, 5*time.Millisecond,
		"a pre-canceled instance settles in Terminated via the cascade")

	leak()
}

// TestTerminationCascade verifies ADR-001 §7 termination cascade at the runtime
// level: cancelling the instance context stops every track and drains its
// goroutine within a bound, leaving the instance in a terminal state. The BPMN
// Terminate End Event node that triggers this cascade is owned by the Events
// ADR; this test covers the ctx.Done() cascade the runtime implements.
func TestTerminationCascade(t *testing.T) {
	_ = data.CreateDefaultStates()

	s := buildForkSnapshot(t)
	ep := mockeventproc.NewMockEventProducer(t)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
	require.NoError(t, err)

	leak := assertNoGoroutineLeak(t)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, inst.Run(ctx))

	cancel()

	require.Eventually(t,
		func() bool {
			st := inst.State()
			return st == Completed || st == Terminated
		},
		time.Second, 5*time.Millisecond,
		"instance reaches a terminal state after the ctx cascade")

	// the cascade drains every track goroutine back to baseline (no leak).
	leak()
}
