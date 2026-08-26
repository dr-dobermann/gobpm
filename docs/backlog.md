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

- **Check ADR-038's state, and why it is still `Draft`.** The converter
  coverage-boundaries record sits at v.2 `Draft` while already governing work
  (its §2.3 register drives the #335 capability queue, and ADR-039 consumed
  its #323 row). Establish whether the status is an oversight to flip or
  something is genuinely unsettled — and while at it, whether its sibling
  drafts (ADR-024 v.5, SAD-001) are in the same state for the same reason.

## Planned work / deferred

Genuinely un-homed items — not yet tracked in an ADR/SRD, the roadmap, or the
audit-backlog.

- **Wipe the version pins from the remaining SAD/ADR references.** A reference
  to a SAD or ADR now names the document and the section only — `ADR-033 §2.8`,
  not `ADR-033 v.5 §2.8`. Bumping one of them updates everything related to it
  in the same change-set, so a reference is current by construction, and the
  pin only supplies something for the next bump to falsify.

  The SRD-091 branch converted what it already had to touch: ADR-003 (bumped to
  v.2 there), the continuously-current ADRs and the RU twin that referenced it,
  the roadmap, SRD-091, ADR-037, the persistence guide, and the ADR references
  in code comments that branch added. That leaves **~2,400 pins** under
  `docs/`.

  Two rules bound the sweep. **Accepted SRD/FIX documents are not touched** — a
  one-shot document is a historical snapshot, its pin was correct when written,
  and retro-editing it rewrites what its author read. The same holds for the
  **CHANGELOG's released sections**, which record what shipped. The target is
  the continuously-current set: SAD, ADR, their RU twins, the guides, the
  roadmap and `README*`.

  Deliberately not a rider on a feature branch, and not urgent: an un-swept pin
  goes stale only when its target bumps, which the sync-linked-docs step
  catches one document at a time. It is worth a pass of its own precisely
  because leaving it to that step means every bump keeps paying the repair cost
  the convention exists to abolish.

The six entries this section carried through 2026-08-01 all
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
- **Audit findings** — disposition in
  [`audit/remediation-status.md`](audit/remediation-status.md), design deferrals
  in [`audit/audit-backlog.md`](audit/audit-backlog.md). Both maintain their own
  counts; this file does not restate them.

  The note that used to sit here — that row 11 (`memrepo` can evict an Active
  instance after a terminal→Active re-save) was the single remaining open row,
  deferred *"until persistence lands"* and now belonging to **E2** — was
  **stale**. It was remediated by SRD-078 FR-9: `Save` untracks a revived
  record from the eviction ledger, `remediation-status.md` row 11 reads
  ✅, and `TestTerminalRevivedToActiveNotEvicted` pins it. This file kept
  pointing at work that had already landed, which is the failure mode a
  backlog has: an entry costs nothing to leave behind and reads as open
  forever.
