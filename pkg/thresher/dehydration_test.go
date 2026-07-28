package thresher_test

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
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

// dehydrationEpoch is the fake clock's start — a fixed instant so a deadline
// is exactly "epoch + d" with no wall-clock coupling.
var dehydrationEpoch = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// longTimerProc builds start → timer(deadline) → lane → end with PINNED node
// ids (the deployment-parity contract a wake shares with restart recovery).
func longTimerProc(
	t *testing.T, key string, deadline time.Time, hit *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	def, err := events.NewTimerEventDefinition(timeExpr(t, deadline), nil, nil)
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

// bootDehydrationEngine builds an armed engine on a fresh CONTROLLED clock over
// repo, registers p and runs it.
func bootDehydrationEngine(
	t *testing.T, name string, repo repository.Repository, p *process.Process,
) (*thresher.Thresher, *factWatch, *clocktest.Clock, context.CancelFunc) {
	t.Helper()

	return bootDehydrationEngineWithClock(t, name, repo,
		clocktest.New(dehydrationEpoch), p)
}

// bootDehydrationEngineWithClock is bootDehydrationEngine over a caller-owned
// clock (a second engine in the crash-recovery trace drives its own).
func bootDehydrationEngineWithClock(
	t *testing.T, name string, repo repository.Repository,
	clk *clocktest.Clock, p *process.Process,
) (*thresher.Thresher, *factWatch, *clocktest.Clock, context.CancelFunc) {
	t.Helper()

	th, fw, cancel := armedEngine(t, name, repo, clk, time.Minute, p)

	return th, fw, clk, cancel
}

// bootShortLeaseEngine boots an engine whose lease lapses quickly — the
// abandoned ("crashed") engine of the recovery trace. Its context is
// deliberately NOT cancelled by the caller: abandonment, not shutdown.
func bootShortLeaseEngine(
	t *testing.T, name string, repo repository.Repository,
	clk *clocktest.Clock, p *process.Process,
) (*thresher.Thresher, *factWatch, error) {
	t.Helper()

	th, fw, cancel := armedEngine(t, name, repo, clk,
		80*time.Millisecond, p)
	t.Cleanup(cancel)

	return th, fw, nil
}

// armedEngine builds, registers and runs one checkpoint-armed engine.
func armedEngine(
	t *testing.T, name string, repo repository.Repository,
	clk *clocktest.Clock, ttl time.Duration, p *process.Process,
) (*thresher.Thresher, *factWatch, context.CancelFunc) {
	t.Helper()

	th, err := thresher.New(name,
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRepository(repo),
		thresher.WithClock(clk),
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

// TestDehydrationTimerWake covers SRD-071 T-4 (FR-2/FR-3/FR-4/FR-6, closes
// #84): an instance parked on a LONG timer dehydrates — every goroutine
// released, the loop gone, its checkpoint the wake source — and the engine
// timer service, holding the deadline on its behalf, wakes it when the clock
// reaches it: the continuation fork fires the timer node through and the flow
// completes.
func TestDehydrationTimerWake(t *testing.T) {
	repo := memrepo.New()
	deadline := dehydrationEpoch.Add(2 * time.Hour) // > the 1h threshold

	var hit atomic.Bool

	p := longTimerProc(t, "dehy-timer", deadline, &hit)

	th, fw, clk, cancel := bootDehydrationEngine(t, "engine-D", repo, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	instID := h.ID()

	// the instance releases its goroutines: the Dehydrated fact and a
	// checkpoint carrying the released track.
	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 2*time.Second, 5*time.Millisecond,
		"a fully-idle long-timer instance must dehydrate")

	rec, ok, err := repo.Load(context.Background(), instID)
	require.NoError(t, err)
	require.True(t, ok, "the dehydration checkpoint is the wake source")
	require.Equal(t, repository.StatusActive, rec.Status,
		"a dehydrated instance persists as an in-flight record")

	require.False(t, hit.Load(), "the flow must not pass the timer yet")

	// nothing fires before the deadline.
	clk.Advance(30 * time.Minute)
	require.Never(t, hit.Load, 200*time.Millisecond, 20*time.Millisecond,
		"the held timer must not fire early")

	// the deadline arrives: the service wakes the instance, the continuation
	// fork fires the timer node through and the flow completes.
	clk.Advance(2 * time.Hour)

	require.Eventually(t, hit.Load, 3*time.Second, 5*time.Millisecond,
		"the held timer must wake the dehydrated instance and continue")

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseHydrated)
	}, 2*time.Second, 5*time.Millisecond,
		"the wake must be observable (FR-10)")

	require.Eventually(t, func() bool {
		r, found, _ := repo.Load(context.Background(), instID)

		return found && r.Status == repository.StatusCompleted
	}, 3*time.Second, 10*time.Millisecond,
		"the woken instance must run to completion")
}

// TestDehydrationReleasesGoroutines covers T-4's zero-goroutine half: while
// dehydrated the instance holds NO goroutines — no loop, no per-track waiter,
// and (the M3 arm-time holder) no in-hub timer goroutine either.
func TestDehydrationReleasesGoroutines(t *testing.T) {
	repo := memrepo.New()
	deadline := dehydrationEpoch.Add(4 * time.Hour)

	const instances = 8

	var hit atomic.Bool

	// Many instances of ONE registered process: they share its timer
	// expression, so this also exercises concurrent evaluation of a shared
	// GExpression.
	p := longTimerProc(t, "dehy-goroutines", deadline, &hit)

	th, fw, _, cancel := bootDehydrationEngine(t, "engine-G", repo, p)
	defer cancel()

	// settle first: other tests' goroutines may still be draining, and the
	// baseline must not absorb them.
	settleGoroutines(t)

	before := runtime.NumGoroutine()

	for range instances {
		_, err := th.StartLatest(p.ID())
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		return countFacts(fw, observability.KindInstanceState,
			observability.PhaseDehydrated) == instances
	}, 5*time.Second, 10*time.Millisecond,
		"every idle long-timer instance must dehydrate")

	// A RESIDENT instance costs at least two goroutines (its loop + its parked
	// track) plus an in-hub timer waiter — so 8 resident instances would sit
	// >= 16 over the baseline. Dehydrated they hold NONE; the allowance below
	// is slack for the shared engine/hub, not per-instance cost.
	const residentCost = 2 * instances

	var over int

	require.Eventually(t, func() bool {
		runtime.Gosched()

		over = runtime.NumGoroutine() - before

		return over < residentCost/2
	}, 5*time.Second, 20*time.Millisecond,
		"dehydrated instances must release their goroutines: %d instances "+
			"left %d goroutines over the %d baseline (resident would be "+
			">= %d)", instances, over, before, residentCost)
}

// settleGoroutines waits for the process-wide goroutine count to stop moving,
// so a count-based assertion is not polluted by an earlier test's teardown.
func settleGoroutines(t *testing.T) {
	t.Helper()

	last := -1
	stable := 0

	require.Eventually(t, func() bool {
		runtime.Gosched()

		n := runtime.NumGoroutine()
		if n == last {
			stable++
		} else {
			last, stable = n, 0
		}

		return stable >= 3
	}, 5*time.Second, 20*time.Millisecond,
		"the goroutine count never settled")
}

// TestDehydrationShortTimerStaysResident covers the ADR-007 §2.4 threshold: a
// SHORT timer is not worth a checkpoint + hydrate round-trip, so it keeps its
// in-hub waiter and the instance stays resident — and still fires normally.
func TestDehydrationShortTimerStaysResident(t *testing.T) {
	repo := memrepo.New()
	deadline := dehydrationEpoch.Add(5 * time.Minute) // < the 1h threshold

	var hit atomic.Bool

	p := longTimerProc(t, "dehy-short", deadline, &hit)

	th, fw, clk, cancel := bootDehydrationEngine(t, "engine-S", repo, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	// it parks (the checkpoint proves it reached the wait) but never dehydrates.
	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), h.ID())

		return ok && rec.Status == repository.StatusActive
	}, 2*time.Second, 10*time.Millisecond,
		"a short timer must reach its park checkpoint")

	require.Never(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 300*time.Millisecond, 25*time.Millisecond,
		"a sub-threshold timer keeps the instance resident")

	// the resident in-hub waiter still fires it.
	clk.Advance(10 * time.Minute)
	require.Eventually(t, hit.Load, 3*time.Second, 5*time.Millisecond,
		"a resident short timer must still fire")
}

// countFacts counts the facts matching kind/phase.
func countFacts(
	fw *factWatch, k observability.Kind, p observability.Phase,
) int {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	n := 0

	for _, f := range fw.facts {
		if f.Kind == k && f.Phase == p {
			n++
		}
	}

	return n
}

// twoTimerProc builds start → timer1 → lane → timer2 → end: an instance that
// dehydrates, wakes, and dehydrates AGAIN (the oscillation of SRD-071 FR-5).
func twoTimerProc(
	t *testing.T, key string, first, second time.Time, hit *atomic.Bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	def1, err := events.NewTimerEventDefinition(timeExpr(t, first), nil, nil)
	require.NoError(t, err)

	wait1, err := events.NewIntermediateCatchEvent("wait1", def1,
		foundation.WithID(key+"-wait1"))
	require.NoError(t, err)

	lane := pinnedLane(t, key+"-lane", hit)

	def2, err := events.NewTimerEventDefinition(timeExpr(t, second), nil, nil)
	require.NoError(t, err)

	wait2, err := events.NewIntermediateCatchEvent("wait2", def2,
		foundation.WithID(key+"-wait2"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID(key+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, wait1, lane, wait2, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, wait1)
	link(t, wait1, lane)
	link(t, lane, wait2)
	link(t, wait2, end)

	return p
}

// TestDehydrationOscillation covers SRD-071 T-7 (FR-5, §4.1): an instance with
// two sequential long timers dehydrates, wakes, continues, and RE-dehydrates —
// each cycle costing one checkpoint + one hydrate, never accumulating lineage:
// the continuation fork inherits the dehydrated track's prev WITHOUT appending
// it, so the recorded lineage stays bounded across cycles.
func TestDehydrationOscillation(t *testing.T) {
	repo := memrepo.New()
	first := dehydrationEpoch.Add(2 * time.Hour)
	second := dehydrationEpoch.Add(6 * time.Hour)

	var hit atomic.Bool

	p := twoTimerProc(t, "dehy-osc", first, second, &hit)

	th, fw, clk, cancel := bootDehydrationEngine(t, "engine-O", repo, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	instID := h.ID()

	// cycle 1: dehydrate on the first timer.
	require.Eventually(t, func() bool {
		return countFacts(fw, observability.KindInstanceState,
			observability.PhaseDehydrated) == 1
	}, 2*time.Second, 5*time.Millisecond, "the first wait must dehydrate")

	lineage1 := recordedLineage(t, repo, instID)

	// wake it: the lane runs, then it parks on the SECOND timer and
	// re-dehydrates (FR-5).
	clk.Advance(3 * time.Hour)

	require.Eventually(t, hit.Load, 3*time.Second, 5*time.Millisecond,
		"the woken flow must run the lane")

	require.Eventually(t, func() bool {
		return countFacts(fw, observability.KindInstanceState,
			observability.PhaseDehydrated) == 2
	}, 3*time.Second, 5*time.Millisecond,
		"a fully-idle woken instance must re-dehydrate")

	lineage2 := recordedLineage(t, repo, instID)
	require.LessOrEqual(t, lineage2, lineage1+1,
		"a dehydrate/wake cycle must not grow the persisted lineage (§4.1)")

	// cycle 2: the second timer wakes it to completion.
	clk.Advance(4 * time.Hour)

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), instID)

		return ok && rec.Status == repository.StatusCompleted
	}, 3*time.Second, 10*time.Millisecond,
		"the second wake must run the instance to completion")
}

// recordedLineage returns the longest track `prev` chain in the instance's
// current checkpoint — the quantity §4.1 forbids growing per cycle.
func recordedLineage(
	t *testing.T, repo repository.Repository, instID string,
) int {
	t.Helper()

	rec, ok, err := repo.Load(context.Background(), instID)
	require.NoError(t, err)
	require.True(t, ok)

	doc, err := checkpoint.Unmarshal(rec.Payload)
	require.NoError(t, err)

	longest := 0
	for _, tr := range doc.Tracks {
		if len(tr.Prev) > longest {
			longest = len(tr.Prev)
		}
	}

	return longest
}

// TestDehydratedCrashRecovery covers SRD-071 T-9 (§4.2): a dehydrated instance
// has no loop to renew its lease, so an abandoned engine's record lapses and
// restart recovery on a second engine reclaims it — the SRD-070 path, unchanged
// by dehydration. The recovered instance re-arms its timer (trigger-absent) and
// completes.
func TestDehydratedCrashRecovery(t *testing.T) {
	repo := memrepo.New()
	deadline := dehydrationEpoch.Add(2 * time.Hour)

	var hit1, hit2 atomic.Bool

	p1 := longTimerProc(t, "dehy-crash", deadline, &hit1)

	clk1 := clocktest.New(dehydrationEpoch)

	th1, fw1, err := bootShortLeaseEngine(t, "engine-1", repo, clk1, p1)
	require.NoError(t, err)

	_, err = th1.StartLatest(p1.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return fw1.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 2*time.Second, 5*time.Millisecond,
		"engine-1's instance must dehydrate")

	// engine-1 is ABANDONED (a crash): no terminal write, and a DEHYDRATED
	// instance has no loop to renew its lease (§4.2) — it simply lapses. The
	// engines run on controlled clocks, so engine-2 starts PAST engine-1's
	// lease expiry: that lapse is what makes the record claimable.
	p2 := longTimerProc(t, "dehy-crash", deadline, &hit2)

	clk2 := clocktest.New(dehydrationEpoch.Add(time.Minute))

	_, fw2, clk, cancel2 := bootDehydrationEngineWithClock(t, "engine-2",
		repo, clk2, p2)
	defer cancel2()

	require.Eventually(t, func() bool {
		return fw2.saw(observability.KindInstanceState,
			observability.PhaseRecovered)
	}, 3*time.Second, 10*time.Millisecond,
		"engine-2 must reclaim the abandoned DEHYDRATED instance")

	// Let the reclaimed instance SETTLE before moving time: it re-arms its
	// timer at the recorded deadline and, being idle again, re-dehydrates.
	// Advancing mid-recovery would race the track's own registration — not
	// what this trace is about (§4.2 is the reclaim, not the arming window).
	require.Eventually(t, func() bool {
		return countFacts(fw2, observability.KindInstanceState,
			observability.PhaseDehydrated) == 1
	}, 3*time.Second, 10*time.Millisecond,
		"the reclaimed instance must settle back into dehydration")

	// the recovered instance re-armed its timer at the RECORDED deadline.
	clk.Advance(3 * time.Hour)

	require.Eventually(t, hit2.Load, 3*time.Second, 5*time.Millisecond,
		"the reclaimed instance's timer must fire on engine-2")
}

// TestDehydrationSingleFlightWake covers SRD-071 T-8 (§4.6, run under -race):
// several triggers racing for ONE dehydrated instance must hydrate it exactly
// once — the wake latch serializes them, the losers deliver into the
// now-resident loop. The instance must complete once, never double-run its lane.
func TestDehydrationSingleFlightWake(t *testing.T) {
	repo := memrepo.New()
	deadline := dehydrationEpoch.Add(2 * time.Hour)

	var runs atomic.Int32

	p := countingTimerProc(t, "dehy-single", deadline, &runs)

	th, fw, clk, cancel := bootDehydrationEngine(t, "engine-1F", repo, p)
	defer cancel()

	h, err := th.StartLatest(p.ID())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return fw.saw(observability.KindInstanceState,
			observability.PhaseDehydrated)
	}, 2*time.Second, 5*time.Millisecond, "the instance must dehydrate")

	// concurrent advances past the deadline: the service fires, and racing
	// goroutines hammer the same wake path.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			clk.Advance(3 * time.Hour)
		}()
	}

	wg.Wait()

	require.Eventually(t, func() bool {
		rec, ok, _ := repo.Load(context.Background(), h.ID())

		return ok && rec.Status == repository.StatusCompleted
	}, 3*time.Second, 10*time.Millisecond,
		"the racing wakes must complete the instance")

	require.Never(t, func() bool { return runs.Load() > 1 },
		300*time.Millisecond, 25*time.Millisecond,
		"racing triggers must hydrate the instance ONCE (§4.6)")
	require.Equal(t, int32(1), runs.Load(),
		"the woken flow runs its lane exactly once")
}

// countingTimerProc is longTimerProc whose lane COUNTS its runs.
func countingTimerProc(
	t *testing.T, key string, deadline time.Time, runs *atomic.Int32,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New(key, foundation.WithID(key))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		foundation.WithID(key+"-start"))
	require.NoError(t, err)

	def, err := events.NewTimerEventDefinition(timeExpr(t, deadline), nil, nil)
	require.NoError(t, err)

	wait, err := events.NewIntermediateCatchEvent("wait", def,
		foundation.WithID(key+"-wait"))
	require.NoError(t, err)

	op, err := gooper.New(key+"-lane",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			runs.Add(1)

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

	link(t, start, wait)
	link(t, wait, lane)
	link(t, lane, end)

	return p
}
