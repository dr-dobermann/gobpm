# FIX-011 — Event-definition interface conformance

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-06-29 |
| Owner | Ruslan Gabitov |
| Related | [ADR-006 v.2 Event delivery](../design/ADR-006-event-subscription-delivery.md) |

One-shot remediation of two **silent interface no-ops** surfaced by
`docs/audit/code-review-third-pass-2026-06-29.md` §2.2 / §2.3: event-definition
methods whose names do not match the interface they are meant to implement, so
the override never happens and a payload is silently dropped. Both compile —
a method with the wrong name is just an unused extra method, and a type
assertion against an unsatisfied interface is legal Go that resolves to `false`.

## 1. Symptoms

- **1.1** A `SignalEventDefinition` always reports **zero** data items:
  `flow.EventDefinition` requires `GetItemsList()` (plural), but Signal spells its
  override `GetItemList()` (singular). Interface dispatch resolves to the embedded
  `definition.GetItemsList()`, which returns an empty list, so a signal's payload
  is excluded from readiness checks and the throw item-collection.
- **1.2** A thrown **Error** / **Escalation** (and Message) propagates **without
  its data payload**: `emitEvent` gathers the throw's data and is supposed to
  clone-with-data via `flow.EventDefCloner`, but the three implementers name the
  method `CloneEvent` while the interface requires `CloneEventDefinition`. The
  assertion `ed.(flow.EventDefCloner)` is always false, the clone step is dead,
  and the un-cloned definition propagates.

## 2. Root-cause analysis

Two method-name mismatches with **no compile-time `var _` assertion** to catch
them. For `GetItemsList`, the embedded `definition` supplies a (wrong, empty)
fallback so the type still satisfies `flow.EventDefinition` — the misnamed
override is dead code. For `CloneEventDefinition`, no type satisfies
`flow.EventDefCloner` at all, but the consuming code reaches it only through a
runtime type assertion, which legally compiles and silently yields `false`. The
durable defence is a `var _ Interface = (*T)(nil)` assertion per event-definition
type, which turns either mismatch into a build error.

## 3. Solution

### 3.1 Considered alternatives
- **Rename the interface methods to match the impls** (`CloneEventDefinition`→
  `CloneEvent`) — rejected: the interface is the contract and the descriptive
  name (`CloneEventDefinition`, `GetItemsList`) is the one to keep; the impls
  conform to it, not the reverse.

### 3.2 Per-site changes
- **3.2.1** `pkg/model/events/signal.go:128` — rename `GetItemList` →
  `GetItemsList` (and its doc comment) so it overrides the interface method;
  return the signal's structure.
- **3.2.2** `pkg/model/events/{message,error,escalation}.go` — rename the three
  `CloneEvent` methods → `CloneEventDefinition` to satisfy `flow.EventDefCloner`.
- **3.2.3** `internal/eventproc/eventhub/waiters/message.go:355` — update the one
  concrete call site `mw.eDef.CloneEvent(...)` → `CloneEventDefinition(...)`.
- **3.2.4** Add compile-time assertions next to each type:
  `var _ flow.EventDefinition = (*SignalEventDefinition)(nil)` and
  `var _ flow.EventDefCloner = (*MessageEventDefinition)(nil)` (+ Error,
  Escalation), so a future rename that breaks conformance fails `make ci`.

## 4. Verification

### 4.1 Tests
| Test | Asserts |
|---|---|
| `TestSignalEventDefinitionGetItemsList` | a signal built with an ItemDefinition returns it from `GetItemsList()` (was empty) |
| `TestEventDefClonerSatisfied` | `*MessageEventDefinition`/`*ErrorEventDefinition`/`*EscalationEventDefinition` each satisfy `flow.EventDefCloner`; `CloneEventDefinition` clones with the supplied data |
| (compile-time) | the new `var _` assertions build |

## 5. Prevention
The `var _` assertions make both classes of mismatch a build error going
forward — the gap existed precisely because no assertion pinned conformance.

## 6. Regressions
`CloneEventDefinition` now actually runs on the throw path, so error/escalation
throws carry their payload (the intended BPMN §10.4.2 behaviour). Signals now
report their items. No signature changes to the interfaces; only method renames
on the concrete types and the one concrete call site.

## 7. Related
ADR-006 v.2 (event delivery). FIX-010 fixed the neighbouring
`bpmncommon.Error.Structure()` nil panic on the same error-event path.

## 8. Implementation summary
*(filled at landing.)*

## 9. Open questions
None.
