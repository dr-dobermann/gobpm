# SRD-052 — Interrupting Event Sub-Processes

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-17 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-023 v.2](../design/ADR-023-sub-process-and-call-activity.md) §2.10 (the Event Sub-Process — interrupting slice; epic #91) |
| Upstream | [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md) (the boundary-watch arming this lifts to scope granularity), [ADR-006 v.3](../design/ADR-006-events-and-subscriptions.md) §2.6 (the Error scope-chain), §2.7 (the conditional false→true edge + the conditional start landing here), [ADR-001 v.6](../design/ADR-001-execution-model.md) (the loop owns the handler registries) |
| Refines | — |

## §1 Background

ADR-023 v.2 §2.10 decides the **Event Sub-Process**: a `SubProcess` marked
`triggeredByEvent` that is **armed while its enclosing scope is open** — the
boundary-watch pattern lifted from an activity's window to a scope's window —
whose single triggered start fires to **cancel the scope's sibling tracks**
(§2.5) and **run the handler in the parent's data context**. This SRD lands
the **interrupting** variant with the Message/Timer/Signal/Error/Conditional
triggers (the **conditional start** ADR-006 v.3 deferred lands here). The
non-interrupting variant is the next slice; Transaction/Escalation ride #90.

The code today (evidence-first):

- **`activities.SubProcess` has no event-sub concept.** The struct embeds
  `flow.ElementsContainer` + `activity` (`subprocess.go:20-23`); `Validate()`
  (`subprocess.go:68-118`) **rejects** any triggered start —
  *"a triggered Start Event isn't allowed in an embedded Sub-Process"*
  (`subprocess.go:91-95`). `scopeSeeds`/the entry-seed rule returns
  None-start XOR flow-less nodes (`scope_runtime.go:170-189`).
- **The start-event interrupting flag already exists.**
  `StartEvent.interrupting` + `IsInterrupting()` (`start.go:49,107-110`) and
  the `WithInterrupting()` option are complete; the trigger set
  (Message/Timer/Signal/Error/Conditional) already passes start validation.
- **The scope model is landed** (ADR-023): `scopeEntry{host,node,parent,
  queue,active}` (`scope_runtime.go:29-35`); `onScopeOpen` seeds inner
  tracks (`:71-125`), `cancelScope` stops every track under a path
  (`:292-335`), `completeScope`/`resumeScopeHost` drain-resume the host
  (`:221-274`).
- **The boundary-watch arming is the pattern to lift.**
  `boundaryWatch{host,boundary,def,loopOwned}` (`boundary_watch.go:20-30`);
  `armBoundaries` registers a watch per boundary when a track enters a
  guarded activity (`:72-152`), `disarmBoundaries` tears them down
  (`:174-206`), `fireBoundary` cancels the host on an interrupting fire and
  routes the exception flow (`:223-256`). **There is no cross-handler
  interrupting budget today** — each activity owns its own watches.
- **The conditional machinery is reusable.**
  `condWatch{track,node,def,deps,last,boundary}` (`conditional.go:21-33`);
  `armConditionals`/`armCondBoundary`/`sweepConditionals` implement the
  false→true edge (`conditional.go:113,168,203`) — the conditional START
  reuses this with `boundary=false`.
- **The snapshot scans instantiating starts.**
  `discoverInstantiatingStarts` lists no-incoming Message/Signal starts
  (`snapshot/instantiating_starts.go:30-90`); an event-sub start has no
  incoming flow and would be **wrongly** listed as a process instantiator.

## §2 Requirements

### Functional — model

- **FR-1 — `triggeredByEvent` on `SubProcess`.** A `triggered bool` field +
  a `WithTriggeredByEvent()` construction option. When set, `Validate()`
  **allows exactly one triggered Start Event** (§10.5.2) and **requires** it
  (no None start, no flow-less seeds); the triggered-start rejection at
  `subprocess.go:91-95` is gated on `!sp.triggered`. When unset, the v.1
  embedded-sub-process rules stand unchanged. An `IsEventSubProcess()`
  accessor exposes the flag; `Clone()` copies it.
- **FR-2 — the triggered start's kind & interruption are read from the
  model.** The event sub-process's single start carries exactly one trigger
  definition (Message/Timer/Signal/Error/Conditional) and its
  `IsInterrupting()` selects the variant. Validate rejects a triggered start
  with zero or multiple trigger definitions. **This slice validates the
  start as interrupting** — a non-interrupting event-sub start is **rejected
  at validation** with a clear "non-interrupting event sub-processes land in
  a later slice" error, so the model never expresses runtime-unsupported
  behaviour; slice 2 relaxes this validation (forward-only). Error is always
  interrupting (§10.5.6), so an Error start needs no interrupting flag.

### Functional — not-an-entry-node

- **FR-3 — an event sub-process is never seeded as an entry node.**
  `scopeSeeds` (`scope_runtime.go:170-189`) **skips** a `SubProcess` with
  `triggered=true`: a normal instantiation seeds the None-start/flow-less
  nodes and leaves the event sub-process to be **armed** (FR-5), not run.
- **FR-4 — an event sub-process is not a process instantiator.**
  `discoverInstantiatingStarts` (`snapshot/instantiating_starts.go`) skips a
  `triggered` SubProcess's start: it has no incoming flow but is a scope
  handler, not a process-starting event.

### Functional — arming

- **FR-5 — scope-handler arming.** When a scope opens (`onScopeOpen`, or the
  instance root at start), the loop **arms** each event sub-process declared
  directly in that scope — a new `armScopeHandlers(sh, path)` mirroring
  `armBoundaries`, registering the triggered start's waiter per kind:

  | Trigger | Arming (reused) |
  |---|---|
  | Message | `inst.RegisterEvent(instance, def)` (the instance owns correlation, ADR-016) → delivered as the loop's `evDeliver` |
  | Signal | `inst.RegisterEvent(watch, def)` on the signal name |
  | Timer | `inst.RegisterEvent(watch, def)` → the timer subsystem fires into the loop |
  | Conditional | a **loop-local `condWatch`** (`boundary=false`), swept by `sweepConditionals` on the false→true edge — **the conditional start** |
  | Error | no armed waiter — resolved by the §2.6 scope-chain walk at the throw site (FR-8) |

  The armed watches live in a **new loop-owned registry**
  `scopeHandlers map[scope.DataPath][]*scopeHandlerWatch`; disarmed when the
  scope drains (`completeScope`), is cancelled (`cancelScope`), or the budget
  closes (FR-6). A scope with **no** event sub-processes arms nothing
  (NFR-3).

### Functional — the shared interrupting budget

- **FR-6 — one interrupting handler per Event Declaration, shared with
  boundary events.** A new per-scope registry
  `scopeInterrupted map[scope.DataPath]bool` (or the winning Event
  Declaration): the **first** interrupting fire in a scope — whether an event
  sub-process **or** a boundary event on the scope host — flips the scope to
  *interrupted*, and every further handler (interrupting or not) is
  **suppressed** (§10.5.6). The boundary-watch fire path (`fireBoundary`) and
  the scope-handler fire path both consult and set this flag, so the two
  constructs cooperate rather than double-fire.

### Functional — interrupting runtime

- **FR-7 — cancel-and-run.** On an interrupting scope-handler fire (a new
  `evScopeHandlerFire` loop event carrying the handler + its bound trigger
  payload): (1) `cancelScope` the enclosing scope's sibling tracks — the
  data plane **stays open**; (2) open the **handler's own** scope (the event
  sub-process is itself a `scopeHost`) via `onScopeOpen`, its inner flow
  seeded from the triggered start treated as fired (the born-event payload
  binding); (3) the handler runs in the parent's data context (the §10.5.7
  walk-up); (4) on the handler's drain, the enclosing scope completes and
  resumes the parent host, following the handler's outcome. The
  Error-vs-non-error parent lifecycle (Failing/Terminating) is realized by
  **which cancel path ran** and surfaced in observability (FR-10), not a
  token state.
- **FR-8 — Error-triggered handler on the scope chain.** An Error thrown
  inside the scope walks the §2.6 chain (`matchErrorScopeChain`); the walk is
  extended to consider a scope's **event-sub Error handler** as a catch point
  (alongside the composite's Error boundary), obeying the §10.5.6 precedence
  (an inline handler that absorbs suppresses a same-declaration boundary).
- **FR-9 — absorb (+ re-throw where modelled).** A handler that reaches its
  End **without re-throwing** absorbs the event: the scope completes and the
  parent resumes on its **normal** flow, any same-declaration boundary
  suppressed. A handler that **re-throws** (a Throw of the trigger's kind at
  its End) re-enters the existing throw path so a boundary fires after — the
  decorator case; verified where both are modelled.

### Functional — observability & front door

- **FR-10 — observability.** A scope-handler **arm** / **fire** / **disarm**
  fact (the `Boundary` kind extended, or a `Scope`-family phase) carrying the
  handler's kind, its Event Declaration, whether it interrupted, and the
  Failing-vs-Terminating intent; the existing `Scope` cancel/complete facts
  cover the scope side.
- **FR-11 — front door.** A thresher e2e (an interrupting Timer/Message/Error
  handler catching inside a scope and cancelling it; a conditional-start
  handler); an `examples/event-subprocess/`; the composition guide gains an
  Event Sub-Process section; changelog; the conformance tracker row 2 moves
  to 🟡 (interrupting landed, non-interrupting/#90 remain); READMEs.

### Non-functional

- **NFR-1 — the budget is a correctness property.** No two interrupting
  handlers (event-sub or boundary) ever both fire in one scope; the
  loop-owned registry makes it single-writer, race-free.
- **NFR-2 — single-writer preserved.** All new registries
  (`scopeHandlers`, `scopeInterrupted`) are loop-owned; hub/timer fires
  re-enter via the loop's event channel, never mutate registries off-loop.
- **NFR-3 — handler-free scopes pay nothing.** A scope with no event
  sub-processes arms no watches and allocates no registry entries.
- **NFR-4 — the v.1 embedded sub-process is unchanged.** A `SubProcess`
  without `triggeredByEvent` behaves exactly as SRD-049 landed it.
- **NFR-5 — coverage**: touched files 100% (min 80%); diff-coverage ≥95%;
  `make ci` per milestone (frozen tree).

## §3 Models

### §3.1 `activities.SubProcess` (delta)

```go
type SubProcess struct {
	flow.ElementsContainer
	activity
	triggered bool // triggeredByEvent (BPMN §13.5.4) — an event sub-process
}
```

`WithTriggeredByEvent() SubProcessOption`; `IsEventSubProcess() bool`;
`Validate()` gates the triggered-start rule on `triggered`.

### §3.2 Runtime deltas

- `loopState`: `scopeHandlers map[scope.DataPath][]*scopeHandlerWatch` +
  `scopeInterrupted map[scope.DataPath]bool` (loop-owned).
- `scopeHandlerWatch{handler *track?; start flow.EventNode; def
  flow.EventDefinition; loopOwned bool}` — the event-sub twin of
  `boundaryWatch`.
- `trackEvent`: a new `evScopeHandlerFire` kind (+ names row).
- `armScopeHandlers` / `disarmScopeHandlers` / `fireScopeHandler` — the
  scope twins of `armBoundaries`/`disarmBoundaries`/`fireBoundary`.
- `snapshot`: `discoverInstantiatingStarts` skips a `triggered` SubProcess.
- `observability`: the scope-handler arm/fire/disarm fact.

## §4 Analysis

### §4.1 Reuse, don't re-implement

The event sub-process is deliberately the **same machinery** as the boundary
event, one scope level up: the same `RegisterEvent` waiters, the same
`condWatch`, the same `cancelScope`, the same drain-resume. The only genuinely
new pieces are (a) the **arm-on-scope-open** trigger (vs arm-on-activity-enter)
and (b) the **shared interrupting budget** the standard mandates but the v.1
boundary machinery didn't need. Modelling event sub-processes as N boundary
events was rejected (ADR-023 §4): different lifetime, different catch reach,
different handler location — only the budget overlaps.

### §4.2 The handler runs in its own scope, under the parent

An event sub-process is a `scopeHost` (it has an inner graph). On fire the
loop cancels the *parent* scope's tracks, then opens the *handler's own*
child scope — so the handler's locals live and die with it, and its reads
walk up into the (still-open) parent scope's data. This keeps the ADR-023
"one scope per composite" invariant: the handler is just another composite,
opened by a trigger instead of a token.

### §4.3 Non-interrupting deferral — reject at validation, not at runtime

This slice **rejects** a non-interrupting event-sub start at model
validation (FR-2) rather than accepting it and faulting at runtime. Reason:
a validation-time gate keeps the model from ever expressing behaviour the
engine won't run — the gobpm pattern (the v.1 sub-process shape validation
rejects unsupported starts the same way). Slice 2 **relaxes** the gate
(forward-only — a strictly wider acceptance, never a re-restriction), so no
model that validates today stops validating later. The alternative
(accept-and-runtime-fault) was rejected: it ships an API surface that
compiles a process which then dies at runtime — a worse failure mode than a
build-time rejection.

## §6 Test scenarios

Model: `TestEventSubProcessValidate` (triggered start required+unique when
flagged; rejected when unflagged; None+triggered mix rejected),
`TestEventSubProcessClone`.
Snapshot: `TestEventSubStartNotInstantiator`.
Runtime (`internal/instance`, fakes): `TestScopeHandlerArmDisarm`,
`TestInterruptingTimerCancelsScope`, `TestInterruptingMessageHandler`,
`TestErrorEventSubCatchesOnChain`, `TestConditionalStartHandler`
(the false→true start), `TestSharedInterruptingBudget` (boundary + event-sub
compete, one fires), `TestAbsorbSuppressesBoundary`, `TestHandlerFreeScopeArmsNothing`.
E2E (`pkg/thresher`): `TestEventSubProcessE2E` (an interrupting handler
catches a timer inside a scope, cancels it, runs the handler, resumes),
`TestEventSubConditionalStartE2E`.

## §7 Milestones

| # | Scope |
|---|---|
| M1 | The model — `triggeredByEvent` + validation + `scopeSeeds`/snapshot exclusion (FR-1..4) + tests |
| M2 | Scope-handler arming — `armScopeHandlers`/`disarmScopeHandlers`, the per-kind waiters incl. the conditional start, the `scopeHandlers` registry (FR-5, FR-9) + tests |
| M3 | Interrupting runtime — the shared budget, `evScopeHandlerFire`, cancel-and-run, the Error-chain catch, absorb, observability (FR-6..10) + tests |
| M4 | Thresher e2e + `examples/event-subprocess/` + guide + changelog + tracker + READMEs (FR-11) |

Post-M4: `/check-srd`, §10 fill, SRD Accepted, sync linked docs (ADR-023 v.2
stays Draft until its own re-accept — this SRD landing is the evidence for
that flip, handled with the ADR, not here), PR handover.

## §8 Cross-doc

- Implements [ADR-023 v.2](../design/ADR-023-sub-process-and-call-activity.md)
  §2.10.
- Rides [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md)
  boundary-watch arming; [ADR-006 v.3](../design/ADR-006-events-and-subscriptions.md)
  §2.6 (Error chain) / §2.7 (conditional edge + start); [ADR-001 v.6](../design/ADR-001-execution-model.md)
  loop ownership; extends [ADR-013 v.2](../design/ADR-013-observability.md)
  with the scope-handler fact.

## §9 Definition of Done

- [ ] FR-1..11 wired and traced to §6 tests.
- [ ] `make ci` green per milestone; diff-coverage ≥95%; touched files 100% (min 80%).
- [ ] Example runs to completion (exit 0), binary gitignored.
- [ ] Conformance tracker row 2 updated (interrupting landed).
- [ ] Changelog `[Unreleased]`.
- [ ] `/check-srd` PASS; §10 filled; SRD Accepted; linked docs synced.

## §10 Implementation summary

> ⚠️ TODO: fill after landing.

## Open questions

None.
