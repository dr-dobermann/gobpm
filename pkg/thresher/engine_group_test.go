package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// SRD-078 T-6 — the engine-group options (FR-2): validation, the solo
// default, the Run-time establish/assert semantics and the group scope
// of recovery.

func TestEngineGroupOptions(t *testing.T) {
	base := []thresher.Option{
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
	}

	t.Run("an empty group name is rejected", func(t *testing.T) {
		_, err := thresher.New("e-1",
			append(base, thresher.WithEngineGroup("   "))...)
		require.Error(t, err)
		require.Contains(t, err.Error(), "WithEngineGroup")
	})

	t.Run("an empty join-only group name is rejected", func(t *testing.T) {
		_, err := thresher.New("e-2",
			append(base, thresher.WithExistingEngineGroup(""))...)
		require.Error(t, err)
		require.Contains(t, err.Error(), "WithExistingEngineGroup")
	})

	t.Run("a second group set is rejected", func(t *testing.T) {
		_, err := thresher.New("e-3",
			append(base,
				thresher.WithEngineGroup("alpha"),
				thresher.WithExistingEngineGroup("beta"))...)
		require.Error(t, err)
		require.Contains(t, err.Error(), "already set")
	})

	t.Run("join-only demands an explicit repository", func(t *testing.T) {
		_, err := thresher.New("e-4",
			append(base, thresher.WithExistingEngineGroup("cluster"))...)
		require.Error(t, err)
		require.Contains(t, err.Error(), "WithRepository")
	})

	t.Run("the group name is trimmed", func(t *testing.T) {
		repo := memrepo.New()

		th, err := thresher.New("e-5",
			append(base,
				thresher.WithRepository(repo),
				thresher.WithEngineGroup("  padded-group  "))...)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		require.NoError(t, th.Run(ctx))

		ok, err := repo.GroupExists(ctx, "padded-group")
		require.NoError(t, err)
		require.True(t, ok, "Run must register the TRIMMED group name")
	})
}

// TestSoloDefaultGroup: an ungrouped engine is a single-engine group
// under its own id (§4.7) — established at Run and stamped on every
// record it writes.
func TestSoloDefaultGroup(t *testing.T) {
	repo := memrepo.New()

	var hit atomic.Bool

	p := timerProc(t, "solo-proc", time.Now().Add(time.Hour), &hit)

	th, err := thresher.New("solo-engine",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)
	require.NoError(t, th.Run(ctx))

	ok, err := repo.GroupExists(ctx, "solo-engine")
	require.NoError(t, err)
	require.True(t, ok, "the solo group must be established under the engine id")

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		rec, found, _ := repo.Load(context.Background(), h.ID())

		return found && rec.Group == "solo-engine"
	}, 2*time.Second, 5*time.Millisecond,
		"the parked record must carry the solo group")
}

// TestExistingGroupRun: WithExistingEngineGroup asserts at Run — an
// absent group refuses the start loud; an established one joins.
func TestExistingGroupRun(t *testing.T) {
	t.Run("an absent group refuses the start", func(t *testing.T) {
		th, err := thresher.New("joiner-1",
			thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
			thresher.WithRepository(memrepo.New()),
			thresher.WithExistingEngineGroup("cluster-9"))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err = th.Run(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cluster-9")
	})

	t.Run("an established group joins", func(t *testing.T) {
		repo := memrepo.New()
		require.NoError(t,
			repo.RegisterGroup(context.Background(), "cluster-9"))

		th, err := thresher.New("joiner-2",
			thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
			thresher.WithRepository(repo),
			thresher.WithExistingEngineGroup("cluster-9"))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		require.NoError(t, th.Run(ctx))
	})
}

// TestRecoveryIgnoresForeignGroups: recovery lists the engine's own
// group only — a differently-grouped neighbor over the same store sees
// nothing and touches nothing.
func TestRecoveryIgnoresForeignGroups(t *testing.T) {
	repo := memrepo.New()

	var hit1, hit2 atomic.Bool

	deadline := time.Now().Add(time.Hour)
	p1 := timerProc(t, "fg-proc", deadline, &hit1)

	th1, _, cancel1 := bootEngine(t, "engine-1", repo,
		80*time.Millisecond, p1)
	defer cancel1()

	h, err := th1.StartLatest(p1.ID())
	require.NoError(t, err)

	instID := h.ID()

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), instID)

		return ok && rec.Status == repository.StatusActive
	}, 2*time.Second, 5*time.Millisecond)

	time.Sleep(120 * time.Millisecond) // the lease lapses

	// the neighbor lives in ANOTHER group over the same repository.
	p2 := timerProc(t, "fg-proc", deadline, &hit2)

	th2, err := thresher.New("engine-2",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithEngineGroup("another-cluster"))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th2.Observe(fw)
	t.Cleanup(sub.Cancel)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	_, err = th2.RegisterProcess(p2)
	require.NoError(t, err)
	require.NoError(t, th2.Run(ctx2))

	// recovery is synchronous within Run — no Recovered fact may exist,
	// and the abandoned record must still belong to engine-1.
	require.False(t, fw.saw(observability.KindInstanceState,
		observability.PhaseRecovered),
		"a foreign-group record must never be recovered")

	rec, ok, err := repo.Load(context.Background(), instID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "engine-1", rec.Lease.Owner,
		"the neighbor must not have claimed the record")
}

// foreignLister leaks another group's ids out of the listing — the
// defense-in-depth probe for the claim-time group check.
type foreignLister struct {
	*memrepo.Repo
}

func (fl *foreignLister) ListInFlight(
	ctx context.Context, _ string, now time.Time,
) ([]string, error) {
	return fl.Repo.ListInFlight(ctx, "other-group", now)
}

// TestRecoveryRefusesForeignRecord: even when a listing leaks a record
// of another group, the claim refuses it loud (FR-2's last-line check)
// and the engine still starts.
func TestRecoveryRefusesForeignRecord(t *testing.T) {
	inner := memrepo.New()
	ctx := context.Background()

	require.NoError(t, inner.RegisterGroup(ctx, "other-group"))
	require.NoError(t, inner.Save(ctx, repository.InstanceRecord{
		ID:     "foreign-1",
		Status: repository.StatusActive,
		Group:  "other-group",
	}))

	th, err := thresher.New("engine-own",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
		thresher.WithRepository(&foreignLister{Repo: inner}))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)
	t.Cleanup(sub.Cancel)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(runCtx),
		"a refused foreign record must never block the start")

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseFailed)
	}, 2*time.Second, 5*time.Millisecond,
		"the refusal must be loud")

	rec, ok, err := inner.Load(ctx, "foreign-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "other-group", rec.Group,
		"the foreign record must stay untouched")
	require.Empty(t, rec.Lease.Owner)
}
