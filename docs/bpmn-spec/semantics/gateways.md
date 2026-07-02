# Gateway Execution Semantics

_Source: BPMN 2.0 §13.4 (spec p434–438)._

All in-scope gateways consume tokens on incoming sequence flows and produce tokens on outgoing flows according to type-specific rules below.

**Scope note:** Common Executable Subclass (§2.1.3) includes Exclusive, Parallel, Inclusive, and Event-Based gateways. **ComplexGateway is an explicit extension** above Common Executable, added because it enables workflow patterns (Structured Discriminator, Partial Joins) not otherwise expressible. See [../conformance.md](../conformance.md).

## Parallel Gateway (§13.4.1)

**Symbol:** `+`

**Operational semantics (Table 13.1):**

- **Activation:** at least one token on _each_ incoming sequence flow.
- **Consumption:** exactly one token from each incoming sequence flow.
- **Production:** exactly one token on each outgoing sequence flow.
- **Excess tokens:** if multiple tokens are present on a single incoming flow, only one is consumed; the rest remain on that flow after gateway execution.
- **Exceptions:** Parallel Gateway cannot throw an exception.

**Workflow patterns:** WCP-2 Parallel Split, WCP-3 Synchronization.

**Engine notes:**

- Acts as both fork (when multiple outgoing) and join (when multiple incoming) — same element, configuration determined by direction.
- Synchronization is _exact_: needs one token per incoming flow before it fires. Does not consume extras.

## Exclusive Gateway (§13.4.2)

**Symbol:** `×` (with optional X marker) or no marker

**Pass-through semantics for incoming (merging behavior). Each activation routes to exactly one outgoing branch (branching behavior).**

**Operational semantics (Table 13.2):**

- Each `token` arriving at _any_ incoming flow activates the gateway and is routed to exactly one outgoing flow.
- Conditions on outgoing flows are evaluated **in order**.
- The **first** condition that evaluates to `true` determines the chosen outgoing flow. No more conditions are evaluated.
- If and only if no condition evaluates to `true`, the token is passed on the **default** sequence flow (referenced by the `default` attribute on the Gateway).
- If all conditions evaluate to `false` AND no default flow is specified → engine throws an exception.

**Workflow patterns:** WCP-4 Exclusive Choice, WCP-5 Simple Merge, WCP-8 Multi-Merge.

**Engine notes:**

- Evaluation **order** is significant — model authors MUST express priority via flow ordering.
- A token on any incoming flow fires the gateway independently (uncontrolled merge — different from Inclusive merge which synchronizes).
- The `default` attribute on `ExclusiveGateway` references the `SequenceFlow` taken when no condition matches.

## Inclusive Gateway (§13.4.3)

**Symbol:** `O`

**Operational semantics (Table 13.3) — most complex of the four:**

### Activation condition

The Inclusive Gateway is activated iff:

- **(A)** at least one incoming sequence flow has at least one token, AND
- **(B)** For every directed path formed by sequence flow that:
  - starts with a sequence flow `f` of the diagram that has a token,
  - ends with an incoming sequence flow of the inclusive gateway that has _no_ token, and
  - does not visit the inclusive gateway
  
  there is _also_ a directed path formed by sequence flow that:
  - starts with `f`,
  - ends with an incoming sequence flow of the inclusive gateway that _has_ a token, and
  - does not visit the inclusive gateway.

Informally: the gateway waits until all expected tokens have arrived; expectation is computed by tracing upstream tokens.

### Execution

- Upon execution, **one token is consumed from each incoming flow that has one** (tokens on non-token incoming flows obviously stay zero).
- **Production:** evaluate all conditions on outgoing flows (in any order). Each condition that evaluates to `true` produces a token on its respective outgoing flow.
- If and only if no condition evaluates to `true`, the token is passed on the default flow.
- If all conditions evaluate to `false` AND no default → exception thrown.

**Exception:** thrown when all conditions are false and no default flow specified.

**Workflow patterns:** WCP-6 Multi-Choice, WCP-7 Structured Synchronizing Merge, WCP-37 Acyclic Synchronizing Merge, WCP-38 General Synchronizing Merge.

**Engine notes:**

- The activation condition requires reasoning about the **graph reachability** of upstream tokens — non-trivial. Engines typically maintain expected-token counts per inclusive gateway.
- Used both as inclusive split (multi-choice fork) and inclusive merge (synchronizing merge).

## Event-Based Gateway (§13.4.4)

**Symbol:** pentagon inside double-circle

**Pass-through for incoming. Exactly one outgoing branch is activated based on which subsequent Event/Task fires first.**

**Operational semantics (Table 13.4):**

- Choice of outgoing branch is **deferred** until one of the subsequent Tasks or Events completes.
- The first subsequent element to complete causes all other branches to be **withdrawn** (sibling activities transition to `Withdrawn` state — see [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)).
- **Exception:** cannot throw an exception.

**Workflow patterns:** WCP-16 Deferred Choice.

**Engine notes:**

- The "Tasks following an Event Gateway" are limited to **Receive Tasks** — see §10.6.6.
- The "Events following" are intermediate catching events: Message, Timer, Signal, Conditional.
- When used at Process start (`instantiate=true`), only **message-based** triggers are allowed.
- If used at Process start with `eventGatewayType=Parallel`: first trigger instantiates the Process; remaining triggers join the existing instance (do not create new instances). The messages that trigger the arms **MUST share the same correlation information** (§10.6.6, p. 298) — that shared key is how a later arm's message routes to the created instance rather than spawning its own.
- Distinct configurations via `eventGatewayType`: `Exclusive` (default) vs `Parallel`.

### Race-withdrawal interaction with Activity Lifecycle

When an Event-Based Gateway has multiple subsequent receive activities, all of them simultaneously transition `Inactive → Ready` when the gateway fires. The first one whose triggering Event arrives transitions `Ready → Active → Completing → Completed`; all siblings transition `Ready → Withdrawn` via the "Activity Interrupted + Alternative Path Selected" transition.

## Complex Gateway (§13.4.5)

**Symbol:** `*` (asterisk inside diamond)

Facilitates complex synchronization, particularly **race situations** where the diverging behavior is similar to Inclusive Gateway but the activation rule is an explicit Boolean expression over per-incoming-flow token counts.

### Runtime attributes

Each **incoming** gate has an `activationCount` runtime attribute — an integer representing the number of tokens currently on that incoming sequence flow.

The gateway has an `activationExpression` — a Boolean `Expression` that:
- References the `activationCount` of incoming gates (e.g., `x1 + x2 + ... + xm >= 3` — "at least 3 of m incoming gates have a token").
- MAY also reference Process data.
- **Should use only addition and constants** in subexpressions over `activationCount` — i.e., `expr >= const` form — to prevent **undesirable oscillation** of gateway activation.

Each **outgoing** sequence flow has a Boolean condition evaluated during gateway execution to determine if it receives a token. These conditions MAY refer to the gateway's internal state via the runtime attribute `waitingForStart` (Boolean).

### Two-phase state machine

The gateway is in one of two runtime states:
- `waitingForStart = true` — initial state, waiting for `activationExpression` to fire
- `waitingForStart = false` — `waitingForReset`, waiting for trailing tokens

**Phase 1 — Waiting for start (Table 13.5):**

- Wait for `activationExpression` to become `true`. The expression is **not evaluated** until at least one token exists on some incoming sequence flow.
- When `activationExpression` becomes `true`:
  - Consume one token from each incoming sequence flow that has a token.
  - Determine outgoing flows: evaluate all outgoing conditions (in any order). Those evaluating `true` receive a token.
  - If no outgoing condition evaluates `true`: token passed on the **default** sequence flow.
  - If all conditions `false` AND no default → **runtime exception**.
- Gateway changes state to `waitingForReset`.
- Gateway **remembers** which incoming sequence flows it consumed tokens from in phase 1.

**Phase 2 — Waiting for reset:**

- Gateway waits for a token on each of the incoming flows from which it has **not yet** received a token in phase 1, UNLESS such a token is not expected per **Inclusive Gateway join semantics** (graph-based reachability — see Inclusive Gateway above).
- Formally: resets when, for every directed path formed by sequence flow that
  - starts with a sequence flow `f` having a token,
  - ends with an incoming flow of the ComplexGateway that has no token AND has not consumed a token in phase 1,
  - does not visit the ComplexGateway,
  
  there is also a path that
  - starts with `f`,
  - ends with an incoming flow of the ComplexGateway that has a token OR consumed a token in phase 1,
  - does not visit the ComplexGateway.
- If contained in a Sub-Process: paths crossing the Sub-Process boundary are NOT considered.
- When the gateway resets:
  - Consume one token from each incoming flow that has a token AND did NOT consume a token in phase 1.
  - Evaluate outgoing conditions. Those true receive a token. If none → default. (Note: outgoing conditions MAY evaluate differently in the two phases — e.g., by referring to `waitingForStart`.)
- Gateway state returns to `waitingForStart`.

**Notes:**
- The gateway MAY produce no tokens at all in phase 2 — no exception is thrown in that case (the phase-1 outputs already covered the activation).
- If `activationExpression` never becomes `true`, tokens are blocked indefinitely → MAY cause **deadlock** of the entire Process. Engine SHOULD provide deadlock detection or timeouts.

**Exceptions:** the ComplexGateway throws an exception when it is activated in state `waitingForStart`, no condition on any outgoing sequence flow evaluates `true`, AND no default sequence flow is specified.

**Workflow patterns:** WCP-9 Structured Discriminator, WCP-28 Blocking Discriminator, WCP-30 Structured Partial Join, WCP-31 Blocking Partial Join.

**Engine notes:**
- The activation Expression typically references `activationCount` variables that are NOT process data — they are runtime counters maintained by the engine per incoming gate. Engines need a per-instance map `{incomingFlowId → activationCount}` and re-evaluate the activation Expression whenever the count changes.
- The `waitingForStart` runtime attribute is a Boolean exposed to the engine's Expression evaluator so outgoing condition Expressions can branch on the phase.
- Reset-phase reachability test is the same graph-based join logic used by Inclusive Gateway — share the implementation if possible.

## Decision matrix

| Gateway | Merge behavior | Split behavior | Synchronizes? | Can throw exception? |
|---|---|---|---|---|
| Parallel | Wait for all incoming | Fork all outgoing | yes (exact) | no |
| Exclusive | Pass-through any | Pick first true | no | yes (no default + all false) |
| Inclusive | Wait for expected subset | Fork all true | yes (graph-based) | yes (no default + all false) |
| Event-Based | Pass-through any | First event/task wins | no | no |
| Complex | Activation expression over per-gate counts | Evaluate all outgoing conditions | yes (custom, 2-phase) | yes (no default + all false in phase 1) |

## Cross-references

- Token-flow basics: [token-flow.md](token-flow.md)
- Withdraw transition on race-loss: [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)
- Structural attributes (`default`, `gatewayDirection`, etc.): [../elements/gateways.md](../elements/gateways.md)
