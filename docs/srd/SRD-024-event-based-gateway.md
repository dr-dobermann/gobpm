# SRD-024 — Event-Based gateway (Exclusive mid-flow deferred choice)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-20 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-005 v.4 Gateways & Joins](../design/ADR-005-gateways-and-joins.md) §2.12 |

This SRD lands the **Exclusive mid-flow Event-Based gateway** decided in
[ADR-005 v.4](../design/ADR-005-gateways-and-joins.md) §2.12: a **deferred choice**
(WCP-16) realized as the **gate-as-router** — one gate track owns all its arms'
subscriptions and, on the first event, routes it into the winning arm and advances the
token onto that arm's path; the other subscriptions are dropped (no arm tokens, so no
withdrawal). Arms = Message/Timer/Signal catch events + Receive Tasks.

**Parallel is out of scope** — per the spec it is an instantiation construct (start-only,
no mid-flow Parallel; ADR-005 v.4 §2.12.3), so it lands with the **instantiator
follow-up SRD**, which also confirms its **barrier** semantics against the BPMN PDF. The
**instantiator** forms and **Conditional** arms are likewise out of scope (§9).

---

## 1. Background

ADR-005 v.4 §2.12 decides the Event-Based gateway; no implementation exists
(`pkg/model/gateways/` has Exclusive/Parallel/Inclusive/Complex, no event gateway).
The gateway is heavily event-coupled, but the machinery it needs already exists:

- **Catch-event wait / resume.** A track reaching an event node moves to
  `TrackWaitForEvent` and registers each definition (`internal/instance/track.go`
  `synchronize`/`run` — `RegisterEvent(t, eDef)` per definition); when the event fires
  the hub calls the track's `ProcessEvent`, which delegates to `node.ProcessEvent` to
  bind the payload, unregisters the node's definitions, and returns the track to
  `TrackReady` so `run()` resumes (`track.go` `ProcessEvent`). The Event-Based gateway
  reuses this **wholesale** — it only changes *who* owns the subscriptions (the gate,
  for all its arms) and adds a *routing* step (the fired definition belongs to an arm,
  not the gate's own node).
- **Subscription registry.** `internal/eventproc/eventhub` `RegisterEvent` /
  `UnregisterEvent` / `RemoveEventProcessor` — register a `eventproc.EventProcessor`
  (`pkg/eventproc/eventproc.go:18` `ProcessEvent(context.Context, flow.EventDefinition)
  error`) for a definition; unregister drops it; the hub is the sole waiter owner
  ([ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) §2.5).
- **Arm access.** A gate reaches its arms structurally: `flow.Node.Outgoing()`
  (`pkg/model/flow/node.go:75`) → each `*SequenceFlow.Target()`
  (`sequenceflow.go:290`) is an arm node; an arm that is an `flow.EventNode`
  (`flow/events.go:82`) exposes `Definitions() []EventDefinition` (the events to
  subscribe), and implements `eventproc.EventProcessor` to bind them.
- **Gateway + validation patterns.** `gateways.New(opts)` + per-type wrapper
  (`gateways/exclusive.go` `NewExclusiveGateway`); per-node registration validation via
  the `interface{ Validate() error }` hook `Process.Validate` runs
  (`pkg/model/process/process.go:238`, added by SRD-023).

**The gap.** No `EventBasedGateway`; no gate that subscribes to *several* arms and
routes by first-fire; the `TokenWithdrawn` token-state (`internal/instance/token.go`)
is a **mis-model** to retire (ADR-005 v.4 §2.12.1 — there are no arm tokens).

---

## 2. Requirements

### Functional

- **FR-1 — `EventBasedGateway` model type.** New `pkg/model/gateways/event_based.go`
  `EventBasedGateway` embedding `Gateway` (mirrors `ExclusiveGateway`,
  `exclusive.go:15`), **diverging**, **Exclusive** (the only mid-flow configuration —
  ADR-005 v.4 §2.12.3; Parallel is start-only, deferred). `Clone()` (fresh per-instance
  arm state, ADR-009), `Node()`.
- **FR-2 — the gate owns all arm subscriptions.** The gate reuses the existing
  wait-registration path (`track.go:330–349`, which gates on
  `node.(eventproc.EventProcessor)` and loops the node's `Definitions()`): the
  `EventBasedGateway` implements `flow.EventNode` with `Definitions()` returning the
  **union of its arms' definitions** (gathered from `Outgoing()[i].Target()` — each an
  `flow.EventNode` or Receive Task), so when the gate's token arrives the gate track
  moves to `TrackWaitForEvent` and registers all of them with the **gate track** as the
  `eventproc.EventProcessor` — unchanged registration code. No token is produced on any
  arm (ADR-005 v.4 §2.12.1).
- **FR-3 — route on fire.** On `ProcessEvent(ctx, eDef)` the gate resolves
  `eDef → winning arm node`, delegates to that arm's `ProcessEvent(ctx, eDef)` (the arm
  binds its own payload — message/item), advances the gate track's **step to the arm
  node**, and returns it to `TrackReady` so `run()` resumes into the arm (already
  satisfied — it does not re-wait) and on to the arm's continuation.
- **FR-4 — Exclusive policy (first fire wins).** The first fire wins: emit one token
  onto the winning arm's path and `UnregisterEvent` every other arm's definitions. The
  decision is **loop-owned** (the loop serializes fires; first processed wins) — no
  track-side race (NFR-2).
- **FR-5 — no withdrawal; retire `TokenWithdrawn`.** Losing arms never received a token,
  so the gate only drops their subscriptions; the `TokenWithdrawn` token-state is
  removed (`internal/instance/token.go:25,28` + its `String()`/range guard at `:43,:53`)
  along with its projection (`internal/instance/observer_test.go:23`) (ADR-005 v.4
  §2.12.1).
- **FR-6 — validation (registration).** `EventBasedGateway` implements
  `Validate() error` (the `Process.Validate` per-node hook, `process.go:238`), checking
  against its now-linked flows (ADR-005 v.4 §2.12.5): (a) **≥2 outgoing arms**;
  (b) every arm is an intermediate **Message/Timer/Signal catch event or a Receive
  Task**; (c) each arm has **exactly one incoming flow** (this gate); (d) **no
  `conditionExpression`** on the gate's outgoing flows; (e) **no boundary events on a
  Receive-Task arm**; (f) by **default no mixing** of catch events and Receive Tasks
  (FR-7).
- **FR-7 — mixing parametrized.** `WithMixedArms()` opts into mixed arm families; the
  **default rejects** mixed arms (ADR-005 v.4 §2.12.5 Engine note — standard default,
  composition opt-in).
- **FR-8 — per-instance arm state.** The gate's armed/fired bookkeeping (which arm won,
  to keep the fire idempotent and the unsubscribe correct) is **per-node, per-instance**,
  created fresh by `Clone()` (ADR-009) and mutated under the gateway's own mutex (NFR-2).

### Non-functional

- **NFR-1 — reuse, don't rebuild.** No new event subsystem: registration, delivery,
  payload binding, and resume are the existing `RegisterEvent`/`UnregisterEvent`/
  `ProcessEvent`/`TrackWaitForEvent` path; the gate adds only multi-arm subscription +
  routing.
- **NFR-2 — loop-owned race.** The fire/withdraw/complete decision runs on the instance
  loop (single writer of track state), as with the synchronizing joins
  (ADR-005 §2.4/§2.10/§2.11); a track goroutine never decides the race. Verified under
  `-race`.
- **NFR-3 — per-instance subscription identity.** Point-to-point arm definitions
  (Message/Timer) are cloned per instance (`CloneForInstance`) so two instances racing
  the same gate don't cross-fire; signals stay broadcast (ADR-006 §2.1).
- **NFR-4 — Parallel/OR/Complex untouched.** The other gateways keep their contracts;
  the event-gateway path is additive.
- **NFR-5 — coverage.** Touched files finish ≥95% diff-coverage (`make ci`
  `cover-check`), aim 100%.

---

## 3. Models

### 3.1 `EventBasedGateway` (`pkg/model/gateways/event_based.go`)

```go
// EventBasedGateway is a diverging Exclusive deferred choice: it subscribes to all its
// arms' events and routes by which fires first; the other subscriptions are dropped. The
// gate owns the wait — no token ever sits on an arm (ADR-005 v.4 §2.12). Parallel is a
// start-only instantiation construct and is out of this SRD's scope (§2.12.3).
type EventBasedGateway struct {
	Gateway
	allowMixed bool
}
```

Per-instance arm state ("has the gate fired / which arm won") is held in the runtime
layer keyed by the cloned gate (ADR-009), not in the model struct — the model carries
only the static `allowMixed` policy.

### 3.2 Constructor + options

```go
// NewEventBasedGateway builds a diverging Exclusive Event-Based gateway; the default
// rejects mixed arm families (WithMixedArms relaxes it).
func NewEventBasedGateway(opts ...options.Option) (*EventBasedGateway, error)

func WithMixedArms() options.Option // opt-in: mixed catch+receive arms (default: rejected)
```

(`WithDirection(Diverging)` is inherited from `Gateway`; a converging Event-Based
gateway is not a BPMN shape. `EventGatewayType`/`WithEventGatewayType` arrive with the
Parallel instantiator SRD.)

---

## 4. Analysis

### 4.1 The gate as a routing `EventProcessor` (model vs runtime split)

Today `track.ProcessEvent` (runtime) assumes the fired definition belongs to the
track's **current** node and calls `node.ProcessEvent` to bind. For the gate the
current node is the **gateway** but the fired definition belongs to one of its **arm**
nodes — so the work splits across the two layers:

- **Model layer (`EventBasedGateway`).** `Definitions()` returns the arms' union (FR-3,
  for registration); `ProcessEvent(eDef)` **resolves the owning arm** (scan
  `Outgoing()[i].Target()` arms' `Definitions()`) and **delegates to that arm's
  `ProcessEvent(eDef)`** so the arm binds its own payload. The model node has no track
  and cannot touch runtime state.
- **Runtime layer (`track.ProcessEvent`, extended).** When the current node is an
  `EventBasedGateway`, after the model routes the binding it **advances the track's step
  to the resolved arm**, `UnregisterEvent`s the other arms' definitions, and returns to
  `TrackReady` (§4.2).

The resolve + step-advance are the only new logic; binding/resume/unregister are reused.

### 4.2 After the fire — first wins, drop the rest

On the first fire the loop advances the single token onto the winning arm (§4.1) and
`UnregisterEvent`s every other arm's definitions; the per-instance arm state records
that the gate has fired, so a sibling event that was in-flight when its subscription was
dropped is a no-op. One token in, one out; no arm tokens, no withdrawal (FR-5).

### 4.3 Why the race is loop-owned

A fire enters through the hub → the gate track's `ProcessEvent`. To keep the
first-wins decision and the sibling-unsubscribe free of track-goroutine races (the same
hazard the OR-join/Complex hit, ADR-005 §2.4/§2.10/§2.11), the *decision* is made on the
instance loop: `ProcessEvent` records the fire and signals the loop, which performs the
route + (Exclusive) unsubscribe + step-advance as the single writer of track state.

### 4.4 Validation placement

`count`/structure checks are knowable only after linking, so they run at registration
via the per-node `Validate()` hook (`process.go:238`). The gate inspects its
`Outgoing()` arms (their node types, each arm's `Incoming()` count, the absence of a
`conditionExpression`, Receive-Task boundary events, and arm-family mixing under
`allowMixed`).

### 4.5 Retiring `TokenWithdrawn`

`internal/instance/token.go`'s reserved `TokenWithdrawn` was a placeholder for a
race-loser producer that, per ADR-005 v.4 §2.12.1, does not exist (no arm tokens). It
and any reference are removed; race-losers are pure subscription drops.

---

## 5. Test scenarios (§6)

| # | Test | Scenario | Asserts |
|---|---|---|---|
| 1 | `TestEventGatewayExclusiveFirstWins` | gate → {message arm, timer arm}; fire the message | message arm's path runs, timer arm dropped, instance completes once |
| 2 | `TestEventGatewayExclusiveTimerWins` | same; let the timer fire first | timer path runs, message arm unsubscribed |
| 3 | `TestEventGatewayReceiveTaskArm` | gate → receive-task arm + signal arm | receive-task path runs on its message |
| 4 | `TestEventGatewayRace` (`-race`) | concurrent fires on two arms | exactly one path runs, no race, no double |
| 5 | `TestEventBasedGatewayValidate` | <2 arms / non-arm node / arm with 2 incoming / conditioned arm flow / receive-arm boundary / mixed (default) | each rejected at registration; mixed accepted under `WithMixedArms` |
| 6 | model-unit | `NewEventBasedGateway`, `WithMixedArms`, `Clone` | construction + per-instance state |

In-package (`internal/instance`) tests cover the routing for per-package coverage
(cross-package thresher tests don't count — the SRD-022/023 lesson).

---

## 8. Cross-doc

- **Implements** [ADR-005 v.4](../design/ADR-005-gateways-and-joins.md) §2.12 (up).
- [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) §2.1/§2.5 — subscription
  delivery + sole-hub waiter lifecycle (sideways/up).
- [ADR-009 v.1](../design/ADR-009-per-instance-node-graph.md) — per-instance node state
  via `Clone` (up).
- [SRD-023 v.1](SRD-023-complex-gateway.md) — the per-node `Validate` hook this reuses
  (sideways).

No downward references; versions pinned.

---

## 9. Definition of Done

- FR-1…FR-8 wired; §5 tests exist and pass under `-race`.
- `make ci` green: lint, build, `-race`, diff-coverage ≥95% (aim 100%), govulncheck.
- `examples/` gains an event-based-gateway example (Exclusive deferred choice), smoke
  exit 0.
- ADR-005 v.4 NotebookLM mixing-ban check done before its Accepted flip.
- **Out of scope (deferred, ADR-005 v.4 §2.12.7):** the **Parallel** configuration
  (start-only — its **barrier** semantics to-verify against the BPMN PDF) and both
  **instantiators** (Exclusive-start, Parallel-start — born-from-event + correlation),
  all in a follow-up SRD; **Conditional** arms (need a conditional waiter); loop
  re-arming (engine-wide, §4).

## 10. Implementation summary

> ⚠️ TODO: fill AFTER landing — commits, key files, V-results, deltas vs this draft.

## Open questions

- **None.**
