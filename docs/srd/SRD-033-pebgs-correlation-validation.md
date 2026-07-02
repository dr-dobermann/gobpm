# SRD-033 — Parallel-start event gateway: correlation validation and runtime key guard

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-02 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-005 v.4 §2.12.5(7) Gateways & joins](../design/ADR-005-gateways-and-joins.md) |

Implements the one ADR-005 v.4 §2.12.5 validation rule the code never landed —
rule 7, *"Parallel-start arms must carry correlation"* — closing audit finding
**AB-001** (keyless `ParallelEvents` start gate double-instantiates): reject the
misconfiguration at registration, and guard the runtime path where a conformant
model still meets a non-conformant message.

## 1. Background (verified against the code)

### 1.1 The defect (AB-001)

A `ParallelEvents` instantiating event-based gateway (PEBGS) must produce **one**
instance per conversation: the first arm's message creates it, the remaining
arms' messages join it, and completion waits for all arms (BPMN §13.2). The
create-or-route decision is keyed on the message's derived correlation key:

- `instanceStarter.deriveKey` (`pkg/thresher/instance_starter.go:71`) returns
  `""` when the starter has no `CorrelationKey` (`if s.corrKey == nil { return
  "", nil }`) — and also when the key cannot be populated from the payload
  (`DeriveKey` → `ok == false`).
- `Thresher.resolveAndLaunch` (`pkg/thresher/thresher.go`) treats an empty key as
  "no dedup": `if key == "" { … return t.launchInstanceFromEvent(…) }` — it
  **always creates a new instance**.

So a keyless PEBGS spawns a fresh instance per arm message: N arms → N stuck
instances, each waiting forever for sibling arms whose messages went to sibling
instances. `EventBasedGateway.Validate` lets the keyless gate through — its
`validateStartGate` (`pkg/model/gateways/event_based.go:493`) checks
instantiation shape and the ParallelEvents/instantiate coupling, but has **no
correlation check**.

### 1.2 The decision already exists — this SRD lands it

[ADR-005 v.4 §2.12.5](../design/ADR-005-gateways-and-joins.md) decides eight
registration-time validation rules for the gate. The code implements rules 1–6
and 8; **rule 7 is described but unimplemented**:

> "7. **Parallel-start arms must carry correlation** so subsequent events route
> to the created instance."

[ADR-005 v.4 §2.12.4](../design/ADR-005-gateways-and-joins.md) states the
normative basis: every subsequent event of the gate is correlated to the one
instance — *"they MUST share correlation info with the first"*. The spec source
is **BPMN §10.6.6** (p. 298, formal/13-12-09, verified against the PDF):

> "The Parallel Event Gateway is also a type of race condition. In this case,
> however, when the first Event is triggered and the Process is instantiated,
> the other Events of the Gateway configuration are not disabled. The other
> Events are still waiting and are expected to be triggered before the Process
> can (normally) complete. In this case, the Messages that trigger the Events of
> the Gateway configuration MUST share the same correlation information."

The vendored extract carries the clause in
`docs/bpmn-spec/state-machines/process-lifecycle.md` ("they MUST share
correlation info with the first").

### 1.3 What "share the same correlation information" means mechanically

`msgflow.DeriveKey` (`pkg/model/msgflow/correlation.go:52`) composes the key
from the `CorrelationKey`'s properties; for each property it looks up the
retrieval expression **for that message** —
`retrievalExprFor(prop, msg)` matches `expression.MessageRef.Name() ==
msg.Name()` (`correlation.go:117`) — and fails (`ok=false`) when a property has
no expression for the message. Therefore an arm "carries correlation" exactly
when **every property of the gate's key declares a retrieval expression for that
arm's message**. This is a purely structural condition, checkable at
registration with no expression evaluation.

### 1.4 One-shot arm semantics — already correct, pinned here by test

Each remaining arm of a born PEBGS instance is seeded as a parked track at the
arm node (`Instance.seedParallelStart`, `internal/instance/instance.go:1338`),
which registers a **single-shot** in-instance waiter (`EventHub.RegisterEvent`;
the hub removes a fired waiter — ADR-006 v.1 §2.5). So an arm consumes exactly
one message and its subscription dies with the fire; a same-key duplicate on an
already-fired arm reaches only the persistent starter, whose key reservation
makes it a "join existing" no-op. No mechanism change is needed; FR-4 pins the
guarantee with a regression test so it is contractual, not incidental.

## 2. Requirements

### Functional

- **FR-1 — keyless PEBGS rejected at registration.** A `ParallelEvents`
  instantiating gate that declares no `CorrelationKey` fails
  `EventBasedGateway.Validate` (and thus `Process.Validate` / `snapshot.New`)
  with a self-identifying error naming the gate.
- **FR-2 — per-arm key coverage rejected at registration.** For a keyed PEBGS,
  every arm's message must cover the key: each `CorrelationProperty` of the
  gate's key declares a retrieval expression whose `MessageRef` is that arm's
  message. A violation fails validation naming the gate, the arm, and the
  uncovered properties.
- **FR-3 — runtime underivable-key guard (PEBGS starter only).** When the
  persistent starter of a `ParallelEvents` gate derives an empty key at runtime
  (the payload cannot populate the key — `DeriveKey ok=false`), the starter
  **does not instantiate**: the message is consumed with a warning log
  (non-conformant input per §10.6.6). Exclusive-start and message-start
  semantics are untouched.
- **FR-4 — one message per arm (regression pin).** After a PEBGS instance is
  born and one of its remaining arms fires, a second message with the same
  correlation key for that arm neither re-fires the arm nor creates another
  instance.

### Non-functional

- **NFR-1 — no payload values in logs.** The FR-3 warning logs process id, gate
  id, and message name — never payload contents (the `DeriveKey` NFR-1
  precedent).
- **NFR-2 — validation errors are self-identifying** (gate id, arm id, property
  names), consistent with the existing §2.12.5 checks' `errs.D` details.
- **NFR-3 — no behavioural change for valid models.** A keyed, fully-covered
  PEBGS and every non-PEBGS configuration validate and run exactly as before.

## 3. Models

### 3.1 `msgflow` static coverage helper (`pkg/model/msgflow/correlation.go`)

A small exported helper beside `DeriveKey`, reusing `retrievalExprFor`:

```go
// MissingKeyProperties reports which of key's properties declare no retrieval
// expression for msg — the properties DeriveKey could never populate from that
// message. An empty result means msg covers the key (BPMN §10.6.6: gateway-arm
// messages MUST share the same correlation information). It is the static
// registration-time counterpart of DeriveKey's runtime ok=false.
func MissingKeyProperties(
	key *bpmncommon.CorrelationKey,
	msg *bpmncommon.Message,
) []string
```

`pkg/model/gateways` does not import `msgflow` today; `msgflow` imports only
`bpmncommon`/`data`/`expression`/`foundation`, so the new
`gateways → msgflow` edge is cycle-free.

### 3.2 Gate validation (`pkg/model/gateways/event_based.go`)

`validateStartGate` grows the rule-7 checks, active only for
`instantiate && gwType == ParallelEvents`:

1. `g.corrKey == nil` → error (FR-1).
2. For each arm, resolve the arm's message — a `ReceiveTask` arm exposes
   `Message() *bpmncommon.Message` (`receive_task.go:101`); a message-catch arm
   carries a `*events.MessageEventDefinition` in its `Definitions()`, whose
   `Message()` (`events/message.go:69`) yields the same (`gateways` already
   imports `events`) — and reject when
   `msgflow.MissingKeyProperties(g.corrKey, msg)` is non-empty (FR-2), with
   `errs.D` carrying the gate id, arm id, and missing property names.

Arms are already guaranteed message-based at start by the landed rule 6, so a
message is always resolvable where these checks run.

### 3.3 Starter runtime guard (`pkg/thresher/instance_starter.go`)

`instanceStarter.ProcessEvent` gains one branch between `deriveKey` and
`resolveAndLaunch`: if the derived key is empty **and** the starter's
`startNode` is a Parallel-start gate (`interface{ ParallelStart() bool }` — the
same assertion `createTracks` uses), log a warning and return without
instantiating (FR-3). For such a starter `corrKey != nil` is guaranteed by FR-1,
so an empty key can only mean a runtime derive failure.

## 4. Analysis

### 4.1 Reject-at-validation, not an implicit gate-id key (decided)

The earlier AB-001 sketch weighed keying the dedup on the gate id when no
`CorrelationKey` is present. Rejected: §10.6.6 **mandates** shared correlation
information for the gate's messages, so the keyless model is non-conformant —
making it "work" via an engine-invented key would mask a modelling error and
manufacture a singleton semantics the standard does not define. Fail-fast at
registration matches the landed §2.12.5 rules and the engine's
validate-all-inputs discipline.

### 4.2 Static structural check + runtime guard, not either alone (decided)

The static check (FR-1/FR-2) catches model errors at registration but cannot see
payloads; the runtime guard (FR-3) catches conformant models fed non-conformant
messages (an unset key field) that would otherwise re-open the stuck-instance
bug through `resolveAndLaunch`'s empty-key create branch. Both are needed; the
runtime guard is scoped to the PEBGS starter so plain message-start and
Exclusive-start instantiation semantics (ADR-016 v.1 §2.3 territory) are
untouched.

### 4.3 Arm-message resolution via the existing `events` import (decided)

`gateways` already imports `events` (per `go list`), so a message-catch arm's
message is read directly from its `*events.MessageEventDefinition`; a
`ReceiveTask` arm exposes `Message()` itself. No structural-interface
indirection is needed. The only new package edge is `gateways → msgflow`
(§3.1), which is cycle-free.

### 4.4 One-shot arms need no mechanism (decided)

§1.4: single-shot in-instance waiters already realize "an event occurs once";
the starter's key reservation already absorbs duplicates. FR-4 adds only a test.

### 4.5 What stays the same (decided)

`resolveAndLaunch`'s empty-key create branch (legitimate for keyless
message-start events and signal starts), `DeriveKey`, `seedParallelStart`, the
waiter lifecycle, and all §2.12.5 rules 1–6/8.

## 5. API / contract surface

- **New exported:** `msgflow.MissingKeyProperties` (§3.1).
- **Changed contract:** `EventBasedGateway.Validate` rejects two previously
  accepted (but never executable-to-completion) configurations: a keyless
  PEBGS and a keyed PEBGS with an uncovered arm.
- **No signature changes**; the FR-3 guard is internal to the starter.

## 6. Test scenarios

| Test | Site | Asserts |
| --- | --- | --- |
| `TestParallelStartRequiresCorrelationKey` | `gateways/event_based_test.go` | a keyless `ParallelEvents` instantiating gate fails `Validate` naming the gate (FR-1); the same gate with a covering key passes. |
| `TestParallelStartArmMustCoverKey` | `gateways/event_based_test.go` | a keyed PEBGS with one arm whose message lacks a retrieval expression for a key property fails `Validate` naming arm + property (FR-2). |
| `TestMissingKeyProperties` | `msgflow/correlation_test.go` | full coverage → empty; one missing expression → that property's name; nil-safety mirrors `DeriveKey`'s param validation. |
| `TestParallelStarterSkipsUnderivableKey` | `thresher` tests | a running PEBGS starter receiving a message whose payload cannot populate the key creates **no** instance and logs a warning without payload values (FR-3, NFR-1). |
| `TestParallelStartArmFiresOnce` | `thresher`/instance integration test | born PEBGS instance; fire one remaining arm; a duplicate same-key message for that arm does not re-fire it and instance count stays 1 (FR-4). |
| Exclusive-start regression | existing suites | Exclusive-start and message-start behaviour unchanged (NFR-3) — existing tests stay green. |

## 7. Milestones

- **M1 — registration-time validation:** `msgflow.MissingKeyProperties` + the
  `validateStartGate` rule-7 checks + FR-1/FR-2 tests.
- **M2 — runtime guard + one-shot pin:** the starter's underivable-key guard +
  FR-3/FR-4 tests; docs sync (a correlation cross-reference line in
  `docs/bpmn-spec/semantics/gateways.md`'s parallel-start note; AB-001
  graduation from `docs/audit/audit-backlog.md` with the corrected
  governing-doc pointer — the entry predates ADR-005 v.4 landing the EBG-start
  model).

Non-goals: any change to Exclusive-start/message-start instantiation, to
`DeriveKey`, or to the broker no-target policy (ADR-016 v.1 §2.7); conversation
-token threading (deferred in ADR-016 v.1 §2.4).

## 8. Cross-doc

- [ADR-005 v.4 §2.12.4/§2.12.5/§2.12.7](../design/ADR-005-gateways-and-joins.md)
  — the decided model and the rule this SRD implements.
- [ADR-015 v.1 §2.2](../design/ADR-015-event-triggered-instantiation.md) — the
  definition-level instance-starter the FR-3 guard lives in.
- [ADR-016 v.1 §2.2/§2.3](../design/ADR-016-message-correlation.md) — key
  derivation and create-or-route; this SRD consumes both unchanged.
- SRD-025 — the landed EBG instantiation this corrects the validation gap of
  (frozen one-shot; not retro-edited).
- BPMN **§10.6.6** (p. 298: the MUST-share-correlation clause), **§13.2**
  (completion gate) — via `docs/bpmn-spec/state-machines/process-lifecycle.md`
  and the PDF (NotebookLM-verified).
- `docs/audit/audit-backlog.md` **AB-001** — closed by this landing.

## 9. Definition of Done

- [ ] FR-1/FR-2 wired in `validateStartGate`; FR-3 wired in the starter; all §6
      tests exist and pass.
- [ ] `make ci` green (tidy · lint · build · `-race` · diff-coverage ≥95% ·
      govulncheck); examples smoke exit 0.
- [ ] Touched functions ≥80% (aim 100%) coverage, delivered with the change.
- [ ] Docs sync: extract cross-ref line; AB-001 graduated; no dangling refs.
- [ ] §10 implementation summary filled; status flipped only after `/check-srd`
      PASS.

## 10. Implementation summary

_(filled at landing: files/lines, V-results, milestone SHAs.)_

## Open questions

None.
