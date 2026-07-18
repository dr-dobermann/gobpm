# SRD-053 — Non-Interrupting Event Sub-Processes

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-18 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-023 v.2](../design/ADR-023-sub-process-and-call-activity.md) §2.10 (the Event Sub-Process — non-interrupting slice; epic #91) |
| Upstream | [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md) (the non-interrupting boundary multi-shot this mirrors at scope granularity), [ADR-006 v.3](../design/ADR-006-events-and-subscriptions.md) §2.7 (the conditional start), [ADR-001 v.6](../design/ADR-001-execution-model.md) (the loop owns the handler registries), extends [ADR-013 v.2](../design/ADR-013-instance-observability.md) with the per-fire handler fact |
| Refines | — |

## §1 Background

[SRD-052](SRD-052-interrupting-event-subprocess.md) landed the **interrupting**
Event Sub-Process: a `triggeredByEvent` `SubProcess` armed while its enclosing
scope is open, whose fire runs a **cancel-and-run** (cancel the scope's siblings,
run the handler, absorb). ADR-023 v.2 §2.10 also **decided** the
**non-interrupting** variant but sliced its implementation second: *"the
non-interrupting fire is spawn a handler track, don't cancel the scope"* —
**unlimited concurrent** handler instances, **any trigger except Error** (Error
is interrupting-only per §10.5.6). SRD-052 §4.3 rejected a non-interrupting start
at validation *forward-only* precisely so this slice relaxes the gate without
invalidating any model.

This SRD lands that decided variant. The **arming** machinery (SRD-052 M2 —
`armScopeHandlers`, the per-kind waiters, the `scopeHandlers` registry) is
**reused unchanged**; only the **fire** behaviour differs. It is the
scope-granularity twin of the **non-interrupting boundary event** (`fireBoundary`
without `CancelActivity`): fork a concurrent run, leave the watch armed for the
next fire.

**What exists today (verified):** `pkg/model/activities/subprocess.go:182` rejects
a non-interrupting start (`"a non-interrupting event sub-process start isn't
supported yet"`); `classifyEntries` already **counts** them
(`nonInterruptingTriggered`, subprocess.go:194,220-222). The interrupting fire is
`internal/instance/scope_handler.go:174` `fireScopeHandler` →
`runScopeHandler`. The non-interrupting boundary precedent is
`internal/instance/boundary_watch.go` `fireBoundary` (fork, no cancel, watch
stays armed → multi-shot). A colliding scope path **queues** (serialises) —
`scope_runtime.go:88` — which is why concurrent non-interrupting fires need
**unique** child scopes.

## §2 Requirements

### Functional — the model gate

- **FR-1 — relax the validation, keep Error interrupting-only.** `SubProcess`
  validation (`validateEventSubShape`) accepts a **non-interrupting** triggered
  start, EXCEPT an **Error** start, which stays rejected (BPMN §10.5.6: an Error
  event sub-process is always interrupting). The forward-only relaxation SRD-052
  §4.3 promised: every model that validated before still validates.

### Functional — the armed watch

- **FR-2 — the watch knows its mode.** `scopeHandlerWatch` carries an
  `interrupting bool`, set at arm time from the triggered start's
  `IsInterrupting()`. Arming is otherwise unchanged (same per-kind waiters, same
  registry, same conditional/Error handling).

### Functional — the non-interrupting fire

- **FR-3 — fork, don't cancel.** `fireScopeHandler` branches on the watch's mode:
  an **interrupting** fire runs the existing `runScopeHandler` (cancel-and-run,
  the shared budget, disarm); a **non-interrupting** fire runs a new
  concurrent-run that **spawns the handler without** touching the scope's sibling
  tracks, the interrupting budget (untouched), or the watch (left armed).
- **FR-4 — a concurrent instance per fire (unique scope).** Each
  non-interrupting fire opens the handler in its **own unique child scope**
  (`E/sp-<handler>-<n>`, not the shared `E/sp-<handler>`), so two concurrent
  fires run side-by-side instead of the second **queueing** behind the first. The
  fire's trigger **payload is bound into the enclosing scope** (as the
  interrupting handler binds it), read by the handler's inner nodes via walk-up
  — concurrent instances share this binding; per-instance payload isolation is a
  deferred refinement (§4.3).
- **FR-5 — multi-shot.** The non-interrupting watch is **not** disarmed on fire:
  it stays armed and fires again on the next trigger (Message/Signal/Timer via the
  hub's retained waiter; the conditional start on the next false→true edge),
  spawning another concurrent instance — **unlimited**, as the standard permits.
- **FR-6 — drain interplay.** Each concurrent handler instance is a track counted
  into the **enclosing** scope (`incScope`); the enclosing sub-process completes
  only when its own work **and every live handler instance** have drained — the
  existing terminal-event accounting, no new state. A scope-cancel of the
  enclosing scope (an interrupting boundary on its host, instance terminate) stops
  the concurrent instances too — they live under the enclosing path.

### Functional — observability & front door

- **FR-7 — observability.** Each non-interrupting fire emits a handler `Fired`
  fact (the SRD-052 `KindBoundary`+scope-path shape) carrying the instance's
  scope path, so concurrent instances are distinguishable; arm/disarm unchanged.
- **FR-8 — front door.** A thresher e2e (a non-interrupting handler fires while
  its scope keeps running; the scope and the handler both complete; a second fire
  spawns a second concurrent instance); an `examples/event-subprocess/` variant
  or note; the composition-guide Event Sub-Process section gains the
  non-interrupting behaviour; changelog; conformance tracker row 2 🟡 → ✅;
  READMEs (EN + RU where they describe the handler).

### Non-functional

- **NFR-1 — the interrupting slice is untouched.** `runScopeHandler`, the shared
  budget, the Error scope-chain catch, and absorb behave exactly as SRD-052
  landed them; the branch is additive.
- **NFR-2 — single-writer preserved.** The new fire path and any per-fire counter
  are loop-owned; hub/timer fires re-enter via the loop's event channel.
- **NFR-3 — Error stays interrupting-only** at the model gate (FR-1) — never
  reaches the non-interrupting fire path.
- **NFR-4 — coverage**: touched files 100% (min 80%); diff-coverage ≥95%;
  `make ci` per milestone (frozen tree).

## §3 Models

### §3.1 `activities.SubProcess` validation (delta)

`validateEventSubShape` drops the blanket `nonIntr > 0` reject; instead it
rejects only a **non-interrupting Error** start (walk the triggered start's
definition kind). Every other non-interrupting trigger is accepted.

### §3.2 Runtime deltas

- `scopeHandlerWatch`: `+ interrupting bool` (set in `armScopeHandlers` from
  `start.IsInterrupting()`).
- `track`: `+ scopeSeg string` (an optional scope-segment override) — zero for
  every existing spawn, so nothing changes for non-handler tracks.
- `loopState`: `+ handlerSeq int` (a loop-owned monotonic counter minting the
  per-fire unique segment).
- `fireScopeHandler`: branch on `w.interrupting`; the non-interrupting arm calls
  `runNonInterruptingHandler`.
- `runNonInterruptingHandler` (new): bind the payload at the enclosing scope
  (`bindEventPayloadAt(w.path, …)`, as `runScopeHandler` does), then spawn the
  handler track with `scopeSeg = scopeSegment(w.handler) + "-" + handlerSeq++`;
  `incScope`/`spawn` as usual; **no** budget, **no** `interruptScopeSiblings`,
  **no** `disarmScopeHandlers`.
- `onScopeOpen` (`scope_runtime.go:88`): derive the child segment as
  `scopeSegment(node)` unless the host track carries a `scopeSeg` override — so a
  non-interrupting handler instance opens a unique child scope.

## §4 Analysis

### §4.1 Reuse the arm, branch the fire
Arming a non-interrupting handler is identical to an interrupting one — same
`RegisterEvent`/`condWatch`, same registry. Only the fire diverges, so the whole
change is one branch in `fireScopeHandler` plus a `runNonInterruptingHandler`
peer of `runScopeHandler` that omits the three interrupting-only steps (budget,
sibling-cancel, disarm) — the exact shape `fireBoundary` already takes for a
non-interrupting boundary.

### §4.2 Why unique child scopes (the concurrency mechanic)
`onScopeOpen` **queues** a re-entry when a scope path is already open
(`scope_runtime.go:88`) — the sequential-re-entry seam (SRD-049 §4.4). For an
**interrupting** handler that's correct (one instance). For **non-interrupting**,
two fires of the same handler node would derive the same `E/sp-<handler>` path
and the second would serialise behind the first — violating the standard's
**concurrent, unlimited** semantics. A per-fire unique segment
(`E/sp-<handler>-<n>`) makes each fire open its own scope, so instances run
side-by-side; a monotonic loop-owned counter guarantees uniqueness without
per-node bookkeeping.

### §4.3 Payload binding — enclosing scope, per-instance isolation deferred
The trigger payload is bound at the **enclosing** scope, exactly as the
interrupting handler binds it (`runScopeHandler`), read by the handler's inner
nodes via walk-up. Concurrent non-interrupting instances therefore **share** the
enclosing binding: two fires with distinct payloads would see the later one
(last-writer-wins), and the payload item is visible to the scope's still-running
sibling work by name. This is acceptable for the common case (a fixed-shape
trigger; the item name is distinct from process data) and keeps the fire path
symmetric with the interrupting handler. **Per-instance payload isolation** —
binding each fire's payload into its own child scope — is a **deferred
refinement**: it was prototyped but introduced an unreachable commit-error guard
in `onScopeOpen` (the freshly-opened child scope never rejects the commit), so it
is left for a follow-up that needs concurrent distinct-payload handlers.

### §4.4 Error stays interrupting-only
BPMN §10.5.6 makes an Error event sub-process always interrupting (an unhandled
error must interrupt to be meaningful). The gate rejects a non-interrupting Error
at validation (FR-1/NFR-3) rather than silently coercing it — the gobpm
reject-at-build pattern.

## §6 Test scenarios

Model (`pkg/model/activities`): `TestNonInterruptingEventSubValidate` (a
non-interrupting Message/Signal/Timer/Conditional start now validates; a
non-interrupting **Error** start is rejected; the interrupting shapes still
validate).
Runtime (`internal/instance`, fakes): `TestNonInterruptingHandlerDoesNotCancel`
(a fire spawns the handler while the scope's sibling keeps running — both
complete), `TestNonInterruptingHandlerMultiFire` (two fires → two concurrent
instances, distinct scope paths, both run), `TestNonInterruptingHoldsScopeOpen`
(the enclosing scope completes only after a still-running handler instance
drains), `TestNonInterruptingPerInstancePayload` (two fires with distinct
payloads each read their own), `TestNonInterruptingBudgetUnspent` (a
non-interrupting fire leaves `scopeInterrupted` clear, so an interrupting
boundary can still fire).
E2E (`pkg/thresher`): `TestNonInterruptingEventSubProcessE2E`.

## §7 Milestones

| # | Scope |
|---|---|
| M1 | The model — relax `validateEventSubShape` (accept non-interrupting, reject non-interrupting Error) + the `interrupting` flag armed on the watch (FR-1, FR-2) + tests |
| M2 | The non-interrupting runtime — `fireScopeHandler` branch, `runNonInterruptingHandler`, the unique per-fire scope (`scopeSeg`/`handlerSeq` + `onScopeOpen`), enclosing-scope payload bind, multi-shot, drain interplay, observability (FR-3..7) + tests |
| M3 | Thresher e2e + `examples/` + guide + changelog + tracker row 2 → ✅ + READMEs (FR-8) |

Post-M3: `/check-srd`, §10 fill, SRD Accepted, sync linked docs (ADR-023 v.2 is
already Accepted and unchanged — this slice adds no conception), PR handover.

## §8 Cross-doc

- Implements [ADR-023 v.2](../design/ADR-023-sub-process-and-call-activity.md)
  §2.10 (the non-interrupting decision, already Accepted — no ADR change).
- Rides [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md)
  (non-interrupting boundary multi-shot), [ADR-006 v.3](../design/ADR-006-events-and-subscriptions.md)
  §2.7 (conditional start), [ADR-001 v.6](../design/ADR-001-execution-model.md)
  (loop ownership); extends [ADR-013 v.2](../design/ADR-013-instance-observability.md)
  with the per-fire handler fact.
- Sibling: [SRD-052](SRD-052-interrupting-event-subprocess.md) (the interrupting
  slice this completes).

## §9 Definition of Done

- [ ] FR-1..8 wired and traced to §6 tests.
- [ ] `make ci` green per milestone; diff-coverage ≥95%; touched files 100% (min 80%).
- [ ] Example runs to completion (exit 0), binary gitignored.
- [ ] Conformance tracker row 2 → ✅ (the Event Sub-Process type complete).
- [ ] Changelog `[Unreleased]`.
- [ ] `/check-srd` PASS; §10 filled; SRD Accepted; linked docs synced.

## §10 Implementation summary

> ⚠️ TODO: fill after landing.

## Open questions

None.
