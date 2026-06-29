# GoBPM Code Review — Comprehensive Analysis

**Date**: 2025-06-29
**Scope**: Full codebase — `pkg/thresher/`, `pkg/model/`, `internal/`, `pkg/errs/`, `pkg/set/`, `pkg/clock/`, `pkg/messaging/`, `pkg/repository/`, `pkg/tasks/`, tests, build system, examples

---

## Table of Contents

1. [Critical Findings](#1-critical-findings)
2. [Medium Priority](#2-medium-priority)
3. [Low Priority / Improvements](#3-low-priority--improvements)
4. [Modern Go Recommendations](#4-modern-go-recommendations)
5. [Thresher Engine Analysis](#5-thresher-engine-analysis)
6. [Model Layer Analysis](#6-model-layer-analysis)
7. [Internal Packages Analysis](#7-internal-packages-analysis)
8. [Public API & Utilities Analysis](#8-public-api--utilities-analysis)
9. [Testing & Build Analysis](#9-testing--build-analysis)

---

## 1. Critical Findings

### 1.1 Race condition in `pkg/errs` — global mutable state without synchronization

**Location**: `pkg/errs/errors.go:180-214`

```go
var (
    dontPanic  bool
    panicHook  PanicHandler
)
```

`SetDontPanic`, `DontPanic`, `Panic`, `RegisterPanicHandler`, `DropPanicHandler` all read/write these package-level variables **without any mutex**. In a library used concurrently, this is a data race.

**Fix**: Use `sync.Once` or `sync.RWMutex` to protect global panic state.

---

### 1.2 `depguard` configured but NOT enabled

**Location**: `.golangci.yml:9-31` (enabled linters) vs `.golangci.yml:33-67` (depguard rules)

The architectural import direction rules are written:
- `core-no-runtime-no-adapters` — core must NOT import runtime or adapters
- `examples-no-internal` — examples must NOT import internal packages
- `model-no-internal` — `pkg/model` must NOT import internal
- `adapters-no-cross-adapter` — adapters must NOT import other adapters

But `depguard` is **not listed** in the enabled linters. These rules are unenforced.

**Fix**: Add `depguard` to the enabled linters list.

---

### 1.3 `seenKeys` grows without bound — memory leak

**Location**: `pkg/thresher/thresher.go:144`

Completed instances never release their correlation key. A long-running engine:
- Leaks map entries indefinitely
- Silently blocks re-runs of the same correlation value

**Fix**: Implement release-on-terminal-state mechanism.

---

### 1.4 `eventHub.Run` error discarded

**Location**: `pkg/thresher/thresher.go:319`

```go
go func() {
    if err := e.eventHub.Run(e.ctx); err != nil {
        // error silently discarded
    }
}()
```

A fatal hub failure goes unnoticed until shutdown times out.

**Fix**: Add logging at minimum, or use a channel to surface the error.

---

### 1.5 Timer definition validation rejects valid BPMN

**Location**: `pkg/model/events/timer.go:38-44`

```go
if tDate == nil && (tCycle == nil || tDuration == nil) {
    return nil, errs.New(...)
}
```

This rejects timers with **only Cycle** or **only Duration**, but BPMN allows both:
- TimeCycle alone (repeating timer)
- TimeDuration alone (single delay)

**Fix**: Validate that at least one of Cycle/Duration/Date is present, not all combinations.

---

### 1.6 `catchEvent.UploadData` missing format argument

**Location**: `pkg/model/events/event.go:360`

```go
errs.M("output #%s isn't ready")  // %s with no argument
```

Renders as `%!s(MISSING)` at runtime.

**Fix**: Add the missing argument.

---

## 2. Medium Priority

### 2.1 `golang.org/x/maps` should be stdlib `maps`

**Locations**: `go.mod:10`, `pkg/model/activities/activity.go:8`, `pkg/model/process/process.go:9`, `internal/instance/instance.go:22`

Project uses Go 1.25. Stdlib `maps` available since Go 1.21. The `x/exp` dependency is unnecessary.

**Fix**: Replace `golang.org/x/exp/maps` with `maps` from stdlib. Remove `x/exp` from `go.mod`.

---

### 2.2 `errs.D()` compares `any` to empty string

**Location**: `pkg/errs/error_options.go:99`

```go
if k != "" && v != "" {  // v is typed as `any`
    cfg.details[k] = v
}
```

Comparing `any` to `""` is semantically wrong. Nil values are not skipped as likely intended.

**Fix**: Replace `v != ""` with `v != nil`.

---

### 2.3 `errs.JSON()` panics instead of returning error

**Location**: `pkg/errs/errors.go:107-115`

```go
func (ae *ApplicationError) JSON() []byte {
    js, err := json.Marshal(ae)
    if err != nil {
        Panic("couldn't convert application error to json: " + err.Error())
        return nil
    }
    return js
}
```

A library method that panics is an anti-pattern. Callers must nil-check the return.

**Fix**: Change signature to `([]byte, error)`.

---

### 2.4 `errs.Error()` — string concatenation in loop

**Location**: `pkg/errs/errors.go:119-139`

Classic Go anti-pattern. O(n²) string building.

**Fix**: Use `strings.Builder`.

---

### 2.5 Two competing error patterns in model

- `flow/`, `events/`, `gateways/`, `activities/` → `errs.New(errs.M(...), errs.C(...))` — 292 usages
- `data/` → `fmt.Errorf` — 115 usages

Errors from `data/` have no error class. `errors.As(err, &ae)` cannot find them.

**Fix**: Unify on `errs.New` with error classes across all model packages.

---

### 2.6 `ElementType.Validate()` rejects `DataObjectElement`

**Location**: `pkg/model/flow/element.go:30-38`

```go
func (et ElementType) Validate() error {
    switch et {
    case NodeElement, SequenceBaseElement:
        return nil
    // DataObjectElement missing!
    }
}
```

**Fix**: Add `DataObjectElement` to the valid set.

---

### 2.7 Typo in public API: `IsMultyinstance` / `WithMultyInstance`

**Locations**: `pkg/model/activities/task.go:67`, `task_options.go:33`

BPMN standard spelling is "multi-instance".

**Fix**: Rename to `IsMultiInstance` / `WithMultiInstance` (breaking change, requires version bump).

---

### 2.8 `scanInstantiatingStarts` — high cyclomatic complexity

**Location**: `pkg/thresher/instance_starter.go:109-172`

Estimated cyclomatic complexity ~12 with nested loops, type assertions, and type switch.

**Fix**: Extract event-based gateway arm resolution into a named helper.

---

### 2.9 Timer waiter diagnostic data shows wrong value

**Location**: `pkg/thresher/../internal/eventproc/eventhub/waiters/timer.go:248-253`

```go
errs.D("current_state", eventproc.WSReady)  // should be tw.state
```

Logs expected state instead of actual state.

---

### 2.10 40+ uses of `reflect.TypeOf` instead of `%T`

**Throughout `pkg/model/`** — `reflect.TypeOf(cfg).String()` is replaceable with `fmt.Sprintf("%T", cfg)`. Avoids importing `reflect`.

---

### 2.11 `ApplicationError` class system incompatible with `errors.Is`/`errors.As`

**Location**: `pkg/errs/errors.go:94-103`

Error classes are string constants (`errs.ObjectNotFound`), not error values. `errors.Is(err, errs.ObjectNotFound)` never matches because `ObjectNotFound` is a string, not an error.

**Fix**: Define error class sentinels as `error` values, or document the `HasClass()` pattern.

---

### 2.12 `checkstr.go` — unnecessary complexity

**Location**: `pkg/errs/checkstr.go:11-21`

IIFE for filtering error classes is convoluted. `C()` already handles empty strings.

```go
// Current (complex)
func CheckStr(str, errMsg string, errorClasses ...string) error {
    if str == "" {
        return New(
            M(errMsg),
            func(ecc []string) errOption { /* IIFE */ }(errorClasses))
    }
    return nil
}

// Simplified
func CheckStr(str, errMsg string, errorClasses ...string) error {
    if str == "" {
        return New(M(errMsg), C(errorClasses...))
    }
    return nil
}
```

---

## 3. Low Priority / Improvements

### 3.1 Typos

| Location | Current | Should be |
|----------|---------|-----------|
| `pkg/errs/errors.go:37` | `BulidingFailed` | `BuildingFailed` |
| `internal/eventproc/eventhub/waiters/timer.go:24` | `TimerWatierError`, `ERRROR` | `TimerWaiterError`, `ERROR` |
| `pkg/model/events/start.go:78` | `innapropriate` | `inappropriate` |
| `pkg/model/events/end.go:75` | `innapropriate` | `inappropriate` |
| `internal/instance/instance_test.go:30` | `TestInstIvalidParams` | `TestInstInvalidParams` |
| `pkg/model/activities/task.go:19` | `multyInstance` | `multiInstance` |

### 3.2 `CorrelationKey` exported fields

**Location**: `pkg/model/bpmncommon/correlation.go:75-84`

Every other BPMN element uses unexported fields with accessors. `CorrelationKey` exposes `Name` and `Properties` directly, allowing external mutation.

### 3.3 `errs.D()` — cryptic function name

Single-letter exported functions are hard to discover. Consider renaming to `Detail()` or `WithData()`.

### 3.4 Value/pointer receiver inconsistency in `set`

**Location**: `pkg/set/set.go:68`

`All()` uses value receiver while all other methods use pointer receiver. Should be documented.

### 3.5 `ApplicationError` receiver asymmetry

- `Error()` at `errors.go:118` — pointer receiver
- `MarshalJSON()` at `errors.go:151` — value receiver

Mixing pointer/value receivers on the same type is a code smell.

### 3.6 Stale example READMEs

- `examples/basic-process/README.md:28-29` — expected output doesn't match actual `main.go` output
- `examples/README.md:94` — `thresher.New()` shown without required ID argument
- `examples/README.md:57` — says "Go 1.21+" but project requires Go 1.25.11

### 3.7 Missing test patterns

| Pattern | Status | Candidates |
|---------|--------|------------|
| Benchmarks | 0 in codebase | Snapshot.Clone, EventHub, Instance lifecycle |
| Fuzz tests | 0 in codebase | `scope.NewDataPath()`, expression evaluation |
| `t.Parallel()` | 0 usages | Most unit tests |
| `testing.Short()` | 0 usages | Long-running tests |
| `t.Cleanup()` | 3 usages | `data.CreateDefaultStates()` not cleaned |

### 3.8 `make test` lacks `-race`

**Location**: `Makefile:81` — `go test -v -cover ./...` — no `-race`. Only `make test-all` has it.

### 3.9 `errorlint` not enabled

No lint enforcement of `errors.Is`/`errors.As` usage patterns, `%w` formatting, or type-assertion error checks.

---

## 4. Modern Go Recommendations

### 4.1 Replace `x/exp/maps` with stdlib `maps`

```go
// Before
import "golang.org/x/exp/maps"
result := maps.Values(m)

// After (Go 1.21+)
import "maps"
result := maps.Values(m)
```

### 4.2 Replace `reflect.TypeOf(x).String()` with `%T`

```go
// Before
reflect.TypeOf(cfg).String()

// After
fmt.Sprintf("%T", cfg)
```

40+ locations across `pkg/model/`.

### 4.3 Use `errors.Join` for multi-error collection

Replace manual `[]error` accumulation with `errors.Join(errs...)` where appropriate.

### 4.4 Add `errorlint` to linter config

Catches:
- Improper `errors.Is`/`errors.As` usage
- Missing `%w` in `fmt.Errorf`
- Type-assertion error checks

### 4.5 Enable `depguard` in `.golangci.yml`

Rules already written, just not activated.

### 4.6 Extract `internal/testutil/` package

Shared test helpers duplicated across packages:
- `capLogger` — duplicated 3 times
- `linearProcess`, `runEngine` — near-duplicates between `pkg/thresher/` and `internal/instance/`
- `buildPlainSnapshot` — duplicated

### 4.7 Add benchmarks for critical paths

```go
func BenchmarkSnapshotClone(b *testing.B) { ... }
func BenchmarkEventHubPropagate(b *testing.B) { ... }
func BenchmarkInstanceRun(b *testing.B) { ... }
func BenchmarkRegistryRegister(b *testing.B) { ... }
```

### 4.8 Generic base waiter (optional)

Timer, message, and signal waiters share ~200 lines of duplicated code:
- `processors []eventproc.EventProcessor` with `sync.Mutex`
- `AddEventProcessor`, `RemoveEventProcessor`, `EventProcessors`
- `State`, `Done` methods

A `baseWaiter[T flow.EventDefinition]` generic struct could eliminate this.

---

## 5. Thresher Engine Analysis

### Concurrency

**Rating: Excellent**

- Single-writer loop pattern (`instance.go:662-822`) — loop goroutine owns all lifecycle state
- Tracks emit `trackEvent` values onto unbuffered `inst.events` channel — no lock contention on hot path
- `parkCh` correctly buffered(1) to avoid blocking the loop
- All goroutines have `Done()` channels — proper lifecycle management

**Minor concern**: `emit()` silently drops events when `loopDone` is closed during teardown — acceptable since `stopAll()` already cancelled everything.

### Resource Management

**Rating: Excellent**

- Every waiter goroutine: `defer close(done)`, `defer cancel()`
- `EventHub.Shutdown` drains all waiters with `WaitGroup` + ctx deadline
- Custom goroutine leak checker exists (`leakcheck_test.go`)
- No unowned channels

### Complexity

- `loop()`: 160 lines — largest function, but properly decomposed via extraction
- `applyEvent()`: 85 lines, 11 cases — each case is 1-8 lines, delegating to helpers
- Domain complexity (BPMN token routing) is inherent and well-managed

---

## 6. Model Layer Analysis

### Interface Design

**Rating: Excellent — textbook ISP**

Clean layered hierarchy: `Element` → `Node` → `ActivityNode`/`EventNode`/`GatewayNode`

Fine-grained connection interfaces: `SequenceSource`, `SequenceTarget`

`boundaryHost` interface at `events/boundary.go:26-31` — narrow attachment logic.

### Clone/Copy Patterns

**Rating: Good, one concern**

Deliberate shallow-share design documented well. Concern: `SequenceFlow.CloneFlow` copies `conditionExpression` by interface value — if `FormalExpression` has mutable evaluation state, two cloned flows share it.

### Value/Pointer Consistency

Mostly consistent with one issue: `data.SrcState` — global singletons are pointers, `ItemAwareElement.dataState` stores a value, and `UpdateState` takes a pointer but dereferences it. Fragile for future changes.

### Code Duplication

- Option-dispatch pattern repeated 10+ times (type-switch on `options.Option`)
- `unmarkedFlows` method duplicated between `inclusive.go` and `complex.go`
- `NewEndEvent` / `NewStartEvent` option-dispatch nearly identical

---

## 7. Internal Packages Analysis

### Concurrency Architecture

**Rating: Excellent**

- Clear ownership rules documented in comments
- `atomic.Pointer`, `atomic.Bool`, `atomic.Uint32`, `atomic.Int64` — modern typed atomics
- RWMutex used appropriately (read-heavy paths)
- Unbuffered channels for single-reader patterns

### Error Propagation

**Rating: Good**

Structured errors with `errs.M()`, `errs.C()`, `errs.D()` throughout. ~10 `fmt.Errorf` sites in `instance.go` break the pattern:
- `instance.go:198,524,529,538,1475-1499`
- `track.go:304`
- `timer.go:241-243`

### Interface Usage

**Rating: Excellent**

- Compile-time assertions at every seam
- Structural typing for extension points
- Type aliases for internal/public boundary
- `boundaryHoster` narrow interface pattern

---

## 8. Public API & Utilities Analysis

### Package Architecture

**Rating: Excellent** — "Interface package + default implementation" pattern consistently applied:

| Interface | Default | Test Fake |
|-----------|---------|-----------|
| `pkg/clock` | `clock/syscl` | `clock/clocktest` |
| `pkg/auth` | `auth/allowall` | — |
| `pkg/messaging` | `messaging/membroker` | — |
| `pkg/repository` | `repository/memrepo` | — |
| `pkg/tasks` | `tasks/localdispatcher` | — |
| `pkg/observability` | `observability/noop` | `memtrace`, `memmetrics` |

### Dependency Graph

No circular dependencies. `renv` is the aggregation point at the top of the `pkg/` tree.

### `pkg/errs` Issues Summary

1. Global mutable panic state — data race (Critical)
2. `JSON()` panics instead of returning error
3. `Error()` uses string concatenation in loop
4. `D()` compares `any` to `""`
5. `ApplicationError` class system not compatible with `errors.Is`
6. Options `E`, `M`, `C`, `D` are cryptic
7. Value/pointer receiver asymmetry

---

## 9. Testing & Build Analysis

### Test Quality

**Strengths:**
- Table-driven tests in ~15% of tests
- 138 `t.Helper()` calls
- 23 `_internal_test.go` files for white-box testing
- 198 mock usages — "mock only what you must" philosophy
- Dedicated race stress tests (500 iterations)

**Weaknesses:**
- 35 `time.Sleep` calls instead of `require.Eventually`
- 0 benchmarks, 0 fuzz tests, 0 `t.Parallel()`
- `capLogger` helper duplicated 3 times
- `reflect.DeepEqual` in `set` tests instead of `slices.Equal`

### Linter Config

**Enabled**: 21 linters including `gosec`, `govet` (25+ sub-analyzers), `gocyclo` (15), `funlen` (100 lines), `dupl`, `gocritic`

**Missing**:
- `depguard` — rules written but not enabled
- `errorlint` — no error wrapping enforcement
- `exhaustive` — no switch exhaustiveness checking
- `lll` — configured (120 chars) but not enabled
- `nestif` — configured but not enabled

### Build System

**Strengths:**
- `require-tool` guards prevent silent no-ops
- Multi-module support for monorepo
- Diff-coverage gate (95% minimum on changed lines)
- `make ci` mirrors GitHub CI exactly

**Weaknesses:**
- `make test` lacks `-race` (only `make test-all` has it)
- No `-count=1` in test targets
- Coverage only for root module

---

## Summary: Priority Matrix

| # | Finding | Severity | Effort |
|---|---------|----------|--------|
| 1 | Race in `pkg/errs` global state | 🔴 Critical | Medium |
| 2 | `depguard` not enabled | 🔴 Critical | Trivial |
| 3 | `seenKeys` memory leak | 🔴 Critical | Medium |
| 4 | `eventHub.Run` error discarded | 🔴 Critical | Trivial |
| 5 | Timer validation rejects valid BPMN | 🔴 Critical | Low |
| 6 | `UploadData` missing format arg | 🔴 Critical | Trivial |
| 7 | Replace `x/exp/maps` with stdlib | 🟡 Medium | Trivial |
| 8 | `errs.D()` `any` vs `""` comparison | 🟡 Medium | Low |
| 9 | `errs.JSON()` panics | 🟡 Medium | Low |
| 10 | `errs.Error()` string concat in loop | 🟡 Medium | Trivial |
| 11 | Two error patterns in model | 🟡 Medium | High |
| 12 | `ElementType.Validate()` incomplete | 🟡 Medium | Trivial |
| 13 | `IsMultyinstance` typo in API | 🟡 Medium | Low (breaking) |
| 14 | `scanInstantiatingStarts` complexity | 🟡 Medium | Medium |
| 15 | Timer waiter wrong diagnostic data | 🟡 Medium | Trivial |
| 16 | 40x `reflect.TypeOf` → `%T` | 🟡 Medium | Low |
| 17 | `ApplicationError` class ≠ `errors.Is` | 🟡 Medium | High |
| 18 | Add benchmarks | 🟢 Low | High |
| 19 | Add fuzz tests | 🟢 Low | Medium |
| 20 | Extract `internal/testutil` | 🟢 Low | Medium |
| 21 | Add `t.Parallel()` | 🟢 Low | Medium |
| 22 | Fix stale example READMEs | 🟢 Low | Low |
| 23 | Fix typos in constants/names | 🟢 Low | Trivial |
| 24 | Enable `errorlint` linter | 🟢 Low | Trivial |
| 25 | Add `-race` to `make test` | 🟢 Low | Trivial |
