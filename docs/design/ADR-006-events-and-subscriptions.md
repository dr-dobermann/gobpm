# ADR-006 — Events & Subscriptions

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-07 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-001 v.3 Execution Model](ADR-001-execution-model.md) |

> **Draft — not yet implemented.** Home for the event-delivery and
> event-triggered-cancellation conception relocated out of ADR-001 (which scopes
> itself to the built runtime core). Authored in full when the events workstream
> lands, with its own SRD and code in the same branch.

## 1. Context

ADR-001 defines the runtime core and a **generic** `context` cancellation
cascade (Engine → Instance → track). It does **not** define how external event
arrivals (Message / Timer / Signal) reach a running instance, nor the
BPMN-specific nodes that *trigger* cancellation. Those are this ADR's scope.

The runtime today has **no external-signal channel** and **no Terminate End
Event / boundary-event** handling: `checkNodeType` registers an event-node's
definitions and moves a track to `TrackWaitForEvent`, but nothing delivers the
trigger back or interrupts a specific track on a boundary event.

## 2. Decision (seed — to be expanded)

### 2.1 External-signal routing (relocated from ADR-001 §4.1 / §4.3)

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

### 2.2 Terminate End Event & boundary interruption (relocated from ADR-001 §4.6)

ADR-001 owns the generic cascade; this ADR owns the BPMN nodes that *trigger* it:

- **Terminate End Event** → the Instance cancels its context → all tracks
  observe `Done()` → exit as `TrackCanceled` → instance reaches `Terminated`
  (the runtime cascade ADR-001 §7 already verifies via `ctx.Done()`).
- **Interrupting boundary event** on activity X → the Instance cancels **only**
  the track executing X (not the whole instance).

### 2.3 Wait nodes

Intermediate catch events, ReceiveTask, and Timer nodes move their track to
`TrackWaitForEvent` and register a subscription. The **in-memory wait-release**
model (goroutine ends, fresh track spawned on trigger) is specified in
[ADR-007 In-Memory Long Waits](ADR-007-in-memory-long-waits.md), which builds on
the external-signal delivery defined here (§2.1).

## 3. References

- [ADR-001 v.3 Execution Model](ADR-001-execution-model.md) — the runtime this refines.
- [ADR-007 In-Memory Long Waits](ADR-007-in-memory-long-waits.md) — wait-release built on §2.1 delivery.
- [docs/bpmn-spec/semantics/end-events.md](../bpmn-spec/semantics/end-events.md) — Terminate semantics.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-07 | Ruslan Gabitov | Initial Draft seed — event-delivery & event-triggered-cancellation conception relocated from ADR-001 v.3 (§4.1/§4.3 external signals, §4.6 Terminate End Event + boundary interruption). Not yet implemented. |
