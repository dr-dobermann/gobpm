# ADR-007 — In-Memory Long Waits

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-07 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-001 v.3 Execution Model](ADR-001-execution-model.md) |

> **Draft — not yet implemented.** Home for the in-memory long-wait release
> model relocated out of ADR-001 (which scopes itself to the built runtime
> core). Authored in full when the long-wait workstream lands, with its own SRD
> and code in the same branch. The **durable** version (surviving a restart) is
> a separate concern owned by the Persistence & State ADR.

## 1. Context

ADR-001 requires that a long wait (UserTask, Timer, ReceiveTask waiting hours or
days) **must not hold a goroutine**. It does not specify the release mechanism —
that is this ADR's scope. Delivery of the trigger that ends a wait is defined in
[ADR-006 Events & Subscriptions](ADR-006-events-and-subscriptions.md) §2.1.

The runtime today moves a waiting track to `TrackWaitForEvent` but keeps its
goroutine; there is no subscription-register-and-release.

## 2. Decision (seed — to be expanded)

In memory, a long wait must not hold a goroutine (relocated from ADR-001 §4.7):

1. A track reaching a long-wait node registers a subscription with `EventHub`
   and emits a wait event; **its goroutine ends.**
2. The Instance records the pending subscription and keeps no goroutine for the
   wait.
3. When the trigger arrives (delivered per ADR-006 §2.1), the Instance spawns a
   **fresh track** at the wait node to continue.

## 3. Runtime invariants this preserves (from ADR-001 §4.7)

These are the invariants the future **Persistence & State ADR** must honor; this
ADR keeps the *in-memory* version, persistence keeps the durable one:

- A track's continuation state is fully described by its **position (node),
  track/step state, Scope data, and lineage** — there is no hidden state on a
  separate token object.
- A node with resumable in-flight state (timer position, correlation
  subscription, partial activity state) owns the shape of that state; it is
  reached through a **per-node state contract** (defined in the Persistence
  ADR), **not** by storing mutable state on the shared node definition (node
  definitions are shared across instances and tracks and MUST stay immutable).

## 4. References

- [ADR-001 v.3 Execution Model](ADR-001-execution-model.md) — the runtime this refines (§4.7 invariants).
- [ADR-006 v.1 Events & Subscriptions](ADR-006-events-and-subscriptions.md) — trigger delivery this builds on.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-07 | Ruslan Gabitov | Initial Draft seed — in-memory long-wait release model relocated from ADR-001 v.3 §4.7. Durable version remains with the Persistence & State ADR. Not yet implemented. |
