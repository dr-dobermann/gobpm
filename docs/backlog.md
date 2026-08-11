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
- **A restore racing a previous incarnation's teardown** — [#314]. Restoring a
  message-routing instance while the instance it was captured from is still
  draining trips `-race` between `loopState.stopAll` and the new loop's first
  checkpoint. Refuted as a general property of capture-then-restore (the ad-hoc
  family is clean over 15 `-race` runs), so the trigger is narrower and unnamed.
  It blocks a multi-iteration restore test for correlated routing (SRD-085
  covers only the single-remaining-iteration shortcut), and if the shared state
  is real it is a recovery-window defect rather than a test one.

  **A second symptom, measured 2026-08-10 on `feat/adr-003-layout-close`, may
  share that root cause.** `TestIterationCorrelatedRouting` and
  `TestIterationRoutingKillAndResume` (`pkg/thresher/delivery_payload_test.go`)
  fail intermittently in a full-package run by **hanging until their deadline**,
  while passing in **0.021s** when run alone and passing when run with each
  other. No `-race` report — the delivery simply never arrives.

  What this is **not**, since the obvious hypothesis was tested and refuted: it
  is not a deadline too tight for a loaded machine. Raising the wait from 3s to
  30s moved the failure from 3.01s to 30.03s and changed nothing else. Failing
  *exactly at* the deadline is the signature of an event that never arrives, not
  of a slow one — a slow-but-arriving delivery would pass once the wait was
  extended. (The 40-site deadline sweep built on that hypothesis was reverted
  for the same reason; nothing in the tree depends on it.)

  Cross-test interference losing a delivery is what #314's second candidate
  cause predicts — "the message-routing fixture shares an event broker/hub
  between the two instances". Different surface (a hang in `pkg/thresher`, not
  `-race` in `internal/instance`), plausibly one shared object. Naming that
  object is #314's first DoD item, and this symptom is a cheaper reproduction
  of it: no race detector needed, just a full-package run.

  **Correction, same day, after more measurement: the two tests do NOT share
  one mechanism, and the paragraph above generalizes from one to both.** They
  are separated by what happens when the deadline moves:

  - `TestIterationCorrelatedRouting` **hangs regardless of the deadline** —
    raising the wait from 3s to 30s moved the failure from 3.01s to 30.03s.
    That is a delivery that never arrives, and the paragraphs above describe
    it correctly.
  - `TestIterationRoutingKillAndResume` is **load-sensitive**, not
    deadline-independent. It failed twice consecutively in `make ci`, then
    passed **9/9** when the gate's exact conditions were reproduced by hand
    (`GOMAXPROCS=4`, `-race`, `-coverprofile`, the whole package list). The
    two failures happened while `make ci` was piped through an
    output-summarizing wrapper teeing ~10k lines; the run without it passed at
    the same commit. That points at total machine load rather than at the
    engine.

  So a red gate on `KillAndResume` alone is not evidence of a lost delivery,
  and it is worth re-running before investigating. `CorrelatedRouting` failing
  IS evidence, at any deadline. Whether one root cause produces both remains
  open — which is the point of naming the shared object.
- **Audit findings** — disposition in
  [`audit/remediation-status.md`](audit/remediation-status.md), design deferrals
  in [`audit/audit-backlog.md`](audit/audit-backlog.md). Both maintain their own
  counts; this file does not restate them. Note that the single remaining open
  row (`memrepo` can evict an Active instance after a terminal→Active re-save)
  was deferred *"until persistence lands"*, and persistence has since landed
  (ADR-033) — it belongs to the durable-Repository work in **E2**.

[#313]: https://github.com/dr-dobermann/gobpm/issues/313
[#314]: https://github.com/dr-dobermann/gobpm/issues/314
