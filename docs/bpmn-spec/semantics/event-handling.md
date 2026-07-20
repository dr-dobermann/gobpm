# Event Handling — Resolution, Propagation, Scopes

_Source: BPMN 2.0 §10.5.1 (Concepts, p233) + §10.5.6 (Handling Events, p274–279) + §10.5.7 (Scopes, p280)._

This file covers the **modeling-level architecture** of events: how triggers are forwarded to catching handlers, multiplicity constraints on handlers, precedence between inline (Event Sub-Process) and boundary handlers, and the scope hierarchy that defines event resolution boundaries.

Companion to [events.md](events.md), which covers per-position handling (Start / Intermediate / Boundary / Event Sub-Process), and [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md), which covers state transitions triggered by events.

## 1. Event resolution strategies (§10.5.1)

The mechanism by which a thrown trigger reaches its catching handler depends on the trigger type:

| Strategy | Triggers | Forwarding rule |
|---|---|---|
| **Publication** | Message, Signal | Trigger MAY be received by any catching Event in any scope where the trigger is published. Messages = typically B2B / cross-Pool. Signals = broadcast within and across Pools, Processes, and diagrams. |
| **Direct resolution** | Cancellation, Compensation, Termination | Trigger directed at a **specific Process or Activity instance**. Cancellation/Compensation reference an activityRef; Termination targets the enclosing scope. |
| **Propagation** | Error, Escalation | Trigger forwarded from the throw location **upward to the innermost enclosing scope instance** that has an attached catching Event for it. Errors are **critical** — suspend execution at throw location. Escalations are **non-critical** — execution continues at throw location. |
| **Implicit throw** | Timer, Conditional | Triggers thrown automatically when their condition (time / Boolean state) is satisfied. |

**Unresolved trigger:** if no catching Event is found anywhere in the scope chain for an Error or Escalation, the trigger is **unresolved**. The spec does not mandate a specific engine reaction — implementations typically log + abort the Process instance for an unresolved Error, and ignore an unresolved Escalation.

**Conversation grouping for Publication:** Events whose strategy is publication (Messages, Signals) are grouped into **Conversations**. A published Event MAY participate in several Conversations. For Messages, **correlation** (see [correlation.md](correlation.md)) identifies which Process instance the Message reaches.

## 2. Three categories of Event handlers (§10.5.6)

Per §10.5.6 intro, BPMN has three types of Event handlers:

| Category | Where attached | Effect of trigger |
|---|---|---|
| **Process-starting** | `StartEvent` (top-level or in an event sub-process) | Instantiate a new Process (or new Event Sub-Process) |
| **Inline in Sequence Flow** | `IntermediateCatchEvent` in normal flow | Wait → consume → follow outgoing flows |
| **Attached to Activity** | `BoundaryEvent` OR `Event Sub-Process` inside the Activity | Interrupt or augment the Activity's execution |

## 3. Start Event handling patterns

**Single Start Event:** each occurrence creates a new Process instance, sequence flows are followed as usual.

**Exclusive start** (multiple alternative Start Events): the most common multi-trigger pattern. Each Start Event is an alternative entry; each occurrence creates an independent new Process instance. A single `MultipleStartEvent` with multiple Message Event Definitions behaves the same way.

**Event-Based Gateway start** (`instantiate=true`): first matching Event creates the instance; siblings stop waiting (Event-Based Exclusive Gateway semantics). This is the **only scenario** where a Gateway can exist without incoming Sequence Flows.

**Multiple Event-Based Gateway groups at start:** several groups, all in the same Conversation sharing correlation info. One Event per group needs to arrive. First one creates the Process instance; subsequent (from other groups) route to the existing instance.

**Event synchronization (Parallel Start Event):** when a modeler requires several disjoint Start Events to be **merged into a single Process instance**, the Parallel Start Event MAY group them. Each grouped Start Event MUST occur exactly once for the Process instance to be created.

For lifecycle / token semantics see [../state-machines/process-lifecycle.md](../state-machines/process-lifecycle.md).

## 4. Boundary Event attachment rules

A `BoundaryEvent` attaches to an Activity's boundary to change normal flow into exception or augmenting flow.

### Allowed triggers on boundary (§10.5.4 + §10.5.6, Table 10.90 / 10.92)

| Trigger | Allowed on boundary | `cancelActivity` allowed values |
|---|---|---|
| Message | yes | `true` (interrupting) or `false` (non-interrupting) |
| Timer | yes | `true` or `false` |
| Signal | yes | `true` or `false` |
| Conditional | yes | `true` or `false` |
| Escalation | yes | `true` or `false` |
| Multiple | yes | `true` or `false` |
| Parallel Multiple | yes | `true` or `false` |
| **Error** | yes | **always `true`** (no non-interrupting Error event exists) |
| **Cancel** | yes — **only on Transaction Sub-Process** | **always `true`** |
| **Compensation** | yes | `cancelActivity` does not apply (compensation is special — triggered by throw Compensation Event, not by activity execution) |
| None | NO (only in normal flow) | — |
| Link | NO (only in normal flow) | — |

### Visual notation rule

- Interrupting (`cancelActivity=true`): **solid** boundary marker.
- Non-interrupting (`cancelActivity=false`): **dashed** boundary marker.

The visual notation **implicitly sets** `cancelActivity` — a modeling tool MUST infer the value from notation.

### Effect rules

**Interrupting** (`cancelActivity=true`):
- Event occurrence consumed.
- Associated Activity is **terminated**.
- A downstream token is generated which activates the next element via the boundary's outgoing **exception flow**.

**Non-interrupting** (`cancelActivity=false`):
- Event occurrence consumed.
- Activity **continues** to execute.
- A token is generated for the boundary's outgoing Sequence Flow **in parallel** with the continuing Activity.
- "Care MUST be taken" when this parallel flow merges back into the main flow — typically it should be ended with its own End Event to avoid merge-time ambiguities.

## 5. Handler multiplicity rules (§10.5.6, p278)

For a given parent Activity (Sub-Process), the spec constrains how many handlers (Event Sub-Processes + boundary Events combined) may target the same Event Declaration:

### Interrupting handlers

- For each Event Declaration: **only ONE interrupting handler** (Event Sub-Process OR boundary Event) MAY be modeled.
- Reason: interrupting handlers terminate the parent immediately after handler completion → only one can execute at a time per Event Declaration.
- This restriction is **per Event Declaration**, not per Event type. A modeler MAY specify multiple interrupting handlers if each refers to a **different Event Declaration** (e.g., different `errorRef`, different `messageRef`).
- Once an interrupting handler is started, no further handlers (interrupting OR non-interrupting) may be initiated for the parent.

### Non-interrupting handlers

- For each Event Declaration: an **unlimited** number of non-interrupting Event Sub-Processes can be modeled and execute in parallel.
- At runtime, they are invoked in **non-deterministic order**.
- During execution of a non-interrupting Event Sub-Process, the parent Activity **continues executing** normally.
- Same restrictions apply to non-interrupting boundary Events.

### Interrupting Event Declarations

The following triggers can be interrupting: Error, Escalation, Message, Signal, Timer, Conditional, Multiple, Parallel Multiple.

### Non-interrupting Event Declarations

The following triggers can be non-interrupting: Escalation, Message, Signal, Timer, Conditional, Multiple, Parallel Multiple. (**Error is excluded** — errors always interrupt.)

## 6. Inline (Event Sub-Process) vs boundary handler precedence

When a Sub-Process has **both** an inline Event Sub-Process AND a boundary Event handler for the **same `EventDefinition`** (§10.5.6, p278):

| Case | Outcome |
|---|---|
| Inline Event Sub-Process **"re-throws"** the Event after completion | The boundary Event is triggered. Inline handler runs first, then re-emits the Event, then boundary handler runs. |
| Inline Event Sub-Process completes **without re-throwing** | The parent Activity is considered to have completed. Normal Sequence Flow resumes. The Event Sub-Process **"absorbs"** the Event — the boundary handler is NOT triggered. |

This gives the modeler explicit control over whether an inline handler is a **terminal** handler ("absorbs") or a **decorator** ("re-throws").

## 7. Interrupting Event Handler runtime flow

When an interrupting Event (Error / Escalation / Message / Signal / Timer / Conditional / Multiple / Parallel Multiple) occurs on a parent Activity:

1. Whenever the Event occurs — regardless of inline-handler vs boundary-handler delivery — the associated parent Activity is **interrupted**.
2. If an **inline error handler** is specified (Sub-Process case): the handler runs **within the context** of that Sub-Process — has access to Sub-Process's data context.
3. If a **boundary Error Event** is present: Sequence Flows from that boundary Event are then followed.
4. The parent Activity is **cancelled** AFTER either:
   - The error handler completes, OR
   - The Sequence Flow from the boundary Event is followed.

## 8. Non-interrupting Event Handler runtime flow

For Event Sub-Processes:
- Whenever the Event occurs, it is consumed and the associated Event Sub-Process is performed.
- If several Events happen in parallel: handled concurrently — several Event Sub-Process **instances** are created.
- A non-interrupting Start Event indicates the Event Sub-Process instance runs **concurrently** to the parent Sub-Process proper.

For boundary Events:
- Whenever the Event occurs, the handler runs **concurrently** to the Activity.
- If an Event Sub-Process is also specified for that Event (Sub-Process case): it runs within the Sub-Process's context.
- Sequence Flows from the boundary Event are followed.
- A token is generated for the boundary's outgoing Sequence Flow in parallel with the continuing Activity.
- Merge-back caution as noted in §4 above.

## 9. End Event handling recap (§10.5.6, p279)

| End Event type | Effect |
|---|---|
| **Terminate** | All remaining active Activities within the Process (or Sub-Process) are terminated. |
| **Cancel** | Only allowed in a **Transaction Sub-Process**. Cancels the Sub-Process and aborts the associated Transaction. |
| All others (None / Message / Signal / Error / Escalation / Compensation) | EventDefinition behavior performed. When no further active Activities remain → Sub-Process / Process instance is completed. |

Full conditions in [end-events.md](end-events.md).

## 10. Scopes (§10.5.7)

A **scope** describes the context in which an Activity executes. Conceptually:

> _"A scope describes the context in which execution of an Activity happens. This consists of the set of: Data Objects available (including DataInput and DataOutput); Events available for catching or throwing triggers; Conversations going on in that scope."_ — §10.5.7

### Scope structure

- A scope contains **exactly one main flow** of Activities. The flow is started when the scope gets activated.
- All Activities are enclosed by a scope.
- Scopes are **hierarchically nested**.

### Scope instances

- A scope can have **several scope instances** at runtime (e.g., for multi-instance Sub-Processes).
- Scope instances are hierarchically nested according to their generation.
- A single scope instance can have **multiple tokens** active in it concurrently.

### Scope instance lifecycle

A scope instance has its own lifecycle states (separate from the Activity lifecycle):

- `Activated`
- `In execution`
- `Completed`
- `In Compensation`
- `Compensation`
- `In Error`
- `In Cancellation`
- `Cancelled`

### Elements with scope characteristics

| Element | Scope role |
|---|---|
| `Choreography` | Out of scope for execution conformance (see [../conformance.md](../conformance.md)) |
| `Pool` | Out of scope for execution conformance |
| `SubProcess` | Primary in-scope scope container |
| `Task` | Implicit micro-scope (data inputs / outputs + ability to attach boundary Events) |
| `Activity` | Generic |
| `Multi-instances body` | The per-iteration body of an MI Activity is a scope |

### What scopes define the semantics of

Per §10.5.7, scopes are used to define the semantics of three things:

1. **Visibility of Data Objects** (including DataInput and DataOutput) — see [data.md](data.md).
2. **Event resolution** — propagated triggers (Error / Escalation) climb the scope chain looking for a catching handler; published triggers (Message / Signal) are matched against scope-attached Conversations.
3. **Starting / stopping of token execution** — token-flow gates at scope boundaries.

### Implicit vs explicit modeling

The Data Objects, Events, and `correlationKeys` described by a scope can be:
- **Explicitly modeled** (e.g., a Sub-Process with declared DataObjects and attached boundary Events), OR
- **Implicitly defined** (e.g., the implicit scope of a Task with no declared data — it still has a scope, just empty).

## 11. Engine implementation notes

- **Scope chain walk for Error / Escalation propagation:** when a throw Error / Escalation occurs, the engine MUST walk from the throwing scope outward to enclosing scopes, checking each for a catching Event with matching `errorRef` / `escalationRef`. First match consumes the trigger. No match → unresolved (Error → critical, typically aborts; Escalation → silent).
- **Conversation-bound Message publication:** Messages MUST be matched not only by Message ref but also by correlation (see [correlation.md](correlation.md)). A subscriber in a scope doesn't see a published Message unless the Message's correlation matches the subscriber's Conversation correlation.
- **Signal publication is unscoped within reach:** Signals do NOT use correlation. Every catching Signal handler in reach (within the Process, within the same Pool depending on engine architecture) receives the Signal. Engines need a Signal-name → set-of-subscribers index.
- **Interrupting handler exclusion is per Event Declaration:** an interrupting handler for ErrorEventDefinition with `errorRef=X` does NOT exclude an interrupting handler for `errorRef=Y` on the same parent. Engines need to track multiplicity per (parent Activity, EventDefinition reference) pair.
- **Inline vs boundary handler delivery order:** when a Sub-Process has both inline and boundary handler for the same EventDefinition, the engine MUST deliver to the inline first. After inline completes, check whether it "re-threw" (modeled via an Intermediate or End Throw Event with the same EventDefinition in the Event Sub-Process) — if so, deliver to boundary. Otherwise absorb.
- **Multi-instance Activity termination:** a Terminate End Event in an MI Sub-Process terminates **only the affected instance** (§13.5.6), not the whole MI Activity. Cross-checked with scope: each MI iteration body is a separate scope instance.
- **Scope instance state machine** ≠ Activity state machine. Scope instances have their own lifecycle (Activated → In execution → Completed → optionally In Compensation / In Error / Cancelled). Engines that model scope instances explicitly (Camunda-style "concurrent execution tree") gain cleaner semantics for compensation and error handling.

## Cross-references

- Per-position event handling (Start / Intermediate / Boundary / Event Sub-Process): [events.md](events.md)
- Lifecycle transitions triggered by events: [../state-machines/activity-lifecycle.md](../state-machines/activity-lifecycle.md)
- Process-instance instantiation: [../state-machines/process-lifecycle.md](../state-machines/process-lifecycle.md)
- End Event termination conditions: [end-events.md](end-events.md)
- Compensation triggering and handler invocation: [compensation.md](compensation.md)
- Message correlation: [correlation.md](correlation.md)
- Data visibility within scopes: [data.md](data.md)
- Event Sub-Process structural rules: [sub-processes.md](sub-processes.md)
- Structural attributes per event type: [../elements/events.md](../elements/events.md), [../elements/event-definitions.md](../elements/event-definitions.md)

## Appendix: Conditional-event clauses (verbatim, BPMN 2.0 v2.0.2 PDF)

Added 2026-07-15 (verified verbatim against the spec PDF via the BPMN
notebook) — the normative Conditional wording the tables above summarize:

- **Table 10.84 (§10.5.2, Start Event types), the Conditional row:**
  > "This type of event is triggered when a condition such as 'S&P 500
  > changes by more than 10% since opening,' or 'Temperature above 300C'
  > become true. **The condition Expression for the Event MUST become false
  > and then true before the Event can be triggered again.** **The Condition
  > Expression of a Conditional Start Event MUST NOT refer to the data
  > context or instance attribute of the Process** (as the Process instance
  > has not yet been created). Instead, it MAY refer to static Process
  > attributes and states of entities in the environment. The specification
  > of mechanisms to access such states is out of scope of the standard."
- **Tables 10.89 (Intermediate, normal flow, p.251) and 10.90 (Intermediate,
  boundary, p.254), the Conditional rows:**
  > "This type of Event is triggered when a condition becomes true. A
  > condition is a type of Expression."
- **§10.5.1 (implicit throw):**
  > "Timer and Conditional triggers are implicitly thrown. When they are
  > activated they wait for a time based or status based condition
  > respectively to trigger the catch Event."

## Appendix: Link-event clauses (verbatim, BPMN 2.0 §10.5.1)

Added 2026-07-20 — the normative Link-event wording behind ADR-006 §2.8. This
text is carried verbatim in the vendored model's `pkg/model/events/link.go`
doc comment (lifted from the BPMN 2.0 spec, §10.5.1 Link Events concept):

> "A Link Event is a mechanism for connecting two sections of a Process. Link
> Events can be used to create looping situations or to avoid long Sequence
> Flow lines. **The use of Link Events is limited to a single Process level**
> (i.e., they cannot link a parent Process with a Sub-Process). Paired Link
> Events can also be used as 'Off-Page Connectors' for printing a Process
> across multiple pages. They can also be used as generic 'Go To' objects
> within the Process level. **There can be multiple source Link Events, but
> there can only be one target Link Event.** When used to 'catch' from the
> source Link, the Event marker will be unfilled. When used to 'throw' to the
> target Link, the Event marker will be filled."

Metamodel (`elements/event-definitions.md`, LinkEventDefinition):
`name: String [0..1]`, `target: LinkEventDefinition [0..1]`,
`source: LinkEventDefinition [0..*]` — the many-sources / one-target cardinality
is normative. Positions (`conformance.md`): IntermediateCatch (target) +
IntermediateThrow (source) only; boundary is invalid (the "Link | NO (only in
normal flow)" row above).
