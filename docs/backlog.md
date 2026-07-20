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

- **Event-matching generalization** — unify EventHub subscription matching via a
  polymorphic `SubscriptionKey()` (replacing the isolated signal name-scan). Its
  intended trigger is **Link events landing** — Link being the second name-keyed
  event type, at which point the abstraction pays off (deferred there by
  SRD-020/026). NOTE (2026-07-20): Link has **not** landed — `pkg/model/events/link.go`
  is a bare, unwired struct stub (no constructor/`Type()`/runtime, absent from the
  throw/catch trigger allow-lists). This pass is due **with** the Link
  implementation, not before — see `docs/analytics/link-events-kickoff.md`.
- **Optioned-constructor doc-comment audit** — sweep every `New*` constructor whose
  doc comment enumerates its available options and reconcile each list with the
  options actually accepted. Surfaced by `NewUserTask`'s list going stale when the
  triad options were added (SRD-034 M1). A comment-only correctness pass, no
  behaviour change.
### Tracked elsewhere (not duplicated here)
- **Error-propagation & logging policy** — **DONE**: the silent-error-discard
  remediation graduated into **ADR-022** (the policy: handle-exactly-once,
  the fail-fast-vs-best-effort discriminator, the canonical attribute
  vocabulary) + **FIX-022** (the repo-wide sweep — no bare `_ =` error
  discards in production, one record per failure, canonical keys), landed
  2026-07-11. Follow-ups it spun off (below): the timer sentinel-error
  refactor, an `errcheck check-blank` lint, a `gofmt`-enforcing lint (FIX-022
  §8.3).
- **Instance god-object — size decomposition** (event-loop seam): the audit §2.3
  finding is **fully closed** — the data-plane half by SRD-032's `instanceScope`
  extraction, the loop-state/correlation half by **SRD-040** (`loopState` +
  `correlator` collaborators, one-concern-per-file split of `instance.go`).
- **Shared / persistent data — BPMN `DataStore`**: future **Persistence-layer**
  work (**ADR-010 v.2 §2.6**, **ADR-009**). Runtime/instance data access for
  services is *already* solved via the `data.Source` named-source model (the
  `RUNTIME` source, **SRD-007**).
- **Audit findings**: disposition in
  [`audit/remediation-status.md`](audit/remediation-status.md) (28 Fixed / 1 Open)
  and design deferrals in [`audit/audit-backlog.md`](audit/audit-backlog.md).
