# ADR-006 — Events & Subscriptions

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-07 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) |

> **Draft — partly decided.** Home for the event-delivery and
> event-triggered-cancellation conception relocated out of ADR-001 (which scopes
> itself to the built runtime core). The **delivery contract (§2.4)** and the
> **waiter lifecycle (§2.5)** are now **decided** — they remediate audit 2.4/2.5
> and are depended on by ADR-013's `Shutdown` and ADR-014's message waiter. The
> external-signal routing / terminate / wait-node parts (§2.1–§2.3) remain the
> relocated seed, authored in full when the events workstream lands (its own
> SRD + code in the same branch).

## 1. Context

### 1.1 What ADR-001 left to this ADR

ADR-001 defines the runtime core and a **generic** `context` cancellation
cascade (Engine → Instance → track). It does **not** define how external event
arrivals (Message / Timer / Signal) reach a running instance, nor the
BPMN-specific nodes that *trigger* cancellation. The runtime today has **no
external-signal channel** and **no Terminate End Event / boundary-event**
handling: `checkNodeType` registers an event-node's definitions and moves a
track to `TrackWaitForEvent`, but nothing delivers the trigger back or
interrupts a specific track on a boundary event. Those are §2.1–§2.3's scope.

### 1.2 Two delivery/lifecycle defects the audit flagged (now decided here)

The 2026-06-11 audit found the event machinery's **contract undefined** and its
**waiter ownership ambiguous**:

- **2.4 — delivery semantics undefined (MAJOR).** In practice it is
  *at-most-once*: propagating an event with no registered waiter is an **error**,
  there is no buffer, and an event published before its subscriber registers is
  **lost** — while consumers (tracks resuming from a wait) assume guaranteed
  delivery. The contract was never stated, so the behaviour is accidental.
- **2.5 — waiter lifecycle unclosed (MAJOR).** Ownership is decided two ways at
  once: a waiter removes *itself* on trigger **and** the hub removes it on
  unregister — a double-removal race. There is no synchronization of waiter
  goroutines at shutdown (no `WaitGroup`), and a failed `Stop()` leaves the
  waiter in the registry with a live goroutine (a leak).

These must be decided now: **ADR-013's `Thresher.Shutdown(ctx)`** needs a defined
waiter-shutdown, and **ADR-014's `MessageWaiter`** is a new waiter that must obey
one delivery contract and one ownership model. §2.4/§2.5 settle them.

## 2. Decision

### 2.1 External-signal routing (relocated from ADR-001 §4.1 / §4.3) — *seed*

The Instance receives **external signals** from `EventHub` (Message / Timer /
Signal arrival) on a dedicated channel and, in its event loop, routes each to
the right track or **spawns a fresh one** for a waiting subscription:

```go
type Instance struct {
    // ...
    external chan ExternalSignal // EventHub -> Instance
}

// in loop():
case sig := <-i.external:
    i.applyExternalSignal(sig) // spawn/continue a track for the subscription
```

This is the second inbound edge of the event loop (the first is the
track→Instance `events` channel that the runtime already has). It stays a single
serialized owner of instance state — external signals are applied in the same
loop goroutine, no locks.

### 2.2 Terminate End Event & boundary interruption (relocated from ADR-001 §4.6) — *seed*

ADR-001 owns the generic cascade; this ADR owns the BPMN nodes that *trigger* it:

- **Terminate End Event** → the Instance cancels its context → all tracks
  observe `Done()` → exit as `TrackCanceled` → instance reaches `Terminated`
  (the runtime cascade ADR-001 already verifies via `ctx.Done()`).
- **Interrupting boundary event** on activity X → the Instance cancels **only**
  the track executing X (not the whole instance).

### 2.3 Wait nodes — *seed*

Intermediate catch events, ReceiveTask, and Timer nodes move their track to
`TrackWaitForEvent` and register a subscription. The **in-memory wait-release**
model (goroutine ends, fresh track spawned on trigger) is specified in
[ADR-007 v.1 In-Memory Long Waits](ADR-007-in-memory-long-waits.md), which builds
on the external-signal delivery defined here (§2.1).

### 2.4 Delivery contract: in-memory, subscribe-before-publish, non-durable (decides audit 2.4)

The `EventHub` is an **in-memory, non-durable** dispatcher with an explicit
contract, replacing the accidental at-most-once behaviour:

- **Subscribe-before-publish.** A waiter must be registered **before** the event
  it awaits is propagated. The engine guarantees this for every case where a
  consumer *must* receive: a timer / intermediate-catch waiter is registered when
  its track reaches the wait, and a **boundary / error / escalation** handler is
  registered for the **whole lifetime of the activity it guards** — so a targeted
  internal event always finds its waiter already present.
- **No waiter ⇒ no-op, not an error.** Propagating an event no one is waiting for
  is a **logged no-op** (debug), never an error. This is *correct* BPMN **signal**
  broadcast semantics (a signal thrown with no live catcher is simply not caught)
  and harmless for any other kind. (Removes the "error if no waiter" defect.)
- **Messages are buffered by the broker, not the hub.** An external **message**
  arriving before its `ReceiveTask` / catch subscribes is held in the
  `MessageBroker`'s inbox and delivered on subscribe ([ADR-014 v.1](ADR-014-message-handling.md)).
  So the one case that genuinely needs pre-subscribe buffering is the broker's
  job; the hub stays a live dispatcher, not a store. The hub never duplicates the
  broker's buffer.
- **Not a durable bus.** The hub does not persist or replay events; durability
  and replay across restart are the Persistence ADR's concern. In-memory delivery
  is the conformance target's model (single-process).

This makes the previously-accidental behaviour a **stated contract**: guaranteed
to present waiters, broker-buffered for messages, broadcast-to-current-listeners
for signals, and an explicit non-goal for durability.

### 2.5 Waiter lifecycle: the EventHub is the sole owner (decides audit 2.5)

One owner, one shutdown path:

- **The hub owns every waiter's lifecycle** — it creates it, starts its
  goroutine, stops it, and removes it from the registry. A waiter **never removes
  itself**; on trigger/completion it signals the hub (or returns) and the **hub**
  does the removal. This eliminates the double-removal race (self-delete vs
  hub-delete).
- **Shutdown is synchronized.** The hub tracks waiter goroutines with a
  `sync.WaitGroup`; `Shutdown(ctx)` (the public contract in ADR-013 §2.5) stops
  every waiter and **waits for their goroutines to exit**, bounded by `ctx`. No
  waiter goroutine outlives the hub.
- **A failed `Stop()` still cleans up.** If a waiter's `Stop()` errors, the hub
  **still removes it from the registry and ensures its goroutine terminates** —
  the error is logged, never swallowed-with-a-leak.
- **One mutex-guarded registry.** Register / unregister / propagate are atomic
  with respect to the registry (consistent with FIX-003's single-lock fix), so a
  trigger racing an unregister cannot observe a half-removed waiter.

This single-ownership model is what `TimerWaiter`, ADR-014's `MessageWaiter`, and
any future waiter obey, and it is the mechanics ADR-013's `Thresher.Shutdown`
drives.

## 3. Consequences

- **Event behaviour is a contract, not an accident.** Callers know: a present
  waiter is guaranteed delivery; a thrown-into-the-void signal is a no-op;
  messages are broker-buffered; nothing is durable. The "lost event" ambiguity is
  gone.
- **`Shutdown` becomes possible and deterministic.** ADR-013's graceful stop has
  a defined waiter-shutdown to call (stop-all + `WaitGroup` wait); no leaked
  goroutines, no double-free.
- **ADR-014's message waiter slots in cleanly.** It is a waiter under §2.5
  ownership and rides §2.4's contract, with pre-subscribe messages covered by the
  broker — no special path.
- **No durable delivery (by design).** A process waiting on an event across an
  engine restart is not resumed until the Persistence ADR; documented as a
  deliberate boundary, not a silent gap.
- **The seed (§2.1–§2.3) is unchanged** and still awaits the events workstream;
  this expansion decided only the delivery contract and the waiter lifecycle the
  audit and the dependent ADRs forced.

## 4. Alternatives considered

- **Buffer all pending events in the hub (durable-ish in-memory queue).** Deliver
  to late subscribers generally. Rejected: unbounded-memory / staleness risk, and
  it conflates two needs — *messages* (which legitimately pre-arrive) are already
  the broker's job (ADR-014), while *signals* pre-arriving and being "caught late"
  would violate BPMN broadcast semantics. Targeted buffering at the broker beats a
  general hub buffer.
- **Strict subscribe-before-publish with no broker buffering** (even messages must
  subscribe first). Rejected: a message genuinely can arrive before its
  `ReceiveTask` is reached; forbidding that makes correct collaborations
  unrunnable. The broker's bounded inbox is the right place for that one case.
- **Make "no waiter" an error (keep current).** Rejected: it is wrong for signal
  broadcast (no listener is normal) and turns a benign condition into a failure;
  a logged no-op is correct.
- **Per-waiter self-ownership (waiter removes itself, no central owner).** The
  current half-state. Rejected: it is exactly the double-removal race and gives no
  place to synchronize shutdown. Single hub ownership is the fix.
- **A durable/persistent event bus now.** Rejected for this phase: durability is
  the Persistence ADR's; the conformance target is single-process in-memory.

## 5. Enterprise-readiness recommendations

Advisory, not gating — for the implementing SRD(s):

- **Log a dropped/no-waiter propagation** at debug with the event kind + id, so an
  operator can tell a deliberate broadcast-miss from a modelling mistake.
- **Bound and observe the broker inbox** (ADR-014) — the one buffer in the path;
  surface its depth/drops via the metrics extension (ADR-002).
- **Make `Shutdown` waiter-drain ctx-bounded and report stragglers** — if a waiter
  goroutine doesn't exit within the deadline, log which, don't hang.
- **Emit waiter register/trigger/remove onto the lifecycle channel** (ADR-013) so
  waiting and resuming are observable, not just logged.

## 6. Open questions

- None for the decided scope. The delivery contract (§2.4 — subscribe-before-
  publish, no-waiter no-op, broker-buffered messages, non-durable) and the waiter
  lifecycle (§2.5 — sole hub ownership, `WaitGroup` shutdown, no leak on `Stop`
  error) are settled. The seed §2.1–§2.3 (the `ExternalSignal` shape, the
  terminate/boundary wiring, the wait-release spawn) is authored in full with the
  events workstream and its SRD — implementation conception, not a blocking open
  question here.

## 7. References

- [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) — the runtime core and
  the generic cancellation cascade this refines (event triggers feed it).
- [ADR-002 v.1 Extension Architecture](ADR-002-extension-architecture.md) — the
  Logger/Metrics extensions used to log/observe delivery (§5).
- [ADR-007 v.1 In-Memory Long Waits](ADR-007-in-memory-long-waits.md) —
  wait-release built on §2.1 delivery and §2.5 waiter lifecycle.
- [ADR-013 v.1 Instance Observability & Control](ADR-013-instance-observability.md)
  — its `Thresher.Shutdown(ctx)` consumes §2.5's waiter shutdown; its lifecycle
  channel can observe waiters (§5).
- [ADR-014 v.1 Message Handling](ADR-014-message-handling.md) — its `MessageWaiter`
  obeys §2.4/§2.5; the broker owns the pre-subscribe message buffering §2.4 defers
  to it.
- [docs/bpmn-spec/semantics/end-events.md](../bpmn-spec/semantics/end-events.md) —
  Terminate semantics; BPMN signal-broadcast semantics underpinning the no-waiter
  no-op (§2.4).
- Architecture audit 2026-06-11 (`docs/audit/architecture-audit-2026-06-11.md`) —
  findings 2.4 (delivery semantics) and 2.5 (waiter lifecycle) decided here;
  FIX-001/FIX-003 are the related event-subsystem precedents.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-07 | Ruslan Gabitov | Initial Draft seed — event-delivery & event-triggered-cancellation conception relocated from ADR-001 (external signals, Terminate End Event + boundary interruption, wait nodes). Expanded 2026-06-14 to **decide** the delivery contract (§2.4 — in-memory, subscribe-before-publish, no-waiter no-op, messages broker-buffered per ADR-014, non-durable; remediates audit 2.4) and the waiter lifecycle (§2.5 — the EventHub is the sole waiter owner, `WaitGroup`-synchronized shutdown, no self-removal, no leak on `Stop` error, single-lock registry; remediates audit 2.5), the pieces ADR-013's `Shutdown` and ADR-014's `MessageWaiter` depend on. §2.1–§2.3 remain the relocated seed for the events workstream. Refs refreshed to ADR-001 v.5; siblings ADR-007 v.1, ADR-013 v.1, ADR-014 v.1. Not yet implemented. |
