# FIX-003 «Audit bug sweep: event-subsystem defects + track-state minors»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Landed v.1 (2026-06-13, branch `fix/audit-bug-sweep`, M1–M4 implemented, `make ci` green).
**Date:** 2026-06-13.
**Author:** Ruslan Gabitov.
**Branch:** `fix/audit-bug-sweep` (one sweep over the audit-confirmed point defects; each is local and independently testable).
**Paired doc:** none (point fixes; the conceptual decisions stay with [ADR-006](../design/ADR-006-events-and-subscriptions.md) — see §7).
**Upstream:** [ADR-001 v.5 Execution Model](../design/ADR-001-execution-model.md) (track state machine §4.2, token projection §6); architecture audit 2026-06-11 (`docs/audit/architecture-audit-2026-06-11.md`), items 1.3 / 1.4 / 1.5 / non-data 1.6.

**Grounded in (internal artifacts):**
- Audit triage (2026-06-12, owner-confirmed): 1.3/1.4/1.5 + non-data 1.6 → this FIX; data items of 1.6 + 3.1–3.3 → ADR-011; event delivery semantics & waiter lifecycle *ownership* (audit 2.4/2.5) → ADR-006.
- Every claim below re-verified against `master` at `7da7c8a` (post-ADR-010 merge) — line numbers are current.

## §1 Symptoms

### §1.1 Symptom A: timer waiter can panic on `close` of a closed channel; debug `Println` in production code

A concurrent context cancellation and `Stop()` call both close the same
channel — Go panics on the second close, killing the process. Additionally,
the waiter prints raw debug output to stdout, bypassing the engine's logger.

```
panic: close of closed channel        (the race outcome; not yet caught in
                                       CI because no test exercises
                                       ctx-cancel concurrently with Stop)
stopping waiter <id> ...              (stdout noise on every waiter stop)
```

In code: `internal/eventproc/eventhub/waiters/timer.go:260`
(`runTimerService`, `ctx.Done()` branch: `close(tw.stopCh)`) and
`timer.go:350` (`Stop()`: `close(tw.stopCh)`) — two independent close sites
on one channel, the first outside any mutex; `timer.go:271` —
`fmt.Println("stopping waiter ", tw.id, "...")`.

### §1.2 Symptom B: the unregistration chain silently does nothing — goroutine and subscription leaks

`Instance.UnregisterEvent` and `timeWaiter.RemoveEventProcessor` return
`nil` without doing anything, so a caller builds logic on false success: the
hub's `UnregisterEvent` checks "is the waiter now empty?" after a removal
that never happened — waiters never reach the empty→stop path, their
goroutines and subscriptions leak for unfired events (e.g. a terminated
track that was waiting on a timer).

In code:
- `internal/instance/instance.go:584-589` — `UnregisterEvent(_, _) error { return nil }`.
- `internal/eventproc/eventhub/waiters/timer.go:199-201` — `RemoveEventProcessor(_) error { return nil }`.
- `internal/eventproc/eventhub/eventhub.go:197` — the consumer of the lie:
  `if len(w.EventProcessors()) == 0 { … w.Stop() … RemoveWaiter … }` — dead
  code while removal is a no-op.
- The live caller that today depends on the silent `nil`:
  `internal/instance/track.go:618` (`t.instance.UnregisterEvent(t,
  eDef.ID())` inside `unregisterEvent`, called from `track.go:698` after a
  caught event) — an error here **fails the track**, so the fix must keep
  this call succeeding in the fired-event flow.

### §1.3 Symptom C: TOCTOU in `EventHub.RegisterEvent` — concurrent registrations leak a waiter

Two goroutines registering the same `eDef.ID()` can both miss the
existence check (taken under `RLock`, then released) and both create a
waiter; the second insert overwrites the first **with its serving goroutine
still running** — an orphaned goroutine and a lost processor registration.

In code: `internal/eventproc/eventhub/eventhub.go:117-146` — `RLock` →
lookup → `RUnlock` → `CreateWaiter` → `Lock` → blind insert. Additionally,
if `w.Service(ctx)` fails after the insert (`eventhub.go:149`), the dead
waiter stays in the map.

### §1.4 Symptom D: track-state consistency minors

Three small, audit-listed inconsistencies in the same subsystem family:

1. **`TrackProcessStepResults` is declared but never entered** — yet
   [ADR-001 v.5] §4.2 prescribes it in the track state machine
   (`docs/design/ADR-001-execution-model.md:116`: `TrackCreated → TrackReady
   → TrackExecutingStep → TrackProcessStepResults → …`). Grep: zero
   `updateState(TrackProcessStepResults)` calls in `internal/instance/`; the
   state exists only in the constant list (`track.go:68-69`), `String()`
   (`track.go:94`), the token mapping (`token.go:80`) and two table tests.
2. **`tokenStateFor` has no `TrackCreated` case** — a token projected from a
   freshly created track falls into `default → TokenInvalid`
   (`internal/instance/token.go:78-95`), although a created track holds a
   real position (its start node) and its token is alive by ADR-001 §6.
3. **`waiters.go` uses bare `fmt.Errorf`** instead of the project's `errs`
   classes (`internal/eventproc/eventhub/waiters/waiters.go:21,25,29,42`) —
   callers can't classify these failures, unlike every sibling error in the
   subsystem.

## §2 Root Cause Analysis

### §2.1 (A) Two owners of one channel close

`runTimerService`'s `ctx.Done()` branch closes `tw.stopCh` (`timer.go:260`)
even though that branch immediately `return`s and **nobody else listens** on
the channel — the close signals nothing. The real signal direction is the
reverse: `Stop()` closes the channel (`timer.go:350`, under `tw.m` together
with the state check) and the service's `<-tw.stopCh` branch reacts
(`timer.go:269-275`). The gratuitous ctx-side close runs **outside** the
mutex, so `Stop()`'s state-guarded close cannot exclude it — a concurrent
engine shutdown (ctx cancel) and hub-initiated `Stop()` race to a double
close. The `Println` (`timer.go:271`) is a leftover debug line in the
legitimate branch.

### §2.2 (B) The unregistration chain was scaffolded but never implemented

The chain is `track.unregisterEvent` → `Instance.UnregisterEvent` →
(should be) thresher → hub → waiter:

- `Instance.RegisterEvent` (`instance.go:543-580`) shows the intended
  shape — guards, then `inst.parentEventProducer.RegisterEvent(proc, eDef)`.
  Its unregister mirror was stubbed at `nil` and never wired
  (`instance.go:584-589`).
- `timeWaiter.AddEventProcessor` maintains `tw.processors` with dedup
  (`timer.go:185-195`, `slices.Index`); its remove mirror was stubbed
  (`timer.go:199-201`).
- The hub side is **already complete** (`eventhub.go:161-208`): find waiter →
  `RemoveEventProcessor` → if empty, `Stop()` + `RemoveWaiter` — it has been
  waiting for the two stubs to become real.

A complication the implementation must respect: on the **fired** timer path
the waiter removes itself — `processTimerEvent` empties its processors,
flips `WSEnded` and calls `tw.hub.RemoveWaiter(eDef.ID())`
(`timer.go:314-322`). The track unregisters *after* that (`track.go:698`),
so a strict chain would hit "waiter not found"
(`eventhub.go:181-186`) and fail the track. Waiter-lifecycle *ownership*
(hub vs waiter — audit 2.5) is ADR-006's decision; this FIX must be
idempotent at the instance level instead of resolving ownership.

### §2.3 (C) Check and insert under different lock acquisitions

`RegisterEvent` takes `RLock` only for the lookup (`eventhub.go:117-119`)
and `Lock` only for the insert (`eventhub.go:144-146`); `CreateWaiter` runs
between them. Two concurrent callers with one `eDef.ID()` interleave:
both miss, both create, the second insert orphans the first waiter and its
`Service` goroutine. (The `eh.started` read at `eventhub.go:105` is **not**
part of this race — `Start` establishes a happens-before edge that already
makes `started` safe to read lock-free, per FIX-001 `eventhub.go:63-78`; the
fix here is purely the waiters-map check-then-insert.)

### §2.4 (D) Declared-but-unwired state machine pieces

The `TrackProcessStepResults` constant was added with the ADR-001 state
machine but `track.run()`'s step loop goes `TrackExecutingStep` → (checkFlows)
`TrackReady`/`TrackEnded` directly; the results-processing stage
(`finalizeNodeExecution` — producer role + frame commit since SRD-007) never
announces itself. `tokenStateFor` was written against the *observed* states
only, so the prescribed-but-unobserved `TrackCreated` projection falls to
`TokenInvalid`. `waiters.go` predates the `errs`-classes convention.

### §2.5 Where the tests are

- Double close: **none** — `timer_test.go` exercises `Stop()`
  (`timer_test.go:188,214`) but never ctx-cancel concurrently with `Stop`.
- Unregistration: only the error paths
  (`eventhub_base_test.go:124-159`, `eventhub_timer_test.go:119` — not-found
  expectations); **no test for a successful removal**, because there is no
  successful removal.
- RegisterEvent concurrency: **none** (no concurrent-registration test in
  `eventhub_*_test.go`).
- `TrackCreated` projection: `m3_projection_test.go` tables the other states;
  `TrackCreated` is absent.

The absence of these tests is how all four defects survived two FIX rounds
in this subsystem.

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. Wrap both `close(stopCh)` sites in `sync.Once` | smallest diff | keeps two owners of one signal; hides the design smell the audit flagged | ❌ rejected: masks, not fixes |
| B. Single close owner: only `Stop()` closes; the ctx branch just exits | one signal direction, no race by construction, honest semantics | slightly larger diff in `runTimerService` | ✅ chosen (A) |
| C. Make the no-ops return "unsupported" errors | honest, tiny | **breaks the live caller** `track.go:618` — every track fails after a caught event (§1.2) | ❌ rejected |
| D. Implement the real chain (waiter removal + instance delegation), idempotent at the instance for already-removed waiters | the hub's existing empty→stop path goes live; leaks actually close; fired-timer flow keeps working | must not pre-decide ADR-006 ownership — tolerance documented as interim | ✅ chosen (B) |
| E. Resolve waiter-lifecycle ownership (drop the waiter's self-removal) here | cleanest single-owner model | that *is* audit 2.5 — triaged to ADR-006, out of FIX scope | ❌ rejected: deferred to ADR-006 |
| F. Fix TOCTOU with double-checked locking under one `Lock` (+ remove dead waiter on `Service` failure) | race-free by construction; `started` read folds under the same lock | `CreateWaiter` (a cheap constructor) now runs under the hub lock | ✅ chosen (C); constructor cost is negligible vs correctness |
| G. Delete `TrackProcessStepResults` instead of wiring it | less code | contradicts ADR-001 §4.2's prescribed state machine — code must follow the conception | ❌ rejected |

### §3.2 Changes by file

#### §3.2.1 `internal/eventproc/eventhub/waiters/waiters.go` — `errs` classes for builder errors

New `errorClass` const for the package; the three nil-guards become
`errs.New(errs.M(…), errs.C(errorClass, errs.EmptyNotAllowed))`; the
unknown-trigger default becomes `errs.C(errorClass, errs.ObjectNotFound)`
with the definition id/type as `errs.D` details.

#### §3.2.2 `internal/eventproc/eventhub/waiters/timer.go` — single close owner; real `RemoveEventProcessor`; logger instead of `Println`

```go
// before (runTimerService, ctx.Done branch):
case <-ctx.Done():
    close(tw.stopCh)
    tckr.Stop()
    …
// after: no close — the ctx branch only stops the ticker and flips the
// state; tw.stopCh has exactly ONE closing owner, Stop(), whose close is
// already atomic with the state check under tw.m.
```

```go
// before (stopCh branch):
fmt.Println("stopping waiter ", tw.id, "...")
// after:
tw.rt.Logger().Debug("timer waiter stopping",
    slog.String("waiter_id", tw.id))
```

`RemoveEventProcessor` becomes the mirror of `AddEventProcessor`
(`timer.go:182-196`, which dedups by value via `slices.Index(tw.processors,
ep)`): under `tw.m`, locate the processor by the same value comparison,
error (`errs`, `ObjectNotFound`) if absent, `slices.Delete` otherwise.

#### §3.2.3 `internal/eventproc/eventhub/eventhub.go` — `RegisterEvent` under one critical section; dead-waiter cleanup on `Service` failure

The body after the nil-guard runs under a single `eh.m.Lock()`:
started-check, lookup (existing waiter → `AddEventProcessor`, return),
`CreateWaiter`, insert. `w.Service(eh.ctx)` stays outside the lock (it
spawns the serving goroutine), but on its failure the just-inserted entry is
removed so no dead waiter lingers in the map.

#### §3.2.4 `internal/instance/instance.go` — `UnregisterEvent` delegates like its register mirror

```go
// after (shape):
func (inst *Instance) UnregisterEvent(
    proc eventproc.EventProcessor, eDefID string,
) error {
    // guards: nil proc; empty eDefID; nil parentEventProducer.
    err := inst.parentEventProducer.UnregisterEvent(proc, eDefID)
    // Idempotent by design: a fired waiter removes itself before the track
    // unregisters (timer.go:314-322), so "waiter/processor not found" means
    // the desired end-state is already reached. INTERIM until ADR-006
    // settles waiter-lifecycle ownership (audit 2.5).
    if err != nil && isAlreadyUnregistered(err) {
        return nil
    }
    return err
}
```

The not-found classification keys on the `errs` classes the hub/waiter
return (`ObjectNotFound` / the hub's not-found `InvalidParameter` at
`eventhub.go:181-186`). The thresher already forwards `UnregisterEvent`
(`thresher.go:278` → `t.eventHub.UnregisterEvent(ep, eDefID)`,
`thresher.go:294`) — the chain needs no thresher change.

#### §3.2.5 `internal/instance/track.go` — enter `TrackProcessStepResults` at the results stage

`finalizeNodeExecution` (the producer role + frame commit, SRD-007 FR-4)
opens with `t.updateState(TrackProcessStepResults)` — the ADR-001 §4.2 stage
becomes real and observable; the token projection for it (`TokenAlive`,
`token.go:80`) is already correct.

#### §3.2.6 `internal/instance/token.go` — explicit `TrackCreated → TokenAlive`

`TrackCreated` joins the first case arm: a created track has a position
(its start node) and a live token per ADR-001 §6; falling to `TokenInvalid`
misreports brand-new tracks to any observer racing track startup.

#### §3.2.7 Tests (new/extended; details §4.1)

`waiters/timer_test.go`, `eventhub_base_test.go` / `eventhub_timer_test.go`,
`internal/instance/m3_projection_test.go`, `trackstate`-adjacent test for
the new stage transition, `waiters_test.go` error classes.

### §3.3 Explicitly out of scope

- Waiter-lifecycle **ownership** (self-removal vs hub-owned) and delivery
  semantics (at-most-once vs buffered) — ADR-006 (audit 2.4/2.5); the
  §3.2.4 idempotency note marks the seam.
- `Array.GetKeys` / `RemoveParameter` receiver (audit 1.6 data items) —
  ADR-011 (data-flow).
- Waiter shutdown synchronization (no WaitGroup over waiter goroutines —
  audit 2.5 tail) — ADR-006.

## §4 Verification

Current coverage on the touched paths:
- unit: timer Stop/state yes; double-close **none**; removal success
  **none**; RegisterEvent concurrency **none**; `TrackCreated` projection
  **none**.
- integration: `eventhub_timer_test.go` register→fire path only.
- smoke: `examples/simple-timer`, `examples/timer-event` (fired-timer path —
  the §3.2.4 idempotency canary).

### §4.1 Regression tests (mandatory)

| Test | Setup | Assertion |
|---|---|---|
| `TestTimerWaiterStopCtxRace` (`waiters/timer_test.go`) | running waiter; cancel ctx and call `Stop()` from two goroutines, `-race`, repeated | no panic, no race; waiter ends stopped |
| `TestTimerWaiterRemoveEventProcessor` (`waiters/timer_test.go`) | waiter with 2 processors | remove 1 → 1 left; remove unknown → `ObjectNotFound`; remove last → empty list |
| `TestRegisterEventConcurrent` (`eventhub_base_test.go`) | N goroutines register the same `eDef.ID()` with distinct processors | exactly ONE waiter in the hub; all processors attached to it; `-race` clean |
| `TestRegisterEventServiceFailure` (`eventhub_base_test.go`) | waiter whose `Service` fails (unsupported definition shaped to pass create, or fault injection) | hub map has NO entry afterwards |
| `TestUnregisterEventFullChain` (`eventhub_timer_test.go`) | register a long timer; unregister before it fires | waiter stopped AND removed from the hub; goroutine-leak check |
| `TestUnregisterEventIdempotent` (`internal/instance`) | fire a short timer; let the track unregister after the waiter self-removed | track completes, no error (the §1.2 live-caller flow) |
| `TestTokenStateForCreated` (`m3_projection_test.go` row) | `TrackCreated` | `TokenAlive` |
| `TestTrackEntersProcessStepResults` (`internal/instance`) | run a plain node; observe states via the step/record trail | `TrackProcessStepResults` entered between execution and `TrackReady`/`TrackEnded` |
| `TestCreateWaiterErrors` (`waiters_test.go`) | nil hub / nil ep / nil eDef / unknown trigger | each returns the `errs` class, not a bare error |

Coverage standard: touched files ≥95 % on changed lines (the CI gate),
100 % target per the project rule.

### §4.2 Smoke

`examples/simple-timer` and `examples/timer-event` run to completion — the
fired-timer unregistration path through the now-real chain.

### §4.3 Observability

After the fix, stopping a waiter logs `timer waiter stopping` at Debug via
the engine logger (no stdout writes anywhere in `waiters/`); unregistration
failures surface as classified `errs` chains instead of silent `nil`.

## §5 Prevention

- Doc comments on every touched exported/unexported symbol state the
  contract: single close owner on `stopCh` (and why), the idempotency seam
  in `Instance.UnregisterEvent` (pointing at ADR-006), the one-critical-
  section invariant in `RegisterEvent`.
- The §4.1 tests are the canaries; `TestTimerWaiterStopCtxRace` and
  `TestRegisterEventConcurrent` run under the suite-wide `-race` gate.
- `make lint` already forbids nothing here — the `fmt.Println` class is
  worth a future `forbidigo` rule (`fmt.Print*` in non-main packages);
  noted in §8.3 backlog, not wired in this FIX.

## §6 Regressions / side-effects

### §6.1 What may rely on the old behaviour

- `eventhub_timer_test.go:119` expects an **error** for unregistering a
  non-existent id at the HUB level — unchanged (the hub stays strict;
  tolerance lives only in `Instance.UnregisterEvent`). Verify the test still
  passes untouched.
- Anything assuming `RemoveEventProcessor` keeps processors forever: grep
  `EventProcessors()` consumers → only the hub's empty-check
  (`eventhub.go:197`) and the waiter's own snapshot — both *want* real
  removal.
- The ctx-cancel path no longer closes `stopCh`: the only reader is the
  service goroutine itself, which exited on the same `ctx.Done()` — no
  external observer exists (grep `stopCh` → 4 hits, all in `timer.go`).

### §6.2 Rollback path

Single-branch revert; no migrations, no data, no persisted state.

### §6.3 Cross-team backlog

None — single-module repo; ADR-006/ADR-011 items are queued in the roadmap.

## §7 Related

- [ADR-001 v.5 Execution Model](../design/ADR-001-execution-model.md) —
  §4.2 track state machine (the wired stage), §6 token projection.
- [ADR-006 Events & Subscriptions](../design/ADR-006-events-and-subscriptions.md)
  (Draft) — inherits: waiter-lifecycle ownership (self-removal vs hub),
  delivery semantics, per-instance subscription keying, waiter shutdown
  synchronization. The §3.2.4 idempotency note is the marked seam.
- Architecture audit 2026-06-11 (`docs/audit/architecture-audit-2026-06-11.md`)
  — items 1.3, 1.4, 1.5, 1.6 (non-data), source of this sweep.
- [FIX-001](FIX-001-thresher-eventhub-startup-race.md) /
  [FIX-002](FIX-002-event-start-registration-lifecycle.md) — earlier rounds
  in the same subsystem; §2.5's missing-tests finding explains how these
  defects survived both.
- Promote-to-ADR candidate: the "single close owner per channel" and
  "interface no-ops are forbidden — implement or return a classified error"
  invariants recur; after ADR-006 they should be written down there.

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

> ⚠️ TODO: fill AFTER landing; records the implementation history and
> empirical findings vs the §3 draft.

### §8.1 Stages by commit (branch `fix/audit-bug-sweep`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| doc | `059e319` | FIX-003 authored (evidence-first; `/review-srd` corrected one race-claim + two snippet inaccuracies) | — |
| M1 (defect D) | `0bab187` | `finalizeNodeExecution` enters `TrackProcessStepResults`; `tokenStateFor` `TrackCreated`→`TokenAlive`; `waiters.go` `fmt.Errorf`→`errs` | `TestNewWaiter` (upgraded), `TestTokenStateProjection` row, `TestTrackEntersProcessStepResults`, `eventhub_message_test` re-point |
| M2 (defect A) | `2474e69` | timer single close owner (ctx branch stops closing); `Println`→`Logger().Debug` | `TestTimerWaiterStopCtxRace` (`-race`; proven to panic under the old double-close) |
| M3 (defect C) | `cd6084f` | `RegisterEvent` one critical section (Service-before-insert) | `TestRegisterEventConcurrent`, `TestRegisterEventServiceFailure` (white-box, `-race`) |
| M4 (defect B) | `c1a5f74` | real `RemoveEventProcessor`; hub not-found→`ObjectNotFound`; idempotent `Instance.UnregisterEvent` | `TestTimerWaiterRemoveEventProcessor`, `TestUnregisterEventFullChain`, `TestInstanceUnregisterEvent`; timer examples smoke |

`make ci` green at each stage; cumulative diff-coverage 100% of 76 changed
lines (gate ≥95).

### §8.2 Empirical findings — where reality diverged from the §3 draft

- **§3.2.3 — Service-before-insert beats insert-then-cleanup.** The draft
  proposed inserting the waiter, then `delete`-ing it on a `Service` failure.
  Implementation found the cleaner shape: start the waiter (Service) *before*
  inserting it into the map, so a failed start simply never enters the map —
  no dead-waiter cleanup branch exists. Matters for future readers: there is
  deliberately no "remove on failure" code to look for.
- **§4.1.1 — `TestCreateWaiterErrors` realized as an upgraded
  `TestNewWaiter`.** The existing `TestNewWaiter` already drove all four
  builder-error cases (`require.Error`); rather than add a redundant test it
  was upgraded to assert the `errs` classes (`errors.As` + `HasClass`). Same
  coverage, one test.
- **`eventhub_message_test` relied on the old bare-error string.** The §6.1
  audit anticipated old-behaviour reliance; the message-unsupported test
  asserted the literal `fmt.Errorf` string (`"eventDefintion"` typo). It was
  re-pointed to assert the hub's wrapping `BUILDING_FAILED` class plus the
  preserved inner `OBJECT_NOT_FOUND` — a real instance of the §6.1 risk.
- **Hub not-found class changed `InvalidParameter`→`ObjectNotFound`.** Not in
  the §3.2 file list as a standalone item, but required by §3.2.4's
  idempotency: aligning all three already-absent cases (waiter gone,
  processor gone, `RemoveWaiter` not-found) under one class lets the instance
  key idempotency on `ObjectNotFound` alone. Existing tests asserted the
  message, not the class, so none broke.

### §8.3 Backlog (out of FIX-003 scope)

- `forbidigo` lint rule for `fmt.Print*` outside `main` packages (§5).

## §9 Open questions

- None. The live-caller constraint (§1.2) and the self-removal seam (§2.2)
  drove the alternatives table; ownership questions are explicitly ADR-006's.
