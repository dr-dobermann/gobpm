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

_None currently._ **Q1** — value-less (underspecified) Data — was **resolved by
FIX-018** (option B, reject at construction): `NewProperty` / `NewProp` return an
error and `MustProperty` / `MustProp` panic for a value-less property, so
fill-on-write (option A) is moot. FIX-016/017's clone-site rejection remains as
the data-layer clone precondition. Governing: **ADR-010**.

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
- **Optioned-constructor doc-comment audit** — sweep every `New*` constructor whose
  doc comment enumerates its available options and reconcile each list with the
  options actually accepted. Surfaced by `NewUserTask`'s list going stale when the
  triad options were added (SRD-034 M1). A comment-only correctness pass, no
  behaviour change.
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
