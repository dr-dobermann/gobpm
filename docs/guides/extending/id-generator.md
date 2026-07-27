---
title: Custom ID generator
description: Replace how element ids are generated.
---

# Custom ID generator

Every BPMN element gets an id when it is constructed — a process, a task, an
event, a sequence flow. gobpm derives that id from a single package-level
generator in `foundation`. Reach for a custom generator when the engine's
default random ids don't fit: you want UUIDs, a monotonic counter, ids scoped to
a tenant, or deterministic ids in a test so snapshots and logs are reproducible.

This page shows the seam interface, how to install your own generator, a minimal
real implementation, and how the engine uses it.

## Where the id comes from

When you construct any element, its embedded `BaseElement` calls the package
function `GenerateID` unless you pin the id explicitly with the `WithID` option.
`GenerateID` reads whichever generator is currently installed:

```go
func GenerateID() string   // returns a new id from the configured generator
```

The default generator draws from `math/rand` — goroutine-safe, auto-seeded, and
adequate for in-memory runs, but **not** unique across processes and **not**
unpredictable. If either property matters, replace it.

> `WithID("my-id")` on a constructor overrides the generator for that one
> element. The generator only supplies ids for elements you *didn't* name.

## The seam interface

A generator is anything with a `Generate` method:

```go
type IDGenerator interface {
    Generate() string
}
```

That's the whole contract. `Generate` must return a fresh id per call and must
be safe to call from multiple goroutines — the engine constructs elements from
concurrent frames (per-execution instantiation, concurrent instance startup), so
the generator is read under a shared lock and called on many goroutines at once.

For the common case of "I already have a plain func", `foundation` ships an
adapter so you don't need a named type:

```go
type GenFunc func() string          // adapts a plain func to IDGenerator

func (gf GenFunc) Generate() string  // calls the wrapped func
```

## Installing your generator

Swap the package generator with `SetGenerator`:

```go
func SetGenerator(newGen IDGenerator) error
```

| Aspect | Behavior |
|---|---|
| Scope | package-global — affects every element constructed afterward, engine-wide. |
| Nil guard | a `nil` generator is rejected with an `InvalidParameter` error; the default stays in place. |
| Concurrency | safe to call while `GenerateID` runs; in-flight generations finish on the previous generator. |
| Timing | call it **once at startup**, before you build processes — ids already assigned are not rewritten. |

Because the generator is package-global, set it during program initialization,
before constructing any process or engine.

## A minimal implementation

A monotonic-counter generator — deterministic ids for reproducible tests and
logs, wrapped as a `GenFunc` so there's no type to declare:

```go
var seq atomic.Int64

_ = foundation.SetGenerator(foundation.GenFunc(func() string {
    return "el-" + strconv.FormatInt(seq.Add(1), 10)
}))
// elements built after this get ids el-1, el-2, el-3, …
```

A UUID generator is the same shape — return `uuid.NewString()` from the closure,
or implement `IDGenerator` on a named type if you prefer. The `atomic.Int64`
here is what keeps `Generate` goroutine-safe; any implementation you write must
provide that safety itself.

## Reference implementation

The built-in default (`foundation`'s unexported `defaultIDGenerator`) is the
reference: a stateless struct whose `Generate` returns
`strconv.Itoa(int(rand.Int63()))`. It leans on `math/rand`'s goroutine-safe
top-level source, so the generator itself carries no state and needs no locking —
a good model for your own: keep per-call state minimal, make concurrency safety
explicit.

## How the engine uses it

There is no per-engine or per-process wiring — the generator is a single
process-wide setting:

1. You call `SetGenerator(yourGen)` once at startup.
2. Every subsequent `NewBaseElement` (embedded in every element constructor)
   calls `GenerateID`, which reads your generator under a read-lock and returns
   its `Generate()` value as the element's id.
3. Elements you named with `WithID` skip the generator entirely.

So one call re-points id generation for the whole program. Prefer it over
post-hoc id rewriting: pinning ids up front (via generator or `WithID`) keeps
element identity stable from construction through snapshot and execution.

## See also

- Foundation elements: [Foundation elements](../foundation/index.md) — `BaseElement`, `Identifyer`, `WithID`.
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/foundation`
