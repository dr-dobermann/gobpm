---
title: Parallel gateway (AND)
description: Concurrent split and synchronizing join.
---

# Parallel gateway (AND)

A **parallel gateway** does two jobs, chosen by its direction. **Diverging**, it
forks one incoming token into **every** outgoing flow at once — the branches run
concurrently, unconditionally, with no default. **Converging**, it
**synchronizes**: it waits until a token has arrived on *every* incoming flow,
then emits a single surviving token. Reach for it when several independent steps
must run in parallel and the process must not proceed until all of them finish.
This page is the developer reference — the type, its constructor, its options,
and its runtime behavior.

## Taxonomy

| | |
|---|---|
| BPMN category | Gateway → **Parallel (AND) Gateway** (§13.4.1) |
| Package | `github.com/dr-dobermann/gobpm/pkg/model/gateways` |
| Type | `gateways.ParallelGateway` |
| Embeds | `gateways.Gateway` (direction, default flow, flow-connection rules) → `flow.BaseNode` |
| Implements | `flow.Node` (`Node`, `Clone`, `NodeType`, flow wiring), `exec.NodeExecutor` (`Exec`) |
| The work | fork all outgoing flows (diverging) / synchronize all incoming flows (converging) |

Where it sits in the gateway family: [Gateways taxonomy](index.md).

## Constructor

```go
func NewParallelGateway(opts ...options.Option) (*ParallelGateway, error)
```

| Parameter | Meaning |
|---|---|
| `opts` | zero or more options (below). A gateway carries no per-branch configuration — the split forks *all* its outgoing flows and the join waits for *all* its incoming flows, so behavior is fixed by the wiring, not by the constructor. |

It returns an error — never panics — on an invalid option (e.g. an out-of-range
`WithDirection` value). The split and join are the **same type**; you
distinguish them only by direction.

## Options

Most parallel gateways need only one:

| Option | When you reach for it |
|---|---|
| `WithDirection(dir)` | mark the gateway a split (`Diverging`) or a join (`Converging`). |

The full set is the shared **gateway options** — a parallel gateway adds none of
its own:

| Option | Effect |
|---|---|
| `WithDirection(dir GDirection)` | set the gateway direction — `Diverging`, `Converging`, `Mixed`, or `Unspecified`. |
| `foundation.WithID(id string)` | set an explicit element id (otherwise generated). |
| `options.WithName(name string)` | set the diagram name. |
| `foundation.WithDoc(text, format string)` | attach documentation. |

`GDirection` is a string enum with four values:

| Value | Meaning |
|---|---|
| `Diverging` | a split — multiple outgoing flows, one incoming (a fork). |
| `Converging` | a join — multiple incoming flows, one outgoing (synchronize). |
| `Mixed` | both multiple incoming and outgoing. |
| `Unspecified` | direction left open (the zero role). |

> These are the only options `NewParallelGateway` accepts (`foundation.WithID`,
> `foundation.WithDoc`, `options.WithName`, `gateways.WithDirection`). There is
> no condition, threshold, or per-branch option — that is the whole point of an
> AND gateway.

For the complete, always-current signatures run
`go doc github.com/dr-dobermann/gobpm/pkg/model/gateways`.

## Build it

The split and join are both `ParallelGateway`s, distinguished by direction:

```go
split, err := gateways.NewParallelGateway(
    gateways.WithDirection(gateways.Diverging))

join, err := gateways.NewParallelGateway(
    gateways.WithDirection(gateways.Converging))
```

Add every node to the process, then wire the diamond — the split fans out to
both workers, and both workers converge on the join:

```go
for _, e := range []flow.Element{start, split, workerA, workerB, join, end} {
    if err := proc.Add(e); err != nil {
        return fmt.Errorf("add element: %w", err)
    }
}

// start ─> split ─┬─> worker-a ─┬─> join ─> end
//                 └─> worker-b ─┘
for _, l := range [][2]flow.Element{
    {start, split},
    {split, workerA}, {split, workerB},
    {workerA, join}, {workerB, join},
    {join, end},
} {
    if err := link(l[0], l[1]); err != nil {
        return err
    }
}
```

Each branch is an ordinary `ServiceTask` whose operation is a Go functor. Here
it prints and signals a channel, so the demo can prove both branches actually
ran:

```go
op, err := gooper.New(
    name+"-op",
    func(_ context.Context, _ service.DataReader, _ *data.ItemDefinition) (*data.ItemDefinition, error) {
        fmt.Printf("  ▶ %s executed\n", name)
        done <- name

        return nil, nil
    })

st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
```

## Run it

```bash
cd examples/parallel-gateway && go run .
```

Both branches execute (in either order) and the instance completes:

```
  ▶ worker-a executed
  ▶ worker-b executed
✓ parallel-demo completed: split forked both branches, join synchronized, one token reached End
```

> **Note:** the two `▶` lines can print in either order — that is the point. The
> branches run on their own goroutines (tracks), so their relative timing is not
> deterministic.

## Methods & runtime behavior

The engine drives the gateway through these — you rarely call them directly:

| Method | Role |
|---|---|
| `Exec(ctx, re) ([]*flow.SequenceFlow, error)` | activate **every** outgoing flow unconditionally (§13.4.1) — no condition, no default, cannot fail. Drives both the diverging split (1→N) and the surviving track's continuation past a join. |
| `Arrive(incomingFlowID, arrivingTrackID string) (complete bool, merged []string)` | record a token arriving at the join; report whether every incoming flow has now delivered. Atomic under the gateway's own mutex. |
| `Node() flow.Node` | return the concrete `ParallelGateway` so a track dispatches it as a `NodeExecutor`, not the embedded base `Gateway`. |
| `Clone() (flow.Node, error)` | per-instance copy — shared immutable config, fresh arrival state. |
| `Direction() GDirection` / `NodeType()` | introspection (inherited from `Gateway`). |

Behavior worth knowing:

- **Diverging split.** `Exec` fires *all* outgoing flows unconditionally
  (BPMN 2.0 §13.4.1) — one incoming token becomes N branch tokens. No condition
  evaluation, no default flow. Each branch runs on its own **track** (goroutine),
  concurrently and independently.
- **Converging join = synchronization.** `Arrive` records each arrival under the
  gateway's own mutex. A non-completing arrival is recorded and **parks**
  (`complete=false`, `merged=nil`) — it does *not* spawn a continuation. The
  completing arrival (once every incoming flow has delivered) returns
  `complete=true` with `merged` — the ids of every track absorbed into the join;
  the completing arrival itself is the survivor and continues onward.
- **Concurrency-safe.** Because the gateway owns its per-instance arrival state
  and serializes concurrent arrivals with its own lock, two branches finishing at
  the same moment are handled correctly — exactly one token leaves the join.
- **Scaling.** Add more `(split → workerN)` and `(workerN → join)` pairs; the
  split forks all of them and the join waits for all of them, with no per-branch
  configuration. A parallel gateway may also be purely diverging (a fork with no
  matching join) or purely converging — the join synchronizes only the incoming
  flows it actually has.

For an every-*true*-branch split that also synchronizes, use the
[Inclusive gateway](inclusive.md); for a single data-chosen branch, the
[Exclusive gateway](exclusive.md).

## See also

- Examples: `examples/parallel-gateway/`
- Related guides: [Exclusive (XOR)](exclusive.md) · [Inclusive (OR)](inclusive.md) · [Complex](complex.md) · [Event-based](event-based.md)
- Design: [ADR-005 — Gateways and joins](../../design/ADR-005-gateways-and-joins.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/model/gateways`
</content>
</invoke>
