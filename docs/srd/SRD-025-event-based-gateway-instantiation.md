# SRD-025 — Event-Based gateway instantiation (Exclusive-start + Parallel-start)

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-21 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-005 v.4 Gateways & Joins](../design/ADR-005-gateways-and-joins.md) §2.12.3/§2.12.4 |

This SRD lands the **instantiating** Event-Based gateway decided in
[ADR-005 v.4](../design/ADR-005-gateways-and-joins.md) §2.12.4 — the deferred half of
the gateway (the mid-flow Exclusive form landed in [SRD-024](SRD-024-event-based-gateway.md)).
A gateway with **no incoming flow** and `instantiate=true` is a definition-level
**instantiator**:

- **Exclusive-start** (`eventGatewayType=Exclusive`, default): each matching event
  **creates a new instance** running from that arm; the gate does not wait for the other
  events (BPMN §13.2 / §10.6.6).
- **Parallel-start** (`eventGatewayType=Parallel`): the **first** event creates **one**
  instance; the gate's **other arms** re-arm as in-instance receivers **correlated to
  that instance**; each arm proceeds as its event fires; the instance **completes only
  once all the gate's arms have fired** (the §2.12.3 completion gate).

It resolves the deferral in [ADR-015 §2.6](../design/ADR-015-event-triggered-instantiation.md).

---

## 1. Background

[SRD-024](SRD-024-event-based-gateway.md) landed the mid-flow gate (gate-as-router,
first-fire wins). The **instantiating** gate was deferred (ADR-015 §2.6: "the
event-based gateway used at start … and the parallel-event-gateway start"). The
machinery it needs is largely in master (survey 2026-06-21):

- **Born-from-event.** `pkg/thresher/instance_starter.go` `scanInstantiatingStarts`
  (`:99`) builds a persistent definition-level `instanceStarter` for every
  instantiating start trigger (`len(n.Incoming()) == 0 && isInstantiatingStartNode(n)`,
  `:103`); on fire it calls `Thresher.resolveAndLaunch` (`thresher.go:589`) →
  `launchInstanceFromEvent` → `instance.NewFromEvent` (`instance.go:269`), which seeds
  the message payload + the conversation key and runs from the start node's outgoing.
  `isInstantiatingStartNode` (`:146`) today recognises only a message `StartEvent` and
  an `instantiate=true` `ReceiveTask`.
- **Create-or-route-or-join by key.** `resolveAndLaunch` (`thresher.go:589`) — empty key
  ⇒ always instantiate; non-empty key ⇒ atomic dedup via `t.seenKeys` (a second
  same-key start joins, no duplicate). Phase 2b.
- **Conversation-token threading (phase 2c, landed — SRD-017).** A born instance seeds
  its conversation key (`withConversationKey`/`associateConversationKey`,
  `instance.go:138`/`:329`); in-instance receivers declare `CorrelationKeys()`
  (`track.go`), and the membroker routes a follow-up message to the **specific** keyed
  in-instance receiver in preference to a definition-level starter (specificity
  routing). Proven by `pkg/thresher/conversation_routing_test.go`.
- **The instantiate pattern.** `activities.ReceiveTask` already models start-awareness:
  `WithInstantiate()` (`receive_task_options.go:46`) + `Instantiate() bool`
  (`receive_task.go:119`). The gateway mirrors it.
- **The mid-flow gate.** `pkg/model/gateways/event_based.go` `EventBasedGateway`
  (SRD-024) — `Definitions()` (arms' union), `ArmFor`/`defMatches`, `ProcessEvent`
  routing, `Validate`. Mid-flow only; no `instantiate`/`eventGatewayType`.

**The gaps.** (1) the gateway has no `instantiate`/`eventGatewayType`; (2)
`isInstantiatingStartNode`/`scanInstantiatingStarts` don't know the gateway (one starter
must cover its **several arms**); (3) **Parallel-start has no completion gate** —
`Instance.loop()` completes on `active == 0` (`instance.go:667`) with no notion of
"this instance must see all the gate's arms fire first".

---

## 2. Requirements

### Functional

- **FR-1 — gateway start attributes.** `EventBasedGateway` gains `WithInstantiate()` +
  `Instantiate() bool`, `WithEventGatewayType(EventGatewayType)` +
  `EventGatewayType() EventGatewayType` (the enum `{ ExclusiveEvents (default),
  ParallelEvents }`, re-introduced — start-only), `WithCorrelationKey(*CorrelationKey)` +
  `CorrelationKey()` (gate-level correlation, see FR-2), and the convenience
  `ParallelStart() bool` (`instantiate && gwType == ParallelEvents`, read structurally by
  the runtime). All carried through `Clone()` (ADR-009).
- **FR-2 — start validation (registration).** Extend `Validate` (ADR-005 v.4 §2.12.5):
  an **instantiating** gate (`Instantiate()`) must have **no incoming flow** and only
  **message-based** arms (Message catch / Receive Task — BPMN §10.6.6 / §13.2);
  `ParallelEvents` **requires** `Instantiate()` (a non-instantiating gate MUST be
  Exclusive, §10.6.6); a non-instantiating gate keeps the §2.12.5 mid-flow rules. A
  Parallel-start gate declares **one** gate-level `CorrelationKey` (`WithCorrelationKey`,
  FR-1) whose property carries a per-arm-message retrieval expression — so the starter
  derives the same conversation key from whichever arm fires first and the rest route to
  that instance (BPMN §8.4.2). The key lives on the **gate**, not the arms (intermediate
  catch events / receive tasks have no correlation declaration of their own).
- **FR-3 — starter recognition.** `isInstantiatingStartNode` recognises an
  instantiating `EventBasedGateway`; `scanInstantiatingStarts` builds a starter that
  covers **all** the gate's arms — registered (persistent) on each arm's message
  definition, so any arm can instantiate.
- **FR-4 — Exclusive-start (multi-alternative instantiator).** Each occurrence of any
  arm's event → a **new instance** via `resolveAndLaunch` (born from the gate, routed to
  the firing arm); the instance does not wait for the gate's other events — the
  "first matching Event" is the *per-instance* race-stop, not a one-shot (BPMN §10.5.6:
  "each occurrence … leads to the creation of a new Process instance … the only scenario
  where a Gateway can exist without an incoming Sequence Flow"; §13.2; §10.6.6 — the
  instantiate marker is a Multiple Start Event). With no correlation key each event makes
  its own instance; with a key, `seenKeys` dedup applies as for any keyed start.
- **FR-5 — Parallel-start birth.** The first arm event (key `K`) creates **one** instance
  (born from the gate, seeded with `K`); the firing arm's continuation runs, and the
  gate's **remaining arms re-arm as in-instance receivers keyed to `K`** (reusing
  phase-2c specificity routing) so a subsequent arm's message reaches *this* instance.
- **FR-6 — Parallel-start completion gate (automatic).** A Parallel-start instance
  completes only when **every** arm has fired (§2.12.3 — the instance "completes only if
  all Events … have occurred", §13.2). This is achieved **without a dedicated gate field**:
  the born path seeds the gate's not-yet-fired arms as **waiting tracks**, which keep the
  instance's `active` count `> 0` until their events arrive, so the existing
  `active == 0` completion (`instance.go:667`) already blocks on all arms. An arm proceeds
  as its event arrives (no barrier); the unfired arms only block *completion*.
- **FR-7 — Exclusive default / no-instantiate stays mid-flow.** Without `WithInstantiate`
  the gateway is exactly the SRD-024 mid-flow Exclusive gate (no behaviour change);
  `ParallelEvents` without `Instantiate` is a build/registration error (FR-2).

### Non-functional

- **NFR-1 — reuse.** Born-from-event (`instanceStarter`/`NewFromEvent`/`resolveAndLaunch`)
  and phase-2c keyed routing are reused; this SRD adds the gateway recognition, the
  multi-arm starter, and the completion gate — no new event/correlation subsystem.
- **NFR-2 — loop-owned.** The completion gate is evaluated on the instance loop (the
  single writer of instance state), consistent with the join/gate machinery
  (ADR-005 §2.4/§2.10/§2.11/§2.12).
- **NFR-3 — concurrency.** The starter's create-or-route is atomic (`t.seenKeys`,
  `thresher.go`); arms firing concurrently into a Parallel instance are serialized by
  the loop. Verified under `-race`.
- **NFR-4 — mid-flow/other gateways untouched.** SRD-024 mid-flow behaviour and the
  other gateways are unchanged; the start path is additive.
- **NFR-5 — coverage.** Touched files finish ≥95% diff-coverage (`make ci`), aim 100%.

---

## 3. Models

### 3.1 `EventBasedGateway` start attributes (`pkg/model/gateways/event_based.go`)

```go
// EventGatewayType selects an instantiating gate's start policy (ADR-005 v.4 §2.12.4).
// It is meaningful only with WithInstantiate; a non-instantiating (mid-flow) gate is
// always Exclusive (BPMN §10.6.6).
type EventGatewayType uint8

const (
	ExclusiveEvents EventGatewayType = iota // each event → a new instance (default)
	ParallelEvents                          // first event → one instance; wait for all
)

type EventBasedGateway struct {
	corrKey *bpmncommon.CorrelationKey // gate-level correlation (Parallel-start)
	Gateway
	instantiate bool
	gwType      EventGatewayType
}

func WithInstantiate() EventBasedOption              // mark the gate a start instantiator
func WithEventGatewayType(t EventGatewayType) EventBasedOption
func WithCorrelationKey(k *bpmncommon.CorrelationKey) EventBasedOption

func (g *EventBasedGateway) Instantiate() bool
func (g *EventBasedGateway) EventGatewayType() EventGatewayType
func (g *EventBasedGateway) CorrelationKey() *bpmncommon.CorrelationKey
func (g *EventBasedGateway) ParallelStart() bool // instantiate && gwType == ParallelEvents
```

(`EventBasedOption`/the config machinery removed in SRD-024's §10.6.6 rework is
re-introduced minimally for these options; mirrors `complex.go`/the old shape.
`WithCorrelationKey` carries **one** `CorrelationKey` whose `CorrelationProperty`
holds a per-arm-message retrieval expression, so the starter derives the same
conversation key from whichever arm fires first — BPMN §8.4.2: the gate's message
triggers "share the same correlation information".)

### 3.2 Parallel-start born seeding (`internal/instance`)

**No completion-gate field is needed** — completion is *automatic* (see §4.3). The
Parallel-start born path `seedParallelStart` (`instance.go:1018`) pre-fires the firing
arm (a track on its outgoing, via `ArmFor`) and seeds **a waiting track at each other
arm node**. A `TrackWaitForEvent` track keeps the instance's `active` count `> 0` (the
loop does `active++` per spawned track and `active--` only on `evEnded`, `instance.go`),
so the existing completion check at `instance.go:667` — `active == 0` — already blocks
until every arm has fired and run its continuation. The seeded conversation key makes
the waiters keyed to `K` (`CorrelationKeys()`), so subsequent arms route to them.

```go
// createTracks gains a Parallel-start branch: seedParallelStart(gate, bornEvent)
// pre-fires the instantiating arm and arms the rest as keyed waiters. The other arms'
// waiting tracks keep active>0 until they fire — no separate eventGate.expected field.
func (inst *Instance) seedParallelStart(gate flow.Node, bornEvent flow.EventDefinition) error
```

---

## 4. Analysis

### 4.1 Recognising the instantiating gate

`isInstantiatingStartNode` (`instance_starter.go:146`) adds: an `EventBasedGateway`
whose `Instantiate()` is true. `scanInstantiatingStarts` (`:99`) — for such a gate (no
incoming) — builds **one** starter that registers on **each arm's** message definition
(the gate's `Definitions()`), so any arm fires it. The starter records the gate +
`eventGatewayType` so `ProcessEvent` picks Exclusive vs Parallel.

### 4.2 Exclusive-start

The starter's `ProcessEvent`, for an Exclusive gate, calls `resolveAndLaunch` exactly as
a message-start does — born from the gate, routed to the firing arm (the instance runs
from that arm's continuation; the gate node is recorded then routed, reusing SRD-024's
`ArmFor`). Each event is independent; `seenKeys` dedup applies only if the arms carry a
key (normally Exclusive-start arms don't share one).

### 4.3 Parallel-start

The first arm (key `K`) instantiates (born from the gate, seeded `K`). At birth
`seedParallelStart` (`instance.go:1018`) **pre-fires the firing arm** (a track on its
outgoing, resolved via `ArmFor`) and **seeds a waiting track at each of the gate's other
arm nodes**; the seeded conversation key makes those waiters keyed to `K`
(`CorrelationKeys()`). A subsequent arm's message (key `K`) is routed by the membroker's
**most-specific** rule (`pkg/messaging/membroker/membroker.go:128` — "a keyed subscription
… is preferred over a wildcard") to *this* instance's keyed waiter in preference to the
wildcard definition starter; it fires that arm and forks its continuation. (Even if the
starter also saw it, its `seenKeys` dedup makes it a no-op — the outcomes compose.)

**Completion is automatic** — each not-yet-fired arm's waiting track keeps `active > 0`;
the instance reaches `active == 0` (`instance.go:667`) only once every arm has fired and
run its continuation. So no `eventGate.expected` field is needed (a design simplification
vs the v.1 draft — see §10). The `resolveAndLaunch` seen-key path stays a no-op for
instantiation (the subsequent message reaches the instance directly via phase-2c).

### 4.4 Why no new ADR

ADR-005 v.4 §2.12.3 (Parallel completion-gate, verified) and §2.12.4 (the instantiator,
Exclusive vs Parallel) **decide** this; ADR-015 (born-from-event) + ADR-016 (key
correlation, phase 2b/2c) supply the mechanism. This SRD wires them and **resolves
ADR-015 §2.6's deferral** (a linked-doc sync, not an amendment).

---

## 5. Test scenarios (§6)

| # | Test | Scenario | Asserts |
|---|---|---|---|
| 1 | `TestEventGatewayExclusiveStart` | instantiating Exclusive gate, 2 message arms; publish arm A | a new instance runs arm A's path to completion; no wait for B |
| 2 | `TestEventGatewayExclusiveStartEachEventNewInstance` | publish two (uncorrelated) arm events | two independent instances |
| 3 | `TestEventGatewayParallelStartCompletesOnAll` | instantiating Parallel gate, 2 correlated arms; publish A then B (same key) | first creates one instance; B routes to it; instance completes only after **both** |
| 4 | `TestEventGatewayParallelStartDoesNotCompleteEarly` | publish only A | instance stays Active (completion gated on the unfired arm) until B arrives |
| 5 | `TestEventGatewayParallelStartCorrelation` | two keys (K1, K2), arms interleaved | each instance sees only its own key's arms; no cross-talk |
| 6 | `TestEventBasedGatewayValidate` (+ `TestEventBasedConfigValidate`, `TestEventBasedGatewayValidateReceiveArmBoundary`) | Parallel without instantiate / instantiating gate with an incoming flow / non-message arm at start / receive-arm boundary | each rejected at registration |
| 7 | model-unit (`TestEventBasedGatewayParallelStartAndKey`, `TestWithCorrelationKeyNil`, `TestEventBasedGatewayArmForMessageByName`, …) | `WithInstantiate`/`WithEventGatewayType`/`WithCorrelationKey`, `Instantiate`/`EventGatewayType`/`CorrelationKey`/`ParallelStart`, `Clone`, `defMatches` by-name | construction + carry-through + message-by-name match |

In-package (`internal/instance`) tests cover the completion gate for per-package
coverage.

---

## 8. Cross-doc

- **Implements** [ADR-005 v.4](../design/ADR-005-gateways-and-joins.md) §2.12.3/§2.12.4 (up).
- [ADR-015 v.1](../design/ADR-015-event-triggered-instantiation.md) — born-from-event;
  §2.6 deferral resolved here (up).
- [ADR-016 v.1](../design/ADR-016-message-correlation.md) §2.3/§2.4 — key dedup +
  conversation-token threading / most-specific routing (up).
- [SRD-024 v.1](SRD-024-event-based-gateway.md) — the mid-flow gate this extends (sideways).
- [SRD-017 v.1](SRD-017-conversation-token-threading.md) — phase-2c routing reused (sideways).

(Versions pinned during authoring; no downward refs.)

## 9. Definition of Done

- FR-1…FR-7 wired; §5 tests pass under `-race`.
- `make ci` green: lint, build, `-race`, diff-coverage ≥95% (aim 100%), govulncheck.
- `examples/` gains a Parallel-start example (e.g. an order opened by either of two
  correlated messages, completing once both arrive), smoke exit 0.
- ADR-015 §2.6 deferral note updated (linked-doc sync) once landed.
- **Out of scope:** Conditional arms (need a conditional waiter); the §2.5
  context-based correlation (phase 3, deferred); loop re-arming (engine-wide).

## 10. Implementation summary

Landed on branch `feat/event-based-instantiator` (off `master`).

### 10.1 Stages by commit

| Milestone | Commit | Scope | Tests |
|---|---|---|---|
| Doc | `9204e17` | SRD-025 (this doc) | — |
| M1 — start attributes + validation | `577b117` | `EventBasedGateway` `WithInstantiate`/`WithEventGatewayType`/`Instantiate()`/`EventGatewayType()` + `Validate` start rules (`event_based.go`) | `TestEventBasedGatewayValidate`, `TestEventBasedConfigValidate` |
| M2 — Exclusive-start | `28af691` | `scanInstantiatingStarts` gate branch (`startNode = arm` via `ArmFor`); born path reused (`instance_starter.go`) | `TestEventGatewayExclusiveStart`, `…EachEventNewInstance` |
| M3 — Parallel-start (+ M3a, + 2 fixes) | `0e1f1a9` | gate `WithCorrelationKey`/`CorrelationKey()`/`ParallelStart()`; `seedParallelStart` (waiting-track seeding); `scanInstantiatingStarts` Parallel branch (`startNode = gate`, shared key); `defMatches` by-name; `launchInstanceFromEvent` handle | `…ParallelStartCompletesOnAll`, `…DoesNotCompleteEarly`, `…Correlation`, `TestSeedParallelStart*`, `TestEventBasedGatewayParallelStartAndKey`, `TestWithCorrelationKeyNil`, `TestEventBasedGatewayArmForMessageByName` |

The branch also carries two cross-cutting commits folded in at the user's request
(not SRD-025 milestones): `90d4f98` (examples print their process schema) and `2f22881`
(event-processing Debug logging across the EventHub / membroker / starter).

### 10.2 Deltas vs the v.1 draft

- **Completion gate — automatic, no `eventGate` field (FR-6 / §3.2 / §4.3).** The draft
  proposed an `eventGate{expected map[...]}` field cleared per fired arm. The
  implementation seeds the gate's not-yet-fired arms as **waiting tracks**
  (`seedParallelStart`, `instance.go:1018`), which keep `active > 0` until they fire, so
  the existing `active == 0` completion (`instance.go:667`) already gates on all arms. No
  new field — a simplification. (Verified by `…DoesNotCompleteEarly`: one arm fired ⇒ the
  instance stays Active until the other arrives.)
- **Gate-level `CorrelationKey` (M3a, FR-1 / FR-2).** A needed prerequisite the v.1 draft
  only assumed (§4.3): `WithCorrelationKey`/`CorrelationKey()` were added to the gateway
  (`event_based.go`) — correlation declaration is otherwise `StartEvent`/`SendTask`-only;
  the gate's intermediate-catch / receive-task arms have none. One gate key with a
  per-arm-message retrieval expression (BPMN §8.4.2).
- **`defMatches` message-by-name (bug fixed en route).** `ArmFor` matched messages by def
  ID, but `Clone()` gives the instance's arms fresh def IDs, so a Parallel-start gate
  never resolved its firing arm. `defMatches` (`event_based.go:283`) now matches
  `MessageEventDefinition`s by **name** too (mirroring the signal-by-name fallback).
- **Event-born `InstanceHandle` (pre-existing bug fixed en route).**
  `launchInstanceFromEvent` (`thresher.go:694`/`:769`) registered no handle, so
  `Thresher.Instance(id)` returned nil and `WaitCompletion`/`State` broke for **every**
  event-born instance (not just Parallel) — a latent SRD-015/019 gap. Now registers the
  handle like `launchInstance`.

### 10.3 Verification (V-results)

- `make ci` green at HEAD: tidy, lint, build, `-race`, **diff-coverage 98.8%** (`COVER_MIN`
  95), govulncheck clean.
- All 15 `examples/` smoke green (exit 0), incl. the new `examples/event-based-parallel-start`.
- §5 tests pass under `-race`.

## Open questions

- **None.**
