# Persistence — checkpoints, recovery & the shared store

How a gobpm instance becomes durable (ADR-033, first slice landed by
SRD-070): with an **explicitly configured repository**, every instance
writes a **consistent-cut checkpoint** at its observable lifecycle
transitions, a crashed engine's instances are **recovered on restart**
(or by another engine sharing the store), and concurrent engines are
kept safe by **ownership leases with CAS fencing**.

## Arming it

```go
repo := memrepo.New()                       // or a durable adapter

th, _ := thresher.New("engine-A",
    thresher.WithRepository(repo),          // the state of record — arms checkpointing
    thresher.WithLeaseTTL(30*time.Second))  // the ownership window (default 30s)
```

The zero-config engine (no `WithRepository`) stays exactly as before:
**volatile, zero overhead**. Configuring a repository means "this store
holds the state of record": every instance checkpoints into it, and
`Run` recovers what the store says is unfinished.

## What is written, and when

One **schema-versioned document per instance**, written by the
instance's own event loop (the single writer — every cut is consistent
by construction) after each observable transition: activation, a node's
completion, the wait parks (event / human task / worker job), scope
opens, and the terminal state. The document carries:

- the **pinned process version** — recovery re-clones exactly the
  version the instance started from;
- the scope tree's **committed data** (a canonical tagged-JSON codec
  over the value model — scalars, arrays, records, maps; an
  unserializable payload fails **loud**, never silently skipped);
- conversation keys, the compensation ledger (with its snapshots), and
  the **live tracks** — each with its node, lineage and, for a parked
  timer, the **absolute deadline recorded at arming**.

Derived state (armed boundaries, event subscriptions, routing) is
**never stored** — hydration rebuilds it by re-walking the graph.

## Recovery

On `Run`, an armed engine lists the **claimable** records — non-terminal,
with an expired-or-absent lease — claims each under a higher lease
incarnation, and restores it with **re-enter-the-node** semantics: the
parked track respawns entering its recorded node, so the node's own
arming code re-registers whatever the wait needs. Per wait kind:

| Wait | On recovery |
|---|---|
| **Timer** | re-arms at the **recorded** deadline — a Duration never restarts; an **overdue deadline fires once, immediately** (missed cycle repetitions collapse into one catch-up firing) |
| **Human task** | re-announced to the distributor under a fresh task id |
| **Worker job** | re-enqueued to the dispatcher |
| **Message / signal** | the subscription re-registers; messages that arrived while down were never accepted — the sender's retry redelivers (correlation dedups) |
| **Conditional** | re-arming re-runs the initial evaluation — a condition that turned true during the downtime fires right away |

Recovery failures are **per-instance and loud** (an operator-visible
fact); one corrupt record never blocks the rest. A recovered instance
announces itself with the `InstanceState/Recovered` fact at Info.

## Effects are at-least-once; state is exactly-once

A crash window can duplicate an *effect* (a re-announced task, a
re-enqueued job, a re-fired functor) — the receiving seams absorb this
(correlation dedup, job-lock reclaim, idempotent announce). *State*
cannot be duplicated: every save is **CAS-checked** (record version)
and **fenced** (lease incarnation), so a zombie engine that wakes up
after its instances were reclaimed can still run its in-memory copies,
but its saves are rejected — visible as `CheckpointDeferred` warnings
on the zombie, never as corrupted state.

## Sharing one store between engines

Several engines MAY share one repository (ADR-033 §2.8). The rules:

- an instance is **owned by a lease** (engine id + incarnation +
  expiry); only its owner runs it;
- a crashed engine's instances become claimable when the lease lapses
  (`WithLeaseTTL` tunes how fast) — no coordinator, the store's CAS is
  the only synchronization primitive;
- **deployment parity is the operator's contract**: an engine can only
  recover instances whose pinned process version it has registered, and
  the definitions must carry **stable element ids** —
  `foundation.WithID(...)` on every node (or a serialized model with
  persistent ids). Two engines building a model from the same source
  *without* pinned ids mint different ids, and recovery refuses, loud.

## Current limits (the next slices)

An instance with an in-flight **Call Activity**, **parallel
multi-instance group** or **compensation sweep** defers its checkpoint
(the `CheckpointDeferred` fact at Warn) and runs on volatile state until
the next capturable transition — execution is never blocked, and the
degradation is operator-visible. Full-fidelity capture of those
constructs, goroutine-releasing dehydration and wake-on-trigger, and
the operator suspend/resume surface are the following ADR-033 slices.

See it live: [`examples/restart-recovery/`](../../examples/restart-recovery/).
