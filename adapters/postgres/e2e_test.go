package postgres_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/adapters/postgres"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// SRD-078 T-3 (FR-7) and T-8 (FR-8): the fencing and the
// kill-and-resume proofs against real postgres.

// TestZombieFencing — the ADR-033 §2.8 proof (T-3): engine A owns an
// instance; its lease lapses; engine B claims under incarnation+1;
// A's next save — carrying the stale RecVersion — is rejected with
// ConcurrentUpdate, so a lapsed owner can never overwrite the new
// owner's state.
func TestZombieFencing(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, repo.RegisterGroup(ctx, "g"))

	// engine A creates the record under its lease.
	held := repository.InstanceRecord{
		ID:      "i-1",
		Group:   "g",
		Status:  repository.StatusActive,
		Payload: []byte(`{"schema":1,"owner":"a"}`),
		Lease: repository.Lease{
			Owner:       "engine-a",
			Incarnation: 1,
			Expiry:      now.Add(-time.Second), // already lapsed
		},
	}
	require.NoError(t, repo.Save(ctx, held))

	// A's view of its own record: stored version 1.
	zombieView, ok, err := repo.Load(ctx, "i-1")
	require.NoError(t, err)
	require.True(t, ok)

	// the lapsed record lists as claimable; B claims: CAS save under a
	// HIGHER incarnation (the claim IS the save — no separate method).
	ids, err := repo.ListInFlight(ctx, "g", time.Now())
	require.NoError(t, err)
	require.Equal(t, []string{"i-1"}, ids)

	claim := zombieView
	claim.Lease = repository.Lease{
		Owner:       "engine-b",
		Incarnation: zombieView.Lease.Incarnation + 1,
		Expiry:      now.Add(time.Minute),
	}
	claim.Payload = []byte(`{"schema":1,"owner":"b"}`)
	require.NoError(t, repo.Save(ctx, claim))

	// the zombie wakes and writes through its STALE version — fenced.
	zombieView.Payload = []byte(`{"schema":1,"owner":"a-again"}`)

	err = repo.Save(ctx, zombieView)
	require.Error(t, err)

	var ae *errs.ApplicationError
	require.True(t,
		errors.As(err, &ae) && ae.HasClass(errs.ConcurrentUpdate),
		"the stale save must be ConcurrentUpdate-classified, got %v", err)

	// the new owner's state is intact.
	got, ok, err := repo.Load(ctx, "i-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "engine-b", got.Lease.Owner)
	require.Equal(t, int64(2), got.Lease.Incarnation)
	require.Equal(t, []byte(`{"schema":1,"owner":"b"}`), got.Payload)
}

// factWatch collects engine facts for the recovery assertions.
type factWatch struct {
	mu    sync.Mutex
	facts []observability.Fact
}

func (fw *factWatch) OnFact(f observability.Fact) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	fw.facts = append(fw.facts, f)
}

func (fw *factWatch) saw(k observability.Kind, p observability.Phase) bool {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	for _, f := range fw.facts {
		if f.Kind == k && f.Phase == p {
			return true
		}
	}

	return false
}

// timerProc builds start → timer(when) → hit-lane → end with PINNED
// node ids (cross-engine recovery demands stable element identity —
// the deployment-parity contract, ADR-033 §2.8).
func timerProc(
	t *testing.T, key string, when time.Time, hit *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	texpr, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Time{})),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(when), nil
		})
	require.NoError(t, err)

	def, err := events.NewTimerEventDefinition(texpr, nil, nil)
	require.NoError(t, err)

	wait, err := events.NewIntermediateCatchEvent("wait", def,
		foundation.WithID(key+"-wait"))
	require.NoError(t, err)

	op, err := gooper.New(key+"-lane",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			hit.Store(true)

			return nil, nil
		})
	require.NoError(t, err)

	lane, err := activities.NewServiceTask(key+"-lane", op,
		activities.WithoutParams(), foundation.WithID(key+"-lane"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, wait, lane, end} {
		require.NoError(t, p.Add(e))
	}

	for _, pair := range [][2]flow.Element{
		{start, wait}, {wait, lane}, {lane, end},
	} {
		_, err := flow.Link(pair[0].(flow.SequenceSource),
			pair[1].(flow.SequenceTarget))
		require.NoError(t, err)
	}

	return p
}

// bootEngine runs an engine over the shared store in the given group,
// with the process registered BEFORE Run (deployment parity).
func bootEngine(
	t *testing.T, name, group string, repo *postgres.Repo,
	ttl time.Duration, p *process.Process,
) (*thresher.Thresher, *factWatch, context.CancelFunc) {
	t.Helper()

	th, err := thresher.New(name,
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithEngineGroup(group),
		thresher.WithLeaseTTL(ttl))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)
	t.Cleanup(sub.Cancel)

	ctx, cancel := context.WithCancel(context.Background())

	_, err = th.RegisterProcess(p)
	require.NoError(t, err)
	require.NoError(t, th.Run(ctx))

	return th, fw, cancel
}

// TestKillAndResume — the FR-8 e2e over real postgres: engine A parks
// on a timer and is ABANDONED (the "kill" is abandonment: the Active
// record and its expiring lease stay behind); engine B over the same
// database and group recovers, the timer fires at the RECORDED
// deadline, the instance completes under B's lease.
func TestKillAndResume(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	deadline := time.Now().Add(1200 * time.Millisecond)

	var hitA, hitB atomic.Bool

	p1 := timerProc(t, "pg-e2e", deadline, &hitA)

	thA, _, cancelA := bootEngine(t, "engine-a", "g", repo,
		80*time.Millisecond, p1)
	defer cancelA() // teardown only; the "kill" is abandonment

	h, err := thA.StartLatest(p1.ID())
	require.NoError(t, err)

	instID := h.ID()

	// the park checkpoint lands in postgres.
	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(ctx, instID)

		return ok && rec.Status == repository.StatusActive &&
			rec.Lease.Owner == "engine-a"
	}, 3*time.Second, 10*time.Millisecond,
		"engine A must checkpoint the parked instance")

	time.Sleep(120 * time.Millisecond) // A's lease lapses

	p2 := timerProc(t, "pg-e2e", deadline, &hitB)

	_, fwB, cancelB := bootEngine(t, "engine-b", "g", repo,
		time.Minute, p2)
	defer cancelB()

	require.Eventually(t, func() bool {
		return fwB.saw(observability.KindInstanceState,
			observability.PhaseRecovered)
	}, 3*time.Second, 10*time.Millisecond,
		"engine B must claim and recover the abandoned instance")

	require.Eventually(t, func() bool { return hitB.Load() },
		3*time.Second, 10*time.Millisecond,
		"the restored timer must fire at the recorded deadline on B")

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(ctx, instID)

		return ok && rec.Status == repository.StatusCompleted &&
			rec.Lease.Owner == "engine-b"
	}, 3*time.Second, 10*time.Millisecond,
		"the completed record must belong to the recovering engine")
}

// TestOtherGroupRecoversNothing — FR-8's second half: an engine in
// ANOTHER group over the same database sees nothing of group g's
// abandoned instance.
func TestOtherGroupRecoversNothing(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	deadline := time.Now().Add(time.Hour)

	var hitA, hitH atomic.Bool

	p1 := timerProc(t, "pg-e2e-fg", deadline, &hitA)

	thA, _, cancelA := bootEngine(t, "engine-a", "g", repo,
		80*time.Millisecond, p1)
	defer cancelA()

	h, err := thA.StartLatest(p1.ID())
	require.NoError(t, err)

	instID := h.ID()

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(ctx, instID)

		return ok && rec.Status == repository.StatusActive
	}, 3*time.Second, 10*time.Millisecond)

	time.Sleep(120 * time.Millisecond) // the lease lapses

	p2 := timerProc(t, "pg-e2e-fg", deadline, &hitH)

	_, fwH, cancelH := bootEngine(t, "engine-h", "h", repo,
		time.Minute, p2)
	defer cancelH()

	// recovery is synchronous within Run: nothing recovered, the
	// record still belongs to engine A.
	require.False(t, fwH.saw(observability.KindInstanceState,
		observability.PhaseRecovered),
		"group h must never recover group g's instance")

	rec, ok, err := repo.Load(ctx, instID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "engine-a", rec.Lease.Owner)
}
