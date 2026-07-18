# FIX-025 «Automate the go-install dev-tool pin updates»

**Type:** FIX (one-shot; not rewritten after landing).
**Status:** Accepted v.1 (2026-07-18, branch `fix/automate-tool-pin-updates`, implemented — `make ci` green).
**Date:** 2026-07-18.
**Author:** Ruslan Gabitov.
**Branch:** `fix/automate-tool-pin-updates`.
**Paired doc:** closes the FIX-024 §8.3 backlog item.
**Upstream:** self-contained (tooling/CI).

**Grounded in (internal artifacts):**
- `Makefile:50-53` (the four `*_VERSION` pins) + `62-64,121,131,193,231` (usage).
- `.github/workflows/check.yml:40-48` (the same versions hardcoded independently).
- `.github/dependabot.yml` (covers gomod + github-actions, NOT the go-install pins).

---

## §1 Symptoms

The four go-install dev tools — **mockery**, **golangci-lint**, **govulncheck**,
**covercheck** — are pinned to exact versions in **two** places (`Makefile`
`*_VERSION` vars + the workflow's install step), but **nothing updates them**.
Dependabot cannot: its `gomod`/`github-actions` ecosystems don't see version
strings embedded in a Makefile or a `go install …@vX` line. So the pins silently
age; a security fix in golangci-lint or govulncheck is picked up only when
someone remembers to bump — by hand, in both files (the FIX-024 §8.3 backlog,
and the recurring "two places must stay in sync" foot-gun).

## §2 Root Cause Analysis

The pins live **outside every dependency ecosystem** — they are tool versions in
build tooling, not module requirements. Dependabot (native, kept for gomod +
actions) has no custom/regex manager, so this niche needs its own mechanism. The
options were Renovate (an external app / heavier self-hosted setup) or a
self-contained in-repo Action; the latter fits gobpm's minimize-external-deps,
in-repo-control posture (FIX-024) and keeps native Dependabot untouched.

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Decision |
|---|---|
| A. **Self-contained weekly Action + a check script** — reads the pins, queries the datasource, opens a PR bumping both files | ✅ chosen — no external app, all in-repo, native Dependabot kept |
| B. Renovate (custom regex manager) — SaaS app or self-hosted | ❌ trades native for a third-party bot / heavier setup for four pins |
| C. Notify-only (open an issue) | ❌ not an update; a PR is testable through CI |

### §3.2 Changes by file

#### §3.2.1 `scripts/check-tool-pins.sh` (new)
For each tool: read the current pin from the `Makefile` `*_VERSION` var; resolve
the latest release from `proxy.golang.org/<module>/@latest` — every tool is a Go
module (mockery, `golangci-lint/v2`, `golang.org/x/vuln`, covercheck), so one
datasource covers all four with no GitHub-API rate limits; if newer (semver
`sort -V`), replace the exact old version string across **both** `Makefile` and
the workflow (versions are unique per tool, so a global replace is
collateral-free) and record it. Emits `changed=true|false` + a notes file for
the PR body. No update ⇒ exit 0, no-op.

#### §3.2.2 `.github/workflows/tool-pins.yml` (new)
`schedule` (weekly) + `workflow_dispatch`; `permissions: contents: write,
pull-requests: write`; SHA-pinned `checkout`. Runs the script; if it changed
files, commits on a `chore/tool-pins-<date>` branch and opens a PR via the `gh`
CLI (native to the runner — no third-party action). The bump then rides normal
CI (`make ci`), so a breaking tool update is caught in the PR, not on trunk.

### §3.3 Repo setting (operational note)
Opening a PR from a workflow requires **Settings → Actions → General → "Allow
GitHub Actions to create and approve pull requests"** to be enabled. Without it,
`gh pr create` fails with a permissions error. (Owner action, one-time.)

## §4 Verification

- The script run against the current (fresh) pins reports "all current" OR
  surfaces a genuinely newer version — either proves the query + compare path.
- A forced-stale probe (temporarily lower a pin) makes the script detect + bump
  it in both files (reverted after the check).
- `bash -n` / shellcheck clean; the workflow YAML parses.
- `make ci` unaffected (no Go/gate change).

## §5 Prevention

- The Action is the prevention — pins can't silently rot.
- A `# tool-pin` comment marks the canonical `Makefile` lines the script reads.

## §6 Regressions / side-effects

- The bot opens PRs; a breaking tool bump surfaces as a **red PR**, never on
  trunk. Rollback: delete the workflow + script.
- Depends on the §3.3 repo setting; documented.

## §7 Related

- FIX-024 (merged) — pinned these versions; §8.3 named this follow-up.
- [[project_ci_tool_versions_duplicated]] — the dual-file pin the script now
  updates together.

## §8 Implementation summary

### §8.1 Stages by commit (branch `fix/automate-tool-pin-updates`)
| Stage | Commit | Scope |
|---|---|---|
| Doc | `0197fc7` | FIX-025 (this document) |
| Impl | `f4ffcb7` | `scripts/check-tool-pins.sh` + `.github/workflows/tool-pins.yml` |

`make ci` green (non-Go change; `diff-coverage 100% of 0 lines — PASS`).

### §8.2 Empirical findings vs the §3 draft

- **All four tools resolve via goproxy — no GitHub API.** The draft routed
  golangci-lint through the GitHub releases API; in practice its module
  `github.com/golangci/golangci-lint/v2` resolves on `proxy.golang.org/@latest`
  like the rest, so all four use one datasource. The GitHub API also
  pretty-prints JSON (`"tag_name": "…"` with a space) that a naive grep missed,
  and is rate-limited unauthenticated — two reasons goproxy is cleaner.
- **The check is already actionable.** Run against current pins it found
  **mockery v3.5.0 → v3.7.1** and **golangci-lint v2.11.4 → v2.12.2** — so the
  first post-merge run will open a real bump PR (govulncheck/covercheck current).
  FIX-025 lands the *automation* only; the pins were reverted so the bumps ride
  the Action, not this doc.

### §8.3 Backlog
- None.

## Open questions

None.
