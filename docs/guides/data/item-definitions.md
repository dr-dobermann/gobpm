---
title: Item definitions & item-aware elements
description: The BPMN data-typing layer in gobpm — ItemDefinition, ItemAwareElement, Property, and DataState — with the real construction and read/write calls.
---

# Item definitions & item-aware elements

Below the [value model](value-model.md) sits BPMN's data-typing layer: an
**`ItemDefinition`** says *what shape* a datum has, an **`ItemAwareElement`**
is the *variable* that carries a value of that shape, a **`Property`** is a
named item-aware element attached to a process/activity/event, and a
**`SrcState`** (BPMN *DataState*) records *where in its lifecycle* a datum is.
A **`Parameter`** is the item-aware element in an activity's input/output set.
This page is the developer reference for these types — their constructors, the
options, and how you read and write through them.

> These are model-build types. You wire them up when you declare process
> properties and task parameters; at run time the engine clones them per
> instance and the values move through scope. See
> [Working with data — overview](index.md) for the runtime data plane.

## The layer at a glance

| Type | BPMN role | What it holds |
|---|---|---|
| `ItemDefinition` | item definition (the *type*) | a `Value` structure + a `Kind` (Physical / Information) |
| `ItemAwareElement` | the *variable* | an `ItemDefinition` + a current `SrcState` + a live `Value` |
| `Property` | a named item-aware element on a container | an `ItemAwareElement` + a name; not shown on the diagram |
| `SrcState` | data state (lifecycle marker) | a state name (e.g. `READY_DATA_STATE`) |
| `Parameter` | data input / data output | an `ItemAwareElement` + required/while-executing role |

`Property`, `Parameter`, and (elsewhere) `DataObject` all **embed**
`ItemAwareElement` — they are the same variable construct wearing different
BPMN hats.

## ItemDefinition — the type

`ItemDefinition` wraps a `Value` (see the [value model](value-model.md)) as a
reusable data shape.

```go
func NewItemDefinition(value Value, opts ...options.Option) (*ItemDefinition, error)
func MustItemDefinition(value Value, opts ...options.Option) *ItemDefinition
```

| Parameter | Meaning |
|---|---|
| `value` | the structure — any `data.Value` (`values.NewVariable`, `NewArray`, `NewRecord`, …). |
| `opts` | see below. `Must…` panics on error; `New…` returns it. |

| Option | Effect |
|---|---|
| `WithKind(kind ItemKind)` | `PhysicalKind` or `InformationKind` (default `InformationKind`). |
| `WithImport(imp *foundation.Import)` | reference an externally-defined structure. |
| `foundation.WithID(id)` / `foundation.WithDoc(…)` | id and documentation. |

Building one from a plain Go value:

```go
idef := data.MustItemDefinition(
    values.NewVariable("dr.Dobermann"),
    foundation.WithID("user_name"))
```

Its accessors: `Structure() Value` (the wrapped value), `Kind() ItemKind`,
`IsCollection() bool`, and `Import() *foundation.Import`.

## ItemAwareElement — the variable

An `ItemAwareElement` binds an `ItemDefinition` to a current `SrcState` and
exposes the live `Value`. It is BPMN's variable construct — Data Objects, Data
Stores, Properties, DataInputs and DataOutputs are all item-aware elements.

```go
func NewItemAwareElement(item *ItemDefinition, state *SrcState,
    baseOpts ...options.Option) (*ItemAwareElement, error)
func MustItemAwareElement(item *ItemDefinition, state *SrcState,
    baseOpts ...options.Option) *ItemAwareElement
```

An options-driven twin builds it from parts instead of positional args:

```go
func NewIAE(opts ...options.Option) (*ItemAwareElement, error)
```

| `NewIAE` option | Effect |
|---|---|
| `WithIDef(iDef *ItemDefinition)` | set the item definition. |
| `WithIDefinition(value Value, opts…)` | build the item definition from a value inline. |
| `WithState(ds *SrcState)` | set the current data state. |

Read/write surface you use at run time:

| Method | Role |
|---|---|
| `Value() Value` | the live value — `.Get(ctx)` to read, `.Update(ctx, v)` to write. |
| `ItemDefinition()` / `Subject()` | the backing item definition. |
| `State() SrcState` | the current data state. |
| `UpdateState(newState *SrcState) error` | move the datum to a new state. |
| `Name()` / `SetName(name)` | the element's name. |
| `IsCollection()` | whether the item definition is a collection. |
| `Clone() (*ItemAwareElement, error)` | per-instance copy (the engine calls this). |

## SrcState — the data state

`SrcState` is BPMN's *DataState*: an optional lifecycle marker on an item-aware
element. The spec leaves the concrete states open; gobpm ships three canonical
ones plus package-level pointers you pass straight into constructors.

```go
const (
    StateUndefined   = "UNDEFINED_DATA"
    StateUnavailable = "UNAVAILABLE_DATA"
    StateReady       = "READY_DATA_STATE"
)

var (
    UndefinedSrcState    *SrcState
    UnavailableDataState *SrcState
    ReadyDataState       *SrcState
)
```

> Call `data.CreateDefaultStates()` once at start-up before using the shipped
> `*SrcState` pointers — the example does this in its first line.

Build your own state with `NewSrcState(name, …)` / `MustSrcState(name, …)`.
`Name()` returns the state name.

## Property — a named item-aware element

A `Property` is an `ItemAwareElement` with a name, attached to a Process,
Activity, or Event. Unlike a Data Object it is **not** drawn on the diagram.

Three ways to build one, from most explicit to most convenient:

| Constructor | Shape |
|---|---|
| `NewProperty(name, item *ItemDefinition, state *SrcState, baseOpts…)` | positional; the full item + state. |
| `NewProp(name, iaeOpt IAEAdderOption)` | options-driven; supply the item-aware element via `WithIAE(…)`. |
| `MustProperty(…)` / `MustProp(…)` | the panicking twins. |

Attach properties to a container with the `WithProperties(props…)` option (a
`PropertyOption` accepted by `process.New`, activities, etc.):

```go
proc, err := process.New("data-demo",
    data.WithProperties(
        data.MustProperty("user_name",
            data.MustItemDefinition(
                values.NewVariable("dr.Dobermann"),
                foundation.WithID("user_name")),
            data.ReadyDataState)))
```

At run time this property lands in the instance's root container scope; a task
reads it by plain name through the operation's `DataReader` (see below).

## Parameter — data input / data output

A `Parameter` substitutes BPMN's *DataInput* / *DataOutput*: it is an
`ItemAwareElement` carrying its role in an activity's single input/output set.
By default a parameter is **required** and **not** while-executing.

```go
func NewParameter(name string, iae *ItemAwareElement,
    opts ...ParameterOption) (*Parameter, error)
func MustParameter(name string, iae *ItemAwareElement,
    opts ...ParameterOption) *Parameter
```

| `ParameterOption` | Effect |
|---|---|
| `Optional()` | the parameter need not be present. |
| `WhileExecuting()` | evaluated during execution rather than at start/completion. |

Two shortcuts build the item definition for you — the datum shape every runtime
commit path assembles:

| Constructor | Shape |
|---|---|
| `ReadyParameter(name, item *ItemDefinition)` | wrap an item as a Ready parameter. |
| `ReadyValueParameter(name, value Value, itemOpts…)` | build the item from a value, then wrap it. |

Introspection: `IsOptional()`, `IsWhileExecuting()`, `Name()`.

Declaring a task's output parameter (from the example):

```go
outParam := data.MustParameter(name+" result",
    data.MustItemAwareElement(
        data.MustItemDefinition(
            values.NewVariable(""),
            foundation.WithID(resID)),
        data.UnavailableDataState))

st, err := activities.NewServiceTask(name, op,
    activities.WithParameters(data.Output, outParam))
```

`data.Output` is a `Direction` (`Input` / `Output`); `Opposite(dir)` flips it.

## Reading & writing at run time

A model-time item-aware element becomes live data in scope. Inside a service
operation you don't touch these types directly — you resolve values by name
through the read-only `service.DataReader`, and the returned datum exposes the
same `Value` surface:

```go
who, err := r.GetData("user_name")          // the process Property, by name
if err != nil {
    return nil, fmt.Errorf("read user_name: %w", err)
}
res := fmt.Sprintf("%s, %s!", greeting, who.Value().Get(ctx))
```

To produce output, the operation returns an `*ItemDefinition` the task's output
parameter fills:

```go
return data.MustItemDefinition(
    values.NewVariable(res),
    foundation.WithID(resID)), nil
```

## Run it

`examples/process-data` declares a `user_name` **Property**, reads it in two
parallel branches through their operations, and each branch returns an
`ItemDefinition` that flows to a bound Data Object:

```
  ▶ greet-a produced "Hello, dr.Dobermann!" (instance started 2026-07-27 …)
  ▶ greet-b produced "Welcome, dr.Dobermann!" (instance started 2026-07-27 …)
  ✓ greet-a-result = "Hello, dr.Dobermann!"
  ✓ greet-b-result = "Welcome, dr.Dobermann!"
✓ data-demo completed: the property fed both branches through their frames; each result reached its per-instance DataObject in scope, read back by name
```

## See also

- Examples: `examples/process-data/`
- Related guides: [Working with data — overview](index.md) · [Reading & writing by path](structural.md) · [Data Objects](data-objects.md)
- Design: [ADR-010 — Process data model](../../design/ADR-010-process-data-model.md) · [ADR-011 — Process data flow](../../design/ADR-011-process-data-flow.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/data`
