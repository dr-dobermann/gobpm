# SRD-011 — Go-operation service reader (polymorphic Operation)

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-14 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-011 v.5 Process Data Flow](../design/ADR-011-process-data-flow.md) |

This SRD lands [ADR-011 v.5](../design/ADR-011-process-data-flow.md) §2.6: a `ServiceTask`'s `Operation` becomes **polymorphic by execution locus** — an **external message operation** (an out-of-process `Implementor`, message-only by locus, decoupled) and an in-process **Go operation** whose functor receives a **narrow, public, read-only `DataReader`** (the data plane's addressable reads from [ADR-010 v.2 §2.7](../design/ADR-010-process-data-model.md), landed by [SRD-010 v.1](SRD-010-addressable-data-access.md)) **and** its optional bound input message, **composing** reader-based and message-based access at the author's choice and **returning its result**. The Go-operation extension is registered in [SAD-001 v.1 §14.2](../design/SAD-001-vision-and-architecture.md).

## 1. Background & motivation

### 1.1 Current state (verified against the code)

- **`Operation` is a concrete struct, message-only.** `service.Operation` (`pkg/model/service/operation.go:41`) holds `implementation Implementor`, `inMessage`/`outMessage *bpmncommon.Message`, an error-class set, a name and a `BaseElement`. It is built by `NewOperation(name, inMsg, outMsg, implementor, …)` (`operation.go:52`) / `MustOperation` (`operation.go:93`), cloned per-instance by `Clone()` (`operation.go:116`), and run by `Run(ctx)` (`operation.go:169`), which calls `implementation.Execute(ctx, in)` with `in = inMessage.Item()` and writes the result into `outMessage`.
- **The Go functor today is an `Implementor`, not a kind.** `gooper.New(f, ers…)` (`pkg/model/service/gooper/gooper.go:34`) returns a `service.Implementor` whose `OpFunctor` is `func(ctx, *data.ItemDefinition) (*data.ItemDefinition, error)` (`gooper.go:22`). It receives **only its operation's input message item** — never the per-execution environment. So a Go functor cannot read a process property or a runtime variable by name, even though the data plane now exposes exactly that (SRD-010).
- **`ServiceTask` drives the message choreography.** `ServiceTask.Exec` (`pkg/model/activities/service_task.go:129`) clones the operation, calls `loadInputMessage` (`service_task.go:165`: `re.GetDataByID(op.IncomingMessage().Item().ID())`, Ready-state check, `inMessage` update), `op.Run(ctx)`, then `uploadOutputMessage` (`service_task.go:202`: wraps `outMessage.Item()` in a `Parameter` and `re.Put`s it).
- **The data reader the ADR needs already exists internally.** `renv.RuntimeEnvironment` (`internal/renv/renv.go`) exposes `GetData(name)`, `GetDataByID(id)`, `GetSources()`, `List(path)` (the last two added by SRD-010). `execEnv.GetData → frame.GetData` resolves `SOURCE/addr` (SRD-010 M2). But `renv` is **internal** — user-facing service code cannot import it.
- **The message accessors have no external readers.** `grep` for `IncomingMessage()`/`OutgoingMessage()` finds callers **only** in `service_task.go`. `MessageEventDefinition.Operation()` (`pkg/model/events/message.go:67`) has **zero callers**. `SendTask` (`send_task.go:11`) and `ReceiveTask` (`receive_task.go:18`) are field-only stubs — no `Exec`, no `exec.NodeExecutor`.

### 1.2 Why

ADR-011 v.5 §2.6 decides the polymorphic `Operation`, split by **execution locus**: an external (out-of-process) service stays message-only and decoupled *by locus* — it cannot receive an in-process reader — while in-process Go code **composes** data-access methods: its functor gets a narrow public reader **and** its optional bound input message, may declare an output message, and returns its result. The author chooses to use the reader, message I/O, or both. SRD-010 built the addressable data plane and surfaced it on `renv.RuntimeEnvironment`; this SRD exposes a **public** read-only face of it to service code and reshapes `Operation` into the two kinds. The blast radius is small because the message accessors have no external readers (§1.1).

## 2. Goals & scope

### 2.1 Goals (in scope)

- **G1.** `service.Operation` is an **interface** with a uniform `Execute(ctx, r DataReader) (*data.ItemDefinition, error)` plus identity/metadata (`ID`/`Name`/`Type`/`Errors`/`Clone`). Two implementations: `messageOperation` (canonical) and `goOperation` (gobpm-native). The interface is **minimal** — message accessors stay private to `messageOperation` (nothing external reads them, §1.1).
- **G2.** A **public, narrow, read-only `service.DataReader`** (in `pkg/model/service`): `GetData`/`GetDataByID`/`GetSources`/`List` and nothing else — no writes, no lifecycle, no events. `renv.RuntimeEnvironment` structurally satisfies it, so `ServiceTask` passes `re` directly (no adapter, no import cycle — `service` does not import `renv`).
- **G3.** The **Go operation** composes reader + optional message access: `gooper.OpFunctor = func(ctx, r service.DataReader, in *data.ItemDefinition) (*data.ItemDefinition, error)` (`in` is the bound input message item, `nil` if none declared); `gooper.New(name, f, opts…) (service.Operation, error)` builds it, with optional in/out messages and error classes supplied via functional options. Its `Execute` binds the input message from scope (if declared), calls the functor with the reader and that item, and returns its result (filling the output message when one is declared). Runtime variables are read by their explicit path `RUNTIME/<var>`; process properties by plain name.
- **G4.** The **message operation** preserves today's behaviour: `Execute` folds in the bind-input / run-implementation / produce-output choreography (the implementation still sees only its message). `Implementor` is unchanged.
- **G5.** `ServiceTask.Exec` becomes **kind-agnostic**: `out, err := op.Execute(ctx, re)`; if `out != nil`, commit it via `re.Put`. `loadInputMessage`/`uploadOutputMessage` move into `messageOperation`.
- **G6.** An example demonstrates a Go operation reading **a process property (plain name) and a runtime variable (`RUNTIME/STARTED_AT`)** through the reader and returning a result.

### 2.2 Non-goals (deferred, each with a named home)

- **Layering of the public reader / node-executor contracts** (which package they ultimately live in) — the layering ADR (ADR-012). This SRD fixes the reader's *existence and shape* (ADR-011 v.5 §2.6 decides that here); placement is provisional in `pkg/model/service`.
- **Observe-from-outside** (a caller inspecting a running instance's data) — ADR-013. This is *in-process* read access only.
- **Write access from service code** — out of scope by design: a Go operation **returns** its result; the `ServiceTask` commits it as the activity's output (ADR §2.6, "no write").
- **`SendTask`/`ReceiveTask` execution semantics** — still stubs; only their `Operation` field type changes. Their executors are a separate effort.
- **Concrete non-`RUNTIME` data sources** (business/JSON providers) and their registration — SRD-010 §2.2 deferral stands.

## 3. Requirements

### 3.1 Functional

| # | Requirement |
|---|---|
| FR-1 | `service.Operation` is an interface: `ID() string`, `Name() string`, `Type() string`, `Errors() []string`, `Clone() Operation`, `Execute(ctx context.Context, r DataReader) (*data.ItemDefinition, error)`. No message accessors on the interface. |
| FR-2 | `service.DataReader` (public, `pkg/model/service`): `GetData(name string) (data.Data, error)`, `GetDataByID(id string) (data.Data, error)`, `GetSources() []string`, `List(path string) ([]string, error)`. It mirrors the read subset of `renv.RuntimeEnvironment` so an `renv.RuntimeEnvironment` value satisfies it structurally. |
| FR-3 | `messageOperation` (the current struct, unexported) implements `Operation`. `NewOperation(name, inMsg, outMsg, implementor, …)` and `MustOperation(…)` return `Operation`. `messageOperation.Execute(ctx, r)`: if `inMessage` has an item, read it by id via `r` (Ready-state check) and update the message structure; run `implementation.Execute(ctx, inItem)`; reconcile against `outMessage` (today's `Run` rules — mismatched presence/absence is an error); return `outMessage.Item()` (or `nil`). The `Implementor` still sees only its message item. |
| FR-4 | `gooper.OpFunctor = func(ctx context.Context, r service.DataReader, in *data.ItemDefinition) (*data.ItemDefinition, error)`. `gooper.New(name string, f OpFunctor, opts ...Option) (service.Operation, error)` validates a non-empty `name` and a non-nil `f`, and returns a `goOperation` implementing `Operation` with `Type() == GoOperType`. Functional options supply the optional incoming/outgoing messages and error classes: `WithInMessage(*bpmncommon.Message)`, `WithOutMessage(*bpmncommon.Message)`, `WithErrors(...string)`. `goOperation.Execute(ctx, r)` binds the input message from `r` by id (Ready-state check) when one is declared, calls `f(ctx, r, in)` (`in` nil if no input message), and returns its result — filling the outgoing message when one is declared (wrapped on error). The old `Implementor`-returning `gooper.New` is removed. |
| FR-5 | `ServiceTask`: the `operation` field and `NewServiceTask`/`loadInputMessage`/`uploadOutputMessage` signatures use `service.Operation` (interface). `Exec` becomes `op := st.operation.Clone(); out, err := op.Execute(ctx, re); if out != nil { re.Put(wrap(out)) }`; `loadInputMessage`/`uploadOutputMessage` are removed (folded into `messageOperation`). `re` (an `renv.RuntimeEnvironment`) is passed where a `DataReader` is expected. |
| FR-6 | Field-type blast radius (no logic change): `events.MessageEventDefinition.operation` + its `NewMessageEventDefinition`/`MustMessageEventDefinition`/`Operation()` signatures, `activities.SendTask.Operation`, `activities.ReceiveTask.Operation` change `*service.Operation` / `service.Operation` (struct) → `service.Operation` (interface). |
| FR-7 | The `process-data` example's Go operation reads a process property by plain name **and** a runtime variable by `RUNTIME/STARTED_AT` through the reader, and returns a result; `basic-process` and `parallel-gateway` Go operations adopt the new `OpFunctor` signature (reader-only — they declare no messages). |

### 3.2 Non-functional

| # | Requirement |
|---|---|
| NFR-1 | Message-operation behaviour is unchanged: existing `service` / `activities` / `events` / `thresher` tests pass; all five examples run to exit 0. |
| NFR-2 | `service` does not import `internal/renv` (or any `internal/*`): the `DataReader` is satisfied structurally, keeping the public surface free of internal types. |
| NFR-3 | `make ci` green per milestone; diff-coverage ≥95 % (target 100 %) on touched files. |
| NFR-4 | Every new/changed exported symbol carries a doc comment; new constructors validate inputs with self-identifying errors (`gooper.New`: nil functor, empty name). |

## 4. Design & implementation plan

### 4.1 The polymorphic Operation

```mermaid
classDiagram
    class Operation {
        <<interface>>
        +ID() string
        +Name() string
        +Type() string
        +Errors() []string
        +Clone() Operation
        +Execute(ctx, r DataReader) (*ItemDefinition, error)
    }
    class messageOperation {
        -implementation Implementor
        -inMessage Message
        -outMessage Message
        -errors Set
    }
    class goOperation {
        -f OpFunctor
        -inMessage Message
        -outMessage Message
        -errors Set
    }
    Operation <|.. messageOperation
    Operation <|.. goOperation
    messageOperation ..> Implementor : external; sees only its message
    goOperation ..> DataReader : in-process; reader + optional messages
```

`Execute` is uniform; the kinds differ by **locus**. The external message kind binds its `inMessage` from scope (via the reader's `GetDataByID`) and hands the **Implementor** only that message — the out-of-process implementation stays decoupled. The in-process Go kind hands its **functor** the reader **and** its optional bound input message, composing both access methods, and returns whatever it produces. Either way, `ServiceTask` commits the returned item.

### 4.2 The reader is the read subset of the environment

`DataReader` is exactly the read-only methods of `renv.RuntimeEnvironment`:

```go
// pkg/model/service/datareader.go
type DataReader interface {
    GetData(name string) (data.Data, error)
    GetDataByID(id string) (data.Data, error)
    GetSources() []string
    List(path string) ([]string, error)
}
```

Because `renv.RuntimeEnvironment`'s method set is a superset, a value of that interface is assignable to `DataReader`; `ServiceTask.Exec` passes `re` straight through — no adapter, and `service` never imports `renv` (NFR-2).

### 4.3 ServiceTask collapses to Execute + Put

```go
op := st.operation.Clone()

out, err := op.Execute(ctx, re) // re satisfies service.DataReader
if err != nil {
    return nil, errs.New(/* operation execution failed */)
}

if out != nil {
    res := data.MustParameter(out.ID(),
        data.MustItemAwareElement(out, data.ReadyDataState))
    if err := re.Put(res); err != nil {
        return nil, errs.New(/* … */)
    }
}

return st.Outgoing(), nil
```

The message-binding (`loadInputMessage`) and output-wrapping (`uploadOutputMessage`) move verbatim into `messageOperation.Execute`; the Go kind never touches messages.

### 4.4 Worked example — a Go operation reading scope (FR-7)

```go
// a reader-only Go operation: read a process property + a runtime variable
// (no messages declared, so the functor's `in` is nil and ignored)
greet, err := gooper.New("greet",
    func(ctx context.Context, r service.DataReader, in *data.ItemDefinition) (*data.ItemDefinition, error) {
        who, err := r.GetData("customer") // process property, plain name
        if err != nil {
            return nil, err
        }

        started, err := r.GetData("RUNTIME/STARTED_AT") // runtime var, by path
        if err != nil {
            return nil, err
        }

        msg := fmt.Sprintf("Hello, %v! (instance started %v)",
            who.Value().Get(ctx), started.Value().Get(ctx))

        return data.NewItemDefinition(values.NewVariable(msg))
    })
// ...
task, err := activities.NewServiceTask("greet-task", greet)
```

A task that *also* wants message I/O declares it: `gooper.New("greet", fn, gooper.WithInMessage(in), gooper.WithOutMessage(out))` — then the functor's `in` carries the bound input item and the returned result fills `out`. At execution the `ServiceTask` calls `greet.Execute(ctx, re)`; the functor reads `customer` from the default scope and `RUNTIME/STARTED_AT` from the `RUNTIME` source (no collision — SRD-010 NFR-2), and the returned item is committed as the task's output.

### 4.5 Milestones (each = one commit, CI-green)

- **M1 — polymorphic `Operation` + `DataReader` (message side).** Introduce `service.DataReader`; turn `Operation` into the interface; rename the struct to `messageOperation` and fold the choreography into its `Execute`; `NewOperation`/`MustOperation` return the interface; `ServiceTask.Exec` → `Execute`+`Put` (drop `loadInputMessage`/`uploadOutputMessage`); change the field types in `events.MessageEventDefinition`, `SendTask`, `ReceiveTask`. `Implementor` and `gooper` (still returning an `Implementor`) are untouched, so every existing example keeps compiling and passing — behaviour-preserving (FR-1/2/3/5/6, NFR-1).
- **M2 — the Go operation kind + example reworks.** `gooper.OpFunctor` gains the reader and the optional input message; `goOperation` (carrying optional in/out messages) implements `service.Operation`; `gooper.New(name, f, opts…)` returns it with functional options (`WithInMessage`/`WithOutMessage`/`WithErrors`); remove the old `Implementor` path. Update `basic-process` and `parallel-gateway` (reader-only functor signature) and rework `process-data` into the showcase (FR-4/7). Smoke all five examples (FR-7, NFR-1).

### 4.6 Tests (per milestone; details §5)

`service` tests (`Operation` interface satisfied by both kinds; `messageOperation.Execute` binds input / produces output / errors on mismatch; `Clone`), a `DataReader`-satisfaction compile assertion (`var _ service.DataReader = (renv.RuntimeEnvironment)(nil)` lives in an internal test that may import both), `gooper` tests (functor receives the reader, returns its result; nil-functor / empty-name rejected; `goOperation.Execute` error wrapping), `activities` `service_task` tests (Execute+Put for both kinds via a stub reader), and the five examples as smoke.

## 5. Verification (Definition of Done)

| # | Check | Expectation |
|---|---|---|
| V1 | `service.Operation` is an interface implemented by `messageOperation` and `goOperation`; `NewOperation`/`MustOperation`/`gooper.New` return it (FR-1/3/4). | green |
| V2 | `service.DataReader` is public and structurally satisfied by `renv.RuntimeEnvironment` (compile assertion) (FR-2, NFR-2). | green |
| V3 | A message operation still binds its input from scope, runs the implementation, and produces its output; mismatched output presence errors as before (FR-3, NFR-1). | green |
| V4 | A Go operation's functor receives the reader and its returned item is committed by the `ServiceTask`; reading a property + `RUNTIME/STARTED_AT` works (FR-4/5/7). | green |
| V5 | `gooper.New` rejects a nil functor and an empty name with self-identifying errors (NFR-4). | green |
| V6 | `service` / `activities` / `events` / `thresher` suites pass; all five examples run to exit 0 (NFR-1). | green |
| V7 | `make ci` green; diff-coverage ≥95 % on touched files (NFR-3). | pass |

## 6. Risks & regressions

- **Interface-ifying a widely-held type.** `*service.Operation` is held by `events`, `SendTask`, `ReceiveTask`. The change is field-type only — no caller reads the message accessors (§1.1), so the surface is mechanical. V6 (suites + examples) is the backstop.
- **Folding choreography into `messageOperation.Execute`.** The bind/run/produce logic moves verbatim; the `Implementor` contract is unchanged (still sees only its message). V3 asserts the preserved behaviour.
- **`gooper` signature change.** `OpFunctor` and `New` change shape; all `gooper`-using examples are updated in the same milestone (M2) so the tree stays green.
- **Structural `DataReader` satisfaction drifting.** If `renv.RuntimeEnvironment` later renames a read method, `ServiceTask` would stop compiling — a compile-time signal, and V2's assertion pins the relationship.

## 7. Implementation summary

Landed on `feat/go-operation-service-reader` in two milestone commits (plus the
doc commits), all `make ci`-green with **100 %** diff-coverage on touched files.

### 7.1 Milestones

| Milestone | Commit | Scope |
|---|---|---|
| (doc) ADR-011 v.5 amendment | `bd7f41c` | §2.6 split by execution locus; in-process composes reader + optional messages (EN+RU) + SAD §14.2. |
| (doc) SRD-011 alignment | `25666c7` | Draft amended to the composition model + functional options. |
| M1 — polymorphic Operation + DataReader | `0b52290` | `Operation` interface; `messageOperation` (Execute folds bind/run/produce); public `DataReader`; `ServiceTask` = `Execute`+`Put`; field-type blast radius. |
| M2 — Go operation kind + examples | `2cd4ed8` | `gooper` Go op (`OpFunctor(ctx, r, in)`, `New(name, f, opts…)`, `WithInMessage`/`WithOutMessage`/`WithErrors`); shared `service.BindInput`; example reworks incl. the `process-data` showcase. |

### 7.2 Files

- `pkg/model/service/operation.go` — `Operation` interface, `messageOperation`, exported `BindInput`.
- `pkg/model/service/datareader.go` (new) — public `DataReader`.
- `pkg/model/service/gooper/gooper.go` — `goOperation` (composing reader + optional messages), functional options.
- `pkg/model/activities/service_task.go` — `Exec` = `Execute`+`Put`.
- `pkg/model/events/message.go`, `pkg/model/activities/{send,receive}_task.go` — field-type (interface).
- `examples/{basic-process,parallel-gateway,process-data}` — new functor; `process-data` reads a property + `RUNTIME/STARTED_AT`.

### 7.3 Verification results

| Check | Result |
|---|---|
| V1 Operation interface + both kinds | ✅ `var _ Operation/_ service.Operation` asserts; `operation_test`, `gooper_test` |
| V2 DataReader public + structurally satisfied | ✅ `var _ service.DataReader = (renv.RuntimeEnvironment)(nil)` |
| V3 message op binds/runs/produces; mismatch errors | ✅ `operation_test` (bind/produce/mismatch) |
| V4 Go op functor gets the reader; result committed; reads RUNTIME/STARTED_AT | ✅ `gooper_test`; `process-data` smoke |
| V5 `gooper.New` rejects nil functor / empty name | ✅ `gooper_test` |
| V6 suites pass; 5 examples exit 0 | ✅ `make ci`; all examples ran |
| V7 `make ci` + diff-coverage 100 % | ✅ |

### 7.4 Deviations from the §4 plan

- The Go operation is no longer message-free (ADR-011 v.5): it composes reader +
  optional messages, so `gooper.New` uses functional options and `OpFunctor`
  carries the bound input item. M1 (message side) landed unchanged.
- Extracted the shared input binding as the exported `service.BindInput` (DRY
  across `messageOperation` and `goOperation`).

## 8. References

- [ADR-011 v.5 Process Data Flow](../design/ADR-011-process-data-flow.md) — §2.6 (the polymorphic `Operation`: message kind + Go kind with a narrow public reader) this SRD lands.
- [ADR-010 v.2 Process Data Model](../design/ADR-010-process-data-model.md) — §2.7 (addressable data access) the reader exposes; runtime variables read via `RUNTIME/<var>`.
- [SRD-010 v.1 Addressable data access](SRD-010-addressable-data-access.md) — the data plane (`GetData`/`GetDataByID`/`GetSources`/`List` on `renv.RuntimeEnvironment`) the `DataReader` mirrors; sideways reference.
- [SAD-001 v.1 Vision & Architecture](../design/SAD-001-vision-and-architecture.md) — §14.2 registers the Go-operation-with-a-data-reader extension.

## 9. Open questions

- None. The `Operation` interface surface (minimal — no message accessors, confirmed), the `DataReader` placement (`pkg/model/service`, confirmed; layering ADR may relocate), the `Execute(ctx, DataReader)` uniform signature with the message choreography folded into `messageOperation`, the in-process Go operation composing reader + optional message access (ADR-011 v.5 — author's choice), and `gooper.New(name, f, opts…)` (functional options for messages/errors) returning the Go kind are decided above. The node-level `MessageProducer`/`MessageConsumer` seam, `SendTask`/`ReceiveTask` execution, and concrete non-`RUNTIME` sources are deferred (§2.2; ADR-011 v.5 §2.6).

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-14 | Ruslan Gabitov | Accepted (landed). Lands ADR-011 v.5 §2.6: `service.Operation` becomes a polymorphic interface (`Execute(ctx, DataReader)`) split by **execution locus** — a canonical external `messageOperation` (folds the bind/run/produce choreography; `Implementor` unchanged; message-only by locus) and an in-process gobpm-native `goOperation` that **composes** reader + optional message access (`OpFunctor = func(ctx, r DataReader, in *data.ItemDefinition) …`; `gooper.New(name, f, opts…)` with `WithInMessage`/`WithOutMessage`/`WithErrors`). `DataReader` is the read subset of `renv.RuntimeEnvironment`, satisfied structurally (no `internal` import). `ServiceTask.Exec` collapses to `Execute`+`Put`. Field-type blast radius in `events.MessageEventDefinition`/`SendTask`/`ReceiveTask` (no message-accessor callers). The node-level `MessageProducer`/`MessageConsumer` seam is deferred to the `SendTask`/`ReceiveTask` executor SRD. Two milestones (message-side interface, behaviour-preserving → Go-operation kind + example showcase reading a property + `RUNTIME/STARTED_AT`). Implements ADR-011 v.5. |
