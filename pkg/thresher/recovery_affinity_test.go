package thresher_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
)

// SRD-087 — recovery affinity: a call tree recovers as a UNIT, so two
// engines of one group can never split a parent from the child it
// awaits (ADR-033 v.5 §2.10).

// reorderRepo serves ListInFlight in a chosen order, so a test can pin
// that the listing order does NOT decide who claims what.
type reorderRepo struct {
	repository.Repository

	reorder func([]string) []string
}

func (r *reorderRepo) ListInFlight(
	ctx context.Context, group string, now time.Time,
) ([]string, error) {
	ids, err := r.Repository.ListInFlight(ctx, group, now)
	if err != nil || r.reorder == nil {
		return ids, err
	}

	return r.reorder(ids), nil
}

// affinityPair parks a caller+child pair in repo and abandons the
// engine that made them, returning both ids.
func affinityPair(
	t *testing.T, repo repository.Repository,
	parent, callee *process.Process,
) (parentID, childID string) {
	t.Helper()

	parentID, childID, cancel := parkedCall(t, repo, parent, callee)
	t.Cleanup(cancel) // teardown only; the crash is abandonment

	return parentID, childID
}

// TestRecoveryAffinityClaimsTheTree is SRD-087 T-1: the engine's
// listing offers the PARENT but not the child — the state a split
// leaves behind (another engine's claim on the child lapsed after this
// sweep listed, or the child simply wasn't listed). The parent's claim
// must pull the child in transitively, so BOTH end up owned here and
// the caller resumes through the re-link.
//
// This is the affinity discriminator: with the transitive claim
// reverted the child is never claimed by anyone, the caller's
// re-attach falls back to a lazy handle that never settles, and the
// run never completes (verified by reverting).
func TestRecoveryAffinityClaimsTheTree(t *testing.T) {
	base := memrepo.New()

	var gate atomic.Int32

	callee := parkingCallee(t, "af-ee", &gate)
	parent := callerOf(t, "af-er", callee)

	parentID, childID := affinityPair(t, base, parent, callee)

	// the child is NOT offered by the listing — only the parent is.
	repo := &reorderRepo{Repository: base,
		reorder: func(ids []string) []string {
			out := make([]string, 0, len(ids))

			for _, id := range ids {
				if id != childID {
					out = append(out, id)
				}
			}

			return out
		}}

	time.Sleep(120 * time.Millisecond) // the leases lapse
	gate.Store(1)                      // the recovered child can drain

	_, _, cancel := bootCallEngine(t, "engine-2", repo,
		time.Minute, parent, callee)
	defer cancel()

	ctx := context.Background()

	require.Eventually(t, func() bool {
		prec, pok, _ := base.Load(ctx, parentID)
		crec, cok, _ := base.Load(ctx, childID)

		return pok && cok &&
			prec.Status == repository.StatusCompleted &&
			crec.Status == repository.StatusCompleted &&
			prec.Lease.Owner == crec.Lease.Owner
	}, 5*time.Second, 10*time.Millisecond,
		"the parent's claim must pull in the unlisted child and complete")
}

// TestRecoveryAffinityIgnoresListingOrder is T-2: with the child
// listed FIRST, it is still deferred to its parent's claim.
func TestRecoveryAffinityIgnoresListingOrder(t *testing.T) {
	base := memrepo.New()

	var gate atomic.Int32

	callee := parkingCallee(t, "af-ord-ee", &gate)
	parent := callerOf(t, "af-ord-er", callee)

	parentID, childID := affinityPair(t, base, parent, callee)

	// the child first — the order that made the split likely.
	repo := &reorderRepo{Repository: base, reorder: func(ids []string) []string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			if id == childID {
				out = append([]string{id}, out...)

				continue
			}

			out = append(out, id)
		}

		return out
	}}

	time.Sleep(120 * time.Millisecond)
	gate.Store(1)

	_, _, cancel := bootCallEngine(t, "engine-ord", repo,
		time.Minute, parent, callee)
	defer cancel()

	ctx := context.Background()

	require.Eventually(t, func() bool {
		prec, pok, _ := base.Load(ctx, parentID)

		return pok && prec.Status == repository.StatusCompleted
	}, 5*time.Second, 10*time.Millisecond,
		"a child-first listing must still recover the tree as a unit")
}

// TestRecoveryAffinityTerminalCaller is T-5 (SRD-087 FR-6): a child
// whose caller is TERMINAL is an interrupted cancel cascade — recovery
// finishes it (the record is written terminal, with its fact) instead
// of reviving an instance whose outcome nothing will consume. Before
// this rule the child was happily revived: recoverOne checked the
// caller's EXISTENCE, never its state.
func TestRecoveryAffinityTerminalCaller(t *testing.T) {
	repo := memrepo.New()

	var gate atomic.Int32

	callee := parkingCallee(t, "af-orph-ee", &gate)
	parent := callerOf(t, "af-orph-er", callee)

	parentID, childID := affinityPair(t, repo, parent, callee)

	ctx := context.Background()

	// mark the caller terminal: it leaves the claimable listing, so the
	// child is a listing root on its own.
	prec, ok, err := repo.Load(ctx, parentID)
	require.NoError(t, err)
	require.True(t, ok)

	prec.Status = repository.StatusCompleted
	require.NoError(t, repo.Save(ctx, prec))

	time.Sleep(120 * time.Millisecond)
	gate.Store(1) // would let a REVIVED child run to completion

	_, fw, cancel := bootCallEngine(t, "engine-orph", repo,
		time.Minute, parent, callee)
	defer cancel()

	require.Eventually(t, func() bool {
		crec, cok, _ := repo.Load(ctx, childID)

		return cok && crec.Status == repository.StatusTerminated
	}, 5*time.Second, 10*time.Millisecond,
		"a terminal caller's child is terminated, not revived")

	// the fan-out is asynchronous — poll, as the other fact assertions do.
	require.Eventually(t, func() bool {
		return sawTerminatedFor(fw, childID)
	}, 3*time.Second, 5*time.Millisecond,
		"the finished cascade is reported, never silent")

	// and it was never run: no Recovered fact for it.
	require.False(t, fw.saw(observability.KindInstanceState,
		observability.PhaseRecovered),
		"the child must not be revived")
}

// sawTerminatedFor reports whether the cascade-finish fact for id was
// emitted (SRD-087 FR-6).
func sawTerminatedFor(fw *factWatch, id string) bool {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	for _, f := range fw.facts {
		if f.Kind == observability.KindInstanceState &&
			f.Phase == observability.PhaseTerminated &&
			f.Details[observability.AttrInstanceID] == id &&
			f.Details["reason"] == "caller-terminal" {
			return true
		}
	}

	return false
}

// recoveryFailureNames reports whether a recovery-failure fact carries
// the given id in its error text (the loud refusal of FR-3).
func recoveryFailureNames(fw *factWatch, id string) bool {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	for _, f := range fw.facts {
		if f.Kind != observability.KindInstanceState ||
			f.Phase != observability.PhaseFailed {
			continue
		}

		if f.Details["reason"] == "recovery" &&
			strings.Contains(f.Details[observability.AttrError], id) {
			return true
		}
	}

	return false
}

// TestRecoveryAffinityUnclaimableChild is T-4 (SRD-087 FR-3): a child
// whose lease is LIVE on another engine fails the parent's recovery
// loud, naming the child — and the child itself is untouched.
func TestRecoveryAffinityUnclaimableChild(t *testing.T) {
	base := memrepo.New()

	var gate atomic.Int32

	callee := parkingCallee(t, "af-live-ee", &gate)
	parent := callerOf(t, "af-live-er", callee)

	parentID, childID := affinityPair(t, base, parent, callee)

	ctx := context.Background()

	// another engine holds the child under a LIVE lease.
	crec, ok, err := base.Load(ctx, childID)
	require.NoError(t, err)
	require.True(t, ok)

	crec.Lease = repository.Lease{
		Owner:       "engine-elsewhere",
		Incarnation: crec.Lease.Incarnation + 1,
		Expiry:      time.Now().Add(time.Hour),
	}
	require.NoError(t, base.Save(ctx, crec))

	// only the parent is claimable now; the recovering engine must
	// refuse its tree rather than half-recover it.
	time.Sleep(120 * time.Millisecond)

	_, fw, cancel := bootCallEngine(t, "engine-live", base,
		time.Minute, parent, callee)
	defer cancel()

	require.Eventually(t, func() bool {
		return recoveryFailureNames(fw, childID)
	}, 3*time.Second, 5*time.Millisecond,
		"an unclaimable child must fail the parent's recovery loud, "+
			"naming the child")

	// the child is untouched: still owned by the other engine.
	after, ok, err := base.Load(ctx, childID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "engine-elsewhere", after.Lease.Owner,
		"the live child keeps running elsewhere")

	prec, ok, err := base.Load(ctx, parentID)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEqual(t, repository.StatusCompleted, prec.Status,
		"the caller does not half-recover")
}
