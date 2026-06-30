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

Known work, not yet scheduled into an ADR/SRD/FIX.

- **Instance god-object refactor** — `internal/instance.Instance` carries too many
  responsibilities (2026-06-11 architecture-audit §2.3). Decompose along the
  loop/track/scope/registry seams. Pairs with the `track` split below.
- **`track` package split behind a host interface** — move `track` out of
  `internal/instance` behind a narrow host interface so the compiler enforces the
  ADR-001 event-only invariant (no direct cross-track field access). Record in
  **ADR-003** after the events ADR lands.
- **Snapshot start-events precompute** — precompute the instantiating start
  triggers in `Snapshot` in a single node pass, dropping
  `scanInstantiatingStarts`' second walk. Fold into the instance-refactoring work.
- **ManualTask** — implement as a pass-through no-op, bundled with the
  Service/User-task interactor refactor (see audit-backlog **AB-002**). Keep it in
  the BPMN-spec taxonomy meanwhile.
- **Event-matching generalization** — unify EventHub subscription matching via a
  polymorphic `SubscriptionKey()` (replacing the isolated signal name-scan) when
  Link events land.
- **Runtime data access for services** — a ServiceTask reading RUNTIME variables
  (not just wired-in properties) → **ADR-011**; observe-instance-data-from-outside
  → **ADR-013**.
- **Broad codebase review** — a deferred sweep beyond the audit-driven FIXes
  (coverage-and-review backlog).
