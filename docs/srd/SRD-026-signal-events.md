# SRD-026 — Signal events (throw / catch / broadcast + signal-start instantiation)

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-21 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-006 v.1 Events & subscriptions](../design/ADR-006-events-and-subscriptions.md) §2.1/§2.3/§2.4 |

This SRD lands **signal events** as a specified, standard-verified feature. Most of the
machinery already exists (it arrived incrementally through the event infrastructure and
the Event-Based gateway work); this SRD **specifies and tests** the landed throw / catch /
broadcast behaviour and **closes the one real gap — signal-*start* instantiation** (a
broadcast signal cannot currently instantiate a process whose start trigger is a signal).

A **signal** is a *broadcast publication, unscoped within reach, with no correlation*:
every catching handler in reach receives it (BPMN §10.5.7 / `docs/bpmn-spec/semantics/event-handling.md:221`).

---

## 1. Background

Signal support landed piecewise and is **largely implemented** (survey 2026-06-21):

- **Signal definition** — `SignalEventDefinition` holds a name-based `*Signal`
  (`pkg/model/events/signal.go:59`; `Signal.Name()` at `:49`). Name is the broadcast
  match key (signals carry no correlation).
- **Throw** — the signal trigger is allowed on intermediate-throw and end events
  (`flow.TriggerSignal` at `intermediate_throw.go:24`, `end.go:25`); both emit via the
  hub's `PropagateEvent`.
- **Catch** — allowed on intermediate-catch (`intermediate_catch.go:21`); the catcher
  registers a `SignalWaiter` on track arrival (`internal/eventproc/eventhub/waiters/signal.go`).
- **Broadcast** — `EventHub.broadcastSignal` (`eventhub.go:465`) fans a thrown signal out
  to **every** waiter whose definition matches **by name** (`:477`, via `signalName` at
  `:505`); a throw with no catcher is a logged **no-op, not an error** (`PropagateEvent`
  returns `nil` after the debug log, `eventhub.go:428-437`).
- **Event-Based gateway signal arms** — `defMatches` matches signal arms by name
  (`pkg/model/gateways/event_based.go:287`), the same key as the hub (SRD-024 v.1 §4.3).
- **Example + tests** — `examples/signal-broadcast/` (one throw → two watcher instances
  catch); `waiters/signal_test.go`, `eventhub_signal_test.go`.

**Two memory items are now stale (confirmed by the survey, recorded here):**
- ADR-006 §2.4 "no-waiter ⇒ no-op" is **already implemented** — `PropagateEvent` returns
  `nil` (not an error) for a no-registered-waiter signal (`eventhub.go:428-437`). The
  earlier "decided but EventHub still errors" note is cleared.
- The "non-message broadcast" concern (concurrent instances sharing one catch waiter both
  fire) does **not** apply to signals: multi-processor fan-out to every catcher is exactly
  correct broadcast semantics (`waiters/signal.go` `AddEventProcessor`), not a bug.

**The gap.** `scanInstantiatingStarts` (`pkg/thresher/instance_starter.go:105`) builds an
instance-starter only for a `*events.MessageEventDefinition` (the type pin at `:26`, the
cast at `:135`). A **signal** StartEvent is recognised as a start node
(`isInstantiatingStartNode` returns true for any `StartEventClass` event, `:183-185`) but
its `SignalEventDefinition` is skipped at `:135`, so **no starter is built** — a broadcast
signal can never instantiate a signal-start process.

---

## 2. Requirements

### Functional

- **FR-1 — signal throw (spec'd, already wired).** An intermediate-throw or end event with
  a `SignalEventDefinition` broadcasts the signal through `EventHub.PropagateEvent` →
  `broadcastSignal`. No behaviour change; this SRD specifies + tests it.
- **FR-2 — signal catch (spec'd, already wired).** An intermediate-catch event with a
  `SignalEventDefinition` subscribes a `SignalWaiter` on track arrival and resumes when a
  matching-by-name signal is broadcast. No behaviour change; specified + tested.
- **FR-3 — broadcast + no-catcher no-op (spec'd, already wired).** A thrown signal reaches
  **every** waiter matching by name (multi-instance fan-out, best-effort per catcher); a
  throw with no live catcher is a logged no-op, not an error (ADR-006 §2.4). Specified +
  tested.
- **FR-4 — signal-start instantiation (NEW — the implementation core).**
  `scanInstantiatingStarts` recognises a signal StartEvent (no incoming flow, signal
  trigger) and builds a starter **registered on the signal** so the hub's broadcast
  reaches it; on fire it calls `resolveAndLaunch` with **no correlation key**, so **each
  broadcast instantiates** (a single broadcast may instantiate **several** signal-start
  processes — broadcast semantics, unlike a point-to-point message start). Reuses the
  born-from-event starter (ADR-015 v.1), the signal waiter/broadcast, and `resolveAndLaunch`
  (empty key ⇒ always instantiate, no dedup).
- **FR-5 — global broadcast as the engine's reach (decision).** gobpm broadcasts to every
  signal waiter engine-wide (no scope/reach filtering). This is a deliberate engine choice
  for a single-process-pool engine and a defensible superset of "unscoped within reach"
  (§4.4 / Engine notes). BPMN §10.5.7 scope filtering is **deferred** to the sub-process
  workstream.

### Non-functional

- **NFR-1 — reuse, no new subsystem.** FR-4 reuses `instanceStarter` / `resolveAndLaunch`
  (SRD-015) and the existing signal waiter + `broadcastSignal`; this SRD adds only the
  signal-start recognition in the scanner. No new event/correlation machinery.
- **NFR-2 — no correlation for signals.** Signals carry no correlation key (BPMN: signals
  do not correlate, `event-handling.md:221`); the signal-start path uses an empty key. The
  message-correlation contract (ADR-016 v.1) is **deliberately not** on the signal path.
- **NFR-3 — concurrency.** Broadcast fan-out and the starter's create path are exercised
  under `-race`; concurrent broadcasts into the starter are serialised by `resolveAndLaunch`
  (`t.seenKeys` mutex), and an empty key never dedups.
- **NFR-4 — additive.** Message-start instantiation, the Event-Based gateway, and all other
  gateways/events are unchanged; the signal-start path is purely additive.
- **NFR-5 — coverage.** Touched files finish ≥95% diff-coverage (`make ci`), aim 100%.

---

## 3. Models

### 3.1 Generalise the starter's trigger definition (`pkg/thresher/instance_starter.go`)

Today `instanceStarter.eDef` is typed `*events.MessageEventDefinition` (`:26`), and
`scanInstantiatingStarts` casts each candidate def to that type (`:135`), silently
skipping a `SignalEventDefinition`. Generalise the field to the interface so the starter
can hold either trigger:

```go
type instanceStarter struct {
	thr       *Thresher
	snapshot  *snapshot.Snapshot
	startNode flow.Node
	eDef      flow.EventDefinition // was *events.MessageEventDefinition — now message OR signal
	corrKey   *bpmncommon.CorrelationKey // nil for signals (no correlation)
	id        string
}
```

`scanInstantiatingStarts` accepts both a `*MessageEventDefinition` (as today, with its
optional `CorrelationKey`) and a `*SignalEventDefinition` (with `corrKey = nil`).
`deriveKey` returns `""` whenever `corrKey == nil` (already its behaviour), so a signal
start derives an empty key → `resolveAndLaunch` always instantiates.

### 3.2 Persistent signal waiter for the starter (`internal/eventproc/eventhub/waiters/waiters.go`)

A starter is registered via `RegisterPersistentEvent` → `CreatePersistentWaiter`, which today
**rejects non-message triggers** (`waiters.go:112`: `eDef.Type() != flow.TriggerMessage` → error).
Extend it to also back a **signal** starter: for `flow.TriggerSignal`, build a `NewSignalWaiter`.
No new waiter type and no one-shot flag are needed — persistence is **processor-driven**: a catch
track self-unregisters as it resumes (one-shot), whereas a starter never self-unregisters, so it
stays subscribed and fires on every broadcast (persistent). Update the message-only comment + error
to "message or signal."

### 3.3 No other model change

Throw/catch/broadcast already exist; FR-1…FR-3 add no shapes. The signal waiter itself, the
`broadcastSignal` name index, and the event-based-gateway signal-arm matching are unchanged.

---

## 4. Analysis

### 4.1 Why no new ADR

ADR-006 v.1 §2.1/§2.3/§2.4 **decide** signal broadcast, the catcher subscription
lifecycle, and the no-catcher no-op; ADR-015 v.1 supplies the born-from-event
instance-starter that FR-4 reuses. This SRD wires the signal-start case and specifies the
landed behaviour. No conceptual decision is open, so no ADR (or ADR bump) is needed.

### 4.2 Signal-start instantiation (FR-4)

The instance-starter machinery (ADR-015) already: scans a snapshot for instantiating start
nodes, builds a persistent `instanceStarter` per trigger, registers it on the EventHub as
an `EventProcessor`, and on a fired event calls `resolveAndLaunch` (empty key ⇒ create;
keyed ⇒ dedup). The only thing blocking signals is the message-only cast at `:135`.

The fix: in `scanInstantiatingStarts`, when a start node's definition is a
`*SignalEventDefinition`, build a starter with `eDef = that signal def`, `corrKey = nil`.
The Thresher registers it via the same `RegisterPersistentEvent` path — which §3.2 extends so
`CreatePersistentWaiter` backs a **persistent signal waiter** for a signal trigger (it was
message-only). Because the hub keys signal delivery by **name** (`broadcastSignal` →
`signalName`), a broadcast of that signal reaches the starter's `ProcessEvent`, which calls
`resolveAndLaunch(…, key="")` → a new instance **born from the signal StartEvent** (pre-fired,
runs from its outgoing).

**Multi-instantiation is intended.** If several processes declare a signal StartEvent with
the same signal name, each registers its own starter; one `broadcastSignal` fans out to all
of them (name match), instantiating one process **per** signal-start declaration — the
broadcast analogue of a message start (which is point-to-point). With an empty key,
`resolveAndLaunch` never dedups, so repeated broadcasts keep instantiating.

### 4.3 Throw / catch / broadcast (FR-1…FR-3) — specify the landed behaviour

These are already wired (see §1). This SRD pins them with tests and standard-citations: a
signal throw broadcasts by name (`broadcastSignal`, `eventhub.go:465`); every name-matching
catcher in reach receives it (multi-processor fan-out); a no-catcher throw is a no-op
(`eventhub.go:428-437`, ADR-006 §2.4). No correlation participates (BPMN, `event-handling.md:221`).

### 4.4 Engine notes — global broadcast reach (FR-5)

BPMN §10.5.7 scopes signal *reach* (a catcher sees a signal published in a scope it
participates in). gobpm currently broadcasts to **every** signal waiter in the engine,
with no scope filtering. For a single-process-pool, in-memory engine this is a defensible
**superset** of "unscoped within reach" (`event-handling.md:221` — signals are unscoped
within their reach; with one reach, all catchers are in it). Scope-bounded reach (relevant
once sub-processes / multiple pools exist) is **deferred to the sub-process workstream**;
this SRD documents the global-reach choice rather than implementing scoping.

### 4.5 Out of scope (explicit)

- **Signal boundary events** — no boundary-event infrastructure exists at all; signal
  boundary is part of the cross-cutting **boundary-events workstream** (signal / error /
  escalation / timer / conditional boundary together), not this SRD.
- **O(1) signal-name → waiters index** — `broadcastSignal` is a linear name-scan
  (`eventhub.go:477`); the indexed lookup is a deferred, non-blocking optimisation.
- **Event-matching generalisation** (`SubscriptionKey()`) — unifying the hub's
  name-scan + the gateway's `defMatches` behind one polymorphic key is deferred until
  Link events land (when a second name-keyed event type makes the abstraction pay off).

---

## 5. Test scenarios (§6)

| # | Test | Scenario | Asserts |
|---|---|---|---|
| 1 | `TestSignalStartInstantiates` | a process with a signal StartEvent (no incoming); broadcast its signal | one new instance is born from the signal start and runs to completion |
| 2 | `TestSignalStartBroadcastInstantiatesAll` | two processes, each a signal StartEvent on the **same** signal name; one broadcast | **both** instantiate (one instance each — broadcast, not point-to-point) |
| 3 | `TestSignalStartEachBroadcastNewInstance` | broadcast the same signal twice | two independent instances (empty key ⇒ no dedup) |
| 4 | `TestSignalCatchThrow` (regression, exists) | intermediate signal throw in one instance, intermediate signal catch in another | the catcher resumes on the broadcast |
| 5 | `TestBroadcastSignalFanOut` / `TestSignalWaiterBroadcastFanOut` (regression, exist) | one throw, N waiting catchers (distinct eDef ids, same name) | all N receive it (`eventhub_signal_test.go`, `waiters/signal_test.go`) |
| 6 | `TestSignalThrownIntoVoid` (regression, exists) | broadcast a signal with no registered catcher | no error; logged no-op (`eventhub.go:428-437`) |

`TestSignalBroadcast` (cross-instance broadcast) and `TestSignalSingleShotConsume`
(a catch consumes once) in `pkg/thresher/signal_test.go` add further coverage; the
signal-start starter is covered by tests 1–3 there, and `waiters/signal_test.go` +
`eventhub_signal_test.go` cover the waiter + fan-out.

---

## 8. Cross-doc

- **Implements** [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) §2.1/§2.3/§2.4 — signal broadcast, catcher lifecycle, no-catcher no-op (up).
- [ADR-015 v.1](../design/ADR-015-event-triggered-instantiation.md) — the born-from-event instance-starter FR-4 reuses (up).
- [ADR-005 v.4](../design/ADR-005-gateways-and-joins.md) §2.12 — the Event-Based gateway's signal-arm matching by name (up).
- [SRD-024 v.1](SRD-024-event-based-gateway.md) — signal-by-name `defMatches` precedent (sideways).
- [SRD-025 v.1](SRD-025-event-based-gateway-instantiation.md) — the `scanInstantiatingStarts` extension precedent this mirrors for signal starts (sideways).

Signals carry **no** correlation, so this SRD has no ADR-016 dependency (correlation is
deliberately absent on the signal path — NFR-2). Versions pinned; no downward refs.

## 9. Definition of Done

- FR-1…FR-5 wired (FR-1…FR-3 already wired + now specified/tested; FR-4 new); §5 tests pass under `-race`.
- `make ci` green: lint, build, `-race`, diff-coverage ≥95% (aim 100%), govulncheck.
- `examples/signal-broadcast/` smoke exit 0; a signal-**start** example added (a process opened by a broadcast signal), smoke exit 0.
- The stale ADR-006 §2.4 memory item closed (no-op already implemented — §1).
- **Out of scope:** signal boundary events (boundary-events workstream); scope/reach filtering (sub-process workstream); the O(1) name index + `SubscriptionKey()` generalisation (deferred).

## 10. Implementation summary

Landed on branch `feat/signal-events` (off `master`).

### 10.1 Stages by commit

| Milestone | Commit | Scope | Tests |
|---|---|---|---|
| Doc | `c18ab2d` | SRD-026 (this doc) | — |
| M1 — signal-start instantiation | `65213ab` | `CreatePersistentWaiter` accepts `TriggerSignal` → `NewSignalWaiter` (`waiters.go`); `instanceStarter.eDef` → `flow.EventDefinition`; `scanInstantiatingStarts` accepts `*SignalEventDefinition`; `deriveKey`/`discovery.go` signal-aware (`triggerName`) | `TestSignalStartInstantiates`, `…BroadcastInstantiatesAll`, `…EachBroadcastNewInstance`, `TestTriggerName`, `TestCreatePersistentWaiter` (signal) |
| M2 — FR-1…FR-3 regression | (no commit) | Behaviour already wired **and tested** — `TestSignalCatchThrow`, `TestSignalBroadcast`, `TestSignalThrownIntoVoid`, `TestBroadcastSignalFanOut` pre-existed; no new tests needed | (existing) |
| M3 — signal-start example | `ce50ffe` | `examples/signal-start/` (one broadcast → two signal-start instances) | smoke exit 0 |

### 10.2 Deltas vs the draft

- **Waiter-layer change (the v.1 draft missed it).** §3.1 alone was insufficient: `CreatePersistentWaiter` (`waiters.go:112`) rejected non-message triggers, so a signal starter would build then fail at registration. M1 added the §3.2 fix — the persistent signal waiter (no new waiter type; persistence is processor-driven, a starter never self-unregisters). SRD §3.2/§4.2 amended (Draft) and landed with the M1 code.
- **M2 was a no-op.** The draft assumed signal throw/catch/broadcast lacked tests; in fact `TestSignalCatchThrow` / `TestSignalBroadcast` / `TestSignalThrownIntoVoid` / `TestBroadcastSignalFanOut` already existed and pass. §5 names were aligned to the real tests; no new tests were written.
- **Two stale memory items confirmed + closed:** ADR-006 §2.4 no-catcher no-op is already implemented (`eventhub.go:428-437`), and the non-message-broadcast concern doesn't apply to signals (multi-processor fan-out is correct).

### 10.3 Verification (V-results)

- `make ci` green at HEAD: tidy, lint, build, `-race`, **diff-coverage 97.1%** (`COVER_MIN` 95; covercheck v0.1.2 excludes log lines), govulncheck clean.
- All 16 `examples/` smoke green (exit 0), incl. the new `examples/signal-start`.
- §5 tests pass under `-race`; message-start instantiation unaffected (its tests still pass).

## Open questions

- **None.**
