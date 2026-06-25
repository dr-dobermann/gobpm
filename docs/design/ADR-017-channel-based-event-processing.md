# ADR-017 — Channel-based event processing (single-writer execution model)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-22 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) |

> **Draft (conception; implementation deferred to sliced SRDs).** Reworks the event-processing
> subsystem (EPS) onto a **Go-native, channel-based** model with two rules: a waiting track
> receives events by **parking on a channel** (the producer *sends*, never calls into the track),
> and a track **never exposes its mutable state for others to read** — it emits state changes to
> the per-instance loop, which is the **single owner** of the shared view (token positions, join
> state). This extends ADR-001 v.5's single-writer principle to **both** event delivery and
> cross-goroutine state reads, eliminating — by construction — the race class that has been
> patched site by site. The channel/queue mechanics, backpressure, and teardown are deferred to
> the accompanying SRD slices; this ADR fixes the **model and direction**.

---

## 1. Context & problem

ADR-001 v.5 makes the per-instance **loop** the single owner of an instance's lifecycle state:
tracks never mutate that state directly — they **emit** events to the loop, which applies them in
order on one goroutine, so no lock guards lifecycle state. That single-writer discipline is what
makes the engine's concurrency tractable.

**The event-processing subsystem bypasses that discipline**, on both sides of the track boundary:

- **Inbound — synchronous foreign-goroutine delivery.** `EventConsumer` is an interface the track
  implements, and `EventProducer` **synchronously calls the track's `ProcessEvent` on the
  producer's own goroutine** — mutating the track (advancing it past its wait, transitioning its
  state, appending its next step) *while the track's own goroutine is concurrently reading and
  executing it*. The waiting track meanwhile **busy-spins** ("am I still waiting? loop again")
  until the event lands, burning CPU, and track-status access is then guarded by **mutexes** to
  paper over the two goroutines.
- **Outbound — cross-goroutine state reads.** The loop (and joins) **read a track's positions
  directly** while the track's goroutine advances them, so the loop observes another goroutine's
  half-settled state.

This is not the Go way — *"don't communicate by sharing memory; share memory by communicating"* —
and it violates ADR-001 v.5's model. The cost is a **recurring race class**, not isolated bugs.
Documented failure modes, at the conceptual level:

- A **waiter goroutine mutating a track while its own run loop reads it** — the concurrent-fire /
  deferred-choice double-win at the Event-Based gateway (two events both pass the "still waiting?"
  guard; the run loop executes a position the waiter moved out from under it).
- The **loop reading track positions while a track's goroutine advances them** — a satisfiable
  Complex / OR-join transiently read as unsatisfiable and spuriously aborted.

Each has been patched **site by site** — a per-track event mutex, a re-read of the position after
the wait-guard, a `runtime.Gosched` to stop the busy-spin starving the loop, a one-shot positional
snapshot. Each patch is correct; the *class* persists, because **delivery and state-sharing run on
the wrong goroutines**. The root cause is structural and is best removed structurally.

## 2. Decision

**Extend the single-writer principle across the whole EPS** with two rules — both instances of
*communicate, don't share*:

### Rule 1 — Inbound (events → track): channel-park

A waiting track exposes a **(buffered) channel** and parks in a blocking
`select { case <-ctx.Done(): … case ev := <-evtCh: … }`. `EventProducer` **sends** the event on
the channel and returns — it **never calls into the track**. One channel is simultaneously the
per-track **event queue**, the **parking primitive**, and the **asynchronous producer↔track
handoff**. No busy-spin (the scheduler parks a blocked goroutine at zero CPU), no event mutex (only
the track's own goroutine touches its state when it receives), no idle computation.

### Rule 2 — Outbound (track state → loop): the loop owns the shared view

A track **never exposes mutable state for others to read**. It **emits** its state changes —
position moves, lifecycle transitions — to the loop, and the **loop is the sole owner** of the
instance's authoritative shared state (token positions, join state) that reachability and joins
consult. No goroutine reads another goroutine's state; the loop reads only its own.

Together, **a track's state is touched by exactly one goroutine**, and everything cross-goroutine
is a channel send. This is the same move ADR-001 v.5 already made for lifecycle state ("tracks
emit, the loop applies"), now applied to the two EPS paths that still bypassed it.

## 3. Consequences

- **The race class is eliminated by construction.** No foreign-goroutine mutation (Rule 1); no
  cross-goroutine state reads (Rule 2). The per-track event mutex, the post-guard re-read, the
  `Gosched`, and the positional snapshot all become unnecessary — there is no longer a window for
  two goroutines to touch a track's state, so there is nothing to guard.
- **Deferred choice becomes free.** An Event-Based gateway parks on one channel; the **first event
  it receives wins atomically** (a channel receive is single-consumer), so "exactly one arm wins"
  cannot be violated. The only residual care is **teardown** — unsubscribing the losing arms after
  the pick so a buffered later event isn't mis-handled (deferred to §7).
- **Execution serializes per instance for shared state; concurrency lives between instances.** The
  loop applies events and owns shared state on one goroutine, so an instance's shared-state changes
  are serial. This is acceptable and conventional — BPMN parallel branches are *orchestration*, not
  CPU-bound work, and mature engines (Zeebe, Temporal, Camunda) run a workflow instance
  single-threaded; real parallelism is **across** instances.
- **BPMN delivery semantics are preserved across the async boundary** — the async path changes
  *which goroutine applies* delivery, never *what* is delivered:
  - **Signal broadcast stays unscoped and name-matched.** "Signal publication is unscoped within
    reach: Signals do NOT use correlation. Every catching Signal handler in reach … receives the
    Signal. Engines need a Signal-name → set-of-subscribers index"
    (`docs/bpmn-spec/semantics/event-handling.md:221`; publication is "broadcast within and across
    Pools, Processes, and diagrams", ibid. :15). A broadcast must still fan out to every in-reach
    catcher.
  - **A no-catcher broadcast stays a benign no-op** (ADR-006 v.1 §2.4: "No waiter ⇒ no-op, not an
    error" — a logged debug, never an error).
  - **Message correlation rejection still leaves the receiver waiting** (a publication whose
    correlation doesn't match is not delivered; `event-handling.md:220`).
- **Net simplification once landed.** The site-by-site EPS guards are removed; the engine gains one
  place to reason about event ordering and delivery, consistent with the rest of the single-writer
  model.

## 4. Alternatives considered

- **A — Channel-park inbound + loop-owned state outbound (chosen).** Removes the race class by
  construction, no lock, fixes both the delivery and the state-read surfaces in one coherent model;
  Go-native. Cost: a per-track channel and a loop-owned state view.
- **B — Per-site locks / defensive re-reads (the status quo + the patches).** Guard each site
  individually (a per-track event mutex; re-read positions after a guard; a snapshot). *Rejected as
  the end state:* correct per site but does not generalize — lock proliferation, and every new
  delivery/read site must independently re-derive the correct interleaving; a missed site is a
  silent, rare race. Useful only as interim safety.
- **C — One coarse instance-wide lock around all delivery and run.** Serialize delivery and the run
  loop under a single instance lock. *Rejected:* serializes far more than necessary (kills track
  concurrency) and holding a lock across node execution invites deadlock — the same reasons
  ADR-001 v.5 chose emit-to-loop over a big lock.
- **D — A single per-instance queue the loop drains (this ADR's own earlier framing).** *Refined,
  not discarded:* per-track channels + loop-owned state is the more Go-native realization of the
  same single-writer direction — the channel **is** the queue, the parking primitive, and the
  handoff in one, and Rule 2 covers the state-read surface a bare delivery queue did not. The
  "loop drains one queue" mechanism is superseded by this rework while its single-writer direction
  is kept.

## 5. Enterprise-readiness recommendations

- **Queue observability:** expose per-track inbound-channel depth and any backpressure/drop events
  as metrics, so operators see delivery saturation before it becomes latency.
- **Foundation for durability/replay:** routing every fired event through a per-track channel with
  a loop-owned ingress is the natural seam for **durable, replayable** delivery later (persist the
  queue; replay on hydration) — a prerequisite for the deferred persistence workstream. Not
  required now, but the model must not preclude it.
- **Delivery-contract documentation:** the implementing SRD slices should specify the new async
  contract (ordering, backpressure, teardown) as first-class public behaviour, since it changes how
  hosts and the broker observe delivery outcomes.

## 6. References

- [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) — the single-writer principle (the loop
  owns shared state; tracks emit, the loop applies) that this ADR extends to event delivery **and**
  cross-goroutine state reads.
- [ADR-006 v.1 Events & subscriptions](ADR-006-events-and-subscriptions.md) — the event-delivery
  contract (broadcast, no-catcher no-op §2.4, waiter lifecycle) whose *semantics* must be preserved
  across the new async boundary.
- BPMN 2.0 — `docs/bpmn-spec/semantics/event-handling.md`: signal publication is unscoped within
  reach and carries no correlation (§ at :221), publication is broadcast across Pools/Processes
  (:15), Message publication is correlation-matched (:220). The async path must keep these intact.

## 7. Open questions

Deferred to the accompanying SRD slices (decided against the concrete waiter→channel→track and
track→loop flows, not assumed here):

- **Buffering & backpressure.** Inbound channel buffer size and the full-buffer policy (block the
  producer / drop / grow), plus per-instance ordering guarantees.
- **Subscription teardown.** A track that ends or is cancelled must stop the producer from sending
  to a dead channel (the send-on-closed-channel trap) — likely solved by routing the send through
  the loop or a done-guarded select on the send side; also the Event-Based gateway's losing-arm
  unsubscribe after a pick.
- **Broadcast fan-out mechanics.** How the unscoped signal broadcast addresses every in-reach
  catcher's channel (the Signal-name → subscribers index of `event-handling.md:221`).
- **Sliced rollout.** The model lands as more than one SRD slice — an **inbound** slice
  (channel-park event delivery: removes the event mutex and the busy-spin, makes deferred choice
  free) and an **outbound** slice (loop-owned positions: removes the loop-reads-track-state race).
  The accompanying SRD slices will decide the exact cut and sequencing.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 (Draft) | 2026-06-22 | Ruslan Gabitov | Draft conception of the EPS concurrency model: a waiting track **parks on a channel** (the producer sends, never calls `ProcessEvent`) and **never exposes mutable state for others to read** (it emits changes; the loop owns the shared view of positions/joins). Extends ADR-001 v.5's single-writer principle to both event delivery and cross-goroutine state reads, eliminating the foreign-goroutine / cross-read race class by construction; preserves ADR-006 v.1 delivery semantics. Supersedes the earlier "loop drains one event queue" framing with per-track channels + loop-owned state. Mechanics (buffering, backpressure, teardown) and the sliced rollout deferred to the accompanying SRD slices. |
