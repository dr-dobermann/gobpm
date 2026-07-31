# FIX-033 «A canceled instance can settle Completed, and the wake-retry tests race their own engine»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Draft (2026-07-31, branch `fix/termination-and-wake-races`, not yet implemented).
**Date:** 2026-07-31.
**Author:** Ruslan Gabitov.
**Branch:** `fix/termination-and-wake-races` (both symptoms are races surfaced by the same 2026-07-30 sweep; the name covers the terminal-state race and the wake-retry test race).
**Paired doc:** none (local to `internal/instance` and `pkg/thresher` tests).
**Upstream:** [ADR-001 v.6](../design/ADR-001-execution-model.md) §7 (the termination cascade this restores), [ADR-007 v.2.1](../design/ADR-007-in-memory-long-waits.md) / [ADR-033 v.2](../design/ADR-033-persistence-and-state.md) (the timer service the §1.2 tests drive).

**Grounded in (internal artifacts):**
- `docs/backlog.md` — both symptoms were recorded there as observations awaiting diagnosis (added by `218f3e7`, 2026-07-30). This FIX graduates them out.
- The reproduction runs recorded in §1 below, on `master` at `391b7d5`.

## §1 Symptoms

### §1.1 Symptom A: a pre-canceled instance can settle `Completed` instead of `Terminated`

An instance whose context is already canceled when `Run` starts — and, by the
same mechanism, one canceled while it runs — can finish in `Completed`. The
cancellation is simply not recorded, so the terminal state claims the process
finished normally when it was in fact torn down.

The engine's own regression test for the cancellation cascade is the one that
fails:

```
--- FAIL: TestTerminatedOnPreCanceledContext
    adr1_gate_test.go:51: a pre-canceled instance settles in Terminated via the cascade
```

In code: `internal/instance/loop.go:193-237` (the loop's `select`) combined with
`internal/instance/lifecycle.go:153` (`settleFinalState`, which reads the flag
the `select` may never set).

**Reproduction status — honest and incomplete.** The backlog records this
failing in a full `make test` run and reproducing at
`GOMAXPROCS=8 … -count=2000`. On this machine it did **not** reproduce in 7,200
attempts:

| Configuration | Result |
|---|---|
| `GOMAXPROCS=8 -count=2000` (the recorded recipe) | 2000/2000 pass |
| `GOMAXPROCS=8 -count=5000 -race`, 6 busy-loop processes as load | 5000/5000 pass |
| single-track instance (fewest `select` turns — the shape most likely to fail), `-count=200` | 200/200 pass |

The defect is therefore established from the **code**, not from a hit rate, and
§2.1 proves it by construction. The rate matters only for judging how long it
could hide, and the answer is: indefinitely.

### §1.2 Symptom B: `TestFailedWakeRetriesAndSucceeds` races the engine it starts

`pkg/thresher/wake_retry_test.go:147`. Under `-race` with CPU contention:

```
WARNING: DATA RACE
Write at 0x00c0001c38e0 by goroutine 1784:
  thresher.TestFailedWakeRetriesAndSucceeds.func1()  wake_retry_test.go:161   ← attempts++
  thresher.(*timerService).fireDue()                 timer_service.go:263
  thresher.(*timerService).run()                     timer_service.go:208
  thresher.(*Thresher).Run.gowrap1()                 thresher.go:570
Previous read at 0x00c0001c38e0 by goroutine 1782:
  thresher.TestFailedWakeRetriesAndSucceeds()        wake_retry_test.go:183   ← require.Equal(t, 2, attempts)
```

Reproduced at **2 failures / 1500 runs** (`-race`, 6 busy-loop processes as
load), both reported as `race detected during execution of test`.

The backlog's provisional diagnosis — "a load-sensitive timing assumption" —
is **wrong**, and is corrected here: there is no timing assumption in the test
body at all. It drives a fake clock and calls `fireDue` explicitly. The fault is
a second, concurrent caller.

## §2 Root Cause Analysis

### §2.1 Symptom A: `select` gives a canceled context no precedence

`internal/instance/loop.go:192-237`:

```go
done := ctx.Done()
for ls.active > 0 {
	select {
	case <-done:
		done = nil
		ls.stopAll()          // the ONLY writer of ls.stopping

	case ev := <-inst.events:
		ls.apply(ctx, ev)     // evEnded here can drop ls.active to 0
	...
	}
}
```

and the settlement that follows it, `internal/instance/lifecycle.go:153`:

```go
func (inst *Instance) settleFinalState(stopping bool) {
	if stopping {
		inst.setState(Terminated)

		return
	}

	inst.setState(Completed)
}
```

Three facts compose into the defect:

1. **`ls.stopping` is set only by `stopAll`**, reached only through the `<-done`
   arm (`loop.go:329-335`).
2. **A `select` with several ready cases chooses uniformly at random.** This is
   the Go language specification, not a scheduling accident. A canceled `done`
   is *permanently* ready, so on every turn where `inst.events` is also ready
   the cancellation may lose — and may keep losing.
3. **The loop's exit condition is `ls.active > 0`**, and `apply` of a terminal
   `evEnded` is what drives `active` to zero. So the loop can exit through the
   events arm alone, with `done` never selected and `stopping` still false.

The result is `settleFinalState(false)` → `Completed`, for an instance whose
context was canceled the whole time. Failure requires the events arm to win on
every turn until `active` reaches zero, which is why the probability falls as
the track count rises — and why a rare event is not a harmless one: it is
mis-reported process state, and downstream a checkpoint records the wrong
terminal status.

**This is an ordering defect, not a data race.** `-race` cannot find it, which
is precisely why it survived: the sweep that noticed it was looking for the
other kind.

### §2.2 Symptom B: the test is a second firer alongside a live service

`retryEngine` (`wake_retry_test.go:50-68`) calls `th.Run(ctx)`. With a
Repository configured — these tests configure one, because
`unregisteredRecord` needs it — `Run` both builds the timer service and starts
its loop (`pkg/thresher/thresher.go:567-570`):

```go
if t.cfg.repoSet {
	t.timerSvc = newTimerService(
		t.cfg.Clock(), t.cfg.wakeBackoff, t.hydrateFromTimer)
	go t.timerSvc.run(runCtx)
}
```

That loop parks on the clock (`timer_service.go:195-210`):

```go
if next, ok := ts.nearest(); ok {
	fire = ts.clk.After(next.Sub(ts.clk.Now()))
}

select {
case <-ctx.Done():	return
case <-ts.kick:		continue
case <-fire:		ts.fireDue(ctx)
}
```

So when the test advances the fake clock, **the service's own goroutine fires
the due hold** — at the same time the test calls `th.timerSvc.fireDue(...)` by
hand. Both paths reach `ts.wake(...)` (`timer_service.go:263`), which is the
test's closure, and that closure mutates `attempts` and reads `failing` with no
synchronization. Two consequences, both real:

- **A data race** on the closure's captured variables (the detector's report,
  §1.2).
- **A correctness flake independent of the detector**: an extra background fire
  increments `attempts`, so `require.Equal(t, 1, attempts)` and
  `require.Equal(t, 2, attempts)` can both see one too many.

The assignment `th.timerSvc.wake = …` (lines 116, 160, 198, 244) is itself
unsynchronized against a goroutine that reads `ts.wake` — so the window opens
before the first `Advance`.

**Scope: every test built on `retryEngine` shares the shape.** Four override the
closure directly — `TestFailedWakeKeepsTheHold` (116),
`TestFailedWakeRetriesAndSucceeds` (160), `TestSuccessfulWakeStillWithdraws`
(198) and `TestFiringHoldIsNotReentered` (244) — and `TestFailedWakeBacksOff`
(105) drives the same service. Only `TestFailedWakeRetriesAndSucceeds` has been
*observed* failing; the others are exposed by construction and must be fixed
together, or the next one simply becomes the flake.

### §2.3 Why the tests did not catch either

- **A**: `TestTerminatedOnPreCanceledContext` asserts the right thing but only
  probabilistically, and its comment asserts a determinism the code does not
  provide: *"the loop's first select sees ctx.Done() before any track has
  emitted"*. Nothing orders those two. The comment is the reason the test's
  passing was read as proof rather than as a coin landing the same way.
- **B**: the race needs `-race` **and** contention. `make ci` runs `-race`, so
  CI would eventually have found it — as a mystery failure in an unrelated PR.

## §3 Solution

### §3.1 Alternatives considered

**Symptom A**

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A1. Poll `done` non-blockingly at the top of each loop turn, before the blocking `select` | Deterministic for an already-visible cancellation; 6 lines; no new state; zero cost once canceled (`done` is nil'd) | One extra non-blocking `select` per turn while the context is live | ✅ **chosen** |
| A2. Re-check `ctx.Err()` after the loop and override the terminal state | Smallest diff | Wrong layer: it papers over the flag rather than recording the cancellation, and `stopAll`'s teardown (releasing holds, stopping tracks) would still not have run | ❌ rejected |
| A3. Give the events arm a `default:` and drain events only when `done` is not ready | Strict precedence | Turns the loop into a spin when both are idle; changes the blocking discipline of the whole loop for one edge | ❌ rejected |

**Symptom B**

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| B1. Make the test the **only** firer: build the timer service directly in the harness and never start its goroutine | Removes the second caller outright — no race, no double count, fully deterministic; `HoldTimer` needs only a non-nil `timerSvc` (`wake.go:31`) | The harness wires one line that `Run` would otherwise wire | ✅ **chosen** |
| B2. Guard the closure's counters with a mutex | Silences the detector | Leaves the double fire in place: `attempts` would still be legitimately 2 when the test demands 1, so the assertion flake survives — it hides the symptom the detector was reporting | ❌ rejected |
| B3. Keep the engine running and relax the assertions to `>= 1` | Tiny diff | Destroys what the test is for: "retried exactly once" is the FIX-027 contract being pinned | ❌ rejected |
| B4. Cancel the engine context right after `Run` | No harness rewiring | Racy in its own right — the exit is not synchronized, so the goroutine may still take one turn; trades a known race for a subtler one | ❌ rejected |

### §3.2 Changes by file

#### §3.2.1 `internal/instance/loop.go` — cancellation is observed before event accounting

```go
// before:
done := ctx.Done()
for ls.active > 0 {
	select {
	case <-done:
		done = nil
		ls.stopAll()

// after:
done := ctx.Done()
for ls.active > 0 {
	// A ready ctx.Done() must be recorded BEFORE any further terminal-event
	// accounting: `select` chooses uniformly among ready cases (Go spec), so a
	// canceled context can lose every turn while evEnded drives ls.active to
	// zero — and the loop would then exit with ls.stopping false, settling
	// Completed for an instance that was torn down (ADR-001 §7). Polling here
	// costs one non-blocking select per turn while the context is live, and
	// nothing at all once it is canceled: done is nil from then on.
	if done != nil {
		select {
		case <-done:
			done = nil
			ls.stopAll()
		default:
		}
	}

	select {
	case <-done:
		done = nil
		ls.stopAll()
```

The blocking arm stays: it is what wakes an idle loop when cancellation arrives
*during* the wait. The poll adds precedence for a cancellation that is already
visible. `stopAll` is idempotent (`loop.go:330`), so the two paths cannot
double-run.

What this deliberately does **not** change: a cancellation arriving after the
last event has been applied still settles `Completed`. That is correct — the
instance genuinely finished first. The defect was ignoring a cancellation the
loop could already see.

#### §3.2.2 `internal/instance/adr1_gate_test.go` — the regression test states the truth

The comment claiming the first `select` deterministically sees `ctx.Done()` is
removed; it describes the bug, not the design. The test is strengthened to wait
on the instance's own terminal signal rather than polling, and to assert the
exact state:

```go
// before:
require.Eventually(t,
	func() bool { return inst.State() == Terminated },
	time.Second, 5*time.Millisecond,
	"a pre-canceled instance settles in Terminated via the cascade")

// after:
<-inst.Done()

require.Equal(t, Terminated, inst.State(),
	"a pre-canceled instance settles Terminated — cancellation is recorded "+
		"before terminal-event accounting, whatever order the loop sees them in")
```

Polling with `Eventually` can only observe *a* terminal state; awaiting
`Done()` and comparing exactly is what distinguishes `Terminated` from
`Completed` — the two the defect confused.

#### §3.2.3 `pkg/thresher/wake_retry_test.go` — one firer, the test

`retryEngine` stops starting the engine and wires the service the tests drive:

```go
// before:
ctx, cancel := context.WithCancel(context.Background())
require.NoError(t, th.Run(ctx))

return th, clk, cancel

// after:
// The tests drive fireDue by hand, so the service must have no other caller:
// Thresher.Run would start timerService.run, whose own clock loop fires the
// same due holds on the same fake-clock Advance — racing the test's closure
// and double-counting its attempts (FIX-033 §2.2). Wiring the service here is
// the one line Run would have contributed (thresher.go:568).
th.timerSvc = newTimerService(clk, backoff, th.hydrateFromTimer)

return th, clk, func() {}
```

Nothing else in these tests needs a running engine: they use
`th.cfg.Repository()` (available without `Run`), `th.HoldTimer` (needs only a
non-nil `timerSvc`, `wake.go:31`), and the service's own `fireDue` / `nearest`.
Restart recovery is not skipped in any meaningful sense either — every test
seeds its record *after* the harness returns, so a `Run` would have found an
empty store.

#### §3.2.4 `docs/backlog.md` — both entries graduate out

The two observation entries are removed: they are now this FIX's §1.1 and §1.2,
with a diagnosis each. Leaving them would leave the backlog claiming an open
question that has an answer.

## §4 Verification

Current coverage of the two areas:

- `internal/instance`: `TestTerminatedOnPreCanceledContext` exists and is the
  canary — it just cannot fail reliably. No test covers "cancellation arrives
  while events are pending".
- `pkg/thresher`: four wake-retry tests exist; none is deterministic under a
  live service.

### §4.1 Regression tests (mandatory)

#### §4.1.1 `TestTerminatedOnPreCanceledContext` (strengthened)

**Updated:** `internal/instance/adr1_gate_test.go`.

| Test | Setup | Assertion |
|---|---|---|
| `TestTerminatedOnPreCanceledContext` | fork snapshot; context canceled before `Run` | awaits `inst.Done()`; state is exactly `Terminated` |

#### §4.1.2 `TestTerminatedWhenCancelRacesPendingEvents`

**New:** `internal/instance/adr1_gate_test.go`. The case §2.1 describes and no
test covered — cancellation competing with events that are already queued.

| Test | Setup | Assertion |
|---|---|---|
| `TestTerminatedWhenCancelRacesPendingEvents` | fork snapshot; cancel before `Run`; run the body `-count`-style in a loop within the test so the uniform choice is exercised many times | every iteration settles `Terminated`; never `Completed` |

#### §4.1.3 The wake-retry suite becomes deterministic

**Updated:** `pkg/thresher/wake_retry_test.go` (harness only; the four test
bodies keep their assertions, which is the point — they were right).

| Test | Setup | Assertion |
|---|---|---|
| every `retryEngine` test | harness wires the service without starting its goroutine | exact `attempts` counts hold; `-race` clean under contention |

### §4.2 Empirical gate

- `rtk proxy go test ./internal/instance -run 'PreCanceled|CancelRaces' -count=5000 -race` under CPU load → 0 failures.
- `rtk proxy go test ./pkg/thresher -run 'Wake' -count=1500 -race` under CPU load → 0 failures, no `DATA RACE`.
  Both are run with raw output and the `=== RUN` / `--- FAIL` lines counted:
  the repo's `go test` is rtk-proxied and prints only a summary, which cannot
  distinguish a real pass from a run that executed no tests.
- `make ci` green.

### §4.3 Observability

No new facts. A correctly-terminated instance already reports
`KindInstanceState`/`Terminated`; the defect made that fact *wrong*, and the fix
makes it right.

## §5 Prevention

- **Doc comments**: the loop's poll carries the reason (the Go-spec uniform
  choice), so the next reader does not "simplify" it away. The wake harness
  carries the reason it does not start the engine.
- **Regression canaries**: §4.1.2 is the canary for A; the whole wake file is
  the canary for B — if either regresses, `-race` under load fails.
- **A note for the next race hunt**: `-race` finds data races, not ordering
  defects. Symptom A is invisible to it. Where a terminal state is chosen from
  a flag set by one arm of a `select`, review the precedence by reading, not by
  running.

## §6 Regressions / side-effects

### §6.1 What may rely on the old behavior

- `grep -rn "settleFinalState" internal/` — one caller (`loop.go:263`);
  behavior changes only for a canceled instance, which is the fix.
- `grep -rn "timerSvc" pkg/thresher/*_test.go` — the wake-retry file is the
  only test touching the service directly; other suites go through the engine
  and are unaffected.
- Instances that complete normally are untouched: with a live, uncanceled
  context the poll's `default:` fires and the loop proceeds exactly as before.

### §6.2 Rollback path

A single revert. Neither change carries data or migration.

### §6.3 Backlog spun off

None expected. Should the §4.1.2 loop prove slow in CI, it can drop its
iteration count — the deterministic assertion, not the repetition, is what
holds the line.

## §7 Related

- [ADR-001 v.6](../design/ADR-001-execution-model.md) §7 — the termination
  cascade whose terminal state this restores.
- [FIX-027](FIX-027-stranded-dehydrated-instances.md) — the wake-retry behavior
  the §1.2 tests pin; this FIX repairs the tests, not that fix.
- `docs/backlog.md` — where both symptoms were parked pending diagnosis.

## §8 Implementation summary

> ⚠️ TODO: fill AFTER landing.

### §8.1 Stages by commit

| Stage | Commit | Scope | Tests |
|---|---|---|---|

### §8.2 Empirical findings — where reality diverged from the §3 draft

### §8.3 Backlog (out of FIX-033 scope)

## §9 Open questions

None.
