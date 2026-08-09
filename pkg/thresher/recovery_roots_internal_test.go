package thresher

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
)

// seedDoc stores a minimal document for id with the given linkage.
func seedDoc(
	t *testing.T, repo repository.Repository,
	id, parentID string, children ...string,
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
		ID: id, Payload: raw, Group: "g", Status: repository.StatusActive,
	}))
}

// TestRecoveryRootsDefersChildren pins SRD-087 FR-1: a child whose
// caller is in the SAME listing is deferred to the parent's claim; a
// child whose caller is absent stays a root; and an unreadable or
// undecodable record is kept, so recoverOne reports it rather than the
// partition silently swallowing both the instance and the fact.
func TestRecoveryRootsDefersChildren(t *testing.T) {
	repo := memrepo.New()
	require.NoError(t, repo.RegisterGroup(t.Context(), "g"))

	th, err := New("roots", WithRepository(repo))
	require.NoError(t, err)

	seedDoc(t, repo, "P", "", "C")
	seedDoc(t, repo, "C", "P")
	seedDoc(t, repo, "orphan", "gone-parent")

	require.NoError(t, repo.Save(t.Context(), repository.InstanceRecord{
		ID: "garbage", Payload: []byte("{not json"), Group: "g",
		Status: repository.StatusActive,
	}))

	roots := th.recoveryRoots(context.Background(),
		[]string{"C", "P", "orphan", "garbage", "missing"})

	require.Equal(t,
		[]string{"P", "orphan", "garbage", "missing"}, roots,
		"only the child of a LISTED parent is deferred; order is kept")
}

// TestRecoverCallTreeCycleTerminates pins FR-5: a document naming its
// own ancestor cannot loop the walk.
func TestRecoverCallTreeCycleTerminates(t *testing.T) {
	repo := memrepo.New()
	require.NoError(t, repo.RegisterGroup(t.Context(), "g"))

	th, err := New("cycle", WithRepository(repo))
	require.NoError(t, err)

	// A calls B, B calls A — a malformed pair.
	seedDoc(t, repo, "A", "", "B")
	seedDoc(t, repo, "B", "A", "A")

	doc := &checkpoint.Document{
		InstanceID: "A", ProcessID: "p", Status: "Active",
		Calls: []checkpoint.CallRecord{{ChildID: "A"}},
	}

	// "A" is already in seen (the caller marks itself), so the walk
	// terminates instead of recursing into itself.
	require.NoError(t, th.recoverCallTree(context.Background(), doc,
		map[string]struct{}{"A": {}}))
}
