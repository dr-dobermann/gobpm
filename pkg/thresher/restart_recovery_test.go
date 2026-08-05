package thresher_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
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
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// factWatch collects engine-wide facts for the recovery assertions.
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

// timeExpr builds a Time-typed expression returning when.
func timeExpr(t *testing.T, when time.Time) data.FormalExpression {
	t.Helper()

	e, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(time.Time{})),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return values.NewVariable(when), nil
		})
	require.NoError(t, err)

	return e
}

// timerProc builds start → timer(when) → [lane] → end under the given
// process key (shared by both engines — deployment parity).
func timerProc(
	t *testing.T, key string, when time.Time, hit *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	// Every node id is PINNED: cross-engine recovery demands stable
	// element identity — two engines building the model from the same
	// code otherwise mint different ids, and the recorded node cannot
	// resolve in the recovering engine's registration (the deployment-
	// parity contract, ADR-033 §2.8, covers ids too).
	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	def, err := events.NewTimerEventDefinition(timeExpr(t, when), nil, nil)
	require.NoError(t, err)

	wait, err := events.NewIntermediateCatchEvent("wait", def,
		foundation.WithID(key+"-wait"))
	require.NoError(t, err)

	lane := pinnedLane(t, key+"-lane", hit)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, wait, lane, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, wait)
	link(t, wait, lane)
	link(t, lane, end)

	return p
}

// recoveryGroup is the engine group the restart-recovery pairs share:
// under solo-by-default (SRD-078 FR-2) engine-1 and engine-2 would land
// in different groups and never see each other's records.
const recoveryGroup = "recovery-cluster"

// bootEngine builds an armed engine over the shared repo, registers the
// process and returns the engine + its fact watch.
func bootEngine(
	t *testing.T, name string, repo repository.Repository,
	ttl time.Duration, p *process.Process,
) (*thresher.Thresher, *factWatch, context.CancelFunc) {
	t.Helper()

	th, err := thresher.New(name,
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithEngineGroup(recoveryGroup),
		thresher.WithLeaseTTL(ttl))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)
	t.Cleanup(sub.Cancel)

	ctx, cancel := context.WithCancel(context.Background())

	// register BEFORE Run: recovery resolves the pinned version at Run
	// (SRD-070 FR-7 -- deployment parity is the operator's contract).
	_, err = th.RegisterProcess(p)
	require.NoError(t, err)

	require.NoError(t, th.Run(ctx))

	return th, fw, cancel
}

// TestRestartRecoveryTimer covers SRD-070 T-6's timer trace + T-7's
// fencing: engine-1 parks on a timer and is ABANDONED (a crash leaves
// the Active record + an expiring lease — no terminal write); engine-2
// over the same repository claims after the lease lapses, restores at
// the RECORDED deadline and completes; the zombie engine-1's late saves
// are CAS-fenced into CheckpointDeferred.
func TestRestartRecoveryTimer(t *testing.T) {
	repo := memrepo.New()
	deadline := time.Now().Add(700 * time.Millisecond)

	var hit1, hit2 atomic.Bool

	p1 := timerProc(t, "rr-timer", deadline, &hit1)

	th1, fw1, cancel1 := bootEngine(t, "engine-1", repo,
		80*time.Millisecond, p1)
	defer cancel1() // teardown only; the "crash" is abandonment, not cancel

	h, err := th1.StartLatest(p1.ID())
	require.NoError(t, err)

	instID := h.ID()

	// wait until the park checkpoint exists, then let the lease lapse.
	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), instID)

		return ok && rec.Status == repository.StatusActive
	}, 2*time.Second, 5*time.Millisecond)

	time.Sleep(120 * time.Millisecond) // > engine-1's lease TTL

	p2 := timerProc(t, "rr-timer", deadline, &hit2)

	_, fw2, cancel2 := bootEngine(t, "engine-2", repo, time.Minute, p2)
	defer cancel2()

	require.Eventually(t, func() bool {
		return fw2.saw(observability.KindInstanceState,
			observability.PhaseRecovered)
	}, 2*time.Second, 5*time.Millisecond,
		"engine-2 must claim and recover the abandoned instance")

	// the recovered instance fires at the RECORDED deadline and completes.
	require.Eventually(t, func() bool {
		return hit2.Load()
	}, 3*time.Second, 5*time.Millisecond,
		"the restored timer must fire on engine-2")

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), instID)

		return ok && rec.Status == repository.StatusCompleted &&
			rec.Lease.Owner == "engine-2"
	}, 3*time.Second, 10*time.Millisecond,
		"the completed record must belong to the recovering engine")

	// T-7: the zombie engine-1 also fires (its own memory copy) but its
	// saves are fenced — the degradation fact, never a state overwrite.
	require.Eventually(t, func() bool {
		return fw1.saw(observability.KindInstanceState,
			observability.PhaseCheckpointDeferred)
	}, 3*time.Second, 10*time.Millisecond,
		"the zombie's late save must be fenced into CheckpointDeferred")
}

// pinnedLane is laneTask with a PINNED node id (stable element identity
// across engines — the recovery requirement).
func pinnedLane(
	t *testing.T, id string, hit *atomic.Bool,
) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(id,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			hit.Store(true)

			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(id, op,
		activities.WithoutParams(), foundation.WithID(id))
	require.NoError(t, err)

	return st
}

// TestRestartRecoveryOverdueTimer: the recorded deadline passed during
// the downtime — the restored timer fires ONCE, immediately.
func TestRestartRecoveryOverdueTimer(t *testing.T) {
	repo := memrepo.New()
	deadline := time.Now().Add(250 * time.Millisecond)

	var hit1, hit2 atomic.Bool

	p1 := timerProc(t, "rr-overdue", deadline, &hit1)

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

	// "crash" engine-1 BEFORE the deadline: fence it out with a foreign
	// claim whose lease is already expired (claimable by anyone), then
	// cancel it — its terminal write is CAS-fenced away, so the record
	// stays Active with the recorded deadline in the past.
	rec, ok, err := repo.Load(context.Background(), instID)
	require.True(t, ok)
	require.NoError(t, err)

	rec.Lease = repository.Lease{
		Owner:       "crash-sim",
		Incarnation: rec.Lease.Incarnation + 1,
		Expiry:      time.Now().Add(-time.Second),
	}
	require.NoError(t, repo.Save(context.Background(), rec))

	cancel1()

	// let the deadline pass while nobody runs the instance.
	time.Sleep(400 * time.Millisecond)

	p2 := timerProc(t, "rr-overdue", deadline, &hit2)

	_, fw2, cancel2 := bootEngine(t, "engine-2", repo, time.Minute, p2)
	defer cancel2()

	require.Eventually(t, func() bool {
		return fw2.saw(observability.KindInstanceState,
			observability.PhaseRecovered)
	}, 2*time.Second, 5*time.Millisecond)

	require.Eventually(t, func() bool { return hit2.Load() },
		time.Second, 5*time.Millisecond,
		"an overdue recorded deadline must fire immediately, once")

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), instID)

		return ok && rec.Status == repository.StatusCompleted
	}, 2*time.Second, 10*time.Millisecond)
}

// condProc builds start → conditional catch(shared val) → [lane] → end
// with pinned ids; both engines share val through the closure.
//
// val is a values.Variable rather than a plain bool because the engine
// evaluates the condition on the instance's own goroutine while the test flips
// it from the test goroutine. Variable guards its own Get and Update with a
// mutex, so sharing one is safe; a bare bool here was a data race that failed
// the run under -race.
func condProc(
	t *testing.T, key string, val *values.Variable[bool], hit *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	cond, err := goexpr.New(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, _ data.Source) (data.Value, error) {
			on, err := data.As[bool](ctx, val)
			if err != nil {
				return nil, err
			}

			return values.NewVariable(on), nil
		})
	require.NoError(t, err)

	def, err := events.NewConditionalEventDefinition(cond)
	require.NoError(t, err)

	wait, err := events.NewIntermediateCatchEvent("wait", def,
		foundation.WithID(key+"-wait"))
	require.NoError(t, err)

	lane := pinnedLane(t, key+"-lane", hit)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, wait, lane, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, wait)
	link(t, wait, lane)
	link(t, lane, end)

	return p
}

// TestRestartRecoveryConditional: a condition that turned true during
// the downtime fires on recovery — re-arming re-runs the initial
// evaluation over the restored world (SRD-070 §4.6).
func TestRestartRecoveryConditional(t *testing.T) {
	repo := memrepo.New()

	val := values.NewVariable(false)

	var hit1, hit2 atomic.Bool

	p1 := condProc(t, "rr-cond", val, &hit1)

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

	// "during the downtime" the condition's world changes.
	require.NoError(t, val.Update(context.Background(), true))

	time.Sleep(120 * time.Millisecond) // the lease lapses

	p2 := condProc(t, "rr-cond", val, &hit2)

	_, fw2, cancel2 := bootEngine(t, "engine-2", repo, time.Minute, p2)
	defer cancel2()

	require.Eventually(t, func() bool {
		return fw2.saw(observability.KindInstanceState,
			observability.PhaseRecovered)
	}, 2*time.Second, 5*time.Millisecond)

	require.Eventually(t, func() bool { return hit2.Load() },
		2*time.Second, 5*time.Millisecond,
		"the now-true condition must fire on recovery")
}

// annCollector is a TaskDistributor recording announcements.
type annCollector struct {
	mu    sync.Mutex
	tasks []string
}

func (ac *annCollector) Distribute(
	_ context.Context, task interactor.TaskInfo,
) error {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	ac.tasks = append(ac.tasks, task.TaskID)

	return nil
}

func (ac *annCollector) Withdraw(context.Context, string) error { return nil }

// taskIDs returns a copy of the announced task ids.
func (ac *annCollector) taskIDs() []string {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	return append([]string{}, ac.tasks...)
}

func (ac *annCollector) count() int {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	return len(ac.tasks)
}

// utProc builds start → user task(pinned) → end.
func utProc(t *testing.T, key string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	ut, err := activities.NewUserTask("approve",
		activities.WithCandidateUsers("operator"),
		activities.WithOutput("result", "string", true),
		activities.WithoutParams(),
		foundation.WithID(key+"-approve"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, ut, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, ut)
	link(t, ut, end)

	return p
}

// TestRestartRecoveryUserTask: a parked human task recovers by
// RE-ANNOUNCE — the new engine's distributor hears it under its
// RECORDED task id (the at-least-once posture; SRD-071 FR-8 made the id
// survive so a reference a human holds stays valid).
func TestRestartRecoveryUserTask(t *testing.T) {
	repo := memrepo.New()

	dist1 := &annCollector{}

	th1, err := thresher.New("engine-1",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithEngineGroup(recoveryGroup),
		thresher.WithLeaseTTL(80*time.Millisecond),
		thresher.WithTaskDistributor(dist1))
	require.NoError(t, err)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	p1 := utProc(t, "rr-ut")
	_, err = th1.RegisterProcess(p1)
	require.NoError(t, err)
	require.NoError(t, th1.Run(ctx1))

	h, err := th1.StartLatest(p1.ID())
	require.NoError(t, err)

	instID := h.ID()

	require.Eventually(t, func() bool { return dist1.count() == 1 },
		2*time.Second, 5*time.Millisecond,
		"engine-1 must announce the parked task")

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), instID)

		return ok && rec.Status == repository.StatusActive
	}, 2*time.Second, 5*time.Millisecond)

	time.Sleep(120 * time.Millisecond) // the lease lapses

	dist2 := &annCollector{}

	th2, err := thresher.New("engine-2",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithEngineGroup(recoveryGroup),
		thresher.WithTaskDistributor(dist2))
	require.NoError(t, err)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	p2 := utProc(t, "rr-ut")
	_, err = th2.RegisterProcess(p2)
	require.NoError(t, err)
	require.NoError(t, th2.Run(ctx2))

	require.Eventually(t, func() bool { return dist2.count() == 1 },
		2*time.Second, 5*time.Millisecond,
		"the recovered task must RE-ANNOUNCE on the new engine")
}

// TestRecoveryUnregisteredVersion covers SRD-070 T-5's loud half: a
// checkpoint whose pinned process version isn't registered on the
// recovering engine fails THAT instance loud — the engine still starts.
func TestRecoveryUnregisteredVersion(t *testing.T) {
	repo := memrepo.New()

	var hit atomic.Bool

	p1 := timerProc(t, "rr-missing", time.Now().Add(time.Hour), &hit)

	th1, _, cancel1 := bootEngine(t, "engine-1", repo,
		50*time.Millisecond, p1)
	defer cancel1()

	h, err := th1.StartLatest(p1.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), h.ID())

		return ok && rec.Status == repository.StatusActive
	}, 2*time.Second, 5*time.Millisecond)

	time.Sleep(80 * time.Millisecond) // the lease lapses

	// engine-2 registers NOTHING — deployment parity broken on purpose.
	th2, err := thresher.New("engine-2",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithEngineGroup(recoveryGroup))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th2.Observe(fw)
	t.Cleanup(sub.Cancel)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	require.NoError(t, th2.Run(ctx2),
		"a failed recovery must never block the engine start")

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseFailed)
	}, 2*time.Second, 5*time.Millisecond,
		"the unrecoverable instance must fail loud and visible")
}

// TestRecoveryCorruptRecord: a payload that doesn't decode fails that
// instance loud; the engine still starts.
func TestRecoveryCorruptRecord(t *testing.T) {
	repo := memrepo.New()

	// the record predates the engine: its solo group ("engine-x") must be
	// established before the seed lands (SRD-078 FR-1).
	require.NoError(t,
		repo.RegisterGroup(context.Background(), "engine-x"))
	require.NoError(t, repo.Save(context.Background(),
		repository.InstanceRecord{
			ID:      "broken-1",
			Status:  repository.StatusActive,
			Group:   "engine-x",
			Payload: []byte("not a checkpoint"),
		}))

	th, err := thresher.New("engine-x",
		thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo))
	require.NoError(t, err)

	fw := &factWatch{}
	sub := th.Observe(fw)
	t.Cleanup(sub.Cancel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseFailed)
	}, 2*time.Second, 5*time.Millisecond)
}

// flakyRepo wraps memrepo to fault the recovery listing paths.
type flakyRepo struct {
	*memrepo.Repo
	failList bool
	ghost    bool
}

func (fr *flakyRepo) ListInFlight(
	ctx context.Context, group string, now time.Time,
) ([]string, error) {
	if fr.failList {
		return nil, context.DeadlineExceeded
	}

	if fr.ghost {
		return []string{"ghost-1"}, nil
	}

	return fr.Repo.ListInFlight(ctx, group, now)
}

// TestRecoveryListingFaults: a failing listing degrades to a warning
// (the engine starts); a vanished record fails that instance loud.
func TestRecoveryListingFaults(t *testing.T) {
	t.Run("failing list never blocks the start",
		func(t *testing.T) {
			th, err := thresher.New("engine-x",
				thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
				thresher.WithRepository(
					&flakyRepo{Repo: memrepo.New(), failList: true}))
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			require.NoError(t, th.Run(ctx))
		})

	t.Run("a vanished record fails loud",
		func(t *testing.T) {
			th, err := thresher.New("engine-x",
				thresher.WithoutBanner(), thresher.WithoutStartupConfig(),
				thresher.WithRepository(
					&flakyRepo{Repo: memrepo.New(), ghost: true}))
			require.NoError(t, err)

			fw := &factWatch{}
			sub := th.Observe(fw)
			t.Cleanup(sub.Cancel)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			require.NoError(t, th.Run(ctx))

			require.Eventually(t, func() bool {
				return fw.saw(observability.KindInstanceState,
					observability.PhaseFailed)
			}, 2*time.Second, 5*time.Millisecond)
		})
}
