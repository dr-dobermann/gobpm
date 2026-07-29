---
title: Persistence & recovery
description: Instance checkpoints, restart recovery, and safely sharing one store between engines.
---

# Persistence & recovery

How a gobpm instance becomes durable (ADR-033): with an **explicitly
configured repository**, every instance
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
| **Human task** | re-announced to the distributor under its **recorded task id** — a reference a human or UI is holding survives the restart |
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

  **A boundary event is a node, so pin its id too.** This one is easy to
  miss, because a boundary reads as an attachment to its activity rather
  than as a step in the flow:

  ```go
  bnd, err := events.NewBoundaryEvent("escalate", approve, timerDef, true,
      foundation.WithID("order-escalate")) // ← as much a node id as any other
  ```

  Unlike a missing node id, this one does **not** refuse loudly. The
  recovered instance runs; only the boundary's **recorded deadline** is
  lost, because the recovering engine cannot match the record to a
  boundary whose id it minted differently. The boundary then re-arms by
  re-evaluating its definition — so "escalate 24 hours from now" quietly
  becomes 24 hours from the *recovery*, and an instance recovered often
  enough never escalates at all. Pin the id and the deadline survives
  exactly.

## Dehydration: a long wait costs no goroutines

An instance waiting on a **long timer** does not need to stay in memory.
When every one of its live tracks is parked on a wait that is both
*dehydratable* and *held* by an engine-level holder, the instance
**dehydrates**: it takes a final consistent-cut checkpoint, releases
**all** its goroutines (the loop and every parked track) and leaves —
its checkpoint becomes the wake source. A thousand instances waiting on
a two-day timer cost a thousand rows and **zero goroutines**.

The engine's **timer service** holds the deadline on the released
instance's behalf. When the deadline arrives it rebuilds the instance
from its checkpoint and continues the woken wait — the timer node fires
*through*, with its trigger already in hand, straight to the outgoing
flow. Two `KindInstanceState` facts make the cycle observable at Info:
`Dehydrated` when it releases — carrying the **wait kinds** it is parked
on, how many, and the `goroutines=0` that is the point of the whole
feature — and `Hydrated` when a trigger brings it back, carrying the
**trigger kind**, the woken wait, and whether the wake **continued** the
flow or **completed** the instance.
An instance oscillates freely: park, release, wake, continue, release
again — each cycle costs one checkpoint and one rebuild, and the
recorded track lineage never grows.

Dehydration arms itself; there is nothing to configure beyond
`WithRepository` (without a repository there is no checkpoint to wake
from, so nothing ever releases). What releases today:

- **A one-shot timer more than an hour out** — released, its deadline
  held by the engine's timer service. A shorter one is not worth a
  checkpoint-and-rebuild round trip, so it keeps its in-memory waiter
  and the instance stays resident. A **repeating** timer also stays
  resident.
- **A message or signal catch** — released. A receive is a pure wait,
  arbitrarily long and entirely externally driven, so there is no
  threshold. The engine takes over the subscription, **keyed to the
  instance's conversation**: a message for a different conversation is
  filtered exactly as it would be for a resident instance, and never
  wakes it. On the wake the message's payload binds normally, and the
  conversation keys it derives are recorded.
- **An Event-Based Gateway** — released when **every arm** it races is
  itself holdable, since the gateway is one wait node holding a *set* of
  subscriptions. When a trigger arrives the winning arm continues and
  the losing arms' holds are withdrawn, exactly as a resident gateway
  withdraws its losers. One unholdable arm (a conditional) keeps the
  whole gateway resident.
- **A human task** — released. The task keeps living in the
  distributor's inbox, which is exactly why the instance need not: a
  `Take` or `Complete` on it hydrates the instance and then proceeds
  normally. The task keeps **the id the human is holding** — a
  rehydrated task is never re-issued under a new one — and the instance
  is held in memory for the duration of the action, so a caller never
  sees dehydration at all.
- **A wait guarded by a boundary event** — released when the boundary is
  held too. A boundary is not a track, so it is easy to forget that it
  is also a wait: "approve within 24 hours or escalate" is *two* things
  to wake for, and releasing the instance while only the task was held
  would drop the escalation on the floor. So an armed boundary takes a
  holder of its own, and the instance releases only when every boundary
  guarding it is held — the same per-arm rule an Event-Based Gateway
  applies to its arms.

  When the boundary's deadline arrives, the rebuilt instance re-arms it
  at its **recorded** deadline. Since that moment has already passed, it
  does not wait again: the token forks at the boundary event with the
  guarded track as its parent — interrupting cancels that parent (the
  task is withdrawn), non-interrupting leaves it running — exactly as a
  resident boundary fires. Boundary kinds that resolve directly rather
  than waiting (Error, Escalation, Compensation, Cancel) arm nothing and
  cost no residency.
- **Everything else** — resident. An **external-worker** task never
  releases (a job in flight is active work, not a passive wait); a
  **conditional** catch never releases — its trigger is the instance's
  own data, so there would be nothing external to wake it, and the same
  applies to a conditional *boundary*. An instance releases only when
  *every* live track qualifies, so one resident wait keeps the whole
  instance in memory. No wait is ever released without something that
  can wake it — a trigger is never lost.

A dehydrated instance is still **in flight**, and the API says so: the
handle reports `StateDehydrated` (non-terminal), `WaitCompletion` keeps
blocking across as many dehydration cycles as the instance goes through,
and a handle taken before a release keeps speaking for the instance after
it is rebuilt.

A dehydrated instance has no loop to renew its lease, so the lease
lapses; that is deliberate and harmless on a single engine (the
in-memory holder owns the wake), and after a crash it is exactly what
lets restart recovery reclaim the instance. That fallback is for a
crashed engine only — a running one never loses a wake source: if a wake
fails (say the pinned process version is not registered on this engine),
the hold is **kept and retried**, so the instance recovers by itself once
the cause clears. A second engine claiming a
lapsed-lease dehydrated instance is multi-node coordination — a later
slice.

## Current limits (the next slices)

An instance with an in-flight **Call Activity**, **parallel
multi-instance group** or **compensation sweep** defers its checkpoint
(the `CheckpointDeferred` fact at Warn) and runs on volatile state until
the next capturable transition — execution is never blocked, and the
degradation is operator-visible. Full-fidelity capture of those
constructs and the operator suspend/resume surface are the following
ADR-033 slices.

## See also

- Runnable example: [`examples/restart-recovery/`](../../../examples/restart-recovery/)
- The seam: [Custom repository](../extending/repository.md)
- Related: [Instance lifecycle](instance-lifecycle.md) · [Observability in practice](observability.md)
- Design: [ADR-033 — Persistence & State](../../design/ADR-033-persistence-and-state.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/repository`
