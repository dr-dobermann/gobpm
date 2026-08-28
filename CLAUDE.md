# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

### Building
```bash
# Build all packages to ./bin/
make build

# Or manually:
go build -o ./bin/ "./..."
```

### Testing
```bash
# Run all tests (generates mocks first)
make test

# Run tests with coverage report
make test_coverage

# Run specific package tests
go test ./pkg/thresher/
go test ./internal/eventproc/

# Run benchmarks
go test -bench=. ./...
```

### Code Quality
```bash
# Run linter
make lint

# Run linter with auto-fixes
make lint_fix

# Run linter on all files (including tests)
make lint_all

# Report engine calls that reach the HOST while a lock is held — a broker, a
# processor, a reporter the embedding application supplied (FIX-038 §1.1,
# FIX-041 §1.1). NOT part of `make ci`: it is syntactic, so a clean run is
# evidence and not proof, and it knows only the method names listed in the
# script. Run it when touching locking code, and add any new host-facing
# method to its PATTERNS set.
make lock-sweep
```

### Mock Generation
```bash
# Generate mock files (required before running tests)
make gen_mock_files

# This removes generated/ directory and regenerates all mocks using mockery
```

### Other Commands
```bash
# Update dependencies
make update_modules

# Clean build artifacts
make clear

# Create git tag (uses .version file)
make tag

# Build the docs site (docs/ → site/, mkdocs build --strict; SRD-080) —
# needs `pip install mkdocs-material==<MKDOCS_MATERIAL_VERSION from Makefile>`.
# NOT part of `make ci`; CI runs it in .github/workflows/docs.yml and deploys
# to https://dr-dobermann.github.io/gobpm/ on master.
make docs-build

# Serve the docs site locally with live reload
make docs-serve
```

### CI Parity (run before pushing)

`make ci` runs the exact local-equivalent of GitHub CI (`.github/workflows/check.yml`),
across all modules. Run it before pushing — if it's green, CI is green. The REQUIRED
half is `ci-core`, and its ten steps run in this order:

mock-check → **link-check** → **examples-module-check** → tidy-check → lint →
build → consumer-smoke → race tests → **diff-coverage gate** → govulncheck

Four of those are easy to meet for the first time as a red gate, so they are worth
knowing before you push: **mock-check** regenerates the mocks and fails if the result
differs from what is committed (run `make gen_mock_files` after changing an
interface); **consumer-smoke** builds a throwaway module against the library, so it
catches a break in the public API that the repo's own packages do not; and the two
below have their own paragraphs.

On GitHub the same set is split into two parallel jobs for PR speed: the
REQUIRED `check` job runs the core gate (`make ci-core` — the non-example
modules), and the non-blocking `examples` job sweeps the `examples/*`
modules (`make ci-examples` — tidy+lint+build **+ run**: each example
executes end-to-end under a timeout asserting exit 0, FIX-029; they carry
no tests, and the core govulncheck already covers their `replace ../..`
dependency graph).
Locally `make ci` still runs BOTH halves — the full gate stays obligatory
before every push.

The **diff-coverage gate** (`make cover-check`, SRD-002) fails when the lines a change
adds/modifies are covered below `COVER_MIN` (95% now, rising toward 100). It judges
only changed lines — reusing the per-module `coverage.txt` files `test-all` writes, one
per core module — so the untouched-code coverage backlog never blocks a PR. The gate runs locally (`make ci`) and in CI via the
same `cmd/covercheck` binary, preserving local↔CI parity.

Note that covercheck is **HEAD-based**: it diffs the committed branch against
`COVER_BASE` (`origin/master`), so uncommitted work is invisible to it. A gate run
on a dirty tree reports only what is already committed — "100.0% of 0 changed
coverable lines" means it measured nothing, not that everything is covered. Measure
after committing a milestone, not before.

The **link check** (`make link-check`, FIX-034) walks every Markdown file in the
repository and fails on any relative link that does not resolve, reporting
`file:line`. It is blocking, because the 78 dead links FIX-031 swept up accumulated
precisely because nothing failed. It is
[`dr-dobermann/linkcheck`](https://github.com/dr-dobermann/linkcheck), pinned by
`LINKCHECK_VERSION` and installed by `make tools` — the same arrangement as
covercheck, and for the same reason: a documentation checker is not part of a BPMN
engine, so it lives in its own repository rather than inside this module's build,
lint and coverage sweeps. It is a small Go program rather than an off-the-shelf
checker because the parity rule requires every CI tool to be pinned and installed
by `make tools`, and the alternatives add a non-Go toolchain plus a network
dependency that reddens the gate for unrelated reasons.
External URLs are therefore out of scope, and so are fenced and inline code spans —
a Go generic like `values.NewArray[T](vals…)` is indistinguishable from a link.

The **example-module check** (`make examples-module-check`) fails when a directory
under `examples/` has no `go.mod`. `EXAMPLE_MODULES` is derived from
`find -name go.mod`, so such a directory is invisible to the run sweep: the core
build compiles it and nothing ever executes it. `examples/usertask` sat outside the
gate that way. The check lives in the REQUIRED core gate rather than the
non-blocking examples job on purpose — a regression here is a hole in *that* job,
and a guard in the half that can go red unnoticed guards nothing.

### Reading a gate run (FIX-039)

`make ci` announces every step and records its own verdict, because an exit code
alone did not survive the trip to whoever asked for it.

```
[ 8/14] test-core              started 23:31:35 (typically 1m05s)
[ 8/14] test-core              … 30s elapsed
[ 8/14] test-core              ok 1m05s
[full] verdict: PASS — 14/14 steps in 2m22s (see .ci/last-run.json)
```

**Judge a run by `.ci/last-run.json`, never by the exit code of whatever wrapped
it.** That file names the failing step, times every step, and carries the HEAD
sha and start time so a stale one cannot pass for the current one.

- **Absent means the run did not finish** — never a pass. It is deleted when a
  run starts and written only when one ends, so a killed run leaves nothing.
- A trailing `echo` masks the real status (`make ci; echo $?` reports *echo's*),
  and an output-filtering wrapper can truncate the log and return its own
  status. Both happened on 2026-08-08; one produced a false pass for a run that
  had failed at 91.7% diff-coverage.
- Do not detect liveness with `pgrep -f 'make ci'` — the pattern matches the
  checking command itself, which once reported a dead run as alive for 18
  minutes. Use the heartbeat lines, or the status file.

The per-step baseline in `.ci/timings.tsv` is machine-local and gitignored: 146s
on a 24-core box is not what a 2-core runner does, and a wrong estimate is worse
than none. On GitHub each core step is its own workflow step, so CI already
names and times them; the driver gives a local run the same information.

```bash
# One-time per machine: install the Go dev tools at the versions CI pins
# (mockery, golangci-lint, govulncheck, covercheck). Versions live in Makefile.
make tools

# Full pre-push gate (mirrors GitHub)
make ci
```

On macOS, install GNU coreutils once with `brew install coreutils`;
`run-examples` detects its `gtimeout` command automatically. Linux already
provides `timeout`. The examples run in parallel — `EXAMPLE_JOBS` at a time,
default the CPU count capped at 8 — with each example's output buffered and
printed in its own group fold in module order once all have finished, so a
failure is never interleaved with a neighbour's log (`scripts/run-examples.sh`).

**Parity rules (do not break these — they exist because a silent local
no-op once let broken code reach CI):**

- **Tools fail loudly, never skip or drift.** Every Make target that shells out
  to a pinned Go dev tool is wrapped in the `require-go-tool` guard, which
  reads the binary's embedded Go module version and requires the exact CI pin.
  Missing or stale binaries abort with a `make tools` hint. Non-Go commands
  use `require-command`; `run-examples` accepts GNU `timeout` on Linux or
  Homebrew's `gtimeout` on macOS. When adding a CI tool, add the corresponding
  guard and installation/documentation path.
- **The Go toolchain is pinned.** Every `go.mod` carries `toolchain
  go1.25.12` and the workflow sets `go-version: '1.25.12'`, so local and CI
  scan the identical stdlib (govulncheck reports stdlib vulnerabilities per
  toolchain patch — a bare `1.25` drifts between runs). To clear new stdlib
  vulns, bump the toolchain line in every module plus the workflow together,
  then re-run `make ci`.

## Architecture Overview

GoBPM is a BPMN v2 compliant Business Process Management engine with an event-driven architecture:

### Core Components

**Thresher (`pkg/thresher/`)** - Main BPM engine and process orchestrator
- Process registration and execution
- Event-driven process flow control
- Process instance lifecycle management

**EventHub (`internal/eventproc/eventhub/`)** - Central event distribution system
- Event routing and processing
- Event waiter management (`internal/eventproc/eventhub/waiters/`)
- Asynchronous event handling

**Process Model (`pkg/model/`)** - Complete BPMN element implementations
- `activities/` - Service tasks, user tasks, script tasks
- `events/` - Start events, end events, timer events, message events
- `gateways/` - Exclusive, inclusive, parallel gateways
- `flow/` - Sequence flows and associations
- `data/` - Variable handling and expression evaluation
- `foundation/` - Base BPMN elements and interfaces

**Instance Management (`internal/instance/`)** - Process execution and state tracking
- Process instance creation and lifecycle
- State snapshots (`internal/instance/snapshot/`)
- Runtime environment integration

**Supporting Components:**
- `internal/scope/` - Data scoping and variable management
- `internal/runner/` - Process execution runtime
- `internal/interactor/` - External system interactions
- `pkg/errs/` - Structured error handling
- `pkg/set/` - Utility data structures

### Key Patterns

**Event-Driven Flow:** Processes execute through event publishing/consumption rather than direct method calls. Events flow through the EventHub to registered waiters.

**Snapshot-Based State:** A process definition is converted once into an immutable snapshot — a validated launch template. Each instance `Clone`s that snapshot into its own node graph and mutates only its own copy; the immutable header (process id/name, properties, correlation keys) is shared by reference across clones. A snapshot is *not* a durable persistence/recovery mechanism — instance tracks, scopes, and history are not stored in it; durable persistence and rehydration remain future work (see ADR-009).

**Interface-Heavy Design:** Heavy use of interfaces for extensibility, with comprehensive mock generation for testing.

**Library, Not Framework:** Designed to be embedded in applications rather than controlling the application structure.

## Testing Strategy

- **Mock Generation:** Uses mockery to generate mocks for interfaces (`.mockery.yaml` configuration)
- **Coverage Target:** >75% across core components
- **Integration Testing:** Real-world scenario validation in examples/
- **Error Path Testing:** Comprehensive error condition coverage

## Common Development Tasks

### Adding New BPMN Elements
1. Implement interface in `pkg/model/[category]/`
2. Add to process model registration
3. Create corresponding event handlers if needed
4. Add tests and update mocks

### Working with Events
- Events flow through `internal/eventproc/eventhub/`
- Implement `EventProcessor` interface for custom processing
- Use waiters for asynchronous event handling

### Process Development
- Create processes using `pkg/model/process/`
- Convert to snapshots via `internal/instance/snapshot/`
- Register with Thresher engine for execution

### Testing New Features
1. Run `make gen_mock_files` after interface changes
2. Create unit tests with mocks
3. Add integration tests in examples/

## Design docs — BPMN standard grounding (ADR/SAD)

gobpm's governing external standard is **BPMN 2.0**. Per the `/sdd-fix` and
`/review-srd` evidence-first rules, every **standard-claim** in an ADR or SAD —
an element/object-model name, a cardinality, a semantic ("the spec says X"), a
lifecycle/state rule, terminology, or a "the standard requires / permits / is
**silent** on Y" — MUST be verified against the authoritative source and **cited
by section**, not asserted from memory. Silence is not a mandate.

Authoritative sources, in order:
- **`docs/bpmn-spec/`** — the vendored BPMN extract (elements, semantics,
  state-machines, conformance scope). Read and quote it; it carries the spec
  `§`-refs (e.g. correlation = `§8.4.2`).
- The **BPMN 2.0 spec text** and the **BPMN NotebookLM notebook** for clauses not
  covered by the extract.

Cite `§N` on every standard-claim (`§8.4.2`, `§13.5.1`, …). Where gobpm
deliberately diverges from the standard, call it out as an explicit **engine
choice** (a "Engine notes" subsection), never as if the standard said so —
matches the "preserve standard taxonomy" and "ADR is prescriptive,
standard-grounded" project rules. `/review-srd` flags an un-pinned standard-claim
🟠 before approval.
4. Ensure coverage with `make test_coverage`

## Design docs — versioning and lifecycle

**Only SAD and ADR carry a version. SRD and FIX do not.**

- **SAD / ADR — continuously-current contracts.** They describe how the system
  *should* work and are expected to outlive many landings, so they are
  versioned: `v.1`, `v.2`, … A version bump is what records a **change of
  contract** on an already-`Accepted` document, and the `Document History` row
  explains what changed and why. Cross-doc references pin the version they were
  written against (`ADR-020 v.2`, `SAD-001 v.1 §11`).

- **SRD / FIX — one-shot documents.** They describe a single landing: what is
  built now, how it is tested, when it is done. They are **not** versioned and
  are **not** retro-edited after acceptance — an Accepted SRD/FIX is a
  historical snapshot of the decision at that moment, and later work gets its
  own document rather than rewriting an old one.

**A document that is still `Draft` is simply edited.** No version bump per
change, no `Document History` row per correction. Drafts churn — that is what a
draft is for — and the churn is not a contract change because nobody has
accepted the contract yet. Only once a doc is `Accepted` does a further change
require a version bump (SAD/ADR) or a new document (SRD/FIX).

Consequences worth stating, because they are the mistakes this rule prevents:

- **Do not bump an ADR from v.2 to v.2.1 while v.2 is still `Draft`.** Fold the
  correction into v.2 and its single history row. Intermediate versions nobody
  accepted are noise that later readers must reconcile — and every one of them
  becomes a stale pin in every document that referenced it.
- **Do not version an SRD at all.** If it is `Draft`, edit it. If it is
  `Accepted` and something changed, that is a new SRD (or a FIX), not `v.1.1`.
- **Status flips at the PR handover**, once the work is landed and the
  `/check-srd` audit passes — never mid-implementation, and never as a way of
  "freezing" a document you intend to keep editing.

**Existing versioned SRDs stay as they are.** Several SRDs predating this rule
carry versions (e.g. `SRD-071 v.2.5`). They are `Accepted`, and an Accepted
one-shot document is not retro-edited — stripping their versions would rewrite
history for tidiness. Keep pinning them at the version they carry; the rule
governs documents written from here on.

## Design docs — cite the symbol, not the line

**A code reference in a design doc names the file and the symbol, never a line
number:** `resolveLanguage` in `pkg/convert/bpmn/language.go`, not
`language.go:59-84`. The same goes for a range — quote the code or name the
function, rather than pinning `:88-105`.

The reason is that a line pin rots on the next refactor while looking exactly
as authoritative as the day it was written, and a **one-shot** SRD or FIX is
precisely the document nobody re-verifies. A reader who greps the symbol finds
it wherever it moved; a reader who follows `:184` lands in the middle of an
unrelated function and cannot tell whether the doc is wrong or they are.

Measured, not assumed: four audit rounds over `SRD-089.D` and `.E` found
line-pin rot to be the single largest defect class in both — twenty-three
stale pins between them, several landing inside code that had nothing to do
with the claim, against a handful of genuine content errors. Every other
defect class was found once and fixed; this one regenerated with every
refactor of the packages the documents describe.

Applies to **documents written from here on**. Existing docs keep their pins:
an Accepted one-shot is not retro-edited for tidiness, and a stale pin inside
a historical record is a smaller problem than rewriting the record. Refresh
one only when you are editing that passage for another reason — which is how
`SRD-089.D` and `.E` got theirs corrected.

Two carve-outs, because the rot argument does not apply to them:

- **The vendored spec extract** (`docs/bpmn-spec/…`) — line-pinned citations
  there are stable, because the extract changes only when the standard's
  transcription does, and a `§`-less structural table has no symbol to name.
- **A commit SHA plus a path** in an implementation summary — that names an
  immutable object, so `git show <sha>:path` always resolves.

## Design docs — Russian twins

**Russian twins (`.ru.md`) are a SAD/ADR privilege, and they live in
`docs/design/ru/`.** A twin is a full translation of a continuously-current
contract, kept in sync on every version bump. SRD and FIX documents **never**
get a twin: they are one-shot landing records, and a translation nobody
re-reads rots the moment it lands (the 38 stale SRD/FIX twins deleted by
SRD-081 are the precedent). When adding or refreshing an ADR twin, put it in
`docs/design/ru/` — its relative links out of the group need one extra `../`
level — and the site's "Russian" nav group picks it up automatically.
