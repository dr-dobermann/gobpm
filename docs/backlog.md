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
construction, no setter), so a value-less Data can never be filled. Today it's a
silent footgun the lenient constructor admits; FIX-016 now makes `snapshot.New`
reject a process carrying one, and SAD-001 §14.1 records the non-execution
deviation. Open decision:
- **(B) reject at construction** (recommended) — `NewProperty`/`NewIAE` refuse a
  nil structure everywhere, fail-fast; the "declare empty, fill later" pattern
  uses a typed-zero value (`NewVariable(0)`/`""`). Small; aligns with the
  typed-valued model and validate-all-public-params.
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
