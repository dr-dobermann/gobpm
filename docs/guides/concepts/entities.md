---
title: The entity stack
description: From BaseElement to Thresher — the type hierarchy a process is built from, and the public interfaces that hold it together.
---

# The entity stack

Every gobpm process is a small tower of types. At the bottom is
`foundation.BaseElement` — an id and its documentation. Above it, `flow.Element`
adds a name and a container; `flow.Node` adds sequence-flow wiring; the activity,
event, and gateway families specialise `Node` into the shapes you actually drop
on a diagram. A `process.Process` holds those nodes; the engine turns the
`Process` into an immutable launch template and runs live instances off it, all
driven by the `Thresher`. This page walks that stack from bottom to top and names
the public contract at each floor — so you know what you get for free (ids, docs,
name, wiring) and what a given interface obliges you to provide.

> This is a concept page. It grounds the **public** interfaces in `pkg/model/…`
> and describes the runtime layers (`Snapshot`, instance, track, token) as
> observable behavior — those live in `internal/…` and are not an API you call.
> The *why* behind each layer lives in [`docs/design/`](../../design/index.md).

## The stack at a glance

| Floor | Type / interface | Package | Adds |
|---|---|---|---|
| 0 | `BaseElement` / `Identifyer`, `Documentator`, `BaseObject` | `foundation` | id, documentation |
| 1 | `flow.Element` | `flow` | name, container membership, element type |
| 2 | `flow.Node` (`BaseNode`) | `flow` | incoming/outgoing flows, node type, `Clone` |
| 3 | `ActivityNode` · `EventNode` · `GatewayNode` | `flow` | the three connectable families |
| 4 | `process.Process` (a `flow.Container`) | `process` | holds nodes, flows, properties, data objects |
| 5 | snapshot → instance → track → token | `internal/…` | the running form (behavior, not an API) |
| 6 | `thresher.Thresher` | `thresher` | registers, versions, starts, drives |

Floors 0–4 are the **definition** you build; floors 5–6 are the **runtime** the
engine builds from it.

## Floor 0 — `BaseElement`: id and docs

`foundation.BaseElement` is the abstract super-class almost every model type
embeds. It carries two attributes — the id and the documentation slice — and
exposes them through two tiny interfaces:

```go
type Identifyer interface {
    ID() string
}

type Documentator interface {
    Docs() []*Documentation
}

type BaseObject interface {
    Identifyer
    Documentator
}
```

`BaseElement` implements both via `ID()` and `Docs()`. You rarely construct one
directly, but when you do it takes options and never a positional id:

```go
func NewBaseElement(opts ...options.Option) (*BaseElement, error)
func MustBaseElement(opts ...options.Option) *BaseElement
```

| Option | Effect |
|---|---|
| `foundation.WithID(id string)` | pin the element's id instead of generating one. |
| `foundation.WithDoc(text, format string)` | attach a `Documentation` entry (`format` e.g. `foundation.PlainText`). |

If you omit `WithID`, the element gets one from the package generator —
`foundation.GenerateID()`, backed by the pluggable `foundation.IDGenerator`
(swap it with `foundation.SetGenerator`; see
[Custom ID generator](../extending/id-generator.md)). So **id is never your
responsibility** unless you want it to be.

## Floor 1 — `flow.Element`: name and container

A `flow.Element` is anything that can appear in a process flow. It builds on
`BaseObject` and adds a name plus a home:

```go
type Element interface {
    foundation.BaseObject          // ID(), Docs()
    foundation.Namer               // Name() string

    Container() Container          // which container holds it
    EType() ElementType            // the element's flow-type
    BindTo(Container) error
    Unbind() error
}
```

The name comes from `foundation.Namer` (`Name() string`), so from `Element`
upward every type answers `ID()`, `Docs()`, and `Name()`. `EType()` returns a
`flow.ElementType` — a named string type with a `Validate()` method, not a bare
string.

## Floor 2 — `flow.Node`: the connectable thing

Only four BPMN element kinds connect to sequence flows — activities, events,
gateways, and (in choreographies) choreography activities. Those are the `Node`s.
`flow.Node` extends `Element` with flow wiring, a node type, and per-instance
cloning:

```go
type Node interface {
    Element

    Incoming() []*SequenceFlow
    Outgoing() []*SequenceFlow
    AddFlow(*SequenceFlow, data.Direction) error

    NodeType() NodeType

    Node() Node                    // the underlying node object
    Clone() (Node, error)          // a per-instance copy
}
```

`flow.BaseNode` embeds `BaseElement` and provides all of this, so a concrete node
type embeds `BaseNode` and inherits `Incoming`/`Outgoing`/`AddFlow`/`Clone` for
free. Two behaviors are worth internalising:

- **Flows are declaration-ordered.** `BaseNode` holds incoming and outgoing flows
  per direction in the order they were added; `Incoming()`/`Outgoing()` return
  that order. Gateway routing depends on it — Exclusive picks the first true
  branch, Inclusive a subset — so the order in which you `Link` matters.
- **`Clone` can fail.** Each running instance gets its own copy of the node graph
  (immutable config shared by reference, runtime state fresh, flow collections
  empty and rewired afterwards). `Clone` returns an error because copying a node's
  properties can fail — a value-less property is unclonable; a node without
  properties never errors. This per-instance-graph model is
  [ADR-009](../../design/ADR-009-per-instance-node-graph.md).

## Floor 3 — the three families

`Node` splits into exactly the three connectable families, each adding one
introspection method:

```go
type ActivityNode interface {
    Node
    ActivityType() ActivityType
}

type EventNode interface {
    Node
    Definitions() []EventDefinition
    EventClass() EventClass         // Start | Intermediate | End
}

type GatewayNode interface {
    Node
    GatewayType() GatewayType
}
```

| Family | Marker method | Concrete package | Reference |
|---|---|---|---|
| Activity | `ActivityType() ActivityType` | `pkg/model/activities` | [Activities taxonomy](../tasks/index.md) |
| Event | `EventClass()` + `Definitions()` | `pkg/model/events` | [Events taxonomy](../events/index.md) |
| Gateway | `GatewayType() GatewayType` | `pkg/model/gateways` | [Gateways taxonomy](../gateways/index.md) |

You almost never implement these interfaces yourself — the concrete Service Task,
Exclusive Gateway, Timer Event, etc. already do. They exist so the engine and
your introspection code can treat any node uniformly and still ask "what kind of
activity/event/gateway is this?".

Nodes are joined by **sequence flows**. `flow.SequenceFlow` is itself a
`BaseElement`; you create one with `flow.Link`:

```go
func Link(
    src SequenceSource,
    trg SequenceTarget,
    flowOptions ...options.Option,
) (*SequenceFlow, error)
```

`Link` also adds the new flow to the source node's container automatically. Its
options are `foundation.WithID`, `foundation.WithDoc`, `options.WithName`, and
`flow.WithCondition` (a conditional flow). See
[Sequence flows & associations](../foundation/flows.md).

## Floor 4 — `Process`: the container

A `process.Process` is the top of the *definition*. It embeds
`foundation.BaseElement` (so it too has an id and docs) and is a `flow.Container`
— the thing nodes and flows live in:

```go
func New(name string, procOpts ...options.Option) (*Process, error)
```

The one option family a `Process` takes is `data.WithProperties(...)` — its
process-level [properties](../data/item-definitions.md). Its public surface is a
container plus a few catalogs:

| Method | Returns |
|---|---|
| `Add(e flow.Element) error` / `Remove(e flow.Element) error` | container mutation |
| `Elements() []flow.Element` | everything in the process |
| `Nodes(types ...flow.NodeType) []flow.Node` | nodes, optionally filtered by type |
| `Flows() []*flow.SequenceFlow` | the sequence flows |
| `Properties() []*data.Property` | process-level data |
| `DataObjects()` / `DataStoreReferences()` | scoped data containers |
| `Validate() error` | structural check before hand-off to the engine |

A `Process` is inert — a validated graph of typed nodes. It carries no runtime
state; running it is the engine's job. See
[Process, instance, track, token](execution-model.md).

## Floors 5–6 — the runtime (behavior, not an API)

When you hand a `Process` to the engine, four runtime shapes appear. They live in
`internal/…` — you observe them, you do not construct them:

- **Snapshot** — the `Process` is converted **once** into an immutable launch
  template: a validated, ready-to-clone node graph. It is *not* a persistence or
  recovery mechanism — the durable record is the Repository's checkpoint
  document, not the snapshot ([Persistence & recovery](../operating/persistence.md)).
- **Instance** — each start `Clone`s the snapshot's node graph into a private,
  mutable copy. The immutable header (process id/name, properties, correlation
  keys) is shared by reference across instances; only the per-instance graph is
  copied. You interact with a running instance through a
  `thresher.InstanceHandle`, never the internal struct.
- **Track** — a running instance advances along one or more concurrent tracks (a
  token's path through the graph). Tracks are how parallel branches and boundary
  interruptions are expressed.
- **Token** — the unit of control-flow position. Splits and joins at gateways
  move tokens; a track carries them.

The layering — model → snapshot → instance → engine — and the single-writer
execution model are described in
[Architecture overview](architecture.md) and
[How a process executes](process-execution.md); the rationale is
[ADR-001](../../design/ADR-001-execution-model.md).

At the top sits the **`Thresher`** — the engine. It is the only floor with a rich
public method set (register, version, start, observe, drive):

```go
func New(id string, opts ...Option) (*Thresher, error)
```

| Method | Role |
|---|---|
| `RegisterProcess(p *process.Process, opts ...RegisterOption)` | register a definition (versioned). |
| `StartLatest(key)` / `StartVersion(key, v)` | start an instance, returning an `*InstanceHandle`. |
| `Run(ctx)` / `Shutdown(ctx)` | engine lifecycle. |
| `Instance(id)` / `Instances(query)` | discover running instances. |
| `Observe(o Observer)` | subscribe to runtime facts. |

The full engine surface — every `With*` option and lifecycle method — is its own
page: [The engine (Thresher)](engine.md) and the
[Engine options catalog](../reference/engine-options.md).

## Reading the stack in code

Because each floor embeds the one below, a concrete node satisfies every
interface down the tower at once. A single value can be asked its id, docs, name,
node type, and family:

```go
var n flow.Node = someServiceTask // *activities.ServiceTask

_ = n.ID()          // foundation.Identifyer
_ = n.Name()        // foundation.Namer
_ = n.Docs()        // foundation.Documentator
_ = n.NodeType()    // flow.Node
_ = n.Outgoing()    // flow.Node — declaration-ordered

if a, ok := n.(flow.ActivityNode); ok {
    _ = a.ActivityType()   // it's an activity — which kind?
}
```

That embedding is the whole point of the stack: you build with concrete types,
but the engine — and your own introspection — works against the small interfaces
each floor contributes.

## See also

- Related guides: [Architecture overview](architecture.md) · [Process, instance, track, token](execution-model.md) · [How a process executes](process-execution.md) · [Foundation elements](../foundation/index.md) · [The engine (Thresher)](engine.md)
- Design: [ADR-009 — per-instance node graph](../../design/ADR-009-per-instance-node-graph.md) · [ADR-001 — execution model](../../design/ADR-001-execution-model.md) · [SAD-001 — vision & architecture](../../design/SAD-001-vision-and-architecture.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/foundation` · `go doc github.com/dr-dobermann/gobpm/pkg/model/flow` · `go doc github.com/dr-dobermann/gobpm/pkg/model/process`
