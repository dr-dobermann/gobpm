# SRD-003 — Extract covercheck to a Standalone Tool

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-08 |
| Owner | Ruslan Gabitov |
| Refines | [SAD-001 v.1 §6 Quality Attributes](../design/SAD-001-vision-and-architecture.md) |
| Supersedes | [SRD-002 v.1 §4.2](SRD-002-ci-diff-coverage-gate.md) — the "DIY in-repo" tool-locality decision |

This SRD moves the diff-coverage gate's implementation **out** of gobpm into its
own repository, consumed as a pinned external dev tool. It changes only **where
the tool lives**; the gate's semantics, scope, and threshold (SRD-002 §2/§3,
`COVER_MIN`) are unchanged.

## 1. Background & motivation

[SRD-002](SRD-002-ci-diff-coverage-gate.md) landed the gate as in-repo Go code
(`internal/covercheck` + `cmd/covercheck`), chosen in its §4.2 for "zero
dependency / full local parity".

That code is **domain-agnostic** — it parses Go coverage profiles and `git diff`
output; nothing about it is BPMN. Carrying a generic coverage tool inside the
engine module is a cohesion smell: it conflicts with the library's
minimal-and-focused ethos (SAD-001 G2) and with the module boundaries ADR-003
draws (the engine module should hold engine code). The same parity argument that
justified in-repo is fully preserved by **version-pinning** an external tool —
exactly how the repo already treats mockery / golangci-lint / govulncheck.

## 2. Decision

- The tool lives in its own repo: **`github.com/dr-dobermann/covercheck`**
  (released **v0.1.1**; package `covercheck` 100%-covered, thin `cmd/covercheck`
  CLI, its own CI that dogfoods the gate).
- gobpm **consumes it as a pinned dev tool**: `make tools` runs
  `go install github.com/dr-dobermann/covercheck/cmd/covercheck@$(COVERCHECK_VERSION)`;
  `make cover-check` invokes the installed `covercheck` binary behind the
  `require-tool` guard; `.github/workflows/check.yml` installs it alongside the
  other pinned tools.
- This is identical parity to the in-repo version — same binary decides the
  verdict locally and in CI — minus the in-repo code.

## 3. Functional requirements

| # | Requirement | Acceptance |
|---|---|---|
| FR-1 | `internal/covercheck` and `cmd/covercheck` are removed from gobpm. | Neither path exists; no Go file imports them; build passes. |
| FR-2 | `make tools` installs `covercheck` at a pinned version (`COVERCHECK_VERSION`). | After `make tools`, `covercheck` is on `PATH` at the pinned tag. |
| FR-3 | `make cover-check` runs the installed `covercheck` binary behind `require-tool` (fails loud if absent). | Missing binary aborts with an install hint; present binary gates as before. |
| FR-4 | CI installs `covercheck` and runs the same `make cover-check`. | The `check` workflow's gate step passes using the external binary. |
| FR-5 | The gate's behaviour is unchanged — changed-line coverage vs `COVER_BASE`, floor `COVER_MIN` (80), same exclusions. | A run reports the same verdict it did with the in-repo tool. |

## 4. Verification (Definition of Done)

- `internal/covercheck` / `cmd/covercheck` gone; `grep` finds no importers.
- `make tools` installs `covercheck@v0.1.1`; `covercheck` resolves and runs.
- `make ci` green end to end, including the `cover-check` step via the external
  binary.
- The diff-coverage verdict matches the pre-extraction behaviour at
  `COVER_MIN=80`.

## 5. Implementation summary

Landed on branch `ci/coverage-gate-tuning` (folded with the `COVER_MIN=80` +
Codecov-ignore tuning, per the owner's "one clean gobpm PR" call):

- Removed `internal/covercheck/` and `cmd/covercheck/`.
- `Makefile`: `COVERCHECK_VERSION := v0.1.1`; `tools` installs it; `cover-check`
  uses the installed binary behind `require-tool`.
- `.github/workflows/check.yml`: installs `covercheck@v0.1.1` with the other
  pinned tools.

Verified at landing: `make tools` installs `covercheck@v0.1.1`; `make ci` green
end to end via the external binary; no `go.mod`/`go.sum` drift.

## 6. References

- [SRD-002 v.1 CI Diff-Coverage Gate](SRD-002-ci-diff-coverage-gate.md) — defines the gate; this SRD supersedes only its §4.2 tool-locality decision.
- [SAD-001 v.1 §6 Quality Attributes](../design/SAD-001-vision-and-architecture.md); [ADR-003 v.1 Module Layout](../design/ADR-003-module-layout.md) — the cohesion/boundary rationale.
- `github.com/dr-dobermann/covercheck` (v0.1.1) — the extracted tool.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-08 | Ruslan Gabitov | Extract the diff-coverage tool to `github.com/dr-dobermann/covercheck` (v0.1.1); gobpm consumes it as a pinned dev tool via `make tools`. Supersedes SRD-002 §4.2 (in-repo). Gate semantics/threshold unchanged. |
