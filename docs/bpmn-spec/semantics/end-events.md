# End Event Termination Conditions

_Source: BPMN 2.0 §13.5.6 (spec p443)._

End Events terminate token-flow on a branch. Their behavior depends on (a) where they occur — Process-level vs Sub-Process-level — and (b) their `EventDefinition` type.

## Process-level End Events

### Terminate End Event

- The **Process is abnormally terminated**.
- No other ongoing **Process instances** are affected (terminates only this one instance).
- Remaining tokens are discarded.
- Other End Events behavior is NOT performed.

### Other End Event types (Message / Signal / Error / Escalation / Compensation / None)

- The behavior associated with the Event type is performed:
  - Message End Event → the associated `Message` is sent.
  - Signal End Event → the associated `Signal` is sent.
  - Error End Event → the associated `Error` is thrown.
  - Escalation End Event → the associated `Escalation` is thrown.
  - Compensation End Event → a throw Compensation Event is triggered (see [compensation.md](compensation.md)).
  - None End Event → no specific behavior, just consumes the token.
- The Process instance is then **completed** if and only if both:
  1. All start nodes of the Process have been visited (i.e., all Start Events have been triggered, and for every starting Event-Based Gateway, one of the associated Events has been triggered).
  2. No `token` remains within the Process instance.

## Sub-Process-level End Events

### Terminate End Event

- The **Sub-Process is abnormally terminated**.
- For a multi-instance Sub-Process: ONLY the affected instance is terminated. Other Sub-Process instances and higher-level Sub-Processes / Process instances are NOT affected.
- Remaining tokens within the affected Sub-Process instance are discarded.

### Cancel End Event

- The **Sub-Process is abnormally terminated** AND the associated **Transaction is aborted**.
- Control leaves the Sub-Process through a **Cancel intermediate boundary event** attached to the Sub-Process.
- Use case: only valid within a `Transaction` Sub-Process.

### Other End Event types

- Same as Process-level: the type-specific behavior is performed.
- The Sub-Process instance is then **completed** iff:
  1. All start nodes of the Sub-Process have been visited.
  2. No `token` remains within the Sub-Process instance.

## Comparison table

| End Event Type | Process-level effect | Sub-Process-level effect |
|---|---|---|
| `None` | Token consumed; instance completes if conditions met | Token consumed; instance completes if conditions met |
| `Terminate` | Abnormal Process termination | Abnormal Sub-Process termination (only affected MI instance for MI) |
| `Cancel` | (Not valid at Process level) | Abnormal Sub-Process termination + Transaction abort; control leaves via Cancel boundary event |
| `Message` | Send Message; instance completes if conditions met | Send Message; instance completes if conditions met |
| `Signal` | Throw Signal; instance completes if conditions met | Throw Signal; instance completes if conditions met |
| `Error` | Throw Error; instance fails (propagation per error-handling) | Throw Error; propagates up to enclosing scope |
| `Escalation` | Throw Escalation; instance completes if conditions met | Throw Escalation; propagates up to enclosing scope |
| `Compensation` | Trigger compensation per `CompensateEventDefinition` | Trigger compensation per `CompensateEventDefinition` |

## Completion conditions recap

Normal Process / Sub-Process completion requires:

1. **All start nodes visited** — every Start Event triggered, and for every starting Event-Based Gateway one of its associated Events triggered.
2. **No tokens remaining** — every token has reached an End Event.

When a Process is created via an instantiating **Parallel Event-Based Gateway**, condition (1) extends: all subsequent Events of that gateway MUST have occurred (§13.2). See [../state-machines/process-lifecycle.md](../state-machines/process-lifecycle.md).

## Cross-references

- Process lifecycle: [../state-machines/process-lifecycle.md](../state-machines/process-lifecycle.md)
- Compensation triggering by Compensation End Event: [compensation.md](compensation.md)
- Cancel boundary event on Transaction Sub-Process: [sub-processes.md](sub-processes.md)
- Error propagation (out of scope of this file): handled by error event handlers / boundary error events / event sub-processes — see [events.md](events.md)
- Event definition structural attributes: [../elements/event-definitions.md](../elements/event-definitions.md)
