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

## Running on PostgreSQL

The durable adapter lives in its own module,
[`adapters/postgres`](../../../adapters/postgres/). The database
handle is **yours** — pool, credentials and driver stay with the
application; the adapter imports `database/sql` only:

```go
import (
    _ "github.com/jackc/pgx/v5/stdlib" // or any database/sql driver

    "github.com/dr-dobermann/gobpm/adapters/postgres"
)

db, _ := sql.Open("pgx", os.Getenv("DATABASE_URL"))

repo, _ := postgres.New(db,
    postgres.WithSchema("gobpm"))       // the namespaced schema (default)

th, _ := thresher.New("engine-A",
    thresher.WithRepository(repo))
```

The adapter implements `renv.Migrator`, so **`Run` migrates the schema
itself** before recovery: embedded, versioned SQL applied one file per
transaction under an advisory lock (concurrently booting engines
serialize), recorded in a `schema_version` ledger; re-running is a
no-op. A migration failure aborts the start loud — a half-created
schema never looks like an empty store.

The schema holds four tables: `groups` (the engine-group registry),
`tenants` (per group, with a flag-designated default the database
limits to one per group), `instances` (the checkpoints; records
reference their group and tenant by foreign key), and
`schema_version`. There is deliberately **no CHECK constraint on
`status`** — the vocabulary is append-only, and DDL must not reject a
status a newer engine writes.

For development and the test suite, `make pg-up` runs a disposable
`postgres:17-alpine` container and prints the
`GOBPM_PG_TEST_DSN` export the postgres-gated tests read (unset, they
skip; `make pg-down` stops it). **`make ci` expects that DSN** — with
it unset the adapter's tests skip and the diff-coverage gate goes red
on adapter changes, which is the loud version of "you forgot the
database". CI provides the same database as a service container.

## Engine groups

Recovery is scoped to an **engine group** (ADR-033 v.4 §2.8): an
engine lists, claims and recovers only its own group's instances.

- **Ungrouped = solo.** Without options, an engine forms a
  single-engine group under its **own id** — clustering is explicit
  opt-in, never accidental. A restarted engine with the same id
  recovers its own instances; a differently-named neighbor over the
  same store sees nothing.
- **`thresher.WithEngineGroup("billing")`** — create-or-join: `Run`
  establishes the group in the store's registry and every checkpoint
  carries it. Engines sharing the group name over one store form a
  recovery cluster.
- **`thresher.WithExistingEngineGroup("billing")`** — join-only: `Run`
  refuses to start when the group is not already established. This is
  the typo-guard for fleet members — a misspelled group name refuses
  loud instead of silently minting a fresh one-engine partition. It
  requires an explicit `WithRepository`.

A record reached by id across groups (a wiring mistake, not a race) is
refused loud on the claim, on both the recovery and the wake paths.

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

- engines that should recover each other share an **engine group**
  (`WithEngineGroup`, above); groups never cross-list;
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

## Composite constructs restore at their position

Every composite construct records its position in the checkpoint and
restores **at that position** (ADR-033 v.4 §2.10) — nothing completed
ever re-executes, and no construct defers the capture:

- A **composite scope** mid-body: the drained scope resumes its host
  exactly once.
- **Sequential MI / Standard Loop**: the iteration resumes at the
  recorded pass with its collected outputs; a fired
  `completionCondition` is honored.
- **Parallel MI**: exactly the still-open instances re-open at their
  recorded ordinals; completed outputs (and the holes of canceled
  slots) survive.
- A **resolving compensation sweep**: the remaining queue resumes in
  reverse completion order; the handler that was RUNNING re-runs (a
  handler is an effect — at-least-once over its immutable snapshot);
  the wait-throw resumes only after the drain.
- An **Ad-Hoc Sub-Process**: the routing state — completed counts, a
  manual container's pending offer, a fired completion condition —
  restores with the container, and the next Router decision sees the
  true cross-crash progress. A pre-fidelity document (schema ≤ 4) that
  froze an in-flight container refuses to restore loudly rather than
  resuming with the routing state lost.
- An **in-flight Call Activity**: the child is a durable instance of
  its own, symmetrically linked — recovery restores both ends and
  re-links them, whether the child is recovered, still awaiting
  recovery, or finished while the engine was down. A missing
  counterpart fails the restore loud on either side; a recovered
  caller never launches a duplicate child.

`CheckpointDeferred` now signals only a real failure (an unserializable
payload, a failed save) — still loud, never a silent skip.

## A call tree recovers as a unit

Recovery claims a **call tree**, not an instance: a child is never
revived on its own — an engine reaches it only through its caller's
claim, which walks the recorded call links transitively before
restoring anything. So in a multi-engine group two engines can no
longer split a caller from the child it awaits, and no manual
placement rule is needed: the caller resumes with its children already
resident on the same engine.

Two consequences worth knowing:

- **A child whose caller is terminal is finished, not revived.** A
  caller completes only after its call returns, and a terminating
  caller terminates its children — so a terminal caller with a live
  child means a cancel cascade that a crash interrupted. Recovery
  writes that child's terminal record and reports it
  (`InstanceState/Terminated`, `reason=caller-terminal`) rather than
  running an instance whose outcome nothing will consume.
- **A child genuinely live on another engine refuses loudly.** If a
  record written before this rule (or a misbehaving store) leaves a
  child running elsewhere, the caller's recovery fails with a fact
  naming the child and its holder — and that child keeps running.
  Tearing down another engine's work to satisfy a recovery would
  destroy real state.

## Current limits (the next slices)

The operator **suspend/resume** surface is the remaining ADR-033
slice. One documented corner remains in the multi-engine picture:
re-linking a caller and child that are *deliberately* placed on
different engines — remote child handles, cross-engine completion
delivery and cancel cascade — is distribution work, still open as
#308. Recovery affinity means a healthy group never needs it.

## See also

- Runnable example: [`examples/restart-recovery/`](../../../examples/restart-recovery/)
- The seam: [Custom repository](../extending/repository.md)
- Related: [Instance lifecycle](instance-lifecycle.md) · [Observability in practice](observability.md)
- Design: [ADR-033 — Persistence & State](../../design/ADR-033-persistence-and-state.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/repository`
