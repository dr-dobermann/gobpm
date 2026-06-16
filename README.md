# GoBPM — BPMN 2.0 Process Engine for Go

![GitHub License](https://img.shields.io/github/license/dr-dobermann/gobpm)
![GitHub Tag](https://img.shields.io/github/v/tag/dr-dobermann/gobpm)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/dr-dobermann/gobpm)
[![codecov](https://codecov.io/github/dr-dobermann/gobpm/graph/badge.svg?token=ENKOTEL4VN)](https://codecov.io/github/dr-dobermann/gobpm)
[![Go Report Card](https://goreportcard.com/badge/github.com/dr-dobermann/gobpm)](https://goreportcard.com/report/github.com/dr-dobermann/gobpm)

**GoBPM** is a native Go BPMN 2.0 engine. It is designed to embed directly into a Go application as a minimal, dependency-light **library** — and to scale up to a standalone process **server** through additive runtime components, without forcing library users to ship what they don't need.

> **Status:** v0.1.1 — active development, not yet production-ready.

The vision, scope, and architecture are defined in [SAD-001](docs/design/SAD-001-vision-and-architecture.md) and its ADRs; the delivery plan is the [Development Roadmap](docs/analytics/gobpm%20Development%20Roadmap.md).

## Two journeys

1. **Embedded library.** `import github.com/dr-dobermann/gobpm`, build an engine, register a process, run it. No external services required.
2. **Standalone runtime.** A `gobpm-server` (planned, `runtime/` module) exposes the engine over HTTP/gRPC with real persistence, identity, and observability — built *on* the library, never a fork of it.

The library carries no runtime baggage; the runtime never reimplements the engine.

## Key characteristics

- **Library, not framework** — embeds into your Go binary; no JVM, containers, or external services. Core depends only on the Go stdlib + `github.com/google/uuid`.
- **BPMN 2.0 Process Execution Conformance** — the Common Executable Subclass plus the ComplexGateway extension. Authoritative scope: [docs/bpmn-spec/conformance.md](docs/bpmn-spec/conformance.md).
- **Predictable execution model** — one event-loop goroutine per process instance owns state; each *track* (thread of execution) runs in its own goroutine, and a token is a projection of a track's position, not a stored object; `context.Context` is the cancellation contract. See [ADR-001](docs/design/ADR-001-execution-model.md).
- **Interface-driven extensibility** — persistence, expressions, messaging, observability, authorization, task distribution, and clock are all behind interfaces with in-core defaults. See [ADR-002](docs/design/ADR-002-extension-architecture.md).
- **Observable by default** — `Logger` defaults to `slog.Default()`; you opt *out* of telemetry, you don't opt in. Tracer/metrics default to no-op (OpenTelemetry adapter ships separately).
- **Programmatic model construction** — processes are built in Go. XML parsing is intentionally decoupled from the model layer.

## Architecture

```
Process model ──> Snapshot ──> Engine (Thresher) ──> Instance (orchestrator)
   pkg/model        immutable      pkg/thresher          1 goroutine / instance
                    definition                            ├── Tokens (1 goroutine each)
                                                          ├── EventHub + waiters
                                                          └── Scope (hierarchical data)
```

Dependencies flow downward only; lower layers know nothing of higher ones.

### Core packages

| Package | Description |
|---------|-------------|
| `pkg/thresher/` | Engine façade — process registry and instance lifecycle |
| `pkg/model/` | BPMN element types (activities, events, gateways, flow, data, …) |
| `pkg/errs/`, `pkg/set/` | Structured errors; utility data structures |
| `internal/instance/` | Instance / track / token execution (+ `snapshot/`) |
| `internal/eventproc/` | EventHub + event waiters (timer, …) |
| `internal/scope/` | Hierarchical data scoping and variable shadowing |

## Quick start

```bash
go get github.com/dr-dobermann/gobpm
```

```go
// Start -> ServiceTask -> End  (errors elided for brevity)
engine, _ := thresher.New("demo-engine")

proc, _ := process.New("demo-process")
start, _ := events.NewStartEvent("start")

// A ServiceTask runs your Go code: wrap a functor as the operation's
// implementation. The operation carries no in/out messages (nil, nil) —
// no data mapping needed, the functor just runs inside the process.
work, _ := gooper.New(
    func(ctx context.Context, _ *data.ItemDefinition) (*data.ItemDefinition, error) {
        fmt.Println("  ▶ hello from inside the process")
        return nil, nil
    })
op, _ := service.NewOperation("hello", nil, nil, work)
task, _ := activities.NewServiceTask("work", op, activities.WithoutParams())

end, _ := events.NewEndEvent("end")

_ = proc.Add(start)
_ = proc.Add(task)
_ = proc.Add(end)
_, _ = flow.Link(start, task)
_, _ = flow.Link(task, end)

_ = engine.RegisterProcess(proc)
_ = engine.Run(context.Background())
_ = engine.StartProcess(proc.ID())
```

The `gooper` functor is how you embed arbitrary Go logic in a process — the
same pattern scales from a `Println` to a real handler. A complete, runnable
version (with error handling and waiting for the task to run) lives in
[`examples/basic-process/`](examples/basic-process/); see also
[`examples/parallel-gateway/`](examples/parallel-gateway/) (concurrent
branches),
[`examples/process-data/`](examples/process-data/) (process data through the
task), and the timer examples
[`examples/simple-timer/`](examples/simple-timer/) ·
[`examples/timer-event/`](examples/timer-event/).

### Startup logging

`thresher.New` prints a startup report — an ASCII banner with the engine
version and last commit, then one line per resolved extension — so the wiring
is visible in the log at construction time. Both blocks are on by default; opt
out per block when the noise isn't wanted:

```go
// Fully silent startup:
eng, _ := thresher.New("worker-7",
    thresher.WithoutBanner(),        // drop the banner / version / commit
    thresher.WithoutStartupConfig(), // drop the per-extension config dump
)
```

## Development

```bash
make tools     # one-time: install pinned dev tools (mockery, golangci-lint, govulncheck)
make ci        # full pre-push gate — mirrors GitHub CI exactly (tidy, lint, build, race tests, diff-coverage, vuln scan)

make test         # tests (generates mocks first)
make lint         # lint core module
make build        # build to ./bin/
make cover-check  # diff-coverage gate — changed lines must be >= COVER_MIN (run after `make test-all`)
```

`make ci` is the contract: green locally ⇒ green on CI. The Go toolchain is pinned (`go.mod` → `go1.25.11`) so local and CI scan the identical standard library.

### How we work

- **Specification-first** — non-trivial changes start from a spec (SRD/FIX) referencing the governing ADR; the spec lands in the same change-set as its implementation.
- **`master` is protected** — changes land only through a PR with a green `check`; no direct, force, or admin-bypass pushes.
- **Diff-coverage gate** — CI fails when the lines a change *adds or modifies* are covered below `COVER_MIN` (95% now, rising toward 100%). It judges only changed lines, so the untouched-code backlog never blocks a PR. See [SRD-002](docs/srd/SRD-002-ci-diff-coverage-gate.md).
- **Design docs** under `docs/design/` ([SAD-001](docs/design/SAD-001-vision-and-architecture.md), [ADR-001…007](docs/design/)) are the source of truth; see [CONTRIBUTING.md](CONTRIBUTING.md).

### Requirements

- Go (toolchain pinned to `go1.25.11` via `go.mod`; `GOTOOLCHAIN=auto` fetches it automatically)
- Dev tools via `make tools`: [mockery v3](https://github.com/vektra/mockery), [golangci-lint v2](https://golangci-lint.run/), [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)

## Documentation

- [Vision & Architecture (SAD-001)](docs/design/SAD-001-vision-and-architecture.md) and [ADRs](docs/design/) — the conception
- [Development Roadmap](docs/analytics/gobpm%20Development%20Roadmap.md) — workstreams + milestones
- [Conformance scope](docs/bpmn-spec/conformance.md) and [BPMN 2.0 reference KB](docs/bpmn-spec/)
- [Documentation Index](README_INDEX.md) · [API Reference](https://pkg.go.dev/github.com/dr-dobermann/gobpm) · [Contributing](CONTRIBUTING.md) · [Changelog](CHANGELOG.md)

## License

LGPL-3.0 — see [LICENSE](LICENSE).
