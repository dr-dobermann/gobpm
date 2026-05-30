# Message Correlation

_Source: BPMN 2.0 **§8.4.2** (Correlation, pp.72–78). Cross-cuts with §13.3.3 (Receive Task), §13.5.1 (Start Event), §13.5.2–§13.5.4 (Intermediate / Event Sub-Process), §10.6.6 (Event-Based Gateway)._

> **Section label note:** earlier KB indexes referred to "§8.3.2 Correlation" — the actual spec section number is **§8.4.2**. Earlier references corrected.

## Purpose

Business Processes run for days or months and many instances of the same Process run in parallel (many order processes, each for a different order). When an asynchronous `Message` arrives, the engine MUST decide:

- Does this Message create a **new** Process instance, or route to an **existing** one?
- If an existing instance — *which* one?

Correlation is the mechanism that answers this. It uses values **extracted from the Message payload itself** (e.g. an `orderID` or `customerID`) — no need to introduce technical correlation tokens like sticky IDs.

**Footnote 1 of §8.4.2:** _"All references to Send or Receive Tasks in this sub-clause also include message catch or throw Events; they behave identically with respect to correlation."_ — i.e., everything below applies to `ReceiveTask` + `SendTask` AND to `IntermediateCatchEvent` / `IntermediateThrowEvent` / `BoundaryEvent` / `StartEvent` / `EndEvent` carrying a `MessageEventDefinition`.

## Structural model

Five elements, all RootElement / BaseElement subclasses:

```
                            Conversation (out of scope; logical grouping only)
                                 |
                                 | correlationKeys  (0..*)
                                 v
                        CorrelationKey
                        - name: string [0..1]
                        - correlationPropertyRef: CorrelationProperty [0..*]
                                 |
                                 | (partial keys)
                                 v
        +----------------> CorrelationProperty
        |                  - name: string [0..1]
        |                  - type: string [0..1]
        |                  - correlationPropertyRetrievalExpression: [1..*]
        |                          |
        |                          v
        |             CorrelationPropertyRetrievalExpression
        |             - messagePath: FormalExpression
        |             - messageRef: Message
        |             (one per Message-type in the Conversation)
        |
        |  CorrelationSubscription (Process-specific; for context-based correlation)
        |  - correlationKeyRef: CorrelationKey
        |  - correlationPropertyBinding: CorrelationPropertyBinding [0..*]
        |             |
        |             v
        |  CorrelationPropertyBinding
        |  - correlationPropertyRef: CorrelationProperty -----+
        +-----------------------------------------------------+
           - dataPath: FormalExpression  (extracts from Process context)
```

### Attribute tables

**CorrelationKey** (Table 8.31, p75):

| Attribute | Type / Card | Purpose |
|---|---|---|
| `name` | `string [0..1]` | Name of the key. |
| `correlationPropertyRef` | `CorrelationProperty [0..*]` | Partial keys composing this composite key. |

**CorrelationProperty** (Table 8.32, p75) — inherits `RootElement`:

| Attribute | Type / Card | Purpose |
|---|---|---|
| `name` | `string [0..1]` | Property name. |
| `type` | `string [0..1]` (QName) | Type identifier. |
| `correlationPropertyRetrievalExpression` | `CorrelationPropertyRetrievalExpression [1..*]` | One extraction Expression **per Message type** in the Conversation. |

**CorrelationPropertyRetrievalExpression** (Table 8.33, p76):

| Attribute | Type / Card | Purpose |
|---|---|---|
| `messagePath` | `FormalExpression [1]` | Extracts the CorrelationProperty value from the referenced Message's payload. |
| `messageRef` | `Message [1]` | The specific Message this extraction applies to. |

**CorrelationSubscription** (Table 8.34, p76) — Process-specific:

| Attribute | Type / Card | Purpose |
|---|---|---|
| `correlationKeyRef` | `CorrelationKey [1]` | The CorrelationKey this subscription supplements. |
| `correlationPropertyBinding` | `CorrelationPropertyBinding [0..*]` | Bindings to specific CorrelationProperties via FormalExpressions against Process context. |

**CorrelationPropertyBinding** (Table 8.35, p77):

| Attribute | Type / Card | Purpose |
|---|---|---|
| `correlationPropertyRef` | `CorrelationProperty [1]` (`required`) | Which CorrelationProperty this binding fills. |
| `dataPath` | `FormalExpression [1]` | Extraction rule atop the Process context (Data Objects / Properties). |

## Two correlation mechanisms (non-exclusive)

Per §8.4.2 they are explicitly described as "non-exclusive" — a Process MAY use both simultaneously.

### Key-based correlation (§8.4.2 p72, p74–75)

Simple and efficient. Identifies a Conversation by one or more `CorrelationKey`s.

**Initialization:**
1. The first `Send Task` or `Receive Task` in a Conversation MUST populate at least one of the CorrelationKey instances.
2. Population works by extracting `CorrelationProperty` values from the initially sent / received Message via the corresponding `CorrelationPropertyRetrievalExpression`s (whose `messageRef` matches the message in flight).
3. A CorrelationKey is **only valid** for use once **all** its CorrelationProperty fields have been populated.

**Matching incoming Messages:**
1. Extract `CorrelationProperty` values from the incoming Message via its matching `CorrelationPropertyRetrievalExpression` (the one whose `messageRef` matches).
2. Form a composite key from those values.
3. Compare to the initialized `CorrelationKey` instance for the candidate Conversation.
4. If they match → Message routed to that Conversation (and the Process instance behind it).

**Subsequent-Message rules:**
- If a follow-up Message derives a CorrelationKey instance that **had already been initialized** → the Message's value MUST equal the Conversation's value. (Mismatch = no match — Message does not route to this Conversation.)
- If a follow-up Message derives a CorrelationKey associated with the Conversation but **not yet initialized** → the derived value becomes associated with the Conversation (lazy initialization of secondary keys).

Conceptual model from the spec: a "joint Conversation token" passed back and forth between participants in every outgoing and incoming Message.

### Context-based correlation (§8.4.2 p76)

Also called **predicate-based correlation** in execution-semantics text (e.g. §13.3.3 Receive Task). A more expressive form built on top of key-based correlation.

A Process MAY provide a `CorrelationSubscription` that acts as the Process-specific counterpart to a specific `CorrelationKey`. The subscription evaluates `FormalExpression`s **against the Process context** (Data Objects / Properties) — rather than against the Message payload.

**Initialization:**
- When a Process instance is created, the `CorrelationKey` instances of all its Conversations are initialized with values that specify "correlate any incoming Message for these Conversations" (effectively a wildcard match initially).

**Dynamic update:**
- A "SubscriptionProperty" is updated **whenever any Data Object or Property referenced by the associated `FormalExpression` changes**.
- Incoming Messages are then matched against the now-populated `CorrelationKey` instance.

**Mid-process change:**
- The SubscriptionProperties can change again during the Process run, which **implicitly changes the correlation criterion**. A Process can effectively re-target what Messages it is willing to accept.

**Coexistence with key-based:**
- The first Send Task / Receive Task can still populate the CorrelationKey via the key-based mechanism, in parallel with context-based dynamic updates.

## Message-to-instance resolution algorithm

End-to-end, when a Message arrives at the engine:

1. Identify candidate Conversations the Message might belong to (typically determined by the `Message` reference / endpoint binding).
2. For each candidate Conversation:
   - For each `CorrelationKey` associated with the Conversation:
     - Find the `CorrelationPropertyRetrievalExpression` whose `messageRef` equals the incoming Message's type.
     - Evaluate each `CorrelationProperty`'s `messagePath` against the Message payload to extract the composite key value.
     - Compare against the Conversation's initialized CorrelationKey instance.
   - If all required keys match → Message routes to this Conversation.
3. Resolve the **Process instance** from the matched Conversation:
   - For a `ReceiveTask` / `IntermediateCatchEvent` / boundary Message event: the unique Process instance owning the waiting receiver.
   - For an instantiating `Message Start Event` or instantiating `ReceiveTask`: create a new Process instance UNLESS an instance already exists for this Conversation (then route to existing).
4. If no candidate Conversation matches:
   - If the Message can instantiate a new Process (`MessageStartEvent` with `instantiate=true`, or Event-Based Gateway at start with `instantiate=true`) → create new instance.
   - Otherwise → no routing target; engine drops or holds the Message per implementation policy (the spec is silent on the unrouteable-message case).

Per §13.3.3 (Receive Task):
- **Key-based:** only a single Receive Task for a given `CorrelationKey` can be active → Message matches **at most one** Process instance.
- **Predicate-based (= context-based):** Message MAY be passed to **multiple** Receive Tasks simultaneously.

## Interaction with executable elements

### Instantiating Message Start Event (§13.2 / §13.5.1)

- A Process is instantiated when a Start Event occurs.
- If the Start Event participates in a **Conversation that includes other Start Events**:
  - A new instance is created **only if** no instance already exists for the specific Conversation (identified through correlation info of the Event occurrence).
  - Subsequent Start Events that share the same correlation info as a Start Event that created an instance → routed to that instance.

### Receive Task (§13.3.3)

- Upon activation, waits for the associated Message.
- When the Message arrives matching correlation: Data Output filled from Message data, Receive Task completes.
- Constraints (see "resolution algorithm" above):
  - Key-based: single active receiver per `CorrelationKey`.
  - Predicate-based: multiple receivers possible.
- `instantiate=true`: an unbounded (no incoming sequence flows) ReceiveTask can start a new Process instance — same instance-routing rules as Message Start Event apply.

### Event-Based Gateway (§13.4.4 / §13.5.1 / §10.6.6)

- A Process can be started by an Event-Based Gateway with `instantiate=true`.
- **Exclusive** Event-Based Gateway: first matching Event creates new instance; other branches stop waiting.
- **Parallel** Event-Based Gateway used at start:
  - Only **message-based triggers** allowed.
  - All Message triggers in the gateway configuration MUST be part of a Conversation **with the same correlation information**.
  - After the first trigger instantiates the Process, remaining Message triggers join the **existing** instance (do not create new ones).
- Multiple groups of Event-Based Gateways at start, all in the same Conversation sharing correlation info:
  - One Event from each group needs to arrive.
  - First arriving creates the instance; subsequent ones (from the other groups) route to the existing instance.

### Intermediate Message Events (§13.5.2) and Boundary Message Events (§13.5.3)

- Same correlation semantics as Receive Tasks.

### Send Task / Intermediate Throw Message Event / Message End Event

- Send-side counterpart. When a `SendTask` (or throw Message event) fires:
  - If the Send is the **first Message in the Conversation**: it populates the relevant `CorrelationKey` instance(s) from the outgoing Message via `CorrelationPropertyRetrievalExpression`.
  - If a follow-up: it MUST carry compatible correlation values (the Conversation token).

## Conversation: how it fits despite being out of scope

The full Conversation / Collaboration metamodel (Pool, Participant, ConversationNode, etc.) is **out of scope** for Process Execution Conformance — see [../conformance.md](../conformance.md). But the **logical concept of a Conversation as a grouping mechanism for correlation** is referenced by §13.2 / §13.5.1 (instance-routing rules above) and cannot be cleanly excised.

**Minimum engine understanding required:**

- A `Conversation` (in this minimal sense) = a logical grouping of related Messages that share a `CorrelationKey`.
- It is the **identity scope** of an in-flight message exchange.
- It does NOT need to be modeled with a Pool / Participant / ConversationNode (those are visual Collaboration constructs); it only needs to be modeled to the degree that the engine can associate Messages with the right Conversation instance.
- For a single-Process executable model that exchanges Messages with the outside world: the Conversation is implicitly the binding between the Process's Message events and the external world's send/receive endpoints. CorrelationKeys are attached to the Process via `CorrelationSubscription`.

Spec quote (§9.5.1, referenced for context only — Conversation metamodel is otherwise out of scope):
> A Conversation is a logical grouping of Message exchanges (Message Flows) that can share a Correlation. … A Conversation is associated with a set of name-value pairs, or a Correlation Key … which is recorded in the Messages that are exchanged. In this way, a Message can be routed to the specific Process instance responsible for receiving and processing the Message.

**Multi-level scoping** (out of scope detail, noted): CorrelationKeys can be attached at Collaboration / Choreography / ConversationNode / ChoreographyActivity level; multiple layers may apply to the same Message Flow. For Process Execution Conformance, only the Process-level binding matters.

## Engine implementation notes

- **CorrelationKey instance identity is per-Conversation, not per-Process instance.** Multiple Process instances can participate in the same Conversation; the Conversation's CorrelationKey instance is the routing target.
- **A CorrelationKey is invalid until all its CorrelationProperty fields are populated.** Engines MUST track per-CorrelationProperty population state to know when a key becomes usable.
- **Initial population is order-sensitive.** The "first Send Task or Receive Task in a Conversation" initializes the CorrelationKey. After that, subsequent participants compare against the initialized value (mismatch = no match).
- **Lazy secondary-key initialization:** if a Conversation has multiple CorrelationKeys (typical for layered routing), follow-up Messages that derive previously-uninitialized keys associate those values with the Conversation. This is lazy — keys initialize as Messages arrive that carry them.
- **Context-based correlation requires reactive evaluation.** When a `Data Object` or `Property` referenced by a `CorrelationPropertyBinding`'s `dataPath` changes, the engine MUST re-evaluate the subscription. Implementing this efficiently typically means dependency-tracking from FormalExpressions to underlying data items.
- **Single-receiver invariant under key-based correlation** (§13.3.3): the engine MUST NOT have two active `ReceiveTask`s (or catch Message events) for the same `CorrelationKey` simultaneously. This is a modeling-time check ideally, but runtime enforcement is also required when MI Activities can spawn receivers dynamically.
- **Predicate-based correlation allows fan-out:** a single Message MAY be delivered to multiple `ReceiveTask`s if all of their subscriptions match. Engine MUST not "consume" the Message after first delivery.
- **Conversation modeling minimum:** for a single-process executable model, the engine needs (a) the set of `Conversation`s the Process participates in, (b) for each, the `CorrelationKey`s, (c) per Message type, the `CorrelationPropertyRetrievalExpression`s for extracting correlation values from payloads. Visual Conversation/Collaboration metamodel elements (`Pool`, `Participant`, etc.) are NOT required.

## Cross-references

- Activity lifecycle (Receive Task in Active state waiting for Message): [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)
- Receive Task / Send Task semantics: [tasks.md](tasks.md)
- Start Event Conversation-bound instantiation: [events.md](events.md), [../state-machines/process-lifecycle.md](../state-machines/process-lifecycle.md)
- Event-Based Gateway as instantiator (Parallel mode correlation constraint): [gateways.md](gateways.md)
- Structural attribute catalogue: [../elements/correlation.md](../elements/correlation.md)
- Data semantics (CorrelationPropertyBinding evaluates FormalExpressions against Process context — same data plane as DataAssociation): [data.md](data.md)
