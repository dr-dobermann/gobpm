---
title: Custom rule engine
description: Plug in a decision engine.
---

# Custom rule engine

A **Business Rule Task** delegates a named decision to a *rule engine* — the
engine that resolves a `decisionRef` and evaluates it against the process-data
read surface, returning result rows the task commits. gobpm ships two engines
(the in-core `gorules` registry and the `dtable` Decision Table adapter), and
lets you swap in your own: a DMN service, a remote rules API, anything that
answers the same two questions — *what kind am I* and *what does this decision
decide*. This page is the extension reference: the seam interface, how you
register your engine, a minimal implementation, and how the engine drives it.

## The seam

| | |
|---|---|
| Package | `github.com/dr-dobermann/gobpm/pkg/rules` |
| Interface | `rules.Engine` |
| Registration | `thresher.WithRuleEngine(e rules.Engine)` |
| Consumed by | the `activities.BusinessRuleTask` at execution |
| Default | the in-core `gorules.Registry` |
| Reference impls | [`pkg/rules/gorules`](../../../pkg/rules/gorules) (registry) · [`adapters/dtable`](../../../adapters/dtable) (Decision Table) |

The rule engine is *infrastructure*, not a spec-modeled artifact — BPMN leaves
the rule-engine binding open — so the seam lives beside the other engine
services, not under `pkg/model`.

## The Engine contract

Implement `rules.Engine`:

```go
type Engine interface {
    // Type names the engine kind in the standard's "##"-hint convention
    // ("##GoRules", "##DMN", ...) — reported as the task's implementation
    // attribute and in the startup-config printout.
    Type() string

    // Evaluate resolves decisionRef and evaluates it against the read-only
    // process-data surface, returning the decision result rows (nil or
    // empty when the decision produces no committable result). An unknown
    // decisionRef is an error, never a silent no-op.
    Evaluate(
        ctx context.Context,
        decisionRef string,
        r service.DataReader,
    ) ([]Row, error)
}
```

| Member | You implement it to… |
|---|---|
| `Type() string` | name the engine kind — a `##`-hint string (`"##GoRules"`, `"##DMN"`), surfaced as the task's implementation attribute. |
| `Evaluate(ctx, decisionRef, r) ([]Row, error)` | resolve `decisionRef`, run it against the read-only data surface `r`, and return the result rows. Unknown ref → error, never silent. |

A `Row` is the DMN-universal result element — one output record:

```go
type Row map[string]data.Value
```

Single-hit policies yield one `Row`; multi-hit policies (Rule Order, Collect)
yield many. Each key becomes a committed process datum.

### Optional capabilities

An engine may also implement either of these — the engine detects them by type
assertion, so a plain `Engine` simply opts out:

| Interface | Method | Purpose |
|---|---|---|
| `rules.Deployer` | `Deploy(ctx, definition []byte) error` | ingest an external decision artifact (a DMN file, a JSON table) and cache its executable form. The task never deploys — deployment is the embedder's platform operation. |
| `rules.ReporterBinder` | `BindReporter(sink observability.Reporter)` | receive the observable-event sink at startup so registrar surfaces can emit `KindRules` audit facts. |

For registry-style engines, `rules.DecisionFunc` is the in-process decision
body shape — reads through the data reader, returns one `Row`:

```go
type DecisionFunc func(ctx context.Context, r service.DataReader) (Row, error)
```

## Registration

Pass your engine to the `Thresher` constructor:

```go
func WithRuleEngine(e rules.Engine) Option
```

```go
engine, err := thresher.New("orders",
    thresher.WithRuleEngine(myRuleEngine))
```

The default when the option is omitted is the in-core `gorules` decision
registry. The process model is untouched by the swap — the same
`activities.NewBusinessRuleTask("classify", "discount")` runs on whichever
engine is wired.

## A minimal implementation

A whole rule engine backed by a map of Go closures — this is essentially what
the in-core `gorules.Registry` is:

```go
type MapEngine struct {
    decisions map[string]rules.DecisionFunc
}

func (e *MapEngine) Type() string { return "##Map" }

func (e *MapEngine) Evaluate(
    ctx context.Context,
    decisionRef string,
    r service.DataReader,
) ([]rules.Row, error) {
    fn, ok := e.decisions[decisionRef]
    if !ok {
        return nil, fmt.Errorf("unknown decision %q", decisionRef)
    }

    row, err := fn(ctx, r)
    if err != nil {
        return nil, err
    }
    if row == nil {
        return nil, nil // decided, nothing to commit
    }

    return []rules.Row{row}, nil
}
```

> Validate `decisionRef` at the boundary — an unknown reference is an error, not
> a silent empty result. Every public method that takes an engine, reader, or
> reference must reject bad input with a self-identifying message.

## The reference implementations

You rarely need to write your own — reach for a shipped engine first.

| Engine | When to reach for it |
|---|---|
| [`gorules.Registry`](../../../pkg/rules/gorules) | decisions are Go code — a bounded registry of named `DecisionFunc`s, registered explicitly, evaluated by name. The default. |
| [`dtable.Engine`](../../../adapters/dtable) | decisions are a **data table** — a DMN-shaped hit policy over an ordered rule list, deployed from a JSON artifact, with Go functors as the conditions and yields. |

The `dtable` adapter is the fuller reference: it implements `Engine`,
`Deployer`, and `ReporterBinder`, decodes an external artifact through a
`Decoder` seam, and keeps *behavior* in compiled Go while the *table* stays
data. Build one with a vocabulary of named conditions and yields, then deploy
the artifact:

```go
vocab := dtable.NewVocabulary()
vocab.MustAddCondition("vip", dtable.Eq("tier", "vip"))
vocab.MustAddCondition("big-order", dtable.GT("total", 100))
vocab.MustAddYield("default-discount",
    func(context.Context, service.DataReader) (rules.Row, error) {
        return rules.Row{"discount_pct": values.NewVariable(float64(5))}, nil
    })

dec, _ := dtable.NewJSONDecoder(vocab)
ruleEngine, _ := dtable.New(dtable.WithDecoder(dec))
_ = ruleEngine.Deploy(context.Background(), tableJSON)
```

The deployed artifact (`examples/decision-table/table.json`) carries structure
only — the rule grid, a hit policy, and the *names* of the Go behavior:

```json
{
  "name": "discount",
  "hitPolicy": "FIRST",
  "rules": [
    {"when": ["vip", "big-order"], "then": {"discount_pct": 25}},
    {"when": ["big-order"], "then": {"discount_pct": 15}},
    {"when": [], "thenFn": "default-discount"}
  ]
}
```

## How the engine uses it

At execution, a `BusinessRuleTask` — built with `NewBusinessRuleTask(name,
decisionRef, …)` — calls the wired engine's `Evaluate(ctx, decisionRef,
reader)`, where `reader` is the same `service.DataReader` walk-up every
in-process functor receives. The returned `Row`s become committed process data,
readable by downstream nodes.

Running `examples/decision-table/` — three orders routed through the deployed
FIRST-policy table (vip+big → 25%, big → 15%, the match-always fallthrough →
5%):

```
order: tier=vip total=500
  [report] discount=25%

order: tier=retail total=150
  [report] discount=15%

order: tier=retail total=40
  [decision] fallthrough rule -> retail rate
  [report] discount=5%

✓ decision-table completed: three orders classified by the deployed FIRST-policy table (vip+big 25%, big 15%, default 5%)
```

Swapping engines never touches the model: the same
`NewBusinessRuleTask("classify", "discount")` runs on `gorules`, on `dtable`,
or on a future DMN adapter — only the `WithRuleEngine` wiring changes.

## See also

- Examples: `examples/decision-table/`
- Related guides: [Business Rule Task](../tasks/business-rule-task.md) · [Custom script engine](script-engine.md) · [Custom expression engine](expression-engine.md) · [Custom Operation](operation.md)
- Design: [ADR-027 — The Business Rule Task and the pluggable rule-engine seam](../../design/ADR-027-business-rule-task-and-rule-engine-seam.md) · [ADR-029 — The Decision Table engine: a pluggable adapter with functor rules](../../design/ADR-029-decision-table-engine-adapter.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/rules`
</content>
</invoke>
