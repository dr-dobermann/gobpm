# Instance Attributes

_Source: BPMN 2.0 §10.3.4.1 (User Task), §10.3.8 (Loop Characteristics), §10.4.3
(expression bindings). Authored from the spec text — **not** generated._

BPMN splits an element's attributes across two kinds of table:

- **Model attributes and model associations** — the design-time shape an XML definition
  serializes (`implementation`, `renderings`, …). These are what `elements/` documents.
- **Instance attributes** — runtime facts about a *live* element, which no XML definition
  contains because they do not exist until something is running.

**Why this page is hand-written.** `elements/` is generated from bpmn-moddle, which models
the **XML metamodel** — so it can only ever emit the first kind. Instance attributes are
invisible to it by construction. A reader checking `elements/activities.md` for a runtime
attribute will correctly find none and may wrongly conclude the standard defines none;
this page exists to prevent that. (It is exactly what happened once: the engine's own
human-interaction design initially concluded BPMN was silent on task ownership.)

---

## UserTask

> The User Task inherits the instance attributes of Activity (see Table 8.49). Table 10.14
> presents the instance attributes of the User Task element.

**Table 10.14 – User Task instance attributes**

| Attribute | Description / Usage |
|---|---|
| `actualOwner: string` | Returns the "user" who **picked/claimed** the User task and became the **actual owner** of it. The value is a literal representing the user's id, email address etc. |
| `taskPriority: integer` | Returns the priority of the User Task. |

**Engine notes:**

- `actualOwner` is the standard's own name for who currently *holds* a task, as distinct
  from the resource-assignment model (`Performer` / `HumanPerformer` / `PotentialOwner`,
  see [../elements/human-interaction.md](../elements/human-interaction.md)) which declares
  who *may* perform it. Eligibility and ownership are different values.
- The spec defines the **attribute** and the vocabulary — "picked/claimed" — but **no
  operations** for acquiring, releasing or transferring ownership, and no ownership states
  in the activity lifecycle ([../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md),
  §13.3.2). Those are engine decisions.
- `taskPriority` is implemented as a **reader** (`UserTask.TaskPriority()`), which is the whole
  of what the table specifies, and reported to the `TaskDistributor` on `TaskInfo`. The engine
  assigns the value no meaning — the standard supplies no scale, direction, default or
  behaviour that consumes it — and drives no decision from it. A **setter** exists
  (`WithTaskPriority`) and is an engine extension, registered in
  [SAD-001 §14.2](../../design/SAD-001-vision-and-architecture.md); no BPMN XML can set an
  instance attribute, which is why Camunda added `camunda:priority`.

## Activity

> The User Task inherits the instance attributes of Activity (see Table 8.49).

**This reference is an erratum in the specification.** Table 8.49 is *"Resource attributes and
model associations"* (§8.4.12) and has nothing to do with instance attributes. The set a
UserTask actually inherits is **Table 10.4 – Activity instance attributes** (§10.3, spec
p. 151), and it has exactly one row:

**Table 10.4 – Activity instance attributes**

| Attribute | Description / Usage |
|---|---|
| `state: string = None` | See Figure 13.2 ("The Lifecycle of a BPMN Activity") in Section 13.3.2 for permissible values. |

**Engine notes:**

- The inherited set is therefore a single attribute, and gobpm implements it as the activity
  lifecycle ([../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)),
  not as a separately stored field.
- An earlier revision of this page repeated the spec's misreference and said Table 8.49 was
  "not yet extracted". It was never extractable *as an instance-attribute table*, because it
  is not one.

## Loop Activity (Standard Loop)

**Table 10.27 – Loop Activity instance attributes** (§10.3.8, spec p. 189)

| Attribute | Description / Usage |
|---|---|
| `loopCounter: integer` | The LoopCounter attribute is used at runtime to count the number of loops and is automatically updated by the process engine. |

## Multi-instance Activity

**Table 10.30 – Multi-instance Activity instance attributes** (§10.3.8, spec p. 193)

| Attribute | Description / Usage |
|---|---|
| `loopCounter: integer` | Provided **for each generated (inner) instance** of the Activity. It contains the sequence number of the generated instance — if this value of some instance is *n*, the instance is the *n*-th instance that was generated. |
| `numberOfInstances: integer` | Provided **for the outer instance only**. The total number of inner instances created for the Multi-Instance Activity. |
| `numberOfActiveInstances: integer` | Outer instance only. The number of currently active inner instances. For a **sequential** MI Activity this value cannot be greater than 1; for a **parallel** one it cannot exceed `numberOfInstances`. |
| `numberOfCompletedInstances: integer` | Outer instance only. The number of already completed inner instances. |
| `numberOfTerminatedInstances: integer` | Outer instance only. The number of terminated inner instances. **The sum of `numberOfTerminatedInstances`, `numberOfCompletedInstances` and `numberOfActiveInstances` always sums up to `numberOfInstances`.** |

These are the variables §13.3.7 makes available to a `completionCondition`, to a
`ComplexBehaviorDefinition` condition and to the `DataInputAssociation` of its
event — see [multi-instance.md](multi-instance.md) — and they are what the
implicit `SignalEventDefinition` thrown on `behavior=none`/`one` carries.

**Engine notes:**

- gobpm binds the standard's names verbatim: `loopCounter` per instance and the
  four counts at the host (outer) scope, which is the inner/outer split the two
  tables prescribe.
- **The two clauses about `numberOfActiveInstances` cannot both hold for a
  SEQUENTIAL Multi-Instance mid-run**, and this is a tension in the table
  rather than in any engine. The cap says the value cannot exceed 1; the sum
  says terminated + completed + active equals the total. A sequential activity
  of five at its third pass has two completed and one running — satisfying the
  sum would need `active = 3`, which the cap forbids, and the not-yet-started
  instances belong to no category the table offers. For a **parallel**
  activity there is no tension: every instance exists from activation, so the
  sum holds throughout.
- gobpm honours the **cap**, publishing what is *currently running* — the
  attribute's own definition — so a sequential activity's sum is short by the
  not-yet-started remainder while it runs, and exact at any terminal state.
  Reading `active` as "outstanding" instead would satisfy the sum, break the
  cap, and make the attribute mean two different things depending on
  `isSequential`. For the parallel case the count is *derived* as
  `numberOfInstances − completed − terminated`, so the three cannot drift.
- **`loopCounter` is 0-based in gobpm; Table 10.30's wording is 1-based** ("if
  this value is *n*, the instance is the *n*-th generated"). Table 10.27 states
  no base for the Standard Loop. The 0-based choice is deliberate — it indexes
  the input collection and the output collection directly, which is what
  positional assembly needs — but it **is** a deviation on the MI side, and a
  model ported from another engine may read one lower than its author expects.
  Registered as an engine choice in
  [ADR-025 §2.11](../../design/ADR-025-activity-iteration-loop-and-multi-instance.md).
- The standard defines **no** runtime attribute naming the iteration *mode*.
  Sequentiality is observable only indirectly, through Table 10.30's own rule
  that `numberOfActiveInstances ≤ 1` for a sequential MI. Any published mode
  value is an engine extension, not a standard attribute.
- Neither table is reachable from `elements/activities.md`: the generator emits
  Table 10.29 (`MultiInstanceLoopCharacteristics` **model** attributes) and can
  never emit 10.27/10.30, for the reason stated at the top of this page.

---

## Reading an instance attribute from an expression

§10.4.3 defines XPath extension functions for instance attributes, including
`getActivityInstanceAttribute` — so the standard treats them as expression-accessible
values, not merely as engine internals. See [data.md](data.md) for the full function list.

## Cross-references

- Model attributes of the same elements: [../elements/activities.md](../elements/activities.md)
  (`UserTask` — `implementation`, `renderings`), generated.
- Resource assignment (who *may* act): [../elements/human-interaction.md](../elements/human-interaction.md)
- Loop / Multi-Instance execution semantics, and where these attributes are
  readable from: [multi-instance.md](multi-instance.md)
- Per-task execution behavior: [tasks.md](tasks.md)
- Activity lifecycle states: [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)
