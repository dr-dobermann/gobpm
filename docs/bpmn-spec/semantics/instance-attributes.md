# Instance Attributes

_Source: BPMN 2.0 §10.3.4.1 (User Task), §10.4.3 (expression bindings). Authored from the spec text — **not** generated._

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

---

## Reading an instance attribute from an expression

§10.4.3 defines XPath extension functions for instance attributes, including
`getActivityInstanceAttribute` — so the standard treats them as expression-accessible
values, not merely as engine internals. See [data.md](data.md) for the full function list.

## Cross-references

- Model attributes of the same elements: [../elements/activities.md](../elements/activities.md)
  (`UserTask` — `implementation`, `renderings`), generated.
- Resource assignment (who *may* act): [../elements/human-interaction.md](../elements/human-interaction.md)
- Per-task execution behavior: [tasks.md](tasks.md)
- Activity lifecycle states: [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)
