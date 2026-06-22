# FIX-007 «Event-Based gateway concurrent-fire races (double-win + gate self-execution)»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted v.1 (2026-06-21, branch `fix/event-gate-concurrent-fire`, implemented).
**Date:** 2026-06-21.
**Author:** Ruslan Gabitov.
**Branch:** `fix/event-gate-concurrent-fire` (the defect is a concurrent-fire race in the Event-Based gateway's deferred choice).
**Tracking issue:** [#164](https://github.com/dr-dobermann/gobpm/issues/164) (bug).
**Paired doc:** none (local to `internal/instance` track event delivery).
**Upstream:** [SRD-024 v.1](../srd/SRD-024-event-based-gateway.md) (the gate this fixes), [SRD-026 v.1](../srd/SRD-026-signal-events.md) (signals — concurrent broadcast delivery makes the race reachable), [ADR-001 v.5](../design/ADR-001-execution-model.md) (the single-writer execution model the follow-up ADR in §7 would extend).

---

## 1. Symptoms

The Event-Based gateway's deferred-choice invariant — **exactly one arm wins** — can be violated under **concurrent** event delivery: two arms both win (a double-win). It surfaced as a CI flake.

```
--- FAIL: TestEventGatewayConcurrentFires (0.01s)
    event_gateway_internal_test.go:238:
        Error: Should be true
        Test:  TestEventGatewayConcurrentFires
        Messages: exactly one arm wins
```

Rare: the test passes ~60×/60× locally under `-race`; CI hit the failure once. The test (`internal/instance/event_gateway_internal_test.go:209`) builds a gate with two signal arms (`GO_A`, `GO_B`), fires both **concurrently** via two goroutines each calling `eh.PropagateEvent`, waits for the instance to complete, then asserts (`:238`):

```go
require.True(t, ranA != ranB, "exactly one arm wins")
```

`ranA != ranB` is an XOR — exactly one arm's path (arm + end) ran. A failure means **both** ran (the double-win) — or, far less likely, neither.

---

## 2. Root cause analysis

The flake is **two distinct concurrent-fire races** on the gate track, not one — the second was found during implementation (`eventMu` fixed the double-win but the canary still failed). Both stem from the gate track being mutated by a **waiter goroutine** (`ProcessEvent`) racing another goroutine: a second waiter (race 1), then the track's own `run()` (race 2).

### 2.1 Race 1 — double-win (waiter-vs-waiter): a TOCTOU in `track.ProcessEvent`

An Event-Based gateway subscribes the hub on behalf of **all** its arms through a single gate track (SRD-024 v.1 §4.1) — the gate track is the one `EventProcessor` for every arm's definition. So two concurrent `eh.PropagateEvent` calls (`GO_A`, `GO_B`) become **two waiter goroutines** both invoking `ProcessEvent` on the **same** track. The method guards on the track state, then transitions it much later, with no lock across (`internal/instance/track.go`):

```go
func (t *track) ProcessEvent(...) error {
	if !t.inState(TrackWaitForEvent) { ... }     // :838 — guard (check)
	... ep.ProcessEvent(...) ...                  // bind + (gate) resolve the arm
	t.advanceToArm(n, er, eDef)                   // :889 — append the winning arm's step
	t.updateState(TrackReady)                     // :892 — state leaves WaitForEvent (act)
}
```

```
goroutine A (GO_A)              goroutine B (GO_B)
:838 inState(WaitForEvent)=true
                                :838 inState(WaitForEvent)=true   ← both pass
:889 advanceToArm(armA)
                                :889 advanceToArm(armB)           ← both arms appended
```

Both append an arm step → both arms run → `ranA && ranB`. The narrow window (state changes only at `:892`) is why it is rare. Signals make it reachable in real use: a broadcast (`broadcastSignal`, SRD-026) fans out by name and can deliver to two arms near-simultaneously.

### 2.2 Race 2 — gate self-execution (waiter-vs-`run()`)

Serializing `ProcessEvent` (race 1) was **necessary but not sufficient** — the stress canary still failed, now with the instance `Completed` but the winning arm short of its end (`arm=true, end=false`). A *different* goroutine pair: the waiter (`ProcessEvent`) vs the track's own `run()` loop, which captured the step **before** the wait-guard:

```go
for {
	step := t.currentStep()                 // captured here — the GATE step
	... if t.inState(TrackWaitForEvent) { continue } ...   // :443 — wait-guard
	... executeNode(ctx, step) ...          // ran the stale captured step
}
```

When `ProcessEvent` (waiter goroutine) flipped the state out of `WaitForEvent` and appended the arm step **mid-iteration**, `run()` fell through the `:443` guard but executed the **stale GATE step** — the gate node's `Exec` fails loudly ("the gate must not be executed", SRD-024) → `TrackFailed`, and the instance then silently completed with the arm short of its end. (That silent completion of a `TrackFailed` track is a *separate* latent bug — see §7 / FIX-008 / #165.)

### 2.3 Why the test didn't catch it earlier

`TestEventGatewayConcurrentFires` fired the two events exactly once per run — it exercised the races but only **non-deterministically** triggered them, so it passed nearly always and did not reliably guard the invariant. §4.1 adds a looped stress canary that reproduces both.

---

## 3. Solution

A two-part serialization fix plus a benign-drop tidy:

1. **`eventMu`** (race 1): a dedicated per-track mutex held across `ProcessEvent`'s critical section, so the first fire completes (transitioning the state) before any second fire is evaluated; the loser then fails the guard.
2. **Re-fetch-after-guard** (race 2): `run()` reads the current step **after** the wait-guard, not before — once the track is no longer `WaitForEvent`, `advanceToArm` has appended the arm step, so `run()` runs the arm, never the stale gate step. `eventMu` alone did not fix this (a different goroutine pair); the re-fetch is the surgical fix for the waiter-vs-`run()` race.
3. **Benign loser-drop**: the guard returns `eventproc.ErrRejected` (not an InvalidState error), and the signal waiter treats `ErrRejected` as a Debug no-op (mirroring the message waiter), so the normal deferred-choice drop no longer logs a misleading "delivery failed" WARN.

The correlation-reject path (`validateAndAssociate` → `ErrRejected`) needs **no special handling**: it doesn't transition the state, so a wrong-conversation message naturally stays waiting.

### 3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| **A. `eventMu` + re-fetch-after-guard** | Surgical; serializes waiter-vs-waiter (`eventMu`) **and** fixes waiter-vs-`run()` (re-fetch); the reject path is correct for free | a losing fire blocks briefly (µs) | ✅ **chosen** |
| B. Claimed-flag (`bool` guard + claim/release) | loser fails fast (no block) | needs an explicit un-claim on the reject path or the waiter wedges — extra state, error-prone | ❌ rejected |
| C. Async event-consumer / single-writer event delivery (route `ProcessEvent` through the instance loop) | eliminates the **whole** waiter-goroutine race class (both races + future ones) | changes `PropagateEvent`'s **synchronous contract** (inline error / no-catcher no-op / `ErrRejected`) — a design change touching the waiter→track→loop flow | ❌ for now → ADR-017 (§7) |

`eventMu` **alone** (a per-site lock for race 1) was insufficient — race 2 (waiter-vs-`run()`) needed the re-fetch. Per-site fixes patch each site; the race *class* is what ADR-017 would close.

**Critical detail:** `eventMu` MUST be a **new** mutex, not `t.m`. `t.m` (a `sync.RWMutex`) is taken **inside** `ProcessEvent` (the steps read; `advanceToArm`) — Go mutexes aren't reentrant, so holding `t.m` across the body would deadlock.

### 3.2 Changes by file

#### §3.2.1 `internal/instance/track.go`

- **`eventMu sync.Mutex`** field on `track` (beside `m sync.RWMutex`), held as the first statement of `ProcessEvent` (`:835`). The guard's not-`WaitForEvent` case (`:838`) now returns `eventproc.ErrRejected` (benign drop) instead of an InvalidState error.
- **`run()` re-fetch** (`:454`): `step := t.currentStep()` moved to **after** the wait-guard (`:443`), so it observes the advanced arm step rather than the stale gate step.

#### §3.2.2 `internal/eventproc/eventhub/waiters/signal.go`

The delivery loop treats `errors.Is(err, eventproc.ErrRejected)` (`:212`) as a Debug no-op ("signal delivery skipped: catcher not waiting", `:216`) and continues; other errors keep the existing `Warn` (`:223`). Mirrors the message waiter.

#### §3.2.3 `internal/eventproc/eventhub/waiters/signal_test.go`

`TestSignalWaiterEdgeCases` extended with an `ErrRejected`-returning catcher (covers the benign branch).

(The timer waiter was **not** changed — see §6.)

---

## 4. Verification

`TestEventGatewayConcurrentFires` (`event_gateway_internal_test.go:210`) exercised the races non-deterministically — not a reliable canary.

### 4.1 Regression tests

`TestEventGatewayConcurrentFiresStress` (`event_gateway_internal_test.go:247`): the two-arm gate, both arms fired concurrently, **looped 500× (a fresh instance per iteration)** under `-race`, asserting exactly one arm wins every iteration. It reproduces **both** races pre-fix (the double-win, then the gate-self-execution at iter ~354) and is green post-fix.

`TestSignalWaiterEdgeCases` (`waiters/signal_test.go:95`) is extended with an `ErrRejected`-returning catcher — covers the benign-drop branch.

Post-fix: `go test ./internal/instance/ -run ConcurrentFires -race -count=8` green; `make ci` green.

### 4.5 Observability

None needed — the deferred-choice loser-drop is now a Debug no-op (`signal delivery skipped: catcher not waiting`), not a WARN.

---

## 5. Prevention

- Doc-comment on `ProcessEvent` stating the serialization invariant: **one event is processed to completion per track**, guarded by `eventMu`; concurrent fires at an Event-Based gateway are serialized so exactly one arm wins.
- Doc-comment on the `run()` re-fetch: the step is read **after** the wait-guard so a gate advance landing mid-iteration is observed (never the stale gate step).
- The §4.1 stress test is the named canary — if it falls, either the serialization or the re-fetch regressed.

---

## 6. Regressions / side-effects

### 6.1 What may rely on the old behaviour

`ProcessEvent` is the delivery path for **every** waited event — message/timer/signal intermediate catch, Receive Task, and the Event-Based gateway. Audit before landing:

- **Deadlock:** confirm `eventMu` never nests with `t.m` or the instance loop in a cycle. `eventMu` is held across the body; `t.m` is taken *inside* (steps / `advanceToArm`) — strictly nested (eventMu ⊃ t.m), never the reverse, so no lock-order cycle. The loop never takes `eventMu`.
- **Normal single-event paths** (catch, receive, gate first-fire) still work — `eventMu` is uncontended there.
- **Correlation reject** (`validateAndAssociate` → `ErrRejected`) still leaves the track waiting (state untouched under the held lock).
- The loser-drop now returns `eventproc.ErrRejected` (was an InvalidState error); the signal + message waiters treat it as a benign no-op, so no failure surfaces to callers.
- **Timer-waiter note (follow-up, not in this FIX):** the timer waiter still treats *any* delivery error as a waiter failure (`WSFailed`), so a timer-arm loser-drop would mark the waiter failed. Pre-existing and rare — timer arms don't fire concurrently the way signals broadcast — so it's left unchanged to keep FIX-007 focused; aligning the timer waiter with the signal/message benign-`ErrRejected` handling is a small follow-up.
- **Busy-wait yield (race-2 side-effect, fixed here):** the race-2 re-fetch moved `step := t.currentStep()` out of the `TrackWaitForEvent` busy-wait, removing the per-spin `t.m.RLock` that implicitly yielded — tightening the hot spin enough to starve the per-instance loop under `-race`, which **amplified a pre-existing ~1/100 `TestComplexAbortInstance` flake** (master flakes too, same rate) to ~7/100. A `runtime.Gosched()` in the spin's `continue` path restores master parity (~1/100) with no added latency. The residual ~1/100 is the poll-based `TrackWaitForEvent` wait anti-pattern itself — its proper cure is single-writer event delivery (ADR-017), not this FIX. The yield is correct regardless (a hot busy-wait should not starve the scheduler).

### 6.2 Rollback path

Single-commit revert (the `eventMu` field, the `run()` re-fetch + `runtime.Gosched()` yield, the guard→`ErrRejected`, and the test).

---

## 7. Related

- **Follow-up ADR — ADR-017 (single-writer event delivery):** route `ProcessEvent` through the instance loop / a per-instance event queue so a track is mutated only by its owner — eliminates the whole waiter-goroutine race class (both races here + future ones) and aligns with ADR-001 v.5's single-writer principle, at the cost of redesigning `PropagateEvent`'s synchronous contract (inline error / no-catcher no-op / `ErrRejected`). Drafted as a deferred conception after this fix lands.
- **Sibling bug — FIX-008 / [#165](https://github.com/dr-dobermann/gobpm/issues/165):** a `TrackFailed` track silently completes the instance (`evEnded` → `active--` → `Completed`, `lastErr` lost) — found during this diagnosis (it masked race 2's failure as a silent partial completion). Latent in general; an independent surgical fix.
- [SRD-024 v.1](../srd/SRD-024-event-based-gateway.md) — the Event-Based gateway whose deferred-choice invariant this protects.
- [SRD-026 v.1](../srd/SRD-026-signal-events.md) — signals; concurrent broadcast delivery is what makes the race reachable in real use.
- [ADR-001 v.5](../design/ADR-001-execution-model.md) — the single-writer execution model the §7 follow-up ADR would extend.

---

## 8. Implementation summary (stage-by-stage actual landings + deltas)

### 8.1 Stages by commit (branch `fix/event-gate-concurrent-fire`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| Doc | `8e44513` | FIX-007 (this doc, Draft) | — |
| Fix | `97db6c4` | `track.go` (`eventMu` + `run()` re-fetch + guard→`ErrRejected`); `signal.go` (`ErrRejected`-benign delivery); `signal_test.go` (ErrRejected catcher); the stress canary | `TestEventGatewayConcurrentFiresStress`, `TestSignalWaiterEdgeCases` |

The fix commit was amended once: an initial version also touched the timer waiter and had a per-package coverage gap; the timer change was reverted and the signal `ErrRejected` test added before landing.

### 8.2 Empirical findings — where reality diverged from the §3 draft

- **The RCA was incomplete: one race → two.** The draft (and #164) saw only the double-win (waiter-vs-waiter). `eventMu` fixed that, but the stress canary still failed — a second race (gate self-execution, waiter-vs-`run()`) surfaced, fixed by the `run()` re-fetch-after-guard. §2 now documents both.
- **`eventMu` necessary-but-not-sufficient.** A per-site lock closes only its site; race 2 was a different goroutine pair — the evidence motivating ADR-017 (the race *class*).
- **The feared "needs single-writer" turned out surgical.** Diagnosis suggested race 2 might only be cleanly fixable via the async event-consumer (ADR-017); the re-fetch fixed it surgically. ADR-017 remains architectural prevention, not a prerequisite.
- **Timer-waiter consistency reverted.** An interim version also made the timer waiter treat `ErrRejected` benignly; it dragged in the timer waiter's untested `Process` path and wasn't the reported symptom, so it was reverted (the timer-arm benign-drop is a §6 follow-up).
- **A sibling latent bug found.** The `TrackFailed`-silently-completes hole (§7, FIX-008 / #165) was discovered while diagnosing race 2 — it had masked race 2's failure as a silent partial completion.

### 8.3 Verification (V-results)

- `make ci` PASS: build, lint, `-race`, **diff-coverage 100% of 30 changed lines** (`track.go` 15/15, `signal.go` 15/15), govulncheck.
- `TestEventGatewayConcurrentFiresStress` green under `-race -count=8` (4000 concurrent-fire iterations); reproduced both races pre-fix.

## 9. Open questions

- **None.**
