# FIX-012 — Timer-waiter clock honouring & cycle-count correctness

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-06-29 |
| Owner | Ruslan Gabitov |
| Related | [ADR-004 v.1 Runtime environment contract](../design/ADR-004-runtime-environment-contract.md), [ADR-006 v.2 §2.5 Waiter lifecycle](../design/ADR-006-events-and-subscriptions.md) |

One-shot remediation of four defects in the timer event waiter
(`internal/eventproc/eventhub/waiters/timer.go`) surfaced by
`docs/audit/code-review-codex-second-pass-2026-06-29.md` §1 (P1),
`docs/audit/code-review-third-pass-2026-06-29.md` §3.8 (P3), and
`docs/audit/code-review-2025.md` §2.9 / §3 (naming): the waiter validates
against the injected runtime `Clock` but then waits on the real wall clock, a
cyclic timer fires one time too many, a not-ready diagnostic reports the wrong
state, and the error class is misspelled.

> The earlier `architecture-audit-2026-06-11.md` §1.3 (double `close(stopCh)` +
> a `fmt.Println` debug line) is **already fixed** — `runTimerService` now has a
> single close-owner documented at `timer.go:285-289` (FIX-003 A) and no
> `fmt.Print*` remains. This FIX does not touch that.

## 1. Symptoms

- **1.1 (P1) The waiter ignores the injected `Clock` while waiting.** Timer
  *validation* reads `tw.rt.Clock().Now()` (`timer.go:143`), but the service
  goroutine computes the delay with `time.Until(tw.next)` (`timer.go:258`) and
  waits with `time.NewTicker(tw.duration)` (`timer.go:293`) — the real wall
  clock. A test or embedding app that injects a fake `Clock` (ADR-004's
  contract, "tests inject fake") has its timer creation validated against the
  fake clock yet the goroutine still sleeps on real time: deterministic timer
  tests hang or only pass by really sleeping, and the runtime extension
  contract is only half-honoured.
- **1.2 (P3) A cyclic timer fires N+1 times for a `Cycle` count of N.**
  `processTimerEvent` tests `if tw.cyclesLeft == 0 { …terminal… }`
  (`timer.go:354`) **before** `tw.cyclesLeft--` (`timer.go:366`), so the
  terminal check spends one extra cycle. The sole in-repo caller —
  `timer_test.go:277` "cycle events" — only passes because it feeds
  `cycles - 1` (`timer_test.go:307`) to compensate. An external caller asking
  for N cycles silently gets N+1 deliveries.
- **1.3 The not-ready diagnostic reports the expected state, not the actual
  one.** `Service` rejects a non-ready waiter with
  `errs.D("current_state", eventproc.WSReady)` (`timer.go:252`) — but `WSReady`
  is the *required* state; the branch is reached precisely because
  `tw.state != WSReady`, so the diagnostic prints the value the state is **not**
  and hides what it actually is.
- **1.4 The error class identifier and string are misspelled.**
  `TimerWatierError = "TIMER_WAITER_ERRROR"` (`timer.go:23-24`): "Watier"
  (transposed) and "ERRROR" (triple-R). The misspelled constant is the error
  class on every timer-waiter error and leaks into structured error output.

## 2. Root-cause analysis

- **1.1**: two time sources in one component. When the waiter was written the
  `pkg/clock.Clock` abstraction already exposed `After(d) <-chan time.Time`
  alongside `Now()`, but the execution path was left on `time.NewTicker` /
  `time.Until`. Validation was migrated to the injected clock; waiting was not.
- **1.2**: classic off-by-one — a "decrement after acting" counter whose
  terminal test runs before the decrement. It stayed latent because the only
  caller compensates, so no test caught the extra fire (the test asserts the
  *compensated* count).
- **1.3**: the diagnostic was given the comparison constant (`WSReady`) instead
  of the receiver field (`tw.state`); both are `WaiterState`, so it compiled and
  read plausibly.
- **1.4**: typos in an identifier and a string literal; never grep-caught
  because every reference uses the same misspelled symbol, so it is internally
  consistent.

## 3. Solution

### 3.1 Considered alternatives
- **1.1 — inject a ticker/timer factory into the runtime instead of using
  `Clock.After`.** Rejected: `Clock` already owns `After`; re-arming
  `Clock().After(d)` per cycle is the minimal change that honours ADR-004's
  existing contract and needs no new runtime surface.
- **1.1 — keep `time.NewTicker` and only special-case a fake clock.** Rejected:
  that bakes the test/real split into production code; the whole point of the
  injected `Clock` is that production and test take the *same* path.
- **1.2 — document N+1 as intended and keep the caller `-1` compensation.**
  Rejected: an off-by-one that every caller must know to subtract is a latent
  bug, not a contract; `Cycle(N)` must deliver N.

### 3.2 Per-site changes
- **3.2.1** `timer.go` `runTimerService` (`:290-320`) — replace the
  `time.NewTicker(tw.duration)` ticker with a loop that re-arms
  `tw.rt.Clock().After(tw.duration)` each iteration; drop `tckr.Stop()`. The
  `ctx.Done()` / `tw.stopCh` cases are unchanged. `Service` (`:258`) keeps
  computing the absolute-timer delay against the injected clock
  (`tw.next.Sub(tw.rt.Clock().Now())` in place of `time.Until(tw.next)`).
  The purpose of routing the wait through `Clock().After` is **test
  determinism**: an embedder (chiefly a test) can substitute a
  `clocktest.Clock` and drive the timer by `Advance()` with no real sleeping.
  With the default `syscl` clock `After(d)` is `time.After(d)`, so production
  wall-clock behaviour is identical to the former ticker — the change costs
  nothing in production and unlocks deterministic tests (and any future
  simulation/replay clock for free). `runTimerService` carries a detailed
  doc-comment recording this rationale (test determinism + the ADR-004
  injected-`Clock` contract, why `Clock().After` is re-armed per cycle rather
  than a `time.NewTicker`) so a future reader does not "simplify" it back to
  the wall clock and silently re-break deterministic timer tests.
- **3.2.2** `timer.go` `processTimerEvent` (`:353-366`) — decrement first, then
  test the terminal condition (`tw.cyclesLeft--; if tw.cyclesLeft <= 0 { …end… }`)
  so a `Cycle` of N fires exactly N times. Drop the `cycles - 1` compensation in
  `timer_test.go:307` (feed `cycles`) so the test asserts the true count.
- **3.2.3** `timer.go` `Service` (`:252`) — report the actual state:
  `errs.D("current_state", tw.state)` (optionally keep `WSReady` under a
  separate `expected_state` key).
- **3.2.4** `timer.go` (`:23-24`) — rename `TimerWatierError` →
  `TimerWaiterError` and its value `"TIMER_WAITER_ERRROR"` →
  `"TIMER_WAITER_ERROR"`; update every reference in the file.
- **3.2.5** `timer.go:359` — bump the stale in-code reference `ADR-006 v.1 §2.5`
  → `ADR-006 v.2 §2.5` (the waiter-lifecycle ADR is now v.2) while the file is
  open.

## 4. Verification

### 4.1 Tests
| Test | Asserts |
|---|---|
| `TestTimerWaiterHonorsInjectedClock` | with a `clocktest.Clock`, advancing the fake clock (no real sleep) drives the waiter to fire; the test completes well under any real-time duration |
| `TestTimeWaiter/"cycle events"` (rewritten on a `clocktest.Clock`) | a `Cycle` of N delivers **exactly** N events (def fed `N`, no `-1`), driven by `Advance` with no real sleep; a further advance after the Nth fire yields nothing (no `(N+1)`th) |
| `TestTimerWaiterServiceRejectsNonReady` | `Service` on a non-ready waiter returns an error whose `current_state` diagnostic is the actual state, not `WSReady` |
| `TestTimerWaiterServiceRejectsElapsedTimer` | a timer validated as future at creation, then overtaken by an advanced clock, is rejected by `Service` (`next.Sub(Clock().Now()) <= 0`) — covers the non-positive-duration guard |
| (compile-time) | all references to the renamed `TimerWaiterError` build |

## 5. Prevention
The fake-clock test pins the waiter to the injected `Clock`, so a regression
back to a real-wall-clock wait fails deterministically instead of hanging. The
cyclic test asserting the exact count (no compensation) makes any future
off-by-one visible.

## 6. Regressions
With the default `syscl` clock, `Clock().After(d)` is `time.After(d)`, so
real-time behaviour is unchanged; the timer examples (`simple-timer`,
`timer-event`, `boundary-events`) keep passing. Cyclic timers now fire N
instead of N+1 — any caller that previously compensated with `-1` must stop
(the only in-repo one, the regression test, is updated here). No public API
signatures change; `TimerWaiterError` is an exported identifier rename within
the waiters package (single-developer repo, no external consumers).

## 7. Related
ADR-004 v.1 (runtime environment contract — the injected `Clock` the waiter
must honour). ADR-006 v.2 §2.5 (waiter lifecycle — the EventHub is the sole
remover; the timer reports its fire via `WaiterFired`). FIX-003 A already fixed
the neighbouring double-`close(stopCh)` / `fmt.Println` defects in the same
file.

## 8. Implementation summary

Landed on branch `fix/audit-remediation-2026-06` across three milestones plus a
coverage top-up, all in `internal/eventproc/eventhub/waiters/timer.go` and its
test.

**§3.2 changes:**
- **3.2.1 clock honouring** — `Service` computes the absolute-timer delay as
  `tw.next.Sub(tw.rt.Clock().Now())` (`timer.go:262`); `runTimerService`
  re-arms `tw.rt.Clock().After(tw.duration)` per loop iteration (`:316`),
  replacing `time.NewTicker`/`time.Until`, with a detailed rationale comment
  (test determinism + ADR-004 contract; do-not-revert warning).
- **3.2.2 cyclic count** — `processTimerEvent` decrements then tests
  `tw.cyclesLeft <= 0` (`:376-377`), so a `Cycle` of N fires exactly N.
- **3.2.3 diagnostic** — the not-ready guard reports `current_state=tw.state`
  plus `expected_state=WSReady` (`:252-253`).
- **3.2.4 naming** — `TimerWatierError` → `TimerWaiterError`,
  `"TIMER_WAITER_ERRROR"` → `"TIMER_WAITER_ERROR"` (all 11 refs, package-local).
- **3.2.5** — in-code `ADR-006 v.1 §2.5` → `v.2` (`:382`).

**Tests (`timer_test.go`):** `TestTimerWaiterHonorsInjectedClock` (1-hour timer
fires in ms via `Advance`), `TestTimeWaiter/"cycle events"` rewritten on a
`clocktest.Clock` (exactly N, no `(N+1)`th; removed a 7-second real sleep) with
the `advanceUntilFire` helper, `TestTimerWaiterServiceRejectsNonReady`,
`TestTimerWaiterServiceRejectsElapsedTimer`.

**Verification:** `make ci` green (golangci-lint 0 issues, `-race` tests,
govulncheck); diff-coverage **100% of 74 changed lines** with `timer.go`'s
touched functions (`Service`, `runTimerService`, `processTimerEvent`) at 100%;
`simple-timer`, `timer-event`, `boundary-events` examples smoke exit 0.

**Out of scope (already fixed):** the `architecture-audit-2026-06-11` §1.3
double-`close(stopCh)` + `fmt.Println` defect (FIX-003 A); and the
`code-review-2025` §1.5 Duration-/Cycle-only validation gap (a model-layer
concern for a later FIX, not the waiter).

**Commits:** doc `d097d6b`; M1 `dac3342` (naming + diagnostic + pin); M2
`0d7ed32` (clock honouring); M3 `90192a2` (cyclic N); coverage `7955b95`.

## 9. Open questions
None.
