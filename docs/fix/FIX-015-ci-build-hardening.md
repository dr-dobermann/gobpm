# FIX-015 — CI / build-hardening sweep

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-06-30 |
| Owner | Ruslan Gabitov |
| Related | [ADR-003 v.1 Module layout](../design/ADR-003-module-layout.md), [ADR-004 v.1 Runtime environment contract](../design/ADR-004-runtime-environment-contract.md) |

One-shot remediation of four CI / build-tooling defects surfaced by
`docs/audit/code-review-third-pass-2026-06-29.md` (§2.12, §3.15, §3.16, §3.18).
All four are in the build harness (`Makefile`, `.golangci.yml`), not Go code:
they tighten the gates themselves so a vulnerability, a cached race flake, or an
unlinted test slips through less easily. No production code changes; no public
contract changes.

> **Excluded.** The fifth CI finding, §3.17 (the `depguard` core-import rule
> blocking the future runtime server binary), is parked as **AB-004**: under
> `lint-all-modules`' per-module path resolution the naive carve-outs don't work,
> and the correct multi-module lint-config fix is best validated against a real
> `runtime` import (ADR-004 server wiring), not the current stub.

## 1. Symptoms

- **1.1 (§2.12) `govulncheck` scans only the root module.** The `vuln` target
  (`Makefile:152-154`) runs `govulncheck ./...` from the repo root. Go's `./...`
  **prunes nested modules**, so the scan never descends into `runtime/`,
  `adapters/sqlite/`, or any `examples/*` module — each its own `go.mod`. A
  vulnerable dependency in `runtime` or `adapters` (both slated to gain
  third-party deps per ADR-004) would pass CI undetected. Every other multi-module
  step (`build-all`, `test-all`, `lint-all-modules`) already loops over
  `$(MODULES)`; `vuln` is the lone exception.
- **1.2 (§3.15) Test targets lack `-count=1`; cached `-race` masks flakes.**
  `test-all` runs `go test -race …` (`Makefile:124,126`) with the Go test cache
  enabled, so a second `make ci` / `make test-all` in the same checkout replays
  `(cached)` results instead of re-running the race detector — a flaky goroutine
  race that passed once is never re-exercised locally. (`test`/`test_coverage`,
  `:81/:85`, share the gap.)
- **1.3 (§3.16) `.golangci.yml tests: false` disables all linters on `_test.go`.**
  `run.tests: false` (`.golangci.yml:5`) excludes every test file from linting,
  so the govet analyzers that matter most for this concurrency-heavy suite —
  `testinggoroutine`, `copylocks`, `loopclosure`, `sigchanyzer` — never run on the
  17 goroutine-spawning / 11 sync-using test files. A subtle test-only concurrency
  bug (a `t.Fatal` in a spawned goroutine, a copied mutex) would not be caught.
- **1.4 (§3.18) `make clear` errors on a clean checkout.** `clear`
  (`Makefile:95-96`) runs `rm ./bin/*`; on a fresh clone (no `bin/`) or after a
  prior `clear` the glob does not expand, `rm` receives the literal `./bin/*`,
  and exits non-zero — the target is non-idempotent (`make clear && make clear`
  fails).

## 2. Root-cause analysis

- **1.1**: the `vuln` target predates the multi-module split (ADR-004) and was
  never converted to the `$(MODULES)` loop its sibling targets use.
- **1.2**: the test targets relied on Go's default caching; the `-race`
  interaction (a cached pass is not re-raced) was not accounted for.
- **1.3**: `tests: false` was a blanket noise-reduction that also dropped the
  concurrency analyzers; the suite is in fact clean, so the exclusion only costs
  coverage.
- **1.4**: `rm ./bin/*` assumes a populated `bin/`; the no-match and no-dir cases
  were not handled.

## 3. Solution

### 3.1 Considered alternatives
- **1.3 — enable `tests: true` but add per-linter exclusions for "noisy"
  analyzers on tests**: rejected — enabling test linting on the current suite
  yields **0 issues** (verified: `golangci-lint run --tests ./internal/...
  ./pkg/... ./cmd/...` → clean), so no exclusions are warranted; adding them
  pre-emptively would suppress the very analyzers (`testinggoroutine`,
  `copylocks`) this fix exists to enable.
- **1.4 — `rm -f ./bin/* 2>/dev/null || true`**: rejected in favour of
  `rm -rf ./bin/` — simpler, idempotent, and removes the directory itself rather
  than papering over the failure with a swallowed error.

### 3.2 Per-site changes
- **3.2.1** `Makefile` `vuln` (`:152-154`) — loop over `$(MODULES)`, mirroring
  `build-all`/`test-all`:
  `@set -e; for dir in $(MODULES); do echo "::group::govulncheck $$dir"; (cd $$dir && govulncheck ./...) || exit 1; echo "::endgroup::"; done`.
  The CI step is unchanged (`.github/workflows/check.yml` runs `make vuln`).
- **3.2.2** `Makefile` — add `-count=1` to the `-race` test invocations in
  `test-all` (`:124` `go test -race -count=1 -coverprofile=coverage.txt ./...`,
  `:126` `go test -race -count=1 ./...`) and to `test`/`test_coverage`
  (`:81/:85`) for consistency, so a re-run always re-executes the race detector.
- **3.2.3** `.golangci.yml` (`:5`) — `tests: false` → `tests: true`, linting
  `_test.go` with the full analyzer set. No exclusions added (suite is clean).
- **3.2.4** `Makefile` `clear` (`:95-96`) — `rm ./bin/*` → `rm -rf ./bin/`,
  idempotent on a clean checkout.

## 4. Verification

Build-tooling changes — verified by the targets' behaviour, not Go unit tests
(no Go code is touched, so there is no diff-coverage to measure):

| Check | Command | Expected |
|---|---|---|
| 1.1 govulncheck all modules | `make vuln` | a `::group::govulncheck <dir>` line per module (`.`, `runtime`, `adapters/sqlite`, each `examples/*`); scan descends into every `go.mod` |
| 1.2 no cached race | `make test-all` run **twice** | the second run re-executes the `-race` tests (no `(cached)` on the `-race` lines) |
| 1.3 test linting clean | `golangci-lint run --tests cmd/... internal/... pkg/...` (and `make lint` after the config flip) | `0 issues` |
| 1.4 idempotent clear | `make clear && make clear` on a checkout with no `bin/` | both succeed (exit 0) |
| full gate | `make ci` | exit 0 (tidy → lint → build → `-race` → cover-check → vuln, all modules) |

## 5. Prevention
Each gate now covers what it implied but didn't: `vuln` scans every module like
the other multi-module targets; `-count=1` makes the race detector
re-run-faithful; `tests: true` keeps the concurrency analyzers on the test code
that most needs them; `clear` is idempotent. The `$(MODULES)`-loop convention is
now applied uniformly, so a future module is scanned by every gate automatically.

## 6. Regressions
No production code or public API changes. `vuln` now does more work (scans all
modules) — strictly more coverage, no false positives introduced. `-count=1`
disables only the *test result* cache (build cache is untouched), so CI wall-time
is unaffected on a cold cache and only local re-runs re-execute. `tests: true`
is clean today (0 issues); a future test that trips an analyzer is a true
positive the author fixes. `rm -rf ./bin/` is strictly safer than `rm ./bin/*`.
Tool-version pins remain duplicated in `Makefile` and `.github/workflows/
check.yml` by design — unchanged here.

## 7. Related
ADR-003 v.1 (module layout — the multi-module structure the `$(MODULES)` loop of
1.1 scans; the §3.17 core-import rule whose lint-config interaction is deferred to
**AB-004**). ADR-004 v.1 (runtime environment contract — the `runtime`/`adapters`
modules that gain third-party dependencies, which 1.1 must scan). The §3.17
depguard finding is out of scope (see the intro note, **AB-004**).

## 8. Implementation summary

Landed on `fix/audit-remediation-2026-06` in a single milestone commit
`18ea6c0` (`Makefile`, `.golangci.yml` — no Go code):

| Finding | Change |
|---|---|
| 1.1 | `Makefile` `vuln` loops over `$(MODULES)` (`@set -e; for dir in $(MODULES); do … (cd $$dir && govulncheck ./...) …`), scanning all 21 modules instead of only the root. |
| 1.2 | `-count=1` added to the `-race` runs in `test-all` and to `test`/`test_coverage` (4 sites), so a re-run re-executes the race detector. |
| 1.3 | `.golangci.yml` `run.tests: false` → `true`. |
| 1.4 | `Makefile` `clear`: `rm ./bin/*` → `rm -rf ./bin/`. |

**Verification results.** `make ci` exit 0 (HEAD `18ea6c0`): `lint-all-modules`
with `tests: true` reports **0 issues across all 21 modules** (root + runtime +
adapters/sqlite + 18 examples — the concurrency analyzers now cover `_test.go`);
`test-all` runs `-race -count=1`; `vuln` emits a `::group::govulncheck <dir>` per
module and reports **No vulnerabilities found** for each; `cover-check` passes.
`make clear && make clear` on a checkout with no `bin/` both exit 0. No Go code
changed, so there is no diff-coverage to measure. §3.17 (depguard) is deferred to
**AB-004**.

## 9. Open questions
None.
