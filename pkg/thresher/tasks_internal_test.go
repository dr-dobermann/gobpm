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
	groups  []string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *blockingActor) UserID() string { return a.id }

func (a *blockingActor) Groups() []string {
	a.once.Do(func() { close(a.entered) })
	<-a.release

	return a.groups
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

		_, _ = th.Instances(InstanceQuery{}) // any operation needing t.m
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

	other, err := th.launchInstance(snap, nil)
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

// TestReattachObserversWithoutAnInstance: a handle that speaks for no instance
// yet — one whose adopt has not run — must ignore the re-attachment rather than
// dereference nothing. The call sits on the rebuild path, where the instance
// lookup is allowed to fail.
func TestReattachObserversWithoutAnInstance(t *testing.T) {
	h := &InstanceHandle{}

	require.NotPanics(t, h.reattachObservers)
}

// TestTaskVanishesBetweenThePhases covers the window the §1.2 split opens: the
// engine lock is released for the embedder's Authorize, so the task can be gone
// by the time the verdict is applied. Both phase-2 paths must re-read and
// report the task as unknown — applying a verdict to a record that no longer
// exists is worse than refusing.
func TestTaskVanishesBetweenThePhases(t *testing.T) {
	eligible := interactor.Eligibility{
		CandidateGroups: interactor.ResolvedSlot{
			Declared: true,
			IDs:      []string{"reviewers"},
		},
	}

	// the actor blocks inside the embedder's Groups(), which is where the
	// engine lock is NOT held — the test deletes the task in that window.
	vanish := func(t *testing.T, th *Thresher, act *blockingActor, call func()) {
		t.Helper()

		done := make(chan struct{})

		go func() {
			defer close(done)

			call()
		}()

		<-act.entered

		th.m.Lock()
		delete(th.tasks, "task-1")
		th.m.Unlock()

		close(act.release)
		<-done
	}

	t.Run("completing it", func(t *testing.T) {
		th, err := New("task-vanish-complete",
			WithoutBanner(), WithoutStartupConfig())
		require.NoError(t, err)

		th.m.Lock()
		th.tasks["task-1"] = &taskRecord{eligible: eligible, owner: "u-1"}
		th.m.Unlock()

		act := &blockingActor{
			id:      "u-1",
			groups:  []string{"reviewers"},
			entered: make(chan struct{}),
			release: make(chan struct{}),
		}

		var verdictErr error

		vanish(t, th, act, func() {
			_, verdictErr = th.completeVerdict("task-1", act)
		})

		require.Error(t, verdictErr,
			"a task removed mid-authorization is unknown, not completable")
		require.ErrorContains(t, verdictErr, "task-1")
	})

	t.Run("claiming it", func(t *testing.T) {
		th, err := New("task-vanish-claim",
			WithoutBanner(), WithoutStartupConfig())
		require.NoError(t, err)

		th.m.Lock()
		th.tasks["task-1"] = &taskRecord{eligible: eligible}
		th.m.Unlock()

		act := &blockingActor{
			id:      "u-2",
			groups:  []string{"reviewers"},
			entered: make(chan struct{}),
			release: make(chan struct{}),
		}

		var ownErr error

		vanish(t, th, act, func() {
			ownErr = th.setOwner("task-1", "u-2", act, claimGuard(act))
		})

		require.Error(t, ownErr,
			"a task removed mid-authorization cannot be claimed")
		require.ErrorContains(t, ownErr, "task-1")
	})
}

// TestReattachIsIdempotentPerObject is the independent review's finding A2. An
// Observe landing between adopt and reattachObservers registers on the NEW
// instance object; reattachObservers then re-registered the same fan-out on
// that same object, so every fact arrived TWICE and the first registration's
// cancel was overwritten — it could never be removed.
//
// The window is driven directly: register the observer after the adopt, then
// re-attach, which is exactly the interleaving.
func TestReattachIsIdempotentPerObject(t *testing.T) {
	th, err := New("reattach-idempotent", WithoutBanner(), WithoutStartupConfig())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	proc := noneStartProcess(t, "p-reattach-idem")
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	inst, err := th.instanceByID(h.ID())
	require.NoError(t, err)

	// Count only the marked fact: the instance emits its own lifecycle facts
	// throughout, and they would drown the one delivery this measures.
	obs := &markedObserver{mark: "reattach-probe"}

	sub := h.Observe(obs)
	defer sub.Cancel()

	// the rebuild's second half, with the Observe already landed on this object
	h.reattachObservers()

	inst.Report(observability.Fact{
		Kind:   observability.KindInstanceState,
		NodeID: obs.mark,
	})

	require.Eventually(t, func() bool { return obs.count() > 0 },
		2*time.Second, 10*time.Millisecond, "the observer receives the fact")

	// One fact, one delivery. A duplicated registration makes it two.
	require.Never(t, func() bool { return obs.count() > 1 },
		300*time.Millisecond, 30*time.Millisecond,
		"a re-attach onto the object the observer already sits on must not"+
			" register it twice")
}

// markedObserver counts only the facts carrying its mark in NodeID, so a test
// can measure ONE delivery against an instance that is emitting its own facts.
type markedObserver struct {
	mark string

	mu   sync.Mutex
	seen int
}

func (o *markedObserver) OnFact(f observability.Fact) {
	if f.NodeID != o.mark {
		return
	}

	o.mu.Lock()
	o.seen++
	o.mu.Unlock()
}

func (o *markedObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.seen
}
