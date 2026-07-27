---
title: Foundation elements
description: BaseElement, Documentation, Identifyer — the attributes every element carries.
---

# Foundation elements

Every BPMN element in gobpm — a task, a gateway, an event, a process — is built
on the same base. The `foundation` package defines that base: the abstract
`BaseElement` that carries an **id** and **documentation**, the small interfaces
that expose them (`Identifyer`, `Documentator`, `Namer`), and the id machinery
(`ID`, the pluggable `IDGenerator`). You rarely construct these directly — you
embed `BaseElement` and pass `WithID` / `WithDoc` through an element's
constructor — but this is the vocabulary the whole entity stack inherits.

## Taxonomy

| | |
|---|---|
| BPMN category | Foundation — `BaseElement`, `Documentation` (§8.4) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/foundation` |
| Core type | `foundation.BaseElement` — the abstract super class most elements embed |
| Provides | an **id** and zero or more **`Documentation`** entries |
| Interfaces | `Identifyer` (`ID`), `Documentator` (`Docs`), `Namer` (`Name`), `BaseObject` (both) |

Where it sits in the stack: `BaseElement` → element (activity / event / gateway)
→ `Process` → instance. See [The entity stack](../concepts/entities.md).

## The base type

`BaseElement` is the abstract super class most BPMN elements embed. It gives them
an id and their documentation, with two exported methods:

```go
type BaseElement struct {
    // unexported: id + docs
}

func (be *BaseElement) ID() string
func (be *BaseElement) Docs() []*Documentation
```

| Method | Returns |
|---|---|
| `ID() string` | the element's identifier (an explicit `WithID`, or a generated UUID). |
| `Docs() []*Documentation` | every documentation entry attached to the element (empty if none). |

### Constructors

You construct a `BaseElement` only when writing a new element type — most of the
time an element's own constructor embeds one for you and forwards these options.

```go
func NewBaseElement(opts ...options.Option) (*BaseElement, error)
func MustBaseElement(opts ...options.Option)  *BaseElement
```

| Constructor | Contract |
|---|---|
| `NewBaseElement(opts…)` | builds a `BaseElement`; if no id is supplied a new UUID is generated. Returns an error on an invalid option. |
| `MustBaseElement(opts…)` | same, but panics on failure instead of returning an error — for package-level values and tests where a failure is a bug. |

## Options

Two options configure a `BaseElement` (and, forwarded, any element that embeds
one). Both come from the `foundation` package and satisfy `options.Option`:

| Option | Effect |
|---|---|
| `WithID(id string)` | set the element's id explicitly. Omit it and a UUID is generated. |
| `WithDoc(text, format string)` | attach a documentation entry; `format` is a MIME type — pass the `foundation.PlainText` constant for plain text. |

```go
be, err := foundation.NewBaseElement(
    foundation.WithID("order-check"),
    foundation.WithDoc("Validates the incoming order.", foundation.PlainText),
)
```

> `WithDoc` is additive — call it more than once and the element accumulates
> multiple `Documentation` entries, which is exactly the BPMN model: an element
> may carry one *or more* text descriptions.

## Documentation

`WithDoc` produces a `Documentation` value; `Docs()` hands them back. It is
read-only once attached:

```go
type Documentation struct {
    // unexported: text + format
}

func (d Documentation) Text()   string
func (d Documentation) Format() string
```

| Method | Returns |
|---|---|
| `Text()` | the documentation body. |
| `Format()` | its MIME type (e.g. `text/plain`). |

## The identity interfaces

Elements expose their attributes through small single-method interfaces rather
than a fat base class. Accept the narrowest one your code needs.

| Interface | Member | Satisfied by |
|---|---|---|
| `Identifyer` | `ID() string` | `BaseElement`, `ID` — anything with an id. |
| `Documentator` | `Docs() []*Documentation` | `BaseElement`. |
| `Namer` | `Name() string` | named elements (tasks, events, …). |
| `BaseObject` | embeds `Identifyer` + `Documentator` | `BaseElement`. |

```go
type Identifyer interface {
    ID() string
}

type Documentator interface {
    Docs() []*Documentation
}

type Namer interface {
    Name() string
}

type BaseObject interface {
    Identifyer
    Documentator
}
```

> `Name()` lives on `Namer`, not on `BaseElement`. Naming is not universal —
> a sequence flow or an association need not be named — so an element opts into
> `Namer` only when it genuinely has a name.

## Identifiers

An id is a plain `string`. The standalone `ID` type wraps one for elements that
need an identity without the full `BaseElement`, and it too satisfies
`Identifyer`:

```go
func NewID()                *ID   // wraps a freshly generated identifier
func NewIdentifyer(id string) *ID // wraps id, or generates one if id == ""
func (id *ID) ID() string
```

| Constructor | Behavior |
|---|---|
| `NewID()` | wraps a newly generated identifier. |
| `NewIdentifyer(id)` | wraps `id`, or generates one when `id` is empty. |

### The id generator

Generation goes through a package-level, swappable generator — so ids can be
made deterministic in tests, or namespaced to your host system, without
touching element code.

| Symbol | Role |
|---|---|
| `GenerateID() string` | return a new id from the configured generator (a UUID by default). |
| `IDGenerator` (`Generate() string`) | the seam — implement it to control id shape. |
| `GenFunc` | adapts a plain `func() string` to `IDGenerator`. |
| `SetGenerator(g IDGenerator) error` | install a generator process-wide; safe to call concurrently with `GenerateID` (in-flight generations finish on the previous generator). |

```go
type IDGenerator interface {
    Generate() string
}
```

Replacing the generator is an extension seam — see
[Custom ID generator](../extending/id-generator.md).

## See also

- Related guides: [The entity stack](../concepts/entities.md) · [Sequence flows & associations](flows.md) · [Custom ID generator](../extending/id-generator.md)
- Design: [SAD-001 — vision & architecture](../../design/SAD-001-vision-and-architecture.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/foundation`
