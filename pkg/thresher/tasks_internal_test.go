package thresher

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/stretchr/testify/require"
)

// blockingActor is an embedder-implemented Actor whose Groups() blocks. The
// engine cannot assume an embedder's accessors are cheap, and it must not hold
// its registry lock while finding out.
type blockingActor struct {
	id      string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *blockingActor) UserID() string { return a.id }

func (a *blockingActor) Groups() []string {
	a.once.Do(func() { close(a.entered) })
	<-a.release

	return nil
}

// TestTaskAuthorizationDoesNotHoldTheEngineLock is FIX-038 T-2: the eligibility
// check reaches into embedder code (`Actor.Groups`), and it used to do so while
// holding t.m — the lock every registration, launch and discovery call needs.
//
// The interleaving is driven directly: Groups() blocks until the test releases
// it, and a concurrent registry call must complete meanwhile.
func TestTaskAuthorizationDoesNotHoldTheEngineLock(t *testing.T) {
	th, err := New("task-auth-lock", WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	// a task whose eligibility consults candidate GROUPS, so permits() must ask
	// the actor for them.
	th.m.Lock()
	th.tasks["task-1"] = &taskRecord{
		eligible: interactor.Eligibility{
			CandidateGroups: interactor.ResolvedSlot{
				Declared: true,
				IDs:      []string{"reviewers"},
			},
		},
	}
	th.m.Unlock()

	actor := &blockingActor{
		id:      "u-1",
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}

	verdict := make(chan struct{})

	go func() {
		defer close(verdict)

		_, _ = th.completeVerdict("task-1", actor)
	}()

	<-actor.entered // inside the embedder's Groups(), mid-authorization

	// The engine must still be usable.
	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = th.Instances(InstancesAll) // any operation needing t.m
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the engine lock was held across the actor's Groups() call")
	}

	close(actor.release)
	<-verdict
}

// countingObserver records the facts it is handed.
type countingObserver struct {
	mu   sync.Mutex
	seen int
}

func (o *countingObserver) OnFact(observability.Fact) {
	o.mu.Lock()
	o.seen++
	o.mu.Unlock()
}

func (o *countingObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.seen
}

// TestHandleObserverSurvivesARebuild is FIX-038 T-8. InstanceHandle.Observe
// registered on h.current() — the instance OBJECT — and a rebuild replaces that
// object. SRD-071 made the handle's identity outlive its object precisely so
// callers could hold it across a dehydration; the observer registration was not
// carried across, so a host's subscription went quiet at the first rebuild
// while its Subscription still reported itself live.
//
// The rebuild is simulated the way the engine performs it: adopt the new object
// through trackInstanceLocked, then re-attach outside the lock.
func TestHandleObserverSurvivesARebuild(t *testing.T) {
	th, err := New("observer-rebuild", WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	proc := noneStartProcess(t, "p-obs-rebuild")
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	obs := &countingObserver{}

	sub := h.Observe(obs)
	defer sub.Cancel()

	first, err := th.instanceByID(h.ID())
	require.NoError(t, err)

	first.Report(observability.Fact{Kind: observability.KindInstanceState})
	require.Eventually(t, func() bool { return obs.count() > 0 },
		2*time.Second, 10*time.Millisecond,
		"the observer receives from the instance it registered on")

	before := obs.count()

	// The engine rebuilds the instance: a NEW object, which the handle adopts.
	// A same-id rebuild needs a checkpoint, so the object swap is performed
	// directly — it is exactly what trackInstanceLocked does via adopt, and it
	// is the step the observers must survive.
	snap := th.latestSnapshotLocked(proc.ID())
	require.NotNil(t, snap)

	other, err := th.launchInstance(snap)
	require.NoError(t, err)

	rebuilt, err := th.instanceByID(other.ID())
	require.NoError(t, err)

	h.adopt(rebuilt)
	h.reattachObservers()

	rebuilt.Report(observability.Fact{Kind: observability.KindInstanceState})

	require.Eventually(t, func() bool { return obs.count() > before },
		2*time.Second, 10*time.Millisecond,
		"a subscription taken before the rebuild must keep delivering after it")
}
