# ADR-013 — Instance observability & control (one lifecycle channel nodes plug into)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-14 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-002 v.1 Extension Architecture](ADR-002-extension-architecture.md) |

> **Draft — not yet implemented.** Fixes the audit's finding 2.2 (the public API
> is write-only): once a process starts, a host can learn **nothing** but what
> leaks to logs/console. This ADR builds the **observation-and-control
> mechanism** now — a public `InstanceHandle` (state, token movement, node
> execution progress, data read, wait-for-completion), one **lifecycle channel**
> that nodes and tasks publish into (so future node/task work plugs into one
> seam), explicit **lifecycle control** (cancel now; suspend/resume reserved),
> and the engine lifecycle (`Shutdown`, `UnregisterProcess`). The state/node
> vocabulary is **named per the BPMN standard but left an open set**, so it is
> stable now and extends additively as the deferred lifecycle subsystems land —
> the mechanism is the frozen contract, the vocabulary grows. Per-node *mutating*
> listeners stay rejected (hidden control, ADR-011); coarse *operator* control is
> not that. Conception only; the SRD is code-grounded.

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

The public surface is **write-only** (audit 2.2): `StartProcess(id)` returns only
an `error` — no handle, no state, no tokens, no completion, no data; the
`examples` thread a manual `done` channel through a service functor just to learn
the process ran. `Instance.State()` is `internal/`. There is **no
`Thresher.Shutdown(ctx)`** and **no `UnregisterProcess`** (the `snapshots` map
only grows — a leak). There is **no lifecycle channel** to subscribe to and **no
way to cancel** a running instance.

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
to future ADRs), and BPMN's **activity** lifecycle (§13.2.2:
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
  SRD-011's public reader surface (the "observe from outside" ADR-010/011
  deferred here).
- **`WaitCompletion(ctx)`** — block until the instance finishes or `ctx` is done;
  returns the terminal state + any error (replaces the examples' `done` channel).
- **Control** — `Cancel(ctx)` now (drives `Terminating → Terminated`);
  `Suspend`/`Resume` **reserved** (they need the deferred `Paused` subsystem and
  join additively). Control is **coarse and explicit** — an operator action on
  the whole instance, recorded on the channel.

Observation is concurrency-safe (lock-free state, data-plane lock, copied token
snapshot); control goes through the engine's own state machine, never a back door.

### 2.2 One lifecycle channel that nodes and tasks publish into

The host registers **observers** on a single channel carrying:

- **Instance** events — created / started / completed / terminated (and, later,
  failed / suspended / resumed).
- **Token-movement** events — a token entering/leaving a node, a flow taken.
- **Node-execution** events — node entered / executing / left, plus
  node-kind-specific progress a node chooses to report.
- **Task** events — user-task created / assigned / completed (to the extent
  user-task execution exists).

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

### 2.6 Non-goals and scope (phased core; each deferral named)

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
- **Splitting the `Instance` god-object** (audit 2.3) — sibling refactor.
- **The exact handle/observer/control interface shapes, sync-vs-async delivery,
  and the token/progress representation** — implementation decisions for the
  SRD(s), staged green.

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

## 6. Open questions

- None. Building the mechanism now (handle + one lifecycle/token/node channel +
  coarse control), the standard-named **open** state vocabulary (align names now,
  extend the set additively — §1.4/§2.4), read-only observers with explicit
  operator control, and the engine lifecycle (`Shutdown`/`UnregisterProcess`) are
  decided above — including the **async delivery contract** (best-effort lossy
  per-observer buffered channel + drain goroutine + non-blocking send + drop
  counter; terminal completion guaranteed via a closed `done` channel, §2.2).
  Exact interface shapes, the buffer size N, the token/progress representation,
  and how far the task-listener catalogue reaches are implementation concerns for
  the SRD(s); the waiter-shutdown mechanics belong to ADR-006 (2.5), and the
  deferred states to their subsystem ADRs.

## 7. References

- [SAD-001 v.1 Vision & Architecture](SAD-001-vision-and-architecture.md) — the
  library-embedded-in-a-host goal that makes observability/control table stakes.
- [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) — the instance/track
  lifecycle whose states this ADR names per the standard and whose deferred
  states (`Failing`/`Paused`, §4.2 §9) the open vocabulary reserves slots for.
- [ADR-002 v.1 Extension Architecture](ADR-002-extension-architecture.md) — the
  engine-level extension catalogue (§4.2) observers register through; the §4.7
  public-API versioning this surface joins.
- [ADR-006 v.1 Events & Subscriptions](ADR-006-events-and-subscriptions.md) — the
  waiter lifecycle (2.5) `Shutdown` must close; sibling.
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
- BPMN 2.0 §13.2.2 (Activity lifecycle), process lifecycle — the state names this
  ADR aligns to (and extends as subsystems land); digested in
  `docs/bpmn-spec/state-machines/`.
- Architecture audit 2026-06-11 (`docs/audit/architecture-audit-2026-06-11.md`) —
  finding 2.2 (write-only API; `Shutdown`/`UnregisterProcess`/snapshots leak);
  touches 2.5 (waiter lifecycle, ADR-006).

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-14 | Ruslan Gabitov | Draft. Fixes audit 2.2 by building the observation-**and-control** mechanism: a public `InstanceHandle` (state, token movement, node execution progress, read-only data via SRD-011's reader, `WaitCompletion`, `Cancel` now / `Suspend`·`Resume` reserved), **one lifecycle channel that nodes/tasks publish progress into** (the seam future node/task work plugs into), and the engine lifecycle (`Shutdown`, `UnregisterProcess`, fixing the snapshots leak). Reconciles the unsettled-state concern: the **mechanism** is the stable contract while the **state/node vocabulary is named per the BPMN standard but kept an open set** — align names now, extend additively as `Failing`/`Paused`/`Compensating` subsystems land (no public-API churn; consumers forward-compatible). Observers are read-only over data/flow (no mutating listeners — hidden control, ADR-011); lifecycle control is coarse, explicit, engine-mediated. **Async delivery** (stdlib): best-effort lossy per-observer buffered channel + drain goroutine + non-blocking send + drop counter — the track never blocks on an observer; only terminal completion is a guaranteed, blockable signal (a closed `done` channel via `WaitCompletion`). Phased core: deferred lifecycle states/subsystems, fine-grained step control, waiter-shutdown mechanics (2.5 → ADR-006), persistence/history, and the Instance god-object split (2.3) are out of scope. Refines ADR-002 v.1; siblings ADR-001 v.5, ADR-006 v.1, ADR-010 v.2, ADR-011 v.5, ADR-012 v.1, ADR-014 v.1. |
