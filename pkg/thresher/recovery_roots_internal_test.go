package thresher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	gerrs "github.com/dr-dobermann/gobpm/pkg/errs"
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
// The assertion is the LOAD COUNT, not merely termination, and that
// distinction is the point. Termination here is over-determined: even
// with no seen bookkeeping at all the second visit to a record finds
// the lease this very sweep just took, so the claim reports "not mine"
// and the recursion unwinds. A test that only asserted "it stopped"
// therefore passed with the guard deleted — measured, not assumed. What
// the guard actually buys is that no id is processed twice, and a
// repeat costs exactly one extra Load, which the counting repository
// below makes visible.
//
// An earlier version pre-seeded seen={A} and handed the walk a document
// whose only call was "A": it returned on the very first check and
// never traversed a cycle.
func TestRecoverCallTreeCycleTerminates(t *testing.T) {
	base := memrepo.New()
	require.NoError(t, base.RegisterGroup(t.Context(), "cycle"))

	repo := &countingRepo{Repository: base, loads: map[string]int{}}

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

	// A is loaded once by B's tree walk (the child-record read) and
	// never claimed again; B is loaded by A's walk and by its own claim.
	// Without the seen bookkeeping the walk revisits and every count
	// grows.
	require.Equal(t, 1, repo.loadCount("A"),
		"the cycle must come back to A exactly once")
	require.Equal(t, 2, repo.loadCount("B"),
		"B is read by the tree walk and by its own claim, once each")
}

// countingRepo counts Load calls per id so a walk that revisits a
// record is visible as a number rather than inferred from the fact that
// the test did not hang.
type countingRepo struct {
	repository.Repository

	mu    sync.Mutex
	loads map[string]int
}

func (cr *countingRepo) Load(
	ctx context.Context, id string,
) (repository.InstanceRecord, bool, error) {
	cr.mu.Lock()
	cr.loads[id]++
	cr.mu.Unlock()

	return cr.Repository.Load(ctx, id)
}

func (cr *countingRepo) loadCount(id string) int {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	return cr.loads[id]
}

// failingSaveRepo fails Save for one id with a chosen error, so the two
// halves of a rejected claim can be told apart.
type failingSaveRepo struct {
	repository.Repository

	id  string
	err error
}

func (fr *failingSaveRepo) Save(
	ctx context.Context, rec repository.InstanceRecord,
) error {
	if rec.ID == fr.id {
		return fr.err
	}

	return fr.Repository.Save(ctx, rec)
}

// TestClaimSurfacesStoreFailure: only a CAS mismatch is a lost race.
// The Repository contract classifies one errs.ConcurrentUpdate; anything
// else is a store that is not working, and swallowing it would abandon a
// recoverable instance silently.
func TestClaimSurfacesStoreFailure(t *testing.T) {
	base := memrepo.New()
	require.NoError(t, base.RegisterGroup(t.Context(), "store"))
	seedDoc(t, base, "store", "A", "")

	repo := &failingSaveRepo{
		Repository: base,
		id:         "A",
		err:        errors.New("the store is unreachable"),
	}

	th, err := New("store", WithRepository(repo))
	require.NoError(t, err)

	claimed, err := th.recoverOne(context.Background(), "A",
		map[string]struct{}{})
	require.False(t, claimed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "the claim doesn't save")
}

// TestClaimLostRaceIsSilent is the other half: a CAS mismatch IS the
// normal outcome of two engines sweeping at once, and reports nothing.
func TestClaimLostRaceIsSilent(t *testing.T) {
	base := memrepo.New()
	require.NoError(t, base.RegisterGroup(t.Context(), "race"))
	seedDoc(t, base, "race", "A", "")

	repo := &failingSaveRepo{
		Repository: base,
		id:         "A",
		err: gerrs.New(gerrs.M("someone else got there first"),
			gerrs.C(errorClass, gerrs.ConcurrentUpdate)),
	}

	th, err := New("race", WithRepository(repo))
	require.NoError(t, err)

	claimed, err := th.recoverOne(context.Background(), "A",
		map[string]struct{}{})
	require.NoError(t, err)
	require.False(t, claimed, "a lost CAS race leaves the record alone")
}

// TestCallTreeFailsOnLostChildClaim pins SRD-087 FR-3 through the window
// the lease check cannot cover: the child's lease reads expired, and
// another engine claims it before our own CAS lands. The parent must
// not restore against a child it does not own.
func TestCallTreeFailsOnLostChildClaim(t *testing.T) {
	base := memrepo.New()
	require.NoError(t, base.RegisterGroup(t.Context(), "lost"))
	seedDoc(t, base, "lost", "P", "", "C")
	seedDoc(t, base, "lost", "C", "P")

	repo := &failingSaveRepo{
		Repository: base,
		id:         "C",
		err: gerrs.New(gerrs.M("the child was claimed elsewhere"),
			gerrs.C(errorClass, gerrs.ConcurrentUpdate)),
	}

	th, err := New("lost", WithRepository(repo))
	require.NoError(t, err)

	doc := &checkpoint.Document{
		InstanceID: "P", ProcessID: "p", Status: "Active",
		Calls: []checkpoint.CallRecord{{ChildID: "C"}},
	}

	err = th.recoverCallTree(context.Background(), doc,
		map[string]struct{}{"P": {}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "claimed by another engine mid-sweep")
}
