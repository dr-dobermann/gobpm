# ADR-028 — Transaction Sub-Process: ACID-like abort by Cancel

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-22 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-023 v.3](ADR-023-sub-process-and-call-activity.md) (the Sub-Process execution-scope model this varies; §2.8 keeps the Transaction "designed-for" under #91 — this ADR fulfils it), [ADR-026 v.1](ADR-026-compensation-events.md) §2.2/§2.4 (the completion ledger + reverse-order scope-wide sweep a cancel consumes), [ADR-018 v.1](ADR-018-boundary-events-and-activity-interruption.md) §2.7 (the boundary trigger scope that deferred **Cancel** — this ADR un-defers it for Transactions), [ADR-006 v.4](ADR-006-events-and-subscriptions.md) (the Cancel event definition + the direct-resolution class), [ADR-013 v.2](ADR-013-instance-observability.md) (the `KindScope`/`PhaseCanceled` fact the abort reports; the scope-phase set is open), [ADR-001 v.6](ADR-001-execution-model.md) §4 (the single-writer loop that owns all cancel/compensation state) |

The Transaction Sub-Process is the last live piece of the advanced-sub-process
epic (#91): a Sub-Process with **ACID-like** semantics whose distinguishing
feature is a coordinated **abort** — on cancellation it undoes its
already-completed work and tears down what is still running, then hands control
out through a dedicated boundary. Everything it needs already exists: the
completion **ledger** and reverse-order compensation **sweep** (ADR-026), the
scope-wide **cancel** primitive (ADR-023 §2.5), and the **Cancel** event
definition (ADR-006). This ADR decides how they compose into a transaction abort.

## 1. Context & problem

A plain Sub-Process (ADR-023) runs its inner graph in a nested execution scope
and completes when the scope drains. BPMN adds a **Transaction** variant
([sub-processes.md](../bpmn-spec/semantics/sub-processes.md) §Transaction,
§10.7): execution is identical to a plain Sub-Process **except** for one new
behavior — reaching a **Cancel End Event** inside the Transaction triggers a
transaction **abort**, and a **Cancel intermediate boundary event** attached to
the Transaction is the route control takes out on that abort. The spec defines
abort as: *terminate all running Activities and compensate all successfully
completed Activities* (§10.7), after which the Transaction leaves abnormally
through its Cancel boundary.

The pieces are all present but unconnected:

- **`CancelEventDefinition`** exists and is a valid **End Event** trigger; a
  Cancel **boundary** was deferred (ADR-018 §2.7 scoped boundaries to the 0.1.0
  triggers, leaving Cancel/Multiple for later).
- The **completion ledger** + the reverse-order **scope-wide sweep** (ADR-026
  §2.2/§2.4) already compensate all completed activities of a scope — the exact
  "compensate all successfully completed" half.
- The scope-wide **cancel** (ADR-023 §2.5) already terminates every running
  track under a scope path — the "terminate all running" half.
- The engine has **no Transaction type**, no notion that a Cancel is only legal
  inside one, and no handler that sequences the abort.

The problem this ADR solves: **define the Transaction variant and the abort it
performs, composing the landed ledger/sweep/cancel primitives — without a new
undo mechanism.**

## 2. Decision

### 2.1 A Transaction is a Sub-Process variant, not a new node

A Transaction Sub-Process **is** an embedded Sub-Process (ADR-023 §2.2) carrying
an `isTransaction` marker — the same shape as the Event Sub-Process's
`triggeredByEvent` marker, a boolean discriminator on the one Sub-Process type,
not a distinct node kind. Its **normal** execution — scope open, seed, drain,
boundary events, data, nested composites — is **plain Sub-Process semantics,
unchanged** ([sub-processes.md](../bpmn-spec/semantics/sub-processes.md):
"the execution semantics of the Transaction Sub-Process itself follow the plain
Sub-Process semantics; the Cancel behavior is the distinguishing feature"). The
marker changes exactly two things: it **permits** Cancel (End + boundary) that a
plain activity forbids (§2.6), and it names the **scope a cancel aborts** (§2.3).

A Transaction reaches one of three terminal outcomes:

- **Success** — the inner graph drains normally; the Transaction completes and
  the token leaves on its **normal** outgoing flow (plain Sub-Process behavior).
- **Cancellation** — a Cancel End Event is reached inside; the Transaction
  **aborts** (§2.3) and control leaves through the **Cancel boundary** (§2.4).
- **Hazard** — an error escapes that is neither caught nor a clean cancel; this
  is the **existing** error path (ADR-006 §2.6 scope-chain propagation),
  unchanged. This ADR does not add hazard handling.

### 2.2 Cancel is a direct-resolution event, handled on the loop

A Cancel is **not** a broadcast. The standard classifies Cancellation as a
**direct resolution** event
([event-handling.md](../bpmn-spec/semantics/event-handling.md): "Trigger
directed at a **specific** Process or Activity instance") and permits it **only
on a Transaction Sub-Process** (always interrupting). So a Cancel End Event does
**not** go out through the EventHub the way a Signal or Message throw does —
there is exactly one recipient (the enclosing Transaction), known statically.

The engine therefore handles a Cancel End Event **on the single-writer loop**,
the same locus a **scoped Terminate** already uses (ADR-023 §2.5 —
`terminateScope` resolves a Terminate End Event against its enclosing scope
without touching the hub). The loop recognizes the Cancel trigger, resolves the
**nearest enclosing Transaction scope**, runs the abort against it, and resumes
the Transaction host on its Cancel boundary. No subscription, no correlation, no
cross-goroutine delivery — a local, deterministic resolution.

*(A Cancel End Event that is not enclosed by a Transaction is a modeling error,
rejected at validation — §2.6 — so the loop's resolution never fails to find a
target.)*

### 2.3 The abort sequence — compensate completed, then terminate running

On a Cancel, the Transaction scope aborts in a **fixed order**, and the order is
load-bearing:

1. **Compensate the completed activities** — run the ADR-026 reverse-order
   scope-wide sweep over the Transaction scope's completion **ledger**. Each
   successfully-completed inner activity that has a compensation handler runs it,
   against its captured data snapshot (ADR-026 §2.5), newest-completed first.
2. **Terminate the still-running activities** — tear down every track still live
   under the Transaction scope (the ADR-023 §2.5 scope-wide cancel).
3. **Leave through the Cancel boundary** — the Transaction host resumes on the
   Cancel boundary's outgoing flow (§2.4); the Transaction scope closes.

**Why compensate-before-terminate (the ledger-survival rule).** The scope-wide
cancel primitive **discards the completion ledger** — cancelled work is not
compensable, so a plain cancel drops it. But a transaction abort must compensate
exactly that ledger. So the abort **must sweep the ledger first, while it is
intact**, and only then cancel the residual running tracks. Running activities
were never completed, so they are not in the ledger and not compensated — they
are simply terminated. Sequencing compensation ahead of the teardown is the
whole correctness content of the abort; the two primitives are otherwise reused
verbatim.

The compensation sweep is **synchronous with respect to the abort**: the
Transaction does not leave through its Cancel boundary until every compensation
handler has run (the sweep's completion barrier, ADR-026 §2.4). This preserves
"compensate all completed **before** control leaves" — the ACID-like guarantee.

### 2.4 The Cancel boundary — the modeled exit (un-defers ADR-018 §2.7)

Control leaves an aborting Transaction through a **Cancel intermediate boundary
event** attached to it
([sub-processes.md](../bpmn-spec/semantics/sub-processes.md): "A Cancel
intermediate boundary event MAY be attached to the Transaction Sub-Process —
control leaves through it on cancellation"). This ADR **un-defers** the Cancel
boundary trigger that ADR-018 §2.7 left for later — **narrowly**: a Cancel
boundary is permitted **only on a Transaction Sub-Process**, is **always
interrupting** (Cancel has no non-interrupting form,
[event-handling.md](../bpmn-spec/semantics/event-handling.md): "**always
`true`**"), and is the sole legal home for a Cancel boundary.

Unlike an ordinary boundary event, the Cancel boundary is **not armed as a hub
waiter** — consistent with §2.2, the Cancel never reaches the hub. It is a
**model-declared exit**: the loop, having run the abort (§2.3), routes the host
onto the boundary's outgoing flow directly. The boundary's role is the diagram's
— it makes the abort's exit **visible and routable** — while the loop drives the
resolution. A Transaction with **no** Cancel boundary that aborts simply
completes abnormally-and-locally with no outgoing token (the parent continues
past it), the same shape a boundary-less scoped Terminate takes today.

### 2.5 What a cancel does *not* do

- It does **not** propagate as an error up the scope chain — a cancel is a
  *clean, modeled* abort, not a fault. The parent process is unaffected beyond
  the token that leaves (or does not leave) the Cancel boundary.
- It does **not** compensate across the Transaction boundary — only the
  Transaction's **own** completed inner activities are swept. A completed
  Transaction that is *later* compensated from outside is the ordinary ADR-026
  path (the Transaction, as a completed activity, may itself carry a compensation
  handler in its parent scope), unchanged here.
- It does **not** re-enter a completed **Call Activity** — cross-instance
  compensation stays out of scope (ADR-026 §2.9), a Call Activity never enters a
  ledger.

### 2.6 Shape rules (validation)

Enforced at model construction / registration, fail-fast:

- A **Cancel End Event** is legal **only** inside a Transaction Sub-Process's
  graph (directly or transitively within its scope). Elsewhere → rejected.
- A **Cancel boundary event** attaches **only** to a Transaction Sub-Process, and
  is **always interrupting**. On any other activity → rejected.
- A Transaction Sub-Process **may** carry at most the conventional single Cancel
  boundary; **nested Transactions are out of scope** (§2.8).
- The Transaction marker is exclusive of the **Event Sub-Process** marker (a
  handler is not itself a transaction).

These mirror the Event Sub-Process shape rules (ADR-023 §2.10) — a marker that
*permits* a construct otherwise forbidden, checked once at build time.

### 2.7 `method` = `compensate` only

BPMN's Transaction `method` attribute (`compensate` | `store` | `image`) selects
the abort protocol. Only **`compensate`** — undo-by-compensation-handlers — is a
process-level mechanism the engine can realize; **`store`** and **`image`** are
protocol/transaction-manager-level (WS-AT / XA-style) resource-manager
coordination with no in-engine representation and no ecosystem support (Camunda
implements only `compensate`). The engine supports **`compensate`** (the
default) and treats `store`/`image` as a **non-goal** (§2.8) — an explicit engine
choice, consistent with the conformance-subset posture and the Camunda alignment.

### 2.8 Designed-for & out of scope

- **`method = store | image`** — protocol-coordinated abort; **out of scope**
  (§2.7), an engine choice.
- **Nested Transactions** — a Transaction inside a Transaction; **out of scope**
  now (the abort-resolution needs the *nearest* enclosing Transaction, which is
  well-defined, but nested-transaction cancellation ordering and boundary
  interaction warrant their own conception). Rejected at validation.
- **Error-driven default (presumed-abort) auto-compensation** — a Transaction
  suffering an *uncaught error* auto-compensating before the error propagates
  (§10.7's second half) is the **same** ledger sweep fired from the error path;
  it is **designed-for** here but decided with ADR-026 §2.9's error-path
  co-design, not landed in this ADR. Until then a Transaction's uncaught-error
  behavior is the current conformant-subset error propagation (ADR-006 §2.6),
  called out as an engine note (§2.9).
- **Durable transaction recovery** (rehydrating an in-flight Transaction across a
  crash) — rides ADR-009's persistence line; out of scope.

### 2.9 Engine notes (deviations & choices)

- **No `store`/`image` method** (§2.7) — deliberate; use compensation handlers.
- **Uncaught-error auto-compensation is not automatic** (§2.8) — an uncaught
  error in a Transaction propagates per ADR-006 §2.6 today; the presumed-abort
  auto-sweep is a follow-up. A model wanting undo-on-error attaches an explicit
  error boundary that throws Compensation.
- **Cancel is loop-local, never hub-visible** (§2.2) — an engine mechanism
  reconciling a direct-resolution event with the single-writer loop; invisible to
  a modeler.

## 3. Standard grounding

- **Transaction variant & Cancel** — [sub-processes.md](../bpmn-spec/semantics/sub-processes.md)
  §Transaction; [end-events.md](../bpmn-spec/semantics/end-events.md) §Cancel End
  Event ("Abnormal Sub-Process termination + Transaction abort; control leaves via
  Cancel boundary event"; "Not valid at Process level").
- **Abort = terminate running + compensate completed** — §10.7 (the Cancel /
  Transaction rules), surfaced in
  [event-handling.md](../bpmn-spec/semantics/event-handling.md) §Cancel ("Only
  allowed in a Transaction Sub-Process. Cancels the Sub-Process and aborts the
  associated Transaction").
- **Cancel is direct-resolution, Transaction-only, always interrupting** —
  [event-handling.md](../bpmn-spec/semantics/event-handling.md) (Cancel: "yes —
  only on Transaction Sub-Process"; cancelActivity "always `true`").
- **`method` attribute** — [activities.md](../bpmn-spec/elements/activities.md)
  (`method` String 0..1).
- Where gobpm narrows the standard (`store`/`image`, nested, error-auto-sweep),
  it is called out as an explicit engine note (§2.9), never as if the standard
  required it.

## 4. Alternatives considered

- **Cancel via the EventHub (broadcast + boundary-catch), reusing the ordinary
  boundary machinery.** Rejected: Cancel is a direct-resolution event with a
  single, statically-known recipient; broadcasting it is a semantic mismatch, and
  the boundary-catch path's interrupting teardown (`cancelHostScope`) **discards
  the ledger** the abort must compensate (§2.3). Loop-local resolution both fits
  the standard's class and preserves the ledger.
- **A distinct `Transaction` node type** (not a Sub-Process marker). Rejected:
  the standard is explicit that a Transaction's execution *is* a Sub-Process's;
  a separate type would duplicate the entire scope/drain/boundary/data model for
  one added behavior. A marker (as with the Event Sub-Process) is the minimal,
  truthful shape.
- **Terminate-then-compensate order** (cancel the scope first, then compensate).
  Rejected: the scope-wide cancel discards the completion ledger, so compensation
  would have nothing to sweep — the order is forced by the ledger-survival rule
  (§2.3).
- **A separate transaction-cancel ledger/sweep**, distinct from compensation.
  Rejected: ADR-026's sweep already *is* "run the completed activities' undo in
  reverse order"; a transaction-cancel is that sweep with a different **trigger**,
  not different **machinery** (ADR-026 §2.9 foresaw exactly this consumer).

## 5. Consequences

- The engine gains the Transaction Sub-Process — the fifth and final BPMN
  activity-composition variant (plain, Event-Sub, Call Activity, loop/MI, now
  Transaction) — closing epic #91 and the advanced-sub-process conformance row.
- The Cancel boundary trigger becomes available, **narrowly** (Transaction-only);
  ADR-018 §2.7's deferral is closed by an in-place link annotation (not a bump),
  and ADR-023 §2.9's "designed-for" note is likewise annotated as fulfilled.
- No new undo mechanism, no new event-delivery path: the abort is a composition
  of landed primitives (ledger sweep + scope cancel + loop-local resolution), so
  the change surface is the marker, the two shape rules, the un-deferred boundary
  trigger, and one abort-sequencing handler on the loop.
- The presumed-abort error auto-sweep (§2.8) remains a designed-for follow-up,
  keeping the error path's cancellation ordering a single future co-design rather
  than two competing ones.
- The accompanying SRD lands it against the current substrate; names of code
  symbols are deliberately absent here — that grounding belongs to the SRD.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-22 | Ruslan Gabitov | Initial draft — the Transaction Sub-Process as a Sub-Process variant (`isTransaction`) whose Cancel behavior is the distinguishing feature: a Cancel End Event triggers a **loop-local, direct-resolution** abort (Cancel never reaches the hub), sequenced **compensate-completed (the ADR-026 reverse-order ledger sweep) → terminate-running (the ADR-023 scope cancel) → leave via the Cancel boundary**, the order forced by the ledger-survival rule (the scope cancel discards the ledger the abort must sweep). Un-defers the Cancel **boundary** trigger (ADR-018 §2.7) narrowly — Transaction-only, always interrupting, a model-declared exit not a hub waiter. `method = compensate` only (`store`/`image` non-goal); nested Transactions and error-driven presumed-abort auto-compensation designed-for but out of scope. Reuses the landed ledger/sweep/cancel verbatim; no new undo mechanism. Standard-grounded against §10.7 / sub-processes / end-events / event-handling. |
| v.1 | 2026-07-24 | Ruslan Gabitov | **Accepted** — landed by the accompanying SRD across M1 (model), M2 (runtime abort), M3 (e2e + example): the `WithTransaction()` marker, the `evTransactionCancel` loop-local resolution, the compensate → terminate → Cancel-boundary sequence via the `scopeEntry.aborting` flag + wait-mode sweep, and the always-interrupting Cancel boundary as a model-declared exit. Status flip only, no conception change (no version bump). Corrections at acceptance: the Refines pins to ADR-023 **§2.8** (the "designed-for" bullet, not §2.9) and ADR-013 observability to the **`KindScope`/`PhaseCanceled`** fact the abort reuses (no new phase — Option A, no ADR-013 flip). |
