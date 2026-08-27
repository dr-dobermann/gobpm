# ADR-028 — Transaction Sub-Process: ACID-like abort by Cancel

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.2 |
| Date | 2026-08-26 |
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

The pieces were all present but unconnected when v.1 was written (the
v.1 landing connected them; this section is that baseline):

- **`CancelEventDefinition`** exists and is a valid **End Event** trigger; a
  Cancel **boundary** was deferred (ADR-018 §2.7 scoped boundaries to the 0.1.0
  triggers, leaving Cancel/Multiple for later).
- The **completion ledger** + the reverse-order **scope-wide sweep** (ADR-026
  §2.2/§2.4) already compensate all completed activities of a scope — the exact
  "compensate all successfully completed" half.
- The scope-wide **cancel** (ADR-023 §2.5) already terminates every running
  track under a scope path — the "terminate all running" half.
- The engine had **no Transaction type**, no notion that a Cancel is only legal
  inside one, and no handler that sequences the abort.

The problem this ADR solves: **define the Transaction variant and the abort it
performs, composing the landed ledger/sweep/cancel primitives — without a new
undo mechanism.**

## 2. Decision

### 2.1 A Transaction is a Sub-Process variant, not a new node

A Transaction Sub-Process **is** an embedded Sub-Process (ADR-023 §2.2) that
carries **transaction characteristics**: a value object composed into the one
Sub-Process type — the shape ADR-025 §2.1 gives iteration — not a distinct node
kind and not a wrapper around one. The characteristics hold what is specific to
a transaction, the abort **method** and the coordination **protocol** (§2.7);
the unit that executes the node reads them when the scope opens, as the
iteration runtime reads loop characteristics, and performs one extra step
before running the node: it **binds** the execution to them — resolves
`method` to the coordinator that will abort this scope and hands that
coordinator `protocol` — so the node then runs as a plain Sub-Process whose
abort has an owner. With compensate the only coordinator, the bind resolves to
the engine's own §2.3 sequence and changes nothing observable; it is the
point at which a host-registered coordinator later takes over (§2.8), with no
change to the executing unit.

The binding reaches the **inner nodes** too. A node executed inside a bound
scope is executed under that scope's binding: its own executing unit reports
the node's start, completion and failure to the scope's coordinator, so the
coordinator learns which work belongs to the transaction from the nodes
themselves — not from a transaction handle threaded through the operation,
data-store and worker seams. For compensate this is what already happens: the
completion **ledger** ADR-026 keeps per scope is the coordinator being told
"this node completed", and the §2.3 sweep is that coordinator acting on it.
"Is a transaction" is the derived question
*are characteristics present*, and a Sub-Process that carries none is bound to
nothing and untouched by everything below.

Its **normal** execution — scope open, seed, drain, boundary events, data,
nested composites — is **plain Sub-Process semantics, unchanged**
([sub-processes.md](../bpmn-spec/semantics/sub-processes.md): "the execution
semantics of the Transaction Sub-Process itself follow the plain Sub-Process
semantics; the Cancel behavior is the distinguishing feature"). Today the
characteristics change exactly two things: they **permit** Cancel (End +
boundary) that a plain activity forbids (§2.6), and they name the **scope a
cancel aborts** (§2.3) — the scope the bind step gives an owner.

That scope is why the characteristics are composed into the Sub-Process rather
than wrapped around it: the abort sweeps *its* ledger and cancels *its* tracks,
a Cancel End asks whether it sits inside *that* scope, and the Cancel boundary
attaches to *that* host (§2.4). A wrapping node would split the node in the
graph from the scope that aborts, and every one of those rules would have to
unwrap it.

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

### 2.7 `method` selects a coordinator; `protocol` is handed to it, never read

BPMN's Transaction has two own attributes beyond the Sub-Process it extends —
`method` and `protocol` ([activities.md](../bpmn-spec/elements/activities.md)
§Transaction: both `String`, `0..1`). The schema types `method` as
`tTransactionMethod` — a union of the three tokens `##Compensate`, `##Store`,
`##Image` and **any URI**, defaulting to `##Compensate` (OMG `Semantic.xsd`,
`tTransaction`); the metamodel spells the three `compensate`, `store`,
`image`. `protocol` is a metamodel attribute the schema does not declare at
all (`tTransaction` extends `tSubProcess` with `method` alone), so a
schema-strict tool never writes it and a metamodel-driven modeller may.

**`method` is an open identifier naming the coordinator that aborts.** The
schema's `anyURI` member makes the value set open by design, and the engine
keeps it open: `method` is a typed identifier, not an enumeration. The one
coordinator the engine itself provides is **compensate** — undo by running the
completed activities' compensation handlers, the §2.3 sequence — and it is
the default: a Transaction that names no method is a compensate transaction,
and the schema token `##Compensate` and the metamodel spelling `compensate`
both denote it. **store**, **image**, or a URI name coordination the engine
does not perform — transaction-manager-level (WS-AT / XA-style) resource
management for the first two, a protocol-defined method for the third. They
are not refused by the *model*, which carries any identifier: they are refused
at **registration**, where a process is checked against the engine it will run
in, with the reason *no coordinator is registered for this method*. Until a
coordination seam exists (§2.8) that is every method but compensate, and the
refusal says to model the undo as compensation handlers; once a host can
register coordinators, the same check passes for whatever it registered.

It is the **model** that reads the attribute's forms and holds the value, and
a converter maps the attribute verbatim onto it: ADR-024 §2.16's rule applied
here — a document format keeps no second copy of the value set to drift from.

**`protocol` is an opaque string handed to the coordinator.** The vendored
extract defines it no further, and nothing in the engine reads it: with
compensate the only built-in coordinator, no consumer exists today, and a
registered coordinator is the only thing that ever will. The model **carries**
it beside `method` — so a document stating it loads whole and a round-trip
re-emits it, the model-only obligation lanes and artifacts meet (the model
holds what the diagram states) — and execution passes it through untouched. A
`protocol` may be set only with transaction characteristics; on a plain or
Event Sub-Process it is a construction error, because the metamodel places the
attribute on Transaction alone.

### 2.8 Designed-for & out of scope

- **A transaction-coordination seam** — what a coordinator *is*: a
  host-registered coordinator per `method` (a thresher-level registry beside
  the script, rule and worker seams of ADR-002), the session the bind step
  (§2.1) opens on it — committed when the scope drains, aborted inside §2.3 —
  and what it does with the node reports the inner bindings deliver (§2.1)
  — the work it must undo arrives as the nodes' own start/complete/fail
  reports, the ledger generalized. **Designed-for**: §2.1 decides *where* it
  attaches (the bind step, on the Transaction node and on every node inside
  it) and §2.7 *what selects it* (the open identifier, the carried
  `protocol`); the coordinator contract itself — what a session is, what a
  node report carries, how a non-compensate abort composes with §2.3 — is
  decided in its own conception, not here.
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

- **Transaction is a composition, not a subclass** (§2.1) — the standard
  derives `Transaction` from `SubProcess`; the engine composes characteristics
  into the one Sub-Process type, as it does iteration (ADR-025). Same
  observable shape, one node type.
- **No `store`/`image` coordination** (§2.7) — deliberate until a seam exists
  (§2.8); use compensation handlers.
- **`protocol` is inert** (§2.7) — carried for loading, round-trip and a
  future coordinator, never read by execution, and refused without
  transaction characteristics.
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
- **`method` and `protocol` attributes** —
  [activities.md](../bpmn-spec/elements/activities.md) §Transaction (`protocol`
  String 0..1, `method` String 0..1; the vendored metamodel
  `bpmn-spec/scripts/bpmn-moddle.json`, `Transaction`, carries both). The
  schema — OMG `Semantic.xsd`, `tTransaction` — declares `method` alone, typed
  `tTransactionMethod` (union of the tokens `##Compensate` / `##Image` /
  `##Store` and `xsd:anyURI`, default `##Compensate`), and no `protocol`;
  §2.7's two-spelling reading and its carrying of a schema-less attribute
  both follow from that split.
- Where gobpm narrows the standard (`store`/`image`/URI methods, nested,
  error-auto-sweep, an uninterpreted `protocol`),
  it is called out as an explicit engine note (§2.9), never as if the standard
  required it.

## 4. Alternatives considered

- **Cancel via the EventHub (broadcast + boundary-catch), reusing the ordinary
  boundary machinery.** Rejected: Cancel is a direct-resolution event with a
  single, statically-known recipient; broadcasting it is a semantic mismatch, and
  the boundary-catch path's interrupting teardown (`cancelHostScope`) **discards
  the ledger** the abort must compensate (§2.3). Loop-local resolution both fits
  the standard's class and preserves the ledger.
- **Enlisting through the host seams** (a transaction handle in the context
  every operation, data store and worker receives). Rejected: it makes every
  seam transaction-aware and every host implementation responsible for
  reporting; binding the inner nodes' executing units (§2.1) gives the
  coordinator the same knowledge from inside the engine, once, and is what
  the ledger already does for compensate.
- **Binding at registration instead of at execution.** Rejected: registration
  can only check that a coordinator *exists* for a method (§2.7), and does;
  the binding is per scope instance — the session a coordinator opens belongs
  to one running Transaction — so it is the executing unit's step, taken when
  the scope opens, as it is for iteration.
- **A boolean marker** (v.1's `isTransaction`). Superseded in v.2: once the
  Transaction has attributes of its own and, later, a coordinator, a flag
  scatters them as loose Sub-Process fields; a characteristics object owns
  them, validation reads one value, and the executing unit asserts one type —
  the ADR-025 shape.
- **A one-member `method` enumeration** (compensate only, closed). Rejected:
  the schema's `anyURI` member makes the set open, and closing it would force a
  model change the day a coordinator is registered; an open identifier
  validated at registration costs nothing now and nothing later.
- **A decorator node wrapping the Sub-Process.** Rejected: the transaction
  scope *is* the Sub-Process scope (§2.1), and a wrapper splits the node from
  the scope every abort and shape rule reasons about.
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
- v.2 changes the model's shape, not the abort: the Sub-Process carries one
  transaction value object instead of a flag, a schema-valid document loads,
  no converter keeps a private value table, and the coordination seam (§2.8)
  needs no further model change. Re-emission waits on the export half of
  ADR-024's fence (§7 there): until a Sub-Process can be exported at all, the
  round-trip obligation is met on the model side only.
- The accompanying SRD lands it against the current substrate; names of code
  symbols are deliberately absent here — that grounding belongs to the SRD.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-22 | Ruslan Gabitov | Initial draft — the Transaction Sub-Process as a Sub-Process variant (`isTransaction`) whose Cancel behavior is the distinguishing feature: a Cancel End Event triggers a **loop-local, direct-resolution** abort (Cancel never reaches the hub), sequenced **compensate-completed (the ADR-026 reverse-order ledger sweep) → terminate-running (the ADR-023 scope cancel) → leave via the Cancel boundary**, the order forced by the ledger-survival rule (the scope cancel discards the ledger the abort must sweep). Un-defers the Cancel **boundary** trigger (ADR-018 §2.7) narrowly — Transaction-only, always interrupting, a model-declared exit not a hub waiter. `method = compensate` only (`store`/`image` non-goal); nested Transactions and error-driven presumed-abort auto-compensation designed-for but out of scope. Reuses the landed ledger/sweep/cancel verbatim; no new undo mechanism. Standard-grounded against §10.7 / sub-processes / end-events / event-handling. |
| v.1 | 2026-07-24 | Ruslan Gabitov | **Accepted** — landed by the accompanying SRD across M1 (model), M2 (runtime abort), M3 (e2e + example): the `WithTransaction()` marker, the `evTransactionCancel` loop-local resolution, the compensate → terminate → Cancel-boundary sequence via the `scopeEntry.aborting` flag + wait-mode sweep, and the always-interrupting Cancel boundary as a model-declared exit. Status flip only, no conception change (no version bump). Corrections at acceptance: the Refines pins to ADR-023 **§2.8** (the "designed-for" bullet, not §2.9) and ADR-013 observability to the **`KindScope`/`PhaseCanceled`** fact the abort reuses (no new phase — Option A, no ADR-013 flip). |
| v.2 | 2026-08-26 | Ruslan Gabitov | **The marker becomes transaction characteristics (§2.1), and the Transaction's own attributes get a home in it (§2.7).** A value object composed into the one Sub-Process type — the ADR-025 shape iteration already has — replaces the bare `isTransaction` boolean; the executing unit reads it when the scope opens and **binds** the execution to a coordinator by `method`, handing it `protocol`, and every inner node runs under that binding, its executing unit reporting start/completion/failure to the coordinator — the ADR-026 ledger generalized; today always the built-in compensate sequence, later a host-registered one — and "is a transaction" is derived. `method` is an **open identifier** (the schema's `tTransactionMethod` admits any URI): compensate is the built-in and default coordinator, read in both standard forms — the schema token `##Compensate` and the metamodel spelling `compensate` — and any other method is refused at **registration** as *no coordinator registered*, not by the model; owning the parse in the model is ADR-024 §2.16's rule and retires the converter's private value table, which read only the lowercase form and refused a schema-valid file. `protocol` — in the metamodel, absent from the schema — is **carried** as an opaque string for loading, round-trip and a future coordinator, never read by execution, refused without characteristics. §2.8 records the **coordination seam** (registry, session, enlistment) as designed-for and deferred to its own conception; §2.9 notes composition-not-subclass; §4 adds the rejected boolean, closed enum and wrapper node. Abort semantics unchanged. Closes the transaction half of the coverage question filed as #324. |
| v.2 | 2026-08-27 | Ruslan Gabitov | **Accepted** — landed by SRD-095 across M1 (the characteristics object and options), M2 (the registration check), M3 (the scope binding and `transaction_method` on the abort's facts), M4 (the importer mapping both attributes verbatim), M6 (the example). On the way, FR-8 fixed a pre-existing checkpoint defect the restore test exposed: a document taken at a wait right after a compensable activity dropped its ledger entry. Export of a Sub-Process, and so re-emission of the two attributes, still waits on ADR-024's export slice. |
