# ADR-014 — Message handling (SendTask / ReceiveTask, message events, the broker & the producer/consumer seam)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-14 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) |

> **Draft — not yet implemented.** Decides how gobpm sends and receives BPMN
> messages: the `SendTask` and `ReceiveTask` executors, the message
> throw/catch events, their shared **producer/consumer** seam (deferred here
> from [ADR-011 v.5 §2.6](ADR-011-process-data-flow.md)), and how both ride the
> message broker and the event-wait machinery. Scope is **phased**: this ADR
> decides the core send/receive model; correlation-key derivation and
> message-triggered instantiation are deferred to named follow-ups (§2.8). The
> implementing SRD does the file-level work and is code-grounded.

## 1. Context

### 1.1 What the standard requires

BPMN 2.0 (§8.3.2 Correlation, §8.4 Messages & Operations, §10.3.x Tasks,
§10.4.2 Data, §11 Collaboration) defines messaging between a process and an
external participant:

- **A Message is content exchanged between two participants.** A `Message`
  references an `ItemDefinition` (its payload structure). A `MessageFlow`
  connects a sender to a receiver across pool boundaries; inside a process the
  send/receive is modelled by tasks or message events.
- **A `SendTask` sends a message and completes once it is sent.** It references
  a `Message` (the content) and, optionally, an `Operation` (when the send is a
  service call). It does not wait for a reply.
- **A `ReceiveTask` waits for a message and completes when it arrives.** It
  references a `Message`; its `instantiate` flag, when set on a process's first
  activity, lets an arriving message **start a new process instance**.
- **Throw/catch message events mirror the tasks.** A throw message event emits
  a message (like a `SendTask`); a catch message event waits for one (like a
  `ReceiveTask`). The message semantics are identical — only the modelling
  element (activity vs event) differs.
- **Messages are correlated to instances** (§8.3.2). A `CorrelationKey` is
  computed from message properties via retrieval expressions; an arriving
  message is routed to the instance whose key matches. Correlation is what
  makes "the right message reach the right running process".
- **Message flow is asynchronous to the sender but synchronous to the
  receiver's lifecycle.** The sender emits and proceeds; the receiver's
  activity does not complete (and emits no tokens) until a matching message
  arrives and its data is bound.

### 1.2 What the engine has today

The pieces exist but the message path is not connected:

- **A message broker exists.** `pkg/messaging` defines a `MessageBroker`
  (`Publish(Envelope)` / `Subscribe(name, key) → <-chan Envelope`) with an
  in-memory default; the `Envelope` carries a payload, a message name, and a
  flat correlation key. The engine runtime already exposes it to executions.
- **The event-wait machinery exists.** The EventHub registers a **waiter** per
  event definition and resumes the waiting track when the event fires; a track
  moves to a "wait for event" state and back. Only a **timer** waiter is
  implemented — there is **no message waiter**, so a catch message event or a
  `ReceiveTask` has nothing to wait on.
- **Throw works only from an end event, internally.** A throwing event emits
  through the EventHub's propagate path, not through the broker — so today a
  thrown message never leaves the engine onto the external channel.
- **`SendTask` and `ReceiveTask` are stubs.** They are field-only structs (a
  `Message`, a vestigial `Operation`, a `ReceiveTask.instantiate` flag) with no
  executor — they cannot run.
- **Correlation is modelled but unwired.** The `CorrelationKey` /
  `CorrelationSubscription` types exist; nothing computes or matches a key at
  run time. The broker matches on the message **name** plus a flat key.

### 1.3 Why now

ADR-011 v.5 §2.6 settled the `ServiceTask` `Operation` and explicitly **deferred
the node-level message-handling seam** (`MessageProducer` / `MessageConsumer`)
to "the executor SRD, where there are several implementors to force its shape."
That moment is here: `SendTask`, `ReceiveTask`, and the throw/catch message
events are exactly those implementors. Deciding the message model now gives the
engine its first real cross-participant capability and a clean seam the four
elements share, without re-opening the data-flow ADRs.

## 2. Decision

### 2.1 Messages travel the broker; the EventHub stays the internal wait machine

gobpm keeps two distinct channels and a bridge between them:

- **The message broker is the external message channel.** Every message a
  process *sends* — a `SendTask` or a throw message event — is **published to
  the `MessageBroker`** as an `Envelope`. Every message a process *waits for* —
  a `ReceiveTask` or a catch message event — is received by **subscribing to
  the broker**. The broker is the boundary across which messages cross to and
  from external participants (and between instances).
- **The EventHub remains the internal token-resume machine.** It owns the
  per-definition waiter and the track wait/resume lifecycle; it is how a waiting
  node is parked and woken. It is **not** the message transport.
- **A `MessageWaiter` bridges the two.** A waiting message node registers a
  `MessageWaiter` (the missing keystone) that subscribes to the broker for its
  message and, when an `Envelope` arrives, fires the event into the EventHub —
  so the track resumes through the *same* path a timer uses. This keeps one
  uniform wait/resume model for every trigger kind and confines broker
  knowledge to the waiter.

Rationale: messaging is inherently a boundary concern (it crosses participants),
so it belongs on the broker, not on the internal event bus; but a waiting
`ReceiveTask` must integrate with the existing track-resume lifecycle, so the
waiter, not the node, owns the broker subscription and adapts it to the EventHub.

### 2.2 A producer/consumer seam, shared by tasks and message events

Message handling splits by **direction**, as two narrow contracts (the seam
ADR-011 v.5 deferred):

- **`MessageProducer`** — binds its `Message` from process scope and **publishes**
  it to the broker. Implemented by `SendTask` and the **throw** message event.
- **`MessageConsumer`** — **subscribes** for its `Message`, and on arrival binds
  the payload into process scope. Implemented by `ReceiveTask` and the **catch**
  message event.

The seam is two interfaces, not one: BPMN message handling is genuinely
directional (send/throw vs receive/catch), and a node is one or the other. The
task and the event of the same direction share the *same* producer/consumer
implementation, so the message choreography (bind → publish, or subscribe →
bind) lives in exactly one place per direction and the activity/event wrapper
only adapts it to its execution shape (an activity completes; an event fires a
token).

### 2.3 SendTask is a message producer, synchronous to its lifecycle

A `SendTask` executes like any node executor: it binds its `Message` from the
execution's data (the message payload is filled from process scope by the
activity's input data associations), **publishes** the resulting `Envelope` to
the broker (message name + payload; correlation key per §2.6), and **completes**,
emitting its outgoing flows. It does **not** wait for a reply (a request/reply
exchange is two nodes, a send then a receive — the diagram shows the wait). The
send is synchronous to the activity lifecycle: the node is not Complete until the
publish returns.

### 2.4 ReceiveTask is a message consumer that waits, then binds

A `ReceiveTask` registers a `MessageWaiter` for its `Message` and moves its track
to the wait state (§2.1). When a matching `Envelope` arrives, the waiter fires
the event; the track resumes, the executor **binds the payload into process
scope** (through the activity's output data associations — the reverse of a
`SendTask`'s input binding), and the node completes, emitting its outgoing flows.
A `ReceiveTask` never times out by itself (a deadline is modelled with a boundary
timer event — the diagram shows the wait, per ADR-011's no-hidden-wait
principle).

### 2.5 The MessageWaiter parallels the TimerWaiter

The `MessageWaiter` is built the same way the timer waiter is: the waiter
registry gains a `TriggerMessage` builder; the waiter runs its own service loop,
subscribes to the broker for `(message name, correlation key)`, and on the first
matching `Envelope` calls the event-processing path that resumes the registered
track(s). The waiter carries the arrived payload to the node so the consumer can
bind it. This makes message waiting a peer of timer waiting — one waiter
abstraction, many trigger kinds — rather than a special path.

### 2.6 Correlation is phased: name-match now, key derivation later

Phase-1 (this ADR) routes a message by its **message name** (the broker's
`Envelope.Name`), with the correlation key passed through verbatim when present
but **not derived** from the model. This is enough for the common single-message,
single-waiter exchange and for the examples.

Full **correlation-key derivation** — computing a `CorrelationKey` from a
`CorrelationSubscription`'s retrieval expressions over the message payload and
matching it to the target instance — is **deferred** to a follow-up (§2.8). The
seam and the `Envelope.CorrelationKey` field are designed to carry a key, so
adding derivation later does not reshape the producer/consumer contracts.

### 2.7 Message-triggered instantiation is deferred

A `ReceiveTask` (or start message event) with `instantiate` set, starting a *new*
instance on an arriving message, is **deferred** (§2.8). It needs the engine to
route a broker message to a *process definition* (not a running instance) and
spawn an instance — a thresher-level concern beyond the per-instance executor
model this ADR decides. Phase-1 `ReceiveTask` runs inside an already-started
instance.

### 2.8 Non-goals and out of scope (each with a named home)

- **Correlation-key derivation** (CorrelationSubscription expressions → key,
  composite keys, key-based routing) — a follow-up *Correlation* SRD/ADR; the
  seam already carries a key.
- **Message-triggered instantiation** (`instantiate` ReceiveTask / start message
  event spawning an instance) — a follow-up, with the thresher message-routing
  work.
- **Service-operation-backed send** (a `SendTask`/`ReceiveTask` whose transport
  is a `service.Operation` call rather than a broker message) — a deferred
  alternative (§4); phase-1 message handling is `Message` + broker, so the
  vestigial `Operation` field on these tasks is removed by the implementing SRD
  and re-introduced only when service-backed messaging is needed.
- **Reply/timeout/transactional delivery, message ordering guarantees, dead-letter
  handling** — broker-quality concerns owned by the broker implementation and the
  future Distribution & Scale ADR (ADR-008), not the model layer.
- **Durable subscriptions / persistence** of a pending `ReceiveTask` across a
  restart — the Persistence ADR.

## 3. Consequences

- **The engine gains real cross-participant messaging.** Send and receive,
  tasks and events, all ride one broker and one wait model — the first capability
  that lets a gobpm process talk to the outside (and to other instances).
- **One choreography per direction.** The producer/consumer seam means
  bind→publish and subscribe→bind each live once; `SendTask`/throw-event and
  `ReceiveTask`/catch-event are thin wrappers, so the four elements cannot drift
  apart.
- **The wait model stays uniform.** A message wait is a peer of a timer wait
  through the `MessageWaiter`; the track lifecycle, history, and resume path are
  unchanged — no second wait mechanism.
- **No hidden waits.** A `ReceiveTask` waits visibly (it is a node on the
  diagram); a deadline is a boundary timer, consistent with ADR-011 §2.3. The
  engine never blocks on an invisible condition.
- **Correlation is honest about its phase.** Name-match now is documented as a
  limitation; the seam carries a key so derivation lands later without a
  contract change. A single instance per message name is the phase-1 assumption.
- **Cost: a new waiter, two executors, the seam, and a broker round-trip in the
  hot path.** The implementing SRD stages this (the waiter first, then the
  executors) and keeps `make ci` green per step.

## 4. Alternatives considered

- **Route messages through the EventHub instead of the broker.** Reuse the
  internal propagate path for messages too. Rejected: messaging is a boundary
  concern (it crosses participants and instances); folding it into the internal
  token bus conflates "resume my own track" with "deliver across participants",
  and gives no place for external transports. The broker is the right boundary;
  the EventHub stays internal.
- **One unified `MessageHandler` interface instead of producer/consumer.**
  A single contract with both send and receive. Rejected: BPMN message handling
  is directional and a node is exactly one direction; a unified handler forces
  every implementor to stub the half it does not use (the same reason ADR-011
  split the kinds rather than bolting both onto one type).
- **Make `SendTask`/`ReceiveTask` thin shims over the throw/catch message
  events.** Rejected as the *primary* structure: the activity and event lifecycles
  differ (an activity completes and runs data associations; an event fires a
  token). Sharing happens at the **producer/consumer** layer (§2.2), not by making
  a task pretend to be an event.
- **Implement correlation-key derivation now.** Conformant and complete, but it
  is a self-contained sub-problem (expression evaluation over the payload,
  composite keys, routing) that does not change the send/receive shape. Deferred
  (§2.6) to keep this ADR's scope on the core path; the seam carries the key.
- **Implement message-triggered instantiation now.** Rejected for this phase: it
  is a thresher/routing concern (message → definition → new instance), orthogonal
  to the per-instance executor model decided here (§2.7).
- **Send via a `service.Operation` (web-service style).** The standard allows a
  SendTask to call an Operation. Rejected for phase-1: gobpm's message channel is
  the broker; an Operation-backed transport is a deferred alternative (§2.8) that
  the producer seam can grow into without reshaping callers.

## 5. Enterprise-readiness recommendations

Advisory, not gating — for the implementing SRD(s) and later work:

- **Surface a failed publish / a never-arriving message as an incident.** A
  broker publish error, or a `ReceiveTask` waiting past a (modelled) deadline, is
  an operational event a process owner must see — a structured, classified
  failure carrying the activity and the message name, not a silent stall.
- **Make the phase-1 name-match limitation explicit to modellers.** Until
  correlation-key derivation lands, two live instances waiting for the same
  message name are ambiguous; user-facing docs must state the single-waiter
  assumption so a modeller is not surprised.
- **Log message handling by name/key/ids, never payloads.** Message payloads are
  business-sensitive; log the message name, correlation key, item ids, and
  states — consistent with ADR-010/011's masking recommendation.
- **Keep the broker contract swappable.** The in-memory broker is a default; the
  `MessageBroker` interface must stay narrow enough that a real transport (Kafka,
  NATS, …) drops in under the Distribution & Scale ADR without touching the model
  layer.

## 6. Open questions

- None. The send=publish / receive=MessageWaiter-subscribe model, the
  broker-vs-EventHub split with the bridging waiter, the directional
  producer/consumer seam, the phase-1 name-match correlation, and the deferral
  of key-derivation and instantiation are decided above. The exact interface
  signatures, the waiter's service-loop shape, and the data-binding wiring are
  implementation concerns for the landing SRD, not open conception questions.

## 7. References

- [SAD-001 v.1 Vision & Architecture](SAD-001-vision-and-architecture.md) — §14
  Conformance & Compliance Scope; the BPMN Process Execution Conformance target
  this messaging serves.
- [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) — the two-layer
  runtime, the track lifecycle, and the node-executor contract this messaging
  plugs into.
- [ADR-006 v.1 Events & Subscriptions](ADR-006-events-and-subscriptions.md) — the
  event-delivery and wait-node conception; message events are the message-typed
  case of its catch/throw model. Sibling — this ADR decides the message specifics
  it leaves open.
- [ADR-011 v.5 Process Data Flow](ADR-011-process-data-flow.md) — §2.6 deferred
  the node-level `MessageProducer`/`MessageConsumer` seam to here; the data
  associations that bind a message to/from scope are its §2.4.
- BPMN 2.0 §8.3.2 (Correlation), §8.4 (Messages & Operations), §10.3 (Tasks —
  Send/Receive), §10.4.2 (Data associations), §11 (Collaboration & message flow)
  — the messaging model this ADR encodes (and, for correlation/instantiation,
  phases).

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-14 | Ruslan Gabitov | Draft. Decides message handling: messages travel the `MessageBroker` (external channel) while the EventHub stays the internal wait machine, bridged by a new `MessageWaiter` (peer of the timer waiter); a directional **producer/consumer seam** (`MessageProducer` = `SendTask` + throw message event; `MessageConsumer` = `ReceiveTask` + catch message event) carries one bind→publish / subscribe→bind choreography per direction (the seam deferred from ADR-011 v.5 §2.6). `SendTask` publishes and completes; `ReceiveTask` waits via the `MessageWaiter`, then binds the payload to scope. **Phased core**: correlation routes by message name now (key-derivation deferred), and message-triggered instantiation is deferred (§2.8); the vestigial `Operation` field on the tasks is removed (service-operation-backed send is a deferred alternative). Refines ADR-001 v.5; sibling to ADR-006 v.1 and ADR-011 v.5. |
