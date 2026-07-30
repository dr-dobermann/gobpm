# FIX-030 — Local CI parity on macOS and pinned-tool version checks

**Type:** FIX (build/CI infrastructure).
**Status:** Implemented (2026-07-30; pending landing).
**Date:** 2026-07-30.
**Author:** Ruslan Gabitov.
**Paired doc:** none.
**Upstream:** operational backlog item “`make ci` cannot pass on macOS”; also
closes the pinned-tool-version finding recorded in
`docs/audit/code-review-codex-second-pass-2026-06-29.md`.

## §1 Symptoms

The local `make ci` contract was weaker than the GitHub CI contract in two
ways:

1. `run-examples` invoked GNU `timeout` by its Linux name. macOS does not ship
   that binary; Homebrew coreutils installs the same command as `gtimeout`.
   The target had no preflight, so it failed late with a bare
   `command not found`.
2. `require-tool` checked only whether a binary existed. An incompatible global
   install therefore passed the guard and failed later with misleading
   diagnostics — observed with `covercheck v0.1.2`, which does not support the
   pinned `v0.2.0` `-exclude-paths` flag. The same risk applied to mock
   generation and lint configuration.

The Go tests themselves are portable; the failure was in their local CI
orchestration.

## §2 Root cause

The example gate assumed GNU userland even though local CI is documented for
all developer platforms. Separately, the tool pins in `Makefile` were used only
for installation, not validation, so they were not an executable parity
contract.

Parsing each tool's human-readable `--version` output is brittle because the
four tools format it differently. Go binaries already carry stable build
metadata containing their module path and version.

## §3 Solution

### §3.1 Portable example timeout

`Makefile` resolves `EXAMPLE_TIMEOUT` in this order:

1. `timeout` (GNU/Linux and any explicitly exposed GNU coreutils binary);
2. `gtimeout` (Homebrew's non-conflicting macOS name);
3. literal `timeout`, so the guard emits a deterministic install error.

`run-examples` uses `require-command` before starting the loop. On macOS the
error names the exact prerequisite:

```text
brew install coreutils
```

`EXAMPLE_TIMEOUT` remains overrideable for nonstandard environments.

### §3.2 Exact pinned versions

`require-go-tool` now validates all pinned Go development tools:

| Command | Required module | Pin |
|---|---|---|
| `mockery` | `github.com/vektra/mockery/v3` | `v3.5.0` |
| `golangci-lint` | `github.com/golangci/golangci-lint/v2` | `v2.11.4` |
| `govulncheck` | `golang.org/x/vuln` | `v1.6.0` |
| `covercheck` | `github.com/dr-dobermann/covercheck` | `v0.2.0` |

The guard resolves the executable from `PATH`, reads `go version -m`, matches
the expected module, and requires the exact version. Missing, stale,
wrong-module, and unversioned binaries all fail before the destructive or
expensive target body starts. `make tools` remains the single repair command.
Its golangci-lint installer is fetched from `$(GOLANGCI_VERSION)` rather than
the moving `master` branch, matching the workflow's supply-chain pin.

The guard was also added to the single-module `lint`, `lint_fix`, and
`lint_all` targets, which previously bypassed the repository's fail-loud rule.

## §4 Documentation

- `README.md` and `README.ru.md` list all four tools installed by `make tools`
  and the one-time macOS coreutils prerequisite.
- `CLAUDE.md` records the version-aware guard and the `timeout`/`gtimeout`
  platform mapping.
- The completed item leaves `docs/backlog.md`; this FIX is its durable record.

## §5 Verification

Verified on macOS on 2026-07-30:

- the installed pins passed their preflights;
- `COVERCHECK_VERSION=v9.9.9` failed before `covercheck` ran, reporting the
  installed `v0.2.0` and required `v9.9.9`;
- a missing `EXAMPLE_TIMEOUT` failed before the loop and named
  `brew install coreutils`;
- a PATH containing `gtimeout` but no `timeout` passed the `run-examples`
  preflight;
- `make mock-check` regenerated the committed mocks with no drift;
- `make lint` passed with zero issues;
- `git diff --check` passed;
- a full isolated `make ci` passed: race tests, 96.3% diff coverage (95%
  minimum), vulnerability scans, and the complete examples tidy/lint/build/run
  sweep.
