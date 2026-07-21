# ADR-026 — Compensation: undoing completed work by throw-triggered handlers

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-20 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-006 v.4](ADR-006-events-and-subscriptions.md) §2.2/§2.3 (the explicit deferral this ADR fulfills: the compensation-boundary eligibility window is decided there, the *handling* — throw resolution, reverse-order invocation, snapshot restore, presumed-abort, the `compensate-on-terminate` switch — is decided **here**), [ADR-018 v.1](ADR-018-boundary-events-and-activity-interruption.md) (the boundary attachment model; Compensation was deferred from its 0.1.0 trigger scope), [ADR-023 v.2](ADR-023-sub-process-and-call-activity.md) (the execution-scope tree the completion ledger lives on), [ADR-013 v.2](ADR-013-instance-observability.md) (the reserved `Compensating` slot this ADR populates), [ADR-001 v.6](ADR-001-execution-model.md) §4 (the single-writer loop that owns all compensation state) |

Compensation is the last event behavior of the events epic (#90): the mechanism
by which a process **undoes work that already completed successfully**. It is
the inverse of the error/escalation family — those react to something going
wrong *while work is in flight*; compensation reaches back to work that
*finished*, after the process decides its effects must be reverted.

## 1. Context & problem

The model layer already carries `CompensateEventDefinition` (an `activityRef`
and a wait flag), the `Association` artifact ("used to show the Activity used
for compensation" — kept in the executable subset for exactly that reason), and
the `isForCompensation` marker on activities. Nothing executes any of it: a
compensation throw drops onto the generic event bus with no subscriber, no
handler is ever recorded as eligible, and no completed activity's data survives
its scope.

Three prior decisions constrain this ADR:

- **ADR-006 v.4 §2.3** owns the *subscription-layer fact*: a compensation
  boundary handler is **not a live subscription** — it becomes *eligible* when
  its guarded activity reaches `Completed` (a data snapshot is captured at that
  moment) and stays eligible until the **enclosing scope finishes**. The
  *handling* is explicitly delegated to this ADR.
- **ADR-006 v.4 §2.2** fixes the terminate default: *Terminate runs NO
  compensation* (§13.5.6 — "without compensation or event handling"); an
  optional, off-by-default `compensate-on-terminate` extension is to be decided
  **here**, including its activation scope (process-/instance-level, explicitly
  not a blunt engine-wide switch).
- **ADR-001 v.6 §4 / ADR-023 v.2**: all instance state mutation happens on the
  single-writer loop, and the execution scope tree is the unit of containment,
  cancellation, and data visibility. Whatever records "what completed, in what
  order, with what data" belongs to the loop-owned scope state.

### Object model (BPMN 2.0, verbatim from the vendored extract)

- `CompensateEventDefinition` carries `activityRef: Activity [0..1]` and
  `waitForCompletion: boolean [1] = true`
  ([event-definitions.md](../bpmn-spec/elements/event-definitions.md) §10.4.5).
- Valid positions: **Start (event sub-process only), IntermediateThrow, End,
  Boundary** ([conformance.md](../bpmn-spec/conformance.md)). There is **no**
  in-flow Compensation Intermediate *Catch* — the catch side exists only as a
  boundary or an event-sub-process start.
- A compensation handler is one of ([compensation.md](../bpmn-spec/semantics/compensation.md) §13.5.5):
  a **boundary Compensation event** on the activity, linked by an `Association`
  to a **Compensation Activity** marked `isForCompensation = true`; or a
  **Compensation Event Sub-Process** (start event carrying
  `CompensateEventDefinition`) inside a Sub-Process.
- ADR-006 §2.1 classifies Compensation under the **direct resolution** reach
  strategy — aimed at a specific Process/Activity instance, never published,
  never broadcast through the hub.

## 2. Decision

### 2.1 The completion ledger — the scope remembers what finished

Each open execution scope keeps a **completion ledger**: an ordered record of
the activities inside it that completed successfully **and are compensable** —
an activity is compensable when a compensation handler is attached to it (a
Compensation boundary with its associated `isForCompensation` activity) or when
the scope carries a Compensation Event Sub-Process covering it.

An entry is appended at the activity's `Completed` transition and carries:

- the activity's identity and its **completion ordinal** (the ledger's order
  *is* the reverse-compensation order — §2.4);
- the **data snapshot** captured at that instant (§2.5);
- the resolved handler reference.

The ledger lives and dies with its scope **for normal completion of the
enclosing scope**: per ADR-006 §2.3, eligibility ends when the enclosing scope
finishes — at that point the ledger (and its snapshots) is discarded. While the
scope that *threw* compensation is still open, the ledgers of its **completed
child scopes** must still be reachable (a Sub-Process that completed inside the
current scope is itself a compensable, ledgered entry) — so a child scope's
ledger is **folded into its parent's ledger entry** when the child completes,
rather than being dropped at child close. Activities that never completed
(failed, canceled, interrupted) never enter a ledger — the **presumed-abort**
principle (§13.5.5): only successfully completed work is compensable; anything
else is presumed already undone.

*Why a ledger and not a live subscription:* ADR-006 §2.3 already decided a
compensation boundary is not armed as a watcher — there is no live trigger to
wait for. What the engine needs at throw time is a *record of the past* (what
completed, in what order, with what data), and only the loop can own that
record race-free (ADR-001).

### 2.2 Triggering — a throw event, targeted or scope-wide

Compensation is triggered **only** by a throw Compensation Event — an
**Intermediate Throw** in normal flow or a **Compensation End Event**
(§13.5.5; ADR-006 §2.2 keeps Terminate compensation-free). The throw resolves
**directly** against the ledgers (the ADR-006 §2.1 direct-resolution strategy)
— never through the event hub:

- **Targeted** (`activityRef` set): compensate exactly that activity — look up
  its ledger entry in the throwing scope (or the entries folded from completed
  child scopes) and run its handler. If the referenced activity has no ledger
  entry (it never completed, or its scope already finished), the throw is a
  **no-op for that target — logged, never silently dropped** (§2.6).
- **Scope-wide** (no `activityRef`): compensate **all** ledgered activities of
  the throw's enclosing scope, in **reverse completion order** (§2.4). Per the
  spec's scoping rule, the default target context is the throw's own containing
  activity/process: a throw inside a Sub-Process's handler compensates that
  Sub-Process; a throw at process level compensates the process's completed
  activities.

**`waitForCompletion`** (spec default `true`) governs the throwing token:
`true` — the throw **parks** until every invoked handler completes, then
continues (End: then ends); `false` — fire-and-forget: handlers are launched
and the throwing token continues immediately. Either way the throw is
**non-fault**: like Escalation, compensation is a normal control-flow act, not
an error.

### 2.3 Handlers — how compensation work runs

- **Boundary handler**: the Compensation boundary on the completed activity
  routes to its associated **Compensation Activity** (`isForCompensation =
  true`, linked by `Association`). The compensation activity is a normal
  activity executed **once per invocation**, in a fresh track, but with the
  ledger entry's **data snapshot as its read context** (§2.5). It is excluded
  from normal token flow: the extract models it as a specialized "black-box"
  Compensation Activity reachable only through its `Association` (§13.5.5), so
  the engine **validates** that an `isForCompensation` activity carries no
  incoming/outgoing normal sequence flow — reject-at-registration, the
  fail-fast idiom.
- **Compensation Event Sub-Process**: an event sub-process whose start carries
  `CompensateEventDefinition` handles compensation **of its enclosing
  Sub-Process as a whole** — it is that Sub-Process's own compensation handler,
  invoked when the completed Sub-Process is compensated (targeted or swept by a
  scope-wide throw). It reuses the ADR-023 §2.10 handler machinery — but is
  **never hub-armed** (the Error/Escalation no-waiter pattern): it is recorded
  as the Sub-Process's handler in the parent's ledger entry at the
  Sub-Process's completion.
- The **`cancelActivity` notion does not apply** to a Compensation boundary
  (there is nothing to interrupt — the activity already completed); the flag is
  meaningless and the model rejects a non-default value.

**The compensable unit of this conception is an activity with its own
handler** — a leaf activity, or a Sub-Process with its own handler (boundary or
compensation event sub-process). The spec's **default/implicit compensation** —
a completed Sub-Process *without* its own handler recursively compensating its
children in reverse order — is **designed-for**: the ledger's child-folding
(§2.1) is shaped to carry it, and it completes this conception in a follow-up
revision (§2.9). Until then, sweeping a handler-less completed Sub-Process is a
logged no-op per §2.6.

### 2.4 Order — reverse completion, from the ledger

A scope-wide compensation runs handlers in **reverse completion order** — the
ledger walked backward (§13.5.5: compensation "in the reverse order" of forward
execution; sequence-flow and data dependencies invert). The completion ordinal
recorded at `Completed` time is the authority; the engine does not re-derive
graph dependencies at throw time. Handlers run **sequentially** in that reverse
order — the spec permits concurrency only where the
original activities were independent, and sequential-reverse is always a
conformant serialization of that partial order (an engine choice, revisitable
without a contract change).

### 2.5 The data snapshot — compensate against the past

At an activity's `Completed` transition, the engine captures a **value-copy
snapshot** of the data readable in the activity's context (§13.5.5: a snapshot
"is taken and kept for later use" and the handler sees "the original data
context restored"). When a handler runs, that snapshot is its **read
surface** — the handler sees the world as it was when the work finished, not
the current state. Handler **writes** go to the *current* live scope through
the normal commit path (compensation exists to change current state — e.g.
issue a refund record — while *reading* what the completed work knew).
Snapshots are per-ledger-entry; for a loop/MI activity each completed instance
snapshots separately (per §13.5.5's per-instance rule — MI compensation
specifics ride ADR-025 §2.10's deferral).

### 2.6 Unresolved compensation is logged, never silent, never a fault

A throw whose target has nothing to compensate — an `activityRef` that never
completed or whose scope already closed, or a scope-wide sweep over an empty
ledger — is a **non-fault no-op that is always logged** and emits an
`Unresolved`-phase fact. This is the engine's cross-cutting
uncaught-events-always-log rule (established for Escalation in ADR-006's §2.4
lineage): the spec is silent on observability here, and a compensation throw
that silently does nothing is a debugging trap. Execution continues per §2.2.

### 2.7 Lifecycle & observability

Compensation populates the state slot ADR-013 v.2 reserved:

- Activity-side transitions (§13.5.5): `Completed → Compensating` when its
  handler is invoked; `Compensating → Compensated` on handler success;
  `Compensating → Failed` when the handler itself fails — the handler's failure
  is a **real fault** raised in the throwing scope's context and travels the
  ADR-006 §2.6 Error chain (a broken undo must not vanish); `Compensating →
  Terminated` when an instance terminate interrupts an in-flight compensation
  (ADR-006 §2.2's mandatory default).
- A dedicated **Compensation fact kind** with phases: `Thrown` (at the throw,
  with target/scope attribution), `Eligible` (a ledger entry recorded at
  `Completed`), `Compensating`/`Compensated` per handler invocation, and
  `Unresolved` (§2.6, echoed at Warn). The Escalation kind's shape is the
  precedent; `Compensating`/`Compensated` fill ADR-013's reserved names.
- **The ledger itself is observable** — it is a new first-class runtime object
  (§2.1), so its *whole* lifecycle emits facts, not only the handler firings.
  Every transition an entry can take is covered: **recorded** (`Eligible`, at
  the activity's `Completed`, carrying the completion ordinal), **folded**
  (`Folded` — a completed child scope's entries reparented into the enclosing
  scope's ledger, §2.1), **consumed** (`Compensating`/`Compensated` above), and
  **discarded** (`Discarded` — the enclosing scope finished and its ledger,
  with any never-compensated entries and their snapshots, is dropped; the
  normal end of the ADR-006 §2.3 eligibility window, echoed at Debug). With
  these, an operator can reconstruct at any moment *what would compensate right
  now and in what order*, and audit where every entry went — recorded → folded*
  → consumed | discarded. Fact details carry the scope path, the activity, and
  the ordinal, so the observer stream is the ledger's audit log.

### 2.8 `compensate-on-terminate` — decided, off by default

ADR-006 §2.2 delegated the optional extension to this ADR. Decision:

- **Activation scope**: a **per-process registration option** (declared when
  the process is registered, so every instance of that definition behaves
  alike) — honoring ADR-006's constraint that it is not an engine-wide switch.
  No per-instance override in the first design (YAGNI; revisit on demand).
- **Semantics when enabled**: a Terminate End Event (or instance terminate)
  first runs a **scope-wide compensation sweep** (§2.2's broadcast, reverse
  order, `waitForCompletion = true` semantics) of the terminating scope's
  ledger, then proceeds with the normal terminate cascade. A second terminate
  arriving during that sweep interrupts it (`Compensating → Terminated`) —
  terminate always wins.
- **Default**: **off** — the conformant §13.5.6 behavior (no compensation on
  terminate) stays the out-of-the-box contract.

### 2.9 Designed-for & out of scope

- **Transaction / Cancel-triggered compensation.** A Transaction Sub-Process
  cancel "terminates all running Activities and compensates all successfully
  completed Activities" (§10.7 / the Cancel boundary rules). The Transaction
  type itself does not exist in the engine (ADR-023 keeps it designed-for under
  #91), and no runtime Cancel handling exists. Cancel-triggered compensation
  therefore rides the **Transaction workstream**, which will consume this ADR's
  ledger and sweep machinery as-is. Nothing here precludes it: a
  transaction-cancel is, mechanically, §2.2's scope-wide sweep triggered by
  Cancel instead of a Compensation throw.
- **Call Activity.** Compensating a completed Call Activity means re-entering a
  **finished child instance** — a cross-instance compensation protocol (the
  child's ledger died with the child). Explicitly **out of scope**; a
  Call Activity never enters a ledger in this design. Revisit with durable
  persistence (ADR-009's rehydration line).
- **Error-driven default compensation.** The presumed-abort principle's second
  half (§10.7): a Sub-Process with **no** error handler that suffers an error
  should, by default, auto-compensate its completed contained activities before
  the error propagates. Mechanically this is §2.2's scope-wide sweep fired from
  the Error path instead of a Compensation throw — another consumer of the
  ledger, like Transaction-cancel. **Designed-for**: this conception leaves the
  decided Error semantics (fault propagation up the scope chain, ADR-006 §2.6)
  unchanged; the auto-sweep joins the error path in a follow-up conception that
  must co-design with the Error chain's cancellation ordering. Until then the
  engine's error behavior remains the current conformant-subset one, called out
  as an engine note (§2.10).
- **Recursive default compensation** — designed-for in §2.3.

### 2.10 Engine notes (deviations & choices)

| Choice | Standard position | Engine choice |
|---|---|---|
| Sequential reverse-order sweep | permits concurrency between independent compensations (§13.5.5) | strictly sequential reverse completion order — a conformant serialization; simpler, deterministic (§2.4) |
| Unresolved throw is logged | silent no-op ("compensation is not performed" when nothing qualifies) | logged + `Unresolved` fact, execution continues — the cross-cutting always-observable rule (§2.6) |
| Handler reads snapshot, writes live scope | "original data context restored" (§13.5.5); silent on write targets | reads = snapshot; writes = normal commit into the live scope (§2.5) |
| `compensate-on-terminate` | terminate never compensates (§13.5.6) | conformant default kept; optional per-process-registration switch decided here, off by default (§2.8) |
| Compensable set | any completed activity | this conception: activities with their **own** handler; handler-less Sub-Process recursion designed-for (§2.3) |
| Error without an error handler | auto-compensates the Sub-Process's completed children (§10.7 presumed abort) | designed-for (§2.9) — errors keep the decided fault-propagation semantics meanwhile |
| Attribute name | `waitForCompletion` (§10.4.5) | the model aligns on the spec's name — the stub's divergent field naming is corrected by the landing SRD |

## 3. Standard grounding

| Claim | Source |
|---|---|
| Trigger = throw Compensation Intermediate/End; `activityRef` targeted vs default-context scope-wide | [compensation.md](../bpmn-spec/semantics/compensation.md) (§13.5.5); [conformance.md](../bpmn-spec/conformance.md) positions row |
| No in-flow Compensation Intermediate Catch; catch = Boundary or Event-Sub-Process start only | [conformance.md](../bpmn-spec/conformance.md); the model stub's position notes |
| Handler forms: boundary + `Association` + `isForCompensation` activity; compensation event sub-process | [compensation.md](../bpmn-spec/semantics/compensation.md) (§13.5.5, §13.2.2) |
| Eligible at `Completed`, snapshot captured then; eligibility ends with the enclosing scope | [ADR-006 v.4](ADR-006-events-and-subscriptions.md) §2.3 (the compensation-boundary row); [compensation.md](../bpmn-spec/semantics/compensation.md) |
| Reverse-order compensation; dependency inversion; MI parallel/sequential rules | [compensation.md](../bpmn-spec/semantics/compensation.md) (§13.5.5); [multi-instance.md](../bpmn-spec/semantics/multi-instance.md) §Compensation |
| Presumed abort — only completed work compensates; error-driven default compensation for handler-less Sub-Processes | [compensation.md](../bpmn-spec/semantics/compensation.md) (§10.7) |
| `waitForCompletion: boolean = true` | [event-definitions.md](../bpmn-spec/elements/event-definitions.md) (§10.4.5) |
| Terminate runs no compensation; terminate interrupts `Compensating` | [ADR-006 v.4](ADR-006-events-and-subscriptions.md) §2.2; [event-handling.md](../bpmn-spec/semantics/event-handling.md) (§13.5.6) |
| Cancel/Transaction: cancel compensates completed inner work; Cancel only in Transaction | [event-handling.md](../bpmn-spec/semantics/event-handling.md) (§10.7, boundary table); [compensation.md](../bpmn-spec/semantics/compensation.md) |
| `Completed → Compensating → Compensated/Failed/Terminated` lifecycle | [compensation.md](../bpmn-spec/semantics/compensation.md) (§13.5.5 state table); [ADR-013 v.2](ADR-013-instance-observability.md) reserved slots |
| Direct-resolution reach (never hub-published) | [ADR-006 v.4](ADR-006-events-and-subscriptions.md) §2.1 strategy table |

## 4. Alternatives considered

- **Hub-delivered compensation (the Signal pattern).** Rejected: ADR-006 §2.1
  classifies Compensation as direct resolution — there is no waiting subscriber
  to deliver to; the catch side is a record of the past, not a live wait. A hub
  round-trip would add an async hop to what is a loop-local lookup.
- **Arming the compensation boundary as a live watcher (the ADR-018 pattern).**
  Rejected — contradicts ADR-006 §2.3's decided row (not a live subscription);
  a watcher over a completed activity has no window to watch.
- **Deriving compensation order from the graph at throw time** (invert
  sequence-flow/data dependencies). Rejected: the
  recorded completion order is simpler, race-free (single-writer ledger), and
  is itself the ground truth the spec's dependency rules approximate — what
  actually completed after what.
- **Snapshot-free compensation** (handlers read live data). Rejected —
  contradicts §13.5.5's explicit snapshot/restore semantics; a handler
  compensating against mutated state undoes the wrong thing.
- **Engine-wide `compensate-on-terminate` switch.** Rejected by ADR-006's
  constraint; per-process registration scope chosen (§2.8).
- **Ledger as a separate global registry keyed by activity id** (instead of
  scope-owned with child folding). Rejected: eligibility is scope-bounded by
  ADR-006 §2.3 — a scope-owned ledger makes the "discard when the enclosing
  scope finishes" rule structural instead of policed.

## 5. Consequences

**Positive:** the last #90 event behavior lands on the same loop-owned,
scope-structured machinery as Error/Escalation; the ledger + snapshot mechanism
is exactly what the future Transaction (#91) and default-recursion work
consume; ADR-013's reserved lifecycle slots get filled.

**Negative / cost:** the completion ledger and snapshots hold memory
proportional to completed compensable work for the life of the enclosing scope
— the price §13.5.5 demands; models without compensation handlers pay nothing
(no handler ⇒ no ledger entry). Snapshot capture adds a value-copy at
`Completed` for compensable activities only.

**Follow-ups this conception sets up:** default recursive compensation (§2.3),
`compensate-on-terminate` activation (§2.8), Transaction/Cancel (#91, §2.9), MI
compensation (ADR-025 §2.10).

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-20 | Ruslan Gabitov | Draft conception. Fulfills ADR-006 v.4 §2.2/§2.3's explicit deferral: compensation handling = a loop-owned, scope-structured **completion ledger** (ordered compensable completions + per-entry **data snapshots** captured at `Completed`, folded child→parent at scope completion, discarded when the enclosing scope finishes) resolved **directly** by a throw Compensation Event — `activityRef`-targeted or scope-wide in **reverse completion order**, `waitForCompletion` parking the thrower; handlers = the boundary + `Association`/`isForCompensation` pair or the compensation Event Sub-Process, never hub-armed; unresolved throws logged (never silent, never a fault); `Completed → Compensating → Compensated/Failed/Terminated` fills ADR-013's reserved slots (handler failure = a real Error-chain fault); the **ledger's own lifecycle is observable** (recorded/`Eligible` → `Folded`* → consumed \| `Discarded`), making the observer stream the ledger's audit log. **Designed-for:** recursive default compensation of handler-less Sub-Processes; error-driven default compensation (§10.7 presumed-abort auto-sweep on a handler-less Sub-Process error); `compensate-on-terminate` as a per-process registration option (off by default). **Out of scope:** Cancel/Transaction (rides #91 on this machinery), Call Activity cross-instance compensation. Standard-grounded against §13.5.5/§10.7/§10.4.5/§13.5.6 via the vendored extract. Implementation rides the accompanying SRD. |
