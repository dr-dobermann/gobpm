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

- **Generated true-BPMN example diagrams (rides ADR-024) — now unblocked** — the
  example READMEs carry hand-written mermaid *approximations* of BPMN (no event
  icons, no attached-boundary notation — mermaid has no BPMN diagram type; added
  2026-07-22). The exporter this waited on landed with SRD-051
  (`convert.Export(ctx, convert.BPMN, w, p)`, 2026-07-30), so the next step is a
  make/CI step that runs each example's own process definition through it +
  `bpmn-to-image` → true-notation SVGs that regenerate from the code and can
  never drift. Caveat discovered at landing: export emits **no** Diagram
  Interchange, so `bpmn-to-image` must auto-layout — verify that before
  committing to the pipeline. Only examples built from the SRD-051 §FR-8 subset
  will export; anything richer raises `UnsupportedElementError` until later
  slices land. Optional interim: a mermaid v11 "BPMN-ish" shape convention
  (`dbl-circ` ends, `diam` gateways, `subproc` frames + a classDef palette) —
  pilot one example against GitHub's renderer first.

Genuinely un-homed items — not yet tracked in an ADR/SRD, the roadmap, or the
audit-backlog. Each graduates out into an ADR/SRD (when designed) or a FIX (when
implemented), and leaves this list.

- **Optioned-constructor doc-comment audit** — sweep every `New*` constructor whose
  doc comment enumerates its available options and reconcile each list with the
  options actually accepted. Surfaced by `NewUserTask`'s list going stale when the
  triad options were added (SRD-034 M1). A comment-only correctness pass, no
  behaviour change.
- **Discard/assertion lint guard** — one `golangci-lint` config pass adding
  `forbidigo` (or equivalent) patterns for two idioms the codebase has decided
  against: a bare `_ =` on an error-returning call outside the documented
  carve-outs (FIX-028 §8.3) and a `.Get(ctx).(T)` payload assertion where
  `data.As[T]` belongs (ADR-034 v.1 §5). Both are guidance today, enforced by
  review only.
- **Per-module coverage profiles** — `test-all` writes `coverage.txt` only for
  the root module (`if [ "$$dir" = "." ]`), so `cover-check` measures the diff
  in core and nowhere else. `runtime/` and `adapters/{lua,dtable}` have never
  been diff-gated: both adapters landed *after* `COVER_MIN` went to 95 and CI
  stayed green regardless of what they added. Fix is a Makefile/covercheck
  change — one profile per module, all of them passed to `-profiles` — and it
  benefits four modules, so it does not belong to any single SRD. Surfaced by
  SRD-051, which sidestepped it by keeping the BPMN converter in the root
  module (SRD-051 v.2 §4.1); recorded here so the general gap is not mistaken
  for closed.
- **`TestFailedWakeRetriesAndSucceeds` is flaky** (`pkg/thresher`) — observed
  failing once inside a full `GOMAXPROCS=4 go test -race ./...` sweep on
  2026-07-30, then not reproduced in ~50 targeted runs (`-count=30 -race`
  focused, `-count=20` on a clean tree) nor in a repeat full-suite run. The
  test drives the FIX-027 wake-retry backoff, so a load-sensitive timing
  assumption is the obvious suspect, but nothing is proven — a one-off
  observation, not a diagnosis. Worth a deliberate reproduction attempt under
  CPU contention before it bites in CI; graduates to a FIX once the cause is
  actually identified.
- **A pre-canceled instance can settle `Completed` instead of `Terminated`**
  (`internal/instance/TestTerminatedOnPreCanceledContext`) — observed in a full
  `make test` run on 2026-07-30 and reproduced with
  `GOMAXPROCS=8 go test ./internal/instance
  -run '^TestTerminatedOnPreCanceledContext$' -count=2000` (also approximately
  1 failure per 1000 ordinary targeted runs). This is an ordering race, not a
  data race: `loop` spawns the initial track before selecting between the
  already-closed `ctx.Done()` and the track's ready `evEnded`. Go gives ready
  `select` cases no priority; if `evEnded` wins for the last active track,
  `active` reaches zero while `stopping` remains false, so final settlement
  chooses `Completed`. The test's comment claiming deterministic cancellation
  precedence is therefore false. The fix must give the instance-loop context
  precedence before terminal-event accounting (not merely increase the test
  timeout, and not inspect a per-track context, which boundary events may
  legitimately cancel), then update the regression test to await `Instance.Done()`
  and assert exact `Terminated`. This predates and is independent of the BPMN
  converter changes.
- **No automated Markdown link check** — nothing in `make ci` or
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
