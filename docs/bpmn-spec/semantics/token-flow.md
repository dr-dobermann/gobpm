# Token Flow

_Source: BPMN 2.0 §13.3.1 (spec p427) + §13.2._

`token` is a theoretical concept used to describe sequence flow behavior. Implementations are NOT required to materialize tokens; they MAY use any internal representation as long as observable behavior matches.

## Sequence flow basics

A `SequenceFlow` connects two `FlowNode`s. A `token` traverses the flow from source to target. Token movement has no timing constraints — it MAY take arbitrary time.

### `isImmediate` attribute

| Value | Effect |
|---|---|
| `true` (or unset, treated as `true` for non-Process Modeling Conformance) | Activities not in the model MAY NOT execute while the token is moving. |
| `false` | Other (out-of-model) activities MAY execute while the token is in transit. |

For Process Execution Conformance, `isImmediate` is a **non-operational** attribute (§13.1) — engines MAY ignore it.

## Multiple incoming sequence flows on an activity

An activity with multiple incoming sequence flows participates in **uncontrolled flow**:

- For each token arriving on _any_ incoming sequence flow, the activity is enabled independently. Multiple tokens on different incoming flows behave as an implicit **Exclusive Gateway**.
- If the modeler needs synchronization, an explicit Gateway (Parallel / Inclusive) MUST precede the activity.

## Multiple outgoing sequence flows on an activity

When an activity transitions to **Completed**, all of its outgoing sequence flows receive a token. Behavior depends on the conditions:

| Configuration | Behavior |
|---|---|
| All outgoing unconditional | Parallel split — all branches activated. |
| All outgoing have `conditionExpression` | Inclusive split — each true condition produces a token. |
| Mix of unconditional + conditional | Combination — unconditional always fire, conditional fire when their condition is true. |

This is illustrated by Figure 13.1: a Task with 3 outgoing flows behaves like a Task followed by an implicit Parallel Gateway (or Inclusive, per above rules).

The number of tokens placed on each outgoing flow is `completionQuantity` (default 1).

## No outgoing sequence flows

An activity with no outgoing sequence flows terminates without producing tokens. Termination semantics of the containing Process / Sub-Process then apply (see [../state-machines/process-lifecycle.md](../state-machines/process-lifecycle.md)).

## No incoming sequence flows

An activity / gateway with no incoming sequence flows is instantiated when the containing `Process` / `SubProcess` is instantiated. It receives a token at that point. Exception: Compensation Activities (which are triggered by compensation events, not by control flow).

## Token semantics at gateways

Gateways consume and produce tokens per their type-specific rules — see [gateways.md](gateways.md).

## Token flow at Process instantiation

- Each `Start Event` that occurs creates a token on its outgoing sequence flow(s).
- Activities / Gateways with no incoming sequence flows receive a token at Process instantiation (with the exceptions above).

## Token flow at termination

- All tokens MUST reach an end node (no outgoing sequence flows) for normal completion.
- A token reaching an `End Event` triggers the behavior of that End Event type — Message sent, Signal sent, etc. — and is consumed.
- A token reaching a **Terminate End Event** abnormally terminates the entire Process instance (or Sub-Process if scoped).

## Cross-references

- Gateway-specific token consumption / production rules: [gateways.md](gateways.md)
- Activity lifecycle (when tokens cause Inactive → Ready): [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)
- Process termination conditions: [../state-machines/process-lifecycle.md](../state-machines/process-lifecycle.md)
- End Event behavior: [end-events.md](end-events.md)
