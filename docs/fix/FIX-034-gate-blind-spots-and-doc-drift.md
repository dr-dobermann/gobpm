# FIX-034 «The gate measures less than it claims, and three decided rules are unenforced»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted (2026-07-31, branch `fix/gate-and-doc-hardening`).
**Date:** 2026-07-31.
**Author:** Ruslan Gabitov.
**Branch:** `fix/gate-and-doc-hardening` (four gaps between what the gate and the docs promise and what they check).
**Paired doc:** none (gate configuration + a doc-comment sweep).
**Upstream:** [ADR-034 v.1](../design/ADR-034-data-layer-generics-policy.md) §5 (the typed-extraction rule §1.2 leaves unenforced), [ADR-022 v.1](../design/ADR-022-error-propagation-and-logging-policy.md) (the error-handling policy behind the discard rule).

**Grounded in (internal artifacts):**
- `docs/backlog.md` — the four entries this FIX graduates (per-module coverage, the discard/assertion guard, the link check, the constructor doc-comment audit).
- [FIX-031](FIX-031-documentation-link-rot.md) — the 78 dead cross-references that motivate §1.3, and the two checker requirements its §4.1 discovered.
- [FIX-028](FIX-028-close-silent-error-discards.md) §6.1/§8.3 — the discard inventory that makes §1.2 enforceable today without a sweep.

## §1 Symptoms

None of these produce a wrong answer at runtime. They are gaps between a rule
the project has **decided** and the machinery that is supposed to hold it — so
each one degrades silently, and is only discovered by the damage it let through.

### §1.1 Symptom A: the diff-coverage gate measures one module out of five

`make ci` reports a single `diff-coverage: … PASS`, which reads as "this change
is covered". It is not: the gate sees the **root module only**.

`Makefile:214-223`:

```make
test-all:
	@set -e; for dir in $(MODULES); do \
		if [ "$$dir" = "." ]; then \
			(cd $$dir && … $(GO) test -race -count=1 -coverprofile=coverage.txt $(COVER_PACKAGES)) || exit 1; \
		else \
			(cd $$dir && … $(GO) test -race -count=1 ./...) || exit 1; \
		fi; \
	done
```

Every non-root module runs its tests with **no `-coverprofile`**, and
`cover-check` is handed `-profiles coverage.txt` — the root's. The repository has
five non-example modules:

```
.  ./runtime  ./adapters/sqlite  ./adapters/dtable  ./adapters/lua
```

so **four of them have never been diff-gated**. Both `adapters/lua` and
`adapters/dtable` landed *after* `COVER_MIN` reached 95, and CI stayed green
regardless of what they added.

### §1.2 Symptom B: two decided-against idioms are enforced by review only

- A bare `_ =` on an error-returning call. Policy: ADR-022; the sweep that
  removed them: FIX-022, closed by FIX-028.
- A `.Get(ctx).(T)` payload assertion where `data.As[T]` belongs. Policy:
  ADR-034 §5.

Both are documented decisions. Neither is checked: `.golangci.yml` enables
`errcheck` but not its `check-blank`, and enables no assertion linter
(`grep -c forbidigo .golangci.yml` → `0`). The rules hold today only because
someone remembers them at review.

### §1.3 Symptom C: nothing validates a documentation link

`make ci` and `check.yml` contain no link check of any kind. That is the
mechanism behind FIX-031: **78 dead relative cross-references** accumulated
across several refactors, including in both READMEs and the SAD, and two of its
three causes — a retired document and a renamed ADR — are exactly what a checker
catches for free.

### §1.4 Symptom D: constructor doc comments enumerate options that drift

Several `New*` constructors document their available options in prose. Those
lists go stale silently when an option is added — the precedent is
`NewUserTask`, whose list missed the triad options added by SRD-034 M1.
**15 files** under `pkg/` and `internal/` carry an `Available options` block,
and nothing reconciles them with what the constructor accepts.

## §2 Root Cause Analysis

### §2.1 A: the profile is written where the loop happens to special-case it

`MODULES` is every directory containing a `go.mod` (`Makefile:54`), so `test-all`
already iterates all of them. The coverage profile was wired only into the `.`
branch — reasonably, when the root was the only module with tests — and never
revisited as `runtime/` and the three adapters appeared. `cover-check` then
consumes one file (`Makefile`, the `cover-check` target: `-profiles coverage.txt`),
so even a module that *did* write a profile would not be read.

Nothing warns, because a missing profile is indistinguishable from a module with
no changed lines: covercheck reports on what it is given.

**Why the tests did not catch it:** there is no test of the gate. This is
configuration, and its failure mode is silence.

### §2.2 B: the linters that would enforce these are available and unused

`errcheck` is enabled but its `check-blank` option defaults to false, so
`_ = f()` is invisible to it — precisely the form the policy forbids. For the
assertion rule the repo enables nothing.

Measured before proposing, because the cost of a guard is the debt it exposes:

| Rule | Tool | Production violations today |
|---|---|---|
| bare `_ =` on an error call | `errcheck` `check-blank: true` | **3** — `console.go:127` (FIX-028 §6.1's documented carve-out), `internal/scope/scope.go:400`, `pkg/thresher/observer.go:104` |
| unchecked type assertion | `forcetypeassert` | **25**, across 13 files |

> The assertion count was first measured as 5. That figure was golangci-lint's
> `max-same-issues: 3` reporting cap, not the truth — three consecutive runs
> each reported exactly five, and each a *different* five, which is what gave
> it away. Lifting the caps reports 25. Any count taken from this linter
> without raising `max-same-issues` / `max-issues-per-linter` is a lower bound,
> never a total.

Two nearby forms are *not* violations and must not be caught: `internal/instance/jobs.go:162`
(`ps, _ = wc.WorkerConfig()`) discards a **bool**, not an error —
`WorkerConfig() (tasks.Policy, bool)`, `pkg/tasks/workerdispatcher.go:213` — and
of the 15 `.Get(ctx).(…)` sites in production, most are comma-ok or type-switch
forms that are already safe. This is why the backlog's suggested `forbidigo`
regex is the wrong instrument: it matches text, so it would flag the safe forms
too. `forcetypeassert` matches the *unchecked* shape, which is the actual rule.

`.golangci.yml` sets `tests: true`, and tests contain **34** blank-assignment
sites (`_ = data.CreateDefaultStates()` and kin) — a deliberate test idiom, not
a policy breach.

### §2.3 C: adopting a checker was blocked on a decision nobody had to make

The backlog left the choice open ("`lychee`, `markdown-link-check`, or ~20 lines
of Go in `cmd/`") *and* the policy open ("blocking, or advisory"). An item that
needs two decisions before any work starts does not get picked up. Both are
settled in §3.1.

### §2.4 D: prose duplicates a fact the compiler already knows

An `Available options` list restates the constructor's accepted option set in a
place the compiler cannot check. Duplication of a machine-known fact drifts by
default; the only question is when.

## §3 Solution

### §3.1 Alternatives considered

**A — per-module coverage**

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A1. Write a profile per module and pass all of them to `-profiles` | Gates every module; covercheck already accepts a comma-separated list | Profile paths must be module-relative and collected | ✅ **chosen** |
| A2. Merge into one profile | One file | Package paths from different modules collide; merging is a tool of its own | ❌ rejected |
| A3. Gate the root only, and say so | Zero work | Keeps a gate that reads as global and is not — the defect | ❌ rejected |

**B — the guards**

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| B1. `errcheck check-blank` + `forcetypeassert`, tests excluded from the blank rule | Both are purpose-built; measured cost is 1 + 5 sites | Two settings rather than one | ✅ **chosen** |
| B2. `forbidigo` patterns, as the backlog suggested | One mechanism | Text matching cannot tell `a, ok := v.Get(ctx).(T)` from `v.Get(ctx).(T)`; would flag ~13 safe sites and need `nolint` noise | ❌ rejected |
| B3. Leave as review guidance | No config | The premise of the item: review already missed these classes once each | ❌ rejected |

**C — the link checker**

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| C1. A small Go checker in `cmd/linkcheck`, relative links only | No new toolchain — the repo's parity rule requires every CI tool to be pinned and installed by `make tools`; Go is already there. Implements FIX-031's two requirements exactly (ignore fenced/inline code, percent-decode). Deterministic and offline | ~150 lines to own | ✅ **chosen** |
| C2. `lychee` (Rust) or `markdown-link-check` (Node) | Off-the-shelf, also checks external URLs | Adds a non-Go toolchain to `make tools` and CI, plus a network dependency that makes the gate flaky for reasons unrelated to the change | ❌ rejected |
| C3. Advisory rather than blocking | Never blocks a PR | The 78 accumulated *because* nothing failed | ❌ rejected |

**Blocking, and relative links only.** A dead relative link is deterministic,
locally reproducible and cheap to fix — unlike a coverage percentage, it cannot
legitimately wobble. External URLs are deliberately out of scope: they rot for
reasons outside the repository and would make the gate depend on the network.

**D — the doc comments**

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| D1. Reconcile all 15 lists once, in this FIX | Correct today; no new machinery | Can drift again | ✅ **chosen** |
| D2. Generate the lists from the option types | Cannot drift | Needs a generator and a marker convention for a doc-comment fragment — disproportionate | ❌ rejected (recorded in §5 as the escalation if it drifts again) |
| D3. Delete the lists | Cannot drift | Removes genuinely useful documentation at the point of use | ❌ rejected |

### §3.2 Changes by file

#### §3.2.1 `Makefile` — a coverage profile per module

`test-all` writes `coverage.txt` inside **every** module (not only `.`), each
excluding that module's generated and example packages, and records the paths;
`cover-check` receives all of them:

```make
# before: only the root branch carried -coverprofile
# after:  every module writes its own profile, and COVER_PROFILES lists them
```

`COVER_PROFILES` is derived from `MODULES` so a new module is gated the day it
appears — the property that failed here.

#### §3.2.2 `.golangci.yml` — the two guards

```yaml
linters:
  enable:
    - errcheck
    - forcetypeassert     # ADR-034 §5: an unchecked assertion is a panic where
                          # data.As[T] returns a classified error
  settings:
    errcheck:
      check-blank: true   # ADR-022: `_ = f()` is a silent discard, not handling
  exclusions:
    rules:
      - path: _test\.go
        linters: [errcheck]   # the blank-assign idiom is deliberate in tests
```

The test exclusion follows the existing `goconst: ignore-tests: true`
precedent in this file: these rules govern production discipline.

#### §3.2.3 The five unchecked assertions

Each is fixed at its own site, not silenced:

| Site | Fix |
|---|---|
| `pkg/model/activities/flowselect.go:103` | `data.As[bool]` — ADR-034 §5's exact case |
| `pkg/model/activities/subprocess.go:432,440` | comma-ok with a classified error |
| `pkg/model/process/process.go:400` | comma-ok |
| `pkg/thresher/instance_starter.go:109` | comma-ok |

#### §3.2.4 `cmd/linkcheck` + the gate — relative links are verified

A new checker walks the repository's Markdown, extracts relative link targets,
and reports the ones that do not resolve. Two behaviours are requirements, both
learned from FIX-031 §4.1:

- **fenced and inline code is skipped** — `` `values.NewArray[T](vals…)` ``
  matches a naive link regex, and eight such matches exist today;
- **hrefs are percent-decoded** — otherwise every correct link to
  `gobpm Development Roadmap.md` reports as broken.

Wired into the gate as a blocking step, and into `make tools` in the sense that
it needs nothing: it is built from this repository.

#### §3.2.5 The 15 `Available options` blocks — audited, and two shapes emerge

Each list was compared against what its constructor actually dispatches on. The
audit found four defects and, more usefully, showed that one convention does not
fit all of them:

- **A constructor that accepts a family WHOLESALE** should document the family,
  because an enumeration drifts the moment the family gains a member — which is
  exactly how the Camunda triad went unlisted. `NewServiceTask` and
  `NewUserTask` now lead with the families their type-switch names
  (`SrvTaskOption`/`UsrTaskOption`, `taskOption`, `ActivityOption`,
  `data.PropertyOption`, `foundation.BaseOption`), state that an option added to
  a listed family is accepted whether or not it is named, and list today's
  members underneath.
- **A constructor that accepts a family but REJECTS members** must keep the
  enumeration, because the family heading would be a lie. `NewEndEvent` is the
  case: `endConfig` implements `setCondition` and `setTimer` solely to refuse
  them, so its seven-trigger list is the *accepted subset* of `EventOption` and
  is correct as it stands.

The four defects fixed: `NewServiceTask` omitted `WithWorkerTrust`;
`NewUserTask` omitted the whole `ActivityOption`, `taskOption` and
`data.PropertyOption` families; `NewEventBasedGateway` omitted
`WithCorrelationKey`; `NewIAE` omitted `WithIDefinition`; and `WithIAE` named
`data.IDef` / `data.IDefinition`, neither of which exists — the functions are
`WithIDef` / `WithIDefinition`.

Verified correct and left alone: `gateways.New` and the Exclusive / Inclusive /
Parallel constructors (`GatewayOption` has one member), `process.New`,
`consinp.NewRenderer`, `flow.NewBaseNode`, `bpmncommon.NewMessage`,
`goexpr.New`, and `NewItemDefinition`. Comment-only throughout; no behaviour
change.

#### §3.2.6 `docs/backlog.md` — four entries graduate out

Marked **DONE** in place, with what was found: per-module coverage profiles,
the discard/assertion guard, the link check, the constructor doc-comment audit.

The file's header says a graduated item "leaves this list", but the convention
actually in use is to mark it DONE with a summary — both the
`Error-propagation & logging policy` entry and the examples outcome-gate that
landed the same day do that, and the note is worth more than the empty space:
it records what the item turned out to be, which is exactly what a future
reader of a closed item wants. Matching the practice rather than the header.

The **true-BPMN diagram** entry stays open: it is blocked on later converter
slices, not on this work — see §6.3.

## §4 Verification

### §4.1 Regression tests (mandatory)

| Test | Setup | Assertion |
|---|---|---|
| `cmd/linkcheck` unit tests | fixtures: a valid link, a dead link, a link inside a fence, an inline-code false positive, a percent-encoded href, an anchor-only href | dead links reported; the four non-links are not |
| the gate itself | `make ci` | four module profiles produced; `diff-coverage` reads all of them; lint clean with both new rules |

The guards need no test of their own: a linter that does not fire on a known
violation is caught by §4.2's before/after measurement.

### §4.2 Empirical gate

- **Before/after for B**: `forcetypeassert` reports 5 today and 0 after §3.2.3;
  `errcheck check-blank` reports 1 (the carve-out, annotated) and 0 after.
- **Before/after for C**: the checker reports 0 dead links on a tree FIX-031
  already cleaned — a non-zero result would mean either rot since, or a checker
  bug, and both are worth knowing before it becomes blocking.
- **Before/after for A**: a deliberate uncovered line added to `adapters/lua`
  makes the gate fail; today it does not. Removed before commit — recorded in
  §8.2, not shipped.
- `make ci` green.

### §4.3 Observability

None — this is build-time machinery.

## §5 Prevention

- **A**: `COVER_PROFILES` derives from `MODULES`, so the next module is gated on
  arrival rather than remembered.
- **B**: the rules are now the linter's, not the reviewer's memory.
- **C**: the checker runs on every PR, so link rot cannot accumulate to 78 again.
- **D**: if these lists drift a second time, escalate to D2 (generate them) —
  a second drift is evidence that prose duplicating a compiler-known fact is not
  maintainable by discipline alone.

## §6 Regressions / side-effects

### §6.1 What may rely on the old behavior

- `grep -rn "coverage.txt" Makefile .github/ ` — the profile path is referenced
  by `cover-check` and by the Codecov upload; both must follow the new list.
- Enabling `check-blank` and `forcetypeassert` can fail **other modules** whose
  code has never been linted under them; `lint-all-modules` runs every module, so
  the measurement in §2.2 (root only) is re-taken across all five before landing.
- The new gate step lengthens `make ci` — the checker is offline and file-local,
  so the cost is a directory walk.

### §6.2 Rollback path

Each of the four is independently revertible; they share no state.

### §6.3 Out of scope

The **true-BPMN example diagrams** item stays in the backlog. Measured while
scoping this FIX: the exporter supports seven element types (Start/End events,
Manual/User/Service tasks, Exclusive/Parallel gateways), so only **13 of 46**
example modules could export at all, and every example is `package main`, so
each would need a new hook to expose its definition to a generator — plus a Node
toolchain for `bpmn-to-image` and the unverified auto-layout caveat. It is a
feature gated on later converter slices and wants an SRD, not this FIX.

## §7 Related

- [FIX-031](FIX-031-documentation-link-rot.md) — the link rot §1.3 prevents recurring.
- [FIX-028](FIX-028-close-silent-error-discards.md) — the discard sweep §1.2 makes permanent.
- [ADR-034 v.1](../design/ADR-034-data-layer-generics-policy.md) §5 — the typed-extraction rule.
- [ADR-022 v.1](../design/ADR-022-error-propagation-and-logging-policy.md) — the error-handling policy.

## §8 Implementation summary

### §8.1 Stages by commit (branch `fix/gate-and-doc-hardening`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| doc | `1ae8fda` | this document | — |
| M1 | `47d291d` | per-module coverage profiles (§3.2.1) | before/after on a deliberately uncovered line |
| M2 | `613250d` | the two guards + 25 assertion fixes + `errs.Invariant` (§3.2.2/§3.2.3) | 8 new tests; `TestInvariant`, `TestAsAdapter*`, `TestCondArming*`, `TestWireClonedGraph*`, the two non-bool engine tests, `TestBoolComparison` |
| M3 | `8363e83` | `cmd/linkcheck` + the blocking gate step (§3.2.4) | 7 tests over the parsing rules and the three exit codes |
| M4 | `4367543` | the option-block audit (§3.2.5) | comment-only |
| M5 | this commit | backlog graduation, §8 (§3.2.6) | — |

### §8.2 Empirical findings — where reality diverged from the §3 draft

**The assertion count was five times the estimate, and the estimate came from
the tool itself.** §2.2 measured 5 unchecked assertions; the truth was 25 across
13 files. Three consecutive runs each reported exactly five and each a
*different* five — that inconsistency is what exposed golangci-lint's
`max-same-issues: 3` cap. Any count from that linter without raising
`max-same-issues` and `max-issues-per-linter` is a lower bound, never a total.
The corrected numbers are in §2.2.

**Ten of the 25 were deleted rather than checked, and both deletions left the
code simpler than before this FIX started.** `BoundaryEvents()` returned
`[]flow.EventNode` while `AddBoundaryEvent(flow.BoundaryEvent)` was its only
writer — the type was wider than the invariant, so every consumer asserted it
back; narrowing the accessor removed seven. The three frozen `Clone()`s now
delegate to the package's own `freeze()`, which wraps by kind: no assertion, no
panic, and shorter than the original.

**The narrowing exposed a wrong test fixture.** `stubTaskBoundary` reported an
`IntermediateCatchEvent` as a boundary event. It is not one; the wide element
type hid it, and production code asserted the value back to
`flow.BoundaryEvent` — a panic had that path ever run.

**Guards believed untestable mostly were not.** The first attempt to drive them
crashed on a hand-built `loopState`, which lacks the wiring `Run` performs. A
*completed* instance is fully wired, and with that the two conditional arming
guards, `failInvariant`, both clone guards and — via an engine stub returning a
non-bool — the two `data.As` branches all became ordinary tests. Only the
boundary-fire guard is genuinely unreachable: `boundaryWatch.boundary` is
already typed `flow.BoundaryEvent`, so no test can hand it anything else.

**The coverage gate has a second blind spot, found by this FIX's own new
package.** See §8.3.

**One convention did not fit all fifteen option blocks** — §3.2.5 records the
split between families-accepted-wholesale and families-with-rejected-members.
`NewEndEvent` was nearly "fixed" into being wrong.

**A process note, because it cost real time.** Two measurements in this work
were taken from runs whose exit status was never checked: `make test-core` was
sent to `/dev/null` and only the coverage number read afterwards, while the
tests underneath were failing — so several intermediate percentages came from a
stale `coverage.txt`. The rule the repository already states for `make ci` —
judge a gate by its own completion markers, never by a wrapper or a
side-effect — applies equally to a helper invocation.

### §8.3 Backlog (out of FIX-034 scope)

**covercheck does not measure `cmd/`, so the gate has a second blind spot.**
Found by adding `cmd/linkcheck` in M3: its files are in the git diff and its 55
blocks are in `coverage.txt`, yet covercheck reports nothing for them — the
package's 236 lines never enter the denominator. Measured directly,
`go test -cover ./cmd/linkcheck/` reported **74.3 %** while the gate said PASS,
which is exactly the failure §1.1 was written about, one directory over. Whether
it skips `cmd/` specifically or every `package main` is unknown; the tool is
external (`github.com/dr-dobermann/covercheck`), so this wants a reproduction
there rather than a workaround here. Recorded so the FIX does not claim a
completeness it lacks.

**A panicking observer is still swallowed** (`pkg/thresher/observer.go`).
`deliver` contains the panic per ADR-013 §5 but has no sink to report it to, so
a broken observer leaves no trace — the accidental-silence class ADR-022 treats
as the worse failure. Threading a logger through would have widened M2 beyond
gate hardening; it wants its own small change.

## §9 Open questions

None.
