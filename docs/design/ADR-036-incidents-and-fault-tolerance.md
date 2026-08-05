# ADR-036 — Incidents: technical failure as durable, operable state

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-08-04 |
| Owner | Ruslan Gabitov |
| Refines | [SAD-001 v.1.1](SAD-001-vision-and-architecture.md) §10 (persistence as the state of record — this ADR makes *failure* part of that record), [ADR-021 v.1](ADR-021-service-task-execution-model.md) §2.8 (the first-class Incident construct this ADR was deferred to), [ADR-022 v.2](ADR-022-error-propagation-and-logging-policy.md) (the propagation policy whose "goroutine top" consequence this refines) |
| Related | [ADR-001 v.6](ADR-001-execution-model.md) (tracks and token projection), [ADR-007 v.2.1](ADR-007-in-memory-long-waits.md) (the wait model an incident joins), [ADR-013 v.2](ADR-013-instance-observability.md) (the `Incident` phase reserved in the fact taxonomy), [ADR-017 v.1](ADR-017-channel-based-event-processing.md) (the loop as sole state owner), [ADR-025 v.2](ADR-025-activity-iteration-loop-and-multi-instance.md) (multi-instance interaction), [ADR-026 v.1](ADR-026-compensation-events.md) (the completion ledger interaction), [ADR-033 v.2](ADR-033-persistence-and-state.md) (the checkpoint an incident rides) |

## 1. Context & problem

The engine's present contract for a technical failure is **fatal and
invisible**. When an activity fails with anything other than a modeled BPMN
error, the failing track terminates, its token is consumed, every sibling
track is stopped, and the instance faults as a whole. Persistence records
none of it: a failed track is not checkpointable state, and the durable
record of a faulted instance says only that it terminated. The one retry
mechanism the engine owns lives below the instance loop — the job
dispatcher's retry policy for external workers (ADR-021 v.1 §2.7) — and when
it exhausts, the terminal fault surfaces as exactly the fatal, invisible
kind above.

For an embedded library this was a defensible early posture: the host
process observes the fact stream and owns its own supervision. For a
production engine it is not. A transient infrastructure failure — a database
blip inside one service task — must not destroy an instance that carries
hours of correct work in its other branches, and a failure that leaves no
durable record cannot be seen, diagnosed, retried or resolved by anyone.
ADR-021 v.1 §2.8 named this gap explicitly and deferred "a first-class
Incident construct (stuck-job registry + operator resolution surface, the
instance stays alive)" to its own ADR. This is that ADR.

**The standard is silent here, deliberately.** BPMN models *business*
failure — the Error event, its boundary catch, the scope-chain resolution —
and mandates nothing about *technical* failure. The vendored extract states
it directly: for an unresolved Error trigger "the spec does not mandate a
specific engine reaction" (`semantics/event-handling.md`, unresolved
trigger); the extract as a whole contains no notion of incident, retry or
technical failure. Everything this ADR decides is therefore an **engine
choice**, to be registered as such, with Camunda's incident model as the
alignment target the project has already declared.

## 2. Decision

### 2.1 The incident

An **incident** is the durable record of a failure the model did not handle:
it names the instance, the node, the failing track's lineage, the cause
chain, the attempt count, the timestamps of first and last failure — and a
**failure-time data snapshot**: the variables visible in the failing node's
scope chain, captured at each raise, so the operator sees what the attempt
*saw* rather than what the scope has since become. It is **state, not a
fact**: it lives in the instance's state of record, survives
restarts through the checkpoint, and exists until it is resolved. The
observability stream *announces* it (§2.7) but never carries it — the fact
stream stays best-effort and lossy by design (ADR-013 v.2).

What raises one:

| Failure | Reaction |
|---|---|
| **Technical failure** of an activity — an in-process task error, a data-flow failure, an expression failure | Incident at the failing node |
| **Job retries exhausted** — the ADR-021 dispatcher gave up on an external-worker job | Incident at the service task; the job's diagnostic (topic, attempts, last error) becomes the incident's cause |
| **Uncaught BPMN error thrown by a failing activity** — no boundary, no scope-chain catcher | Incident at the throwing node. The standard leaves the reaction open (§3); halting recoverably dominates dying, because an uncaught activity error is routinely a *deployment* mistake — a missing boundary — fixable by model correction and retry against live state |
| **Error End Event reaching the root uncaught** | Instance fails, as today. This is the **modeled outcome** — the author explicitly ended the instance in error (`semantics/end-events.md`: an Error End Event fails the instance; Camunda ends the scope likewise, no incident). Turning the model's own verdict into an operator ticket would invert its intent |
| **Invariant violation** — impossible engine state | Instance fault, as today. An incident asserts "the world outside misbehaved, the engine's state is sound"; an invariant violation denies the second half, and retrying against corrupt state compounds it |
| **Any uncaught failure in a child instance** (a Call Activity's callee, at any depth) | The child instance fails and the failure **propagates across the call boundary** to the caller's Call Activity node, typed errors staying catchable by its boundary. **Incidents exist only at top-level instances**: to its caller, the whole called process is a single task with two exits — success or failure — and the incident that finally arises at the (top-level) call node carries the child's diagnostics and offers the honest retry unit: **re-run the whole Call Activity**, or resolve/give up |

Every incident is logged at `Error` level when raised — an unresolved
failure is never silent (the standing uncaught-must-log rule), and ADR-022
v.2's one-failure-one-record discipline holds: the incident *is* the
handling point, so the log record accompanies the incident and nothing
re-logs it upstream.

### 2.2 The ended track, the open incident — token preservation

The failing track **terminates**, in a new terminal state — `TrackIncident` —
and the **incident record** takes over as the durable carrier of everything a
future attempt needs: the node, the scope path, the lineage, the cause chain,
the attempt count. This is the dehydration pattern applied to failure
(ADR-007 v.2.1): the goroutine-bearing thing ends, the recorded state is what
persists, and continuation is a **fresh track spawned from the record** —
exactly how a boundary catch births a new track at the boundary and how
rehydration respawns from a checkpoint. Each retry is its own track, with the
failed track as its predecessor, so attempt history is ordinary track lineage
rather than bookkeeping invented for incidents.

The node was entered but did not complete: no outgoing flow fires, no
completion is recorded, armed boundary events stay armed (§2.4), and the
**incident pins its scope open** — retry re-enters against that data, so the
scope cannot be disposed while the incident is. While the
incident is open, the **token remains visible at the failing node** — the
projection derives it from the open incident record, not from the terminal
track — because a preserved token that no view can see is not preserved in
any sense that matters to an operator.

Consequences, stated as contract:

- **Siblings are unaffected.** Parallel branches keep executing; other
  incidents may accumulate. One stuck payment call no longer destroys the
  shipping branch.
- **The instance stays alive.** Its condition becomes *active with
  incidents* — a queryable predicate, not a terminal state. An instance
  whose only remaining continuations are open incidents holds no goroutine
  at all and is quiescent like any all-waiting instance (ADR-007 v.2.1).
- **Completion waits.** An instance cannot complete while an incident is
  open on it; resolution (§2.6) is what un-sticks it.

### 2.3 Retry: two layers, each owning its kind

Retry ownership splits along the line ADR-021 v.1 drew:

- **Job retry — the first line, unchanged.** The dispatcher's policy
  (attempts, backoff, jitter; per-service and engine-wide defaults) keeps
  retrying external-worker jobs below the loop, with no track state change
  and no incident. An incident begins where that automation *ends*.
- **Incident retry — after automation gives up.** An open incident may be
  retried, which **spawns a fresh track at the failed node** from the
  incident record (§2.2), with the failed track as its predecessor: a fresh
  execution of the activity against the current scope data, under the same
  contract as the checkpoint-recovery re-entry (ADR-033 v.2 — a step is
  atomic, recovery re-enters the node, never the half-step; effects stay
  at-least-once).

There is **no hidden track-level automatic retry loop.** An in-process
task's failure goes straight to an incident. What softens that is the
**incident retry policy** — attempts and backoff, configurable per activity
with an engine-wide default — evaluated *off the loop*, in the pattern
ADR-021 v.1 §2.4 established: a scheduled retry is persisted state plus a
deadline, structurally a timer wait, with no sleeping goroutine. A policy of
zero attempts means every incident waits for an operator; the engine default
is deliberately conservative (retry helps transient faults, and a
deterministic bug retried N times is N identical failures — the attempt
count and unchanged cause make that visible rather than noisy).

### 2.4 Interactions — the three contract questions

**Boundary events.** An incident does not complete the activity, so armed
boundaries **stay armed**. An SLA timer keeps ticking against a stuck
activity — precisely the case where the SLA matters most — and a
non-interrupting timer can fire *while* the incident is open. An interrupting
boundary firing on an incident-holding node **cancels the node and closes the
incident as overtaken**: the model made the decision the operator would
have. A retry re-enters the node **without re-arming** its boundaries —
failing repeatedly must not reset an SLA clock (the boundary's own lifecycle
began when the node was first entered, and per the activity lifecycle
(§13.3.2) the node never left its active phase).

**Compensation.** The completion ledger (ADR-026 v.1) records **completed**
work only. A node holding an incident never entered the ledger; a
failed-then-retried activity enters it exactly once, on the completion that
eventually succeeds. Failed attempts are the incident's history, not the
ledger's.

**Multi-instance.** One inner instance's failure raises the incident **on
that inner instance alone** — each inner instance runs isolated in its own
per-instance scope (ADR-025 v.2 §2.2), and that scope identifies the incident.
Sibling instances run to their own completion; the MI node cannot complete
while the incident is open, exactly as §2.2's instance-level rule, one level
down. **Retry re-runs only the failed instance**: the respawned track enters
the inner activity inside the same per-instance scope, so it sees exactly the
iteration identity and data slice the failed attempt saw, and the node's
completion accounting — already waiting on that instance's open slot — is
untouched. Completed inner instances are **never re-run**: they are completed
work in the ledger, and their effects exist; a model that wants all-or-nothing
semantics across the set says so through an error boundary plus compensation,
which remains the modeled, business-level path. One asymmetry is worth
stating: in a **sequential** MI, instance *k*'s incident blocks *k+1…N* by
definition, so the remainder of the sequence waits behind the retry. There is
no set-wide retry and no set-wide fault.

### 2.5 Persistence — the incident rides the checkpoint

Incidents extend the ADR-033 v.2 document additively:

- **Raising an incident is a persist point.** The transition into
  `TrackIncident` gates a checkpoint, like the wait transitions — an
  incident that vanished with the process would be no incident at all.
- **The incident record is an instance-level entry in the document** —
  terminal tracks stay unpersisted, as today; what persists is the incident
  itself, carrying everything a respawn needs (node, scope path, lineage,
  cause chain, attempt count, retry deadline when a policy retry is
  scheduled, first/last failure times). Structurally it is a wait
  descriptor's sibling: recovery re-arms a scheduled retry by re-deriving
  its deadline, and an operator-waiting incident simply persists.
- **The instance's persisted status vocabulary** gains the *active with
  incidents* condition, so an operator's first query — "what needs me?" —
  is answerable from the store without loading instances.
- **The snapshot's retention caveat.** The failure-time snapshot (§2.1)
  makes the incident self-contained for diagnosis, at a price stated
  openly: data captured into an incident — including a dead-lettered one —
  outlives the instance's normal data lifecycle. Selecting or excluding
  values (sensitive data, size) is the data-marker mechanism deferred in
  §5; until it exists, the snapshot is the full visible scope.
- **Dead-letter recording.** Giving up on an incident (§2.6) is itself
  durable: the incident closes as *dead-lettered*, its record retained with
  its full history. Draining, replaying or discarding dead letters is an
  operator workflow that belongs to the server, not the library; the library's
  obligation is that the record exists and survives.

### 2.6 Resolution — the library's surface

The engine owns the *primitives*; any operator UX composes them:

| Operation | Meaning |
|---|---|
| **Query** | Open incidents by instance, node, cause class, age; the *active with incidents* predicate at the store level |
| **Retry now** | Spawn the re-entering track immediately (§2.3), regardless of the policy's remaining attempts |
| **Resolve** | Close the incident as handled-outside-the-engine: a fresh track spawns from the node's **outgoing flows** — as if the node completed with the scope's current data, without re-executing it — the operator asserts the work's effect exists |
| **Give up** | Close as dead-lettered (§2.5); the track is cancelled and the instance may then be terminated or compensated by the operator's next act |

**Skip / move-token is deliberately absent** — relocating a token is an
intervention primitive with its own semantics (where may it land, what
re-arms), a separate future decision; conflating it with incident resolution
would smuggle that contract in here. Until it exists, *resolve* covers the
honest majority: the operator fixed the world and the process may proceed.

### 2.7 Observability

The reserved `Incident` phase in the ADR-013 v.2 taxonomy activates:
raised, retry-scheduled, retried, resolved, dead-lettered — each a fact on
the stream, carrying the incident's identity and cause class. The stream
remains announcement, not record (§2.1): a lost fact loses a notification,
never the incident.

## 3. Grounding

| Claim | Source |
|---|---|
| The spec mandates no reaction to an unresolved Error; incidents/retries are absent from the standard | the vendored extract, `semantics/event-handling.md` ("unresolved trigger": *"the spec does not mandate a specific engine reaction"*); no incident/retry/technical-failure clause anywhere in the extract |
| An interrupting Error suspends execution at the throw location; the activity lifecycle has a `Failing` state distinct from completion | `state-machines/activity-lifecycle.md` (BPMN §13.3.2); `semantics/event-handling.md` ("Errors are critical") |
| Error boundary / scope-chain catch is the *modeled* path and stays untouched | `semantics/event-handling.md` (first match consumes the trigger) |
| The Incident construct was explicitly deferred to this ADR; "incident-like" meant `Failed` + diagnostic until now | ADR-021 v.1 §2.8, §7 |
| Job retry below the loop: re-enqueue with backoff, no sleeping goroutine, only the terminal outcome reaches the loop | ADR-021 v.1 §2.4, §2.7 |
| Business error vs technical fault classification, evaluated off the loop | ADR-021 v.1 §2.6 |
| One failure ⇒ one handling point ⇒ at most one log record; goroutine tops are the fault boundary | ADR-022 v.2 §2.1, §2.3 |
| Checkpoint transitions gate persistence; a step is atomic — recovery re-enters the node; effects at-least-once, state exactly-once | ADR-033 v.2 §2.1–§2.3 |
| The fact stream is best-effort, lossy, read-only by design — never load-bearing state | ADR-013 v.2 |
| `Incident` is a reserved, unused phase in the fact taxonomy | ADR-013 v.2 (JobState catalog and reserved slots) |
| The completion ledger records completed work only | ADR-026 v.1 §2.1 (the ledger), §2.7 (its lifecycle) |
| Camunda alignment: incident on retries-exhausted and on uncaught error, instance stays alive, operator retries/resolves | Camunda 7 incident semantics (the project's declared alignment target) |

## 4. Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. **Status quo** — instance-fatal failures, supervision external (host watches facts, restarts work) | no engine change; maximal simplicity | the failure is not durable, so external supervision has nothing to read after a crash; one transient fault destroys unrelated correct work; unusable as a Camunda-replacement posture | ❌ rejected |
| B. **Track-level automatic retry inside the engine** — the track re-runs a failed node N times before anything surfaces | transparent to models; no new state | hidden retries mask deterministic bugs as slowness; re-execution without a durable record violates the at-least-once discipline invisibly; contradicts the no-sleeping-goroutine rule or blocks the loop | ❌ rejected — retry becomes visible incident state instead (§2.3) |
| C. **Incidents as facts only** — emit rich failure facts, keep state terminal | tiny change; taxonomy already reserved | the stream is lossy by design; a "record" that can be dropped is not a record; nothing survives restart; no retry target exists | ❌ rejected — an incident is state (§2.1) |
| D. **Full Camunda parity** — incidents also for business errors, condition-evaluation misses, message-correlation misses | one uniform failure surface | erases the modeled/technical line ADR-021 §2.6 established: a caught business error is a *normal outcome*, and turning model semantics into operator tickets inverts BPMN's own design | ❌ rejected — modeled paths stay modeled; only the *uncaught* end becomes an incident |
| E. **Incident as a live, parked track state** — the failing track survives, halted at the node like a wait, and retry resumes it | no new instance-level record; token projection unchanged | invents a third liveness class beside waiting/dehydrated — a "live" track with no goroutine *and no wait descriptor* — which every piece of live-track bookkeeping must then special-case; resumption of a failed execution has no existing machinery, where respawn has two (boundary spawn, rehydration) | ❌ rejected — the dehydration pattern already solves this shape |
| F. **Incident as a terminal track + a durable instance-level incident record**, continuation by spawning a fresh track from the record (retry: at the node; resolve: from its outgoing flows), two-layer retry, library-owned resolution primitives (chosen) | reuses the spawn/rehydration machinery; attempt history is ordinary track lineage; preserves work and the token (projected from the open incident); queryable from the store | a new terminal state, a checkpoint extension and a status-vocabulary change; the token projection must learn one new source | ✅ chosen |

## 5. Deferrals

- **Token relocation ("skip" / administrative token moves)** — its own
  intervention-primitives decision; §2.6 states the boundary. Two things are
  recorded for that future ADR now: §2.2's respawn mechanism is its intended
  substrate ("set a token at node X" is the same primitive as incident retry
  — spawn a track at X with recorded lineage), and a **scope-local
  restriction** — a token may be moved only within its current scope — is the
  starting hypothesis for its legality rule, since it sidesteps relocation
  into a scope whose initialization never ran. What remains genuinely open
  there: join expectations for a moved-away token, what re-arms at the
  target, and the authorization/audit split with the server.
- **Dead-letter drain/replay UX** — the server's operator surface; the
  library records (§2.5), the server operates.
- **Incident history analytics** (mean-time-to-resolve, failure clustering) —
  the history/audit store's concern once it exists.
- **Data markers / tags** — a general annotation mechanism on data
  declarations, of which postmortem selection (§2.5) is only one use:
  retention in incident snapshots, visibility in operator surfaces,
  operator editability, sensitivity/PII exclusion, and whatever further
  extensions attach to declared data rather than to code. A modeling-surface
  decision for its own ADR; tracked in the project tracker.
- **Bulk resolution semantics** — operator-surface composition; the library
  primitives are per-incident.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-08-04 | Ruslan Gabitov | Initial decision: incident as terminal track + durable instance-level record, continuation by respawn (the dehydration pattern); two-layer retry; boundary/compensation/MI contracts; checkpoint extension; resolution primitives |
