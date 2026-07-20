# SRD-058 — Escalation events: non-critical propagation up the scope chain

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-20 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-006 v.4](../design/ADR-006-events-and-subscriptions.md) §2.2/§2.6 (propagation strategy — climb the scope chain to the innermost catcher; Escalation is **non-critical**, unresolved = **non-fault**, which this SRD realizes as *logged, not silently dropped* — §4.5), [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md) (Escalation boundary — interrupting + **non-interrupting**), [ADR-023 v.1](../design/ADR-023-sub-process-and-call-activity.md) §2.6 (the scope-chain walk Escalation reuses); GitHub epic #90 |
| Upstream | [ADR-001 v.6](../design/ADR-001-execution-model.md) §4 (the single-writer loop the propagation runs on), [ADR-009 v.1](../design/ADR-009-per-instance-node-graph.md) (per-instance scope tree), [ADR-013 v.2](../design/ADR-013-instance-observability.md) (the fact kinds reused) |
| Refines | SRD-029 (boundary events + the `matchErrorBoundary` pattern), SRD-049 (the scope tree + `matchErrorScopeChain` chain walk), SRD-052 (event-sub-process handler arming + `errorHandlerAt`) — by number, sideways. No new ADR: the conception is already decided in ADR-006 §2.2/§2.6, ADR-018, ADR-023 §2.6. |

Note on numbering: SRD-055 (MI-seq) / 056 (MI-parallel, reserved) / 057 (Link)
are taken; Escalation events take **058**.

## §1 Background

The model layer carries a **complete** Escalation model — `Escalation`
(`escalation.go:13`: `name`/`code`/`structure`, `NewEscalation`/`MustEscalation`,
`Name()`/`Code()`/`Item()`) and `EscalationEventDefinition` (`escalation.go:80`:
`Type() == flow.TriggerEscalation`, `Escalation()` getter, `CloneEventDefinition`,
`flow.EventDefinition`+`flow.EventDefCloner` asserts). `flow.TriggerEscalation`
exists (`flow/events.go:44`) and the model **accepts** it at its BPMN positions
already: **Intermediate Throw** (`intermediate_throw.go:22`), **End**
(`end.go:23`), and **event-sub-process Start** (`start.go` `startTriggers` — "only
for in-line Sub-Processes"). But **nothing executes it**: `TriggerEscalation`
appears in no `internal/` runtime path — an escalation throw flows through
`IntermediateThrowEvent.Exec → emitDefinition` onto the generic bus with **no
subscriber**, a silent no-op (the pre-SRD-026 state Signal was in). It is
**absent** from `boundaryTriggers` (`boundary.go:16-22`), so an escalation
boundary cannot even be constructed.

**Escalation is Error's non-critical twin.** The vendored spec
(`semantics/event-handling.md:17,20,219`): Error and Escalation both use the
**propagation** strategy — "walk from the throwing scope outward to enclosing
scopes, checking each for a catching Event with matching `errorRef` /
`escalationRef`. First match consumes the trigger." They differ on two axes:

- **Critical vs non-critical.** An Error "suspend[s] execution at throw location";
  an Escalation is **non-critical** — "execution continues at throw location"
  (`event-handling.md:17`). So an escalation **throw event** is a normal
  flow node whose token continues down its outgoing flow; the escalation
  propagates *beside* it.
- **Unresolved handling.** An unresolved Error is critical (abort the instance);
  an unresolved Escalation is **non-fault** — the spec "typically ignores" it
  (`event-handling.md:20`, ADR-006 §2.2/§2.6). This engine reads "ignore" as
  **log, don't abort** (never silently drop) — the unresolved escalation is
  logged + an `Unresolved` fact, execution continues, no instance fault (§4.5).

Plus a third: an Escalation boundary is **non-interrupting-capable**
(`event-handling.md:113`: "non-interrupting: Escalation, Message, Signal, …
Error is excluded"), where an Error boundary is always interrupting.

**The machinery to reuse is landed.** Error propagation runs entirely on the
loop goroutine over the per-instance scope tree: `applyFailed`
(`loop.go:636`) → `matchErrorBoundary` (same-activity, `boundary_watch.go:314`)
→ `matchErrorScopeChain` (the `scopePath → entry.parent` walk, innermost first,
`boundary_watch.go:383`) → else `reportUncaught` + `failFromTrack`. The chain
walk checks, per scope, an event-sub Error handler (`errorHandlerAt:445`) then
the composite's Error boundary (`errorBoundaryOn:462`), both matched by **code**.
Escalation reuses this exact walk, matched on **escalation code**, with the
three deltas above.

## §2 Requirements

### §2.1 Functional

- **FR-1 — the escalation throw initiates propagation without faulting.** An
  **Escalation Intermediate Throw** and an **Escalation End Event** emit an
  **escalation signal to the loop** carrying the escalation code and the throwing
  track (a new `evEscalate` track event, the `evTerminate` pattern
  `loop.go:378`), then behave as their kind normally does — the intermediate
  throw **returns its outgoing flows** (execution continues, non-critical), the
  end event ends its path. The throw does **not** emit a hub event
  (`emitDefinition`/`PropagateEvent`) for the escalation and does **not** fault
  the track — the ADR-006 §2.6 "non-critical, execution continues" contract.

- **FR-2 — the loop matches an escalation up the scope chain.** On `evEscalate`
  the loop runs an **escalation scope-chain match** mirroring
  `matchErrorScopeChain` (`boundary_watch.go:383`): from the throwing track's
  `scopePath`, innermost scope first, at each open scope check an armed
  **event-sub-process Escalation handler** (`escalationHandlerAt`, the peer of
  `errorHandlerAt`) then an **Escalation boundary on the composite host**
  (`escalationBoundaryOn`, the peer of `errorBoundaryOn`), both matched by
  `escalationRef` **code** (empty code = catch-all, §4.3). **First match consumes
  the escalation** (`event-handling.md:219`).

- **FR-3 — interrupting vs non-interrupting fire.** At the matched boundary/
  handler, discriminate on `cancelActivity` (the `fireBoundary` split
  `boundary_watch.go:258`):
  - **interrupting** → cancel the enclosing scope as a unit and continue the
    exception flow in the host's context (exactly `matchErrorScopeChain`'s
    `cancelScope` + `spawnForks(host, bev.Outgoing())` + `host.cancel()`);
  - **non-interrupting** → **fork**: spawn a token on the boundary's outgoing
    flow **without** cancelling the scope (the SRD-053 non-interrupting fork),
    so the escalated scope keeps running alongside the handler. A
    non-interrupting handler stays armed (it may catch a later escalation of the
    same code).

- **FR-4 — unresolved escalation is logged, never faulted, never silent.** If
  the walk reaches the root with no match, the escalation is **not faulted** —
  no `reportUncaught`, no `failFromTrack`; execution continues (the non-critical
  contract, ADR-006 §2.2/§2.6). But it is **always observable**: the engine
  **logs** the unresolved escalation (name + code + throwing node, at a defined
  level — see §4.5) **and** emits an `Unresolved`-phase observability fact. The
  spec's "typically ignore" (`event-handling.md:20`) is not "silently drop" — an
  unresolved trigger a modeller can't see is a debugging trap; **an
  uncaught/unresolved event is always logged, for every event kind** (the
  project's always-observable rule, aligning with ADR-006 §2.4's logged
  no-waiter no-op). It stays a **non-fault** — logged, not aborted.

- **FR-5 — Escalation accepted at the boundary position.** Add
  `flow.TriggerEscalation` to `boundaryTriggers` (`boundary.go:16`), so an
  Escalation boundary (interrupting or non-interrupting) constructs. The
  intermediate **catch** allow-list is **untouched** — BPMN has no Escalation
  Intermediate Catch (`conformance.md:57`: Start/IntermediateThrow/End/Boundary
  only); escalation is caught by a boundary or event-sub-process start, never
  waited on mid-flow.

- **FR-6 — event-sub-process Escalation start arms as a scope handler.** An
  event-sub-process whose triggered start carries an `EscalationEventDefinition`
  arms as a scope handler (the SRD-052 `armScopeHandlers` path,
  `scope_handler.go:61`) with **no hub waiter** — like the Error handler, it is
  resolved at the throw site by the FR-2 walk (`escalationHandlerAt`), not
  delivered. Interrupting and non-interrupting event-sub starts both supported
  (SRD-052/053 fork/cancel semantics).

- **FR-7 — the `NewEscalationEventDefintion` typo is corrected.** Rename the
  misspelled exported constructor to `NewEscalationEventDefinition` (a
  model-only symbol with no runtime callers); update the model tests. A
  deprecated alias is **not** kept — the symbol is unused outside tests (§4.6).

### §2.2 Non-functional

- **NFR-1 — propagation on the loop, no new concurrency.** The escalation match
  runs on the single loop goroutine (ADR-001 v.6 §4), like `applyFailed`; no
  waiter, no hub registration, no lock.
- **NFR-2 — reuse, don't fork the walk.** The escalation chain walk shares the
  scope-tree traversal shape of `matchErrorScopeChain`; the code-matching
  helpers (`escalationBoundaryOn`/`escalationHandlerAt`) mirror the Error ones.
  A common private walk is preferred over a copy where it does not obscure the
  Error-vs-Escalation deltas (§4.2).
- **NFR-3 — additive.** No landed event kind changes shape; the boundary
  allow-list gains one trigger, the loop gains one event kind + two match
  helpers.
- **NFR-4 — the CI gate.** `make ci` green; diff-coverage ≥95% (aim 100%) on
  touched files.
- **NFR-5 — non-critical is observable.** An escalation throw emits a
  `KindEscalation`-style fact (Thrown / Caught / Unresolved phases) so the
  propagation is observable, not just logged (§3.5, ADR-013 v.2).

## §3 Models

### §3.1 The escalation loop event (`internal/instance/`)

A new `evEscalate` track-event kind (the `evTerminate`/`evFailed` pattern),
carrying the escalation **code** and the throwing track. Emitted by the
escalation throw (FR-1); applied by the loop (FR-2) via a new `applyEscalate`
mirroring `applyFailed` (`loop.go:636`) but calling the escalation match and,
on a miss, **logging (no fault)** instead of faulting the instance.

```go
// loop.go — the escalation dispatch (logs on miss, unlike applyFailed's fault).
func (ls *loopState) applyEscalate(ctx context.Context, ev trackEvent) {
	if ls.matchEscalationScopeChain(ctx, ev.track, ev.escalationCode) {
		return
	}
	// no match → log + Unresolved fact, no fault (FR-4/§4.5); execution continues.
	ls.reportUnresolvedEscalation(ctx, ev)
}
```

### §3.2 The escalation scope-chain match (`internal/instance/escalation_watch.go`, new)

`matchEscalationScopeChain` mirrors `matchErrorScopeChain` (`boundary_watch.go:383`)
— the `scopePath → entry.parent` walk — but: matches `EscalationEventDefinition`
by code, and at a match discriminates interrupting vs non-interrupting (FR-3)
rather than always cancelling.

```go
// escalationHandlerAt / escalationBoundaryOn — the peers of the Error helpers,
// matched on escalationRef code (empty = catch-all, §4.3).
func escalationBoundaryOn(node flow.Node, code string) flow.BoundaryEvent
func (ls *loopState) escalationHandlerAt(
	path scope.DataPath, code string) *scopeHandlerWatch
```

### §3.3 Boundary + start wiring (`pkg/model/events/`)

`boundaryTriggers` gains `flow.TriggerEscalation` (`boundary.go:16`); the start
allow-list already accepts it (event-sub-process only). `intermediateCatchTriggers`
is **untouched** (no escalation intermediate catch).

### §3.4 Model correction (`pkg/model/events/escalation.go`)

`NewEscalationEventDefintion` → `NewEscalationEventDefinition` (typo fix, FR-7);
add a `MustEscalationEventDefinition` twin for symmetry with the other event
definitions.

### §3.5 Observability (`pkg/observability/`, `internal/instance/`)

A `KindEscalation` fact (or reuse `KindFault` with an escalation attribute —
§4.5) with **Thrown / Caught / Unresolved** phases, emitted at the throw, at the
matched catcher, and at an unresolved miss (paired with the FR-4 log line) — the escalation peer
of the Error `KindFault` facts (`boundary_watch.go:335`).

## §4 Analysis & decisions

### §4.1 Reuse the Error propagation, add the three deltas
The spec makes Error and Escalation the same **propagation** strategy
(`event-handling.md:17,219`); ADR-006 §2.2/§2.6 and ADR-023 §2.6 already say
Escalation rides the Error scope-chain walk. So the design is: the same
`scopePath → entry.parent` traversal (`matchErrorScopeChain`), with (a) a
non-critical throw (an event, not a fault — execution continues), (b)
**logged-not-faulted** when unresolved (a log line + `Unresolved` fact, no
instance fault — §4.5), (c) a non-interrupting boundary option.
*Alternative — a hub subscription (like Signal):* rejected — escalation is
scope-directed propagation, not broadcast; a hub delivery would lose the
innermost-first scope semantics the spec mandates.

### §4.2 A separate `evEscalate`, not overloading `evFailed`
The escalation throw is **not** a fault: overloading `evFailed`/`applyFailed`
(which faults the instance on a miss) would fight the non-critical contract. A
dedicated `evEscalate` + `applyEscalate` keeps the fault path (`failFromTrack`)
untouched and the logged-not-faulted miss explicit. *Alternative —
a boolean "critical" flag on `evFailed`:* muddies the fault dispatch with a
non-fault case; rejected.

### §4.3 Empty escalation code = catch-all
An `EscalationEventDefinition` with no `escalationRef`/empty code catches **any**
escalation (the BPMN convention, matching how an Error boundary with no code
catches any error). Matching: a catcher with a non-empty code matches only that
code; an empty-code catcher matches any. Mirrors `errorBoundaryOn`'s code
compare (`boundary_watch.go:472`), extended with the empty-code catch-all.

### §4.4 First match consumes; the throw's own token still continues
"First match consumes the trigger" (`event-handling.md:219`) — the walk stops at
the innermost matching catcher. Independently, the **throw event's own token**
continues (non-critical): an intermediate throw returns its outgoing flows. If
the match is an **interrupting** boundary on an enclosing scope, that scope —
which contains the throwing track — is cancelled, so the just-continued token
is cancelled with it (deterministic: the throw returns its flows, then the loop
applies `evEscalate` in FIFO order). A **non-interrupting** match forks beside
the still-running scope.

### §4.5 Unresolved escalation is logged (no fault), never silent
ADR-006 §2.2/§2.6 + `event-handling.md:20`: an unresolved escalation must **not
fault** the instance (unlike an unresolved Error). But "typically ignore" is
read here as **log, don't abort** — not **silently drop**. The engine choice:
an unresolved escalation is **logged** (via the ADR-002 Logger, name + code +
throwing node id) **and** emits a `KindEscalation`/`PhaseUnresolved` fact, then
execution continues. *Why:* an event thrown into the void with no trace is a
debugging trap — the same reasoning behind ADR-006 §2.4's "log a dropped/
no-waiter propagation" (§5) for signals. The rule is **cross-cutting**: every
uncaught / unresolved / no-catcher event — a signal with no listener, an
escalation with no handler — is **logged**, never silently dropped. The
log **level** is a defined choice (INFO/WARN — a no-listener signal is often
benign, an unresolved escalation more often a modelling mistake; settled in M3
against the Logger contract). *Alternative — spec-literal silent ignore:*
rejected on the always-observable principle. *Alternative — fault the instance:*
rejected — that is the Error contract, not Escalation's.

### §4.6 Fix the constructor typo now (no alias)
`NewEscalationEventDefintion` is misspelled and, being model-only, has no runtime
callers — only tests. Correcting it as part of the landing that first wires
escalation is the right moment; a deprecated alias would preserve a typo for a
symbol nobody depends on. *Alternative — keep the typo:* rejected (it would be
copied by every future escalation caller).

## §5 API / contract summary

| Surface | Change | Compatibility |
|---|---|---|
| `boundaryTriggers` | + `TriggerEscalation` | additive (previously rejected) |
| `events.NewEscalationEventDefinition` | renamed from `…Defintion`; `Must…` added | model-only rename, no runtime callers |
| `internal/instance` | `evEscalate` event, `applyEscalate`, `matchEscalationScopeChain`, `escalationBoundaryOn`, `escalationHandlerAt` | additive |
| escalation throw (`IntermediateThrowEvent`/`EndEvent` Exec) | emits `evEscalate`, continues (non-critical) | additive; a non-escalation throw is unchanged |
| observability | `KindEscalation` (or `KindFault`+attr) Thrown/Caught/Unresolved | additive |
| `intermediateCatchTriggers` | **none** (no escalation intermediate catch) | — |

## §6 Test scenarios

| # | Test (location) | Covers |
|---|---|---|
| T-1 | model (`events/escalation_test.go`) | FR-7: `NewEscalationEventDefinition`/`Must…`, `Type()`, `Escalation()`; boundary constructs with an Escalation def (FR-5); intermediate-catch still rejects Escalation |
| T-2 | escalation boundary on a sub-process, interrupting (`internal/instance/*_test.go`) | FR-2/FR-3: an escalation thrown inside a sub-process is caught by an interrupting Escalation boundary on the host — scope cancels, exception flow runs |
| T-3 | non-interrupting escalation boundary (`internal/instance/*_test.go`) | FR-3: the handler forks, the escalated scope keeps running; a second same-code escalation fires it again |
| T-4 | event-sub-process Escalation start (`internal/instance/*_test.go`) | FR-6: an armed Escalation event-sub handler catches via the chain walk (interrupting + non-interrupting) |
| T-5 | code matching + catch-all (`internal/instance/*_test.go`) | FR-2/§4.3: a coded boundary matches only its code; an empty-code boundary catches any; innermost-first precedence |
| T-6 | unresolved escalation logs, no fault (`internal/instance/*_test.go`) | FR-4/§4.5: an escalation with no catcher causes **no** instance failure and execution continues at the throw, **and** emits an `Unresolved` fact + a log line (asserted via the observer/collector) — logged, never silently dropped |
| T-7 | non-critical throw continues (`pkg/thresher/escalation_test.go`) | FR-1: `start → sub{… → throwEsc"E" → afterThrow}`; boundary"E" on sub → both the exception flow AND (for non-interrupting) the after-throw node ran; interrupting → only the exception flow |
| T-8 | e2e + example (`pkg/thresher/escalation_test.go`, `examples/escalation-events/`) | FR-1…FR-6 end-to-end; example smoked exit 0 |

## §7 Milestones

Each milestone is one commit, `make ci` green:

- **M1 — model + boundary position.** FR-5 (`boundaryTriggers`), FR-7 (typo fix
  + `Must…`); tests T-1. `feat(events): Escalation boundary position + constructor fix`.
- **M2 — throw → scope-chain propagation.** FR-1 (`evEscalate` + throw wiring),
  FR-2 (`matchEscalationScopeChain` + `escalationBoundaryOn`/`escalationHandlerAt`),
  FR-3 (interrupting + non-interrupting), FR-4 (logged-not-faulted when unmatched), FR-6 (event-sub
  arming); tests T-2…T-6. `feat(instance): Escalation propagation up the scope chain`.
- **M3 — observability + e2e + example + doc sync.** FR-1/NFR-5 (`KindEscalation`
  facts), tests T-7/T-8, `examples/escalation-events/`, CHANGELOG, conformance-
  status row 7, README/guides. `feat: Escalation events — facts, e2e, example, doc sync`.

Post-M3: `/check-srd`, §10 fill, SRD status flip, sync linked docs
(conformance-status row 7 → landed, roadmap C6 — Compensation the last #90 item,
ADR-018/ADR-023 Escalation deferral rows annotated "landed here").

## §8 Cross-doc

- Implements **ADR-006 v.4 §2.2/§2.6** (propagation; non-critical; unresolved
  non-fault — realized as logged, §4.5),
  **ADR-018 v.1** (Escalation boundary — interrupting + non-interrupting),
  **ADR-023 v.1 §2.6** (the scope-chain walk). **No new ADR** — the conception is
  decided; this SRD lands it (the ADR-018/ADR-023 "Escalation deferred to #90"
  rows are annotated "landed via this SRD" at sync).
- Upstream: **ADR-001 v.6 §4**, **ADR-009 v.1**, **ADR-013 v.2**.
- Sideways: **SRD-029** (`matchErrorBoundary`), **SRD-049** (`matchErrorScopeChain`),
  **SRD-052** (`errorHandlerAt`/`armScopeHandlers`) — the Error machinery mirrored.
- Closes GitHub epic **#90**'s Escalation item; **Compensation** remains (an
  ADR-026 + its own SRD — ADR-006 §2.3 deferral).

## §9 Definition of Done

- [ ] FR-1…FR-7 implemented; every §6 test exists and passes.
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); touched functions ≥80%.
- [ ] An unresolved escalation causes **no** instance fault **and is logged +
      `Unresolved`-fact** (T-6) — the non-critical, logged-not-silent contract held.
- [ ] Non-interrupting escalation demonstrated (T-3) — fork, scope keeps running.
- [ ] `examples/escalation-events/` runs exit 0; its built binary gitignored.
- [ ] §10 filled; conformance-status row 7 → landed; roadmap / ADR-018 /
      ADR-023 deferral rows synced.

## §10 Implementation summary

*Filled at landing: touched files/lines per milestone, verification results,
milestone commit SHAs, deltas vs this draft.*

## Open questions

- **`KindEscalation` vs `KindFault`+attribute** (§3.5/§4.5) — *resolved (M2a):* a
  dedicated **`KindEscalation`** kind (phases Thrown/Caught/Unresolved, an
  `escalation` attribute). Escalation is explicitly **not** a fault (ADR-006
  §2.6, ADR-013 v.2 taxonomy), so tagging it `KindFault` would misclassify it;
  a distinct kind keeps the fault stream fault-only.

*Implementation note (M2a):* milestone M2 was split into **M2a** (throw seam +
Boundary catch, both interrupting and non-interrupting + unresolved-log) and
**M2b** (event-sub-process Escalation start catch), for smaller reviewable
commits; the §7 milestone table describes the pre-split plan and is reconciled
in §10 at landing.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-20 | Ruslan Gabitov | Initial draft — Escalation events: the non-critical twin of Error (unresolved = non-fault, realized as *logged*, never silently dropped — §4.5), riding the landed scope-chain propagation (`matchErrorScopeChain`). Adds the Escalation boundary position (interrupting + non-interrupting), a non-fault `evEscalate` throw seam, the escalation-code scope-chain match, and event-sub-process Escalation start; corrects the `NewEscalationEventDefintion` typo. No new ADR (ADR-006 §2.2/§2.6, ADR-018, ADR-023 §2.6 decide it). |
