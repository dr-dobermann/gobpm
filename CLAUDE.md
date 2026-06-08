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
```

### CI Parity (run before pushing)

`make ci` runs the exact local-equivalent of GitHub CI (`.github/workflows/check.yml`):
tidy-check → lint → build → race tests → **diff-coverage gate** → govulncheck, across
all modules. Run it before pushing — if it's green, CI is green.

The **diff-coverage gate** (`make cover-check`, SRD-002) fails when the lines a change
adds/modifies are covered below `COVER_MIN` (80% now, rising toward 100). It judges
only changed lines — reusing the `coverage.txt` `test-all` writes — so the untouched-code
coverage backlog never blocks a PR. The gate runs locally (`make ci`) and in CI via the
same `cmd/covercheck` binary, preserving local↔CI parity.

```bash
# One-time per machine: install the dev tools at the versions CI pins
# (mockery, golangci-lint, govulncheck). Versions live in the Makefile.
make tools

# Full pre-push gate (mirrors GitHub)
make ci
```

**Parity rules (do not break these — they exist because a silent local
no-op once let broken code reach CI):**

- **Tools fail loudly, never skip.** Every Make target that shells out to
  a dev tool is wrapped in the `require-tool` guard, so a missing binary
  aborts with an install hint instead of passing as a no-op. When adding a
  CI step that calls a new binary, add a matching `require-tool` guard and
  add the tool to the `tools` target — otherwise an absent tool silently
  "passes" locally while failing on GitHub.
- **The Go toolchain is pinned.** Every `go.mod` carries `toolchain
  go1.25.11` and the workflow sets `go-version: '1.25.11'`, so local and CI
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

**Snapshot-Based State:** Process definitions are converted to snapshots for execution, allowing for state persistence and recovery.

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
4. Ensure coverage with `make test_coverage`