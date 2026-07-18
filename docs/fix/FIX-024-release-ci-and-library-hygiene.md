# FIX-024 «Release metadata, CI supply-chain, docs & library-consumption hygiene»

**Type:** FIX (one-shot; not rewritten after landing).
**Status:** Draft v.1 (2026-07-18, branch `fix/core-robustness-hardening`, not yet implemented).
**Date:** 2026-07-18.
**Author:** Ruslan Gabitov.
**Branch:** `fix/core-robustness-hardening` (folded into the FIX-023 branch — one
"core robustness + hygiene" PR; the two docs cover disjoint files).
**Paired doc:** none. Sibling: FIX-023 (same branch — errs/depguard/mocks).
**Upstream:** self-contained. Touches [ADR-003 v.1](../design/ADR-003-module-layout.md)
territory (module layout / CI) only descriptively.

**Cross-cutting requirement (owner, 2026-07-18):** gobpm must comply with Go
library conventions and be flawlessly consumable via `go get`, without causing
problems for its users. This is an acceptance criterion for every change here
(and a re-verification of FIX-023's committed mocks — see §4.6).

**Grounded in (internal artifacts):**
- `pkg/thresher/buildinfo.go:8` (hardcoded stale version); `.version`; `Makefile:2,71`.
- `.github/workflows/check.yml:20,26,41-44`; `Makefile:50-64`.
- `.github/dependabot.yml`; the 31-module tree (3 non-example: `.`, `runtime`, `adapters/sqlite`).
- `README.md:269,280` / `README.ru.md:189,200`; `CONTRIBUTING.md:19,45,52`; no `SECURITY.md`.

---

## §1 Symptoms

### §1.1 Symptom A — the startup banner reports a stale, wrong version

`.version` and README are `v0.8.1-rc.1`, but `pkg/thresher/buildinfo.go:8` is
`var version = "v0.8.0-rc.1"`. The `debug.BuildInfo` fallback fires only when
`version == ""` (`buildInfoFrom`), so the hardcoded non-empty value wins; and
`make build` passes no ldflags, so the stale string is baked into every build.
`Makefile:2` computes `VERSION = $(shell cat .version)` but nothing consumes it.

### §1.2 Symptom B — the CI supply-chain is not reproducible

`.github/workflows/check.yml`: `actions/checkout@v7` and `actions/setup-go@v6`
are pinned by **moving major tags**, not commit SHA; golangci-lint is installed
by piping `install.sh` fetched from **master** (mutable); `govulncheck@latest`.
So CI can change behaviour with no repo change — the opposite of the "no
local/CI drift" rule the Makefile's pinned versions establish.

### §1.3 Symptom C — dependency automation covers only the root module

`.github/dependabot.yml` tracks `gomod` in `/` only, but the repo has 31 Go
modules. The **3 non-example** ones — `.`, `runtime`, `adapters/sqlite` — carry
real dependencies; `runtime` and `adapters/sqlite` get no automated updates.
(The 28 examples use local `replace` and near-zero deps — deliberately excluded.)

### §1.4 Symptom D — documentation discrepancies

- `README.md:269,280` and `README.ru.md:189,200` say the toolchain is
  `go1.25.11`; `go.mod` pins `go1.25.12`.
- `CONTRIBUTING.md:19` links `doc/design/ADR-003…` — the directory is `docs/design/`.
- `CONTRIBUTING.md:45` says `make vuln` runs "against the core module"; the
  Makefile `vuln` target loops **all** modules.
- `CONTRIBUTING.md:52` says "Pull Request to **opengovernment**" (a leftover
  from the contributing template; the link targets `dr-dobermann/gobpm`).
- No `SECURITY.md` — no private vulnerability-disclosure procedure.

---

## §2 Root Cause Analysis

- **A:** the version was hardcoded before the `.version`/build-info flow existed;
  the `var` + ldflags override was added but the empty-string sentinel was never
  set, and `make build` was never wired to the ldflag, so `.version` and the
  binary drift on every patch.
- **B:** the workflow was written for convenience (tags + upstream install
  script) before the "pin everything" discipline the Makefile later adopted; the
  two never reconciled.
- **C:** dependabot was configured when the repo was single-module; the
  multi-module split (ADR-003) didn't revisit it.
- **D:** doc rot — the toolchain bumped 1.25.11→1.25.12 without a README sweep;
  `docs/` was `doc/` earlier; the CONTRIBUTING template text was never localized.

---

## §3 Solution

### §3.1 Alternatives considered

**A — version source of truth**

| Alternative | Decision |
|---|---|
| A. `version=""` + `make build` ldflags from `.version`; build-info fallback for `go install` | ✅ chosen — `.version` is the one source; a bare `go build` honestly shows the module version / `(devel)` |
| B. Read `.version` at init via `go:embed` | ❌ couples the library to a repo-relative file; wrong for a consumed module |
| C. Keep the hardcoded var, add a "bump it" checklist | ❌ the drift recurs by hand |

**B — golangci-lint install** (the debated piece): fetch `install.sh` from the
**pinned version tag** (`…/v2.11.4/install.sh`) rather than master — keeps the
official installer, removes the mutable-master risk. (`golangci-lint-action@sha`
rejected: adds an Action to pin/track; `go install` rejected: upstream
discourages it for golangci-lint.) **Renovate for the go-install tool pins:
deferred** — they're already dual-maintained by hand (Makefile + workflow); a
regex-manager is a separate opt-in.

**D — SECURITY.md channel:** GitHub **private security advisories** (a repo
button, no inbox to maintain) over a published email.

### §3.2 Changes by file

#### §3.2.1 `pkg/thresher/buildinfo.go` — empty the version sentinel
`var version = "v0.8.0-rc.1"` → `var version = ""`. The comment already
documents the fallback; this makes it fire. Release/`make build` set it via
ldflags; `go install module@vX` gets it from `debug.BuildInfo`.

#### §3.2.2 `Makefile` — stamp `.version` into `make build`
```makefile
build:
	$(GO) build -ldflags "-X github.com/dr-dobermann/gobpm/pkg/thresher.version=$(VERSION)" -o ./bin/ "./..."
```

#### §3.2.3 `pkg/thresher/buildinfo_test.go` — version guards
`TestVersionSentinelEmpty` (asserts `version == ""` so a hardcoded value can't
creep back); `TestDotVersionIsSemver` (reads `.version`, asserts it matches a
`v\d+\.\d+\.\d+` semver shape) — ties the release string to a checked format.

#### §3.2.4 `.github/workflows/check.yml` — pin the supply-chain
- `actions/checkout@<sha>  # v7.x.y`, `actions/setup-go@<sha>  # v6.x.y` (resolve
  the current release SHAs at implementation).
- golangci-lint: fetch `install.sh` from `…/v2.11.4/install.sh` (the pinned tag),
  not `master`.
- `govulncheck@latest` → `govulncheck@v1.6.0`.

#### §3.2.5 `Makefile` — pin govulncheck
`GOVULNCHECK_VERSION := latest` → `:= v1.6.0` (line 52; keeps Makefile↔workflow
parity).

#### §3.2.6 `.github/dependabot.yml` — cover the real modules
Extend the `gomod` entry to `directories: ["/", "/runtime", "/adapters/sqlite"]`
(newer dependabot syntax). Examples excluded (local-replace, minimal deps,
would flood the PR queue) — noted in a comment.

#### §3.2.7 docs
- `README.md` + `README.ru.md`: `go1.25.11` → `go1.25.12` (4 spots).
- `CONTRIBUTING.md`: `doc/design/` → `docs/design/`; "against the core module" →
  "against all modules"; "Pull Request to opengovernment" → "Pull Request to the
  gobpm repository".

#### §3.2.8 `SECURITY.md` (new)
Supported versions + a private-disclosure procedure via **GitHub private
security advisories** (Security tab → Report a vulnerability), with a
best-effort response window; explicitly ask reporters not to open public issues
for vulnerabilities.

#### §3.2.9 `Makefile` + CI — consumer-consumption smoke (library-compliance)
A `consumer-smoke` target: build a throwaway module (in a temp dir) that
`require`s gobpm via a local `replace`, imports `pkg/thresher`, `go build`s it,
and asserts testify/`generated/mock*` do **not** appear in `go list -deps` — the
permanent guard for "flawless `go get`" (a future root `replace`, a mock import
leaking into a non-test path, etc. fails it). Added to `ci`.

### §3.3 Deliberately unchanged (library-compliance, considered)
- **`toolchain go1.25.12`** kept: the CI/govulncheck stdlib-reproducibility
  rationale outweighs the minor consumer cost (a one-time auto-fetch for
  consumers on 1.25.0–1.25.11; newer Go unaffected; <1.25 fails the `go` line
  regardless). Owner-confirmed 2026-07-18.
- **`generated/mock*` public** kept: harmless (§4.6 proves no leak); moving to
  `internal/generated/` would churn 79 import paths for no functional gain.

---

## §4 Verification

### §4.1 Version (A)
`make build` then `./bin/<binary>` (or a startup log) reports `.version`;
`TestVersionSentinelEmpty` + `TestDotVersionIsSemver` green; a bare `go build`
shows the module version (honest), not a stale literal.

### §4.2 Supply-chain (B)
`make ci` green with pinned `govulncheck@v1.6.0` and the tag-pinned golangci
installer; the workflow diffs show SHA-pinned Actions with version comments.

### §4.3 Dependabot (C)
`.github/dependabot.yml` validates (GitHub's schema); `runtime` + `adapters/sqlite`
appear as tracked gomod directories.

### §4.4 Docs (D)
grep confirms no `go1.25.11`, no `doc/design/`, no "opengovernment", no "core
module" vuln claim; `SECURITY.md` present.

### §4.6 Library-consumption (cross-cutting)
`make consumer-smoke` green: an external module `go build`s against gobpm with
**no** testify / `generated/mock*` in its dependency closure and **no** root
`replace` reachable. (Verified manually 2026-07-18 pre-implementation — this
target makes it permanent.)

### §4.7 Full gate
`make ci` green — all modules; touched-file coverage ≥ standard; lint/vuln clean.

---

## §5 Prevention

- The version test + `.version`-as-source stop banner/version drift.
- The consumer-smoke stops go-get/library regressions.
- Dependabot (Actions SHAs + the 3 gomod dirs) keeps the pins current.
- A `# vX.Y.Z` comment beside each pinned SHA keeps them human-readable.

## §6 Regressions / side-effects

- Emptying `version` means a **bare** `go build` (no ldflags) shows the module
  version / `(devel)` instead of a number — intended; `make build` and releases
  carry the real version.
- SHA-pinned Actions need a bump (via dependabot) to get upstream fixes — the
  cost of reproducibility, mitigated by the automation.
- Rollback: each symptom is an independent commit.

## §7 Related

- FIX-023 (same branch) — the code-side robustness half.
- [ADR-003 v.1](../design/ADR-003-module-layout.md) — the module layout the
  dependabot scope and depguard rules mirror.

## §8 Implementation summary

> ⚠️ TODO: fill AFTER landing.

### §8.1 Stages by commit
| Stage | Commit | Scope | Tests |
|---|---|---|---|

### §8.2 Empirical findings

### §8.3 Backlog
- Renovate/regex-manager for the go-install tool pins (deferred).

## Open questions

None.
