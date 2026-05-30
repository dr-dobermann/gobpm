# Process Lifecycle

_Source: BPMN 2.0 §13.2 (spec p426) + §13.5.6 (spec p443)._

The spec does not define a UML state machine for Process — it defines instantiation triggers and termination conditions. The Process instance is implicitly _running_ between the two.

## Instantiation

A Process is instantiated when one of its **Start Events** occurs. Each occurrence creates a new Process instance — UNLESS the Start Event participates in a `Conversation` that includes other Start Events sharing the same correlation information. In that case:

- If no instance exists yet for that Conversation → create new instance.
- If an instance already exists → route the event to the existing instance.

### Other instantiation triggers

| Trigger | Condition |
|---|---|
| Receive Task with no incoming sequence flows AND `instantiate=true` | Acts like an implicit Message Start Event |
| Event-Based Gateway with no incoming sequence flows AND `instantiate=true` | See below |

### Event-Based Gateway as instantiator

- **Exclusive** Event-Based Gateway (`eventGatewayType=Exclusive`): the first matching Event creates a new Process instance. The Process does NOT wait for other Events originating from that gateway.
- **Parallel** Event-Based Gateway (`eventGatewayType=Parallel`): the first matching Event creates a new instance, BUT the Process then waits for all other Events to arrive (they MUST share correlation info with the first). Process instance completes only if all Events that succeed a Parallel Event-Based Gateway have occurred.
- A `MultipleParallel` Start Event achieves the same multi-trigger waiting at top-level.
- Multiple Parallel Event-Based Gateways may exist — allowing "either all Events after the first OR all Events after the second" instantiation.

### Constraints on instantiation

- A **global Process** MUST NOT have any empty Start Events.
- A **global Process** MUST NOT have any Gateway or Activity without incoming sequence flows. **Exception:** Event-Based Gateway acting as instantiator (`instantiate=true`).
- An ordinary (non-global) Process MAY have activities without incoming sequence flows — they receive a token upon Process instantiation.

## Normal termination

A Process instance _completes_ iff and only if all three conditions hold:

1. If the instance was created through an instantiating **Parallel Event-Based Gateway**, all subsequent Events of that gateway MUST have occurred.
2. No `token` remains within the Process instance.
3. No Activity of the Process is still active.

For (2): all tokens MUST reach an _end node_ (a node with no outgoing sequence flows). When a token reaches an End Event, the behavior associated with the End Event type is performed (Message sent, Signal sent, etc.). See [../semantics/end-events.md](../semantics/end-events.md).

## Abnormal termination

A token reaching a **Terminate End Event** abnormally terminates the entire Process instance. No other End Event behaviors are performed; remaining tokens are discarded; nested Sub-Processes are interrupted.

Sub-Process-level termination is scoped:

- **Terminate End Event** inside a Sub-Process: terminates that Sub-Process only. For an MI Sub-Process, terminates only the affected instance.
- **Cancel End Event** inside a Transaction Sub-Process: abnormally terminates the Sub-Process AND aborts the transaction. Control leaves the Sub-Process through a Cancel intermediate boundary event attached to it.

## Sub-Process completion (recap)

A Sub-Process instance completes iff:

1. All start nodes have been visited (all Start Events triggered, and for every starting Event-Based Gateway one of the associated Events has been triggered).
2. No `token` remains within the Sub-Process instance.

Same shape as the Process completion conditions, scoped one level deeper.

## Cross-references

- Per-activity lifecycle: [activity-lifecycle.md](activity-lifecycle.md)
- Start Event semantics: [../semantics/events.md](../semantics/events.md)
- End Event semantics: [../semantics/end-events.md](../semantics/end-events.md)
- Conversation-based correlation: [../semantics/events.md](../semantics/events.md) (correlation section)
