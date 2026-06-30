# FIX-011 — Event-definition interface conformance

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-06-29 |
| Owner | Ruslan Gabitov |
| Related | [ADR-006 v.2 Event delivery](../design/ADR-006-events-and-subscriptions.md), [FIX-010 Public-API input validation](FIX-010-public-api-input-validation.md) |

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
ADR-006 v.2 (event delivery).
[FIX-010](FIX-010-public-api-input-validation.md) fixed the neighbouring
`bpmncommon.Error.Structure()` nil panic on the same error-event path.

## 8. Implementation summary

Landed on branch `fix/audit-remediation-2026-06`.

**Production (4 files + 1 call site):**
- `pkg/model/events/signal.go` — `GetItemList` → `GetItemsList` (+ doc); added
  `var _ flow.EventDefinition = (*SignalEventDefinition)(nil)`.
- `pkg/model/events/{message,error,escalation}.go` — `CloneEvent` →
  `CloneEventDefinition` (+ doc) to satisfy `flow.EventDefCloner`; added the
  `var _ flow.EventDefinition` + `var _ flow.EventDefCloner` assertion pair to
  each type; removed the now-duplicate assertion block in `escalation.go`.
- `internal/eventproc/eventhub/waiters/message.go:355` — call site
  `CloneEvent(...)` → `CloneEventDefinition(...)`.
- `pkg/model/events/timer.go` — fixed a stale `CloneEvent` mention in a comment.

**Tests:**
- `pkg/model/events/edef_test.go` — added `TestSignalEventDefinitionGetItemsList`
  and `TestEventDefClonerSatisfied` (each of message/error/escalation satisfies
  `flow.EventDefCloner`, the clone carries the supplied data item, and an
  ID-mismatched datum is rejected).
- `pkg/model/events/end_test.go`, `intermediate_throw_test.go` — the two tests
  that had encoded the old dead behaviour now build a payload-less signal
  (`NewSignal(name, nil)`), and the EndEvent all-triggers test gives its mocked
  scope items the ids their definitions declare so the now-active
  clone-with-data id check passes.
- `escalation_test.go`, `message_test.go`, `conversation_key_test.go`,
  `message_instantiation_test.go` — call-site renames.

**Verification:** `make ci` green (golangci-lint 0 issues, `-race` tests,
diff-coverage **100% of 53 changed lines**, govulncheck); full suite 1068
passed; all 6 throw-path examples (`signal-broadcast`, `signal-start`,
`terminate-end-event`, `message-intermediate-events`, `boundary-events`,
`basic-process`) smoke exit 0; the `var _` assertions build.

**Commits:** doc `2d94df2`; implementation `b69bae6`; dangling-ADR-link fix
`29f10fa`.

## 9. Open questions
None.
