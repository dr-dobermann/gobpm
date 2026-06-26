# SRD-027 — Channel-park inbound event delivery (ADR-017 inbound slice)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-25 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-017 v.1 Channel-based event processing](../design/ADR-017-channel-based-event-processing.md) §2 Rule 1 (inbound slice) |

This SRD lands the **inbound slice** of ADR-017: a waiting track stops busy-spinning and **parks
on a per-track channel**; event producers stop mutating the track on a foreign goroutine and instead
**emit the fired event to the per-instance loop**, which is the **sole sender** to a track's channel.
The hub-facing `EventProcessor` is **per-trigger** (ADR-017 §2 Rule 1): the **Instance** for Message
(correlation is instance-owned, so the fine `validateAndAssociate` runs in the loop), the **track**
for Signal/Timer. It removes the `runtime.Gosched` busy-spin and the per-track `eventMu`, makes
deferred choice atomic at the loop, and replaces the O(n) signal-broadcast scan with a name index.
The **outbound slice** (the loop owning token positions — ADR-017 Rule 2) is **SRD-028, not here**.

---

## 1. Background & current state (verified against the code)

ADR-001 v.5 makes the per-instance **loop** the single writer of lifecycle state: tracks `emit`
`trackEvent`s on `inst.events` and `loop()` applies them on one goroutine (`internal/instance/instance.go:608,619`).
Event delivery bypasses that discipline on the inbound side:

- **Synchronous foreign-goroutine delivery.** `EventProducer` reaches a track through
  `track.ProcessEvent` (`internal/instance/track.go:870`), which **mutates the track on the
  producer's goroutine** — it reads `t.steps`, runs the node, advances the arm, and flips the state
  to `TrackReady`. Signal is the worst case: `PropagateEvent → broadcastSignal → w.Process →
  ProcessEvent` runs entirely on the thrower's goroutine (`internal/eventproc/eventhub/eventhub.go:419,498`).
- **Track registration leaks instance state.** Today the track is the hub-facing `EventProcessor`:
  `checkNodeType` calls `t.instance.RegisterEvent(t, d)` for **every** trigger (`track.go:371`), and
  `track.CorrelationKeys()` (`track.go:325-331`) exposes the **instance's** conversation keys for the
  keyed message subscription — instance state surfaced through the track only because the track is
  what is registered.
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
- **FR-2 — Producers emit, never mutate; the registered processor is per-trigger.** A producer's
  `eventproc.EventProcessor.ProcessEvent` no longer mutates a track — it **emits** an `evDeliver`
  `trackEvent` to the loop and returns. The processor the hub holds is chosen by trigger: the
  **track** for Signal/Timer (`track.ProcessEvent` emits `evDeliver{track, eDef}`), the **Instance**
  for Message (FR-8). No track state is read or written on the producer goroutine in either case.
- **FR-3 — The loop is the sole sender to `evtCh`.** Only `loop()` sends on a track's `evtCh`
  (dispatch) and only `loop()` closes it (teardown). No producer holds a track-channel reference.
- **FR-4 — Deferred choice is atomic at the loop.** The loop keeps a loop-local set of
  parked-and-undelivered tracks. A track entering the wait emits `evWaiting` (which adds it); the
  **first** matching `evDeliver` for that track removes it (the flip) and sends the event; every
  later `evDeliver` for it finds it absent and is **dropped** (the losing arm of an Event-Based
  gateway / a duplicate fire). This holds for a **mixed-trigger** gateway too (a message arm via the
  Instance, a timer arm via the track): both land at the same loop targeting the same track. The
  FIX-007 concurrent-fire double-win cannot occur, and `eventMu` is removed.
- **FR-5 — Park-before-register ordering, and index seeding.** A track emits `evWaiting` **before**
  it registers its waiters with the hub, so — because `inst.events` is FIFO and registration
  happens-before any matching `evDeliver` — the loop always records a track as parked before an event
  can target it. `evWaiting` carries the track's **message** catch-definition IDs, which the loop
  also enters into the FR-8 `msgEDef.ID() → track` index at the same point (and `spawn` seeds both
  for tracks that start parked at construction). No fired event is lost to a not-yet-known-parked
  track.
- **FR-6 — Signal broadcast uses a name index.** The hub maintains `signalName → []subscriber`
  built on register/unregister; `broadcastSignal` looks the name up and calls each subscriber track's
  `ProcessEvent` (→ emit to that track's loop), replacing the O(n) all-waiters scan. A broadcast with
  no catcher yields an empty lookup and is therefore a benign no-op (ADR-006 v.1 §2.4). Cross-instance
  fan-out is preserved (signal is unscoped within reach).
- **FR-7 — Stop wakes a parked track.** `loop()`/`stopAll` closing a track's `evtCh` wakes a track
  blocked in the FR-1 `select` (receive on a closed channel), which then cancels — the existing
  `stopIt` flag covers only the running path.
- **FR-8 — Message is registered at instance granularity (the hybrid boundary).** For a **Message**
  catch, the registered `eventproc.EventProcessor` is the **Instance**, not the track (Signal/Timer
  stay track-registered — FR-2). `Instance.ProcessEvent` emits `evDeliver{eDef}` (carrying **no**
  track) to the loop; the loop resolves the target track via the per-instance `msgEDef.ID() → track`
  index (FR-5) and runs the fine correlation gate (`validateAndAssociate`) **before** the flip — a
  mismatch **drops** the event and **leaves the track parked** (the receiver keeps waiting). On a
  match the loop flips and dispatches. `CorrelationKeys()` moves from `track` to `Instance` (the keyed
  broker subscription is instance-owned). Rationale: ADR-017 v.1 §2 Rule 1 + Engine note —
  correlation is the only instance-scoped matching state, and in BPMN it is Message-only
  (Correlation §8.4.2).

### Non-functional

- **NFR-1 — No new race.** `go test -race ./...` is clean; the per-track `eventMu` is removed
  because only the track's own goroutine touches its state on receive (delivery is single-consumer).
- **NFR-2 — Message semantics unchanged; the gate moves to the loop.** The two-tier correlation
  match is preserved in *what* it matches: the broker does the coarse name+key match (ADR-014/016),
  and `validateAndAssociate` (`instance.go:375`, `convMu`-guarded) does the fine match — now run in
  **`loop()`** when the Instance emits an inbound message (FR-8), not on the track goroutine. A
  mismatch leaves the track parked (it keeps waiting), never advances it; the verdict is identical,
  only the deciding goroutine changes.
- **NFR-3 — Deferred-choice / abort flakes cured (the real root cause).** Removing the busy-spin
  (FR-1) was necessary but **not sufficient**: the complex/OR-join `-race` flakes
  (`TestComplexRequiredGate`, `TestComplexAbortOnDeath`, `TestComplexAbortInstance`) had two
  distinct timing races in the loop's join recheck — a double-read of token positions and an abort
  that did not stop the loop deterministically (§3.8). Both are fixed; `pkg/thresher` under `-race`
  passes 40/40 (was ~1/6).
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
        if err := t.deliver(ctx, eDef); err != nil {
            t.updateState(TrackFailed); t.lastErr = err; return
        }
    }
```

`deliver` is the body lifted out of today's `ProcessEvent`, now **without** the correlation step
(the loop already gated it — FR-8) and **without** the `eventMu`/`!inState` foreign-goroutine guard
(the loop guarantees a single delivery to a parked track): node `ProcessEvent` → `unregisterEvent`
→ `advanceToArm` → `updateState(TrackReady)`.

### 3.2 The producer entry points — non-blocking emits (`internal/instance`)

**Signal/Timer — `track.ProcessEvent` (`track.go:870`):**

```go
// ProcessEvent (eventproc.EventProcessor) is called by a Signal/Timer producer goroutine. It no
// longer touches track state — it hands the event to the loop, which dispatches it to t.evtCh.
func (t *track) ProcessEvent(_ context.Context, eDef flow.EventDefinition) error {
    t.instance.emit(trackEvent{kind: evDeliver, track: t, eDef: eDef})
    return nil
}
```

**Message — `Instance.ProcessEvent` (new; `internal/instance/instance.go`):**

```go
// ProcessEvent (eventproc.EventProcessor) is the hub-facing entry for Message: the Instance is the
// registered processor (FR-8), because message correlation state is instance-owned. It emits the
// event to its own loop carrying NO track — the loop resolves the parked track via the msgEDef→track
// index and runs validateAndAssociate before dispatch.
func (inst *Instance) ProcessEvent(_ context.Context, eDef flow.EventDefinition) error {
    inst.emit(trackEvent{kind: evDeliver, eDef: eDef}) // track == nil ⇒ the message branch (§3.4)
    return nil
}

// CorrelationKeys moves here from track (track.go:325): the keyed broker subscription is
// instance-owned, so the Instance is what the message waiter type-asserts for its keys.
func (inst *Instance) CorrelationKeys() []string { /* the instance's conversation key values */ }
```

`Instance` thus satisfies `eventproc.EventProcessor` (`foundation.Identifyer` + `ProcessEvent`); the
instance-starter (`pkg/thresher/instance_starter.go`) already establishes the instance-granularity
processor pattern (ADR-015).

### 3.3 `trackEvent` — `eDef` field, `evDeliver` + `evWaiting` kinds (`internal/instance/event.go`)

`trackEvent.eDef flow.EventDefinition` (`event.go:8`) carries the fired definition; `evWaiting`
carries the track's message catch-definition IDs (FR-5). The two kinds (`event.go:52`) and their
`String()` arms:

```go
// evWaiting: the track entered TrackWaitForEvent (emitted BEFORE it registers its hub waiters,
// FR-5). The loop records it as parked-and-undelivered and indexes its message defs → track.
evWaiting
// evDeliver: a producer handed a fired event to the loop (FR-2). A track-carried evDeliver
// (Signal/Timer) targets ev.track directly; a track-less one (Message via Instance.ProcessEvent,
// FR-8) is resolved through the msgEDef→track index and correlation-gated. The loop dispatches to
// the track's evtCh iff it is parked-and-undelivered, else drops it (FR-4).
evDeliver
```

### 3.4 `loop()` — parked set, message index, gated dispatch, teardown (`internal/instance/instance.go:619`)

Two loop-local maps, loop-goroutine-only (no lock — like `active`/`stopping`):

```go
waiting := map[string]struct{}{}        // track.ID() ⟺ parked-and-undelivered
msgIdx  := map[string]*track{}          // waited message eDef.ID() → parked track (FR-5/FR-8)
```

New `switch` arms:

```go
case evWaiting:
    waiting[ev.track.ID()] = struct{}{}
    for _, id := range ev.msgDefIDs {       // message catch defs this track parks on
        msgIdx[id] = ev.track
    }

case evDeliver:
    tr := ev.track                          // Signal/Timer carry the track …
    if tr == nil {                          // … Message is resolved via the index (FR-8)
        tr = msgIdx[ev.eDef.ID()]
        if tr == nil {                       // no parked track for this message → drop
            break
        }
    }
    if _, parked := waiting[tr.ID()]; !parked {
        break                                // losing arm / already-delivered → drop (FR-4)
    }
    if ev.track == nil && inst.validateAndAssociate(ctx, ev.eDef) {
        break                                // correlation mismatch → drop, KEEP parked (FR-8/NFR-2)
    }
    flipNotParked(tr, waiting, msgIdx)       // remove from waiting + clear tr's msgIdx entries
    tr.evtCh <- ev.eDef                      // sole sender; buffered single slot
```

`flipNotParked` deletes `waiting[tr.ID()]` and every `msgIdx` entry pointing at `tr` (so a late
event to a losing message arm of the same track is dropped). `stopAll` closes each live track's
`evtCh` (FR-7) — safe because the loop is the sole sender — and clears both maps.
`evEnded`/`evFailed`/`evAwaiting` also flip the track out so a set/index entry never outlives its
track. `spawn` seeds `waiting` + `msgIdx` for any track that starts parked at construction (before
the loop drains `inst.events`, where an `evWaiting` emit would deadlock — FR-5).

### 3.5 Signal name index (`internal/eventproc/eventhub/eventhub.go`)

Add `signalIdx map[string][]eventproc.EventWaiter` maintained in `registerWaiter`/`UnregisterEvent`
(`eventhub.go:198,331`) for waiters whose definition is `TriggerSignal`. `broadcastSignal`
(`eventhub.go:465`) looks up by name instead of scanning `eh.waiters`; for each subscriber it routes
the event to the track's instance loop (via the existing `track.ProcessEvent → emit` path of §3.2 —
signal stays **track**-registered). No behavioural change to which catchers receive — only the lookup
cost and the delivery goroutine.

### 3.6 Fixed buffer depth — a constant, not an option (`internal/instance`)

```go
// eventBufferDepth is the per-track inbound event-channel capacity. One slot is exactly
// enough: the loop dispatches at most one event per parked episode (it flips the track out
// of the waiting set on first delivery, §3.4), and a single slot decouples the loop's
// send from the track's scheduling so the loop never blocks. Unbuffered would risk
// blocking the loop in the window between evWaiting and the track reaching its receive.
const eventBufferDepth = 1
```

No engine option and no `thresherConfig` field: the depth has exactly one correct value under
flip-on-dispatch, so a knob would be a setting nobody should turn. If a future need appears (e.g.
the deferred durability/replay work, ADR-017 §5), introduce the option then.

### 3.7 Per-trigger registration in `checkNodeType` (`internal/instance/track.go:335`)

The per-definition loop selects the processor by trigger type — the one place the hybrid boundary is
chosen:

```go
for _, d := range defs {
    proc := eventproc.EventProcessor(t)        // Signal/Timer: the track is the processor
    if d.Type() == flow.TriggerMessage {
        proc = t.instance                       // Message: the Instance owns correlation (FR-8)
    }
    if err := t.instance.RegisterEvent(proc, d); err != nil {
        return /* … wrapped … */
    }
}
```

`evWaiting` (emitted just above this loop, FR-5) carries the IDs of the `TriggerMessage` definitions
in `defs`, so the loop's `msgIdx` resolves a fired message back to this track. `track.CorrelationKeys`
is deleted (only the message path was keyed, and that is now the Instance's — §3.2).

### 3.8 Race-free join recheck (`internal/instance/reachability.go`, `instance.go`)

Removing the busy-spin let the loop's reachability/activation-join recheck (SRD-022 / SRD-023)
run at moments it previously never reached, exposing two pre-existing timing races that made the
complex/OR-join tests flake under `-race`:

- **Double-read of token positions.** `recheckJoin` sampled live positions **twice** — once for the
  in-transit guard (the old `hasInTransitArrival`), then again for reachability
  (`Recheck` → `CheckFlows` → `occupiedNodes`). A token slipping from a branch node (where it makes
  its join flow *reachable*) to the join node (*arrived-pending*, its incoming flow not yet marked)
  was read as "on the branch" by the guard (→ proceed) but "at the join" by reachability (→
  unreachable), so a required flow looked **neither arrived nor reachable** → a spurious
  *"complex gateway activation rule is unsatisfiable"* abort (and the symmetric *missed* abort).
  **Fix:** one snapshot. `joinPositions(node)` does a **single** pass over `inst.tracks` returning
  `(occupied, inTransit)` — each position read exactly once; `recheckJoin` defers on `inTransit`
  and passes a `fixedFlowChecker` bound to that same `occupied` set to `Recheck`, so the guard and
  the reachability can never disagree across two reads. `hasInTransitArrival` is removed;
  `occupiedNodes` delegates to `joinPositions(nil)`.
- **Abort did not stop the loop deterministically.** An activation-join abort called `inst.fail`,
  which only records `lastErr` and cancels the instance ctx, relying on the loop *then* selecting
  `<-ctx.Done()` → `stopAll`. But the cancel also wakes the parked tracks, whose `evEnded` events
  race the `<-done` case; if they drained `active` to 0 first (Go `select` picks randomly among
  ready cases), the loop exited with `stopping == false` → the instance reported **Completed**
  instead of **Terminated**. **Fix:** the abort (and guard-error) path calls `stopAll()` right after
  `inst.fail()` — matching the existing `failFromTrack` pattern; `stopAll` is threaded through
  `recheckParked` / `recheckAwaitingJoins` / `recheckJoin`.

## 4. Analysis

- **Path (chosen) — ADR-017 Rule 1 (Model Y, loop-dispatched, per-trigger boundary).** Producers
  emit to the loop; the loop is the sole sender. The hub boundary is the Instance for Message and the
  track otherwise. Rationale, the hybrid-vs-uniform decision (Alternative E), and the rejected
  alternatives (direct per-track channel; per-site locks; one coarse lock) live in **ADR-017 v.1 §2 +
  §4** — not repeated here.
- **Why the Instance for Message only.** Correlation is the sole reason to lift the hub boundary off
  the track, and it is a Message-only condition in BPMN (Correlation §8.4.2). Making the Instance the
  message processor puts the conversation keys, the keyed subscription, and the `validateAndAssociate`
  gate at one owner, and deletes the `track.CorrelationKeys` leak. Signal/Timer keep the simpler
  track registration — routing them through the Instance would force an internal re-fan-out for the
  broadcast with no matching benefit (ADR-017 §4 E).
- **Why the gate moves to the loop.** With `track.ProcessEvent` reduced to an emit, the fine match
  cannot stay a synchronous return on the producer goroutine. The loop — already the instance's
  single writer and the owner of `convMu`-guarded state — is the natural place to run it; a mismatch
  is a loop-local drop that keeps the track parked, with no track re-park hop.
- **Why `ProcessEvent` keeps its signature.** Both the track and the Instance implement the existing
  `eventproc.EventProcessor`; the hub/waiter call sites are unchanged. Only the *body* moves off the
  foreign goroutine and the *registered object* differs by trigger. Smallest blast radius.
- **Why loop-local maps, not track state read by the loop.** Reading track state from the loop is
  the cross-goroutine read Rule 2 forbids (SRD-028's subject). `waiting` and `msgIdx` are owned by
  the loop alone, so Slice 1 introduces no new cross-read.
- **Why park-before-register (FR-5).** It is the one ordering hazard: an event firing in the window
  between subscribe and park. Emitting `evWaiting` (with the message def IDs) before `RegisterEvent`
  puts the parked record and the index entry on `inst.events` ahead of any `evDeliver` the
  registration can cause (FIFO), closing the window without a lock.
- **Engine notes — deferred-choice drop is correct, not lossy.** Dropping an `evDeliver` for an
  absent (already-delivered / not-parked) track only ever discards a losing Event-Based-gateway arm
  or a duplicate fire of an already-consumed catch — never a trigger a parked track still needs. A
  correlation-mismatch drop (FR-8) is distinct: it keeps the track parked.
- **Out of scope (explicit).** The loop owning token positions / join state (ADR-017 Rule 2,
  outbound) → **SRD-028**. No change to *what* message correlation matches (ADR-014/016), timer
  scheduling, or the Event-Based gateway's arm resolution (`eventRouter`/`advanceToArm`).
- **Synchronous producer→loop binding (known, bounded).** Producers call `ProcessEvent` (the track's
  or the Instance's) synchronously, and `emit` is `select { events<-ev; <-loopDone }` — so the call is
  bounded by the instance's lifetime: no deadlock, no send-on-closed, a stale processor reference is a
  no-op. The only residual cost is signal-broadcast head-of-line latency (sequential fan-out on the
  thrower goroutine, bounded by the loop's drain rate); message is per-waiter-goroutine and
  unaffected. Full async decoupling is the deferred buffered-intake (below / ADR-017 v.1 §3, §5), on
  measured contention only — not this slice.
- **`inst.events` stays unbuffered (out of scope).** This slice adds `evDeliver`/`evWaiting` onto
  the existing per-instance intake but does **not** retune its capacity. `inst.events` is unbuffered
  by the ADR-001 single-writer backpressure contract (`emit` blocks on `select { events<-ev;
  <-loopDone }`), and the loop never blocks while draining it — flip-on-dispatch sends only to a
  parked-and-undelivered track, so `evtCh <- eDef` always lands in the free single slot. A
  configurable loop-intake buffer is a core-loop throughput knob, not an event-delivery concern;
  introduce it only with measured contention, in its own change.

## 5. Public API surface

- **No new public API.** The buffer depth is an unexported constant (§3.6), not an option. The
  per-trigger registration and the message index are internal (`internal/instance`).
- **Changed implementer (same interface):** `Instance` now implements `eventproc.EventProcessor`
  (`ProcessEvent` + `CorrelationKeys`) for the Message path; `track` keeps `ProcessEvent` for
  Signal/Timer and **loses** `CorrelationKeys`. Hosts that implement custom `EventProcessor`s see no
  signature change.
- **Changed semantics (same signature):** `eventproc.EventProcessor.ProcessEvent` returns once the
  event is **enqueued to the instance loop**, not once it is applied. Delivery and ordering
  (per-track FIFO via the channel) become part of the documented async contract (ADR-017 §5).

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
- **T-6 (FR-8, NFR-2):** a message whose correlation mismatches leaves the receiver waiting
  (kept parked by the **loop** gate — `Instance.ProcessEvent` → `evDeliver{eDef}` → loop drop), a
  matching one advances it; the gate runs in `loop()`, not on the track goroutine.
- **T-7 (regression):** `make ci` green; `TestComplexAbortInstance` and the prior poll-wait flakes
  pass under `-race`.
- **T-8 (FR-2/FR-8, hybrid):** a Message catch registers the **Instance** as the hub processor and a
  Signal catch registers the **track**; a **mixed-trigger** Event-Based gateway (message arm + timer
  arm) resolves both deliveries at the same loop/track and picks exactly one winner.
- **T-9 (NFR-3, §3.8 join recheck):** the complex/OR-join `-race` stress is stable —
  `pkg/thresher` under `-race` passes 40/40 (was ~1/6); `TestComplexRequiredGate` no longer
  false-aborts and `TestComplexAbortOnDeath` reaches **Terminated**, not **Completed**.

## 8. Cross-doc

- **Implements** [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) §2 Rule 1 — upward, versioned.
- **Preserves** [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) §2.4 (no-catcher no-op),
  [ADR-014 v.1](../design/ADR-014-message-handling.md) / [ADR-016 v.1](../design/ADR-016-message-correlation.md)
  (two-tier correlation — *what* it matches is unchanged; the fine step relocates to the loop and the
  Message processor becomes the Instance).
- **Supersedes the mechanism of** FIX-007 (the `eventMu` + post-guard re-read) — the inbound slice
  removes the guard's reason to exist; FIX-007 remains a frozen historical record (not edited).
- **Fixes the recheck of** [SRD-022](SRD-022-inclusive-or-join.md) (OR-join) /
  [SRD-023](SRD-023-complex-gateway.md) (complex gateway) — §3.8 makes their loop-side recheck
  race-free; *what* those joins decide is unchanged, only the racing reads are removed. Sideways
  (SRD→SRD); no version pin — SRD/FIX are single-shot.
- **Paired slice:** SRD-028 (outbound, loop-owned positions) — the ADR-017 second slice; **not** in
  this change-set.

## 9. Definition of Done

- [ ] FR-1…FR-8 wired and demonstrated by §6 tests.
- [ ] `eventMu` and the `runtime.Gosched` busy-spin removed; `track.CorrelationKeys` moved to
      `Instance`; no new mutex added.
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
