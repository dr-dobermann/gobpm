# ADR-025 — Activity Iteration: Standard Loop & Multi-Instance

| Field | Value |
|---|---|
| Status | Draft (v.3 — flips back to Accepted when the v.3 changes land) |
| Version | v.3 |
| Date | 2026-08-10 |
| Owner | Ruslan Gabitov |
| Refines | [SAD-001 v.1.1](SAD-001-vision-and-architecture.md) §5 / §15.3, [ADR-023 v.3](ADR-023-sub-process-and-call-activity.md) (the execution-scope model this reuses), [ADR-018 v.1](ADR-018-boundary-events-and-activity-interruption.md) (boundary catch for thrown behavior events), [ADR-017 v.1](ADR-017-channel-based-event-processing.md) (the single-writer execution model §2.12–§2.13 extend), [ADR-006 v.5](ADR-006-events-and-subscriptions.md) (event throwing/catching; §2.9 in-instance delivery, whose processor-identity seam §2.13 moves) |
| Related | [ADR-007 v.2.1](ADR-007-in-memory-long-waits.md) §2.4 (holdable waits and the per-arm releasability rule §2.13 extends to iteration granularity), [ADR-013 v.2](ADR-013-instance-observability.md) (the token view §2.9.1 enriches), [ADR-010 v.2](ADR-010-process-data-model.md) (the execution frame that isolates an iteration), [ADR-036 v.1](ADR-036-incidents-and-fault-tolerance.md) §2.1–§2.3 (the incident and retry contract §2.14 applies at iteration granularity) |

> **Draft (v.3)** — decides how an activity marked with *loop characteristics* runs
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
>
> **v.3** sharpens the **node execution model** and finishes what v.2 started.
> v.2 gave *composite* iteration to the decorator and left a leaf loop where it
> was, which produced three iteration mechanisms and one construct nobody could
> build: an iterated activity that WAITS. §2.13 replaces that with one model
> covering every node kind — a simple node, an inline Sub-Process, a Call
> Activity — and their conjunction with loop characteristics: the **node
> executor** runs one instance of an activity and owns whatever that instance
> awaits, a **decorator** holds N of them and implements the same interface, so
> a track drives one executor and cannot tell how many instances are behind it.
> A track therefore means *a token walking a path* again; the decorator is the
> node's single registered event processor; the parallel barrier moves off the
> loop, as v.2 already said it should; and the instance↔track protocol is
> unchanged by construction.
>
> Four accepted decisions change with it. §2.2 replaces v.1's "parallel
> Multi-Instance always needs a per-instance scope" with one isolation rule (a
> frame per iteration always, a scope only where the activity is itself a scope
> host). §2.9.1 decides an iterated activity holds ONE token carrying its
> iteration state, rather than the token count reporting the execution
> mechanism. §2.12's composite-only scope widens to every iterated activity.
> §2.14 keeps failure at the granularity of execution: one instance failing
> raises an incident carrying its iteration context and is retried alone, its
> siblings untouched. Semantics §2.1–§2.11 are preserved; what changes is which
> runtime object does the work, and what the engine says about it.

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
([conformance.md](../bpmn-spec/conformance.md), the project's scope). Mass
per-element processing is impossible to express without them.

The problem this ADR solves is **conceptual**, not mechanical: *what does it
mean for one activity to be many executions* — how instances are counted, how
each instance is isolated from its siblings, how per-instance data is split from
and re-assembled into a collection, when the whole thing is considered done, and
how progress can be observed from the boundary. The mechanics (which existing
runtime seam carries each part) are the SRDs' job.

### Object model (BPMN 2.0, verbatim from the vendored extract)

From [activities.md §StandardLoopCharacteristics / §MultiInstanceLoopCharacteristics /
§ComplexBehaviorDefinition](../bpmn-spec/elements/activities.md) and
[multi-instance.md](../bpmn-spec/semantics/multi-instance.md):

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

**One rule decides the mechanism (v.3): a frame per iteration always; a scope
only where the activity is itself a scope host.** Every iteration runs in its
own execution frame, the per-execution data boundary (ADR-010 v.2): its inputs,
properties and locals are its own, and no iteration can read another's
in-flight state. A composite additionally opens its child scope per iteration —
not for isolation, but because a Sub-Process body *is* a scope and needs one to
exist at all.

A frame bounds an **execution**, not its **results**: what an instance commits
goes to the enclosing scope. What that means across iterations — a reduce when
sequential, order-dependent when parallel, deterministic when a strategy is
declared — is §2.6.1, and it is a separate decision from this one. Reading the
frame as though it also isolated results is the mistake §2.6.1 exists to
prevent.

v.1 made parallel Multi-Instance an exception, giving every parallel instance
its own scope even for a leaf Task, on the reasoning that concurrent instances
must not share state. The premise was true; the conclusion followed only
because an iteration was executed by a *track*, and one track has one frame —
so concurrency forced a heavier construct. Once an iteration is executed by a
**node executor** (§2.13) that owns its own frame, concurrent iterations have
concurrent frames and the exception disappears. A leaf activity therefore never
gets a child scope, sequential or parallel; a composite always does, sequential
or parallel; and *why* is the same sentence in both cases.

Rationale for not giving a looped leaf Task a child scope: a Task is not a scope
container — a scope would mean seeding an empty inner graph and routing a
synthetic completion for isolation the fresh frame already provides.

Per-instance **addressability**, which v.1 obtained from the scope identity, is
now the executor's ordinal (§2.9) — stable, derived from the activity and the
0-based instance number, and available whether or not a scope exists.

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
  concurrently in **distinct per-instance execution contexts** (§2.2 — a frame
  each, plus a scope where the activity is itself a scope host); the activity
  completes
  when the last drains. Scope isolation ensures concurrent instances never share
  token or per-instance data state.

Both shapes expose per-instance **`loopCounter`** (0-based ordinal) and the
aggregate runtime attributes of §2.9 to the activity's expressions.

For a composite activity these two shapes are the decorator's two driving
strategies — await-each (sequential) and fan-out-then-await-all (parallel) — run
on the activity's own execution (§2.12).

### 2.6 Data flow — split in, assemble out

Multi-Instance is fundamentally a **collection transform**
([multi-instance.md §Data semantics](../bpmn-spec/semantics/multi-instance.md)).
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
  ([multi-instance.md §Data semantics](../bpmn-spec/semantics/multi-instance.md):
  "should not be accessible" — token-passing alone cannot guarantee the
  collection is fully written). The engine **strengthens this recommendation
  into a guarantee**: the collection must not be readable by concurrent
  activities before completion — the assembled output is published to the
  enclosing scope only at activity completion, never incrementally.

Positional assembly (output slot = input ordinal) is the engine's realization of
the spec's under-specified mediator, chosen for determinism: the output
collection mirrors the input order regardless of instance completion order
(critical for parallel MI, where completion order is nondeterministic).

#### 2.6.1 Iteration results — one default, three declared strategies (v.3)

§2.6 above governs the **declared** MI collection. It says nothing about what
happens to everything *else* an instance writes, and that gap is load-bearing:
an instance runs in its own frame, but a frame's commit target is the
**enclosing scope** — isolation of an execution is not invisibility of its
writes. This subsection decides what an iteration's results mean.

**The default is last write wins.** Each instance executes in its own frame;
whatever it commits lands in the enclosing scope, and a later write replaces an
earlier one. The consequence differs by sequencing, and both are intended:

- **Sequential iteration (Standard Loop, sequential MI) is therefore a reduce.**
  Iteration *k* reads what iteration *k-1* committed, accumulating in the
  enclosing scope. This is not new — it is what a Standard Loop already does,
  since each pass commits there and the next pass resolves frame-first then
  walks up — but it was implicit, and an implicit fold is a thing readers
  rediscover by experiment. It is the default *because* it is the useful one:
  "keep a running total", "narrow a candidate set", "append to a report".
- **Parallel Multi-Instance is therefore order-dependent for undeclared
  writes.** Which instance's value survives depends on completion order, which
  the engine does not fix. This is stated plainly rather than hidden, and it is
  exactly why the declared strategies below exist: a model that needs every
  instance's result must **say so**, and then gets a deterministic one.

**Three declared strategies, each opt-in and deterministic:**

| Strategy | Result | Standard footing |
|---|---|---|
| **array** | results indexed by **ordinal** — slot *i* holds instance *i*'s output, regardless of completion order | For MI this IS the spec's `loopDataOutputRef` assembly (§13.3.7, §2.6 above). For a Standard Loop it is an **engine extension**: the spec gives a loop no output aggregation at all |
| **map** | results keyed by a per-instance **key**, evaluated in that instance's frame at its completion | **Engine extension** for both kinds — BPMN's MI output is an ordered collection, never a keyed one |
| **reduce** | the accumulating value in the enclosing scope — the default, nameable so a model can declare the intent it is relying on | **Engine convention**; the spec is silent |

**The map's key is an expression, evaluated in the completing instance's
frame.** That timing is the point: it lets the key use something the instance
*produced* — the assignee of a User Task being the motivating case, since it is
unknown until the task is claimed. No bespoke key mechanism is introduced; the
expression reads the same data any other expression can, including the
iteration runtime values of §2.9.2.

Two rules, and they differ deliberately:

- **An empty or missing key refuses.** There is no sensible slot for a result
  with no key, and silently dropping one instance's output is the failure this
  whole subsection exists to make impossible.
- **A duplicate key overwrites by default, and `ErrorOnKeyRewrite` makes it a
  fault.** Overwriting is consistent with the last-wins default above rather
  than an exception to it, and the loss is *detectable* rather than silent:
  §2.9.2 publishes the instance total, so a model or host comparing the map's
  size against it sees that N instances produced fewer than N entries. A model
  that treats a collision as a modelling error — a fan-out over participants
  who must each answer once — sets the option and gets a fault naming both
  ordinals and the key.

**The visibility barrier (§2.6) covers a declared result.** An array or a map
publishes to the enclosing scope once, at activity completion — never
incrementally, so no concurrent activity can read a half-assembled result. The
default (last-wins) has no barrier by construction: it is the enclosing scope,
written as instances go.

**Engine notes.** BPMN §13.3.7 defines the ordered output collection and is
otherwise **silent** on what an iteration's other writes mean; §13.3.6 gives a
Standard Loop no data aggregation whatsoever. The default, the reduce naming,
the loop's array and the map in both kinds are therefore engine choices, and
the parallel default's order-dependence is a documented property rather than a
guarantee. A modeller who needs determinism declares a strategy; one who needs
a running total gets it without declaring anything.

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
([multi-instance.md §Event throwing](../bpmn-spec/semantics/multi-instance.md)).
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
  ([events.md §ImplicitThrowEvent](../bpmn-spec/elements/events.md)). A single
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
progress. **Where they live and why they cannot be overwritten is §2.9.2** —
v.3 moved them into the engine-published runtime source, so "read-only" is
enforced rather than merely intended.

#### 2.9.1 Iteration state is projected onto the token (v.3)

**An iterated activity holds ONE token, and what its iterations are doing is a
property of that token, not a multiplicity of tokens.**

BPMN describes Multi-Instance as a wrapper that executes an Activity several
times (§13.3.7, "wraps an Activity to execute multiple times") and speaks of
*instances of an activity* throughout; it says nothing about tokens
multiplying, and this ADR does not read that silence as permission — the
statement here is an explicit engine choice about what an observer sees.

The choice matters because the alternative was never a decision. When an
iteration was a track and the token view was derived from tracks, a parallel
MI over three items *appeared* as three tokens — an artifact of the execution
mechanism leaking into the observable model. It also observed badly: three
tokens said how many iterations were parked but never which, with no ordinal
and no counts, while a sequential MI or a Standard Loop showed a single token
that looked identical on pass 1 and pass 7. The mechanism was visible and the
information was not.

The token view (ADR-013 v.2) therefore carries, for a token resting on an
iterated activity, an optional **iteration view**: the kind (Standard Loop,
sequential MI, parallel MI), the total from §2.4 (absent for a Standard Loop,
whose count is not known ahead), the completed count from §2.9, and one entry
per live executor giving its ordinal and what that instance is doing —
executing, or waiting for an event.

Three properties make this the cheap answer rather than an extra bookkeeping
burden. It is derived from the decorator's executors, so there is exactly one
source. It is the same state the decorator consults to route a delivery
(§2.13), so the projection cannot disagree with the routing. And it survives a
restart by construction, because those executor states are precisely what
hydration rebuilds — where the previous model's token count reappeared only as
a side effect of rebuilding tracks.

The field is optional and absent for every non-iterated node, so an observer
that ignores it sees one token per activity, which is what BPMN describes.

**Engine note.** ADR-013 v.2 owns the token view; this subsection decides only
that iteration state belongs *on* it rather than being inferred from token
count. The contract change to the view itself is that ADR's to make.

#### 2.9.2 Iteration values are engine-published, and outlive the activity (v.3)

§2.9's variables are bound as ordinary scope data, which has two consequences
worth deciding rather than inheriting: a model can **overwrite** them, and they
**vanish** when the activity completes — so "how many did we process?" is
unanswerable one node later, and a map key (§2.6.1) has nothing durable to key
on.

**Iteration values are therefore also published in the reserved read-only
RUNTIME source**, alongside the instance values already served there (the
started time, the state, the track count, the performer register). Being
engine-published is the point: a process reads them and cannot overwrite the
answer, nor collide with it by naming a variable the same way.

| Name | Shape | Available |
|---|---|---|
| `ITERATION_NUMBER` | the executing instance's 0-based ordinal | inside an instance |
| `ITERATIONS` | map: activity id → `{kind, total, completed, terminated}` | during, **and after the activity completes** |
| `ITERATION_OWNERS` | map: activity id → (ordinal → actual owner) | during, and after |

**Maps rather than one name per activity**, deliberately: the RUNTIME name set
is closed, and an open per-activity namespace would make it grow without bound
and force prefix matching to serve it. Keying by activity id is also what lets
the values outlive the activity — a frame dies with its execution and a counter
dies with the token, but a map in the instance's runtime source does not.

**§2.9's BPMN-named variables move here too — every iteration value is
engine-published, without exception.** They were bound as ordinary scope data,
which means a model could overwrite `loopCounter` or `numberOfInstances` and
then read its own value back from an expression that looks exactly like the
engine's. That is the failure this project already built a tool against, and
leaving the most-read names outside it would keep one rule with an exception on
precisely the values most models touch.

| BPMN attribute (§13.3.7) | Address | Per |
|---|---|---|
| `loopCounter` | `RUNTIME/loopCounter` | executing instance |
| `numberOfInstances` | `RUNTIME/numberOfInstances` | activity |
| `numberOfActiveInstances` | `RUNTIME/numberOfActiveInstances` | activity |
| `numberOfCompletedInstances` | `RUNTIME/numberOfCompletedInstances` | activity |
| `numberOfTerminatedInstances` | `RUNTIME/numberOfTerminatedInstances` | activity |

**Naming rule: a value the standard names keeps the standard's spelling; a
value the engine invented uses the engine's convention.** So the five above
read exactly as BPMN writes them and the migration is a pure prefix, while
`ITERATIONS` and `ITERATION_OWNERS`, which the spec has no word for, follow the
existing runtime-name convention.

**This requires the runtime source to know WHICH execution is asking.** Today
the supplier answers per instance, which suffices for instance-wide values and
for the activity-keyed maps above, but not for `loopCounter`: its value differs
per executing instance, and the supplier is handed only a name. The reader
already holds the frame of the execution doing the reading, so the asking
execution's identity can be carried into the lookup. That seam belongs to the
data model (**ADR-010 v.2**), and this ADR states the requirement it places on
it rather than redesigning it here.

**A model that declares a colliding name refuses at build time.** A process
property or data object named `loopCounter` (or any reserved iteration name) is
rejected when the process is built, naming the element — rather than silently
shadowing the engine's value and producing a wrong answer somewhere far away.
Located errors are the point: an overwritten counter is discovered as a strange
result three nodes later, a refused name is discovered where it is written.

**Consequence: this is a breaking change for models that read the bare names.**
`loopCounter` no longer resolves unprefixed; expressions read
`RUNTIME/loopCounter`. Measured at the time of writing, that is 14 Go files —
three runnable examples, the iteration test suites, and the engine's own
binding — plus two guides. The accompanying SRD carries the migration and the
CHANGELOG states it, because a silent address change is worse than a loud one.

**The performer register needs an iterated-case rule, and gets one.** The
existing register maps an activity to the user who completed it — one activity,
one performer. An iterated User Task has N performers for one activity, so the
register can hold only one of N and would report an arbitrary answer the moment
an iterated waiting activity becomes buildable. The register therefore keeps
the **last** completer, for compatibility with every non-iterated model, and
`ITERATION_OWNERS` is the honest source for the iterated case — one entry per
ordinal, which is what a fan-out over N approvers actually needs to be
answerable.

**Engine note.** BPMN says the runtime attributes are available to
expressions and does not enumerate them (§2.9), says nothing about their
lifetime after the activity, and has no concept of an engine-published
read-only source. Everything in this subsection is an engine choice.

### 2.10 Deferred: compensation of Multi-Instance

BPMN §13.3.7 specifies that a Multi-Instance activity compensates only if **all**
its instances completed, sequential/loop instances compensating in **reverse**
order and parallel ones in parallel
([multi-instance.md §Compensation](../bpmn-spec/semantics/multi-instance.md)).
**Refreshed in v.3.** v.1 deferred this on the grounds that gobpm had no
compensation substrate. It has one: compensation is decided by
[ADR-026 v.1](ADR-026-compensation-events.md), which already states the
per-instance rule for an iterated activity — each completed instance snapshots
separately (§13.5.5). So MI compensation is no longer deferred *here*; it is
owned there, and this ADR's obligation is to keep supplying what it consumes.

That obligation survives §2.2's revision intact, but by a different route. v.1
supplied per-instance addressability through the per-instance **scope**;
§2.2/§2.13 remove that scope for a leaf activity, and the addressability is now
the instance **ordinal** — stable, recorded, and available whether or not a
scope exists. A compensation ledger entry per completed instance therefore
keys on the ordinal, which is also what the incident record (§2.14) and the
token view (§2.9.1) key on. One identifier for one instance, across all four
surfaces.

### 2.11 Engine notes (deviations & choices)

- **Iteration isolation** (§2.2) — a fresh frame per iteration always, a child
  scope only where the activity is itself a scope host — is an engine choice:
  the standard mandates neither construct, only that iterations execute.
- **The node executor and the decorator** (§2.13) are engine mechanisms. BPMN
  frames loop characteristics as a wrapper around an activity (§13.3.7) but is
  silent on how an engine realizes one; the executor exists to separate "run
  this node once and own its wait" from "walk a path", which is an engine
  distinction, invisible to a modeler.
- **One token per iterated activity, with iteration state on it** (§2.9.1) is
  an engine choice on a point the standard leaves alone.
- **Iteration result semantics** (§2.6.1) — the last-wins default, the reduce
  naming, a Standard Loop's array and the map in both kinds — are engine
  choices. BPMN defines only MI's ordered output collection (§13.3.7) and
  gives a loop no aggregation at all (§13.3.6); the parallel default's
  order-dependence is documented as a property, never presented as a
  guarantee.
- **Engine-published iteration values** (§2.9.2) are an engine choice
  entirely: the standard neither enumerates the runtime attributes, nor says
  anything about their lifetime after the activity, nor has a concept of a
  read-only engine source. It does require them to be *available to
  expressions* (§13.3.7), which they remain — at a stated address, where the
  engine can guarantee the answer is its own.
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

**Scope (widened in v.3).** v.2 governed only **composite** activities
(Sub-Process, Call Activity), leaving a leaf-task loop as it was on the grounds
that it already ran in place on the task's own runner and so was already off
the loop goroutine. That was true and insufficient: "off the loop" is not the
only thing the decorator provides, and the two mechanisms diverged on
everything else — who owns the wait, what isolates an iteration, what a
restart rebuilds. §2.13 therefore extends this decision to **every** iterated
activity, leaf and composite alike. What v.2 decided here is unchanged; it now
applies to one more kind, through the executor that makes the kinds uniform.

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

### 2.13 Node execution — one model for every node kind, decorated or not (v.3)

§2.12 fixed *who drives* iteration. This subsection fixes something larger and
underneath it: **what executes a node at all.** The answer is one model
covering every node kind — a simple node, an inline Sub-Process, a Call
Activity — and their conjunction with loop characteristics, which is where the
current design breaks down. It removes a category error the earlier model was
built on, and the construct the engine refuses today (an iterated activity that
WAITS) is unbuildable precisely because that error had no repair short of
this one.

This is a concept, not an implementation note. It decides what a *track* means,
what object an activity's execution actually is, and which of them the event
hub knows about — the same class of decision §2.12 made about goroutines and
the loop. How each piece is realized in code is the accompanying SRD's, and it
is grounded there.

**The problem stated plainly.** A **track** means a token walking a path. A
Multi-Instance activity is one token resting on one activity that executes N
times — BPMN §13.3.7 wraps *an Activity* and speaks of *instances of that
activity*; the vendored extract mentions tokens only once, and about collection
visibility rather than instance multiplicity
([multi-instance.md §Constraints](../bpmn-spec/semantics/multi-instance.md)).
The engine's reading of that silence is §2.9.1's, and is stated there as a
choice. Yet the engine forked N tracks to
run one node N times, because a track was the only runtime object that could
both *execute a node* and *be an event processor*, so anything needing those two
capabilities had to become one. The consequences were not cosmetic: three
different iteration mechanisms (composite re-opens a scope, parallel leaf forks
tracks, sequential leaf re-runs in place), a marker on spawned tracks whose only
job was to stop them re-decorating themselves, a token count that reported
mechanism rather than model (§2.9.1), and one construct that could not be built
at all — **an iterated activity that waits**, which the engine refuses today.

**The decision.**

- **A node executor runs one node once and owns that node's wait.** It binds
  the iteration's data in its own frame (§2.2), executes the node, and — if the
  node parks — is the thing that is waiting: the executor is the event
  processor for that execution. It is the smallest object with the two
  capabilities a track was previously borrowed for.
- **A track executes nodes through executors; a decorator holds N of them.**
  On an ordinary node a track drives one executor and moves on. On an iterated
  activity the track drives the **decorator**, which holds one executor per
  instance and sequences them per §2.5 — one at a time, or all at once. Nothing
  forks a track to iterate; tracks are tokens again.
- **A composite executor opens the child scope; a leaf executor does not**
  (§2.2). Inside a composite instance the body's own tokens are ordinary tracks,
  because they are ordinary tokens walking the body's path. The distinction is
  no longer leaf-vs-parallel-vs-sequential but *is this activity a scope host*.
- **The decorator is the node's single registered event processor** — for
  those executors that await an *event*. Each executor owns its wait, but a
  wait is not always a subscription: only a node executor's is (§2.13's
  contract). The decorator registers those **as itself**, so the event hub sees
  one processor with as many subscriptions as there are waiting instances, and
  hands a delivery to that one processor. Differentiation
  is unchanged and remains the hub's: a message reaches the subscription whose
  correlation value matches, a signal reaches every subscription, per
  ADR-006 v.5 §2.9. What moves is only *whose* processor identity is
  registered — and the decorator dispatches the delivery to the executor that
  owns the matching wait. "Single processor" therefore means one owner and one
  dispatch point, not one subscription; the hub's behaviour is untouched.
  This is the seam whose absence made an iterated *waiting* activity
  impossible: with no single owner of the node's registrations, a second
  instance either never armed or served the wrong one.
- **The activity's own node never learns it is decorated.** It is executed, it
  may park, and its wait is registered on its behalf. Whether that registration
  named a track, an instance or a decorator is not the node's concern — which
  is what makes an iterated ReceiveTask a ReceiveTask rather than a special
  kind of one.
- **The N-of-N barrier moves off the loop.** v.2 already prescribed
  fan-out-then-await-all as "ordinary control flow on the decorator's
  goroutine"; with executors that is finally what it is. The decorator awaits
  its own executors. The loop no longer maintains a per-activity barrier and no
  longer counts per-instance scope drains for a leaf, because a leaf has no
  per-instance scope to drain.

**The executor contract — one interface, four realizations.**

The same abstraction serves every activity kind, which is what keeps a track's
job describable in one sentence: **a track drives one executor.**

| Activity | What executes one instance of it | What that executor awaits |
|---|---|---|
| a leaf Task | a **node executor** | an event subscription, if the node parks |
| an inline Sub-Process | a **sub-process executor** — it opens the child scope; the body's tokens are ordinary tracks | the scope's drain |
| a Call Activity | a **call executor** — it owns the child instance | that child instance's terminal state |
| any of the three under loop characteristics | the **decorator**, holding N of the above | whatever its executors await, in conjunction |

**The decorator implements the same interface as the executors it holds.** The
composition is therefore closed, and a track cannot tell whether it drives one
instance of an activity or twelve — which is what "the decorator is a node from
the track's point of view" means structurally rather than by convention.
Nesting decorators is not a modelable case (§2.1: the two loop forms are
mutually exclusive on an activity); the closure exists for uniformity.

Four things the interface must express, each demanded by a decision above:

- **Run or resume one instance**, including at a recorded position — hydration
  (§2.13) and incident retry (§2.14) both re-create one.
- **What it awaits, and of which kind.** This is one question with three
  answers, and asking it is mandatory — which is what prevents the residency
  bug above from recurring, and what tells the decorator which waits to
  register with the hub and which are none of the hub's business.
- **Its state, in the one iteration vocabulary** — the token view projects it
  (§2.9.1), the record persists it (§2.13), the incident carries it (§2.14).
  One source, three surfaces, no translation between them.
- **Cancel it** — for a fired completion condition (§2.7), an interrupting
  boundary (ADR-023 v.3 §2.5) and the terminate cascade (ADR-023 v.3 §2.7).
  Cancelling is not uniform in *cost*: a node executor abandons an execution, a
  sub-process executor cancels a scope, and a call executor **terminates a
  child instance**. A completion condition firing on instance 2 of 5 of an
  iterated Call Activity therefore terminates three durable child instances,
  each with its own record — worth stating, because it is invisible in the
  model and expensive in fact.

Two responsibilities stay **out** of the interface, deliberately:

- **Boundary arming belongs to the activity, not to an instance.** §2.2 has a
  boundary arm once and guard every iteration; an executor that armed its own
  would give a boundary timer N arms and N firings.
- **Output assembly belongs to the decorator.** It is positional across
  instances (§2.6); an executor produces its own result and knows its ordinal,
  but never writes into the collection.

**A call executor owns an instance, and that ownership is the parent linkage.**
The child of a Call Activity is a full Instance, but it does not belong to the
engine the way a root instance does — it belongs to its caller. That is the
root/child distinction the engine already draws: the parent records the call
descriptor and the child the reverse linkage (ADR-033 v.5 §2.10, ADR-023 v.3
§2.7), recovery claims a child only through its caller's claim (ADR-033 v.5
§2.10), and incidents exist only at top-level instances (ADR-036 v.1 §2.1), so
a child's failure surfaces at its owner's call node. Under iteration the caller
owns **N** children, one per ordinal, and two consequences follow that a single
call never had:

- **The record maps child to ordinal.** The caller's checkpoint records each
  in-flight child instance id *with the ordinal that owns it*. Without that
  mapping a recovered caller cannot tell which completed child feeds which
  slot, and positional assembly (§2.6) would bind the right output into the
  wrong position — silently.
- **The retry unit is the failed ordinal's child**, not the activity (§2.14),
  which is where this ADR and the current incident contract disagree; see the
  contract note there.

**The instance↔track protocol is unchanged.** To the per-instance loop, a track
executing an iterated activity is an ordinary track: it advances, it may park,
it reports transitions over the same channel with the same vocabulary, and the
decorator — running inside that track's off-loop execution — mutates no
loop-owned state, using §2.12's request/response protocol for anything it
needs. The loop simply stops seeing per-iteration track lifecycle events,
because there are no per-iteration tracks: fewer messages of kinds that already
exist, not new kinds. This is a property to preserve deliberately, not an
accident — it is what keeps the change contained to how an activity executes.

**Dehydration and hydration follow the rules a multi-wait track already obeys.**

- An instance may be released only when every track is parked on a *releasable*
  wait, already released, or terminal — a track that is executing keeps its
  instance resident. A decorator inherits this by **asking each executor what
  it awaits**, never by looking at whether it is "running": an executor
  awaiting a scope drain or a child instance is not doing work, and treating it
  as such would pin an instance resident forever. A Multi-Instance Sub-Process
  whose three iterations each contain a parked User Task must dehydrate — that
  is the week-long-approval case ADR-007 v.2.1 exists for — and it does,
  because a sub-process executor contributes nothing to residency and the
  body's own tracks are inspected as the tracks they are.
- **Releasability is per-wait, so a decorator's is the conjunction of its
  executors'.** This is the rule an Event-Based Gateway already applies to its
  arms (ADR-007 v.2.1 §2.4) at the granularity a gateway needs; a decorator has
  several waits on one track for the same reason and takes the same rule at
  iteration granularity — that ADR already states eligibility is "per-wait, not
  per-token", which is precisely the property a decorator needs. One unholdable wait keeps the instance resident, as
  one unholdable arm does.
- **Hydration has an ordering obligation**: rebuild the executors at their
  recorded ordinals and states (a completed instance never re-runs), register
  the decorator as the node's processor, and **re-arm every waiting executor
  before any delivery can be accepted**. Not because an early message would be
  lost — the broker buffers an envelope that matches no subscription and
  delivers it when one appears, which its conformance suite requires — but
  because a PARTIALLY armed set can match it into the **wrong** instance. An
  envelope carrying instance 1's correlation value, arriving while only
  instance 0 has re-armed, must wait for instance 1 rather than be served by
  whatever subscription happens to exist; delivering it to the wrong iteration
  is worse than delivering it late, and unlike a delay it is silent.
- **What is recorded is the executor set, not its frames.** Ordinal, state, the
  awaited definition and its correlation value are recorded; the frame contents
  are not, because they are recomputable — the split item is the collection
  element at that ordinal and `loopCounter` is the ordinal, and §2.4 fixes
  cardinality once so the collection cannot shift underneath. A Standard Loop
  has no collection at all: its counter is the pass number and its accumulated
  data lives in the data plane. Persisting frames would add state that must be
  kept consistent forever to describe something derivable.

**Consequences this ADR accepts.** The refusal of an iterated waiting activity
is retired — the construct becomes ordinary, because a waiting executor is just
an executor whose node parks. The per-instance scopes of a parallel leaf
disappear, so per-iteration data is addressed by ordinal rather than by scope
path. And the durable record changes shape, since an open instance is no longer
a track: the iteration record carries the executor set. The accompanying SRD
carries the schema move and its restore path.

**Engine note.** The executor, the decorator and their division of labour are
engine mechanisms. BPMN frames loop characteristics as a wrapper around an
activity and says nothing about how the wrapper is realized, what a runtime
"track" is, or which object holds a subscription; the distinctions here exist
to keep the engine's own invariants — single-writer state, one token per
activity, a wait that survives a restart — and are invisible to a modeler.

### 2.14 A failed iteration is an ordinary incident, at iteration granularity (v.3)

**One instance of a Multi-Instance activity failing must not cost the other
N−1 their work, and retrying it must re-run *it* — not the activity.**

This is a requirement on §2.13, not a consequence of it. An iteration used to
be a track, and the incident contract (ADR-036 v.1 §2.1–§2.3) is written in
terms of the failing track: the track ends in `TrackIncident`, the record
carries the node, scope path, lineage and cause, and retry **spawns a fresh
track from the record**. Replace the track with an executor and the granularity
would silently coarsen to the whole activity — the failure this subsection
exists to prevent.

It transfers almost verbatim, because that contract is already record-based
rather than track-based: "the goroutine-bearing thing ends, the recorded state
is what persists, and continuation is a fresh <thing> spawned from the record"
(§2.2). Only the noun changes.

**The decision.**

- **An executor's failure raises an incident at the activity node, carrying an
  iteration section of the same shape §2.9.1 puts on the token.** Everything
  ADR-036 v.1 §2.1 records is recorded — cause chain, attempt count,
  failure-time data snapshot, scope path — plus the iteration context: the
  kind, the total, the completed count, and which ordinal failed.

  A bare ordinal would name the retry unit but not enough to act on it. The
  section is what lets **retry target the failed executor directly**: the
  ordinal says which executor to rebuild, and the completed count says which
  instances must not run again — the resume rule below depends on exactly that,
  and would otherwise have to re-derive it from state the incident does not
  own. It also makes the record legible on its own ("instance 3 of 5 failed"),
  which a number in a field does not.

  Using the **same shape** in both places is deliberate. Iteration state has
  one vocabulary — kind, total, completed, per-instance ordinal and state —
  whether it is being observed on a token or recorded in an incident. Two
  representations of the same thing drift, and the operator who reads the token
  view and then the incident would have to translate between them.
- **Retry re-creates that executor, and only it.** The decorator rebuilds the
  executor at its ordinal and re-binds its frame; §2.13's rule that frames are
  recomputed from the ordinal rather than persisted is what makes a retry after
  a restart bind the same item as the attempt that failed. Every other executor
  is untouched, running or parked as it was. This is ADR-036 v.1 §2.2's
  "siblings are unaffected" at iteration granularity — the same sentence, one
  level down.
- **The activity cannot complete while one of its instances has an open
  incident.** ADR-036 v.1 §2.2 says an instance cannot complete with an open
  incident; the same holds for the activity, and for a sharper reason here:
  output assembly is **positional** (§2.6), so completing with an unresolved
  ordinal would publish a collection with a hole in it and call that success.
  The decorator waits; resolution un-sticks it.
- **The completion condition still cancels remaining instances — including a
  failed one — and closes its incident as cancelled, with the cause retained.**
  §2.7 lets the model decide the activity is done before every instance is;
  when it does, the outcome of the remaining instances is not needed, and a
  failed instance is a remaining instance. Leaving the activity stuck on an
  incident whose result the model has just declared irrelevant would let a
  transient fault in one branch defeat an early-completion rule that exists
  precisely to tolerate stragglers. The record is **closed, not deleted** — the
  operator can still see that the instance failed and why, which keeps the
  standing rule that a failure is never silent.
- **The failed ordinal is visible in both places an operator looks**: the
  iteration view (§2.9.1) shows that instance as in-incident, and the incident
  query (ADR-036 v.1 §2.6) returns a record naming the ordinal. "Which
  iteration failed, and retry that one" is answerable without reading the
  other.
- **Dehydration is unchanged.** A failed executor holds no goroutine — the
  incident record is the continuation — so an instance whose only remaining
  continuations are operator-waiting iteration incidents is quiescent, exactly
  as ADR-036 v.1 §2.2 describes.

**Two failures belong to the activity, not to an iteration.**

- **Before any executor starts** — the cardinality expression, or resolving the
  input collection (§2.4) — nothing has run, there is no ordinal, and the
  incident is an ordinary activity-level one. Retry re-evaluates and begins the
  iteration set from scratch.
- **After iterations have begun**, a failure in the decorator's own work — the
  completion condition (§2.7), output assembly (§2.6) — is also activity-level,
  because no single instance caused it. But retry here **resumes the decorator
  against its recorded executor set** rather than restarting the activity:
  completed instances never re-run, which is the same invariant §2.13's
  hydration obeys. Without that distinction, a failing completion-condition
  expression would silently re-execute every completed instance on retry — the
  duplicate-side-effect failure the recorded executor set exists to prevent.

**Engine note.** BPMN does not define incidents at all; the standard leaves the
reaction to an unhandled failure open (ADR-036 v.1 §3). Everything here is an
engine choice about *granularity* — that the unit of failure and retry matches
the unit of execution — and is invisible to a modeler, who sees only that one
instance of a Multi-Instance activity can fail, be retried and succeed while
its siblings proceed.

**Contract note.** The incident record's iteration section is ADR-036's field
to add, as the token's is ADR-013's; the decision here is only that the
incident is raised, recorded and retried at iteration granularity, and that
both surfaces describe an iteration the same way. Those contract changes are
those ADRs' to make.

---

## 3. Standard grounding

| Claim | Source |
|---|---|
| Standard Loop attributes & semantics | [multi-instance.md §Standard Loop](../bpmn-spec/semantics/multi-instance.md); [activities.md §StandardLoopCharacteristics](../bpmn-spec/elements/activities.md) (§13.3.6) |
| MI cardinality (expression \| collection), fixed at activation | [multi-instance.md §Cardinality](../bpmn-spec/semantics/multi-instance.md) (§13.3.7) |
| `isSequential` sequencing | [multi-instance.md §Sequencing](../bpmn-spec/semantics/multi-instance.md); [activities.md §MultiInstanceLoopCharacteristics](../bpmn-spec/elements/activities.md) |
| `completionCondition` cancels remaining | [multi-instance.md §Completion](../bpmn-spec/semantics/multi-instance.md) |
| `behavior` = All/None/One/Complex event throwing | [multi-instance.md §Event throwing / §ComplexBehaviorDefinition](../bpmn-spec/semantics/multi-instance.md); [activities.md §MultiInstanceLoopCharacteristics / §ComplexBehaviorDefinition](../bpmn-spec/elements/activities.md) |
| `ImplicitThrowEvent` as the complex-behavior event | [events.md §ImplicitThrowEvent](../bpmn-spec/elements/events.md) |
| Data split/assemble, output visibility barrier | [multi-instance.md §Data semantics](../bpmn-spec/semantics/multi-instance.md) |
| Compensation ordering | [multi-instance.md §Compensation](../bpmn-spec/semantics/multi-instance.md) |

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

Compensation (§2.10) is out of this rollout; it is owned by ADR-026 v.1.

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

**v.3 rollout (§2.13–§2.14).** The slices above landed. v.3 changes what
executes a node, so it re-lands them on the executor model — smallest-first,
each its own SRD and PR, the suite green at every step:

1. **The executor and its contract** — the interface, the node executor, and
   the decorator implementing it, proven on a leaf loop with no waits, where
   the observable behaviour must not move at all.
2. **The waiting instance** — registration under the decorator's identity, and
   with it the retirement of the engine's refusal of an iterated waiting
   activity.
3. **The composite kinds** — the sub-process and call executors, including the
   child-to-ordinal mapping a recovered iterated Call Activity needs.
4. **The record and the surfaces** — the durable executor set, the iteration
   view on the token (ADR-013's bump), and the iteration section on the
   incident with per-ordinal retry (ADR-036's bump).

The order is deliberate: each step is observable on its own, and the two that
change a public surface come last, after the model underneath them is proven.

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
  [multi-instance.md](../bpmn-spec/semantics/multi-instance.md),
  [activities.md](../bpmn-spec/elements/activities.md),
  [events.md](../bpmn-spec/elements/events.md)

---

## Open questions

None.

---

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-19 | Ruslan Gabitov | Initial draft — Standard Loop & Multi-Instance iteration model: per-iteration isolation by a mechanism fitting the activity kind (in-place fresh-frame for a leaf Task, per-iteration child scope for a composite, distinct per-instance scope for parallel MI), cardinality (expression \| collection), sequential/parallel sequencing, split/assemble data mediator with visibility barrier, completion condition, full `behavior` event-throwing (All/None/One/Complex), engine-convention runtime attributes; MI compensation deferred to the future Transaction work. |
| v.2 | 2026-07-21 | Ruslan Gabitov | Added §2.12 — composite iteration runs on the activity's own off-loop execution (an *iteration decorator*), not under control code on the per-instance loop goroutine; the decorator requests scope operations from the single-writer loop via a request/response protocol (ADR-017 v.1 invariant preserved), sequential/parallel become its two driving strategies, and the §2.8 behavior throw becomes an ordinary off-loop emit with a deterministic boundary catch (unimplementable correctly on the v.1 model). Semantics §2.1–§2.11 unchanged; only *who drives* composite iteration moves. Forward-pointers added to §2.2/§2.5/§2.8; execution-model alternatives added to §4; v.2 rework consequences/rollout added to §5/§7. Leaf-task loops unchanged. The SRDs that landed the loop-goroutine-driven composite model are superseded and deleted when the re-landing completes. |
| v.3 | 2026-08-10 | Ruslan Gabitov | **Sharpens the node execution model** (§2.13), one model for every node kind — simple node, inline Sub-Process, Call Activity — and their conjunction with loop characteristics, motivated by a construct the engine refuses today: an iterated activity that WAITS. The **node executor** runs one instance of an activity and owns whatever that instance awaits (an event subscription, a child scope's drain, a child instance); a **decorator** holds N and implements the same interface, closing the composition, so a track drives one executor and cannot tell how many instances are behind it — a track means *a token walking a path* again. The decorator is the node's single registered EVENT processor (one owner, one dispatch point, one subscription per waiting instance; the hub's differentiation by correlation untouched, ADR-006 v.5 §2.9) — the seam whose absence made the waiting case unbuildable. Residency asks each executor WHAT IT AWAITS rather than whether it is running, which is what keeps an iterated Sub-Process dehydratable; hydration re-arms every waiting instance BEFORE accepting a delivery — not against loss (the broker buffers an unmatched envelope and delivers it when a subscription appears, per its conformance suite) but against a partially armed set matching an envelope into the WRONG instance; the executor set is recorded, frames are not (recomputable from the ordinal, §2.4 fixing cardinality once); boundary arming stays at activity level and positional assembly stays with the decorator. A call executor owns its child instance under the parent linkage the engine already records (ADR-033 v.5 §2.10, ADR-023 v.3 §2.7): under iteration the caller owns N children, so the record maps child to ORDINAL or positional assembly binds the right output into the wrong slot, and cancelling an iteration terminates a durable child instance. Four accepted decisions change with it — **§2.2** replaces v.1's "parallel Multi-Instance always needs a distinct per-instance scope" with ONE isolation rule (a frame per iteration always, a scope only where the activity is a scope host); **§2.9.1** gives an iterated activity one token carrying its iteration state, the previous N-tokens reporting the mechanism and telling an operator how many instances were parked but never which, and nothing at all for a sequential loop; **§2.12**'s composite-only scope widens to every iterated activity; **§2.14** keeps failure at the granularity of execution — an incident carries an iteration section of the same shape §2.9.1 puts on the token, is retried alone, and cannot be completed around (positional assembly would publish a collection with a hole), with the cardinality/collection expressions and the decorator's own post-start work staying activity-level, the latter resuming against the recorded instances. Consequences accepted: the refusal retires, a parallel leaf loses its per-instance scopes, and the durable record carries the executor set. Contract changes named for their owners: the token's iteration field (ADR-013), the incident's iteration section and its track-keyed retry unit incl. "re-run the whole Call Activity" for a failed child (ADR-036). New **§2.6.1** decides iteration RESULT semantics, which §2.2's frame rule alone does not settle — a frame bounds an execution, but its commit target is the enclosing scope, so isolating an execution is not hiding its writes (§2.2 corrected). Default: **last write wins**, which makes a sequential iteration a **reduce** (already the Standard Loop's de-facto behaviour, previously implicit) and makes a parallel MI order-dependent for undeclared writes — documented as a property, not hidden. Three opt-in deterministic strategies: **array** by ordinal (the spec's collection for MI, an engine extension for a loop), **map** by a key **expression** evaluated in the completing instance's frame (an engine extension; the User Task assignee is the motivating case, unknown until the task is claimed), and **reduce** named explicitly. An empty key refuses; a duplicate key overwrites by default with **ErrorOnKeyRewrite** making it a fault — permissive by default because the loss is detectable against §2.9.2's total, strict on request for a fan-out where each participant must answer once. New **§2.9.2** publishes EVERY iteration value in the reserved read-only RUNTIME source — the five BPMN-named attributes (`RUNTIME/loopCounter`, `RUNTIME/numberOf*`) as well as the maps `ITERATIONS` (activity id → kind/total/completed/terminated) and `ITERATION_OWNERS` (activity id → ordinal → actual owner). The counters were ordinary scope data, so a model could overwrite one and read its own value back from an expression indistinguishable from the engine's; leaving them out of the tool the project already built against exactly that would keep one rule with an exception on the most-read names. Naming rule: a value the standard names keeps the standard's spelling (a pure prefix migration), a value the engine invented follows the engine's convention. Two consequences stated rather than discovered: it is a **breaking change** for models reading the bare names — measured at 14 Go files including three runnable examples, plus two guides — and it requires the runtime source to know WHICH execution is asking, since `loopCounter` differs per instance while the supplier is handed only a name; the reader already holds the asking execution's frame, and that seam is **ADR-010 v.2**'s to change. A model declaring a colliding property name now refuses at BUILD time, naming the element, rather than silently shadowing the engine's value and surfacing as a wrong answer three nodes later. Maps rather than a name per activity, because the RUNTIME name set is closed; keying by activity id is also what lets them **outlive the activity**, where a frame dies with its execution and a counter with its token. §2.9's BPMN-named variables keep their names and addresses — relocating them would break every existing expression — and stay writable, a hole named here and left to ADR-010 v.2, since write-protecting reserved data names is the data model's decision. The performer register gains its iterated-case rule: it keeps the LAST completer for compatibility, and `ITERATION_OWNERS` is the honest per-ordinal source — without which an iterated User Task would report one of N performers arbitrarily the moment the construct becomes buildable. Semantics §2.1–§2.11 otherwise unchanged. Three pre-existing claims refreshed at the bump, since a version re-asserts the whole document as current: **§2.10**'s "gobpm has no compensation substrate yet" was false — ADR-026 v.1 owns compensation and already states the per-instance snapshot rule for an iterated activity, so the deferral is replaced by the obligation this ADR owes it, now met through the ordinal rather than the per-instance scope §2.2 removes; **§2.5**'s "distinct per-instance scopes" contradicted the revised §2.2 and becomes "distinct per-instance execution contexts"; and **§7**'s rollout, whose slices have all landed, gains the v.3 sequence (executor contract → the waiting instance → the composite kinds → the record and the public surfaces, the surface-changing steps last). |
