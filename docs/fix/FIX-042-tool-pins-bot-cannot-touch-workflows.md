# FIX-042 «The tool-pin bot cannot push its bump, and its PR would never run CI»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted.
**Date:** 2026-10-19.
**Author:** Ruslan Gabitov.
**Branch:** `fix/tool-pins-workflow-permission`.
**Paired doc:** corrects FIX-025 (Accepted), which introduced the bot.
**Upstream:** self-contained (tooling/CI).

**Grounded in (internal artifacts):**
- `.github/workflows/tool-pins.yml` — the `Open a bump PR` step, pushing with `github.token`.
- `scripts/check-tool-pins.sh` — the `tools` table (fourth column = a workflow
  file) and the bump loop's second `sed -i` on `"$wf"`.
- `.github/workflows/check.yml` — the `Install tools` steps of the `check` and
  `examples` jobs; `.github/workflows/docs.yml` — `Install the site toolchain`.
- `Makefile` — the `*_VERSION` pins, the `tools` target, `require-mkdocs`,
  `workflow-check`.
- `internal/lintcfg/toolpins_test.go` — `TestMakefilePinsAppearInWorkflow`.

---

## §1 Symptoms

### §1.1 The bump push is rejected

The scheduled run of 2026-10-19 found newer pins, committed the bump and failed
on push:

```
[chore/tool-pins-202610191245 7f61ead] chore: bump pinned dev-tool versions
 2 files changed, 11 insertions(+), 11 deletions(-)
 ! [remote rejected] chore/tool-pins-202610191245 -> chore/tool-pins-202610191245 (refusing to allow a GitHub App to create or update workflow `.github/workflows/check.yml` without `workflows` permission)
Error: Process completed with exit code 1.
```

The two files are `Makefile` and `.github/workflows/check.yml`. No PR was
opened, so the pins keep aging — the exact condition FIX-025 exists to prevent.

Two attempts to fix it by permission already went in and out of master:
`b96f1bf1` added `workflow: write` (an unknown scope, which made GitHub reject
the whole file; removed by `5cac1ec9`), and `265c8d89` added `workflows: write`
(removed again by `df9ffc76`).

### §1.2 Latent: the bump PR would not run CI

Not yet observed, because no bump PR has ever been opened. Once §1.1 is fixed,
no `check` run would appear on the bot's PR (reason in §2.2), so FIX-025
§3.2.2's "the bump then rides normal CI" does not hold.

## §2 Root Cause Analysis

### §2.1 Each pin is written in two places, one of them a workflow file

FIX-025 §3.2.1 replaces the old version string "across **both** `Makefile` and
the workflow". In `scripts/check-tool-pins.sh`:

```bash
	sed -i "s/^\(${var}[[:space:]]*:=[[:space:]]*\)${esc}\$/\1${new}/" "$makefile"
	sed -i "s/${esc}/${new}/g" "$wf"
```

The second line rewrites `check.yml` (the five Go tools) or `docs.yml` (the two
mkdocs packages). A token without the `workflows` scope may not push any change
under `.github/workflows/`, and `GITHUB_TOKEN` can never hold that scope: the
`permissions:` block of a workflow has no `workflows` key at all. No setting of
`tool-pins.yml` can therefore make the current script's push succeed — which is
why both attempts in §1.1 failed.

The duplication itself was a deliberate choice, and the reason for it no
longer holds. `TestMakefilePinsAppearInWorkflow` records it:

```go
// The tool pins are duplicated on purpose: the Makefile keeps `make` usable
// locally, and the workflow installs its own list so CI does not depend on a
// Make target.
```

Every step after `Install tools` in both jobs is already `run: make <target>`,
so CI depends on `make` regardless; and the drift the test was written for
(linkcheck added to `make tools` but not to the workflow) is a property of
having two lists, which one list removes. The comment above `check.yml`'s
`Install tools` step ("not in Makefile to keep Makefile local-friendly") and the
Makefile tooling-section comment ("mirrored by the "Install tools" step") state
the same design.

### §2.2 `GITHUB_TOKEN` events do not trigger workflows

`tool-pins.yml` opens the PR with `GH_TOKEN: ${{ github.token }}`. GitHub does
not start workflow runs for events made with the repository's `GITHUB_TOKEN`,
with the exception of `workflow_dispatch` and `repository_dispatch`. `check.yml`
triggers only on `push` to master and `pull_request`, and has no
`workflow_dispatch`, so not even an explicit dispatch could run it today.
`docs.yml` already has one.

### §2.3 The check that guards `check.yml`'s step list runs nowhere

`workflow-check` compares `CI_CORE_STEPS` with the `make <step>` lines in
`check.yml` — the file §3.2.2 edits — and its comment says it exists because "a
duplicated list that nothing compares is a list that drifts". Nothing runs it:
it is not in `CI_CORE_STEPS`, not in `CI_EXAMPLE_STEPS`, and no workflow calls
it (`grep -rn workflow-check` hits only its own definition and comments, and
two Accepted docs, FIX-039 T-11 and SRD-088 NFR-2, that ran it by hand). The
same defect it was written against, one level up.

### §2.4 A newer local Go overrides the toolchain pin

Found while verifying this fix: on a machine with go1.27.0 installed, `make
lint-core` fails in every module, on master as on this branch:

```
panic: file requires newer Go version go1.27 (application built with go1.26)
```

`golangci-lint v2.11.4` is built with go1.26 and cannot load go1.27's stdlib.
CLAUDE.md's parity rule says "the Go toolchain is pinned", on the strength of
every go.mod's `toolchain go1.26.9` line — but a `toolchain` line only ever
**upgrades**: a local Go older than the pin switches up to it, a newer one
simply runs. So the gate scans a different stdlib locally from CI's, and a
pinned tool crashes on it. Nothing in the Makefile sets `GOTOOLCHAIN`. And
nothing checks that the 60 `toolchain` lines and the workflow's two
`go-version` pins agree, although the parity rule requires them to move
together.

### §2.5 The pinned toolchain itself became vulnerable

Also found while verifying: `vuln-core` fails on master and this branch alike
on GO-2026-6809 (crypto/x509 trusting compromised intermediate certificates),
present in go1.26.9's stdlib and reachable from `memrepo` through
`x509.Certificate.Verify`. It is fixed in go1.26.10. No PR can pass the
required check until the toolchain moves.

### §2.6 Why it was not caught

FIX-025 §4 verified the script locally (a forced-stale probe bumping both
files) and checked that the workflow YAML parses. Neither exercises a push with
`GITHUB_TOKEN`, and the first real run is the one in §1.1.

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. **The Makefile is the only place a pin is written**; workflows install through `make` targets, the bot edits only the `Makefile`, and dispatches CI on its branch | No secret; one source of truth, which SRD-080 FR-5 already requires of the docs toolchain ("in one place consumed by both CI and the local target") and `docs.yml`'s literal `9.7.7` violates today; the parity rule ("CI calls into Makefile targets") now covers tool install too | Reverses the deliberate duplication of §2.1 | ✅ chosen |
| B. A fine-grained PAT or GitHub App token with `workflows` + `pull_requests` scopes, stored as a secret | Fixes §1.1 and §1.2 at once, no file changes | A long-lived secret to rotate; bot PRs run CI as the owner; duplication stays | ❌ rejected: trades a layout fix for a credential |
| C. The script skips workflow files and lists the needed edits in the PR body | Smallest change | `check.yml` lags the `Makefile` until someone edits it by hand; CI installs the old version and tests the wrong thing | ❌ rejected: makes the PR's CI meaningless |
| D. For §1.2: the owner closes and reopens the bot PR so CI runs as them | No workflow change | A manual step every week, easy to forget | ❌ rejected in favour of a dispatch |

### §3.2 Changes by file

#### §3.2.1 `Makefile` — add `docs-tools`, fix the two comments

- New `docs-tools`: `python3 -m pip install
  mkdocs-material==$(MKDOCS_MATERIAL_VERSION)
  mkdocs-awesome-nav==$(MKDOCS_AWESOME_NAV_VERSION)`. `python3 -m` matches
  `require-mkdocs` and `docs-build`, so the packages land in the interpreter
  that later checks and runs them — locally and, after §3.2.3, in CI.
- `require-mkdocs`'s error message changes from `Run 'pip install
  $$name==$$want'` to `Run 'make docs-tools'`, as `require-go-tool` already says
  `Run 'make tools'`.
- `tools` is unchanged and stays the only Go-tool install target. It gains the
  comment `check.yml` carries today and loses in §3.2.2: the golangci-lint
  `install.sh` is fetched from the pinned version tag, not master, so the
  installer itself is immutable.
- The tooling-section comment drops "mirrored by the "Install tools" step" and
  says `check.yml` installs through `make tools`; the docs-section comment
  ("which mirrors this pin") says `docs.yml` installs through `make docs-tools`.

#### §3.2.2 `.github/workflows/check.yml` — install through make, allow dispatch

- Both the `check` and the `examples` job: `Install tools` → `run: make tools`.
  The `examples` job needs only golangci-lint and installs four more binaries
  than before; a `tools-lint` target was considered and dropped, because
  `workflow-check` filters `make <target>` invocations in `check.yml` by exact
  name and `tools` is already on its exclusion list.
- The stale comment above `Install tools` goes.
- `on:` gains `workflow_dispatch:` so the bot can start it (§3.2.5).

#### §3.2.3 `.github/workflows/docs.yml` — install through make, watch the Makefile

- `Install the site toolchain` → `run: make docs-tools`; its "Pins mirrored by
  …" comment goes.
- `Build the site (strict)` → `run: make docs-build` (the same `python3 -m
  mkdocs build --strict` into `site/`), so `require-mkdocs` asserts the
  installed pins in CI as `require-go-tool` does for the Go tools. The step's
  `sys.path` comment goes with it; the Makefile already carries it.
- `Makefile` is added to both `paths:` lists, so a human edit of a mkdocs pin
  also builds the site. The two lists stay identical, as their comment requires.
- `deploy` is unchanged: its `github.ref == 'refs/heads/master'` guard keeps a
  dispatch on the bot's branch from deploying.

#### §3.2.4 `scripts/check-tool-pins.sh` — bump only the Makefile

- The fourth column of `tools` (the workflow path) is dropped, and the
  `sed -i … "$wf"` line with it.
- With the `sed` line go the comments that describe it: the header ("bump them
  in BOTH the Makefile and the tool's CI workflow"), the in-loop "Workflow:
  every occurrence is a legit bump target", the `| workflow` column header, and
  the now-unused `check_wf` / `docs_wf` variables.

#### §3.2.5 `.github/workflows/tool-pins.yml` — dispatch CI

- After `gh pr create`, inside the same `Open a bump PR` `run:` block (so
  `$branch` is still in scope): `gh workflow run check.yml --ref "$branch"` and
  `gh workflow run docs.yml --ref "$branch"`, both unconditionally. Skipping
  `docs` on a Go-only bump would save one site build bounded by
  `timeout-minutes: 10`, whose `deploy` is ref-guarded (§3.2.3), at the cost
  of a new script output; §6.1 already accepts that build for every
  `Makefile`-touching PR. A dispatch runs the target
  workflow as it exists on `--ref`, so it tests the branch's `Makefile`, and
  reports on the branch head commit, which is the PR head.
- `permissions:` gains `actions: write`, the scope `gh workflow run` needs.
- The header comment ("pins that live in the Makefile + check.yml … opens a PR
  bumping both files, which then rides normal CI") says the pins live in the
  Makefile, and that the PR bumps it and dispatches `check` and `docs` on the
  branch.
- No guard on what the commit touches: after §3.2.4 the script writes only the
  `Makefile`, and if it ever wrote a workflow again GitHub's own rejection names
  the file, as §1.1 shows.

#### §3.2.6 `internal/lintcfg/toolpins_test.go` — invert the duplication test

`TestMakefilePinsAppearInWorkflow` and its "duplicated on purpose" comment are
replaced by `TestWorkflowsInstallThroughMake`. Every `.github/workflows/*.yml`
line is read with its comment stripped (from `#` to the end of the line, so an
action's trailing `# v7.0.1` release note is ignored), and no line may then
contain `go install`, `pip install`, `install.sh`, or a `v`-prefixed
`X.Y.Z` version. The first three catch an install command coming back; the
version pattern catches a pin coming back in any other form (an action's
`with: version:`, an `env:` entry), which the old literal-match test caught and
the command check alone would not. It keeps the existing
anti-vacuity guard in the form that fits: per job, not per file, it requires
exactly one active `run: make tools` in check.yml's `check` and `examples` and
one `run: make docs-tools` in docs.yml's `build`, so a moved directory, or an
install step dropped, commented out or moved into another job, cannot leave it
passing. `TestWorkflowViolations` and `TestMakeRunsByJob` pin the two rules on
synthetic input, so the real-workflow test cannot pass because a rule went
blind.

`TestBotDispatchesCI` pins §3.2.5's contract on comment-stripped lines:
`workflow_dispatch:` in check.yml and docs.yml, `actions: write` in
tool-pins.yml, and both `gh workflow run <file> --ref "$branch"` lines after
`git push` and after `gh pr create`. Losing any of them leaves the bot's PR
without its required check while the weekly run itself stays green, so
nothing else would notice.

All of these run in `test-core`, so on every PR.

#### §3.2.7 `CHANGELOG.md` — record the fix

The existing `[Unreleased] → Fixed` entry "The weekly tool-pin check runs
again" is replaced rather than amended: after `5cac1ec9` the check did start,
but its push was rejected (§1.1), so the entry's claim never held. In its
place, one entry for the rejected push and the CI-less PR, and one each for
§3.2.10 and §3.2.9. The `[Unreleased] → Changed` toolchain entry, which named
`go1.26.9`, names `go1.26.10` and why (§3.2.11).

#### §3.2.8 `CLAUDE.md` — the docs-build note and the core step list

The `make docs-build` entry says it "needs `pip install
mkdocs-material==<MKDOCS_MATERIAL_VERSION from Makefile>`"; it becomes "needs
`make docs-tools`", agreeing with the new `require-mkdocs` hint. The CI-parity
section's "its ten steps run in this order" list, and its sample run output
(`[ 8/14]`, `14/14 steps`), follow §3.2.9.

#### §3.2.9 `Makefile` + `check.yml` — `workflow-check` joins the core gate

- `workflow-check` becomes the first entry of `CI_CORE_STEPS`: it reads two
  files, takes well under a second, and a step list that disagrees with the
  workflow is worth knowing before the slow steps run.
- `check.yml`'s `check` job gains a `run: make workflow-check` step right after
  `Install tools`. The target compares the two lists, so it must appear in both
  — which it then checks about itself.
- The counts in the Makefile comments follow: "The ten core steps" → eleven,
  "all ten happen inside one process" → eleven, "`ci` drives all fourteen
  steps" → fifteen.

#### §3.2.10 `Makefile` + `internal/lintcfg/toolchain_test.go` — pin GOTOOLCHAIN

- The Makefile reads the root go.mod's `toolchain` line and exports
  `GOTOOLCHAIN` set to it, so every `go` call — and every Go tool and script
  that calls `go` — runs the pinned release whatever Go is installed. An empty
  line stops make with an error rather than falling back to the local Go.
  `:=` rather than `?=`, so a value left in the shell cannot break parity
  unnoticed; `make GOTOOLCHAIN=…` still overrides it deliberately.
- `TestToolchainPinIsUniform` requires every go.mod to carry the root's
  `toolchain` line and check.yml's two jobs to set the same `go-version`,
  naming the files that disagree. `TestToolchainPins` pins its two regexps on
  synthetic input.
- CLAUDE.md's parity rule and both READMEs say `make` pins the toolchain.

#### §3.2.11 Every `go.mod`, `check.yml` and four docs — go1.26.10

`toolchain go1.26.9` → `go1.26.10` in all 60 go.mod files and both
`go-version` pins in check.yml, per the parity rule; CLAUDE.md, README.md,
its twin README.ru.md and the installation guide name the new version. No `go`
directive changes.

#### §3.2.12 Hardening from the independent review

The pre-merge review of the branch (doc-blind, another model family) found
the plumbing tests and the script weaker than the properties they claim:

- **CI reads Go from go.mod.** Both setup-go steps in check.yml use
  `go-version-file: go.mod`; setup-go uses the `toolchain` directive when one
  is present (its README at the pinned SHA). A Go bump is then the go.mod
  files alone. `TestToolchainPinIsUniform` requires every setup-go step in
  every workflow to read go.mod and forbids a `go-version:` key, replacing the
  regexp over `go-version:` values and its count of two.
- **One comment rule.** `stripComment` treats `#` as a comment only outside
  quotes and after whitespace, so `echo "# x" && go install …` is no longer
  read as a comment. Every workflow helper uses it; `makeRunsByJob` also
  trims trailing space and CR and anchors `run:` as a step key.
- **The install guard catches pins without a `v`** (each current Makefile pin
  value, as a whole word), `go run …@`, `pip3`/`pipx`/`uv … install`, and
  `.yaml` workflows.
- **The bot's tool table is checked against the Makefile.**
  `TestPinBotCoversEveryPin` requires the same `*_VERSION` set in both and
  every pin in the `NAME := value` form the script's sed rewrites. The script
  itself fails, naming the pin, when the line is missing or the sed changed
  nothing — it used to record a bump that never happened.
- **The dispatch contract is checked in one step.** `TestBotDispatchesCI` also
  requires a single permissions block (a job-level one would replace
  `actions: write`), the PR opened after the push, and both dispatches in the
  step that pushed, where `$branch` is set.
- **The toolchain line is exact.** `toolchainLine` accepts only `toolchain
  goX.Y.Z` with nothing after it, and reports any other toolchain line by
  file: setup-go reads the line itself, a two-part version would float to the
  runner's patch, and how setup-go reads a trailing `//` comment is not
  verified. The Makefile's `sed` still strips such a comment, harmlessly. The
  first review round asked for the comment to be *accepted*; the second
  showed that trusted a reader nobody had checked, and the acceptance was
  reverted.
- The second review round also found: a `dispatchProblems` panic when the
  dispatch sits between the PR and the push (now reported), the setup-go
  count summed over every workflow instead of check.yml's own, an apostrophe
  in a plain YAML value read as an opening quote (a quote still open at the
  end of the line now falls back to the whitespace rule), pins written `?=`,
  `+=` or `export NAME :=` invisible to every check (now reported as
  malformed), and the script's post-`sed` check escaping only dots (it now
  compares strings).

## §4 Verification

One Go test changes (§3.2.6); the rest is checked by runs.

### §4.1 Regression checks

| # | Check | Expected |
|---|---|---|
| V-1 | `bash -n scripts/check-tool-pins.sh`; `shellcheck` if installed | clean |
| V-2 | `make tools`, `make docs-tools`, then `make docs-build` | pinned versions installed; `require-mkdocs` passes; the site builds |
| V-3 | Forced-stale probe: lower one Go pin and one mkdocs pin in the `Makefile`, run the script | only `Makefile` changes (`git diff --name-only`); output has `changed=true`; reverted after |
| V-4 | `go test ./internal/lintcfg/ -run TestWorkflowsInstallThroughMake`, then again with a `go install …` line, and separately an `env: X: v1.2.3` line, put back into `check.yml` | passes, then fails each time naming the file and line; reverted after |
| V-5 | `sed 's/#.*//' .github/workflows/check.yml .github/workflows/docs.yml \| grep -nE 'v[0-9]+\.[0-9]+\.[0-9]+'` | no output (the same rule as V-4, by hand) |
| V-6 | `make workflow-check`, then again with one `run: make <step>` line removed from `check.yml` | "check.yml runs exactly the 11 core steps", then `CI_CORE_STEPS and check.yml disagree`; reverted after. `make ci` (V-7) shows it as step 1 |
| V-7 | `make ci`, with no `GOTOOLCHAIN` in the shell | PASS by `.ci/last-run.json` |
| V-7a | `TestToolchainPinIsUniform`, then again with one adapter's go.mod on the old toolchain, one check.yml `go-version` moved alone, and an example go.mod without a toolchain line | passes, then fails each time naming the file; reverted after |
| V-7c | The root go.mod's `toolchain` line removed, then any `make` target; separately, trailing spaces after it | make stops with the FIX-042 error; with the spaces, `$(GO) version` inside make still reports the pin (the value is stripped). Reverted after |
| V-7d | `make lint-core` on a machine whose local Go is newer than the pin | passes (it panicked before §3.2.10) |
| V-7e | §3.2.12 breakages, each reverted: `9.7.7` in an `env:`, `pip3 install`, `go install` after a quoted `#`, a `.yaml` workflow installing a tool, the `examples` install dropped, a dispatch moved to a later step, a job-level `permissions:`, `FOO_VERSION` added to the Makefile alone, a pin with `=`, a pin with a trailing comment, `go-version:` restored, `go-version-file` commented out | each fails its test |
| V-7f | The script on a pin written with `=`, then on one with trailing space | exits 1 naming the pin; no `changed=true` |
| V-7g | Second-round breakages, each reverted: the apostrophe fallback removed, the `d < push` case removed, `makefilePin` narrowed back, `toolchainLine` accepting a `//` comment or a two-part version, check.yml losing a setup-go step | each fails its test |
| V-7b | The FIX-042 PR's own `check` and `docs / build` runs | green — the only pre-merge proof that `make tools`, `make docs-tools` and `make docs-build` work on a runner, and that `site/` lands where `upload-pages-artifact` reads it |

### §4.2 Post-merge (owner)

| # | Check | Expected |
|---|---|---|
| V-8 | Run `tool-pins` by `workflow_dispatch` on master | push succeeds, a `chore/tool-pins-*` PR opens |
| V-9 | That PR's checks | a `check` and a `docs` run on the head commit, started by the dispatch; branch protection sees the required `check` |

These need the merge for two reasons: the scheduled or master-dispatched
`tool-pins` run reads its own file from master, and `check.yml` can be
dispatched only once its `workflow_dispatch` trigger is on the default branch.

## §5 Prevention

- The drift guard (§3.2.6) lives in `test-core`, not in the weekly script:
  after §3.2.4 no automated path writes a workflow, so a pin can come back only
  through a human edit, and `test-core` fails the PR that makes it. A check in
  the weekly run would report a week late, in a workflow nobody watches.

## §6 Regressions / side-effects

### §6.1 What relied on the old behaviour
- `grep -rn 'check.yml\|docs.yml\|_VERSION' --include='*.go' --include='*.sh' --include=Makefile .`
  — the script (§3.2.4), `toolpins_test.go` (§3.2.6) and `workflow-check`
  (its logic unchanged: it reads `make <target>` lines, and `tools` is
  excluded; §3.2.9 only wires it in).
- `docs.yml` now also runs on PRs that change the `Makefile`: one extra site
  build (≤10 min) on those PRs, no deploy. Every master merge that touches the
  `Makefile` also re-runs `deploy` — an idempotent redeploy of an unchanged
  site.
- The `examples` job installs four tools it does not use (§3.2.2).
- SRD-080 (Accepted) FR-4 lists `docs.yml`'s trigger paths, which §3.2.3
  widens, and FR-6 cites the `check.yml` "Install tools" comment §3.2.2
  deletes. It is a one-shot record and stays as written, like FIX-025; FR-5's
  one-place pin is what this change finally delivers.

### §6.2 Rollback
Revert the branch's commits **and every `chore/tool-pins-*` bump merged since**,
newest first: a bare revert restores `TestMakefilePinsAppearInWorkflow` against
a `Makefile` the bot has already moved forward, and the reverted tree fails
`test-core`. FIX-025's layout then comes back with its §1.1 defect.

## §7 Related

- FIX-025 (Accepted) — introduced the bot; its §3.2.1 "both files" and §3.2.2
  "rides normal CI" are what this corrects. It is not retro-edited.
- SRD-080 (Accepted) — FR-5 asks for the one-place pin; see §6.1.
- `CHANGELOG.md` `[Unreleased]` "The weekly tool-pin check runs again" — the
  earlier half-fix (`5cac1ec9`).

## §8 Implementation summary

### §8.1 Stages by commit (branch `fix/tool-pins-workflow-permission`)
| Stage | Commit | Scope |
|---|---|---|
| Doc | `08fa5e8d` | FIX-042 (this document) |
| M1 | `f4ec9dad` | §3.2.1–§3.2.3, §3.2.6 (`TestWorkflowViolations`, `TestMakeRunsByJob`, `TestWorkflowsInstallThroughMake`), §3.2.8 docs-build note |
| M2 | `2485244d` | §3.2.4, §3.2.5, §3.2.2 `workflow_dispatch`, `TestBotDispatchesCI` |
| M3 | `a5cb4bf2` | §3.2.9, §3.2.8 step list |
| M3b | `e0fa5f25` | §3.2.11 — go1.26.10 across 60 go.mod, check.yml, four docs |
| M3a | `2bf3c612` | §3.2.10 — GOTOOLCHAIN pin, `TestToolchainPins`, `TestToolchainPinIsUniform`; §2.4–§2.5 |
| Docs | `56f12e64` | §3.2.7 CHANGELOG |

`make ci` with no `GOTOOLCHAIN` in the shell on a go1.27.0 machine: PASS
15/15 at `2bf3c612` (`.ci/last-run.json`). Diff coverage 100% of 0 changed
coverable lines — every Go change is a `_test.go` file. M3b and M3a are
numbered by the order they were found, not landed: the bump had to land
first so the pin would read the new version.

### §8.2 Empirical findings vs the §3 draft

- **No `tools-lint` target.** The plan split `tools` so the `examples` job
  could install golangci-lint alone; `workflow-check` filters `make <target>`
  lines in check.yml by exact name, and `tools` was already excluded. Both
  jobs run `make tools` instead (§3.2.2).
- **No push guard, and no `docs` output.** Two draft mechanisms in
  §3.2.4/§3.2.5 were removed by the document review: the guard checked a
  condition §3.2.4 makes impossible and would have discarded a valid bump; the
  `docs=true` output existed only to skip one bounded site build. Both
  workflows are dispatched unconditionally.
- **Per job, not per file.** `TestWorkflowsInstallThroughMake` first counted
  `run: make tools` lines per file; the milestone check showed an install
  moved from `examples` into `check` would still pass. `makeRunsByJob` counts
  per job (§3.2.6).
- **The dispatch contract needed its own test.** Neither the forced-stale
  probe nor the install test would notice the loss of `actions: write`, a
  `workflow_dispatch` trigger or a dispatch line — the weekly run stays green
  while the PR goes without CI. `TestBotDispatchesCI` was added in M2.
- **Two defects outside the draft.** Verification hit a local go1.27 that
  crashed the pinned golangci-lint (§2.4 → M3a) and GO-2026-6809 against
  go1.26.9 (§2.5 → M3b). Both fail on master too; both were fixed here.
- **The pins really are stale.** The V-3 probe resolved mockery v3.8.1,
  golangci-lint v2.15.0, govulncheck v1.9.0 and linkcheck v0.1.3 against the
  pinned v3.5.0, v2.11.4, v1.6.0 and v0.1.2 — the bot's first run after merge
  will open that PR, and V-8/V-9 are checked on it.

- **The independent review hardened the guards (§3.2.12).** Twelve notes
  across three lenses; six were agreed and fixed, the docs-pins move to a
  `requirements.txt` was declined (it would put pins in a second place),
  and one claimed `workflow-check` failure on `make tools` was wrong — the
  filter already excludes it. The agreed fixes were all gaps between a test
  and the property it named: a quoted `#` that hid an install, unprefixed
  pins, a sed that could change nothing while the bot reported a bump.
  The second round, on that commit alone (`qwen3.8-max`, the agy quota being
  exhausted), raised fifteen notes: eleven agreed and fixed, four rejected —
  among them that setup-go reads the `go` directive (its README at the pinned
  SHA says the `toolchain` directive wins) and that the script's pin check
  needs `pipefail` (it runs under `set -euo pipefail`).
  The third round, on the second-round fixes, raised ten notes; three small
  ones were agreed — a tab-indented recipe line read as a pin (pins may now
  be indented by spaces only), the `!=`, `::=` and `override` forms unseen,
  and an indented toolchain line reported as absent rather than quoted. The
  seven rejected include a go.mod with two toolchain lines (Go itself refuses
  it: `repeated toolchain statement`) and a missing `git push` panicking
  `dispatchProblems` (an early return already reports it).

### §8.3 Backlog

None. V-7b (this PR's own CI runs) and V-8/V-9 (§4.2) are post-push checks
for the owner, not deferred work.

## Open questions

None.
