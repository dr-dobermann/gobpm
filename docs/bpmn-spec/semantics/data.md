# Data Semantics

_Source: BPMN 2.0 §8.4.10 (ItemDefinition), §10.4 (Items and Data), §10.4.2 (Execution Semantics for Data), §13.3.2 (data binding in Activity Lifecycle)._

This file covers the data-flow model for executable BPMN: what data elements exist, where they live, how they're typed, and exactly when DataAssociations fire during activity execution.

## 1. The item-aware hierarchy

All BPMN data-carrying elements inherit from `ItemAwareElement`. The element holds (or conveys) **items** whose type is described by an `ItemDefinition`.

```
                BaseElement
                    ^
                    |
              ItemAwareElement
                    ^
        +-----------+-----------+-----------+--------+--------+
        |           |           |           |        |        |
     DataObject  DataStore   Property   DataInput  DataOutput …Reference variants
```

### ItemAwareElement attributes (§10.4.1, Table 10.51)

| Attribute | Type / Card | Purpose |
|---|---|---|
| `itemSubjectRef` | `ItemDefinition [0..1]` | Type of the item stored / conveyed. MAY be omitted ("under-specified") if modeler doesn't want to specify structure. |
| `dataState` | `DataState [0..1]` | Optional state qualifier on the data (e.g. `Draft`, `Approved`). State value semantics are **out of scope** of the standard — engines / domains define their own. |

### ItemDefinition (§8.4.10, Table 8.47)

Lives in `Definitions` (global scope, reusable across the model).

| Attribute | Type / Card | Default | Purpose |
|---|---|---|---|
| `itemKind` | `ItemKind { Information \| Physical }` | `Information` | Nature of the item. Physical items are non-operational per §13.1 — engines MAY ignore. |
| `structureRef` | `Element [0..1]` | — | The concrete data structure (typically an XSD complex type or element). |
| `import` | `Import [0..1]` | — | External schema location for the structure. |
| `isCollection` | `boolean` | `false` | True if the type represents a collection. If `true` but `structureRef` is not a collection type, the model is invalid. |

Default `typeLanguage` for the structure is set on `Definitions` and defaults to XML Schema (`http://www.w3.org/2001/XMLSchema`).

## 2. Data element catalog

### DataObject (§10.4.1)

The primary visual data variable inside a process flow.

- **MUST** be contained in `Process` or `SubProcess`.
- Lifecycle is tied to the parent: instantiated when parent is, disposed when parent is.
- Accessibility: parent + siblings + their children (including DataObjectReferences referencing it).
- **Cannot specify a `DataState`.** If you need to show the same data in different states, use multiple `DataObjectReference`s — each can carry its own `DataState`.
- `isCollection` (on DataObject): if `itemSubjectRef` is set, MUST match the referenced ItemDefinition's `isCollection`.

### DataObjectReference (§10.4.1, Table 10.53)

Visual pointer to a DataObject. Used to:
- Avoid spaghetti wiring (multiple appearances of one DataObject in a diagram).
- Show the same DataObject in different `DataState`s at different points in the process.

| Attribute | Type / Card | Purpose |
|---|---|---|
| `dataObjectRef` | `DataObject [1]` | The underlying DataObject. |

DataObjectReferences cannot specify `itemSubjectRef` themselves (they inherit it from the referenced DataObject).

### DataStore (§10.4.1, Table 10.55) + DataStoreReference

Persistent storage that **outlives the Process instance**.

| Attribute | Type / Card | Default | Purpose |
|---|---|---|---|
| `name` | `string` | — | Descriptive name. |
| `capacity` | `integer [0..1]` | — | Capacity. Ignored if `isUnlimited=true`. |
| `isUnlimited` | `boolean` | `false` | True → capacity unbounded (overrides `capacity`). |

- `DataStore` lives in `Definitions` (RootElement). Globally reusable.
- `DataStoreReference` is the visual ItemAwareElement appearing in flow scope; it carries `dataStoreRef → DataStore`.
- Data flowing into/out of a `DataStoreReference` effectively flows into/out of the global `DataStore`.

### Property (§10.4.1, Table 10.57)

Hidden, non-visual container for data attached to a `FlowElement` — specifically a `Process`, `Activity`, or `Event`.

| Attribute | Type / Card | Purpose |
|---|---|---|
| `name` | `string` | Property name. |

- **MUST** be contained in a `FlowElement`.
- Lifecycle tied to parent FlowElement instance.
- Accessibility: parent + (if parent is Process/SubProcess) immediate children including their nested children. Used for engine-internal state (loop counters, temporary context).
- Property of a `Process A` is accessible by Process A + all nested Activities + Sub-Processes + their nested Activities. Property of a `Sub-Process A` is accessible by Sub-Process A + its immediate children only.

### DataInput / DataOutput (§10.4.1, Tables 10.59, 10.60)

Declared interface I/O for an Activity or CallableElement. Visible on the diagram (small unfilled arrow = input, filled = output).

**Containment rules (§10.4.1, p210):**
- Only `Tasks` and `CallableElements` (Processes, GlobalTasks) MAY define DataInputs/DataOutputs (via their `InputOutputSpecification`).
- Embedded `SubProcess`es MUST NOT define DataInputs/DataOutputs directly — but MAY do so indirectly via `MultiInstanceLoopCharacteristics`.

**DataInput attributes (Table 10.59):**

| Attribute | Type / Card | Default | Purpose |
|---|---|---|---|
| `name` | `string [0..1]` | — | Descriptive name. |
| `inputSetRefs` | `InputSet [1..*]` | — | Derived. The InputSets this DataInput is part of. |
| `inputSetwithOptional` | `InputSet [0..*]` | — | InputSets in which this DataInput MAY be "unavailable" at Activity start. |
| `inputSetWithWhileExecuting` | `InputSet [0..*]` | — | InputSets in which this DataInput MAY be evaluated WHILE the Activity is executing. |
| `isCollection` | `boolean` | `false` | If `itemDefinition` referenced, MUST equal that ItemDefinition's `isCollection`. |

**DataOutput attributes (Table 10.60):** mirror DataInput with `outputSetRefs`, `outputSetwithOptional`, `outputSetWithWhileExecuting`, `isCollection`.

**Additional `optional` attribute** (text, p213): defines if a DataInput is valid even if its state is "unavailable". Default `false`. If `true`, Activity execution will not begin until a value is assigned via DataAssociations.

## 3. InputOutputSpecification (§10.4.1)

Aggregates I/O contract for a Task or CallableElement. Activity has 0..1 `ioSpecification`.

**Attributes (Table 10.58):**

| Attribute | Type / Card | Notes |
|---|---|---|
| `inputSets` | `InputSet [1..*]` | MUST define at least one InputSet. |
| `outputSets` | `OutputSet [1..*]` | MUST define at least one OutputSet. |
| `dataInputs` | `DataInput [0..*]` | Optional. Ordered set. If empty → no data required to start the Activity. |
| `dataOutputs` | `DataOutput [0..*]` | Optional. Ordered set. If empty → no data required to finish the Activity. |

## 4. InputSet and OutputSet (§10.4.1, p217–220)

### InputSet (Table 10.61)

A named collection of DataInputs that together constitutes one valid input mode for an Activity.

| Attribute | Type / Card | Purpose |
|---|---|---|
| `name` | `string [0..1]` | — |
| `dataInputRefs` | `DataInput [0..*]` | DataInputs that collectively make up this requirement. |
| `optionalInputRefs` | `DataInput [0..*]` | Subset of `dataInputRefs` that MAY be "unavailable" when Activity starts. MUST NOT reference DataInputs not listed in `dataInputRefs`. |
| `whileExecutingInputRefs` | `DataInput [0..*]` | Subset of `dataInputRefs` that MAY be evaluated WHILE the Activity is executing. MUST NOT reference DataInputs not listed in `dataInputRefs`. |
| `outputSetRefs` | `OutputSet [0..*]` | **IORule pairing.** Specifies which `OutputSet` is expected to be produced when this InputSet became valid. Paired with `inputSetRefs` on OutputSet. Replaces `IORules` from BPMN 1.2. |

Notes:
- An InputOutputSpecification MUST have at least one InputSet.
- A single DataInput MAY belong to multiple InputSets but MUST always be referenced by at least one.
- An **"empty" InputSet** (no `dataInputRefs`) signifies the Activity requires no data to start.
- **InputSet order is significant** — the order of inclusion in `InputOutputSpecification` is the evaluation order.

### OutputSet (Table 10.62)

Mirror of InputSet for produced data.

| Attribute | Type / Card | Purpose |
|---|---|---|
| `name` | `string [0..1]` | — |
| `dataOutputRefs` | `DataOutput [0..*]` | DataOutputs that MAY collectively be output. |
| `optionalOutputRefs` | `DataOutput [0..*]` | Subset of `dataOutputRefs` that need NOT be produced when Activity completes. |
| `whileExecutingOutputRefs` | `DataOutput [0..*]` | Subset of `dataOutputRefs` that MAY be produced while the Activity is executing. |
| `inputSetRefs` | `InputSet [0..*]` | **IORule pairing.** Specifies which InputSet has to become valid to expect this OutputSet. Paired with `outputSetRefs` on InputSet. |

- An OutputSet MUST be defined; an "empty" OutputSet means the Activity produces no data.
- Which OutputSet is actually produced is determined by the Activity implementation (Service execution, User decision, etc.).

## 5. DataAssociation (§10.4.1, p220–223)

Moves data between item-aware elements. `BaseElement` contained by an `Activity` or `Event`. **Tokens do NOT flow along DataAssociations** — they have no direct effect on control flow.

**Two concrete subtypes:**
- `DataInputAssociation` — target is a DataInput contained in an Activity; sources are item-aware elements accessible in current scope (DataObject, Property, Expression).
- `DataOutputAssociation` — source is a DataOutput contained in an Activity; target is any item-aware element accessible in current scope.

**Attributes (Table 10.63):**

| Attribute | Type / Card | Purpose |
|---|---|---|
| `sourceRef` | `ItemAwareElement [0..*]` | Source(s) — MUST be ItemAwareElement(s). |
| `targetRef` | `ItemAwareElement [1]` | Target — MUST be ItemAwareElement. |
| `transformation` | `Expression [0..1]` | Optional transformation Expression. |
| `assignment` | `Assignment [0..*]` | Zero or more single-element assignments. |

**Constraint on types:**
- `ItemDefinition` of `sourceRef` and `targetRef` MUST match, **OR** the DataAssociation MUST have a `transformation` Expression that transforms source ItemDefinition into target ItemDefinition.

### Execution semantics of a single DataAssociation (§10.4.2, p225)

The execution of ANY DataAssociation MUST follow these rules:

1. **If `transformation` specified:** evaluate the Expression, copy result to `targetRef`. This **completely replaces** the previous value of the target. (Sources are used as scope-availability gate but need not be referenced inside the Expression.)
2. **For each `assignment`:**
   - Evaluate `from` Expression → obtain *source value*.
   - Evaluate `to` Expression → obtain *target element* (any element in context or sub-element of it, e.g. a DataObject or a field of one).
   - Copy *source value* to *target element*.
3. **If neither `transformation` nor `assignment` specified:** simple copy of `sourceRef` value into `targetRef`. **Only ONE `sourceRef` is allowed in this case.**

### Availability gating

- "Sources are used to define if the data association can be _executed_."
- If ANY source is in state `unavailable`, the DataAssociation CANNOT execute.
- The Activity or Event where the DataAssociation is defined MUST wait until the unavailable condition is resolved.

### Assignment (Table 10.64)

Used for mapping a single sub-element of a structure instead of a whole-payload copy.

| Attribute | Type / Card | Purpose |
|---|---|---|
| `from` | `Expression [1]` | Evaluates the source. |
| `to` | `Expression [1]` | Defines the actual Assignment operation and locates the target element. |

Default `Expression` language is from `Definitions.expressionLanguage`; can be overridden on each `Assignment`.

## 6. Activity execution semantics for data (§10.4.2)

This is the precise wiring of DataAssociations into the activity lifecycle ([../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)).

### InputSet selection (Ready → Active)

When an element with an `InputOutputSpecification` is ready to begin execution (triggered by Sequence Flow OR an Event being caught):

1. The defined InputSets are evaluated **in declaration order** within the `InputOutputSpecification`.
2. For each InputSet, the data inputs it references are evaluated:
   - ALL DataAssociations whose target is one of those DataInputs are evaluated.
   - If ANY source of those DataAssociations is `unavailable` → the InputSet is `unavailable` → move to next InputSet.
3. The **first** InputSet where all required DataInputs are `available` is selected.
4. ALL DataAssociations targeting DataInputs of the selected InputSet execute — filling the Activity's DataInputs from process-scope DataObjects / Properties.
5. The Activity transitions Ready → Active.

If NO InputSet is "available" → execution **waits** until the condition is met. (Timing/frequency of re-evaluation is OUT OF SCOPE of the spec — implementations choose.)

### OutputSet execution (Completing → Completed)

When the Activity finishes execution:

1. The defined OutputSets are checked in declaration order.
2. The first available OutputSet is selected.
3. ALL DataAssociations whose sources are any DataOutputs of that OutputSet execute — copying values from Activity DataOutputs back into the container's context (DataObjects, Properties, etc.).

**Runtime exception** (§13.3.2): if no OutputSet becomes available, the engine throws a runtime exception.

### IORule check (§13.3.2)

If `outputSetRefs` is set on the InputSet that started the Activity, the chosen OutputSet at completion is checked: it MUST be one of the OutputSets the originating InputSet's `outputSetRefs` allows. **Runtime exception** if the pairing is violated.

## 7. Event DataAssociations (§10.4.2, p224)

Events have ONE set of DataAssociations (vs Activities' two). Usage depends on direction:

### Throw Events

- When activated, ALL `DataInputAssociation`s of the Event execute, filling the Event's DataInputs from context.
- DataInputs are then copied to elements thrown by the Event (Message, Signal, etc.).
- **Events have no InputSets** — execution NEVER waits.

### Catch Events

- When activated, the Event's DataOutputs are filled with the element that triggered the Event (the incoming Message / Signal / etc.).
- ALL `DataOutputAssociation`s of the Event execute, pushing values into context (DataObjects, Properties).
- **Events have no OutputSets.**

### Process-level Start / End Events (special case for Call Activity + Message Flow)

To allow invoking a Process from both a Call Activity and via Message Flow:

- **Start Event:** the DataInputs of the **enclosing Process** are available as TARGETS to the Event's `DataOutputAssociation`s. Process DataInputs can be filled using the elements that triggered the Start Event.
- **End Event:** the DataOutputs of the **enclosing Process** are available as SOURCES to the Event's `DataInputAssociation`s. End Event resulting elements can use Process DataOutputs as sources.

## 8. Task-type-specific data mapping (§10.4.1, p216)

Cross-cuts with [tasks.md](tasks.md). Key constraints:

| Task type | I/O constraint |
|---|---|
| **ServiceTask** with `operationRef` | MUST have one `Message Data Input` with `itemDefinition` matching the Operation's `inMessageRef` Message. If Operation defines output Messages, MUST have one `DataOutput` matching `outMessageRef`. |
| **SendTask** | MUST have at most one `InputSet` and at most one `DataInput`. If `DataInput` present, its `itemDefinition` MUST equal the associated Message's. If absent, Message is sent without payload data. |
| **ReceiveTask** | MUST have at most one `OutputSet` and at most one `DataOutput`. If `DataOutput` present, its `itemDefinition` MUST equal the associated Message's. If absent, Message payload does not flow into the Process. |
| **UserTask / ScriptTask** | Access to its DataInput, DataOutput, and data-aware elements in scope. |
| **CallActivity** | DataInputs / DataOutputs are mapped to corresponding elements in the CallableElement **without any explicit DataAssociation** — direct positional mapping. |

### Event data binding (p217)

If any `EventDefinition` is associated with an item-bearing element (Message, Escalation, Error, Signal):

- For multiple `EventDefinition`s on one Event: MUST have one DataInput (throw) or one DataOutput (catch) **per** EventDefinition. The **order** of `EventDefinition`s and the order of DataInputs/DataOutputs determines the correspondence.
- For each `EventDefinition` ↔ DataInput/DataOutput pair: the DataInput/DataOutput's `itemDefinition` MUST equal the one defined by the Message/Escalation/Error/Signal on the EventDefinition. If absent, payload does not flow.

## 9. Engine implementation notes

- DataAssociation evaluation is **synchronous to lifecycle transitions** — there's no parallel data plane. The activity blocks on Ready→Active until its chosen InputSet's DataAssociations all complete, and the outgoing tokens are not emitted (Completed→outgoing flows) until Completing→Completed's OutputSet DataAssociations all complete.
- **"Unavailable" is a first-class state**, not just an empty value. Engines need to track availability separately from value content for DataObjects and Properties — at minimum to gate DataInputAssociation execution.
- **Snapshot vs reference:** the spec uses "copy" language consistently. A DataAssociation `source → target` copies the source's current value to the target; later changes to the source do NOT propagate. This is true both for the simple-copy case and the transformation case ("This operation replaces completely the previous value of the targetRef element").
- **DataObject collections** require the `MultiInstanceLoopCharacteristics` mediator pattern for per-instance extraction — see [multi-instance.md](multi-instance.md). The data-extraction mechanism is "left under-specified" by the spec.
- **DataState semantics are out of scope.** Engines/domains define their own state values and what they mean. The spec only mandates the structural attachment.
- **XPath bindings (§10.4.3)** for accessing DataObjects, DataInputs, DataOutputs, Properties, and instance attributes via XPath extension functions — only relevant if the engine supports XPath as an `expressionLanguage`. Functions: `getDataObject`, `getDataInput`, `getDataOutput`, `getProcessProperty`, `getActivityProperty`, `getEventProperty`, `getProcessInstanceAttribute`, `getActivityInstanceAttribute`.

## Cross-references

- Activity lifecycle (when InputSet/OutputSet evaluation happens): [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)
- Task-type-specific behavior: [tasks.md](tasks.md)
- Multi-instance data flow (loopDataInput / loopDataOutput / inputDataItem / outputDataItem): [multi-instance.md](multi-instance.md)
- Event handling (catch/throw Events with data): [events.md](events.md)
- Structural attributes catalogue: [../elements/data.md](../elements/data.md)
- Service interface (Interface / Operation / Message data binding for Service/Send/Receive Tasks): [../elements/service-interfaces.md](../elements/service-interfaces.md)
