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
- `pkg/monitor/` - Process observability and metrics
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