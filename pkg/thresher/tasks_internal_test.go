package thresher

import (
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
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
