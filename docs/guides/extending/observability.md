---
title: Custom observability
description: Plug in a logger, tracer, metrics recorder, or observer.
---

# Custom observability

gobpm has five observability seams, and they split into two groups. **Sinks**
you configure at engine construction — a `Logger` for the structured operator
log, a `Tracer` for spans, a `MetricsRecorder` for instruments — each swapped
with a `thresher.With*` option and each carrying a sensible default. **Observers**
you register at runtime — an `Observer` receives the engine's `Fact` stream, the
single event type every emitter produces. The `Reporter` is the internal producer
behind both halves; a host never constructs it. This page shows each interface,
how you plug your implementation in, a minimal real observer, and when to reach
for which.

> The engine is **visible by default** (ADR-022 §2.6): the log sink is a real
> echo reporter over `slog.Default()`, never a silent no-op. You opt *out* of
> noise, not into it.

## The seams at a glance

| Seam | Interface | Plug-in point | Default |
|---|---|---|---|
| Structured log | `observability.Logger` | `thresher.WithLogger(l)` | `slog.Default()` |
| Tracing | `observability.Tracer` | `thresher.WithTracer(t)` | no-op (`noop.NewTracer`) |
| Metrics | `observability.MetricsRecorder` | `thresher.WithMetricsRecorder(m)` | in-memory (`memmetrics`) |
| Fact stream (host watches) | `observability.Observer` | `(*Thresher).Observe(o)` / `(*InstanceHandle).Observe(o)` | none — opt-in |
| Fact producer (internal) | `observability.Reporter` | not host-constructed | `NewEchoReporter(log)` |

Most hosts touch only two: pass a configured `Logger`, and register an
`Observer`. Tracer and metrics recorders matter when you wire gobpm into an
existing OpenTelemetry stack.

## Logger — the structured operator log

`Logger` is intentionally the leveled subset of `*slog.Logger`, so a standard
`*slog.Logger` satisfies it directly:

```go
type Logger interface {
    Debug(msg string, args ...any)
    Info(msg string, args ...any)
    Warn(msg string, args ...any)
    Error(msg string, args ...any)
}
```

Plug one in — any `*slog.Logger` works as-is:

```go
h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn})
engine, _ := thresher.New("engine", thresher.WithLogger(slog.New(h)))
```

`WithLogger(nil)` is rejected — it does not silently erase the default. To
quiet the log, pass a logger whose handler drops records (a high level, or
`io.Discard`), rather than a nil sink.

## Tracer — spans

`Tracer` is modeled on OpenTelemetry's tracer; it starts a span and returns a
context carrying it plus the `Span` to end:

```go
type Tracer interface {
    Start(ctx context.Context, name string, attrs ...Attr) (context.Context, Span)
}
```

The default is the no-op tracer (`noop.NewTracer`). For a development ring of
recent spans, use `memtrace.New(capacity)`; for production, the
`adapters/otel` module bridges to real OpenTelemetry — core never imports OTel
directly (ADR-002 §4.2).

```go
engine, _ := thresher.New("engine", thresher.WithTracer(memtrace.New(128)))
```

## MetricsRecorder — instruments

`MetricsRecorder` creates instruments by name, modeled on OpenTelemetry's
meter:

```go
type MetricsRecorder interface {
    Counter(name string) Counter
    Histogram(name string) Histogram
    Gauge(name string) Gauge
}
```

The default is the in-memory, series-capped registry in `memmetrics`, readable
via `Snapshot` for tests and diagnostics. Swap to `noop.NewMetricsRecorder()`
to silence, or to `adapters/otel` for production:

```go
engine, _ := thresher.New("engine",
    thresher.WithMetricsRecorder(noop.NewMetricsRecorder()))
```

> `memmetrics` and `memtrace` are the **reference implementations** — read
> their source before writing your own recorder or tracer.

## The Observer contract

An `Observer` is the one interface a host implements to *watch* the engine.
Everything the engine emits — engine state, node progress, faults, data changes
— arrives as a `Fact`:

```go
type Observer interface {
    OnFact(Fact)
}
```

`OnFact` is called from a per-observer drain goroutine, never on the engine's
execution path. It **may block** without stalling the engine (the engine drops
Facts past its buffer instead), and a panic in it is recovered.

A `Fact` carries identity, phase, and timing only — never process payload
values (the masking rule, ADR-010/011). Kind-specific identifiers live in
`Details`, keyed by the `Attr*` vocabulary:

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

`Kind` classifies the event by object class; `Phase` names the transition
within a `Kind`. Both are **open, additive** vocabularies — an observer must
tolerate unknown values. Some of the kinds you filter on:

| `Kind` | Emitted for |
|---|---|
| `KindEngineState` / `KindHubState` | engine and event-hub lifecycle |
| `KindInstanceState` | instance Created / Active / Completed |
| `KindNodeProgress` | node execution progress |
| `KindGatewayDecision` | branches a gateway chose |
| `KindFault` | a failure |
| `KindDataChange` | a committed value diff (observer-only — never echoed to the log) |

For the exhaustive list run `go doc github.com/dr-dobermann/gobpm/pkg/observability`.

## Registering an observer

Register on either scope — the engine (every engine-kind event plus every
running instance's events) or a single instance handle. Both return a
`Subscription`; cancel it to stop.

| Call | Scope |
|---|---|
| `(*Thresher).Observe(o)` | engine-wide — all engine events + every instance's Facts (each carrying `instance_id`) |
| `(*InstanceHandle).Observe(o)` | one instance's Fact stream |

Delivery is **best-effort and lossy**: Facts are buffered per observer and
drained by one goroutine; a slow observer's excess is dropped
(`Subscription.Dropped()`) and the engine never blocks.

## Minimal observer

From `examples/data-change/`, an observer that prints only `DataChange` Facts —
it filters on `Kind`, then reads the changed path out of `Details`:

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

Wire it engine-wide and cancel the subscription when done:

```go
sub := engine.Observe(&dataChangePrinter{})
defer sub.Cancel()
```

Running `examples/data-change/` — the `slog` default log echoes lifecycle
Facts, and the observer surfaces the two data-change Facts the log deliberately
withholds:

```
2026/07/27 11:29:33 INFO InstanceState Active instance_id=1056571436249029651
  produce → commit receipt={sum:5}
  ▶ Value_Added receipt @produce
  reprice → commit receipt={sum:6}
  ▶ Value_Updated receipt.sum @reprice
2026/07/27 11:29:33 INFO InstanceState Completed instance_id=1056571436249029651
  ✓ completed (Completed)
```

## How the engine uses these

The engine's single `Reporter` sits behind every observable event: its
`Report` writes the operator-log echo **and** fans the Fact out to registered
observers, on the execution hot path, always non-blocking. You never construct
it — the default is `NewEchoReporter(log)`, wired from whatever `Logger` you
passed. `Echo` composes the two log responsibilities (whether to log, and at
what level); a non-loggable kind like `DataChange` or a nil logger writes
nothing to the log — which is exactly why the observer above is the only way to
*see* a data change.

Two optional visibility capabilities hang off an `AuthorizationProvider` (not
the observer): `LogRedactor` (`RedactLog`) transforms or suppresses the log
echo, and `ObservationFilter` (`FilterObservation`) gates per-recipient
delivery on the observer stream. Both are asserted once at start/registration —
absent means pass-through (ADR-013 v.2 §2.11).

Reach for each seam by intent:

- **`WithLogger`** — route the operator log into your logging stack, or tune
  its level. The one seam nearly every host sets.
- **`Observe`** — react programmatically to engine events (dashboards, audit,
  test assertions, custom sinks). The `Fact` stream, not the log, is the
  machine-readable feed.
- **`WithTracer` / `WithMetricsRecorder`** — only when integrating spans or
  metrics into an existing OTel pipeline; the in-memory defaults cover local
  diagnostics.

## See also

- Concept: [Observability](../concepts/observability.md) — facts, reporters, the operator log.
- In practice: [Observability in practice](../operating/observability.md) — subscribing, filtering, tuning the level.
- Extending: [Custom authorization](authorization.md) — where `LogRedactor` / `ObservationFilter` live.
- Examples: `examples/data-change/`
- Design: [ADR-013 — instance observability](../../design/ADR-013-instance-observability.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/observability`
