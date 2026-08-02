package thresher

import (
	"context"
	"sync"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/stretchr/testify/require"
)

// ownActor is a test hi.Actor for the ownership operations.
type ownActor struct {
	id     string
	groups []string
}

func (a ownActor) UserID() string   { return a.id }
func (a ownActor) Groups() []string { return a.groups }

// slot builds a declared eligibility slot.
func slot(ids ...string) interactor.ResolvedSlot {
	return interactor.ResolvedSlot{Declared: true, IDs: ids}
}

// ownTh builds a Thresher with one registered task carrying eligible, WITHOUT
// starting an engine: the ownership operations are registry mutations that never
// touch an instance (ADR-020 v.2 §2.1.1), so they are exercisable in isolation.
// That is itself the point — a test that needed a running instance would disprove
// the design.
func ownTh(t *testing.T, eligible interactor.Eligibility) (*Thresher, string) {
	t.Helper()

	th, err := New("own-test")
	require.NoError(t, err)

	const taskID = "task-1"

	th.registerTask(interactor.TaskInfo{
		TaskRef:  interactor.TaskRef{TaskID: taskID, InstanceID: "inst-1"},
		Eligible: eligible,
	})

	return th, taskID
}

// owner reads the recorded actualOwner.
func owner(t *testing.T, th *Thresher, taskID string) string {
	t.Helper()

	th.m.Lock()
	defer th.m.Unlock()

	rec, ok := th.tasks[taskID]
	require.True(t, ok)

	return rec.owner
}

// TestClaimUnclaimReassign covers the operation matrix of ADR-020 v.2 §2.5.2 —
// each operation's guard and effect (SRD-073 V3–V9).
func TestClaimUnclaimReassign(t *testing.T) {
	ctx := context.Background()
	candidates := interactor.Eligibility{CandidateUsers: slot("alice", "bob")}

	t.Run("claim by an eligible actor takes ownership", func(t *testing.T) {
		th, id := ownTh(t, candidates)

		require.NoError(t, th.Claim(ctx, id, ownActor{id: "alice"}))
		require.Equal(t, "alice", owner(t, th, id))
	})

	t.Run("claim over another actor's hold is refused", func(t *testing.T) {
		th, id := ownTh(t, candidates)

		require.NoError(t, th.Claim(ctx, id, ownActor{id: "alice"}))
		require.Error(t, th.Claim(ctx, id, ownActor{id: "bob"}))
		require.Equal(t, "alice", owner(t, th, id),
			"a refused claim must not disturb the holder")
	})

	t.Run("re-claiming your own task is an idempotent no-op", func(t *testing.T) {
		// The guard stops one participant seizing ANOTHER's work; a same-owner
		// claim takes nothing from anybody, so refusing it would only make the
		// operation unsafe to retry and break embedders that claim before every
		// completion.
		th, id := ownTh(t, candidates)

		require.NoError(t, th.Claim(ctx, id, ownActor{id: "alice"}))
		require.NoError(t, th.Claim(ctx, id, ownActor{id: "alice"}))
		require.Equal(t, "alice", owner(t, th, id))
	})

	t.Run("claim by an ineligible actor is refused", func(t *testing.T) {
		th, id := ownTh(t, candidates)

		require.Error(t, th.Claim(ctx, id, ownActor{id: "carol"}))
		require.Empty(t, owner(t, th, id))
	})

	t.Run("unclaim by the owner releases to the pool", func(t *testing.T) {
		th, id := ownTh(t, candidates)

		require.NoError(t, th.Claim(ctx, id, ownActor{id: "alice"}))
		require.NoError(t, th.Unclaim(ctx, id, ownActor{id: "alice"}))
		require.Empty(t, owner(t, th, id))

		require.NoError(t, th.Claim(ctx, id, ownActor{id: "bob"}),
			"a released task must be claimable again")
	})

	t.Run("unclaim by a non-owner is refused", func(t *testing.T) {
		th, id := ownTh(t, candidates)

		require.NoError(t, th.Claim(ctx, id, ownActor{id: "alice"}))
		require.Error(t, th.Unclaim(ctx, id, ownActor{id: "bob"}))
		require.Equal(t, "alice", owner(t, th, id))
	})

	t.Run("reassign overrides an existing owner", func(t *testing.T) {
		th, id := ownTh(t, candidates)

		require.NoError(t, th.Claim(ctx, id, ownActor{id: "alice"}))
		require.NoError(t, th.Reassign(ctx, id, "bob"))
		require.Equal(t, "bob", owner(t, th, id))
	})

	t.Run("reassign needs no caller, only an eligible nominee", func(t *testing.T) {
		th, id := ownTh(t, candidates)

		require.Error(t, th.Reassign(ctx, id, "carol"),
			"a nominee the process excludes must be refused")
		require.NoError(t, th.Reassign(ctx, id, "alice"))
		require.Equal(t, "alice", owner(t, th, id))
	})

	t.Run("an unowned task can be reassigned", func(t *testing.T) {
		th, id := ownTh(t, candidates)

		require.NoError(t, th.Reassign(ctx, id, "bob"))
		require.Equal(t, "bob", owner(t, th, id))
	})

	t.Run("a group-only-eligible nominee cannot be reassigned to", func(t *testing.T) {
		// The documented limitation of SRD-073 §4.4: a reassignment nominee is
		// ABSENT, and group membership is authenticated by the embedder for a
		// present actor — it cannot be asserted on someone's behalf. So a task
		// whose only eligibility is a candidate group has no reassignable
		// nominee, even though a present member of that group may claim it.
		th, id := ownTh(t,
			interactor.Eligibility{CandidateGroups: slot("reviewers")})

		require.Error(t, th.Reassign(ctx, id, "alice"),
			"a nominee eligible only through a group must be refused")

		require.NoError(t, th.Claim(ctx, id,
			ownActor{id: "alice", groups: []string{"reviewers"}}),
			"a present member of the group may still claim it")
	})

	t.Run("operations on an unknown task are refused", func(t *testing.T) {
		th, _ := ownTh(t, candidates)

		require.Error(t, th.Claim(ctx, "ghost", ownActor{id: "alice"}))
		require.Error(t, th.Unclaim(ctx, "ghost", ownActor{id: "alice"}))
		require.Error(t, th.Reassign(ctx, "ghost", "alice"))
	})
}

// TestBornOwnership covers ADR-020 v.2 §2.5.3: a triad naming exactly one actor
// assigns the task at distribution, and that ownership is ordinary — releasable and
// reassignable (SRD-073 V13, V14).
func TestBornOwnership(t *testing.T) {
	ctx := context.Background()

	t.Run("a single resolved assignee owns from distribution", func(t *testing.T) {
		th, id := ownTh(t, interactor.Eligibility{Assignee: slot("john")})

		require.Equal(t, "john", owner(t, th, id))
		require.NoError(t, th.Claim(ctx, id, ownActor{id: "john"}),
			"the assignee may claim a task it already owns — an embedder that "+
				"claims before completing must not break on direct assignment")
		require.Error(t, th.Claim(ctx, id, ownActor{id: "mary"}),
			"a different actor may not claim over the assignee's hold")
	})

	t.Run("born ownership is releasable and reassignable", func(t *testing.T) {
		th, id := ownTh(t, interactor.Eligibility{Assignee: slot("john")})

		require.NoError(t, th.Unclaim(ctx, id, ownActor{id: "john"}))
		require.Empty(t, owner(t, th, id))
		require.NoError(t, th.Claim(ctx, id, ownActor{id: "john"}))
		require.NoError(t, th.Reassign(ctx, id, "john"))
	})

	t.Run("several assignees leave the task unowned", func(t *testing.T) {
		th, id := ownTh(t, interactor.Eligibility{Assignee: slot("a", "b")})

		require.Empty(t, owner(t, th, id))
	})

	t.Run("no triad leaves the task unowned but open", func(t *testing.T) {
		th, id := ownTh(t, interactor.Eligibility{})

		require.Empty(t, owner(t, th, id))
		require.NoError(t, th.Claim(ctx, id, ownActor{id: "anybody"}))
	})
}

// TestClaimIsExclusiveUnderRace proves the exclusivity ownership exists to provide:
// many candidates claiming one task concurrently yield exactly one winner
// (SRD-073 NFR-3). Run under -race.
func TestClaimIsExclusiveUnderRace(t *testing.T) {
	const claimers = 32

	ids := make([]string, 0, claimers)
	for i := range claimers {
		ids = append(ids, string(rune('a'+i%26))+string(rune('0'+i/26)))
	}

	th, id := ownTh(t, interactor.Eligibility{CandidateUsers: slot(ids...)})

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)

	for _, uid := range ids {
		wg.Add(1)

		go func(uid string) {
			defer wg.Done()

			if err := th.Claim(context.Background(), id,
				ownActor{id: uid}); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(uid)
	}

	wg.Wait()

	require.Equal(t, 1, wins, "exactly one claimer must win")
	require.NotEmpty(t, owner(t, th, id))
}

// TestOwnershipValidatesParameters covers the public-API guards (SRD-073 NFR-6).
func TestOwnershipValidatesParameters(t *testing.T) {
	ctx := context.Background()
	th, id := ownTh(t, interactor.Eligibility{})

	t.Run("nil actor", func(t *testing.T) {
		require.Error(t, th.Claim(ctx, id, nil))
		require.Error(t, th.Unclaim(ctx, id, nil))
	})

	t.Run("empty task id", func(t *testing.T) {
		require.Error(t, th.Claim(ctx, "", ownActor{id: "alice"}))
		require.Error(t, th.Unclaim(ctx, "", ownActor{id: "alice"}))
		require.Error(t, th.Reassign(ctx, "", "alice"))
	})

	t.Run("empty user id", func(t *testing.T) {
		require.Error(t, th.Claim(ctx, id, ownActor{id: ""}))
		require.Error(t, th.Reassign(ctx, id, ""))
	})
}

// TestGateCompleteRefusesBeforeHydration covers the pre-hydration stages of
// ADR-020 v.2 §2.4 in their own order — eligibility, then ownership — proving a
// doomed completion is refused from the registry alone (SRD-073 FR-4).
func TestGateCompleteRefusesBeforeHydration(t *testing.T) {
	th, id := ownTh(t, interactor.Eligibility{CandidateUsers: slot("alice", "bob")})

	require.Error(t, th.gateComplete(id, ownActor{id: "alice"}),
		"an unowned task is completable by nobody")

	require.NoError(t, th.Claim(context.Background(), id, ownActor{id: "alice"}))

	require.Error(t, th.gateComplete(id, ownActor{id: "carol"}),
		"an ineligible actor is refused at the eligibility stage")
	require.Error(t, th.gateComplete(id, ownActor{id: "bob"}),
		"an eligible non-owner is refused at the ownership stage")
	require.NoError(t, th.gateComplete(id, ownActor{id: "alice"}))

	require.Error(t, th.gateComplete("ghost", ownActor{id: "alice"}))
	require.Error(t, th.gateComplete(id, nil))
}

// TestOwnershipAgainstRoleSlot — SRD-075 T-17: the ownership operations get the
// composed eligible set for free, because all of them authorize through
// interactor.Eligibility (§4.5). This test proves that rather than changing it,
// and pins the one asymmetry the composition inherits.
func TestOwnershipAgainstRoleSlot(t *testing.T) {
	ctx := context.Background()

	t.Run("a role-eligible actor may claim", func(t *testing.T) {
		th, id := ownTh(t, interactor.Eligibility{Roles: slot("john")})

		require.NoError(t, th.Claim(ctx, id, ownActor{id: "john"}))
		require.Equal(t, "john", owner(t, th, id))
	})

	t.Run("a group-named role identifier authorizes a present actor",
		func(t *testing.T) {
			th, id := ownTh(t,
				interactor.Eligibility{Roles: slot("reviewers")})

			require.NoError(t, th.Claim(ctx, id,
				ownActor{id: "john", groups: []string{"reviewers"}}))
			require.Equal(t, "john", owner(t, th, id))
		})

	t.Run("an actor matching no role identifier is refused",
		func(t *testing.T) {
			th, id := ownTh(t, interactor.Eligibility{Roles: slot("john")})

			require.Error(t, th.Claim(ctx, id, ownActor{id: "stranger"}))
		})

	t.Run("unclaim returns a role-claimed task to the pool",
		func(t *testing.T) {
			th, id := ownTh(t, interactor.Eligibility{Roles: slot("john")})

			require.NoError(t, th.Claim(ctx, id, ownActor{id: "john"}))
			require.NoError(t, th.Unclaim(ctx, id, ownActor{id: "john"}))
			require.Empty(t, owner(t, th, id))
		})

	t.Run("reassign to a user-named role identifier succeeds",
		func(t *testing.T) {
			th, id := ownTh(t,
				interactor.Eligibility{Roles: slot("john", "mary")})

			require.NoError(t, th.Claim(ctx, id, ownActor{id: "john"}))
			require.NoError(t, th.Reassign(ctx, id, "mary"))
			require.Equal(t, "mary", owner(t, th, id))
		})

	// The inherited asymmetry (SRD-075 §4.5): Reassign checks its nominee
	// through userIDActor, whose Groups() is nil, because an absent person's
	// group membership cannot be authenticated — only a present actor reports
	// its own. So a nominee eligible ONLY via a group-named role identifier
	// cannot be reassigned to, exactly as with candidateGroups.
	t.Run("reassign to a group-only role nominee is refused",
		func(t *testing.T) {
			th, id := ownTh(t,
				interactor.Eligibility{Roles: slot("john", "reviewers")})

			require.NoError(t, th.Claim(ctx, id, ownActor{id: "john"}))
			require.Error(t, th.Reassign(ctx, id, "mary"),
				"mary is only eligible as a member of reviewers, which the "+
					"engine cannot assert for an absent person")
			require.Equal(t, "john", owner(t, th, id))
		})
}
