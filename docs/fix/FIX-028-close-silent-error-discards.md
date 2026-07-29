# FIX-028 «Invariant-only errors silently discarded in the diff walk and task parameter access»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted (2026-07-29, branch `fix/engineering-odds-and-ends`, landed).
**Date:** 2026-07-29.
**Author:** Ruslan Gabitov.
**Branch:** `fix/engineering-odds-and-ends` (bundles the small engineering-backlog
closures; sibling doc FIX-029 shares the branch — the `fix/audit-remediation-2026-06`
bundling precedent).
**Paired doc:** none (local to `pkg/model/data` / `pkg/model/activities`).
**Upstream:** [ADR-022 v.1](../design/ADR-022-error-propagation-and-logging-policy.md)
(silent discards are forbidden; the §2.3 logger-less carve-out);
[ADR-011 v.7](../design/ADR-011-process-data-flow.md) §2.9.4 (the commit-diff
walk the first symptom lives in).

**Grounded in (internal artifacts):**
- The queued repo-wide error-discard sweep (owner directive: one pass, not
  piecemeal). This FIX **is** that pass — §6.1 records the full inventory and
  the classification of every non-fixed site.

## §1 Symptoms

No runtime artifact exists — the defect class is *latent by construction*:
both sites discard an error that is impossible while invariants hold, so a
violated invariant (a future refactor breaking a guard, a lying `Collection`
implementation, memory corruption) would be **masked**, not reported. The
observable-when-it-matters failures would be:

### §1.1 Symptom A: a corrupted collection diffs as nil — conditional events misfire

`diffCollections` reads both sides positionally and ignores `GetAt` errors:

```go
// pkg/model/data/diff.go:196,200
if i < oldN {
    ov, _ = oldV.GetAt(ctx, i)
}
if i < newN {
    nv, _ = newV.GetAt(ctx, i)
}
```

If `GetAt` ever failed there, the element would silently diff as `nil` —
producing a false `Deleted`/`Added`/`Changed` record or masking a real one.
The walk's output feeds the commit-diff change stream
(`internal/scope/scope.go:381` → `data.DiffValues(...)`), which is the
Conditional-Event substrate (ADR-011 v.7 §2.9.4): a masked slot means a
conditional event silently not firing, far from the cause.

### §1.2 Symptom B: a task with an invalid direction yields empty parameters, no error

```go
// pkg/model/activities/task.go:529
func (t *task) getParams(dir data.Direction) []*data.ItemAwareElement {
    params, _ := t.IoSpec.Parameters(dir)
    ...
```

If `Parameters` ever failed, `getParams` would return an empty slice —
`Outputs()`/`Inputs()` (the `flow.AssociationSource`/`AssociationTarget`
contracts, `task.go:517`/`task.go:565`) would report a task with *no
parameters*, and data associations would quietly copy nothing.

## §2 Root Cause Analysis

### §2.1 The discards mask *invariant-only* error surfaces

Per-site classification against the callee's **actual** error surface (the
WaiterFired precedent — classify by what the callee can really return, not a
generic best-effort label):

- **`Array.GetAt` / `Collection.GetAt`** (`pkg/model/data/values/array.go:243`)
  errors only on a non-`int` index (`checkValue[int]`) or an out-of-range index
  (`checkIndex`). In `diffCollections` the index is a loop `int` bounded by
  `i < oldN` / `i < newN` where `oldN, newN` come from `Count()` — **both
  failure modes are impossible while the invariant holds**.
- **`InputOutputSpecification.Parameters`** (`pkg/model/data/io_spec.go:48`)
  errors only on `dir.Validate()` failure. Both `getParams` call sites pass
  the package constants `data.Output` (`task.go:518`) and `data.Input`
  (`task.go:566`) — **the failure mode is impossible while the constants are
  valid directions**.

An impossible error is exactly the case the fail-fast rule reserves: silently
converting it to a zero value turns an invariant violation into wrong data
downstream. Log-and-continue is equally wrong — there is nothing to continue
*with*.

### §2.2 Why these two sites are unlike the walk's or-nil helpers

The neighbouring `fieldOrNil` (`diff.go:134`) and `entryOrNil` (`diff.go:175`)
also swallow errors — **deliberately and correctly**: the record/map walk
descends over the *union* of keys, so "key absent on one side" is an expected
outcome that *is* the diff signal (`Added`/`Deleted`). Their error surfaces
include legitimate absence (`Field` → `ObjectNotFound`); the collection sites'
do not (the index guard removes absence). Same syntax, opposite semantics —
which is why the collection discards survived the walk's design review.

### §2.3 The audited non-defects (recorded so the sweep can close)

- `pkg/interactor/console/console.go:113` `_, _ = fmt.Fprintf(d.w, ...)` —
  an **explicit, documented carve-out**: the doc comment cites ADR-022 v.1
  §2.3 (the Driver *is* the output channel; no logger to report to). Verified
  correct; untouched.
- Every other `, _ :=` hit in library code is a **comma-ok type assertion or
  comma-ok map/bool API**, not an error discard — capability probes
  (`authz.(observability.LogRedactor)`, `node.(interactor.HumanTask)`,
  `ew.WorkerTopic()` → `(Topic, ok bool)`, `linkDefName` → `(string, bool)`,
  `strings.Cut`, `debug.ReadBuildInfo`, `adapterCache.LoadOrStore`). The full
  audit greps and per-hit classification are in §6.1.

### §2.4 Test coverage of the invariant branches

None: `grep -rnF "diffCollections" pkg/model/data/*_test.go` → 0 hits;
`grep -rnF "getParams" pkg/model/activities/*_test.go` → 0 hits. No test
exercises either discard branch — they are unreachable through the public
walk with well-behaved values (which is the point: covering them needs a
lying-`Collection` stub / an invalid direction).

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. Or-nil helper (align with `fieldOrNil`/`entryOrNil`) | uniform look | masks an *impossible* state as data; §2.2 shows the semantics are opposite — absence is expected there, forbidden here | ❌ rejected |
| B. Thread `error` up the walk (`DiffValues` → `([]Change, error)`) and out of `getParams` (interface change) | textbook propagation | breaks the landed `DiffValues` public contract and the `flow.AssociationSource`/`Target` model-wide interfaces — recursive signature churn across 6+ functions and every implementor, for branches that cannot fire | ❌ rejected |
| C. `errs.Panic` on the impossible branch, with the invariant named in a comment | loud at the violation point; zero API change; the established house idiom for impossible states (5 sites in `pkg/model/events` — `signal.go:101`, `link.go:56`, …) | a genuine violation crashes the goroutine — accepted: that is what fail-fast means for a corrupted invariant | ✅ chosen |

### §3.2 Changes by file

#### §3.2.1 `pkg/model/data/diff.go` — fail-fast on the impossible `GetAt` error

```go
// before:
if i < oldN {
    ov, _ = oldV.GetAt(ctx, i)
}

// after:
if i < oldN {
    var err error
    if ov, err = oldV.GetAt(ctx, i); err != nil {
        // i is an int in [0, Count()) — GetAt cannot fail here unless the
        // Collection contract is violated; surface the corruption loudly
        // (FIX-028) instead of diffing the slot as nil.
        errs.Panic(err)
    }
}
```

Same shape for the `newV` read. The comment names the invariant; the walk's
signature (and the `DiffValues` public contract) stays untouched.

#### §3.2.2 `pkg/model/activities/task.go` — fail-fast on the impossible `Parameters` error

```go
// before:
params, _ := t.IoSpec.Parameters(dir)

// after:
params, err := t.IoSpec.Parameters(dir)
if err != nil {
    // getParams is called only with the data.Input/data.Output constants
    // (Outputs/Inputs) — Parameters cannot fail here unless the Direction
    // constants are broken; fail loudly (FIX-028) instead of reporting a
    // parameterless task.
    errs.Panic(err)
}
```

#### §3.2.3 `pkg/model/data/diff_test.go` — regression test, symptom A

Black-box (the file is `package data_test`; `DiffValues` is public): a stub
`Collection` whose `Count()` reports 1 but whose `GetAt` always errors (the
lying-implementation case) → `require.Panics` on the walk.

#### §3.2.4 `pkg/model/activities/task_internal_test.go` — regression test, symptom B (new file)

White-box, following the package's `*_internal_test.go` convention
(`brule_task_internal_test.go`, `receive_task_options_internal_test.go`):
`getParams` with an invalid `Direction` → `require.Panics`; the
valid-constant paths (`Outputs()`/`Inputs()`) still return normally.

## §4 Verification

Current coverage in the test dirs: unit — extensive on the happy walk
(`diff_test.go`, landed with SRD-044) and task I/O; **none** on either discard
branch (§2.4).

### §4.1 Regression tests (mandatory)

| Test | Setup | Assertion |
|---|---|---|
| `TestDiffCollectionsFailFastOnBrokenGetAt` | stub `Collection`: `Count()=1`, `GetAt` → error; `DiffValues` over it | panics (via `errs.Panic`); no nil-slot diff emitted |
| `TestGetParamsFailFastOnInvalidDirection` | a built `task`; call `getParams` with an invalid direction | panics; `Outputs()`/`Inputs()` with valid constants still return normally |

Coverage standard: both touched files finish ≥95% on changed lines (target
100% — the panic branches are coverable via `require.Panics`).

### §4.5 Observability

`errs.Panic` produces a classified panic value — a violated invariant surfaces
as a crash at the corruption point with the causing error attached, instead of
a wrong diff / empty parameter list N steps later.

## §5 Prevention

- The §3.2.1/§3.2.2 comments name the invariant *and* the FIX, so a future
  refactor that weakens the guard trips over the rationale.
- The §4.1 tests are the canaries: if they fall, the fail-fast regressed to a
  silent discard.
- Backlog (not this FIX): a lint guard (`forbidigo`/custom) rejecting new
  `_ =` on error-returning calls in library paths — noted in §8.3 alongside
  the ADR-034 v.1 §5 `.Get(ctx).(` guard; one lint-config pass can carry both.

## §6 Regressions / side-effects

### §6.1 What may rely on the old behaviour — the closing sweep inventory

Audit greps run over `pkg/` + `internal/` (non-test, non-generated):

```
grep -rn "^\s*_ = \|, _ = \|, _ :=" --include=*.go pkg/ internal/ | grep -v _test.go
```

Classification of every hit: **2 defects** (this FIX — §1.1, §1.2);
**1 documented carve-out** (console `printf`, ADR-022 v.1 §2.3 — §2.3);
**all remaining hits are comma-ok assertions / bool APIs** (§2.3 list), which
are total by language definition — no error exists to discard. No caller can
observe the old masked-nil/empty-slice behaviour on the fixed sites, because
the error branches are unreachable while invariants hold — the change is
behaviourally invisible until a real corruption, which is the point.

### §6.2 Rollback path

Single-commit revert; no data or contract migration.

## §7 Related

- [ADR-022 v.1](../design/ADR-022-error-propagation-and-logging-policy.md) —
  the no-silent-discard policy this FIX closes out repo-wide.
- [ADR-011 v.7](../design/ADR-011-process-data-flow.md) §2.9.4 — the
  commit-diff walk (symptom A's blast radius: conditional events).
- FIX-026 — the sibling library-paths hygiene pass (Must-constructors), same
  spirit.
- SRD-044 — landed the diff walk this FIX hardens.
- FIX-029 (same branch) — the examples run-step; independent scope.

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

### §8.1 Stages by commit (branch `fix/engineering-odds-and-ends`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| doc | `e291f86` | this document (Draft) | — |
| 1 | `4bd875e` | §3.2.1–§3.2.4: both guards + both canaries | 2 (the new-side walk canary runs as two subtests) |

Verification: `make ci` green; diff-coverage **100.0% of 17 changed coverable
lines** (`task.go` 7/7, `diff.go` 10/10); touched functions `diffInto`,
`diffCollections`, `getParams` at 100%; `conditional-events` and
`multi-instance-sequential` examples smoke-ran to exit 0.

### §8.2 Empirical findings — where reality diverged from the §3 draft

**One canary covers two branches, not one.** The drafted single
`TestDiffCollectionsFailFastOnBrokenGetAt` case panicked on the *old*-side
guard before the new-side guard could ever execute, leaving `diffCollections`
at 91.7% — the coverage gate caught it. The landed test runs two subtests
(broken old side; empty old + broken new side) so both §3.2.1 guards are
exercised. Lesson for symmetric guards: one failing fixture cannot cover both
arms — the first panic shadows the second.

### §8.3 Backlog (out of FIX-028 scope)

- The combined lint guard: `forbidigo` patterns for bare `_ =` on
  error-returning calls **and** `.Get(ctx).(` assertions (ADR-034 v.1 §5) —
  one lint-config pass, separate change-set.

## §9 Open questions

None.
