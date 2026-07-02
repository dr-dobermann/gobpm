# Operational backlog

The project's living working list — **our** plans, deferred work, and open design
questions to analyze, independent of any single audit. It complements:

- **[Development Roadmap](analytics/gobpm%20Development%20Roadmap.md)** — *where
  we're going* (direction, releases, milestones).
- **[`audit/audit-backlog.md`](audit/audit-backlog.md)** — design items deferred
  from a *specific* code-review audit (point-in-time; `AB-NNN`).
- **[`audit/remediation-status.md`](audit/remediation-status.md)** — per-finding
  audit disposition.

This file is the durable home for the rest: deferred refactors, forward-looking
ideas, and decisions we've flagged but not yet taken. Items graduate out of here
into an ADR/SRD (when designed) or a FIX (when implemented).

## Open questions to analyze

Design decisions we've surfaced but not yet made.

### Q1 — Value-less (underspecified) Data: reject at construction, or support fill-on-write?
BPMN makes an `ItemAwareElement`'s `itemSubjectRef`/structure optional (`0..1`),
so a Property/DataObject MAY be declared underspecified. gobpm can't use one: an
`ItemDefinition`'s structure *is* its value (an immutable `Variable[T]` bound at
construction, no setter), so a value-less Data can never be filled. It was a
silent footgun the lenient constructor admits. **Registration-time rejection is
now settled and landed:** FIX-016 made `snapshot.New` reject a value-less
*process* property, and **FIX-017** extended that — via clone-is-validation — to
*activity and event* properties, so a process carrying any value-less property is
now rejected at registration uniformly. SAD-001 §14.1 records the non-execution
deviation. The practical footgun (admit-then-crash-at-runtime) is therefore
closed; what remains open is only *how early / whether to also mutate*:
- **(B) reject at construction** — `NewProperty`/`NewIAE` refuse a nil structure
  everywhere, fail-fast (earlier than registration); the "declare empty, fill
  later" pattern uses a typed-zero value (`NewVariable(0)`/`""`). Now lower-stakes
  since registration already rejects; a small consistency improvement.
- **(A) support fill-on-write** — mutable structure (install-on-first-write),
  dynamic/`any`-typed values, `Undefined → Ready` transition. A real data-model
  change with marginal payoff; deferred unless concrete need appears.

Governing: **ADR-010** (process data model). *(Was audit-backlog AB-005;
relocated here — it's our design question, not an audit finding.)*

## Planned work / deferred

Genuinely un-homed items — not yet tracked in an ADR/SRD, the roadmap, or the
audit-backlog. Each graduates out into an ADR/SRD (when designed) or a FIX (when
implemented), and leaves this list.

- **ManualTask** — implement as a pass-through no-op, bundled with the
  Service/User-task interactor refactor (see audit-backlog **AB-002**). Keep it in
  the BPMN-spec taxonomy meanwhile.
- **Event-matching generalization** — unify EventHub subscription matching via a
  polymorphic `SubscriptionKey()` (replacing the isolated signal name-scan). Its
  trigger is met — **Link events have landed** (`pkg/model/events/link.go`) — so
  the pass is now due.
- **Property configuration missing on some Activity/Event constructors.** Per
  BPMN, only Processes, Activities, and Events MAY carry Properties — but gobpm
  wires property options into only *some* constructors. `NewServiceTask` /
  `NewSendTask` / `NewReceiveTask` and `NewStartEvent` / `NewEndEvent` accept
  `data.WithProperties`; `NewUserTask` rejects it (`invalid option type`), and
  `NewIntermediateCatchEvent` / `NewIntermediateThrowEvent` / `NewBoundaryEvent`
  reject it (`config doesn't support PropertyConfigurator`). So those four node
  types cannot declare properties at all, though they structurally hold a
  properties collection (via the embedded `activity` / `Event`). Consequence:
  the FIX-017 node-clone error branch is unreachable (dead defensive code) on
  exactly those four types — it becomes live once they gain property support.
  Discovered 2026-07-02 during FIX-017; fix is to route the property option
  through those constructors' configs (make each config a `PropertyConfigurator`
  / accept the activity property option) — likely a small FIX. Grounding:
  `data.WithProperties` (`pkg/model/data/property_option.go`), the accepting vs
  rejecting constructors listed above.

### Tracked elsewhere (not duplicated here)
- **Instance god-object — size decomposition** (event-loop seam): the audit §2.3
  finding is closed (SRD-032's `instanceScope` extraction); the remaining split is
  a named deferral in **ADR-012 v.1 §2.5**.
- **Shared / persistent data — BPMN `DataStore`**: future **Persistence-layer**
  work (**ADR-010 v.2 §2.6**, **ADR-009**). Runtime/instance data access for
  services is *already* solved via the `data.Source` named-source model (the
  `RUNTIME` source, **SRD-007**).
- **Audit findings**: disposition in
  [`audit/remediation-status.md`](audit/remediation-status.md) (25 Fixed / 1 Open)
  and design deferrals in [`audit/audit-backlog.md`](audit/audit-backlog.md).
