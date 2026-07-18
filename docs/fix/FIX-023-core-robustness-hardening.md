# FIX-023 «Core robustness — errs panic-config removal, depguard enforcement, reproducible test build»

**Type:** FIX (one-shot; not rewritten after landing).
**Status:** Draft v.1 (2026-07-18, branch `fix/core-robustness-hardening`, not yet implemented).
**Date:** 2026-07-18.
**Author:** Ruslan Gabitov.
**Branch:** `fix/core-robustness-hardening` (three independent hygiene fixes landed as one themed change-set).
**Paired doc:** none (local to `pkg/errs`, `.golangci.yml`, and the build tooling).
**Upstream:** self-contained per hierarchy-rule — no ADR/SAD contract changes.

**Grounded in (internal artifacts):**
- `pkg/errs/errors.go:194-251` (the panic globals + config API).
- `.golangci.yml:7-66` (the `depguard` block, configured but not enabled).
- `Makefile:91-115,139,193` + `.gitignore:19` (mock generation coupled to the test target; `generated/` git-ignored).

---

## §1 Symptoms

### §1.1 Symptom A — data races on the `pkg/errs` panic globals

`pkg/errs/errors.go:194` declares two mutable package globals with no
synchronization:

```go
var (
	dontPanic bool          // errors.go:197
	panicHook PanicHandler  // errors.go:200
)
```

`SetDontPanic`/`DontPanic` (204/209), `RegisterPanicHandler`/`DropPanicHandler`/
`HasPanicHandler` (231/244/249) and `Panic` (215) read and write these globals
with no lock or atomic. A `-race` probe that drives them from concurrent
goroutines reports races. For an **embeddable concurrent engine** this is a real
hazard: a host may touch the package from several goroutines.

### §1.2 Symptom B — the architecture rules in `depguard` never run

`.golangci.yml:8` sets `default: none`; the `enable:` list (lines 9-31) does
**not** include `depguard`, yet a full `depguard:` settings block (lines 33-66)
encodes the intended architecture boundaries:

```yaml
# .golangci.yml — configured but never enabled
depguard:
  rules:
    # core (pkg/internal/cmd) must not import runtime / adapters
    # examples must not import internal
    # pkg/model must not import internal
    # adapters must not import adapters (siblings)
    # + io/ioutil deny
```

Because `depguard` is absent from `enable`, **none of these rules are checked**.
No current violations exist, but there is zero regression guard: a future
downward import compiles and lints clean.

### §1.3 Symptom C — a clean checkout fails `go test ./...`

`.gitignore:19` ignores `generated/*`, but **33** packages' tests import
`github.com/dr-dobermann/gobpm/generated/mock*` (79 test files). On a fresh
clone the mocks do not exist, so the standard command fails to compile:

```
no required module provides package github.com/dr-dobermann/gobpm/generated/mockeventproc
```

Only `make test` works, because it depends on `gen_mock_files`
(`Makefile:91,110`). That target **mutates the working tree**
(`Makefile:112-114`):

```makefile
gen_mock_files:
	rm -rf generated/   # deletes tree state
	mockery
	go mod tidy         # can rewrite go.mod / go.sum
```

So the idiomatic "clone and `go test ./...`" workflow is broken, and running the
test target rewrites the developer's tree.

---

## §2 Root Cause Analysis

### §2.1 Symptom A — the raced state is dead configurability

`errs.Panic(v)` — the panic **emitter** — is load-bearing: **33 call sites
across 27 production files** (`Must*` constructors and invariant guards
throughout `pkg/model/**`, `internal/scope`, `internal/eventproc`). But the
**configurability around it** — the `dontPanic` toggle and the `panicHook`
handler, i.e. `SetDontPanic`/`DontPanic`/`RegisterPanicHandler`/
`DropPanicHandler`/`HasPanicHandler` and the `PanicHandler` type — has **zero
callers outside `pkg/errs`'s own tests**:

```
grep -rl 'SetDontPanic|RegisterPanicHandler|DropPanicHandler|HasPanicHandler|DontPanic|PanicHandler'
  --include=*.go . | grep -v pkg/errs/   →  (empty)
```

The two globals that race are **only** ever written by the errs package's own
unit tests (`errors_test.go:TestPanic`/`TestDontPanic`, `helpers_test.go:
TestHasPanicHandler`). The config was built for a hypothetical embedder that
never materialized — a speculative hook. So the race is in dead surface, and the
fix is **removal**, not synchronization.

### §2.2 Symptom B — `depguard` was configured but never added to `enable`

Under `default: none` a linter runs only if it is named in `enable`. The
`depguard` **settings** block was written and maintained, but the one line that
turns it on was never added — a silent no-op. The tests for these boundaries
(none) never existed, so the gap was invisible.

### §2.3 Symptom C — generated mocks are a build input but not a repo artifact

Tests depend on `generated/mock*` at compile time, but the tree is git-ignored,
making mock generation a mandatory pre-step that (a) needs `mockery` installed
and (b) is wired into the test target, which also runs `rm -rf` + `go mod tidy`.
The generated code is a **build input** to `go test` yet is treated as a
throwaway, so the standard Go workflow cannot see it.

---

## §3 Solution

### §3.1 Alternatives considered

**Symptom A — remove vs harden the panic config**

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. **Remove** the configurability; `Panic(v)=panic(v)`, keep the wrapper | deletes the raced state (no race by construction); drops dead API; no lock/atomic cost | removes exported `pkg/errs` symbols (breaking — but unused, pre-1.0) | ✅ chosen |
| B. Make thread-safe (`atomic.Bool` + `atomic.Pointer[PanicHandler]`) | keeps the API | hardens code nobody uses; keeps dead surface | ❌ rejected |
| C. Inline `panic()` at all 33 sites, drop the wrapper | no indirection | 33-site churn; loses the single named chokepoint (future telemetry) | ❌ rejected |

**Symptom B — enable vs delete depguard**

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. **Enable** `depguard` + a guard-test asserting it stays enabled | enforces the intended architecture; guards the exact regression | may surface latent violations (none found — verified) | ✅ chosen |
| B. Delete the `depguard` block (it isn't running anyway) | removes an inert block | abandons the intended enforcement | ❌ rejected |
| C. Enable, no guard-test | one-line fix | nothing stops it being dropped from `enable` again | ❌ rejected |

**Symptom C — commit mocks vs keep ignored**

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. **Commit** `generated/`; decouple `test`/`test-all` from `gen_mock_files`; add a CI drift-check | `go test ./...` works on a clean clone; tests stop mutating the tree | generated code in git; regenerate-and-commit on interface changes (the drift-check enforces currency) | ✅ chosen |
| B. Keep ignored, document `make test` | no generated code in git | does **not** fix the reported symptom | ❌ rejected |

> **Open decision for the approval gate:** Symptom C carries a repo-policy call
> (committing generated code). This draft assumes Option A; if rejected at the
> gate, C is dropped and only A/B (§1.1/§1.2) land.

### §3.2 Changes by file

#### §3.2.1 `pkg/errs/errors.go` — remove the panic configurability

Delete the two globals and the config API; reduce `Panic` to a plain panic:

```go
// before: globals + SetDontPanic/DontPanic/RegisterPanicHandler/
//         DropPanicHandler/HasPanicHandler + PanicHandler type + branching Panic
// after:
// Panic raises v as a runtime panic — the single chokepoint the Must*
// constructors and invariant guards route through (33 call sites).
func Panic(v any) {
	panic(v)
}
```

Removes: `dontPanic`, `panicHook`, `PanicHandler`, `SetDontPanic`, `DontPanic`,
`RegisterPanicHandler`, `DropPanicHandler`, `HasPanicHandler`. Drops the now-unused
`fmt`/`os` imports if they become dead. The 33 `errs.Panic(...)` call sites are
unchanged.

#### §3.2.2 `pkg/errs/errors_test.go` + `helpers_test.go` — prune config tests

Delete `TestDontPanic` and `TestHasPanicHandler` (they test removed API). Reduce
`TestPanic` to assert the wrapper panics:

```go
func TestPanic(t *testing.T) {
	require.Panics(t, func() { errs.Panic("boom") })
}
```

#### §3.2.3 `.golangci.yml` — enable depguard

```yaml
  enable:
    - depguard   # + the existing list
```

#### §3.2.4 depguard guard-test — assert it stays enabled

A cheap regression guard so the linter cannot be silently configured-but-off
again. Primary: a tiny test (`.golangci_test.go` or a `make` check) that reads
`.golangci.yml` and asserts `depguard ∈ linters.enable`. (Stronger optional: a
build-ignored `testdata` file with a forbidden import + a target asserting
`golangci-lint` exits non-zero — records but not required.)

#### §3.2.5 `.gitignore` — un-ignore generated mocks (Option A)

Remove `generated/*` (line 19); commit the generated tree (14 files, 232K,
6 `generated/mock<pkg>/` dirs — the `.mockery.yaml`-scoped set; verified
deterministic across regenerations, header carries no timestamp/version).

#### §3.2.6 `Makefile` — decouple gen from test; add a drift-check (Option A)

- `test` / `test_coverage` / `test-all` / `tidy-check-all`: drop the
  `gen_mock_files` prerequisite — they run against the **committed** mocks, so
  no target rewrites the tree on a test run.
- `gen_mock_files`: stays as the explicit "interfaces changed → regenerate →
  commit" command, reduced to `rm -rf generated/ && mockery`. The `go mod tidy`
  is **removed** (it was the tree-mutation source; `tidy-check-all` already
  guards `go.mod`, and committed mocks add no deps — testify is already
  required).
- new `mock-check`: `rm -rf generated/ && mockery && git diff --exit-code --
  generated/` — fails when the committed mocks drift from the interfaces (a
  changed interface without regenerate+commit). Added to `ci` **first**
  (`ci: mock-check tidy-check-all …`) so it both catches drift and leaves fresh
  mocks present for the build/test steps. Deterministic per §3.2.5 + the pinned
  mockery version, so `git diff` is a reliable drift signal.

#### §3.2.7 coverage — exclude `generated/` from BOTH the measurement and the gate

The committed mocks are **1774 profile lines, all count=0** (Go 1.20+
instruments no-test packages at 0; mock methods run cross-package but aren't
attributed without `-coverpkg`). While git-ignored they were invisible to
`covercheck` (not in the git diff); committing them without care would (a)
crater the diff-gate with 1774 uncovered changed lines and (b) drag every
`go test -cover` / `go tool cover -func` aggregate down with permanent 0%
noise. Both layers must exclude `generated/`:

**(a) Measurement** — keep `generated/` out of `coverage.txt` in the first
place, so the raw coverage numbers and `go tool cover -func` reports stay
clean. In the coverage-producing commands (`test-all`'s root-module
`-coverprofile`, and `test` / `test_coverage`), test the **filtered package
set**:

```makefile
go test -race -count=1 -coverprofile=coverage.txt $(shell $(GO) list ./... | grep -v '/generated/')
```

`generated/` has no test files, so filtering it out of the list removes nothing
real — it only stops the no-test package being instrumented and listed at 0%.
(The mocks still **compile** — they're built as imports of the tests that use
them.)

**(b) Gate** — `covercheck` gained **`-exclude-paths`** (v0.2.0) for the diff
denominator; belt-and-suspenders with (a) since `covercheck` also reads the
git diff:

- Bump `COVERCHECK_VERSION` `v0.1.2 → v0.2.0` in **`Makefile:53`** and
  **`.github/workflows/check.yml:44`** (the pins are duplicated — bump both).
- Add `-exclude-paths '^generated/'` to the `cover-check` invocation
  (`Makefile:186`).

#### §3.2.8 lint — already excluded (no change)

`.golangci.yml` `exclusions.paths` already lists `generated`, `.generated`, and
`mock_.*\.go$`. Verified: `golangci-lint run ./generated/...` → **0 issues**.
The config anticipated committed generated code; M3 makes no lint change.

#### §3.2.9 `.gitattributes` (new) — GitHub display hygiene

```gitattributes
generated/** linguist-generated=true
```

Collapses the mocks in PR diffs, drops them from language stats, and
de-emphasizes them in blame — so committing generated code doesn't pollute
review.

---

## §4 Verification

Current coverage: `pkg/errs` has unit tests; no concurrency test; no depguard
guard; no clean-checkout smoke.

### §4.1 Regression tests (mandatory)

| Test / check | Setup | Assertion |
|---|---|---|
| `TestPanic` (reduced) | call `errs.Panic("boom")` | `require.Panics` |
| `go test -race ./pkg/errs/...` | — | green; no race (the raced state is gone) |
| depguard guard-test | read `.golangci.yml` | `depguard` is in `enable` |
| `make lint` | depguard enabled | 0 issues (no latent architecture violation) |
| `mock-check` | regenerate mocks | `git diff --exit-code generated/` clean |
| diff-coverage on the M3 commit | `cover-check` with `-exclude-paths '^generated/'` | ≥95% — the 1774 generated lines are excluded, not counted uncovered |
| coverage measurement clean | `grep -c '/generated/' coverage.txt` after `test-all` | 0 — the filtered package set keeps generated/ out of the profile/aggregate |
| lint skips generated/ | `golangci-lint run ./generated/...` | 0 issues (already excluded) |
| clean-checkout smoke | fresh worktree of HEAD | `go test ./...` **compiles** without `make` (mocks committed) |

### §4.2 Full gate

`make ci` green end-to-end (tidy-check, lint incl. depguard, build, `-race`
tests, diff-coverage ≥95% on touched files, govulncheck), all modules.

---

## §5 Prevention

- **Doc comments** on the reduced `Panic` (why the wrapper stays: the single
  chokepoint for 33 invariant sites) so its minimality isn't mistaken for an
  oversight.
- The **depguard guard-test** is the canary: if it fails, `depguard` was dropped
  from `enable` again.
- The **`mock-check`** target is the canary for stale committed mocks.

---

## §6 Regressions / side-effects

### §6.1 Removing exported `pkg/errs` symbols (Symptom A)
A breaking API change for any external consumer of `SetDontPanic`/
`RegisterPanicHandler`/etc. Audit: `grep` shows **zero** internal callers outside
the errs tests; gobpm is pre-1.0. Pre-landing grep to re-confirm no new caller
appeared.

### §6.2 Committing generated code (Symptom C, Option A)
`generated/` now lives in git → interface changes require regenerate-and-commit;
review diffs gain generated churn. Mitigated by `mock-check` (drift fails CI) and
by keeping `gen_mock_files` a one-command regenerate.

### §6.3 Rollback path
Each symptom is an independent commit → revert individually. Symptom C is the
only one touching git-tracked file inventory.

---

## §7 Related

- No upstream ADR/SAD (self-contained tooling/hygiene).
- Aligns with the project rules: audit/remove stale interfaces; no speculative
  universality; never lower the coverage gate.

---

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

> ⚠️ TODO: fill AFTER landing.

### §8.1 Stages by commit (branch `fix/core-robustness-hardening`)
| Stage | Commit | Scope | Tests |
|---|---|---|---|

### §8.2 Empirical findings — where reality diverged from the §3 draft

### §8.3 Backlog (out of FIX-023 scope)

## Open questions

None (the Symptom-C policy call is surfaced in §3.1 for the approval gate).
