# FIX-007 «Event-Based gateway double-win under concurrent fires»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Draft v.1 (2026-06-21, branch `fix/event-gate-concurrent-fire`, not yet implemented).
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

### 2.1 A TOCTOU in `track.ProcessEvent` (`internal/instance/track.go`)

`ProcessEvent` guards on the track state, then much later transitions it — with no lock held across the two:

```go
func (t *track) ProcessEvent(ctx context.Context, eDef flow.EventDefinition) error {
	if !t.inState(TrackWaitForEvent) {          // :816  — lock-free guard (check)
		return errs.New(errs.M("...doesn't expect any event"), ...)
	}
	...
	if t.instance.validateAndAssociate(ctx, eDef) { // :847 — correlation reject (keeps waiting)
		return eventproc.ErrRejected
	}
	if err := ep.ProcessEvent(ctx, eDef); err != nil { // :851 — bind + (gate) resolve the arm
		return err
	}
	...
	if er, ok := n.(eventRouter); ok {
		t.advanceToArm(n, er, eDef)             // :868 — append the winning arm's step
	}
	t.updateState(TrackReady)                   // :871 — state finally leaves WaitForEvent (act)
	return nil
}
```

The check is at `:816`; the state only changes at `:871`, **after** `advanceToArm`. The window `:816 → :871` is unprotected.

### 2.2 Why two arms win

An Event-Based gateway subscribes the hub on behalf of **all** its arms through a single gate track (SRD-024 v.1 §4.1) — the gate track is the one `EventProcessor` for every arm's definition. So two concurrent `eh.PropagateEvent` calls (here `GO_A` and `GO_B`) become **two waiter goroutines** both invoking `ProcessEvent` on the **same** gate track:

```
goroutine A (GO_A)              goroutine B (GO_B)
:816 inState(WaitForEvent)=true
                                :816 inState(WaitForEvent)=true   ← both pass
:868 advanceToArm(armA)
                                :868 advanceToArm(armB)           ← both arms appended
:871 updateState(Ready)
                                :871 updateState(Ready)
```

Both append an arm step → both arms run → `ranA && ranB` → the invariant breaks. The narrow window (state changes only at `:871`) is why it is rare.

Signals make this reachable in real use, not just in a stress test: a signal broadcast (`broadcastSignal`, SRD-026) fans out to every catcher by name and can deliver to two arms of the same gate near-simultaneously.

### 2.3 Why the test didn't catch it earlier

`TestEventGatewayConcurrentFires` exists but fires the two events exactly once per run — it exercises the race but only **non-deterministically** wins it, so it passed nearly always and did not reliably guard the invariant (§4.1 hardens it).

---

## 3. Solution

Serialize event delivery to a track with a **dedicated per-track mutex** held across `ProcessEvent`'s critical section, so the first fire runs to completion (transitioning the state) before any second fire is evaluated. The loser then fails the `:816` guard (state is no longer `TrackWaitForEvent`) and is dropped.

The correlation-reject path needs **no special handling**: `validateAndAssociate` (`:847`) returning `ErrRejected` does not transition the state, so a wrong-conversation message naturally leaves the track in `TrackWaitForEvent` — the next event still finds it waiting.

### 3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| **A. Dedicated `eventMu` held across `ProcessEvent`** | Minimal; serializes the check+act; the reject path is correct for free (state untouched ⇒ still waiting) | A losing fire blocks briefly (µs) before being dropped | ✅ **chosen** |
| B. Claimed-flag (`bool` guard + claim/release) | Loser fails fast (no block) | Needs an **explicit un-claim** on the `validateAndAssociate` reject path, or a rejected message wedges the waiter — extra state, easy to get wrong | ❌ rejected: more error-prone for no real gain |
| C. Async event-consumer queue (route `ProcessEvent` through the instance loop / a per-instance event queue — single-writer, ADR-001-aligned) | Eliminates the **whole** waiter-goroutine race class, not just this site | Changes `PropagateEvent`'s **synchronous contract** — callers get the processing error / no-catcher no-op / `ErrRejected` inline today; a queue decouples that — a design change touching the waiter→track→loop flow | ❌ rejected for now: too big for a one-shot FIX → recorded as a follow-up ADR (§7) |

**Critical implementation detail:** it MUST be a **new** mutex, **not** the existing `t.m`. `t.m` (a `sync.RWMutex`, `track` field at `:178`) is taken **inside** the method — the steps `RLock` at `:831` and again inside `advanceToArm`. Go mutexes are not reentrant, so holding `t.m` across the body would deadlock; reusing it would force gutting the inner locking. A separate `eventMu` is the surgical change.

### 3.2 Changes by file

#### §3.2.1 `internal/instance/track.go` — `eventMu` field + held critical section

New field on `track` (placed next to the existing `m sync.RWMutex`):

```go
// eventMu serializes ProcessEvent on this track: one event is processed to
// completion (guard → state transition) before the next is evaluated, so
// concurrent fires at an Event-Based gateway can't both advance an arm
// (FIX-007). Distinct from m (the steps lock, taken inside) — Go mutexes
// aren't reentrant.
eventMu sync.Mutex
```

`ProcessEvent` takes `eventMu` for the whole body:

```go
func (t *track) ProcessEvent(ctx context.Context, eDef flow.EventDefinition) error {
	t.eventMu.Lock()
	defer t.eventMu.Unlock()

	if !t.inState(TrackWaitForEvent) {   // loser (2nd concurrent fire) lands here → dropped
		return errs.New(errs.M("...doesn't expect any event"), ...)
	}
	// ... unchanged: steps read, validateAndAssociate, ep.ProcessEvent, advanceToArm, updateState ...
}
```

No other production change. `validateAndAssociate`, `advanceToArm`, the gate, and the catch/receive paths are untouched.

---

## 4. Verification

Current coverage: `TestEventGatewayConcurrentFires` (`event_gateway_internal_test.go:209`) exercises the race but wins it non-deterministically — it is not a reliable canary.

### 4.1 Regression test (mandatory)

Harden the canary so it reproduces the double-win **pre-fix** and is green **post-fix**.

| Test | Setup | Assertion |
|---|---|---|
| `TestEventGatewayConcurrentFires` (or a new `TestEventGatewayConcurrentFiresStress`) | the same two-arm gate; fire both arms concurrently, **looped 200–1000 iterations** (a fresh instance per iteration) under `-race` | every iteration: exactly one arm wins (`ranA != ranB`); never a double-win |

Run pre-fix (expect a failure within the loop) and post-fix (`go test ./internal/instance/ -run ConcurrentFires -race -count=1` green; also `-count=20`).

### 4.5 Observability

None needed — the loser already returns the existing "doesn't expect any event" error, which `broadcastSignal` ignores (best-effort delivery).

---

## 5. Prevention

- Doc-comment on `ProcessEvent` stating the serialization invariant: **one event is processed to completion per track**, guarded by `eventMu`; concurrent fires at an Event-Based gateway are serialized so exactly one arm wins.
- The §4.1 stress test is the named canary — if it falls, the serialization regressed.

---

## 6. Regressions / side-effects

### 6.1 What may rely on the old behaviour

`ProcessEvent` is the delivery path for **every** waited event — message/timer/signal intermediate catch, Receive Task, and the Event-Based gateway. Audit before landing:

- **Deadlock:** confirm `eventMu` never nests with `t.m` or the instance loop in a cycle. `eventMu` is held across the body; `t.m` is taken *inside* (steps / `advanceToArm`) — strictly nested (eventMu ⊃ t.m), never the reverse, so no lock-order cycle. The loop never takes `eventMu`.
- **Normal single-event paths** (catch, receive, gate first-fire) still work — `eventMu` is uncontended there.
- **Correlation reject** (`validateAndAssociate` → `ErrRejected`) still leaves the track waiting (state untouched under the held lock).
- The loser's "doesn't expect any event" error is already swallowed by `broadcastSignal` (best-effort) — no new error surfaces to callers.

### 6.2 Rollback path

Single-commit revert (the `eventMu` field + the lock placement + the test).

---

## 7. Related

- **Promote-to-ADR candidate (follow-up):** Option C — an **async event-consumer / single-writer event delivery** model (route `ProcessEvent` through the instance loop or a per-instance event queue). It would eliminate the whole class of waiter-goroutine races and align event delivery with ADR-001 v.5's single-writer principle, at the cost of redesigning `PropagateEvent`'s synchronous contract (inline error / no-catcher no-op / `ErrRejected`). To be drafted as an ADR after this fix lands.
- [SRD-024 v.1](../srd/SRD-024-event-based-gateway.md) — the Event-Based gateway whose deferred-choice invariant this protects.
- [SRD-026 v.1](../srd/SRD-026-signal-events.md) — signals; concurrent broadcast delivery is what makes the race reachable in real use.
- [ADR-001 v.5](../design/ADR-001-execution-model.md) — the single-writer execution model the §7 follow-up ADR would extend.

---

## 8. Implementation summary (stage-by-stage actual landings + deltas)

> ⚠️ TODO: fill AFTER landing — stage commits, scope, tests, empirical deltas vs this §3 draft.

## 9. Open questions

- **None.**
