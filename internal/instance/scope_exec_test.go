package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// openedScopeExec starts a scope executor for ordinal ord against a stand-in
// loop that opens its scope and stops, leaving the instance PARKED for the
// drain. It returns the executor and the channel its run reports on; the
// caller releases it by closing the executor's own drain channel.
//
// The park is the state under test and the only one an executor cannot report
// after the fact, so the fixture holds it open rather than racing it.
func openedScopeExec(
	t *testing.T, inst *Instance, host *track, node flow.Node, ord int,
) (*scopeExec, chan error) {
	t.Helper()

	e := newScopeExec(host, &stepInfo{node: node}, ord)
	opened := make(chan struct{})

	go func() {
		req := <-inst.scopeReq
		req.reply <- scopeReply{scopePath: host.scopePath}
		close(opened)
	}()

	done := make(chan error, 1)

	go func() {
		_, err := e.run(t.Context())
		done <- err
	}()

	<-opened

	require.Eventually(t, func() bool { return e.awaits() == awaitScope },
		time.Second, time.Millisecond,
		"an instance whose scope is open is parked for its drain")

	return e, done
}

// TestScopeExecAwaitsItsScope pins the member residency reads (SRD-090.A
// FR-8, M3c): a composite instance reports awaitScope for exactly as long as
// its child scope is open, and awaitNothing on either side of that.
//
// Nothing else can answer the question. From outside the runner's own stack a
// host parked for a child's drain is indistinguishable from one executing —
// which is why it pins its whole process instance resident today, and why the
// mechanism had to become an executor before residency could ask.
func TestScopeExecAwaitsItsScope(t *testing.T) {
	inst, _, node, host := decoratorFixture(t)

	e := newScopeExec(host, &stepInfo{node: node}, 2)
	require.Equal(t, awaitNothing, e.awaits(),
		"an instance that has not opened its scope awaits nothing")

	e, done := openedScopeExec(t, inst, host, node, 2)

	st := e.state()
	require.Equal(t, awaitScope, st.await)
	require.Equal(t, 2, st.ordinal,
		"and says WHICH instance is parked — the ordinal is the join key")
	require.False(t, st.done, "a parked instance is not done")

	close(e.drain) // the loop reports THIS instance's scope drained

	require.NoError(t, <-done)
	require.Equal(t, awaitNothing, e.awaits(),
		"a drained instance awaits nothing")
}

// TestScopeExecClearsItsWaitOnFailure: an instance whose drain faults must
// stop reporting a wait. It is the arm that decides whether a failure leaks
// residency — an executor left claiming awaitScope would keep answering for a
// scope that is gone, and its instance would never release again.
func TestScopeExecClearsItsWaitOnFailure(t *testing.T) {
	inst, _, node, host := decoratorFixture(t)

	e, done := openedScopeExec(t, inst, host, node, 0)

	close(inst.loopDone) // the loop stops mid-pass

	require.Error(t, <-done)
	require.Equal(t, awaitNothing, e.awaits(),
		"a faulted instance awaits nothing")
	require.False(t, e.state().done, "and it did not finish either")
}

// TestScopeExecRefusesAnUnopenableScope: the open is a roundtrip to the
// single-writer loop and can fail (a stopped instance). The executor must
// fault WITHOUT having claimed a wait — it never got a scope to await.
func TestScopeExecRefusesAnUnopenableScope(t *testing.T) {
	inst, _, node, host := decoratorFixture(t)
	close(inst.loopDone)

	e := newScopeExec(host, &stepInfo{node: node}, 0)

	_, err := e.run(t.Context())
	require.Error(t, err)
	require.Equal(t, awaitNothing, e.awaits())
}

// TestLoopDecoratorAwaitsItsLivePass: a Standard-Loop decorator answers for
// the pass currently running and reports nothing between passes — the same
// contract iterDecorator holds, which is what lets a track drive either
// without knowing which it has (ADR-025 §2.13).
func TestLoopDecoratorAwaitsItsLivePass(t *testing.T) {
	inst, _, node, host := decoratorFixture(t)

	d := newLoopDecorator(host, &stepInfo{node: node}, standardLoopOf(node), true)

	require.Equal(t, awaitNothing, d.awaits(),
		"a decorator between passes awaits nothing")
	require.Equal(t, awaitNothing, d.state().await)
	require.Equal(t, 0, d.state().ordinal)

	e, done := openedScopeExec(t, inst, host, node, 1)
	d.live = e

	require.Equal(t, awaitScope, d.awaits(),
		"the decorator reports what its live pass awaits")
	require.Equal(t, 1, d.state().ordinal,
		"and which pass that is")

	close(e.drain)
	require.NoError(t, <-done)
}

// TestLoopDecoratorSatisfiesActivityExec: the composition is closed for the
// Standard-Loop decorator too. A compile-time assertion, kept as a test so
// the reason survives next to it.
func TestLoopDecoratorSatisfiesActivityExec(t *testing.T) {
	var (
		_ activityExec = (*scopeExec)(nil)
		_ activityExec = (*loopDecorator)(nil)
	)

	require.Implements(t, (*activityExec)(nil),
		newLoopDecorator(&track{}, &stepInfo{}, nil, true))
}

// seededSet is a restored executor set marking ordinal 0 complete — the
// shape that makes a stale seed harmful: restoredStates reads it and skips
// that instance.
func seededSet() *checkpoint.IterationRecord {
	return &checkpoint.IterationRecord{
		Kind: iterKindMIParallel,
		N:    3,
		Instances: []checkpoint.IterationInstance{
			{Ordinal: 0, State: instanceCompleted},
		},
	}
}

// TestIterationTakesItsSeedFromTheTrack pins the ownership rule the restored
// executor set has to obey (SRD-090.A FR-7): the seed describes the instances
// of the ONE activity the track was restored on, and starting that activity
// takes it off the track.
//
// Leaving it there is not inert. A restored track does not stop at its
// recorded activity — it finishes it and walks on, so the seed would still be
// sitting there when the token reached the NEXT iterated activity, whose
// decorator would read another activity's ordinals as its own and skip every
// instance recorded complete. Those instances would never run and nothing
// would say so: the run would just produce a shorter result.
//
// Both decorators are checked, because both can be handed one — and each is
// driven into an immediate fault to make the point that the seed is taken
// when the activity STARTS, not when it succeeds.
func TestIterationTakesItsSeedFromTheTrack(t *testing.T) {
	t.Run("a Multi-Instance", func(t *testing.T) {
		inst, node, host := miSeqFixture(t)
		close(inst.loopDone) // the first scope request faults

		host.iterSeed = seededSet()

		_, err := newIterDecorator(host, &stepInfo{node: node},
			multiInstanceOf(node), true).run(t.Context())
		require.Error(t, err)

		require.Nil(t, host.iterSeed,
			"the activity that started took its restored set with it")
	})

	t.Run("a Standard Loop", func(t *testing.T) {
		inst, _, node, host := decoratorFixture(t)
		close(inst.loopDone)

		host.iterSeed = seededSet()

		_, err := newLoopDecorator(host, &stepInfo{node: node},
			standardLoopOf(node), true).run(t.Context())
		require.Error(t, err)

		require.Nil(t, host.iterSeed,
			"a Standard Loop resumes from the count, but still takes the set")
	})
}

// TestTakeIterSeedIsConsumedOnce: a second reader gets nothing. The seed is a
// handover, not a field to poll — two activities reading the same one is the
// failure it exists to prevent.
func TestTakeIterSeedIsConsumedOnce(t *testing.T) {
	tr := &track{iterSeed: seededSet()}

	require.NotNil(t, tr.takeIterSeed())
	require.Nil(t, tr.takeIterSeed(), "the set is handed over exactly once")
	require.Nil(t, tr.iterSeed)
}

// TestIterKindOf pins the record's vocabulary against the node (SRD-090.A
// FR-6). The kind describes the iteration SHAPE and not the node: a
// sequential Multi-Instance reads the same whether its instances are
// executions of a leaf or child scopes of a composite, which is the whole
// point of recording instances rather than tracks.
func TestIterKindOf(t *testing.T) {
	sl, err := activities.NewStandardLoop(loopCondLt(t, 3))
	require.NoError(t, err)

	seq, err := activities.NewMultiInstance(
		activities.WithSequential(), activities.WithCardinality(cardExpr(t, 2)))
	require.NoError(t, err)

	par, err := activities.NewMultiInstance(
		activities.WithCardinality(cardExpr(t, 2)))
	require.NoError(t, err)

	for _, tc := range []struct {
		lc   activities.LoopCharacteristics
		want string
	}{
		{lc: sl, want: iterKindStdLoop},
		{lc: seq, want: iterKindMISequential},
		{lc: par, want: iterKindMIParallel},
	} {
		task, terr := activities.NewSubProcess("body",
			activities.WithLoop(tc.lc))
		require.NoError(t, terr)

		require.Equal(t, tc.want, iterKindOf(task))
	}

	plain, err := activities.NewSubProcess("plain")
	require.NoError(t, err)

	require.Empty(t, iterKindOf(plain),
		"a node that does not iterate reaches no mirror and names no shape")
}
