# GoBPM - BPMN v2 Compliant Business Process Management Engine

![GitHub License](https://img.shields.io/github/license/dr-dobermann/gobpm)
![GitHub Tag](https://img.shields.io/github/v/tag/dr-dobermann/gobpm)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod-go-version/dr-dobermann/gobpm)
[![codecov](https://codecov.io/github/dr-dobermann/gobpm/graph/badge.svg?token=ENKOTEL4VN)](https://codecov.io/github/dr-dobermann/gobpm)
[![Go Report Card](https://goreportcard.com/badge/github.com/dr-dobermann/gobpm)](https://goreportcard.com/report/github.com/dr-dobermann/gobpm)

**GoBPM** is a native BPMN 2.0 engine written in Go. It is designed as a library (not a framework) that embeds directly into your application binary, providing lightweight process orchestration without external runtime dependencies.

> **Status:** v0.1.1 — active development, not yet production-ready.

## Key Characteristics

- **Library, not framework** — embeds into your Go binary with no JVM, containers, or external services required. You control the application architecture.
- **BPMN v2 Process Execution Conformance** — strict adherence to the OMG BPMN 2.0 specification, allowing gobpm to serve as a drop-in replacement for enterprise BPM engines.
- **Event-driven execution** — internal EventHub with concurrent node processing via goroutines, providing high reactivity and efficient CPU/RAM utilization.
- **Programmatic model construction** — process models are built in Go code. XML parsing is intentionally decoupled from the model layer, isolating business logic from serialization.
- **Interface-driven extensibility** — infrastructure components (persistence, queues, expression engines) are injected through interfaces, not hardcoded implementations.

## Architecture

```
Process Definition ──> Event Processing ──> Execution Engine (Thresher)
        |                     |                       |
        v                     v                       v
  BPMN Model            EventHub &              Instance
  Components             Waiters              Management
```

### Core Packages

| Package | Description |
|---------|-------------|
| `pkg/thresher/` | Main BPM engine — process registration and execution |
| `pkg/model/` | BPMN element implementations (activities, events, gateways, flows, data) |
| `internal/eventproc/eventhub/` | Central event distribution and waiter management |
| `internal/instance/` | Process instance lifecycle and state tracking |
| `internal/scope/` | Hierarchical data scoping and variable management |
| `pkg/model/data/` | Variable handling, expressions, and data associations |
| `pkg/errs/` | Structured error handling with classification |

## Quick Start

```bash
go get github.com/dr-dobermann/gobpm
```

```go
// Create a simple process: Start -> ServiceTask -> End
proc, _ := process.New("my-process")

start, _ := events.NewStartEvent("start")
task, _ := activities.NewServiceTask("do-work", op, activities.WithoutParams())
end, _ := events.NewEndEvent("end")

proc.Add(start)
proc.Add(task)
proc.Add(end)

flow.Link(start, task)
flow.Link(task, end)

// Create snapshot and run via Thresher
snap, _ := snapshot.New(proc)
engine := thresher.New()
engine.RegisterProcess(snap)
engine.Run(ctx)
```

## Development Roadmap

The project follows a 6-phase development plan, from infrastructure foundation to Day-2 operations tooling. Full details are in [docs/analytics/gobpm Development Roadmap.md](docs/analytics/gobpm%20Development%20Roadmap.md).

| Phase | Focus | Key Deliverables |
|-------|-------|-----------------|
| **0. Infrastructure Foundation** | Context isolation, multi-tenancy, data contracts | Scope tree, IAM interfaces, Formal Expression, Form Registry, EventHub observability |
| **1. Core Flow and Fault Tolerance** | Basic execution and failure handling | None/Terminate events, Service/User/Manual tasks, BpmnError, Incidents/Retry/DLQ, XOR/AND gateways |
| **2. Asynchrony and Reusability** | Inter-process communication, time management | Message Correlation, persistent Timers, Sub-Process, Call Activity, Event-Based Gateway |
| **3. Business Logic and Mass Processing** | Rules integration, iterative execution | Business Rule Task (DMN), Script Task, Loop/Multi-Instance, Conditional Events |
| **4. Full Conformance and Flexibility** | Complex events, adaptive scenarios | Signal/Compensation/Escalation/Link events, Transaction/Event Sub-Process, Ad-Hoc, Inclusive/Complex Gateways |
| **5. Day-2 Operations** | Industrial lifecycle management | Instance versioning, Migration API, Administration tools (Move Token, incident resolution) |

## Documentation

- [Project Analysis](docs/analytics/Analysis%20of%20the%20gobpm%20project.md) — architectural analysis, design rationale, and enterprise requirements
- [Development Roadmap](docs/analytics/gobpm%20Development%20Roadmap.md) — phased implementation plan with detailed deliverables
- [Documentation Index](README_INDEX.md) — component documentation navigation
- [API Reference](https://pkg.go.dev/github.com/dr-dobermann/gobpm) — generated Go documentation
- [Contributing Guide](CONTRIBUTING.md) — how to contribute
- [Changelog](CHANGELOG.md) — version history

## Development

```bash
# Build
make build

# Run tests (generates mocks first)
make test

# Run tests with coverage
make test_coverage

# Lint
make lint
```

### Requirements

- Go 1.25+
- [mockery v3](https://github.com/vektra/mockery) (for mock generation)
- [golangci-lint v2](https://golangci-lint.run/) (for linting)

## License

LGPL-3.0 — see [LICENSE](LICENSE) for details.
