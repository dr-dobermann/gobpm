# ADR-013 — Observability & control (one event seam, engine-wide)

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.2 |
| Date | 2026-07-11 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-002 v.2 Extension Architecture](ADR-002-extension-architecture.md) |
| Siblings | [ADR-022 v.1 Error Propagation and Logging Policy](ADR-022-error-propagation-and-logging-policy.md) — the log channel this ADR's single producer echoes into |

> Conception, now landed by the accompanying wiring SRD; v.2 supersedes v.1 as
> the accepted contract (DataChange emission is the one ⏳ deferral — its
> vocabulary is landed, its wiring rides the ADR-011 data-plane rework).
> **v.1** fixed the
> audit's finding 2.2 (the public API is write-only) with the
> observation-and-control mechanism: a public `InstanceHandle`, one lifecycle
> channel nodes and tasks publish into, explicit coarse control, the engine
> lifecycle (`Shutdown`, `UnregisterProcess`), and a standard-named **open**
> state vocabulary. **v.2** completes the *coverage*: v.1's landed form was
> deliberately minimal (see what happens inside one Instance); v.2 defines the
> **full observable-event taxonomy** across every major engine object — engine,
> event hub, process registration, instance, node, gateway decisions, events,
> correlation, worker jobs, user tasks, boundaries, faults, data — emitted
> through **one producer** that both feeds the observer stream and writes the
> operator-log echo (levels per ADR-022 v.1), and adds the **engine-scope
> observer registry** so non-instance events are observable too: one consistent
> view of gobpm. A **visibility-policy seam** (optional capabilities on the
> authorization extension; pass-through when unimplemented) lets an embedder
> hide or redact events per recipient — the policy model itself rides the
> future IAM work. Per-node *mutating* listeners stay rejected (hidden control,
> ADR-011); coarse *operator* control is not that. The concept is prescriptive;
> the accompanying SRD is code-grounded.

## 1. Context

### 1.1 What an embeddable engine owes its host

gobpm is embedded in an application (SAD-001). Today, once a process starts the
host is **blind** — it has nothing but log lines and console output to guess at
progress. An embeddable engine must instead let the host:

- **Observe** — the instance's lifecycle state, **where its tokens are and how
  they move**, which nodes are executing and their progress, its data, and its
  outcome (block until done).
- **Control** — coarse, explicit operator actions on a running instance: cancel
  it; (later) suspend and resume it.
- **React** — hook defined lifecycle moments (instance/node/flow/task events) to
  drive UI, audit, or integration.

This is not BPMN-normative (an embedding API is engine-defined), so it is a
product/architecture decision grounded in embeddability.

### 1.2 What the engine has today

**v.1's mechanism landed**: the `InstanceHandle` (state, tokens, data reader,
`WaitCompletion`, `Cancel`), the per-instance observer stream with its
async lossy delivery contract, and the engine lifecycle
(`Shutdown`, `UnregisterProcess`) all exist. The write-only-API finding (audit
2.2) is closed.

**But the landed coverage is deliberately minimal** — enough to see what
happens inside one Instance, and no more. A completeness audit of both
channels (the observer stream and the operator log, post the ADR-022 v.1
remediation) shows:

- The observer taxonomy carries **two event kinds**: the instance's lifecycle
  state (and not even `Created`), and a node-progress event **collapsed to a
  three-value token projection** — a listener cannot distinguish a node
  entering from executing from leaving, nor a completed node from a failed,
  canceled, or merged one.
- **Everything outside the Instance is invisible to observers**: engine and
  event-hub lifecycle, process registration/unregistration/version
  supersession, the whole external-worker job lifecycle, correlation
  decisions, user-task interactions, boundary-event arming and firing — all
  richly *logged* since ADR-022, none *observable*. The inverse also holds:
  instance state flips reach observers but write **no log**, though ADR-022
  §2.4 names lifecycle milestones an `Info` concern.
- Some transitions are silent on **both** channels: a gateway's branch
  decision, a user task being taken, boundary arm/disarm — and, sharpest, a
  **boundary-caught BPMN error**, which today leaves no trace anywhere (only
  an *uncaught* fault surfaces, as the instance-fault `Error`).
- The data plane has a **dormant change-notification mechanism**
  (`data.UpdateCallback`, with added/updated/deleted change kinds and an async
  fan-out) that no engine code consumes — wired to neither channel.

So the host has two half-views that do not even overlap consistently. v.2
exists to make the view **complete and consistent**: every failure and every
major-object lifecycle transition observable, engine-wide, through one
producer.

### 1.3 Why build the mechanism now (and the seam argument)

Two reasons not to wait:

- **Users are blind today.** Visibility into progress is table stakes; "read the
  logs" is not an answer for an embedded library.
- **The seam is cheapest to land before the nodes that feed it.** SendTask/
  ReceiveTask (ADR-014) and every future node should report progress through
  **one** channel. Building that channel now means new node/task work plugs into
  it; retrofitting observation into each node later is the expensive path.

### 1.4 Reconciling with the unsettled state model

The legitimate worry (raised in review) is that our lifecycle states are **not
yet standard-complete**: the instance lifecycle is partial (`Created → Active →
Completed`, `Terminating → Terminated`; `Failing`/`Failed` and `Paused` deferred
to future ADRs), and BPMN's **activity** lifecycle (§13.3.2:
`Ready → Active → Completing → Completed`, `Withdrawn`/`Compensating`/…) is **not
modelled at all**. Exposing a *frozen state enum* publicly now would churn the
public API when those subsystems land.

The resolution separates the **mechanism** from the **vocabulary**:

- The **mechanism** — the handle, the channel, the control ops — is the stable
  public contract decided here.
- The **vocabulary** — the set of lifecycle states and node kinds the mechanism
  reports — is **named to the BPMN standard for the subset we already have, and
  left an open set**. Standard naming is cheap (it is naming, not implementing
  error/compensation/suspend); and **adding** states/kinds later is additive and
  non-breaking. So we get visibility now without freezing an incomplete enum:
  the deferred states (`Failing`, `Paused`, `Compensating`, the activity
  sub-states) join the same vocabulary as their subsystems land.

This is the direct answer to "align to the standard, or extend later?" — **align
the *names* now (stable), extend the *set* additively**.

## 2. Decision

### 2.1 A public InstanceHandle — observe and control

Starting a process returns a public **`InstanceHandle`** (lookupable by id), the
host's window into one instance:

- **`State()`** — the lifecycle state, from the standard-named open vocabulary
  (§2.4), read lock-free.
- **Token view** — a snapshot of where execution is (which nodes hold tokens)
  and a stream of **token-movement** events through the channel (§2.2), so the
  host can follow progress, not just sample it.
- **Node execution progress** — which nodes are active and their progress
  (entered → executing → left), reported by the nodes themselves (§2.2).
- **Data read** — process properties + runtime variables, **read-only**, via
  the public read-only data reader (ADR-011 v.5 §2.6; the "observe from outside"
  use case ADR-010/011 deferred here).
- **`WaitCompletion(ctx)`** — block until the instance finishes or `ctx` is done;
  returns the terminal state + any error (replaces the examples' `done` channel).
- **Control** — `Cancel(ctx)` now (drives `Terminating → Terminated`);
  `Suspend`/`Resume` **reserved** (they need the deferred `Paused` subsystem and
  join additively). Control is **coarse and explicit** — an operator action on
  the whole instance, recorded on the channel.

Observation is concurrency-safe (lock-free state, data-plane lock, copied token
snapshot); control goes through the engine's own state machine, never a back door.

### 2.2 One lifecycle channel that nodes and tasks publish into

The host registers **observers** on a single channel. v.1 sketched the event
families in prose (instance / token-movement / node-execution / task events);
**v.2 replaces that sketch with the canonical taxonomy of §2.6**, which is the
authoritative catalog of what the channel carries.

Crucially, **nodes and tasks report their own progress into this one channel** —
it is the seam future node/task implementations plug into (ADR-014's SendTask/
ReceiveTask included). Adding a node kind means emitting its events here, not
inventing a new observation path.

Observers are **read-only over data and flow**: they receive an event (ids,
names, the standard-named state, timestamps — **never** payloads, per the
log-masking rule) plus the read-only handle, and **cannot alter data or redirect
flow**. That follows ADR-011's no-hidden-control principle. (Explicit *lifecycle
control*, §2.1, is a separate, visible, operator-initiated thing — not a
listener side effect.)

**Delivery is asynchronous and never blocks the track — a channel is a blocking
primitive, so the engine must not wait on an observer to read.** The contract:

- The event stream is **best-effort, lossy, ordered**. Per observer: a
  **buffered channel** (size N) drained by **one dedicated goroutine** that calls
  the observer; the track emits with a **non-blocking send**
  (`select { case ch <- ev: default: dropped++ }`). The track never blocks; memory
  is bounded; ordering is preserved (one channel + one drain goroutine); a slow
  observer drops events and **learns how many** (a surfaced `dropped` counter),
  and cannot affect the engine or other observers. A panicking observer is
  recovered, not propagated. This is plain stdlib (`chan` + a goroutine +
  `sync/atomic`).
- **Terminal completion is the one guaranteed, blockable signal — and only
  because the host asked for it.** `WaitCompletion(ctx)` is backed by a
  `done` channel the track **closes** on the terminal state (plus a stored
  result), never a send: closing is non-blocking for the engine and releases all
  waiters at once, so "completed/failed/terminated" is never dropped even though
  the lossy stream may drop progress events.

(A drop-**oldest** ring — keeping the freshest progress — is a possible later
refinement behind the same observer contract; drop-newest + counter is the
robust default and is what this ADR decides.)

### 2.3 Lifecycle control is coarse, explicit, and engine-mediated

Control operations act on the **whole instance** through the engine's state
machine:

- **`Cancel(ctx)`** — request termination; the instance walks
  `Active → Terminating → Terminated`, tokens are withdrawn, the channel reports
  it. Available now.
- **`Suspend(ctx)` / `Resume(ctx)`** — pause/continue token movement.
  **Reserved**: they require the `Paused` state and suspend subsystem deferred by
  ADR-001 §4.2; the handle declares them so the contract is stable, and they
  activate when that subsystem lands.

This is distinct from the rejected mutating per-node listener (§4): control is a
visible operator action on the instance, not invisible logic injected into a
node's execution.

### 2.4 The state & node-kind vocabulary is standard-named and open

The states the mechanism reports use **BPMN-standard names** for the subset that
exists (process/instance: `Active`, `Completed`, `Terminating`, `Terminated`;
activity, as it is modelled: `Ready`, `Active`, `Completed`), and the set is
**explicitly open** — `Failing`/`Failed`, `Paused`, `Compensating`, and the
remaining activity sub-states join it additively as their subsystems land. The
public contract is the *handle and channel shapes*, not a closed enum; a host
must treat an unknown state/kind gracefully (forward-compatible). This makes the
observability surface stable today and standard-complete over time without a
breaking change — the reconciliation of §1.4.

### 2.5 Engine lifecycle — graceful shutdown and process unregistration

- **`Thresher.Shutdown(ctx)`** — graceful stop: stop accepting starts, settle (or
  cancel on deadline) running instances, and close the event machinery and its
  waiters. The public contract is decided here; the waiter-goroutine ownership/
  `WaitGroup` mechanics are audit 2.5, landing with ADR-006 — `Shutdown` is its
  public consumer.
- **`UnregisterProcess(id)`** — remove a process definition + its snapshot,
  fixing the `snapshots` leak (2.2); rejects (or documents the policy for)
  removal with live instances.

### 2.6 The observable-event taxonomy (v.2) — kinds, phases, scope, log echo

The canonical catalog. Each **kind** names an object class; its **phases** are
an open, standard-named set (§2.4 discipline — extend additively, consumers
tolerate unknowns); **scope** says which observer registry sees it natively
(§2.8 — engine-scope observers see everything); **log echo** is the operator-log
level the single producer (§2.7) writes, per the ADR-022 v.1 §2.4 semantics.

| Kind | Object | Phases (open set; ⏳ = reserved slot) | Scope | Log echo |
|---|---|---|---|---|
| `EngineState` | Thresher | Starting, Started, Paused, Stopping, Stopped (⏳Resumed as a distinct phase — resuming re-emits Started meanwhile) | engine | Info |
| `HubState` | EventHub | Started, Stopped, ⏳Paused/Resumed | engine | Info |
| `ProcessLifecycle` | process definition | Registered, Unregistered, VersionSuperseded | engine | Info |
| `InstanceState` | instance | Created, Active, Terminating, Completed, Terminated, Failed, ⏳Suspended/Resumed | instance | Info (Failed → Error) |
| `NodeProgress` | node on a track | Entered, Executing, Completed, Failed, Canceled, Merged, Parked (BPMN §13.3.2-aligned, un-collapsed — §2.10) | instance | Debug |
| `GatewayDecision` | gateway | BranchesChosen (the taken flow(s) in details) | instance | Debug |
| `EventFlow` | event definition | Registered, Fired, Delivered, Dropped, Unregistered | engine | Debug |
| `Correlation` | conversation | KeyAssociated, Matched, Mismatched | instance | Debug |
| `JobState` | worker job | Enqueued, Locked, Completed, TechnicalFault, BusinessError, RetryScheduled, RetriesExhausted, LockReclaimed, ⏳Incident | engine | Debug (RetriesExhausted, LockReclaimed → Warn) |
| `TaskState` | user task | Announced, Taken, Completed, Withdrawn | both | Info |
| `Boundary` | boundary event | Armed, Fired, Disarmed | instance | Debug |
| `Fault` | BPMN error / fault | Thrown, Caught, Uncaught | both | Debug (Thrown, Caught — a designed path is expected behavior) / Error (Uncaught — the instance fault) |
| `DataChange` ⏳ | data element | Value_Added, Value_Updated, Value_Deleted (the existing change-kind vocabulary) | instance | **none** — observer-stream only (§2.10 volume guard) |

The table IS the listener contract: a listener implementation subscribes to
kinds and switches on phases; both axes grow additively. The **emission
completeness rule**: every failure and every transition named here MUST be
emitted — an unemitted catalog transition is a defect of the same class as a
silently discarded error (ADR-022 §2.6: accidental silence is worse than
noise).

### 2.7 One Reporter, two channels

v.1 emitted observer events and (since ADR-022) operator logs **independently**
— which is exactly how the two half-views of §1.2 diverged. v.2 unifies the
**Reporter**, not the channels:

- The record is a **`Fact`** (§2.7a names the vocabulary). Every catalog
  transition is emitted by **one call** — `Report(fact)` on the runtime's
  `Reporter` — which **internally** (a) writes the operator-log echo at the
  kind's §2.6 level, with the canonical ADR-022 §2.5 attribute keys, and (b)
  hands the Fact to the observer registries of its scope (§2.8) under the v.1
  async lossy delivery contract.
- **One call site per transition** means the log and the observer stream can
  never drift apart again: completeness is enforced at a single seam, and a
  reviewer checks *one* emission per catalog row, not two.
- Downstream, the channels **stay separate** exactly as ADR-022 §2.7 requires
  — the log synchronous and reliable for operators, the observer stream
  best-effort, lossy, and non-blocking for programmatic listeners. v.2
  refines that rule to: *separate channels, single Reporter*.

### 2.7a The reporting policy — Fact vs diagnostic, one non-nil Reporter

The terminology is deliberate: BPMN "Event" is load-bearing domain vocabulary
(Start/End/Boundary events, Message/Timer/Signal/Error triggers), so the
observability record is a **`Fact`**, never an "event". The canonical names:

- **`Fact`** — the one observation record (identity + `Kind` + `Phase` +
  `Details`; masked, never payload), from emitter to delivery. There is no
  second "public event" projection.
- **`Reporter`** — the joiner behind `Report(Fact)`: it echoes the Fact to the
  operator log AND fans it out to the registered observers. The default is
  echo-only; the engine's richer Reporter adds the observer registries.
- **`Observer` / `OnFact(Fact)`** — the ONE interface a host implements to
  watch the engine; a host registers it and never constructs a Reporter.
  `Observer`, `Fact`, and the `Kind`/`Phase` vocabulary are canonical in
  `pkg/observability`.

**The boundary rule — a report is a Fact iff it names a `(Kind, Phase)` in the
§2.6 catalog** (a lifecycle transition or failure of a first-class engine/domain
object). Everything else is a **diagnostic**. This is a checklist, not a
judgment call:

1. A catalog fact is emitted through the **one Reporter**, never `Logger()`d
   directly. The Reporter echoes it (level per the §2.6 table) and fans it out.
2. **The emitter never chooses the echo level** — the `kind+phase` table does
   (an unclassified kind surfaces loudly). Diagnostics are logged at their site;
   being logged *is* their purpose.
3. Diagnostics use `Logger()` only and never fabricate a `Kind`. They are
   free-form, may carry rich errors/stacks, and target a human debugging the
   *engine* — not a process-monitoring listener (a retry backoff calc, a
   subscription-extend failure, the startup banner, an infra-loop error).
4. **Bias to Facts.** If a consumer would plausibly *subscribe* to it, it is a
   Fact — grow the §2.6 catalog rather than leave it a diagnostic.
5. Diagnostics are a small, enumerable set per module; if that set grows,
   re-check whether an item should be promoted to the catalog.

**The single-non-nil-Reporter invariant.** Every module that reports (Thresher,
Instance, EventHub, the dispatcher) holds exactly **one** `Reporter`, and it is
**never nil** — a module reaches it through the runtime (`EngineRuntime.Reporter()`
returns a non-nil echo-only default when no richer Reporter is installed) or, for
a component that holds no runtime (the dispatcher), a non-nil default set at
construction that the engine overrides at wiring. A module NEVER decides "log or
observe" per call and never falls back between a sink and a logger; it holds a
Reporter and calls `Report`. `Logger()` remains a separate accessor, retained
strictly for the diagnostics of rule 3.

### 2.8 Two observer scopes — instance and engine

v.1's registry is per-instance (`AddObserver` on the handle) — right for
"follow my instance", but structurally unable to carry engine, hub, process,
or worker-job events, which belong to no instance. v.2 adds the missing scope:

- **Instance scope** (exists): observers registered on an `InstanceHandle`
  receive that instance's events — `InstanceState`, `NodeProgress`,
  gateway/correlation/boundary/task/fault/data events of that instance.
- **Engine scope** (new): observers registered on the engine receive
  **everything** — the engine-scope kinds natively (engine, hub, process,
  job) **and every instance-scoped event of every instance** (each event
  carries its `instance_id` in the details, §2.9). One subscription = the
  consistent whole-engine view; a listener filters by kind/id rather than
  juggling per-instance registrations.
- Both scopes share the same event shape, the same delivery contract
  (per-observer buffered channel, non-blocking send, drop counter, panic
  containment), and the same read-only rule.

### 2.9 The event payload — one attribute vocabulary across both channels

The event keeps v.1's identity-only shape (timestamp, node id/name, the
phase/state string, the kind) and gains a flat **string-to-string details
map** for kind-specific identifiers — a job id, a task id, the chosen gateway
flows, a correlation key name, an error code. Two rules:

- **The details keys ARE the ADR-022 §2.5 canonical log-attribute vocabulary**
  (`instance_id`, `node_id`, `job_id`, `task_id`, `correlation_key`/`_value`,
  `error`, …). One vocabulary serves both channels, so a listener and a log
  dashboard correlate on the same names — and the §2.7 producer can write the
  log echo directly from the event's details.
- **Masking holds**: ids, names, states, codes — **never payload values**
  (ADR-010/011). `DataChange` reports *that* a named element changed and
  how (added/updated/deleted), never what it now contains; a listener that
  needs the value reads it through the read-only data reader, visibly.

### 2.10 Corrections to the landed v.1 minimal form

Three defects of the landed minimum become contract-level requirements:

- **Un-collapse node progress.** The landed node event projects everything
  onto a three-value token state; §2.6's `NodeProgress` phases carry the
  real, BPMN-named execution phase (entered/executing/completed/failed/
  canceled/merged/parked). The token *projection* remains available on the
  handle's token view — it is the collapse into the *event stream* that goes.
- **Faults are first-class events.** A BPMN error caught at a boundary is a
  designed, expected path — but it must be *visible* (`Fault: Caught`,
  Debug echo), not silent as today; an uncaught fault is `Fault: Uncaught`
  with the `Error` echo (the existing instance-fault record becomes this
  kind's log echo). `Thrown` completes the triple so a listener can follow an
  error from raise to disposition.
- **Data changes are observable — ⏳ deferred to the data-plane redesign.**
  `DataChange` is a named kind in the taxonomy (a listener contract keeps the
  row), but its **emission is deferred**. The intended source was the data
  plane's existing `Value` change-notification callback (added/updated/deleted,
  async fan-out) — but the execution model is **frame-clone-then-replace**: a
  node works on a cloned frame copy and the frame commit *replaces* the
  container-scope value object (`Scope.Commit`: `vv[name] = d`), so a callback
  registered on the original value is bypassed and observes few or none of the
  real changes. Rather than wire a mechanism against a data plane that is about
  to be redesigned, `DataChange` observability is designed **together with** the
  structural-data + mapping rework (ADR-011) — the change-notification mechanism
  it depends on is itself part of that redesign. When it lands it is
  observer-stream only, **no log echo** (at the hot-path volume — roughly ten
  writes per node — even `Debug` would drown flow tracing; the lossy stream is
  the right transport, and the ADR-022 hot-path corollary stays intact).

### 2.11 Visibility policy — hide-by-policy at the delivery edge

Identifiers and names are themselves information: a `correlation_value` is a
business value (possibly PII), node/task names can be sensitive, and the
engine-scope observer (§2.8) sees **every** instance — in a multi-tenant host,
one tenant's listener must not see another tenant's events. Structural masking
(§2.9) keeps payload *values* out; visibility of the rest is a **policy**
question, and policy belongs to the engine's policy authority.

The mechanism is the house **optional-capability pattern** (a type assertion
against an existing extension — the same way the hub's key-extension and the
worker-config capabilities work), anchored on the **authorization provider**
(ADR-002's auth extension), since "who may see what" is an authorization
concern:

- **Log visibility** — if the provider implements the log-redaction capability
  (working name `LogRedactor`), the §2.7 producer passes each event through it
  before writing the log echo: the policy may pass it, redact detail keys, or
  suppress the record. One recipient (the log), one global decision.
- **Observer visibility** — if the provider implements the observation-filter
  capability (working name `ObservationFilter`), each event is checked **per
  recipient** before delivery: allow as-is, deliver with redacted details, or
  deny (the observer never sees the event; drop counters are not incremented —
  a denied event is policy, not backpressure).
- **Not implemented ⇒ pass-through.** No capability, no policy: events reach
  the log and the observers exactly as produced. The default costs nothing —
  the capability is asserted **once at wiring** (engine start / observer
  registration), never per event.

The **policy model itself** — identities, tenants, attribute classifications,
per-kind rules — is deliberately *not* designed here: it arrives with the
multi-tenancy/IAM work, implemented on the same authorization extension. This
ADR fixes the seam, its anchor, and its zero-cost default; exact interface
names and signatures are confirmed by the wiring SRD.

### 2.12 Non-goals and scope (phased core; each deferral named)

- **Mutating per-node listeners** (an observer that sets a variable, redirects a
  flow, vetoes a transition) — rejected on principle (§4), not deferred. Behaviour
  changes go in the model, visibly.
- **Fine-grained execution control / a step-debugger** (single-step a token,
  breakpoints) — out of scope; control is coarse (instance-level).
- **Implementing the deferred lifecycle states** (`Failing`/`Paused`/
  `Compensating` and their subsystems) — owned by the error-handling, suspend,
  and compensation ADRs; this ADR only reserves their **names/slots** in the open
  vocabulary so they extend additively.
- **The waiter-goroutine shutdown mechanics** (2.5) — ADR-006; this ADR decides
  the public `Shutdown` contract that drives it.
- **Persistence / durable history** (querying finished instances after restart) —
  the Persistence ADR; the channel and handle are live/in-memory here.
- **Splitting the `Instance` god-object** (audit 2.3) — landed as a sibling
  refactor; no longer open.
- **The exact handle/observer/control interface shapes and the token/progress
  representation** — implementation decisions for the SRD(s), staged green.
- **The v.2 taxonomy wiring** — emitting every §2.6 kind through the §2.7
  producer, the engine-scope registry, the payload details map, the §2.11
  visibility capabilities, and the three §2.10 corrections — rides the
  accompanying SRD; this ADR fixes the contract.
- **The visibility policy model** (identities, tenants, attribute
  classifications, per-kind rules) — the multi-tenancy/IAM work owns it,
  implemented on the same authorization extension §2.11 anchors to; this ADR
  only fixes the capability seam and its pass-through default.
- **Metrics/tracing export** of the same events (an OpenTelemetry bridge over
  the engine-scope observer) — a natural consumer of this contract, deferred
  to its own work.

## 3. Consequences

- **The public API stops being write-only.** A host follows state, token
  movement, node progress, data, and outcome live — and can cancel — instead of
  reading logs (2.2 closed).
- **One seam for all nodes.** Every node/task reports progress into one channel;
  ADR-014's executors and future kinds plug in there, not into bespoke paths.
- **Observability survives state evolution.** Standard-named, open vocabulary +
  forward-compatible consumers mean the deferred states land additively — no
  public-API churn (the §1.4 worry resolved by design).
- **Control is safe and visible.** Coarse, engine-mediated cancel/suspend can't
  corrupt execution; no hidden per-node control is introduced.
- **The engine gains a real lifecycle.** `Shutdown` (forcing the 2.5 waiter
  question) and `UnregisterProcess` (fixing the leak).
- **New public surface to keep stable** under ADR-002 §4.7 — the handle, channel,
  and control ops; kept narrow and forward-compatible.
- **Cost: a projection + a channel threaded through the track + cancel wiring.**
  Bounded by read-only-observation + coarse-control constraints; the SRD stages
  it, and nodes adopt progress-reporting incrementally.
- **(v.2) The two channels cannot drift.** One producer per transition writes
  both the log echo and the observer event — mirror-by-construction, one
  review point per catalog row, and the ADR-022 handle-once rule extends
  naturally (an `Observe()` call IS the single handling of that transition's
  reporting).
- **(v.2) Listeners become a real platform.** With the full §2.6 taxonomy and
  the engine scope, audit trails, UIs, integration bridges, and a future
  OpenTelemetry export all hang off one stable contract instead of scraping
  logs.
- **(v.2) Cost: an engine-level registry + one emission per catalog row.**
  The per-event work is bounded (a map of ids + a non-blocking send); the
  hot-frequency kind (`DataChange`) deliberately skips the log echo, and
  the emission sites are exactly the places the error-and-logging remediation
  already visited — well-known, recently-touched code.

## 4. Alternatives considered

- **Freeze a closed state `enum` in the public API now.** Simple and typed.
  Rejected: our states are incomplete (§1.4); a closed enum churns when
  `Failing`/`Paused`/`Compensating` land. The standard-named **open** vocabulary
  (§2.4) gives the same clarity, stays stable, and extends additively.
- **Defer the whole mechanism until the lifecycle is standard-complete.** My
  first instinct. Rejected (correctly pushed back): it leaves hosts blind for a
  long time (the deferred subsystems are far off), and it forces every future
  node to be retrofitted with observation later — the expensive order. The seam
  should exist first; the vocabulary grows into it.
- **Camunda-style mutating execution/task listeners.** Powerful but they are
  exactly the invisible, out-of-diagram control ADR-011 forbids; process
  behaviour would depend on registered code a diagram reader can't see. Rejected
  — observation is read-only; *operator* control is explicit and coarse (§2.3).
- **Return a raw `*Instance` / expose `internal/instance`.** Leaks the
  god-object (2.3), its mutating methods, and its lock discipline; a host could
  corrupt a running instance. Rejected for the narrow handle.
- **Polling only (no channel).** Can't catch transient token-movement / node
  events between polls and burns cycles. Rejected as the *only* mechanism
  (polling via the handle stays available for simple cases).
- **Fold into the metrics/tracer extensions (ADR-002).** Aggregate telemetry is
  a different need from per-instance state/control + lifecycle callbacks.
  Complementary, not a substitute.
- **(v.2) Keep two independent emissions per transition — a log call AND an
  observer call, synchronized by convention (a "mirroring rule").** The first
  v.2 draft. Rejected on review: it doubles every call site, and convention
  is exactly what let the two channels diverge in the first place (instance
  states observable-but-unlogged, worker events logged-but-unobservable).
  The single producer makes the mirror structural.
- **(v.2) A flat per-transition event enum** (one value per catalog row,
  ~40+ values). Rejected: brittle and ever-growing; the kind + open-phase
  model matches §2.4's vocabulary discipline and lets both axes extend
  additively.
- **(v.2) Merge the channels — deliver log records to observers, or logs via
  an observer.** Rejected: the reliability contracts differ by design
  (ADR-022 §2.7) — logs must not become lossy, and the stream must not become
  blocking; only the *producer* unifies.
- **(v.2) Log everything instead of extending observers** (completeness on the
  log channel alone). Rejected: leaves programmatic listeners blind — logs are
  for operators; the listener platform is the stream.

## 5. Enterprise-readiness recommendations

Advisory, not gating — for the implementing SRD(s):

- **Mask payloads** in every event (names/ids/states/timestamps only), per
  ADR-010/011.
- **Contain observer failures** (recover/timeout/drop-with-warning) so an
  observer can never stall or crash a track.
- **Make consumers forward-compatible** — document that the state/kind set is
  open; a host must tolerate unknown values (so additive growth never breaks it).
- **Make `Cancel`/`Shutdown` idempotent and ctx-bounded**; document what happens
  to in-flight instances on a `Shutdown` deadline.
- **Return a completion *result*** (terminal state + error/incident) from
  `WaitCompletion`, not just a state.
- **(v.2) Pair the visibility capabilities (§2.11) with compliance needs**:
  PII/GDPR redaction on the log channel via `LogRedactor` + a JSON handler,
  tenant isolation on the engine-scope observer via `ObservationFilter`;
  document that a denied event is invisible by policy, not lost by
  backpressure (the drop counter stays honest).

## 6. Open questions

- None. v.1 decided the mechanism (handle + channel + coarse control + the
  async lossy delivery contract + the open vocabulary). v.2 decides the
  coverage: the **canonical taxonomy** (§2.6 — kinds, phases, scopes, log-echo
  levels, reserved slots), the **single producer** over two still-separate
  channels (§2.7), the **engine-scope registry** receiving everything (§2.8),
  the **details map keyed by the ADR-022 §2.5 vocabulary** with masking intact
  (§2.9), the three **corrections** to the landed minimum — un-collapsed
  node phases, first-class faults, data changes via the existing callback with
  no log echo (§2.10) — and the **visibility-policy seam** (optional
  capabilities on the authorization extension, pass-through default; §2.11).
  Exact type shapes, the emitter API's form, buffer sizes, and the staging of
  emission sites are implementation concerns for the accompanying SRD(s).

## 7. References

- [SAD-001 v.1 Vision & Architecture](SAD-001-vision-and-architecture.md) — the
  library-embedded-in-a-host goal that makes observability/control table stakes.
- [ADR-001 v.6 Execution Model](ADR-001-execution-model.md) — the instance/track
  lifecycle whose states this ADR names per the standard and whose deferred
  states (`Failing`/`Paused`, §4.2 §9) the open vocabulary reserves slots for.
- [ADR-002 v.2 Extension Architecture](ADR-002-extension-architecture.md) — the
  engine-level extension catalogue (§4.2) observers register through; the §4.7
  public-API versioning this surface joins.
- [ADR-006 v.2 Events & Subscriptions](ADR-006-events-and-subscriptions.md) — the
  waiter lifecycle (2.5) `Shutdown` must close; sibling.
- [ADR-022 v.1 Error Propagation and Logging Policy](ADR-022-error-propagation-and-logging-policy.md)
  — the log channel's levels (§2.4), attribute vocabulary (§2.5), and the
  two-channel separation (§2.7) this ADR's single producer writes into and
  refines to "separate channels, single producer".
- [ADR-010 v.2 Process Data Model](ADR-010-process-data-model.md) — the data
  plane (own lock → safe external reads) + §2.7 reads the handle's data view
  reuses; deferred "observe from outside" to here.
- [ADR-011 v.5 Process Data Flow](ADR-011-process-data-flow.md) — the
  no-hidden-control principle the read-only-observer rule follows; the public
  read surface the handle mirrors.
- [ADR-012 v.1 Execution Layering](ADR-012-execution-layering.md) — the
  public-contract surface this handle/channel extend (concurrent sibling).
- [ADR-014 v.1 Message Handling](ADR-014-message-handling.md) — SendTask/
  ReceiveTask report progress into this ADR's lifecycle channel.
- BPMN 2.0 §13.3.2 (Activity lifecycle, spec p428–429) + §13.2/§13.5.6 (process
  lifecycle) — the state names this ADR aligns to (and extends as subsystems
  land); digested in `docs/bpmn-spec/state-machines/`.
- Architecture audit 2026-06-11 (`docs/audit/architecture-audit-2026-06-11.md`) —
  finding 2.2 (write-only API; `Shutdown`/`UnregisterProcess`/snapshots leak);
  touches 2.5 (waiter lifecycle, ADR-006).

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.2 | 2026-07-11 | Ruslan Gabitov | Accepted (landed by the accompanying wiring SRD). **Observability completeness — one event seam, engine-wide.** v.1's landed form was deliberately minimal (two observer-event kinds, instance scope only); a two-channel completeness audit showed the observer stream and the ADR-022 operator logs each covering a different half of the engine, with some transitions (gateway decisions, task-taken, boundary arm/disarm, boundary-**caught** faults) silent on both. v.2 adds: the **canonical observable-event taxonomy** (§2.6 — 13 kinds across engine/hub/process/instance/node/gateway/event/correlation/job/task/boundary/fault/data, each with an open phase set, a scope, a log-echo level, and ⏳ reserved slots for pause/resume/incident/dehydration) with the **emission-completeness rule** (an unemitted catalog transition is a defect); the **single producer** — one `Report(fact)`-style call per transition that writes the log echo AND feeds the observer stream, refining ADR-022 §2.7 to *separate channels, single producer* (rejects the two-independent-emissions "mirroring rule" that let the channels drift); the **engine-scope observer registry** (receives everything, incl. all instances' events — one consistent view of gobpm) alongside v.1's instance scope; the **details map** keyed by the ADR-022 §2.5 canonical attribute vocabulary (one vocabulary across both channels; masking intact — ids/names/codes, never payload values); and three **corrections to the landed minimum** — un-collapsed node-progress phases (the 3-value token projection stays on the handle, not in the stream), first-class `Fault` (Thrown/Caught/Uncaught — a boundary-caught error becomes visible), and `DataChange` (⏳ deferred to the ADR-011 data-plane rework — its vocabulary is landed, its wiring rides that rework; observer-stream-only, no log echo when it lands). Adds the **visibility-policy seam** (§2.11): optional capabilities on the authorization extension — log redaction (working name `LogRedactor`) and per-recipient observation filtering (`ObservationFilter`), discovered by type assertion at wiring; unimplemented ⇒ pass-through (events reach the log/observers as-is, zero cost); the policy model (identities/tenants/classifications) deferred to the multi-tenancy/IAM work on the same extension. Non-goals updated (god-object split landed; OTel bridge + the policy model deferred); references re-pinned (ADR-001 v.5→v.6, ADR-006 v.1→v.2, ADR-022 v.1 added). Conception; the taxonomy wiring rides the accompanying SRD. |
| v.1 | 2026-06-18 | Ruslan Gabitov | Accepted. Fixes audit 2.2 by building the observation-**and-control** mechanism: a public `InstanceHandle` (state, token movement, node execution progress, read-only data via the public reader (ADR-011 v.5), `WaitCompletion`, `Cancel` now / `Suspend`·`Resume` reserved), **one lifecycle channel that nodes/tasks publish progress into** (the seam future node/task work plugs into), and the engine lifecycle (`Shutdown`, `UnregisterProcess`, fixing the snapshots leak). Reconciles the unsettled-state concern: the **mechanism** is the stable contract while the **state/node vocabulary is named per the BPMN standard but kept an open set** — align names now, extend additively as `Failing`/`Paused`/`Compensating` subsystems land (no public-API churn; consumers forward-compatible). Observers are read-only over data/flow (no mutating listeners — hidden control, ADR-011); lifecycle control is coarse, explicit, engine-mediated. **Async delivery** (stdlib): best-effort lossy per-observer buffered channel + drain goroutine + non-blocking send + drop counter — the track never blocks on an observer; only terminal completion is a guaranteed, blockable signal (a closed `done` channel via `WaitCompletion`). Phased core: deferred lifecycle states/subsystems, fine-grained step control, waiter-shutdown mechanics (2.5 → ADR-006), persistence/history, and the Instance god-object split (2.3) are out of scope. Refines ADR-002 v.2; siblings ADR-001 v.5, ADR-006 v.1 (Accepted), ADR-010 v.2, ADR-011 v.5, ADR-012 v.1, ADR-014 v.1. Accepted at v.1 with pin/standard-claim corrections at acceptance: ADR-002 v.1→v.2; activity-lifecycle §13.2.2→§13.3.2 (the KB attributes it to §13.3.2, p428–429). Conception; implementation rides the SRD. |
