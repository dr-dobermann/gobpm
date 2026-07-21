# ADR-025 — Activity Iteration: Standard Loop & Multi-Instance

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.2 |
| Date | 2026-07-21 |
| Owner | Ruslan Gabitov |
| Refines | [SAD-001 v.1](SAD-001-vision-and-architecture.md) §5 / §15.3, [ADR-023 v.2](ADR-023-sub-process-and-call-activity.md) (the execution-scope model this reuses), [ADR-018 v.1](ADR-018-boundary-events-and-activity-interruption.md) (boundary catch for thrown behavior events), [ADR-017 v.1](ADR-017-channel-based-event-processing.md) (the single-writer execution model §2.12 extends), [ADR-006 v.3](ADR-006-events-and-subscriptions.md) (event throwing/catching) |

> **Draft (v.2)** — decides how an activity marked with *loop characteristics* runs
> **more than once**: BPMN's Standard Loop (a condition-driven sequential loop)
> and Multi-Instance (a cardinality-driven fan-out, sequential or parallel, over
> a data collection). It is prescriptive and grounded in the BPMN 2.0 object
> model (§13.3.6–§13.3.7); the accompanying SRDs land it incrementally on the
> existing execution-scope substrate. Names of code symbols are deliberately
> absent — that grounding belongs to the SRDs.
>
> **v.2** adds §2.12: a **composite** looped activity iterates on the activity's
> own (off-loop) execution — an *iteration decorator* — rather than under control
> code run on the per-instance loop goroutine. This makes the §2.8 behavior throw
> an ordinary off-loop emit with a deterministic boundary catch (the v.1 landing
> could not implement it correctly), while keeping [ADR-017 v.1](ADR-017-channel-based-event-processing.md)'s
> single-writer invariant intact. The §2.1–§2.11 semantics are unchanged; only
> *who drives* composite iteration moves. The SRDs that landed the
> loop-goroutine-driven composite model are superseded and deleted when the
> decorator re-landing completes.

---

## 1. Context & problem

Today every activity in gobpm runs **exactly once** per token that reaches it.
BPMN 2.0 lets an activity carry *loop characteristics* that make it run
repeatedly without duplicating the node in the diagram. Two forms exist
(§13.3), both attached to any `Activity` — `Task`, `SubProcess`, or
`CallActivity`:

- **Standard Loop** (§13.3.6) — a structured `while`/`until` loop: run the
  activity, re-evaluate a boolean `loopCondition`, repeat. Purely sequential.
  Workflow pattern WCP-21 (Structured Loop).
- **Multi-Instance** (§13.3.7) — run the activity a *fixed* number of times
  (decided once at activation), either **sequentially** (one after another) or
  **in parallel** (all at once), typically **once per element of a collection**.
  Workflow patterns WCP-13/14 (Multiple Instances), WCP-34/36. This is the
  engine's answer to "do X for each line item in the order".

Both are core to the BPMN *Process Execution Conformance* target
([conformance.md](../../bpmn-spec/conformance.md), the project's scope). Mass
per-element processing is impossible to express without them.

The problem this ADR solves is **conceptual**, not mechanical: *what does it
mean for one activity to be many executions* — how instances are counted, how
each instance is isolated from its siblings, how per-instance data is split from
and re-assembled into a collection, when the whole thing is considered done, and
how progress can be observed from the boundary. The mechanics (which existing
runtime seam carries each part) are the SRDs' job.

### Object model (BPMN 2.0, verbatim from the vendored extract)

From [activities.md §StandardLoopCharacteristics / §MultiInstanceLoopCharacteristics /
§ComplexBehaviorDefinition](../../bpmn-spec/elements/activities.md) and
[multi-instance.md](../../bpmn-spec/semantics/multi-instance.md):

- `StandardLoopCharacteristics → LoopCharacteristics → BaseElement`:
  `testBefore` (Boolean, default `False`), `loopCondition` (Expression),
  `loopMaximum` (Integer).
- `MultiInstanceLoopCharacteristics → LoopCharacteristics → BaseElement`:
  `isSequential` (Boolean, default `False`), `behavior`
  (`MultiInstanceBehavior`, default `All`), `loopCardinality` (Expression),
  `loopDataInputRef` / `loopDataOutputRef` (ItemAwareElement refs), `inputDataItem`
  (DataInput), `outputDataItem` (DataOutput), `complexBehaviorDefinition`
  (0..*), `completionCondition` (Expression), `oneBehaviorEventRef` /
  `noneBehaviorEventRef` (EventDefinition refs).
- `ComplexBehaviorDefinition → BaseElement`: `condition` (FormalExpression),
  `event` (ImplicitThrowEvent).

`LoopCharacteristics` is the shared abstract base — the two concrete forms are
mutually exclusive on a given activity.

---

## 2. Decision

### 2.1 One family: loop characteristics as an activity marker

An activity's iteration is a **marker** it carries, not a new node type. The
abstract `LoopCharacteristics` gains two concrete forms —
`StandardLoopCharacteristics` and `MultiInstanceLoopCharacteristics` — and an
activity carries **at most one**. An activity with no loop characteristics runs
once, exactly as today (the change is strictly additive).

The marker is orthogonal to *what* the activity is: the same iteration model
wraps a Task, a Sub-Process, or a Call Activity. A looped Sub-Process runs its
whole body per iteration; a looped Task runs the task per iteration; a looped
Call Activity launches a child instance per iteration.

### 2.2 Each iteration is isolated; the mechanism fits the activity kind

The central decision: **each iteration of a looped activity runs in its own
isolated execution context**, so per-iteration state — the current element, a
per-iteration `loopCounter` — never bleeds across iterations. Isolation is the
invariant; the *mechanism* is the cheapest one that satisfies it for the
activity kind:

- **A leaf activity (Task)** iterates **in place**: the engine re-executes the
  activity once per pass, each pass in a **fresh execution frame**. A frame is
  already the per-execution data boundary (ADR-010), so a new frame per
  iteration *is* the isolation — no heavier construct is needed, and the
  activity's single outgoing flow is followed once, after the loop exits.
- **A composite activity (Sub-Process, Call Activity)** iterates by **re-opening
  its child scope per iteration** — the ADR-023 v.2 nested-scope open/drain/close
  lifecycle it already runs for its body. Sequential iteration = the scope for
  iteration *i+1* opens only after iteration *i*'s scope has drained and closed
  (the re-entry seam); the composite follows its single outgoing flow once, after
  the final iteration.

Both mechanisms share one lifecycle shape — run, test the continuation, repeat —
and both let a boundary event on the looped activity arm **once** and guard every
iteration (the desirable BPMN semantics: a boundary timer spans the whole loop).

**Parallel Multi-Instance is the exception that always needs a distinct
per-instance scope**, because its instances run *concurrently* and must not share
token or data state; each parallel instance therefore gets a scope with a
**distinct, stable identity** derived from the activity and the instance ordinal
(§2.5), so siblings are addressable and never collide. This ADR does not force
that heavier construct onto the sequential cases (Standard Loop, sequential MI),
where one-at-a-time execution already guarantees isolation.

Rationale for not giving a looped leaf Task a child scope: a Task is not a scope
container — a scope would mean seeding an empty inner graph and routing a
synthetic completion for isolation the fresh frame already provides.
Isolation-by-frame for leaves and isolation-by-scope for composites is one
uniform *principle* (per-iteration isolation) realized by two mechanisms, not two
competing models.

This subsection fixes the *mechanism*; **who drives it** for a composite activity
— the activity's own off-loop execution, not the per-instance loop goroutine — is
§2.12.

### 2.3 Standard Loop — a sequential condition-driven loop

A Standard-Loop activity runs its inner activity repeatedly **in sequence**,
one iteration at a time (by the §2.2 mechanism for its activity kind), governed
by:

- `loopCondition` — a boolean expression re-evaluated each pass. The loop
  continues while it is `true`.
- `testBefore` — `False` (default) → **post-tested** (`do…while`): run once,
  then test. `True` → **pre-tested** (`while`): test before each run, so zero
  iterations are possible.
- `loopMaximum` — an optional cap: when set, no more than that many iterations
  run regardless of the condition (a guard against runaway loops; unset =
  unbounded).

The loop exposes a per-iteration **`loopCounter`** (0-based) to expressions
inside the activity and to `loopCondition`. Standard Loop has **no** collection
data-flow, no parallelism, no completion condition, and no `behavior` — those
are Multi-Instance concepts. When the loop finishes (condition `false` or the
maximum reached), control flows out of the activity's outgoing sequence flow
once.

### 2.4 Multi-Instance — cardinality decided once at activation

A Multi-Instance activity computes its instance count **exactly once, at
activation** (§13.3.7), from one of two sources — the engine supports both:

- `loopCardinality` — an integer-valued expression, evaluated once.
- **Collection** — `loopDataInputRef` points at a collection-valued data item;
  the cardinality is that collection's element count.

The count is fixed for the activity's lifetime: adding elements to the source
collection mid-flight does not spawn more instances. If both a cardinality and a
collection are supplied, that is a modelling error surfaced at validation (they
are alternative cardinality sources, not composable).

### 2.5 Sequential vs. parallel

`isSequential` selects the execution shape:

- `True` — **sequential**: instance *i+1* begins only after instance *i* has
  completed (by the §2.2 mechanism for the activity kind). At most one instance
  runs at a time; ordering is the collection/cardinality order.
- `False` (default) — **parallel**: all instances start at activation and run
  concurrently in **distinct per-instance scopes** (§2.2); the activity completes
  when the last drains. Scope isolation ensures concurrent instances never share
  token or per-instance data state.

Both shapes expose per-instance **`loopCounter`** (0-based ordinal) and the
aggregate runtime attributes of §2.9 to the activity's expressions.

For a composite activity these two shapes are the decorator's two driving
strategies — await-each (sequential) and fan-out-then-await-all (parallel) — run
on the activity's own execution (§2.12).

### 2.6 Data flow — split in, assemble out

Multi-Instance is fundamentally a **collection transform**
([multi-instance.md §Data semantics](../../bpmn-spec/semantics/multi-instance.md)).
The spec calls the split/assemble *mediator* "under-specified"; this ADR fixes a
concrete engine convention:

- **Split.** Before each instance runs, the engine binds that instance's
  `inputDataItem` to element *`loopCounter`* of the `loopDataInputRef`
  collection. The instance reads it by name in its input data associations,
  exactly like any other per-scope datum.
- **Assemble.** When an instance completes, the engine writes that instance's
  `outputDataItem` into slot *`loopCounter`* of the `loopDataOutputRef`
  collection, preserving positional correspondence with the input.
- **Visibility barrier.** The spec **recommends** the `loopDataOutputRef`
  collection not be accessible until *all* instances have completed
  ([multi-instance.md §Data semantics](../../bpmn-spec/semantics/multi-instance.md):
  "should not be accessible" — token-passing alone cannot guarantee the
  collection is fully written). The engine **strengthens this recommendation
  into a guarantee**: the collection must not be readable by concurrent
  activities before completion — the assembled output is published to the
  enclosing scope only at activity completion, never incrementally.

Positional assembly (output slot = input ordinal) is the engine's realization of
the spec's under-specified mediator, chosen for determinism: the output
collection mirrors the input order regardless of instance completion order
(critical for parallel MI, where completion order is nondeterministic).

### 2.7 Completion condition — early, orderly cancellation

`completionCondition` is a boolean expression **evaluated each time an instance
completes** (§13.3.7):

- `true` → the Multi-Instance activity is **done now**: the remaining
  not-yet-completed instances are **cancelled** (their scopes torn down as a
  unit, the ADR-018 §interruption mechanism applied per instance scope), and
  control flows out of the activity.
- `false` → that instance is counted; the remaining instances continue.

Without a `completionCondition`, the activity completes when **all** instances
have completed. Cancellation is orderly: cancelled instances do not contribute
their `outputDataItem` (their slot stays at its pre-run value), and the output
collection is still published atomically per §2.6.

### 2.8 Behavior — events thrown as instances complete

`behavior` (`MultiInstanceBehavior`, default `All`) governs whether the activity
**throws an event** as instances complete
([multi-instance.md §Event throwing](../../bpmn-spec/semantics/multi-instance.md)).
The thrown events are **catchable on the boundary** of the Multi-Instance
activity (ADR-018 boundary mechanism), letting a model react to progress:

- `All` (default) — **no** event is ever thrown. The common case; zero cost.
- `None` — an event (`noneBehaviorEventRef`) is thrown for **every** instance
  completion.
- `One` — an event (`oneBehaviorEventRef`) is thrown once, on the **first**
  instance completion.
- `Complex` — the `complexBehaviorDefinition` entries drive it: on every
  instance completion, each definition's `condition` (a FormalExpression) is
  evaluated, and each one that is `true` throws its associated
  `ImplicitThrowEvent`
  ([events.md §ImplicitThrowEvent](../../bpmn-spec/elements/events.md)). A single
  completion can therefore throw several distinct events, each catchable by a
  different boundary event — enabling progress-dependent flows (e.g. "throw
  *quorum-reached* once 3 of 5 approvals arrive").

The thrown events implicitly carry the Multi-Instance activity's runtime
attributes (§2.9), so a boundary handler can read how far the activity has
progressed.

This subsection fixes *what* is thrown and *when*; the throw **executes** as an
ordinary off-loop emit issued by the iteration decorator before it completes the
activity (§2.12), which is what makes the boundary catch deterministic.

### 2.9 Instance runtime attributes (engine convention)

The standard states that a Multi-Instance activity's *runtime attributes* are
available to `completionCondition`, `ComplexBehaviorDefinition.condition`, and the
behavior events' data associations, but the vendored extract does **not**
enumerate them. gobpm therefore **defines** the following engine-provided
variables as its realization of that under-enumerated set (an explicit engine
choice, to be pinned against §13.3.7 when the KB is extended):

| Variable | Meaning |
|---|---|
| `loopCounter` | 0-based ordinal of the current instance (also available inside each instance). |
| `numberOfInstances` | Total instance count fixed at activation (§2.4). |
| `numberOfActiveInstances` | Instances currently running (parallel) — ≤ 1 for sequential. |
| `numberOfCompletedInstances` | Instances that have completed so far. |
| `numberOfTerminatedInstances` | Instances cancelled by a completion condition (§2.7). |

These are read-only in expressions; the engine maintains them as instances
progress.

### 2.10 Deferred: compensation of Multi-Instance

BPMN §13.3.7 specifies that a Multi-Instance activity compensates only if **all**
its instances completed, sequential/loop instances compensating in **reverse**
order and parallel ones in parallel
([multi-instance.md §Compensation](../../bpmn-spec/semantics/multi-instance.md)).
gobpm has **no compensation substrate yet** — compensation is Transaction-scope
work (tracked separately). This ADR therefore **defers** MI compensation: the
iteration model is designed so per-instance scopes are individually addressable
(§2.2), which is the prerequisite the future compensation work will consume, but
no compensation behavior is realized here. When compensation lands, its ADR
extends this one.

### 2.11 Engine notes (deviations & choices)

- **Iteration mechanism by activity kind** (§2.2) — in-place fresh-frame
  re-execution for a leaf Task, per-iteration child scope for a composite — is an
  engine choice: the standard mandates neither construct, only that iterations
  execute; the engine picks the cheapest one that isolates each iteration.
- **Positional output assembly** (§2.6) is the engine's concretization of the
  spec's under-specified mediator.
- **Cardinality-vs-collection exclusivity** (§2.4) is an engine validation
  choice; the spec lists both attributes without forbidding both, but a
  well-formed MI activity uses exactly one source.
- **Runtime-attribute set** (§2.9) is an engine convention pending a KB extension.

### 2.12 Composite iteration runs off the loop — the iteration decorator (v.2)

§2.2 fixes the *mechanism* (a composite activity re-opens its child scope per
iteration); this subsection fixes *who drives it*. **A composite looped activity
iterates on the activity's own execution — an off-loop *iteration decorator* —
not under control code run on the per-instance loop goroutine.**

**Why this is decided here.** The engine's execution model
([ADR-017 v.1](ADR-017-channel-based-event-processing.md)) has a **single-writer
loop goroutine** that owns all execution-lifecycle state (open scopes, token
positions, the parallel instance barrier), while a node's *work* runs **off** it,
on a per-token runner goroutine that reports state transitions back as events.
The v.1 landing drove the iteration *control* — resolve the count, split data,
evaluate the completion condition, decide re-entry, and (§2.8) **throw behavior
events** — **on the loop goroutine**, splitting control and work across the
goroutine boundary the wrong way. The §2.8 behavior throw is the proof: throwing
an event means handing it to the loop's ordered inbound channel, but issued *from*
the loop goroutine that hand-off self-deadlocks (the loop is the channel's only
reader and is busy inside the throw); made fire-and-forget it instead drops the
catch nondeterministically, because the throw and its boundary catch become
separate loop steps the activity's own completion can race between. Both are
symptoms of one structural fact: **the v.1 control was not the decorator BPMN
describes** (§13.3.6–§13.3.7 frame loop characteristics as a wrapper *around the
activity*, whose control belongs to the activity's execution).

**The decision.**

- **The activity's runner drives the iteration.** The composite host's own
  (off-loop) execution resolves the count/condition, opens each instance, awaits
  each completion, evaluates the completion condition, assembles output (§2.6),
  throws behavior events (§2.8), and then completes the activity and follows its
  outgoing flow. The host **no longer parks** while the loop drives iteration on
  its behalf — its runner *is* the driver. Parking returns to its BPMN meaning
  (waiting for an external event).
- **The loop stays the single writer; the decorator *requests* scope
  operations.** Running off-loop, the decorator must not mutate loop-owned state
  directly (that would reintroduce the cross-goroutine races
  [ADR-017 v.1](ADR-017-channel-based-event-processing.md) removed). It uses a
  **request/response** protocol over the existing event channel: it requests an
  operation (open an instance scope, close a drained one, bind a per-instance
  datum), the loop performs the mutation on its own goroutine and acknowledges on
  the decorator's inbound channel, and the decorator — blocked on that
  acknowledgement — resumes. Strictly ordered, no shared mutable state, no lock:
  the single-writer invariant is **preserved and extended**, not relaxed.
- **Sequential and parallel are two driving strategies** (§2.5): await-each, or
  fan-out-then-await-all with the N-of-N barrier — ordinary control flow on the
  decorator's goroutine rather than callbacks re-entered by the loop.
- **Behavior events become ordinary off-loop throws** (§2.8). The decorator emits
  by the same path any activity uses, and can **block until the throw is
  accepted** before completing the activity — so the boundary catch is ordered
  *before* completion by construction, on a boundary that is still armed. The v.1
  deadlock and nondeterministic drop are **structurally impossible** on this
  model.

**Scope.** This governs **composite** activities (Sub-Process, Call Activity) —
the only activities that carry boundaries and throw behavior events. A **leaf-task
loop already** runs in place on the task's own runner (§2.2); it is already off
the loop goroutine and is **unchanged**.

**Semantics are unchanged.** Everything §2.1–§2.11 decides — count fixed once
(§2.4), split-in/assemble-out with the visibility barrier (§2.6), the completion
condition (§2.7), the runtime attributes (§2.9) — is preserved verbatim. This
subsection changes only **where that control runs** (on the decorator, off the
loop), not **what it computes**. The re-open-a-child-scope mechanism of §2.2
stands; the decorator merely *requests* each open/close rather than the loop
performing it inline.

**Engine note.** The request/response scope protocol is an engine mechanism, not
a BPMN concept — BPMN is silent on the engine's goroutine model; the protocol
exists solely to reconcile off-loop control with the single-writer invariant, and
is invisible to a modeler.

---

## 3. Standard grounding

| Claim | Source |
|---|---|
| Standard Loop attributes & semantics | [multi-instance.md §Standard Loop](../../bpmn-spec/semantics/multi-instance.md); [activities.md §StandardLoopCharacteristics](../../bpmn-spec/elements/activities.md) (§13.3.6) |
| MI cardinality (expression \| collection), fixed at activation | [multi-instance.md §Cardinality](../../bpmn-spec/semantics/multi-instance.md) (§13.3.7) |
| `isSequential` sequencing | [multi-instance.md §Sequencing](../../bpmn-spec/semantics/multi-instance.md); [activities.md §MultiInstanceLoopCharacteristics](../../bpmn-spec/elements/activities.md) |
| `completionCondition` cancels remaining | [multi-instance.md §Completion](../../bpmn-spec/semantics/multi-instance.md) |
| `behavior` = All/None/One/Complex event throwing | [multi-instance.md §Event throwing / §ComplexBehaviorDefinition](../../bpmn-spec/semantics/multi-instance.md); [activities.md §MultiInstanceLoopCharacteristics / §ComplexBehaviorDefinition](../../bpmn-spec/elements/activities.md) |
| `ImplicitThrowEvent` as the complex-behavior event | [events.md §ImplicitThrowEvent](../../bpmn-spec/elements/events.md) |
| Data split/assemble, output visibility barrier | [multi-instance.md §Data semantics](../../bpmn-spec/semantics/multi-instance.md) |
| Compensation ordering | [multi-instance.md §Compensation](../../bpmn-spec/semantics/multi-instance.md) |

Where the extract is silent (the MI runtime-attribute set), §2.9 marks the
engine convention explicitly rather than asserting a spec mandate.

---

## 4. Alternatives considered

- **A shared iteration node for the concurrent (parallel-MI) case.** Model
  parallel MI as a single node that internally fans out, without a distinct scope
  per instance. Rejected: parallel isolation would then need a bespoke per-branch
  data-partitioning mechanism, duplicating what the scope model already gives for
  free, and a looped Sub-Process would need *two* different composition models
  (scope for the body, something else for the iteration). Reusing the ADR-023
  scope per concurrent instance (§2.2) is strictly simpler — while the *sequential*
  cases avoid the scope entirely for a leaf Task (§2.2), so no ceremony is paid
  where one-at-a-time execution already isolates.
- **Incremental output publication.** Write each instance's output into the
  shared collection as it completes, rather than at activity completion.
  Rejected: violates the §13.3.7 visibility barrier — a concurrent activity could
  read a half-assembled collection, and completion order (parallel) would leak
  into observable ordering.
- **Deferring `behavior` event-throwing.** Land only All/None/One and skip
  `Complex`. Rejected by the owner: the full §13.3.7 surface, including
  `ComplexBehaviorDefinition`, is in scope — progress-dependent boundary flows are
  a genuine expressiveness win and the boundary-catch substrate (ADR-018) already
  exists to receive them.
- **Standard Loop via a self-looping sequence flow.** Ask modellers to draw an
  explicit gateway-and-back-edge instead of supporting `StandardLoopCharacteristics`.
  Rejected: it is a first-class BPMN marker in the conformance scope and changes
  the diagram's meaning (a marked activity vs. an explicit cycle).

For the §2.12 execution model (v.2), the rejected alternatives were:

- **Keep control on the loop; move only the behavior throw off-loop.** Special-case
  the §2.8 throw onto a transient goroutine, or fire its boundary inline, leaving
  iteration loop-driven. Rejected: it treats the symptom, not the structural
  mismatch — control stays on the wrong goroutine — and it needs bespoke
  inline-fire machinery to order the catch before completion. It does not
  generalize: every future control-side emit re-hits the same wall.
- **Relax the single-writer invariant.** Let the off-loop decorator open/close
  scopes and update positions directly under a lock. Rejected: it reintroduces the
  cross-goroutine mutation of lifecycle state that
  [ADR-017 v.1](ADR-017-channel-based-event-processing.md) removed, and a lock over
  the position/scope maps is a strictly worse synchronization than goroutine
  confinement.
- **Fire-and-forget async throw (keep the v.1 model).** Emit the behavior event
  from a transient goroutine and let the catch land whenever. Rejected: empirically
  nondeterministic — the catch is a later loop step that races the activity's
  completion, dropping behavior events on both the sequential and parallel shapes.
  A correctness gap, not a style choice.

---

## 5. Consequences

- **Positive.** Mass per-element processing becomes expressible; the epic's
  parallel-isolation and aggregation requirements fall out of the existing scope
  model; a single iteration concept serves Task, Sub-Process, and Call Activity;
  progress observability via boundary-caught behavior events.
- **Cost.** The activity model grows two concrete loop-characteristics types and
  their validation; the runtime learns to open N scopes (sequentially or in
  parallel) for one activity and to maintain the aggregate runtime attributes;
  the data layer gains the split/assemble mediator and the visibility barrier.
- **Risk.** Parallel MI multiplies concurrency — the scope drain accounting must
  be exact so an activity neither completes early nor hangs. Mitigated by reusing
  the proven ADR-023 open/drain/close lifecycle rather than inventing new
  accounting. `Complex` behavior is the least-used, highest-complexity surface;
  it lands last, after the sequential and parallel cores are proven.
- **v.2 rework (§2.12).** Moving composite iteration onto the off-loop decorator
  **re-lands** the composite Standard Loop and Multi-Instance execution paths; the
  element SRDs that landed the loop-goroutine-driven composite model are
  **deleted and reused** — each is rewritten in place on the decorator (its old
  content deleted, its number reused), rather than marked Obsolete or renumbered,
  which keeps the element→SRD mapping stable and the doc-set consistent with the
  code — and the conformance tracker is updated to the new landing. It adds one off-loop↔loop **coordination surface** (the scope
  request/response protocol) — historically where races appear; mitigated by the
  strict request/response discipline (the loop stays sole writer, no shared
  state), by re-landing **incrementally** (Standard Loop, then sequential, then
  parallel) with the existing loop / MI / boundary suites as the green-throughout
  safety net, and by leaving **leaf-task loops untouched**, which bounds the blast
  radius to composite iteration.

---

## 6. Enterprise-readiness recommendations

- **Observability.** Emit facts for iteration lifecycle — activation (with the
  resolved cardinality and source), each instance start/complete/cancel (with
  `loopCounter`), and activity completion (with the completed/terminated counts)
  — so operators can watch a 10 000-element fan-out make progress and spot a
  stuck instance. Reuse the ADR-013 observability vocabulary.
- **Bounded fan-out.** A cardinality driven by external data can be huge;
  recommend (and document) an operational guard on parallel MI width, so a
  pathological collection cannot exhaust goroutines/memory. `loopMaximum` guards
  Standard Loop; parallel MI needs an analogous engine-level ceiling as an
  operational concern.
- **Deterministic aggregation.** The positional-assembly guarantee (§2.6) should
  be part of the public contract — consumers can rely on `output[i]`
  corresponding to `input[i]`.
- **Expression cost.** `completionCondition` and `Complex` conditions run on
  every instance completion; document that they should be cheap and side-effect
  free.

---

## 7. Rollout plan

Landed incrementally, smallest-first, each slice its own SRD and PR on the
existing execution-scope substrate:

1. **Standard Loop** — the sequential condition loop (`loopCondition`,
   `testBefore`, `loopMaximum`, `loopCounter`); the simplest iteration, proving
   the per-iteration-scope model on a single sequential path. (Lands with this
   ADR.)
2. **Multi-Instance sequential** — cardinality (both sources), the data
   split/assemble mediator + visibility barrier, `completionCondition`, runtime
   attributes; reuses the sequential re-entry lifecycle.
3. **Multi-Instance parallel** — concurrent per-instance scopes, drain-to-join
   completion, plus the `behavior` event-throwing surface (All/None/One/Complex)
   catchable on the MI boundary.

Compensation (§2.10) is out of this rollout; it rides the future Transaction /
compensation work.

**v.2 re-landing (§2.12).** The three slices above landed on the
loop-goroutine-driven composite model; v.2 re-lands them on the off-loop
decorator, smallest-first, each its own SRD and PR:

1. **Decorator engine + composite Standard Loop** — the request/response scope
   protocol, proven by re-landing the simplest composite iteration, green against
   the existing suites.
2. **Sequential Multi-Instance** on the decorator.
3. **Parallel Multi-Instance** on the decorator — the fan-out-then-await-all
   strategy with the N-of-N barrier expressed as decorator control flow.
4. **Multi-Instance behavior** (§2.8) on the decorator — a straightforward
   off-loop throw with a deterministic boundary catch.
5. **Retire the old seam** — remove the loop-goroutine-driven composite-iterator
   seam and update the conformance tracker to the new landing.

Each slice **deletes and reuses** its element's SRD — rewriting it in place on the
decorator (old content deleted, its number reused) rather than marking it Obsolete
or minting a new number — so the element→SRD mapping stays stable.

---

## 8. References

- [SAD-001 v.1 — Vision & Architecture](SAD-001-vision-and-architecture.md) §5, §15.3
- [ADR-023 v.2 — Sub-Process & Call Activity Execution Model](ADR-023-sub-process-and-call-activity.md) — the execution-scope model reused here
- [ADR-018 v.1 — Boundary Events & Activity Interruption](ADR-018-boundary-events-and-activity-interruption.md) — boundary catch for behavior events; per-instance cancellation
- [ADR-006 v.3 — Events & Subscriptions](ADR-006-events-and-subscriptions.md) — event throwing/catching
- BPMN 2.0 §13.3.6 (Standard Loop), §13.3.7 (Multi-Instance); vendored extract
  [multi-instance.md](../../bpmn-spec/semantics/multi-instance.md),
  [activities.md](../../bpmn-spec/elements/activities.md),
  [events.md](../../bpmn-spec/elements/events.md)

---

## Open questions

None.

---

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-19 | Ruslan Gabitov | Initial draft — Standard Loop & Multi-Instance iteration model: per-iteration isolation by a mechanism fitting the activity kind (in-place fresh-frame for a leaf Task, per-iteration child scope for a composite, distinct per-instance scope for parallel MI), cardinality (expression \| collection), sequential/parallel sequencing, split/assemble data mediator with visibility barrier, completion condition, full `behavior` event-throwing (All/None/One/Complex), engine-convention runtime attributes; MI compensation deferred to the future Transaction work. |
| v.2 | 2026-07-21 | Ruslan Gabitov | Added §2.12 — composite iteration runs on the activity's own off-loop execution (an *iteration decorator*), not under control code on the per-instance loop goroutine; the decorator requests scope operations from the single-writer loop via a request/response protocol (ADR-017 v.1 invariant preserved), sequential/parallel become its two driving strategies, and the §2.8 behavior throw becomes an ordinary off-loop emit with a deterministic boundary catch (unimplementable correctly on the v.1 model). Semantics §2.1–§2.11 unchanged; only *who drives* composite iteration moves. Forward-pointers added to §2.2/§2.5/§2.8; execution-model alternatives added to §4; v.2 rework consequences/rollout added to §5/§7. Leaf-task loops unchanged. The SRDs that landed the loop-goroutine-driven composite model are superseded and deleted when the re-landing completes. |
