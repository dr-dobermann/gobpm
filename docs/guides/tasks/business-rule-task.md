---
title: Business Rule Task
description: Evaluate a decision table.
---

# Business Rule Task

A Business Rule Task hands a named **decision** to the engine's configured
Business Rule Engine, takes back the result, and commits it to process data — so
a downstream condition or task reads the answer with zero ceremony. Reach for it
when a "which rate / which tier / which route" choice belongs in a rules
artifact, not scattered across `if`-branches. This page is the developer
reference — the type, its constructor, the options it accepts, the rule-engine
contract behind it, and its runtime behavior.

The decision reference is **opaque to the task**: the engine wired at Thresher
construction resolves it — a registered name for the in-core `gorules` registry,
a DMN decision id/key for an external engine — so the same model runs under
whichever engine the embedder chose.

## Taxonomy

| | |
|---|---|
| BPMN category | Activity → Task → **Business Rule Task** (§13.3.3) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/activities` |
| Type | `activities.BusinessRuleTask` |
| Inherits | the `Activity` attributes and associations — I/O sets, boundary events, loop characteristics, compensation |
| Implements | `flow.Node`, `exec.NodeExecutor` (`Exec`), `flow.ActivityNode` (`ActivityType`, `AddBoundaryEvent`), `LoadData`/`UploadData` |
| The work | a decision *reference* evaluated on the configured `rules.Engine` |

Where it sits in the activity family: [Activities taxonomy](index.md).

## Constructor

```go
func NewBusinessRuleTask(
    name, decisionRef string,
    opts ...options.Option,
) (*BusinessRuleTask, error)
```

| Parameter | Meaning |
|---|---|
| `name` | the task's diagram name (and default id source). |
| `decisionRef` | the decision to evaluate (e.g. `"discount"`) — resolved by the configured engine, never by the task. |
| `opts` | zero or more foundation/activity options (below). |

Unlike the Service Task, the work is **not** passed to the constructor — the task
binds only the reference, and the engine supplies the logic at run time. It
returns an error, never panics, on an invalid combination.

## Options

The Business Rule Task takes no rule-specific options — most tasks are the bare
constructor. The options it does accept are the shared **activity options** (any
activity carries them):

| Option | When you reach for it |
|---|---|
| `WithParameters(dir, params…)` | declare typed `data.Input` / `data.Output` associations. |
| `WithoutParams()` | declare no parameters (the decision reads process data by name). |
| `WithCompensation()` | mark the task a compensation handler (armed, off the normal flow). |

The full activity-option family:

| Activity option | Effect |
|---|---|
| `WithParameters(dir data.Direction, params ...*data.Parameter)` | declare typed inputs/outputs. |
| `WithoutParams()` | declare no parameters. |
| `WithCompensation()` | mark the task a compensation handler (armed, off the normal flow). |
| `WithLoop(lc)` / `WithMultyInstance()` | repeat the activity — see [Standard Loop](../iteration/standard-loop.md), [Multi-Instance](../iteration/multi-instance.md). |
| `WithStartQuantity(n)` / `WithCompletionQuantity(n)` | BPMN token quantities (default 1). |

> Boundary events are attached with the method `AddBoundaryEvent`, not a
> constructor option — see [Boundary events](../events/boundary.md).

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/activities`.

## The rule-engine contract

The task itself is a thin caller — the pluggable half you configure (or
implement) is the `rules.Engine`, wired with `thresher.WithRuleEngine`. The
default is the in-core `gorules` registry; a DMN or table adapter plugs in behind
the same interface:

```go
type Engine interface {
    // Type names the engine kind ("##GoRules", "##DMN", …).
    Type() string
    // Evaluate resolves decisionRef and evaluates it against the read-only
    // process-data surface, returning the result rows (nil/empty when the
    // decision produces no committable result). An unknown ref is an error.
    Evaluate(
        ctx context.Context,
        decisionRef string,
        r service.DataReader,
    ) ([]Row, error)
}

type Row map[string]data.Value
```

You rarely implement `Engine` directly — the batteries-included `gorules`
registry lets you register a decision as a plain Go function:

```go
type DecisionFunc func(ctx context.Context, r service.DataReader) (Row, error)
```

An engine that ingests external artifacts (a DMN adapter, the table engine) also
implements `rules.Deployer` (`Deploy(ctx, definition []byte) error`); an engine
that emits audit facts implements the optional `rules.ReporterBinder`. See
[Custom rule engine](../extending/rule-engine.md).

## Build it

The task is one line — a name and the decision reference. It carries no inline
logic:

```go
classify, err := activities.NewBusinessRuleTask("classify", "discount")
```

The decision lives on the rule engine. Register it by name on a `gorules`
registry — a function that reads process data and returns one `rules.Row`:

```go
reg := gorules.New()

err := reg.Register("discount",
    func(ctx context.Context, r service.DataReader) (rules.Row, error) {
        d, err := r.GetData("total")
        if err != nil {
            return nil, err
        }
        total, _ := d.Value().Get(ctx).(int)

        pct := 5
        if total > 100 {
            pct = 15
        }
        return rules.Row{"discount_pct": values.NewVariable(pct)}, nil
    })
```

Plug the engine into the Thresher and wire the flows. The outgoing condition
reads the committed `discount_pct` like any other property:

```go
engine, _ := thresher.New("business-rule-task-engine",
    thresher.WithRuleEngine(reg))

flow.Link(start, classify)
flow.Link(classify, big, flow.WithCondition(cond)) // discount_pct > 10
sf, _ := flow.Link(classify, small)
classify.SetDefaultFlow(sf.ID())                   // fallback lane
```

> Call `data.CreateDefaultStates()` once before building data-carrying elements
> — it registers the standard data states the properties and committed result
> rely on.

## Run it

```bash
cd examples/business-rule-task && go run .
```

The decision fires once, commits `discount_pct=15` for the 250 order, and the
conditional flow routes to the big-discount lane:

```
  [decision discount] total=250 -> discount_pct=15
  [apply-big-discount] wholesale rate applied

✓ business-rule-task completed (Completed): the pluggable rule engine
  (##GoRules) evaluated "discount" for a 250 order, the task committed
  discount_pct=15, and the conditional flow routed to apply-big-discount
```

## Methods & runtime behavior

The engine drives the task through these — you rarely call them directly:

| Method | Role |
|---|---|
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | evaluate the decision on the configured engine and commit the result rows; return the outgoing flows. |
| `LoadData` / `UploadData` | bind declared inputs before, commit outputs after. |
| `DecisionRef() string` | the decision reference the task evaluates. |
| `AddBoundaryEvent(be)` / `BoundaryEvents()` | attach / inspect boundary events. |
| `ActivityType()` / `TaskType()` | introspection. |
| `ForCompensation()` | whether the task is a compensation handler. |

Behavior worth knowing:

- **Call, complete, commit.** `Exec` calls the engine with the decision
  reference, then commits the returned rows and completes on return (ADR-027
  §2.3). An **empty result commits nothing** — the task still completes.
- **1-row / 1-output fold.** A decision that yields exactly one row with one
  output commits it as a **scalar** process variable named by that output key —
  no output mapping to declare. The example's `discount_pct` is then readable by
  a `goexpr` condition directly.
- **Data by name.** The runtime environment satisfies `service.DataReader`
  structurally, so the decision reads exactly what an in-process Go operation
  reads — the ordinary process-data walk-up.
- **Fails loud.** An unknown decision reference surfaces a classified error
  through the normal fault machinery, not a silent no-op.

## Swap the engine, keep the model

The same `NewBusinessRuleTask("classify", "discount")` runs on any engine passed
to `thresher.WithRuleEngine(...)`. The `decision-table` example moves from the
in-core registry to a JSON **decision table** (`adapters/dtable`) where
**structure is data** and **behavior stays compiled Go** — conditions and yields
registered by name in a `Vocabulary`:

```go
vocab := dtable.NewVocabulary()
vocab.MustAddCondition("big-order", dtable.GT("total", 100)).
    MustAddYield("wholesale", func(context.Context, service.DataReader) (rules.Row, error) {
        return rules.Row{"discount_pct": values.NewVariable(15)}, nil
    })

dec, _ := dtable.NewJSONDecoder(vocab)
engine, _ := dtable.New(dtable.WithDecoder(dec))
engine.Deploy(context.Background(), tableJSON) // structure only; a redeploy replaces
```

The embedded artifact carries only the rule grid, the hit policy, and the names;
re-ordering rules or moving a threshold is a redeploy, and an unresolved name
fails the deploy loud. See [Custom rule engine](../extending/rule-engine.md) and
[ADR-029](../../design/ADR-029-decision-table-engine-adapter.md).

## See also

- Examples: `examples/business-rule-task/` (registry) · `examples/decision-table/` (table adapter)
- Related guides: [Service Task](service-task.md) · [Script Task](script-task.md) · [Exclusive gateway](../gateways/exclusive.md) · [Expressions](../data/expressions.md) · [Custom rule engine](../extending/rule-engine.md)
- Design: [ADR-027 — Business Rule Task & rule-engine seam](../../design/ADR-027-business-rule-task-and-rule-engine-seam.md) · [ADR-029 — Decision Table engine adapter](../../design/ADR-029-decision-table-engine-adapter.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/activities` · `go doc github.com/dr-dobermann/gobpm/pkg/rules`
