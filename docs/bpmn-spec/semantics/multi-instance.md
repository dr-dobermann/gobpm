# Loop and Multi-Instance

_Source: BPMN 2.0 §13.3.6 + §13.3.7 (spec p432–433)._

`StandardLoopCharacteristics` and `MultiInstanceLoopCharacteristics` are markers attached to an activity (`Task`, `SubProcess`, `CallActivity`) to make it execute multiple times. Reference structural definitions in [../elements/activities.md](../elements/activities.md).

## Standard Loop (§13.3.6)

Wraps an inner Activity executed **sequentially** multiple times.

| Attribute | Meaning |
|---|---|
| `loopCondition` | Boolean `Expression`. Loop continues while `true`. |
| `testBefore` | `true` = pre-tested loop (check before each iteration). `false` (default) = post-tested loop. |
| `loopMaximum` | Optional integer. Maximum iteration count. If unset → unbounded. |

**Workflow pattern:** WCP-21 Structured Loop.

## Multi-Instance Activity (§13.3.7)

Wraps an Activity to execute multiple times — sequentially OR in parallel.

### Cardinality

The number of instances is determined ONCE at activation, by one of:

| Source | Mechanism |
|---|---|
| `loopCardinality` | Integer-valued `Expression`. Evaluated once. |
| Collection-based | Cardinality = size of the collection-valued data item referenced by `loopDataInputRef`. |

### Sequencing

| `isSequential` | Behavior |
|---|---|
| `true` | New instance generated only after the previous instance has completed. |
| `false` (default) | All instances generated and execute in parallel. |

### Completion

- `completionCondition`: boolean `Expression` evaluated every time an instance completes.
  - When `true`: the **remaining instances are canceled** and the MI Activity itself completes.
  - When `false`: instance is counted, remaining continue.
- Without a `completionCondition`, MI Activity completes when all instances have completed.

### Event throwing on completion — `behavior` attribute

Determines if and when an Event is thrown from an MI Activity about to complete:

| Value | Behavior |
|---|---|
| `none` | An `EventDefinition` is thrown for ALL instances completing. |
| `one` | An `EventDefinition` is thrown upon FIRST instance completing. |
| `all` | No Event is ever thrown. |
| `complex` | `ComplexBehaviorDefinition`s are consulted — see below. |

For `none` and `one`: the `EventDefinition` is referenced from `MultiInstanceLoopCharacteristics` via `noneEvent` / `oneEvent` associations. It implicitly carries the current runtime attributes of the MI Activity (the `ItemDefinition` of these `SignalEventDefinition`s is implicitly the MI Activity's runtime attributes).

### ComplexBehaviorDefinition (for `behavior=complex`)

- Holds multiple `ComplexBehaviorDefinition` entries, each consisting of:
  - A boolean condition (a `FormalExpression`).
  - An `Event` which is an `ImplicitThrowEvent`.
- Whenever an Activity instance completes, ALL `ComplexBehaviorDefinition` conditions are evaluated.
- Each one whose condition evaluates `true` causes its associated Event to be thrown.
- A single instance completion can therefore lead to multiple different Events being thrown.
- The `Event`s can be caught on the **boundary of the MI Activity** — allowing different flows depending on progress state.

**Available variables** inside `completionCondition`, `condition` in `ComplexBehaviorDefinition`, and `DataInputAssociation` of an `Event` in `ComplexBehaviorDefinition`:
- MI Activity instance runtime attributes
- `loopDataInput`, `loopDataOutput`, `inputDataItem`, `outputDataItem` (from `MultiInstanceLoopCharacteristics`)

## Data semantics for Multi-Instance

Multi-Instance Activities process data **collections**:

```
                                  Process scope
                                  
                         DataObject (collection)
                              |  (DataInputAssociation)
                              v
                    MI Activity's loopDataInput
                              |  (split per instance)
                              v
                    inputDataItem (per instance)
                              |  (DataInputAssociation)
                              v
                     Inner Activity DataInput
                              |
                              .... (executes)
                              |
                     Inner Activity DataOutput
                              |  (DataOutputAssociation)
                              v
                    outputDataItem (per instance)
                              |  (assembled)
                              v
                    MI Activity's loopDataOutput
                              |  (DataOutputAssociation)
                              v
                         DataObject (collection)
                                  Process scope
```

### Constraints

- The MI Activity's `loopDataInput` MUST be linked to a process-scope `DataObject` via `DataInputAssociation`.
- The source `DataObject` MUST be a collection (visual: marked with the MI indicator — three-bar).
- The items of the `loopDataInput` collection determine the number of instances (whether sequential or parallel).
- Inner instances are created; data values are extracted and assigned to each instance via `inputDataItem` → `DataInputAssociation` → inner `DataInput`.
- The extraction mechanism is **under-specified** by the spec — typically requires a special-purpose mediator that handles both extraction and any necessary data transformation.
- Output: each instance produces a value in its `DataOutput`. This is passed to a corresponding `outputDataItem`. The mechanism for updating `loopDataOutput` collection is also under-specified — typically the same kind of mediator.
- The `loopDataOutput` collection in Process scope **should not be accessible** until all items have been written to it (could otherwise be accessed by a concurrent Activity, and control flow through token passing cannot guarantee the collection is fully written before access).

## Compensation of Multi-Instance

The MI Activity is compensated only if ALL its instances completed successfully — see [compensation.md](compensation.md).

For Loop / sequential MI: instances compensated in **reverse order** of forward execution.
For parallel MI: instances can be compensated in parallel.

## Workflow patterns supported

- Standard Loop: WCP-21 Structured Loop.
- Multi-Instance: WCP-21 Structured Loop, plus Multiple Instance Patterns WCP-13, WCP-14, WCP-34, WCP-36.

## Cross-references

- Activity lifecycle (MI does not replace the lifecycle; each instance follows it): [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)
- Compensation per-instance snapshot: [compensation.md](compensation.md)
- Structural attributes of MultiInstanceLoopCharacteristics / ComplexBehaviorDefinition: [../elements/activities.md](../elements/activities.md)
- Data flow basics: see §10.4 in the spec PDF (not yet in this KB)
