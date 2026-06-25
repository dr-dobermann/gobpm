# SRD-027 — Channel-park inbound event delivery (ADR-017 inbound slice)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-25 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-017 v.1 Channel-based event processing](../design/ADR-017-channel-based-event-processing.md) §2 Rule 1 (inbound slice) |

This SRD lands the **inbound slice** of ADR-017: a waiting track stops busy-spinning and **parks
on a per-track channel**; event producers stop calling into the track on a foreign goroutine and
instead **emit the fired event to the per-instance loop**, which is the **sole sender** to a track's
channel. It removes the `runtime.Gosched` busy-spin and the per-track `eventMu`, makes deferred
choice atomic at the loop, and replaces the O(n) signal-broadcast scan with a name index. The
**outbound slice** (the loop owning token positions — ADR-017 Rule 2) is **SRD-028, not here**.

---

## 1. Background & current state (verified against the code)

ADR-001 v.5 makes the per-instance **loop** the single writer of lifecycle state: tracks `emit`
`trackEvent`s on `inst.events` and `loop()` applies them on one goroutine (`internal/instance/instance.go:608,619`).
Event delivery bypasses that discipline on the inbound side:

- **Synchronous foreign-goroutine delivery.** `EventProducer` reaches a track through
  `track.ProcessEvent` (`internal/instance/track.go:837`), which **mutates the track on the
  producer's goroutine** — it reads `t.steps`, runs the node, advances the arm, and flips the state
  to `TrackReady`. Signal is the worst case: `PropagateEvent → broadcastSignal → w.Process →
  ProcessEvent` runs entirely on the thrower's goroutine (`internal/eventproc/eventhub/eventhub.go:419,498`).
- **Busy-spin wait.** A waiting track does not block — `track.run` loops on `TrackWaitForEvent`
  with `runtime.Gosched()` (`internal/instance/track.go:444-456`), burning CPU and needing an
  explicit yield so it does not starve the loop.
- **Two mutexes papering over the two goroutines.** `eventMu` (`track.go:180-187`) serializes
  concurrent `ProcessEvent` so two arm fires at an Event-Based gateway cannot both pass the
  `TrackWaitForEvent` guard (FIX-007); `m` guards `t.steps` because the producer goroutine and the
  run goroutine both touch it.
- **O(n) signal broadcast.** `broadcastSignal` linearly scans **all** waiters and name-matches
  (`eventhub.go:465-514`); the registry is `map[eDef.ID()]EventWaiter` (`eventhub.go:50`).

ADR-017 (Draft) decided the structural fix. This SRD implements its **inbound** half.

## 2. Requirements

### Functional

- **FR-1 — Per-track park channel.** A `track` exposes a buffered channel `evtCh` and, while in
  `TrackWaitForEvent`, **blocks** in a `select` on it (zero CPU) instead of spinning. `evtCh` has a
  fixed single-slot buffer (§3.6).
- **FR-2 — Producers emit, never call in.** `track.ProcessEvent` (the `eventproc.EventProcessor`
  entry called by producers) no longer mutates the track. It **emits** `trackEvent{kind:
  evDeliver, track, eDef}` to the loop and returns. No track state is read or written on the
  producer goroutine.
- **FR-3 — The loop is the sole sender to `evtCh`.** Only `loop()` sends on a track's `evtCh`
  (dispatch) and only `loop()` closes it (teardown). The producer never holds a track-channel
  reference.
- **FR-4 — Deferred choice is atomic at the loop.** The loop keeps a loop-local set of
  parked-and-undelivered tracks. A track entering the wait emits `evWaiting` (which adds it); the
  **first** `evDeliver` for that track removes it and sends the event; every later `evDeliver` for
  it finds it absent and is **dropped** (the losing arm of an Event-Based gateway / a duplicate
  fire). The FIX-007 concurrent-fire double-win cannot occur, and `eventMu` is removed.
- **FR-5 — Park-before-register ordering.** A track emits `evWaiting` **before** it registers its
  waiters with the hub, so — because `inst.events` is FIFO and registration happens-before any
  matching `evDeliver` — the loop always records a track as parked before an event can target it.
  No fired event is lost to a not-yet-known-parked track.
- **FR-6 — Signal broadcast uses a name index.** The hub maintains `signalName → []subscriber`
  built on register/unregister; `broadcastSignal` looks the name up and emits `evDeliver` to each
  subscriber's instance loop, replacing the O(n) all-waiters scan. A broadcast with no catcher
  yields an empty lookup and is therefore a benign no-op (ADR-006 v.1 §2.4). Cross-instance fan-out
  is preserved (signal is unscoped within reach).
- **FR-7 — Stop wakes a parked track.** `loop()`/`stopAll` closing a track's `evtCh` wakes a track
  blocked in the FR-1 `select` (receive on a closed channel), which then cancels — the existing
  `stopIt` flag covers only the running path.

### Non-functional

- **NFR-1 — No new race.** `go test -race ./...` is clean; the per-track `eventMu` is removed
  because only the track's own goroutine touches its state on receive (delivery is single-consumer).
- **NFR-2 — Message semantics unchanged.** The two-tier correlation match is preserved: the broker
  does the coarse name+key match (ADR-014/016), and `validateAndAssociate` runs on the **track**
  goroutine after receive (still `convMu`-guarded, `instance.go:334`). A correlation mismatch
  re-parks the track (it keeps waiting), never advances it.
- **NFR-3 — Deferred-choice / abort flakes cured.** `TestComplexAbortInstance` and the poll-based
  wait flakes (whose root cause is the busy-spin) no longer flake under `-race`.
- **NFR-4 — Diff coverage ≥ COVER_MIN (95%) on touched functions**, aiming 100%.

## 3. Models

### 3.1 `track` — park channel, drop the event mutex (`internal/instance/track.go`)

Add `evtCh chan flow.EventDefinition` (capacity = the `eventBufferDepth` constant of §3.6 — a
single slot), created at track construction. **Remove** `eventMu sync.Mutex` (`track.go:180-187`) — delivery is now single-consumer
on the track's own goroutine, so there is nothing to serialize.

The `TrackWaitForEvent` branch of `run()` (`track.go:444-456`) replaces the `runtime.Gosched` spin
with a blocking park, and on receive runs the delivery body on **this** goroutine:

```go
case TrackWaitForEvent (in run's loop):
    select {
    case <-ctx.Done():
        t.updateState(TrackCanceled); t.lastErr = ctx.Err(); return
    case eDef, ok := <-t.evtCh:
        if !ok {            // loop closed it on stop (FR-7)
            t.updateState(TrackCanceled); return
        }
        if err := t.deliver(ctx, eDef); err != nil {  // rejected -> stay parked
            // ErrRejected (wrong correlation / not-for-this-arm): re-loop, still WaitForEvent
        }
    }
```

`deliver` is the body lifted out of today's `ProcessEvent` (validate correlation → node
`ProcessEvent` → `unregisterEvent` → `advanceToArm` → `updateState(TrackReady)`), now without the
`eventMu`/`!inState` foreign-goroutine guard (the loop guarantees one delivery; a rejected event
leaves the track in `TrackWaitForEvent`).

### 3.2 `track.ProcessEvent` becomes a non-blocking emit (`internal/instance/track.go:837`)

```go
// ProcessEvent (eventproc.EventProcessor) is called by a producer goroutine. It no longer
// touches track state — it hands the event to the loop, which dispatches it to t.evtCh.
func (t *track) ProcessEvent(_ context.Context, eDef flow.EventDefinition) error {
    t.instance.emit(trackEvent{kind: evDeliver, track: t, eDef: eDef})
    return nil
}
```

### 3.3 `trackEvent` — `eDef` field, `evDeliver` + `evWaiting` kinds (`internal/instance/event.go`)

Add `eDef flow.EventDefinition` to `trackEvent` (`event.go:8`); add two kinds (`event.go:52`) and
their `String()` arms:

```go
// evWaiting: the track entered TrackWaitForEvent (emitted BEFORE it registers its hub waiters,
// FR-5). The loop records it as parked-and-undelivered.
evWaiting
// evDeliver: a producer handed a fired event to the loop for `track` (FR-2). The loop dispatches
// it to the track's evtCh iff the track is parked-and-undelivered, else drops it (FR-4).
evDeliver
```

### 3.4 `loop()` — parked set, dispatch, teardown (`internal/instance/instance.go:619`)

Add a loop-local `waiting := map[string]struct{}{}` (presence ⟺ parked-and-undelivered; loop
goroutine only, no lock — like `active`/`stopping`). New `switch` arms:

```go
case evWaiting:
    waiting[ev.track.ID()] = struct{}{}

case evDeliver:
    if _, parked := waiting[ev.track.ID()]; parked {
        delete(waiting, ev.track.ID())          // consume the wait (winner)
        ev.track.evtCh <- ev.eDef               // sole sender; buffered (default 1)
    }                                            // else: losing arm / duplicate -> drop
```

`stopAll` closes each live track's `evtCh` (FR-7) — safe because the loop is the sole sender.
`evEnded`/`evFailed`/`evAwaiting` also `delete(waiting, id)` so a set entry never outlives its track.

### 3.5 Signal name index (`internal/eventproc/eventhub/eventhub.go`)

Add `signalIdx map[string][]eventproc.EventWaiter` maintained in `registerWaiter`/`UnregisterEvent`
(`eventhub.go:198,331`) for waiters whose definition is `TriggerSignal`. `broadcastSignal`
(`eventhub.go:465`) looks up by name instead of scanning `eh.waiters`; for each subscriber it routes
the event to the track's instance loop (via the existing `ProcessEvent → emit` path of §3.2). No
behavioural change to which catchers receive — only the lookup cost and the delivery goroutine.

### 3.6 Fixed buffer depth — a constant, not an option (`internal/instance`)

```go
// eventBufferDepth is the per-track inbound event-channel capacity. One slot is exactly
// enough: the loop dispatches at most one event per parked episode (it removes the track
// from the waiting set on first delivery, §3.4), and a single slot decouples the loop's
// send from the track's scheduling so the loop never blocks. Unbuffered would risk
// blocking the loop in the window between evWaiting and the track reaching its receive.
const eventBufferDepth = 1
```

No engine option and no `thresherConfig` field: the depth has exactly one correct value under
flip-on-dispatch, so a knob would be a setting nobody should turn. If a future need appears (e.g.
the deferred durability/replay work, ADR-017 §5), introduce the option then.

## 4. Analysis

- **Path (chosen) — ADR-017 Rule 1 (Model Y, loop-dispatched).** Producers emit to the loop; the
  loop is the sole sender. Rationale, alternatives (direct per-track channel; per-site locks; one
  coarse lock), and rejection reasoning live in **ADR-017 v.1 §4** — not repeated here.
- **Why `ProcessEvent` → emit (not a new producer API).** Keeping `EventProcessor.ProcessEvent` as
  the producer entry point means the hub/waiter call sites are unchanged; only the *body* moves off
  the foreign goroutine. Smallest blast radius, and the public interface keeps its shape (its
  semantics change from synchronous-apply to enqueue — documented in §5).
- **Why a loop-local set, not a track flag read by the loop.** Reading track state from the loop is
  the cross-goroutine read Rule 2 forbids (and is SRD-028's subject). The parked set is owned by
  the loop alone, so Slice 1 introduces no new cross-read.
- **Why park-before-register (FR-5).** It is the one ordering hazard: an event firing in the window
  between subscribe and park. Emitting `evWaiting` before `RegisterEvent` puts the parked record on
  `inst.events` ahead of any `evDeliver` the registration can cause (FIFO), closing the window
  without a lock.
- **Engine notes — deferred-choice drop is correct, not lossy.** Dropping an `evDeliver` for an
  absent (already-delivered / not-parked) track only ever discards a losing Event-Based-gateway arm
  or a duplicate fire of an already-consumed catch — never a trigger a parked track still needs.
- **Out of scope (explicit).** The loop owning token positions / join state (ADR-017 Rule 2,
  outbound) → **SRD-028**. No change to message correlation (ADR-014/016), timer scheduling, or the
  Event-Based gateway's arm resolution (`eventRouter`/`advanceToArm`).
- **`inst.events` stays unbuffered (out of scope).** This slice adds `evDeliver`/`evWaiting` onto
  the existing per-instance intake but does **not** retune its capacity. `inst.events` is unbuffered
  by the ADR-001 single-writer backpressure contract (`emit` blocks on `select { events<-ev;
  <-loopDone }`), and the loop never blocks while draining it — flip-on-dispatch sends only to a
  parked-and-undelivered track, so `evtCh <- eDef` always lands in the free single slot. A
  configurable loop-intake buffer is a core-loop throughput knob, not an event-delivery concern;
  introduce it only with measured contention, in its own change.

## 5. Public API surface

- **No new public API.** The buffer depth is an unexported constant (§3.6), not an option.
- **Changed semantics (same signature):** `eventproc.EventProcessor.ProcessEvent` returns once the
  event is **enqueued to the instance loop**, not once it is applied. Delivery and ordering
  (per-track FIFO via the channel) become part of the documented async contract (ADR-017 §5). Hosts
  that implement custom `EventProcessor`s see no signature change.

## 6. Test scenarios

- **T-1 (FR-1, NFR-3):** a catch-event process completes on a fired event with **no busy-spin** —
  assert via no `runtime.Gosched` path (the waiting goroutine is blocked) and stable timing.
- **T-2 (FR-4, FIX-007):** two arms of an Event-Based gateway fire concurrently → exactly one arm
  advances, the other is dropped; `-race` clean, repeated N× with no double-win.
- **T-3 (FR-5):** an event fired in the subscribe→park window is still delivered (park-before-
  register) — not lost.
- **T-4 (FR-6):** a broadcast signal reaches every in-reach catcher across multiple instances via
  the name index; a no-catcher broadcast is a no-op (no error, debug log).
- **T-5 (FR-7):** stopping/cancelling an instance with a parked track terminates promptly (closed
  `evtCh` wakes it) — no hang.
- **T-6 (NFR-2):** a message whose correlation mismatches leaves the receiver waiting (re-parked),
  a matching one advances it; association still happens on the track goroutine.
- **T-7 (regression):** `make ci` green; `TestComplexAbortInstance` and the prior poll-wait flakes
  pass under `-race`.

## 8. Cross-doc

- **Implements** [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) §2 Rule 1 — upward, versioned.
- **Preserves** [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) §2.4 (no-catcher no-op),
  [ADR-014 v.1](../design/ADR-014-message-handling.md) / [ADR-016 v.1](../design/ADR-016-message-correlation.md)
  (two-tier correlation, unchanged).
- **Supersedes the mechanism of** FIX-007 (the `eventMu` + post-guard re-read) — the inbound slice
  removes the guard's reason to exist; FIX-007 remains a frozen historical record (not edited).
- **Paired slice:** SRD-028 (outbound, loop-owned positions) — the ADR-017 second slice; **not** in
  this change-set.

## 9. Definition of Done

- [ ] FR-1…FR-7 wired and demonstrated by §6 tests.
- [ ] `eventMu` and the `runtime.Gosched` busy-spin removed; no new mutex added.
- [ ] `make ci` green (tidy → lint → build → `-race` tests → diff-coverage ≥ 95% on touched
      functions → govulncheck), across modules.
- [ ] Runnable examples smoke-run (not just build) exit 0 (FIX-002 discipline).
- [ ] §8 cross-doc pins consistent (up/sideways only; every referenced doc exists at its pin).
- [ ] §10 implementation summary filled (files/lines, V-results, milestone SHAs). ADR-017 stays
      **Draft** until SRD-028 also lands (per the conception's two-slice rollout).

## 10. Implementation summary

_(placeholder — filled at landing: §10.1 stages by commit · §10.2 deltas vs draft · §10.3 V-results)_

## Open questions

None.
