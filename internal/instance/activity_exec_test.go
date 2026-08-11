package instance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/scope"
)

// TestNodeExecRunsTheNode pins SRD-088.A M1's seam: a non-iterated node is
// executed THROUGH an activityExec, and what comes back — the outgoing
// flows — is what the track follows. Running the whole process is what
// proves the seam is wired; asserting on the executor alone would pass with
// executeStep still calling executeNode directly.
func TestNodeExecRunsTheNode(t *testing.T) {
	got := make(chan string, 2)

	s, _ := routedMIProcess(t, "ae-run", got, true)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, inst.Run(ctx))

	// the start event and the MI host are non-iterated steps on this path;
	// reaching the parked catches means both ran through the executor.
	require.Eventually(t, func() bool {
		parked := 0

		for _, tk := range inst.GetTokens() {
			if tk.State == TokenWaitForEvent {
				parked++
			}
		}

		return parked >= 2
	}, 3*time.Second, 5*time.Millisecond,
		"execution must reach the parked catches through the executor")
}

// TestNodeExecAwaits pins the member M3's residency rule depends on: a leaf
// instance reports awaitEvent exactly while it is parked, and awaitNothing
// otherwise. A leaf can hold no other kind — it opens no scope and owns no
// child instance — and that is the whole reason a leaf contributes to
// residency where a sub-process instance does not (ADR-025 v.3 §2.13).
func TestNodeExecAwaits(t *testing.T) {
	tr := &track{}
	e := newNodeExec(tr, &stepInfo{}, 0)

	// the state field directly: updateState also records track history,
	// which a bare track has none of, and awaits() reads this field.
	tr.state = TrackExecutingStep
	require.Equal(t, awaitNothing, e.awaits())
	require.Equal(t, awaitNothing, e.state().await)
	require.False(t, e.state().done, "an executing instance is not done")

	tr.state = TrackWaitForEvent
	require.Equal(t, awaitEvent, e.awaits())
	require.Equal(t, awaitEvent, e.state().await)
}

// TestNodeExecStateOrdinal: a non-iterated activity is instance zero of one,
// and the ordinal is the join key the record, the projection and an incident
// all name (ADR-025 v.3 §2.9.1). Keeping the identity uniform here is what
// lets M2 add instances without special-casing the single-instance path.
func TestNodeExecStateOrdinal(t *testing.T) {
	tr := &track{}

	require.Equal(t, 0, newNodeExec(tr, &stepInfo{}, 0).state().ordinal)
	require.Equal(t, 2, newNodeExec(tr, &stepInfo{}, 2).state().ordinal)
}

// TestNodeExecDoneNeedsAnEndedStep: done means the instance finished its
// work, not merely that it is not waiting — an instance that has not started
// is also not waiting, and reporting it done would let a decorator complete
// an activity whose instances never ran.
func TestNodeExecDoneNeedsAnEndedStep(t *testing.T) {
	tr := &track{}
	tr.state = TrackExecutingStep

	fresh := newNodeExec(tr, &stepInfo{state: StepCreated}, 0)
	require.False(t, fresh.state().done)

	ended := newNodeExec(tr, &stepInfo{state: StepEnded}, 0)
	require.True(t, ended.state().done)
}

// TestLeafDecoratorAwaitsItsLiveInstance: a decorator answers for the
// instance currently running, and reports nothing between instances. M3's
// residency rule reads this — an activity whose instances are all finished
// must not look like one that is waiting, or it would pin its process
// instance resident forever (ADR-025 v.3 §2.13).
func TestLeafDecoratorAwaitsItsLiveInstance(t *testing.T) {
	tr := &track{}
	d := newLeafDecorator(tr, &stepInfo{}, nil)

	require.Equal(t, awaitNothing, d.awaits(),
		"a decorator with no live instance awaits nothing")
	require.Equal(t, awaitNothing, d.state().await)
	require.Equal(t, 0, d.state().ordinal)

	// instance 2 is running and parks
	d.live = newNodeExec(tr, &stepInfo{}, 2)
	tr.state = TrackWaitForEvent

	require.Equal(t, awaitEvent, d.awaits(),
		"the decorator reports what its live instance awaits")
	require.Equal(t, 2, d.state().ordinal,
		"and reports WHICH instance that is — the ordinal is the join key")
}

// TestLeafDecoratorSatisfiesActivityExec: the composition is closed — a
// decorator is an activityExec, which is what lets a track drive one without
// knowing how many instances are behind it. A compile-time assertion, kept as
// a test so the reason survives next to it.
func TestLeafDecoratorSatisfiesActivityExec(t *testing.T) {
	var (
		_ activityExec = (*nodeExec)(nil)
		_ activityExec = (*leafDecorator)(nil)
	)

	require.Implements(t, (*activityExec)(nil), newLeafDecorator(
		&track{}, &stepInfo{}, nil))
}
