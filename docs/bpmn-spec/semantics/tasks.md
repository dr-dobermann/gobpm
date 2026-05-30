# Task Execution Semantics

_Source: BPMN 2.0 §13.3.3 (spec p430)._

Per-task-type execution rules. All tasks share the activity lifecycle ([../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)); this file documents what each task DOES during its Active state.

## ServiceTask

- Upon activation: data in the `inMessage` of the referenced `Operation` is assigned from data in the Service Task's `DataInput`.
- The `Operation` is invoked.
- On completion of the service: data in the Service Task's `DataOutput` is assigned from data in the `outMessage` of the `Operation`, and the Service Task **completes**.
- If the invoked service returns a **fault**, that fault is treated as an **interrupting error** and the activity **fails** (state: `Failing` → `Failed`).

**Engine notes:** the `operationRef` attribute on `ServiceTask` resolves the `Operation`. The `implementation` attribute is a string hint (e.g. `##WebService`, `##Unspecified`) for the invocation mechanism.

## SendTask

- Upon activation: data in the associated `Message` is assigned from data in the Send Task's `DataInput`.
- The `Message` is sent.
- The Send Task **completes**.

**Engine notes:** the destination of the message is determined by the messaging mechanism (operation reference, message flow at Collaboration level — though Collaboration is out of scope here, see [../conformance.md](../conformance.md)).

## ReceiveTask

- Upon activation: the Receive Task **begins waiting** for the associated `Message`.
- When the Message arrives: data in the Receive Task's `DataOutput` is assigned from data in the Message, AND the Receive Task **completes**.

### Correlation

- **Key-based correlation:** for a given `CorrelationKey`, only a single Receive Task instance can be active at a time. A Message matches at most one Process instance.
- **Predicate-based correlation:** the Message MAY be passed to multiple Receive Tasks simultaneously.

### Instantiating Receive Task

- If `instantiate=true` and the Receive Task has no incoming sequence flows, the Receive Task itself can start a new Process instance (acts like a Message Start Event).

## UserTask

- Upon activation: the User Task is **distributed** to the assigned person or group of people (per `HumanPerformer` / `PotentialOwner` / `Performer` / `Rendering` — see [../elements/human-interaction.md](../elements/human-interaction.md)).
- When the work has been done: the User Task **completes**.

**Engine notes:** distribution mechanism is implementation-defined. The spec does not mandate a specific task list / inbox structure.

## ManualTask

- Upon activation: the manual task is **distributed** to the assigned person or group.
- When the work has been done: the Manual Task **completes**.
- **Conceptual only** — a Manual Task is _never actually executed by an IT system_.

**Conformance note:** Manual Task is listed in §13.1 as a **non-operational** element. An engine conforming to Process Execution Conformance MAY ignore Manual Tasks (treat as no-op pass-through). Implementations MAY extend it to become operational — that is an optional extension to BPMN.

## BusinessRuleTask

- Upon activation: the associated business rule is **called**.
- On completion of the business rule: the Business Rule Task **completes**.

**Engine notes:** the spec does not mandate a rule engine binding. Typical wiring is to DMN.

## ScriptTask

- Upon activation: the associated **script** is invoked.
- On completion of the script: the Script Task **completes**.

**Engine notes:** the spec does not mandate a script language. The `scriptFormat` attribute on `ScriptTask` (MIME type) and `script` element carry the language and source.

## AbstractTask (Task base)

- Upon activation: the Abstract Task **completes** (immediately).
- **Conceptual only** — never actually executed by an IT system.

**Conformance note:** Abstract Task (the un-typed `Task` base) is listed in §13.1 as **non-operational**. An engine MAY treat it as a no-op pass-through. This is the bare `<bpmn:task>` with no type specialization.

## Common patterns

### Data flow on activation (Ready → Active)

For all task types: `InputSet` evaluation populates `DataInput`s via `DataInputAssociation`s before the task begins its specific behavior. See [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md) "Ready → Active (data binding)".

### Completion → outgoing tokens

On completion, the task transitions through `Completing → Completed`, then emits `completionQuantity` tokens on each outgoing sequence flow (acts as implicit Parallel Gateway for multiple outgoing). See [token-flow.md](token-flow.md).

### Faults during execution

Any task that raises an error (Service Task on service fault; Script Task on script exception; etc.) transitions `Active → Failing`. Recovery follows the standard error-handling chain — boundary error events, error event sub-processes, or unhandled propagation up the parent chain. See [events.md](events.md).

## Cross-references

- Activity lifecycle: [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)
- Structural attributes per task type: [../elements/activities.md](../elements/activities.md)
- Operation / Message: [../elements/service-interfaces.md](../elements/service-interfaces.md)
- Message correlation: [events.md](events.md), [../elements/correlation.md](../elements/correlation.md)
- User Task distribution: [../elements/human-interaction.md](../elements/human-interaction.md)
