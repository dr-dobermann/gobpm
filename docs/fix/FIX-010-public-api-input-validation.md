# FIX-010 — Public-API input validation hardening

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-06-29 |
| Owner | Ruslan Gabitov |
| Related | [ADR-006 v.2 Event delivery](../design/ADR-006-events-and-subscriptions.md), [ADR-013 v.1 Lifecycle/handle](../design/ADR-013-instance-observability.md) |

One-shot remediation of a class of defects surfaced by the 2026-06-29 code
reviews (`docs/audit/code-review-codex-second-pass-2026-06-29.md` §3/§4/§5/§7,
`docs/audit/code-review-third-pass-2026-06-29.md` §2.1/§2.11): **public entry
points that dereference a nullable/optional argument before validating it**, so a
caller mistake becomes a deferred panic deep inside the library instead of a
classified domain error at the boundary. This is the project's standing rule —
*validate all parameters of public APIs, with self-identifying errors* — applied
to the seams that violate it.

## 1. Symptoms

- **1.1** `EventHub.RegisterEvent` / `RegisterPersistentEvent` / `PropagateEvent`
  panic (nil dereference) when given a nil `flow.EventDefinition` after the hub is
  started — the processor argument is validated, the event definition is not.
- **1.2** A BPMN `bpmncommon.Error` constructed with a nil structure (legal — an
  Error's `ItemDefinition` is optional) panics in `Error.Structure()`; the guards
  meant to tolerate it (`events/error.go`) call `Structure()` *inside* the
  nil-check and so panic in the guard itself. Reachable at runtime from a
  boundary/end Error event routed through `GetItemsList`.
- **1.3** `Instance.RegisterEvent(nil, eDef)` on a **terminal** instance panics:
  the terminal-state branch builds a diagnostic with `proc.ID()` before the nil
  guard runs.
- **1.4** A user `goexpr.GExpFunc` that returns `(nil, nil)` panics the evaluating
  goroutine (`res.Get(ctx)` with no nil result guard) instead of yielding a
  classified error.
- **1.5** `localdispatcher.Register` accepts a nil handler; the panic surfaces far
  away at dispatch time.
- **1.6** `membroker.Publish` / `Subscribe` ignore a cancelled `context.Context` —
  a cancelled call still mutates broker state.

## 2. Root-cause analysis

A single pattern: **the validation either is missing or runs after the first
dereference of the argument**. EventHub and `Instance.RegisterEvent` validate the
processor but not the event definition, and build state-specific diagnostics from
the unvalidated argument before the nil guard. `bpmncommon.NewError` stores its
`structure` with no nil check while `Structure()` unconditionally dereferences it.
`goexpr` checks the error but not the value. `localdispatcher`/`membroker` accept
inputs (handler, ctx) they never inspect. None of these is a logic bug in the
happy path; each is an unguarded **public boundary** that converts a caller error
into a library-internal crash, exactly the failure class the `WithLogger(nil)`
precedent established the rule against.

## 3. Solution

Validate every nullable/optional public parameter **at the boundary, before any
use**, returning a self-identifying `errs` error (naming the function + the
parameter). For cancellation, return `ctx.Err()` before mutating state.

### 3.1 Considered alternatives
- **Panic with a clearer message** — rejected: a library must return errors for
  caller mistakes, not crash the embedding process (same rationale as the rule).
- **Validate only at the outermost call** — rejected: each listed method is itself
  an exported entry point; defence belongs at each boundary.

### 3.2 Per-site changes

- **3.2.1 EventHub** (`internal/eventproc/eventhub/eventhub.go:140,164,419`) —
  add `if eDef == nil { return errs.New(errs.M("EventHub.RegisterEvent: a nil
  EventDefinition isn't allowed"), errs.C(errorClass, errs.EmptyNotAllowed)) }` at
  the top of `RegisterEvent`, `RegisterPersistentEvent`, and `PropagateEvent`
  (each with its own self-naming message), before any `eDef.ID()`/`eDef.Type()`.
- **3.2.2 bpmncommon.Error** (`pkg/model/bpmncommon/error.go:58`) — make
  `Structure()` nil-safe: `if e.structure == nil { return nil }` before the
  dereference. Audit the `events/error.go` guards to read the field result once
  and handle nil (no `Structure()`-inside-the-guard panic).
- **3.2.3 Instance.RegisterEvent** (`internal/instance/instance.go:~1462`) — move
  the `proc == nil` / `eDef == nil` guards **above** the terminal-state branch so
  diagnostics are never built from an unvalidated argument.
- **3.2.4 goexpr** (`pkg/model/data/goexpr/goexpr.go:126`) — after the error
  check, `if res == nil { return errs.New(errs.M("goexpr: evaluation produced a
  nil value"), errs.C(errorClass, errs.OperationFailed)) }` before `res.Get`.
- **3.2.5 localdispatcher** (`pkg/tasks/localdispatcher/localdispatcher.go:43`) —
  reject a nil handler (and an empty job type) in `Register` with a self-naming
  error.
- **3.2.6 membroker** (`pkg/messaging/membroker/membroker.go:158,211`) — `if err
  := ctx.Err(); err != nil { return …err }` at the top of `Publish` and
  `Subscribe`, before acquiring `b.mu`.

## 4. Verification

### 4.1 Tests
| Test | Asserts |
|---|---|
| `TestEventHubRejectsNilEventDefinition` | started hub: `RegisterEvent`/`RegisterPersistentEvent`/`PropagateEvent` with nil eDef return a classified error, no panic |
| `TestErrorStructureNilSafe` | `NewError(name, code, nil)` then `Structure()` returns nil (no panic); a boundary Error event with no item def routes through `GetItemsList` without panic |
| `TestRegisterEventNilProcessorTerminal` | terminal instance: `RegisterEvent(nil, eDef)` returns the validation error, no panic |
| `TestGoexprNilResult` | a `GExpFunc` returning `(nil,nil)` yields a classified error, no panic |
| `TestLocalDispatcherRejectsNilHandler` | `Register(type, nil)` / empty type return errors |
| `TestMembrokerHonorsCancelledContext` | cancelled `Publish`/`Subscribe` return `ctx.Err()` and do not mutate broker state |

## 5. Prevention
Each fix carries a self-identifying message naming the function + parameter, so a
future failure points at the offending call. The pattern (guard-before-use at
every exported boundary) is the standing rule; these sites are now compliant.

## 6. Regressions
Behaviour-preserving for all valid inputs; only previously-panicking invalid
inputs now return errors. No public signature changes (all six already return
`error` or are guarded internally).

## 7. Related
ADR-006 v.2 (event delivery), ADR-013 v.1 (lifecycle). Sibling FIX docs in this
remediation set: FIX-011 (event-def interface conformance) shares the
`bpmncommon.Error`/`GetItemsList` neighbourhood.

## 8. Implementation summary
*(filled at landing: files/lines, test results, commit SHAs.)*

## 9. Open questions
None.
