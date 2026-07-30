package thresher

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/stretchr/testify/require"
)

// TestOwnedTaskTeardownDropsTheRecord covers ADR-020 v.2 §2.1.1's cancellation rule
// (SRD-073 V19, V20): ownership grants an exclusive right to COMPLETE a task while it
// lives, and no claim at all on the task's continued existence.
//
// "Claimed" invites the opposite intuition — that holding a task confers a right to
// finish it — so the property worth pinning is that a held task is torn down exactly
// like an unheld one, leaving no registry entry behind for a holder to act on.
func TestOwnedTaskTeardownDropsTheRecord(t *testing.T) {
	ctx := context.Background()
	eligible := interactor.Eligibility{CandidateUsers: slot("alice", "bob")}

	t.Run("withdrawal drops a held task's record", func(t *testing.T) {
		th, id := ownTh(t, eligible)

		require.NoError(t, th.Claim(ctx, id, ownActor{id: "alice"}))

		// Withdraw is what the engine calls on completion, on cancellation by an
		// interrupting boundary event, and on instance teardown — one path for all
		// three, so ownership cannot survive any of them.
		th.unregisterTask(id)

		th.m.Lock()
		_, present := th.tasks[id]
		th.m.Unlock()

		require.False(t, present,
			"a held task must leave no record behind after withdrawal")

		// Every ownership operation and the completion gate now refuse it: there is
		// nothing left to hold.
		require.Error(t, th.Claim(ctx, id, ownActor{id: "bob"}))
		require.Error(t, th.Unclaim(ctx, id, ownActor{id: "alice"}))
		require.Error(t, th.Reassign(ctx, id, "bob"))
		require.Error(t, th.gateComplete(id, ownActor{id: "alice"}))
	})

	t.Run("a held task is re-registered unheld", func(t *testing.T) {
		// A task that is withdrawn and announced again — the shape a hydrate
		// produces — comes back with no holder. Ownership is per parked task, not a
		// property of the node, so a stale hold cannot leak across the boundary.
		th, id := ownTh(t, eligible)

		require.NoError(t, th.Claim(ctx, id, ownActor{id: "alice"}))
		th.unregisterTask(id)

		th.registerTask(interactor.TaskInfo{
			TaskRef:  interactor.TaskRef{TaskID: id, InstanceID: "inst-1"},
			Eligible: eligible,
		})

		require.Empty(t, owner(t, th, id))
		require.NoError(t, th.Claim(ctx, id, ownActor{id: "bob"}),
			"the task returns to the pool, claimable by anyone eligible")
	})

	t.Run("a born-owned task is torn down like any other", func(t *testing.T) {
		th, id := ownTh(t, interactor.Eligibility{Assignee: slot("john")})

		require.Equal(t, "john", owner(t, th, id))

		th.unregisterTask(id)

		require.Error(t, th.gateComplete(id, ownActor{id: "john"}),
			"a design-time assignment confers no immunity to teardown")
	})
}
