package thresher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
)

// seedDoc stores a minimal document for id with the given linkage.
func seedDoc(
	t *testing.T, repo repository.Repository,
	group, id, parentID string, children ...string,
) {
	t.Helper()

	doc := &checkpoint.Document{
		InstanceID: id, ProcessID: "p", Status: "Active",
		ParentID: parentID,
	}

	for _, c := range children {
		doc.Calls = append(doc.Calls,
			checkpoint.CallRecord{ChildID: c, NodeID: "call", TrackID: "t"})
	}

	raw, err := doc.Marshal()
	require.NoError(t, err)

	require.NoError(t, repo.Save(t.Context(), repository.InstanceRecord{
		ID: id, Payload: raw, Group: group, Status: repository.StatusActive,
	}))
}

// TestRecoveryRootsDefersChildren pins SRD-087 FR-1: EVERY child is
// dropped from the roots — revived only through its caller's claim,
// never on its own, whatever the listing holds; and an unreadable or
// undecodable record is kept, so recoverOne reports it rather than the
// partition silently swallowing both the instance and the fact.
func TestRecoveryRootsDefersChildren(t *testing.T) {
	repo := memrepo.New()
	require.NoError(t, repo.RegisterGroup(t.Context(), "g"))

	th, err := New("roots", WithRepository(repo))
	require.NoError(t, err)

	seedDoc(t, repo, "g", "P", "", "C")
	seedDoc(t, repo, "g", "C", "P")
	seedDoc(t, repo, "g", "orphan", "gone-parent")

	require.NoError(t, repo.Save(t.Context(), repository.InstanceRecord{
		ID: "garbage", Payload: []byte("{not json"), Group: "g",
		Status: repository.StatusActive,
	}))

	roots := th.recoveryRoots(context.Background(),
		[]string{"C", "P", "orphan", "garbage", "missing"})

	require.Equal(t,
		[]string{"P", "garbage", "missing"}, roots,
		"every child is dropped — even one whose caller isn't listed; "+
			"order is kept")
}

// TestRecoverCallTreeCycleTerminates pins FR-5 with a REAL cycle: A
// calls B and B calls A, so the walk comes back to A and must stop on
// the SHARED seen set threaded through the recursion.
//
// Its previous version pre-seeded seen={A} and handed the walk a
// document whose only call was "A": it returned on the very first
// check and never traversed a cycle, so it would have passed with no
// guard at all.
func TestRecoverCallTreeCycleTerminates(t *testing.T) {
	repo := memrepo.New()
	require.NoError(t, repo.RegisterGroup(t.Context(), "cycle"))

	th, err := New("cycle", WithRepository(repo))
	require.NoError(t, err)

	// the records must carry the ENGINE's own group, or the walk stops
	// at the cross-group refusal before it can recurse.
	seedDoc(t, repo, "cycle", "A", "", "B")
	seedDoc(t, repo, "cycle", "B", "A", "A")

	doc := &checkpoint.Document{
		InstanceID: "A", ProcessID: "p", Status: "Active",
		Calls: []checkpoint.CallRecord{{ChildID: "B"}},
	}

	done := make(chan error, 1)

	go func() {
		done <- th.recoverCallTree(context.Background(), doc,
			map[string]struct{}{"A": {}})
	}()

	select {
	case err := <-done:
		// B cannot finish recovering in this bare engine (its process
		// version is not registered); what this pins is that the walk
		// TERMINATED instead of recursing A→B→A without end.
		require.Error(t, err)
		require.Contains(t, err.Error(), "B")
	case <-time.After(3 * time.Second):
		t.Fatal("the cyclic call tree did not terminate")
	}
}
