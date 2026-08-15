package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// TestExecForRoutesByLoopCharacteristics pins the SEAM, which is the thing
// M1/M2 actually introduced: which executor a node gets.
//
// Its predecessor ran a whole process and asserted that tokens parked — which
// would have passed just as well with the seam deleted and executeStep
// calling executeNode directly, since the side effects are identical. A test
// that cannot fail on the change it guards is worse than no test, because it
// reports coverage of something it never checked.
func TestExecForRoutesByLoopCharacteristics(t *testing.T) {
	s, _ := routedMIProcess(t, "ef-route", make(chan string, 2), true)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	tr := &track{instance: inst}

	var plain, seqMI flow.Node

	for _, n := range inst.s.Nodes {
		if mi := multiInstanceOf(n); mi != nil && mi.IsSequential() {
			if _, composite := n.(scopeHost); !composite {
				seqMI = n
			}

			continue
		}

		if plain == nil && multiInstanceOf(n) == nil {
			plain = n
		}
	}

	require.NotNil(t, plain, "the fixture must offer a non-iterated node")
	require.IsType(t, &nodeExec{}, execFor(tr, &stepInfo{node: plain}),
		"a node with no loop characteristics runs as one instance")

	if seqMI != nil {
		require.IsType(t, &iterDecorator{},
			execFor(tr, &stepInfo{node: seqMI}),
			"a sequentially iterated leaf runs through a decorator")
	}
}

// TestNodeExecAwaits pins the member M3's residency rule depends on: a leaf
// instance reports awaitEvent exactly while it is parked, and awaitNothing
// otherwise. A leaf can hold no other kind — it opens no scope and owns no
// child instance — and that is the whole reason a leaf contributes to
// residency where a sub-process instance does not (ADR-025 §2.13).
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
// all name (ADR-025 §2.9.1). Keeping the identity uniform here is what
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
// instance resident forever (ADR-025 §2.13).
func TestLeafDecoratorAwaitsItsLiveInstance(t *testing.T) {
	tr := &track{}
	d := newIterDecorator(tr, &stepInfo{}, nil, false)

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
		_ activityExec = (*iterDecorator)(nil)
	)

	require.Implements(t, (*activityExec)(nil), newIterDecorator(
		&track{}, &stepInfo{}, nil, false))
}

// TestRefuseIfParked pins the guard's DECISION: an instance still waiting
// stops the iteration, and the refusal names which instance — a sequential
// decorator that advanced past a parked one would run the next ordinal over
// the same step and publish output as though every instance had finished.
//
// Reaching this from runInstance requires the construct the snapshot refuses
// (an activity that both iterates and parks), so SRD-090.B covers the path;
// what is testable now is what the guard decides, and that is tested here
// rather than assumed.
func TestRefuseIfParked(t *testing.T) {
	s, _ := routedMIProcess(t, "guard", make(chan string, 2), true)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	var host flow.Node

	for _, n := range inst.s.Nodes {
		if multiInstanceOf(n) != nil {
			host = n

			break
		}
	}

	require.NotNil(t, host)

	tr := &track{instance: inst}
	d := newIterDecorator(tr, &stepInfo{node: host}, nil, false)

	require.NoError(t, d.refuseIfParked(0),
		"no live instance is not a parked one")

	d.live = newNodeExec(tr, &stepInfo{node: host}, 3)
	tr.state = TrackExecutingStep
	require.NoError(t, d.refuseIfParked(3),
		"an executing instance does not stop the iteration")

	tr.state = TrackWaitForEvent

	err = d.refuseIfParked(3)
	require.Error(t, err, "a waiting instance stops the iteration")
	require.ErrorContains(t, err, "instance 3",
		"and the refusal names WHICH instance is waiting")
	require.ErrorContains(t, err, host.Name())
}
