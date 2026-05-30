# Sub-Process Variants

_Source: BPMN 2.0 §13.3.4 + §13.3.5 (spec p430–431)._

`SubProcess` is an activity that encapsulates an inner Process — modeled by Activities, Gateways, Events, and Sequence Flows. Once instantiated, contents execute as if in a normal Process. Sub-process completion follows the activity lifecycle ([../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)).

## Plain Sub-Process

### Instantiation

A Sub-Process is instantiated when reached by a Sequence Flow `token`. Two structural variants:

| Configuration | Token-routing behavior |
|---|---|
| Single empty Start Event + flows from it | Start Event receives the token on instantiation; sequence flows from it carry it onward. |
| No Start Event, with Activities/Gateways without incoming sequence flows | All such elements receive a token on instantiation. |

**Constraint:** A Sub-Process MUST NOT have non-empty Start Events (Message, Timer, Signal, etc.). Those belong to **Event Sub-Processes** (see below).

### Event-triggered Sub-Process (no incoming flow)

If the Sub-Process has no incoming sequence flows but contains `Start Event`s targeted by sequence flows _from outside_ — those external Start Events instantiate the Sub-Process when reached by a token. Multiple such Start Events are alternatives — each one creates its own instance.

### Completion

A Sub-Process instance completes when:
- No tokens remain inside, AND
- No nested Activity is still active.

### Abnormal termination

- **Terminate End Event** reached inside: Sub-Process abnormally terminated.
- **Cancel End Event** reached inside: Sub-Process abnormally terminated AND the enclosing transaction is aborted; control leaves via a Cancel intermediate boundary event attached to the Sub-Process.
- For all other End Event types: the End Event behavior is performed (Message sent, Signal sent, etc.).

## Call Activity

Invokes a reusable `CallableElement` — typically a global Process or `GlobalTask` variant.

- **If the callable is a Process:** the Call Activity has the same instantiation and termination semantics as a Sub-Process.
- The called Process MAY have non-empty Start Events (unlike a Sub-Process). When invoked via Call Activity, those non-empty Start Events are **ignored** — the empty Start Event is used. (Non-empty Start Events are alternatives for direct/independent invocation, not for the call-activity path.)

**Engine notes:** Call Activity is a reuse mechanism. The boundary between caller and callee is identical to a Sub-Process boundary from the perspective of token flow, error propagation, and compensation scoping.

## Event Sub-Process (§13.5.4)

A `SubProcess` with `triggeredByEvent=true`. Specialized handling — instantiated by an event, not by control flow.

- Always begins with a Start Event of a specific type (Message, Timer, Error, Escalation, Compensation, Signal, Conditional).
- **NOT instantiated by normal control flow.** Instantiated only when its Start Event triggers.
- **Self-contained:** MUST NOT be connected to the rest of the parent Sub-Process's sequence flows.
- Cannot have boundary events attached.
- Runs in the **context** of the parent Sub-Process (has access to its data).
- Can re-throw the triggering Event outside the parent's boundary (continuation).

### Interrupting vs non-interrupting

Determined by `isInterrupting` on the **Start Event** of the Event Sub-Process.

| `isInterrupting` | Effect on parent activity |
|---|---|
| `true` (interrupting) | Parent activity is interrupted. For MI parent, only the affected instance is cancelled. No new Event Handlers can be initiated in the parent after this. |
| `false` (non-interrupting) | Parent continues in parallel with the Event Sub-Process. Multiple non-interrupting handlers MAY be initiated, at different times. |

### Trigger constraints

- Only **one** interrupting Event Handler MAY be initiated for a given `EventDefinition` within a parent Activity at any time.
- Once the interrupting handler started, parent is interrupted; no new handlers (interrupting OR non-interrupting) may be initiated.
- Multiple **non-interrupting** handlers MAY be initiated concurrently.

### Effect on parent's lifecycle

- Triggered by an `Error` (interrupting): parent activity enters `Failing` state.
- Triggered by a non-error (interrupting, e.g. Escalation): parent activity enters `Terminating` state.
- During Failing/Terminating: running Event Handler can access parent's data context; NO new Event Handlers may start.

### Event Sub-Process completion

- Completes when all tokens reach an `End Event`.
- If the parent is in `Completing` state: it remains there until ALL active Event Sub-Processes complete. No new Event Sub-Processes can be initiated during Completing.

## Ad-Hoc Sub-Process (§13.3.5)

Loose-ordered activity container. Contents execute multiple times in an order constrained only by explicitly specified sequence flows.

**Containment constraints:**
- Contains: Activities, Sequence Flows, Gateways, Intermediate Events.
- MAY contain: Data Objects, Data Associations.
- Activities within an Ad-Hoc Sub-Process are NOT required to have incoming/outgoing sequence flows.
- Intermediate Events MUST have outgoing sequence flows (they can be triggered multiple times while Ad-Hoc Sub-Process is active).

**Operational semantics:**

- At any point, a **subset** of embedded inner Activities is `enabled`. Initially: all Activities without incoming sequence flows.
- One enabled Activity is selected for execution — typically by a Human Performer (not necessarily by the implementation).
- `ordering` attribute:
  - `sequential`: another enabled Activity can be selected only after the previous terminates.
  - `parallel`: another enabled Activity can be selected at any time. Allows multiple parallel instances of the same inner Activity.
- After each completion of an inner Activity, the `completionCondition` is evaluated.
  - `false`: enabled set is updated; new selections can occur.
  - `true`: Ad-Hoc Sub-Process completes.
    - If `cancelRemainingInstances=true` (default): running inner Activity instances are **canceled**.
    - If `cancelRemainingInstances=false`: Ad-Hoc Sub-Process waits for remaining instances to complete or terminate.

**Token-flow within:**

- When an inner Activity with outgoing sequence flows completes, tokens are produced on outgoing flows per `completionQuantity`.
- Resulting state may also have tokens on incoming flows of converging Parallel/Complex Gateways or Intermediate Events.
- Tokens propagated as far as possible — converging gateways execute until quiescence.
- An inner Activity is re-enabled when its incoming sequence flows have sufficient tokens (per `startQuantity`).

**Workflow pattern:** WCP-17 Interleaved Parallel Routing.

## Transaction Sub-Process

A Sub-Process variant (`Transaction`) with ACID-like semantics. Distinguished by:
- Reaching a **Cancel End Event** inside triggers transaction abort.
- A Cancel intermediate boundary event MAY be attached to the Transaction Sub-Process — control leaves through it on cancellation.

The execution semantics of the Transaction Sub-Process itself follow the plain Sub-Process semantics; the Cancel behavior is the distinguishing feature.

## Cross-references

- Activity lifecycle: [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)
- Event Sub-Process structural attributes: [../elements/activities.md](../elements/activities.md) (SubProcess with triggeredByEvent)
- Compensation in Sub-Processes: [compensation.md](compensation.md)
- Multi-instance / Loop wrappers on Sub-Processes: [multi-instance.md](multi-instance.md)
- Cancel End Event: [end-events.md](end-events.md)
