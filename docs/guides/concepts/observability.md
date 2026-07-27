---
title: Observability
description: Facts, reporters, observers, and the operator log — the one event type the engine emits and the two ways you watch it.
---

# Observability

Every meaningful thing the engine does — an engine starting, an instance
completing, a fault thrown, a data element changing — surfaces as one
`observability.Fact`. There is exactly **one** event type and **one** producer
behind it; you watch that stream two ways at once: a synchronous **operator log**
for a human tailing output, and an asynchronous **observer** stream your code
subscribes to. This page is the developer reference — the `Fact` shape, the
`Kind`/`Phase` vocabularies, the interfaces you implement, and the runtime
behavior of the fan-out. Backing example:
[`examples/data-change/`](../../../examples/data-change/).

## Taxonomy

| | |
|---|---|
| Package | `github.com/dr-dobermann/gobpm/pkg/observability` |
| The event | `observability.Fact` — identity, `Kind`, `Phase`, timing; never payload values |
| The producer | `observability.Reporter` — echoes to the log **and** fans out to observers (internal) |
| What you implement | `observability.Observer` (`OnFact`); optionally `Logger`, `Tracer`, `MetricsRecorder` |
| How you subscribe | `engine.Observe(o)` / `handle.Observe(o)` → a `*thresher.Subscription` |
| Rationale | [ADR-013 — instance observability](../../design/ADR-013-instance-observability.md) |

The observation seam is a **contract-only** package: the interfaces live in
`pkg/observability`, their default implementations in sibling subpackages
(`noop`, `memmetrics`, `memtrace`) and inside the engine. Core never imports
OpenTelemetry — the real OTel types live only in the `adapters/otel` module.

## The Fact

A `Fact` is the single canonical observable event — a failure or a
major-object lifecycle transition. Every emitter (engine, event hub, dispatcher,
instance loop, `pkg/model` nodes) produces this one shape, so there is no
internal-vs-public mapping:

```go
type Fact struct {
    At       time.Time
    Details  map[string]string
    Kind     Kind
    Phase    Phase
    NodeID   string
    NodeName string
}
```

| Field | Meaning |
|---|---|
| `Kind` | the object class the event belongs to (`KindInstanceState`, `KindFault`, …). |
| `Phase` | the transition within that kind (`Started`, `Completed`, `Value_Updated`, …). |
| `At` | when the engine produced it. |
| `NodeID` / `NodeName` | the emitting node, when the event is node-scoped (empty otherwise). |
| `Details` | kind-specific identifiers, keyed by the `Attr*` vocabulary. |

> A Fact carries identity, phase, and timing **only** — never process payload
> values (the masking rule). To read what changed, you read the *path* from
> `Details[AttrDataPath]`, not the value at it.

Read kind-specific data out of `Details` by the typed `Attr*` keys rather than
raw strings — the same keys name both a Fact's `Details` and its log echo, so the
two streams correlate. The ones you reach for most:

| Key | Details value |
|---|---|
| `AttrInstanceID` | the owning instance (stamped automatically on instance-scoped Facts). |
| `AttrNodeID` / `AttrNodeName` | the node, when not already in the `NodeID`/`NodeName` fields. |
| `AttrDataPath` | the changed path on a `DataChange` Fact (e.g. `receipt.sum`). |
| `AttrError` | the fault message on a `Fault` Fact. |
| `AttrProcessID` / `AttrVersion` | the process and version on a lifecycle Fact. |

The full `Attr*` set — job/worker, correlation, escalation, compensation,
data-store, call-activity keys — is in the package source; run the `go doc`
pointer at the foot of this page.

## Kind — the event classes

`Kind` is an **open** vocabulary: a consumer must tolerate an unknown value,
because kinds and their phases grow additively. It is a named type (not a bare
string) so a wrong literal cannot compile into an event. The classes a host
watches most:

| Kind | Emitted for |
|---|---|
| `KindEngineState` | Thresher lifecycle (`Starting` → `Started` → `Stopping` → `Stopped`). |
| `KindInstanceState` | instance lifecycle (`Created`, `Active`, `Completed`, `Failed`, `Terminated`). |
| `KindNodeProgress` | a track's node execution (`Entered`, `Executing`, `Completed`, `Parked`, `Failed`). |
| `KindFault` | a BPMN error / fault (`Thrown`, `Caught`, `Uncaught`). |
| `KindDataChange` | a committed data-element change — **observer-only** (never logged). |

The complete catalog, by object class:

| Kind | Emitted for |
|---|---|
| `KindEngineState` | Thresher lifecycle. |
| `KindHubState` | EventHub lifecycle. |
| `KindProcessLifecycle` | process registration / version supersession. |
| `KindInstanceState` | instance lifecycle. |
| `KindNodeProgress` | a track's node execution phases. |
| `KindGatewayDecision` | the branch(es) a gateway chose. |
| `KindEventFlow` | event registration / fire / delivery. |
| `KindCorrelation` | conversation-key decisions. |
| `KindJobState` | external-worker job lifecycle. |
| `KindTaskState` | user-task lifecycle. |
| `KindBoundary` | boundary-event arm / fire / disarm. |
| `KindFault` | BPMN error / fault. |
| `KindEscalation` | escalation throw / catch. |
| `KindCompensation` | completion-ledger + compensation runs. |
| `KindRules` | Business Rule Engine decision evaluation. |
| `KindScript` | Script Engine execution. |
| `KindDataChange` | data-element change (observer-only). |
| `KindScope` | nested-scope lifecycle. |
| `KindCall` | call-activity lifecycle. |
| `KindDataObject` | per-instance Data Object read / write (observer-only). |
| `KindDataStore` | engine-global Data Store read / write. |

`Phase` names the transition within a kind and is likewise open, additive, and
per-kind — some phases are reused across kinds (`Completed` covers instance,
node, job, and task; `Failed` covers instance and node). A few phase slots
(`Paused`/`Resumed`, `Incident`) are reserved names for subsystems that have not
landed yet, so a listener sees a stable name when they do.

## The Observer contract

An observer is the one interface a host implements to watch the engine:

```go
type Observer interface {
    OnFact(Fact)
}
```

You implement `OnFact` once and register it; `OnFact` is called from a
per-observer drain goroutine, **never** on the engine's execution path, so it MAY
block without stalling the engine (the engine drops Facts past its buffer
instead), and a panic in it is recovered. Filter on `Kind`, then read the
identifiers you need out of `Details`:

```go
type dataChangePrinter struct{}

func (p *dataChangePrinter) OnFact(f observability.Fact) {
    if f.Kind != observability.KindDataChange {
        return
    }

    fmt.Printf("  ▶ %s %s @%s\n",
        f.Phase, f.Details[observability.AttrDataPath], f.NodeName)
}
```

`thresher.Observer` is a type alias of `observability.Observer`, so the same
implementation registers on either the engine or an instance handle.

## Subscribe & run

Register the observer on the engine with `Observe`, which returns a
`*thresher.Subscription` you cancel when done:

```go
engine, _ := thresher.New("data-change-engine",
    thresher.WithoutBanner(), thresher.WithoutStartupConfig())

sub := engine.Observe(&dataChangePrinter{})
defer sub.Cancel()
```

| Registration | Scope |
|---|---|
| `engine.Observe(o)` | the engine-wide stream — every engine-kind Fact **and** every running instance's Facts (each stamped with `instance_id`). |
| `handle.Observe(o)` | just one instance's Facts, via its `*InstanceHandle`. |

The `*Subscription` you get back:

| Method | Role |
|---|---|
| `Cancel()` | stop observing; drains the buffered Facts before returning (idempotent). |
| `Dropped() uint64` | how many Facts were shed because the observer fell behind its buffer. |

Running `examples/data-change/` — two service tasks each commit `receipt`; the
observer prints only the `DataChange` Facts while the engine's log echoes the
lifecycle Facts (banner and config dump suppressed):

```
2026/07/27 09:14:44 INFO EngineState Starting
2026/07/27 09:14:44 INFO HubState Started
2026/07/27 09:14:44 INFO EngineState Started
2026/07/27 09:14:44 INFO ProcessLifecycle Registered process_id=… version=1
2026/07/27 09:14:44 INFO InstanceState Created instance_id=…
2026/07/27 09:14:44 INFO InstanceState Active instance_id=…
  produce → commit receipt={sum:5}
  reprice → commit receipt={sum:6}
  ▶ Value_Added receipt @produce
  ▶ Value_Updated receipt.sum @reprice
2026/07/27 09:14:44 INFO InstanceState Completed instance_id=…
  ✓ completed (Completed)
```

The `INFO …` lines are the operator log; the `▶` lines are your observer. The
first commit is one `Value_Added` at the `receipt` root (a new subtree is one
change, not one per leaf); the re-commit is one `Value_Updated` at the changed
leaf `receipt.sum`.

> The example calls `sub.Cancel()` explicitly after `WaitCompletion` so the
> buffered `DataChange` lines drain before the final status prints — otherwise
> the async fan-out may trail the synchronous log.

## Runtime behavior

- **Two sinks, one Fact.** Behind every Fact is a single `Reporter` (internal):
  its `Report` writes the operator-log echo **and** fans the Fact out to the
  registered observers. `Report` is non-blocking for the caller because it runs
  on the execution hot path — the log echo is a plain synchronous logger call,
  and each observer's delivery is buffered and drained by its own goroutine.
- **The engine is visible before you subscribe.** The default sink is an
  echo-only reporter (`observability.NewEchoReporter`), never a silent no-op, so
  lifecycle Facts already reach the log the moment the engine runs — you add an
  observer only for what the log deliberately omits.
- **Lossy by design.** If an observer is slower than its buffer, the excess is
  **dropped** (count via `Subscription.Dropped()`), never queued without bound.
  Dropping protects the engine from a slow consumer.
- **Log level is derived, not set per call.** The echo level is a pure function
  of the Fact's kind and phase — lifecycle milestones at `Info`, flow tracing at
  `Debug`, a retries-exhausted job at `Warn`, a failed instance or uncaught
  fault at `Error`. A producer never picks a level.
- **Some kinds are observer-only.** `KindDataChange` (and `KindDataObject`)
  never reach the operator log — their high per-node write volume would drown
  flow tracing even at `Debug` (the flood guard). An observer is the *only* way
  to see them, which is why the `data-change` example needs one.

Why this shape — one Fact type, one producer, the masking rule, the drop policy
— is argued in the design record; this page describes the behavior, not the
rationale. See [ADR-013](../../design/ADR-013-instance-observability.md).

## Retargeting the log & telemetry

The operator log and the OTel-shaped telemetry seams are set as engine options
at construction. Each rejects a `nil` argument — a `nil` logger never silently
silences the engine:

| Option | Effect |
|---|---|
| `thresher.WithLogger(l)` | swap the log sink (default `slog.Default()`); any `observability.Logger` — the leveled subset of `*slog.Logger` — works. Raise the threshold to `Info` to mute `Debug` flow tracing. |
| `thresher.WithTracer(t)` | set the tracer (default: no-op). |
| `thresher.WithMetricsRecorder(m)` | set the metrics recorder (default: in-memory registry). |

Two optional **visibility capabilities** let an `AuthorizationProvider` govern
what the streams show; each is asserted once (not per event) and is pass-through
when absent:

| Interface | Governs |
|---|---|
| `observability.LogRedactor` (`RedactLog`) | transform or suppress the **log** echo of a Fact (`ok=false` drops the log record). |
| `observability.ObservationFilter` (`FilterObservation`) | per-recipient visibility on the **observer** stream (`ok=false` denies delivery to that observer — a policy denial, distinct from a counted buffer drop). |

Building your own reporter, logger, tracer, or metrics recorder is covered on the
extension page: [Custom observability](../extending/observability.md).

## See also

- Example: [`examples/data-change/`](../../../examples/data-change/)
- Related guides: [The engine (Thresher)](engine.md) · [Scope & the data plane](scope-and-data.md) · [Observability in practice](../operating/observability.md)
- Extend it: [Custom observability](../extending/observability.md)
- Design: [ADR-013 — instance observability](../../design/ADR-013-instance-observability.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/observability`
