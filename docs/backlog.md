# Operational backlog

The project's living **short-term** working list: work we intend to pick up
soon, and decisions we have flagged but not yet taken. It complements:

- **[Development Roadmap](analytics/gobpm%20Development%20Roadmap.md)** — *where
  we're going* (the engine and server tracks, milestones E0–E5 / S0–S6).
- **[`audit/audit-backlog.md`](audit/audit-backlog.md)** — design items deferred
  from a *specific* code-review audit (point-in-time; `AB-NNN`).
- **[`audit/remediation-status.md`](audit/remediation-status.md)** — per-finding
  audit disposition.

Items graduate out of here into an ADR/SRD (when designed) or a FIX (when
implemented), and leave this file. The narrative stays in the document that
closed them, not here.

**Long-term and blocked work belongs in GitHub issues, not here.** An item
waiting on something that isn't scheduled — another subsystem, a decision not
yet due — has no business in a working list; keeping it here splits the backlog
across two sources and makes neither trustworthy. Move it to an issue with what
was measured and what would unblock it, and drop it from this file. Roadmap
work is milestoned there too, not restated here.

## Open questions to analyze

Design decisions we've surfaced but not yet made.

_None currently._

## Planned work / deferred

Genuinely un-homed items — not yet tracked in an ADR/SRD, the roadmap, or the
audit-backlog.

_None currently._ The six entries this section carried through 2026-08-01 all
landed: the optioned-constructor doc-comment audit, the discard/assertion lint
guard, per-module coverage profiles and the Markdown link check closed with
[FIX-034](fix/FIX-034-gate-blind-spots-and-doc-drift.md); the swallowed observer
panic with [FIX-035](fix/FIX-035-observer-silence-and-attribute-vocabulary.md);
and "examples assert their own outcome" as plain commits on
`test/examples-assert-outcome`.

## Tracked elsewhere (not duplicated here)

- **Event-matching generalization** — **RETIRED** (2026-07-30). The premise was
  that Link would be the second name-keyed event type, making a polymorphic
  `SubscriptionKey()` pay off. Link landed as a static graph redirect (SRD-057),
  so Signal remains the only name-matched subscription and the abstraction has
  no second consumer. Revisit only if a genuinely name-keyed event type appears.
- **The loop/MI decorator as the single event processor** — [#313]. A
  multi-instance or looped activity that both iterates *and* parks on an event
  needs the decorator to own the node's event registration: one
  `EventProcessor` for all iterations, substituting the Instance on the node's
  registration calls, so the node never learns whether it runs bare or
  decorated. Until it lands, `snapshot.New` **refuses** such a model at build
  time with a message pointing at the sub-process workaround (SRD-086) — the
  refusal is the placeholder, not a fix.
- **Audit findings** — disposition in
  [`audit/remediation-status.md`](audit/remediation-status.md), design deferrals
  in [`audit/audit-backlog.md`](audit/audit-backlog.md). Both maintain their own
  counts; this file does not restate them. Note that the single remaining open
  row (`memrepo` can evict an Active instance after a terminal→Active re-save)
  was deferred *"until persistence lands"*, and persistence has since landed
  (ADR-033) — it belongs to the durable-Repository work in **E2**.

[#313]: https://github.com/dr-dobermann/gobpm/issues/313
