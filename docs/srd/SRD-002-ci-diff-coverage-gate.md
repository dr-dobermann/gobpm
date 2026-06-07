# SRD-002 — CI Diff-Coverage Gate

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-07 |
| Owner | Ruslan Gabitov |
| Refines | [SAD-001 v.1 §6 Quality Attributes](../design/SAD-001-vision-and-architecture.md) |

This SRD proposes an automated **diff-coverage gate**: a check that fails CI when
the lines a change *adds or modifies* are insufficiently covered by tests —
**without** blocking on the pre-existing untouched-code coverage backlog. It
turns the project's "touched files finish at ≥80% (aim 100%)" convention from a
manual discipline into a mechanical, reproducible gate, starting at a **70%**
floor and raising over time.

## 1. Background & motivation

### 1.1 Current state (verified against the repo)

- **`make ci`** (`Makefile:143`) = `tidy-check-all lint-all-modules build-all
  test-all vuln`. It mirrors `.github/workflows/check.yml` (the `check` required
  for the protected `master`). There is **no coverage threshold** anywhere in it.
- **Coverage profiles already exist.** `test-all` (`Makefile:104-108`) runs
  `go test -race -coverprofile=coverage.txt ./...` per module — every module
  already produces a `coverage.txt`.
- **Codecov is already wired.** `check.yml` uploads coverage to Codecov
  (`Upload coverage reports to Codecov`), and the README carries a Codecov badge.
- **Dev tools are guarded.** Tools install via `make tools` behind a
  `require-tool` guard (fail-loud parity rule); the Go toolchain is pinned to
  `go1.25.11` in every `go.mod` and the workflow.

### 1.2 The problem

The coverage standard ("every file I create/update finishes at ≥80%, ideally
100%, with the change") lives only in project conventions (CLAUDE.md / the SDD
skills). It is enforced by the author *remembering* to measure — which is exactly
what failed in the ADR-001 finalization: the touched-file coverage was only
checked after an explicit prompt. Meanwhile the **aggregate** is below target
(e.g. `internal/instance` at ~69%), a deliberately deferred backlog.

A naive aggregate-threshold gate is the wrong tool: it would block on the
untouched backlog (or, set low to accommodate it, never enforce anything on new
code). What's needed is a **diff/patch-coverage** gate — judge only the
**added/changed lines**, leave untouched legacy to the separate backlog.

## 2. Goals & scope

### 2.1 Goals (in scope)

- **G1.** A reproducible **diff-coverage** check: of the lines a change adds or
  modifies (vs the PR base / merge-base), the covered fraction must be ≥ a
  configured floor, or the check fails.
- **G2.** **Local↔CI parity.** The gate runs from a single `make` target so
  "green locally ⇒ green on CI" still holds (the project's core ethos). It does
  not depend on a server-side-only computation for the pass/fail decision.
- **G3.** **Phased floor.** One configurable threshold, **starting at 70%**, with
  a documented intent to raise it (→ 80 → toward 100) as the backlog is paid down.
- **G4.** **Touched-lines scope only.** Untouched code (the aggregate backlog) is
  explicitly out of the gate; it never causes a failure.
- **G5.** **Wired into the existing gate** (`make ci` + `check.yml`) and the tool
  installed via `require-tool` + `make tools` (no silent skip).
- **G6.** **Excludes non-product code** from measurement: `generated/` (mocks),
  `*_test.go`, `examples/`, and any vendored/`doc.go`-only files.

### 2.2 Non-goals

- **N1.** Raising the **aggregate** package coverage — that is the separate
  deferred backlog (`internal/instance` to ≥80%, broad review). This gate must
  not be conflated with it.
- **N2.** 100% enforcement now — the floor is phased (G3).
- **N3.** Replacing Codecov reporting — Codecov stays for visualization/trends;
  this gate is about a deterministic pass/fail the author can reproduce locally.
- **N4.** Per-stack gates beyond Go — the repo is Go-only; revisit if a non-Go
  module appears.
- **N5.** Mutation testing / branch-vs-line debate — start with line diff-coverage.

## 3. Requirements

| # | Requirement | Acceptance |
|---|---|---|
| FR-1 | A `make cover-check` target computes diff-coverage from the per-module coverage profiles against a base ref and exits non-zero below the floor. | Running it on a branch that adds an uncovered function fails; on a fully-covered change it passes. |
| FR-2 | The floor is a single configurable value (`Makefile` var, e.g. `COVER_MIN := 70`), documented as "raise toward 80 → 100". | Changing one var changes the gate; default is 70. |
| FR-3 | Diff scope = lines changed vs the merge-base with the base branch (`origin/master` in CI; a `BASE` override locally). | A change touching only well-covered new lines passes even if the file's overall % is low. |
| FR-4 | Measurement excludes `generated/`, `*_test.go`, `examples/`, and non-product files. | A change to generated mocks or tests does not affect the gate. |
| FR-5 | Wired into `make ci` and `.github/workflows/check.yml` so the protected-`master` `check` enforces it. | CI fails a PR whose changed lines fall below the floor. |
| FR-6 | The diff-coverage tool is installed via `make tools` behind a `require-tool` guard; toolchain pinned. | Missing tool aborts with an install hint (no silent pass); local and CI use the same pinned version. |
| FR-7 | CI checks out with enough history for a merge-base diff (`fetch-depth: 0` or equivalent). | The base diff resolves on CI, not just locally. |
| NFR-1 | Local↔CI parity: the same `make cover-check` decides pass/fail in both. | The pass/fail does not depend on a server-only computation. |
| NFR-2 | Deterministic & reasonably fast — reuses the profiles `test-all` already produces; no second full test run. | `make ci` wall-clock grows only by the diff computation, not a re-test. |

## 4. Design & alternatives

### 4.1 Where the gate runs — local target vs server-side (the key decision)

| Option | Mechanics | Verdict |
|---|---|---|
| **A. Codecov patch-coverage status** | Set `codecov.yml` `coverage.status.patch.target: 70%`; Codecov (already wired) computes patch (=diff) coverage on upload and posts a commit status; add that status to branch protection. | **Rejected as the *sole* gate.** Patch coverage is exactly what we want, and effort is near-zero — but the pass/fail is computed **server-side on upload**, so it **cannot be reproduced locally**. That breaks the project's load-bearing "`make ci` green ⇒ CI green" parity rule. Keep Codecov for reporting; do not make it the gate. |
| **B. Local `make cover-check` in `make ci`** — **chosen** | A `make` target computes diff-coverage from the existing `coverage.txt` profiles against the merge-base and fails below the floor; wired into `make ci` + `check.yml`. | **Selected.** Preserves local↔CI parity (G2/NFR-1); reuses profiles `test-all` already emits (NFR-2); threshold lives in the repo. Codecov stays as the reporting complement. |
| **C. Aggregate threshold** | Fail if total package coverage < X%. | Rejected. Blocks on the untouched backlog or enforces nothing (N1). Not diff-scoped. |

### 4.2 Tooling for option B (to pin at implementation)

The target needs **changed-line** (patch) coverage from a Go coverprofile +
a git diff. Candidates, decided at the milestone-plan stage against the criteria
*(must run locally; changed-lines not whole-file; honors excludes; maintained)*:

- **`gocovdiff`** — purpose-built changed-lines coverage vs a base ref.
- **`octocov`** — coverage report + diff + thresholds; CI-and-local; config file.
- **`vladopajic/go-test-coverage`** — file/package/total thresholds + a
  diff-against-base mode; strong on excludes.
- **DIY helper** — a small Go/awk tool: parse `coverage.txt` + `git diff
  --unified=0 <base>...HEAD`, intersect changed lines with covered lines, compute
  the ratio. Zero external dependency; more code to own.

Lean: a maintained tool (gocovdiff/octocov) for changed-line patch coverage;
fall back to the DIY helper if none cleanly supports the multi-module + excludes
shape. (Dev-tool dependencies are fine — the minimal-deps rule, SAD G2, governs
the engine hot path, not the dev toolchain, which already carries mockery /
golangci-lint / govulncheck.)

### 4.3 Phasing the floor

`COVER_MIN` starts at **70**. Raise deliberately (its own tiny change-sets):
70 → 80 (the standing standard) → toward 100 as the deferred backlog is paid
down. Each raise is a one-line change gated by `make ci` staying green.

### 4.4 Milestones

1. **M1 — `make cover-check` target** (local), `COVER_MIN := 70`, excludes wired,
   base-ref override; tool added to `make tools` + `require-tool`. Prove it on a
   sample under/over-covered change.
2. **M2 — CI wiring**: add the step to `check.yml` (with `fetch-depth: 0`) and to
   the `make ci` umbrella; confirm a red/green PR behaves.
3. **M3 — docs**: README "How we work" + CLAUDE.md note that the diff-coverage
   floor is enforced; record the phasing plan.

## 5. Verification (Definition of Done)

| Check | Asserts |
|---|---|
| Under-covered change fails | A branch adding an uncovered function/lines makes `make cover-check` (and CI) exit non-zero. |
| Covered change passes | A branch whose changed lines are ≥ floor passes. |
| Backlog not blocking | Touching a low-aggregate file with well-covered new lines passes (diff-scoped, N1). |
| Excludes honored | Changes to `generated/` / `*_test.go` / `examples/` don't move the gate. |
| Parity | The same `make cover-check` decides pass/fail locally and in CI; `make ci` green ⇒ `check` green. |
| Tool guard | Missing tool aborts with an install hint; pinned version local == CI. |

**DoD:** FR-1..7 + NFR-1..2 satisfied; `make ci` green with the gate active at
`COVER_MIN=70`; a demonstration PR shows the gate catching an under-covered diff;
README + CLAUDE.md updated.

## 6. Risks & mitigations

- **Shallow CI clone breaks merge-base.** → `fetch-depth: 0` (FR-7); test the
  base-diff resolution on CI explicitly.
- **Flaky on first PR / new files.** A wholly-new file is 100% "changed lines" —
  it must meet the floor like any code; document that new files are fully in scope.
- **Generated/mocks skew.** → excludes (FR-4); verify mock regen doesn't trip it.
- **Tool drift / abandonment.** → pin the version in `make tools`; the DIY helper
  is the escape hatch (no external dep).
- **Parity erosion.** Never let the gate's decision live only in Codecov (the §4.1
  rejection); the `make` target is the source of truth.

## 7. Implementation summary

> ⚠️ TODO: filled after landing (M1–M3) — chosen tool, `COVER_MIN` location,
> commit SHAs, demonstration-PR link, and any deviations.

## 8. References

- [SAD-001 v.1 §6 Quality Attributes](../design/SAD-001-vision-and-architecture.md) — the quality concern this gate serves.
- `Makefile` (`ci`, `test-all`, `tools`, `require-tool`) and `.github/workflows/check.yml` — the gate this extends; existing Codecov upload.
- Project convention: the touched-file coverage standard (≥80%, aim 100%) this mechanizes; the deferred aggregate-coverage backlog it deliberately does **not** block on.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-07 | Ruslan Gabitov | Initial Draft. Proposes a local-first diff-coverage gate (`make cover-check`) wired into `make ci` + CI, floor 70% (phased up), diff-scoped to changed lines, excluding generated/tests/examples; Codecov patch-status rejected as sole gate (breaks local parity). Not yet implemented. |
