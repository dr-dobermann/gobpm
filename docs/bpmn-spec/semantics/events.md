# Event Handling

_Source: BPMN 2.0 §13.5.1–§13.5.4 (spec p439–441)._

Event behavior is defined per position (Start / Intermediate / End / Boundary) and per type (Message, Timer, Signal, Error, etc.). This file covers the position-level semantics. Per-type catalogue is in [../elements/event-definitions.md](../elements/event-definitions.md).

## Start Events (§13.5.1)

Handling = **starting a new Process instance** each time the Event occurs. Sequence flows leaving the Event are then followed as usual.

### Conversation-bound start

If the Start Event participates in a **Conversation** that includes other Start Events:

- A new Process instance is created ONLY IF no instance already exists for the specific Conversation (identified by correlation information of the Event occurrence).
- Otherwise the Event is routed to the existing instance.

### Start via Event-Based Gateway

Already covered in [gateways.md](gateways.md) (Event-Based Gateway) and [../state-machines/process-lifecycle.md](../state-machines/process-lifecycle.md). Recap:

- First matching Event creates a new Process instance.
- For Parallel Event-Based Gateway at start: subsequent matching Events route to the already-created instance (do not create new ones), as long as their correlation matches.
- Multiple groups of Event-Based Gateways may exist (each a "starting group"). One Event from each group needs to arrive. First creates the instance; subsequent ones join the existing instance.

## Intermediate Events (§13.5.2)

For Intermediate Catching Events:

- Handling = **waiting** for the Event to occur.
- Waiting starts when the Intermediate Event is **reached** by a token.
- Once the Event occurs: it is **consumed**.
- Outgoing sequence flows are followed as usual.

For Intermediate Throwing Events:

- Handling = **producing** the Event (Message sent, Signal sent, Escalation thrown, etc.).
- Then immediately followed by outgoing sequence flows.

### Correlation for catching Message Intermediate Events

Same as `ReceiveTask` correlation (see [tasks.md](tasks.md)):

- **Key-based:** Message matches at most one Process instance per `CorrelationKey`.
- **Predicate-based:** Message MAY be passed to multiple receivers.

## Boundary Events (§13.5.3)

Boundary events are attached to an Activity. Their handling depends on `cancelActivity`:

### Interrupting (`cancelActivity=true`)

- Consume the Event occurrence.
- **Cancel** the Activity to which the boundary event is attached.
  - For a multi-instance Activity: ALL its instances are cancelled.
- Execution follows the sequence flow connected to the boundary event.

### Non-interrupting (`cancelActivity=false`)

- Consume the Event occurrence.
- The Activity **continues execution** in parallel.
- Execution follows the sequence flow connected to the boundary event (a parallel branch).
- Only possible for **Message, Signal, Timer, Conditional** events.
- **NOT allowed** for Error events (error always interrupts).

### Message correlation on boundary

For boundary Message Intermediate Events: same correlation as Receive Tasks (key-based / predicate-based).

## Event Sub-Processes (§13.5.4)

An `Event Sub-Process` is a `SubProcess` with `triggeredByEvent=true`. See [sub-processes.md](sub-processes.md) for structural and lifecycle details.

### Trigger / scope

- Allow handling an Event **within the context of** a Sub-Process or Process.
- Begins with a Start Event of a specific type. The Start Event's `isInterrupting` flag determines interrupting vs non-interrupting handling.
- Created when its associated Start Event triggers — NOT by normal control flow.
- Self-contained: NOT connected to the parent's sequence flows. No boundary events on it.
- Runs in the **context** of the parent (has access to parent's data).
- MAY optionally re-trigger the Event outside the parent's boundary (continuation upward). In that case the Event Sub-Process performs its work; the Event is then re-thrown to the parent's boundary, which may itself trigger handlers (and may cancel the parent including running handlers).

### Initiation rules

- An Event Sub-Process becomes _initiated / Enabled / Running_ through the Activity to which it is attached.
- The Event Handler MAY only be initiated AFTER the parent Activity is `Running` (Active or Completing).

### Concurrency rules

- More than one **non-interrupting** Event Handler MAY be initiated concurrently. Multiple instances of the same handler are possible.
- Only ONE **interrupting** Event Handler MAY be initiated for a given `EventDefinition` within a parent Activity at any time.
- Once an interrupting Event Handler started: the parent is interrupted; no new Event Handlers can be initiated.

### Effect on parent

| Trigger | Parent Activity state transition |
|---|---|
| Interrupting Event Sub-Process triggered by Error (e.g. Escalation classified as error) | Parent → `Failing` |
| Interrupting Event Sub-Process triggered by non-error | Parent → `Terminating` |
| Non-interrupting Event Sub-Process triggered | No state change on parent |

The parent stays in Failing / Terminating until the interrupting Event Handler reaches a final state. During this time the handler can access the parent's data context; new Event Handlers MUST NOT be started.

### Completion conditions

- An Event Sub-Process completes when all tokens have reached an End Event (same shape as Sub-Process completion).
- If the parent is in `Completing`: the parent remains there until all contained active Event Sub-Processes have completed. No new Event Sub-Processes can be initiated while parent is in Completing.

## Message correlation (general)

Used by: Message Start Event, catching Message Intermediate Event, boundary Message Event, Receive Task, Message Event Sub-Process Start, message-instance routing.

Two modes:

- **Key-based (`CorrelationKey`):** a named set of `CorrelationProperty`s. At most one receiver per key active at a time. Message matches at most one Process instance.
- **Predicate-based:** the Message may be passed to multiple receivers simultaneously.

Detailed mechanism: see [correlation.md](correlation.md) (spec §8.4.2). Structural elements: [../elements/correlation.md](../elements/correlation.md).

## Cross-references

- Position-by-type matrix (which event definitions are allowed at which positions): [../conformance.md](../conformance.md) "Event definitions"
- Structural attributes per event type: [../elements/events.md](../elements/events.md), [../elements/event-definitions.md](../elements/event-definitions.md)
- End-event termination conditions: [end-events.md](end-events.md)
- Event-Based Gateway interaction: [gateways.md](gateways.md)
- Sub-Process and Event Sub-Process structure: [sub-processes.md](sub-processes.md)
