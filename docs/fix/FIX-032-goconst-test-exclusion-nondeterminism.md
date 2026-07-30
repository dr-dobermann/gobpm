# FIX-032 — `goconst` could escape the test-file lint exclusion nondeterministically

**Type:** FIX (lint configuration / local↔CI parity).
**Status:** Implemented (2026-07-30; pending landing).
**Date:** 2026-07-30.
**Author:** —
**Paired doc:** none.
**Upstream:** a red `lint-core` on GitHub for `pkg/convert/bpmn` that
`make lint-core` reported green locally, repeatedly and with a cleared cache.

## §1 Symptoms

CI failed:

```text
Error: pkg/convert/bpmn/expression.go:62:55: string `bool` has 3 occurrences,
make it a constant (goconst)
```

The same `make lint-core`, same pinned `golangci-lint v2.11.4`, same commit,
run locally with `golangci-lint cache clean` beforehand, reported **0 issues** —
several times in a row. This is exactly the class of divergence the repository's
parity rule exists to prevent (CLAUDE.md: "if it's green, CI is green").

## §2 Root cause

Two configuration facts interact badly:

1. `linters.exclusions.paths` carries `_test\.go` — test files are **not**
   linted. This filters issues **by the path they are reported at**.
2. `goconst` counts occurrences of a literal across the package, **including
   test files** (its `ignore-tests` defaults to false), and then reports the
   finding at *one* of the occurrence sites.

`"bool"` occurred in both halves — `importer.go` (the userTask placeholder
output) and `expression.go` (`ResultType`) in library code, `bpmn_test.go` and
`branches_test.go` in tests. Which site goconst anchors the report to is not
guaranteed. Anchored on a `_test.go` file the issue is filtered and the run is
green; anchored on `expression.go` it survives and the run is red.

So the finding was always *there*; only its visibility flickered. Local runs
happened to anchor in a test file, CI happened to anchor in library code. A
literal confined to test files (`"https://go.dev"`, `"approval"` — both wholly
inside `bpmn_test.go`) cannot exhibit this: every candidate anchor is excluded.
The trap needs a literal that straddles library and test code.

## §3 Solution

Two changes, deliberately both — one fixes the instance, one the class.

### §3.1 The literal earns its constant

`pkg/convert/bpmn/bpmn.go` gains `typeBool = "bool"`, used by `expression.go`,
`importer.go` and both test files. This is not lint appeasement: the string is
the gobpm boolean type name shared by two independent contracts — a
sequence-flow condition is boolean by definition (BPMN §13.2), and the userTask
placeholder output declares the same type — and the existing tag constants in
that file exist for precisely this "cannot drift" reason.

### §3.2 `goconst` stops counting what it may not report

`.golangci.yml` sets `goconst.ignore-tests: true`, aligning what goconst
*counts* with what the exclusion lets it *report*. With it, a literal appearing
twice in library code and five times in tests is simply below the threshold —
deterministically, wherever the anchor would have fallen.

Without §3.2 the class survives: any future literal straddling both halves
reintroduces a coin-flip between local and CI.

## §4 Verification

- `goconst` on `pkg/convert/…` with the repository config: **0**, stable across
  three runs with `golangci-lint cache clean` between each.
- `make lint-core`: 0 issues across all five core modules, before and after
  §3.2 — the setting unmasks nothing and masks nothing else today.
- Re-checked with §3.2 applied but §3.1 reverted: the pre-existing `"bool"`
  count drops to the two library-code sites, below `min-occurrences`, so the
  original failure could not have occurred either.
- `go build ./...` clean; `pkg/convert/…` and `internal/lintcfg` tests green.

## §5 Notes for the next reader

A green local lint is only as trustworthy as the determinism of the linters
behind it. When a path-based exclusion meets a linter that aggregates across
files, the exclusion filters the *report*, not the *analysis* — and anything
that picks one site out of many can cross the boundary. `dupl` aggregates the
same way and is enabled here; it reports both ends of a duplicate pair, so it
is not exposed today, but the pattern is worth remembering before adding a
similar exclusion.
