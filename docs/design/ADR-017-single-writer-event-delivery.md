# ADR-017 — Single-writer event delivery

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-21 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) |

> **Draft (conception; implementation deferred).** Decides that a fired event must be
> **delivered through the per-instance single writer** (the instance loop), not applied to a
> track directly on the EventHub waiter's goroutine. This extends ADR-001 v.5's single-writer
> principle to event delivery and eliminates — by construction — the class of races where a
> track is mutated by a goroutine other than its owner. The queue/response mechanics are
> deferred to the implementing SRD; this ADR fixes the **model and direction**.

---

## 1. Context & problem

ADR-001 v.5 makes the per-instance **loop** the single owner of an instance's lifecycle
state: tracks never mutate that state directly — they **emit** events to the loop, which
applies them in order on one goroutine, so no lock guards lifecycle state. That single-writer
discipline is what makes the engine's concurrency tractable.

**Event delivery currently breaks that discipline.** When a waited event fires, the EventHub
delivers it **synchronously on the waiter's own goroutine**: the waiter calls into the target
track and mutates it (advancing it past its wait, transitioning its state, appending its next
step) — *while the track's own goroutine is concurrently reading and executing it*. A track is
thus mutated by a **foreign goroutine**, in violation of the single-writer model.

This is not one bug but a **class** of races — any place where the waiter-goroutine mutation
and the track's own run loop interleave. Two concrete instances have already been observed at
the **Event-Based gateway** (the BPMN deferred choice, where one gate subscription stands in
for several arms, and where a **signal broadcast** — unscoped, name-matched, fanning out to
every catcher in reach — can deliver to two arms near-simultaneously):

- **Two events race the deferred-choice transition** — both pass the "still waiting?" guard
  before either commits the winner, so both arms advance ("exactly one arm wins" is violated).
- **The run loop executes a stale position** — the waiter flips the track's state and appends
  the winning arm mid-iteration, so the run loop proceeds with the position it captured a
  moment earlier (the gate itself, which must never execute).

These have been patched **site by site** (a per-track event mutex; re-reading the position
after the wait-guard). Each patch is correct, but the *class* persists: every future
event-delivery site must independently get the interleaving right, and a missed site is a
latent, rare, hard-to-reproduce race. The root cause is structural — **delivery runs on the
wrong goroutine** — and is best removed structurally.

## 2. Decision

**Extend the single-writer principle to event delivery.** A fired event is **handed to the
instance's loop**, not applied to the track on the waiter's goroutine:

- The EventHub waiter, on a fired event, **enqueues** it for the target instance — onto a
  per-instance event queue drained by the loop, or the loop's existing event channel — and
  returns. It does **not** call into the track.
- The **loop dequeues and applies** the event to the track, serially, in the same single
  goroutine that already applies track lifecycle events (ADR-001 v.5).

A track is then mutated **only by its owner** (the loop, or the track's own run path acting
under the loop's coordination). The entire foreign-goroutine race class is eliminated **by
construction**, not site by site: per-site event mutexes and defensive re-reads become
unnecessary, because no two goroutines ever touch a track's state concurrently.

This is the same move ADR-001 v.5 already made for lifecycle state ("tracks emit, the loop
applies"), now applied to the one path that still bypassed it.

## 3. Consequences

- **Delivery becomes asynchronous — the principal cost.** Today a caller of "propagate event"
  gets the **outcome inline**: a broadcast with no live catcher is a no-op (ADR-006 v.1 §2.4);
  a correlation mismatch is reported back so the broker keeps or drops the message; an
  Event-Based gateway routes the winning arm immediately and the caller observes it. A queue
  **decouples delivery from outcome**, so the contract needs an explicit **response/ack
  mechanism** (a per-event completion signal the waiter can wait on) — or a deliberate
  fire-and-forget model where outcomes are observed via the instance's state/observers rather
  than the call return. Reconciling this with the existing synchronous expectations is the
  main design work the implementing SRD must do.
- **Per-instance queue policy.** Event **ordering** (FIFO per instance) and **backpressure**
  (a bounded queue, and what happens when full — block the waiter, drop, or grow) must be
  decided. The queue shares the instance's lifetime and must be drained on **shutdown** so no
  fired event is silently lost.
- **BPMN delivery semantics must be preserved** across the async boundary: a signal broadcast
  still reaches every in-reach catcher; a no-catcher broadcast is still a benign no-op
  (ADR-006 v.1); correlation rejection still leaves a receiver waiting. The async path must
  not change *what* is delivered, only *which goroutine* applies it.
- **Net simplification once landed.** The site-by-site concurrency guards added to event
  delivery can be removed; the engine gains one place to reason about event ordering and
  delivery, consistent with the rest of the single-writer model.

## 4. Alternatives considered

- **Per-site locks / careful re-reads (the status quo + patches).** Guard each delivery site
  individually (a per-track event mutex; re-read positions after a guard). *Rejected as the
  end state:* correct per site but does not generalize — lock proliferation, and every new
  delivery site must re-derive the correct interleaving; a missed site is a silent race. Useful
  as an interim safety measure, not as the model.
- **Synchronous waiter-goroutine delivery (today's model).** *Rejected:* it is precisely the
  model that admits the race class; no amount of local guarding makes "a foreign goroutine
  mutates the track" structurally safe.
- **One coarse instance-wide lock around all delivery and run.** Serialize event delivery and
  the run loop under a single instance lock. *Rejected:* serializes far more than necessary
  (kills track concurrency), and holding a lock across node execution invites deadlock — the
  same reasons ADR-001 v.5 chose the emit-to-loop model over a big lock in the first place.

## 5. Enterprise-readiness recommendations

- **Queue observability:** expose the per-instance event-queue depth and any drop/backpressure
  events as metrics, so operators can see delivery saturation before it becomes latency.
- **Foundation for durability/replay:** routing every fired event through one ordered,
  per-instance ingress point is the natural seam for **durable, replayable** event delivery
  later (persist the queue; replay on hydration) — a prerequisite for the deferred persistence
  workstream. The ADR does not require durability now, but the model should not preclude it.
- **Delivery contract documentation:** the implementing SRD should specify the new async
  contract (ack vs fire-and-forget, ordering, backpressure) as a first-class public behaviour,
  since it changes how hosts and the broker observe delivery outcomes.

## 6. References

- [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) — the single-writer principle
  (the loop owns lifecycle state; tracks emit, the loop applies) that this ADR extends to event
  delivery.
- [ADR-006 v.1 Events & subscriptions](ADR-006-events-and-subscriptions.md) — the event
  delivery contract (broadcast, no-catcher no-op, waiter lifecycle) whose *semantics* must be
  preserved across the new async boundary.
- BPMN 2.0 — signal publication is unscoped within reach and carries no correlation
  (`docs/bpmn-spec/semantics/event-handling.md`); the async path must keep this broadcast
  semantic intact.

## 7. Open questions

- **None at the model level** — single-writer event delivery is the decision. The shape of the
  **async delivery contract** (a per-event response/ack the waiter awaits, vs. fire-and-forget
  with outcomes observed via the instance) and the **queue backpressure policy** are deferred
  to the implementing SRD, where they will be decided against the concrete waiter→loop→track
  flow rather than assumed here.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 (Draft) | 2026-06-21 | Ruslan Gabitov | Draft conception: deliver fired events through the per-instance single writer (the loop) instead of mutating a track on the EventHub waiter's goroutine, eliminating the foreign-goroutine event-delivery race class by construction. Refines ADR-001 v.5; preserves ADR-006 v.1 delivery semantics. Implementation (queue + async contract + backpressure) deferred to a follow-up SRD. |
