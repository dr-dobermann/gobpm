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

// TestTerminatedOnPreCanceledContext drives the cancellation-terminal branch: a
// context canceled before Run() is visible to the loop from its first turn, so
// the instance stops every track and settles in Terminated (not Completed).
//
// What makes it deterministic is the loop's non-blocking ctx.Done() poll
// (FIX-033 §3.2.1), NOT any ordering between the cancellation and the tracks'
// events — `select` gives ready cases no priority, so before that poll the
// events arm could win every turn and the instance settled Completed.
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

	// Await the instance's own terminal signal and compare exactly: polling for
	// "some terminal state" cannot tell Terminated from the Completed this
	// defect produced.
	<-inst.Done()

	require.Equal(t, Terminated, inst.State(),
		"a pre-canceled instance settles Terminated via the cascade")

	leak()
}

// TestTerminatedWhenCancelRacesPendingEvents covers the case no test covered: a
// cancellation competing with track events that are already queued. Each round
// is an independent instance, so the loop's choice between the ready done arm
// and the ready events arm is exercised many times over.
//
// Read what this test does and does not prove (FIX-033 §4.1.2). It pins the
// post-fix guarantee exactly — a canceled instance settles Terminated, whatever
// the select chooses — and it is deterministic in that direction, because the
// loop's poll records the cancellation before any event can be applied. It is
// NOT a reliable detector of the defect returning: with the poll removed, 2000
// fork instances here produced zero Completed settlements, because the window
// (tracks emitting before the loop reaches its first select) is far narrower
// than the original 1-in-1000 observation suggested. The defect's proof is the
// code argument in FIX-033 §2.1, not this test's hit rate.
func TestTerminatedWhenCancelRacesPendingEvents(t *testing.T) {
	_ = data.CreateDefaultStates()

	const rounds = 200

	for i := range rounds {
		s := buildForkSnapshot(t)
		ep := mockeventproc.NewMockEventProducer(t)

		inst, err := New(s, scope.EmptyDataPath, enginert.Default(), ep, nil)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		require.NoError(t, inst.Run(ctx))

		<-inst.Done()

		require.Equalf(t, Terminated, inst.State(),
			"round %d settled %s: a canceled instance must never report "+
				"Completed", i, inst.State())
	}
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
