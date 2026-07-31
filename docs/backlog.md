# Operational backlog

The project's living working list — **our** plans, deferred work, and open design
questions to analyze, independent of any single audit. It complements:

- **[Development Roadmap](analytics/gobpm%20Development%20Roadmap.md)** — *where
  we're going* (direction, releases, milestones).
- **[`audit/audit-backlog.md`](audit/audit-backlog.md)** — design items deferred
  from a *specific* code-review audit (point-in-time; `AB-NNN`).
- **[`audit/remediation-status.md`](audit/remediation-status.md)** — per-finding
  audit disposition.

This file holds the **short-term** rest: work we intend to pick up soon, and
decisions we've flagged but not yet taken. Items graduate out of here into an
ADR/SRD (when designed) or a FIX (when implemented).

**Long-term and blocked work belongs in GitHub issues, not here.** An item
waiting on something that isn't scheduled — another subsystem, a decision not
yet due — has no business in a working list; keeping it here splits the backlog
across two sources and makes neither trustworthy. Move it to an issue with what
was measured and what would unblock it, and drop it from this file.

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

- **Optioned-constructor doc-comment audit** — **DONE** (2026-07-31, FIX-034
  §3.2.5). The audit found four defects — `NewServiceTask` missing
  `WithWorkerTrust`, `NewUserTask` missing three whole option families,
  `NewEventBasedGateway` missing `WithCorrelationKey`, `NewIAE` missing
  `WithIDefinition`, and `WithIAE` naming two functions that do not exist — and
  showed that one convention does not fit every block: a constructor accepting a
  family WHOLESALE documents the family (an enumeration drifts as the family
  grows), while one that accepts a family and REJECTS members keeps its
  enumeration, because the family heading would be a lie (`NewEndEvent` refuses
  the Conditional and Timer triggers). Original text: sweep every `New*` constructor whose
  doc comment enumerates its available options and reconcile each list with the
  options actually accepted. Surfaced by `NewUserTask`'s list going stale when the
  triad options were added (SRD-034 M1). A comment-only correctness pass, no
  behaviour change.
- **Examples assert their own outcome** — **DONE** (2026-07-31). All 46 example
  modules now compare the outcome they claim to demonstrate — the branch taken,
  the execution order, the value read back, the version resolved — and fail
  when it differs, so the existing exit-0 run-step became an outcome gate with
  no new CI machinery. Assertions record from inside the tasks (synchronous
  with the run) rather than from engine observers, whose facts arrive
  asynchronously and can still be in flight when `WaitCompletion` returns;
  the exception is `DataChange`, which is observer-only, and those examples
  cancel the subscription first so the drain settles it. Golden-output files
  stayed rejected for the reasons recorded here. Two examples turned out to be
  broken and were fixed in the same work: `timer-event` faulted on every run
  (a ServiceTask on an implementation-less operation) and `simple-timer`
  demonstrated scheduled instantiation, which the engine deliberately does not
  do. Landed as plain commits on `test/examples-assert-outcome`; no design doc.
- **Discard/assertion lint guard** — **DONE** (2026-07-31, FIX-034 §3.2.2/§3.2.3).
  `errcheck check-blank` and `forcetypeassert` are enabled; the 25 unchecked
  assertions they exposed are fixed (ten deleted outright by narrowing
  `BoundaryEvents()` and delegating the frozen `Clone()`s to `freeze()`), and
  branches no input can reach report `errs.Invariant` — the one construct the
  coverage gate excludes, so `grep -rn "Invariant("` lists every such branch.
  `forbidigo`, which this entry proposed, was the wrong instrument: it matches
  text and cannot tell a comma-ok assertion from an unchecked one. Original
  text: one `golangci-lint` config pass adding
  `forbidigo` (or equivalent) patterns for two idioms the codebase has decided
  against: a bare `_ =` on an error-returning call outside the documented
  carve-outs (FIX-028 §8.3) and a `.Get(ctx).(T)` payload assertion where
  `data.As[T]` belongs (ADR-034 v.1 §5). Both are guidance today, enforced by
  review only.
- **Per-module coverage profiles** — **DONE** (2026-07-31, FIX-034 §3.2.1).
  Every module writes its own profile and `COVER_PROFILES` derives from
  `CORE_MODULES`, so a new module is gated the day it appears. Proven by
  before/after on four deliberately uncovered lines in `adapters/lua`: the old
  invocation reported "100.0% of 0 changed coverable lines — PASS", the new one
  "0.0% of 4 — FAIL". One residual gap belongs to covercheck itself and not to
  this repo — it does not measure files under `cmd/`, diagnosed in FIX-034 §8.3
  and worked around by keeping command logic in ordinary packages.
  Original text: `test-all` writes `coverage.txt` only for
  the root module (`if [ "$$dir" = "." ]`), so `cover-check` measures the diff
  in core and nowhere else. `runtime/` and `adapters/{lua,dtable}` have never
  been diff-gated: both adapters landed *after* `COVER_MIN` went to 95 and CI
  stayed green regardless of what they added. Fix is a Makefile/covercheck
  change — one profile per module, all of them passed to `-profiles` — and it
  benefits four modules, so it does not belong to any single SRD. Surfaced by
  SRD-051, which sidestepped it by keeping the BPMN converter in the root
  module (SRD-051 v.2 §4.1); recorded here so the general gap is not mistaken
  for closed.
- **A panicking observer is swallowed without a trace**
  (`pkg/thresher/observer.go`) — `deliver` contains the panic so one bad
  observer cannot crash the drain goroutine or affect the others (ADR-013 §5),
  but it has no sink to report it to, so a broken observer leaves no evidence
  at all. That is the accidental-silence class ADR-022 treats as the worse
  failure: the engine keeps running and the operator never learns their
  observer is dead. Surfaced by FIX-034 §8.3, which annotated the discard
  rather than widening that FIX beyond gate hardening. The fix is small but not
  free — `deliver` is a free function with no logger in scope, and `Logger()`
  is not directly on `Instance` — so it wants its own change rather than a
  drive-by.
- **No automated Markdown link check** — **DONE** (2026-07-31, FIX-034 §3.2.4).
  `cmd/linkcheck` walks the repository's Markdown and fails the gate on any
  relative link that does not resolve; it is blocking, offline and built from
  this repo, so no toolchain or network joins the gate. Both requirements this
  entry recorded are tested: fenced and inline code are skipped, and hrefs are
  percent-decoded. The open question it left — blocking or advisory — was
  settled as blocking, because the 78 dead links accumulated precisely because
  nothing failed. Original text: nothing in `make ci` or
  `check.yml` validates relative documentation links, which is how the 78 dead
  cross-references [FIX-031](fix/FIX-031-documentation-link-rot.md) swept up
  accumulated unnoticed across several refactors. Two of the three causes there
  (a retired doc, a renamed ADR) are exactly what a checker catches for free,
  and both READMEs and the SAD were among the casualties. Adopt one
  (`lychee`, `markdown-link-check`, or ~20 lines of Go/Python in `cmd/`) and
  wire it into the docs half of the gate. Two requirements fall out of the
  sweep: it must **ignore fenced and inline code** — Go generic signatures like
  `` `values.NewArray[T](vals…)` `` match a naive link regex, and there are
  eight such matches today (FIX-031 §4.1) — and it must **percent-decode**
  hrefs, or every correct link to `gobpm Development Roadmap.md` reports as
  broken. Open question for whoever takes it: blocking, or advisory like the
  Codecov patch status.
### Tracked elsewhere (not duplicated here)
- **Event-matching generalization** — **RETIRED** (2026-07-30). The premise was
  that Link would be the second name-keyed event type, making a polymorphic
  `SubscriptionKey()` pay off. Link landed as a **static graph redirect**
  (SRD-057 — resolved at `WireClonedGraph`, no hub, no waiter), so Signal
  remains the only name-matched subscription and the abstraction has no second
  consumer. Revisit only if a genuinely name-keyed event type appears.
- **Error-propagation & logging policy** — **DONE**: the silent-error-discard
  remediation graduated into **ADR-022** (the policy: handle-exactly-once,
  the fail-fast-vs-best-effort discriminator, the canonical attribute
  vocabulary) + **FIX-022** (the repo-wide sweep — no bare `_ =` error
  discards in production, one record per failure, canonical keys), landed
  2026-07-11; **FIX-028** (2026-07-29) closed the residue — the last two
  invariant-only discards now fail fast, and its §6.1 inventory classifies
  every remaining bare-discard site (one documented carve-out; the rest are
  comma-ok assertions). Follow-ups it spun off: the timer sentinel-error
  refactor, an `errcheck check-blank` lint, a `gofmt`-enforcing lint (FIX-022
  §8.3) — the lint half now tracked above as the discard/assertion guard.
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
