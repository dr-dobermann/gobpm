# ADR-007 — In-Memory Long Waits: dehydration & wake-on-trigger

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.2.1 |
| Date | 2026-07-29 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-001 v.6 Execution Model](ADR-001-execution-model.md) §4.7 (the long-wait invariant this realizes) |
| Related | [ADR-006 v.4 Events & Subscriptions](ADR-006-events-and-subscriptions.md) (trigger delivery), [ADR-033 v.1 Persistence & State](ADR-033-persistence-and-state.md) §2.4/§2.5 (the durable projection of this in-memory model), [ADR-013 v.2 Observability](ADR-013-instance-observability.md) (the residency facts), [ADR-020 v.1 Human Interaction](ADR-020-human-interaction-execution-model.md) / [ADR-021 v.1 Service Task](ADR-021-service-task-execution-model.md) (two of the wait-holders) |

This ADR decides **how a long wait releases its goroutines and how a trigger
resumes it, in memory** — the mechanism ADR-001 §4.7 requires and the seed v.1
promised to author when the long-wait workstream landed. The **durable**
projection (the same model surviving a restart) is ADR-033's; the two are one
model at two residency levels (§2.6). The accompanying SRD lands the mechanics.

## 1. Context & problem

- **ADR-001 §4.7's invariant**: a long wait (a UserTask waiting three days, a
  Timer, a ReceiveTask) **must not hold a goroutine**. The runtime today
  violates it: a track reaching a wait node moves to `TrackWaitForEvent` but its
  goroutine **stays alive, blocked** on its event channel for the whole wait,
  and the instance's event loop stays resident alongside it. A single instance
  waiting on one timer therefore pins **three goroutines** — the instance loop,
  the parked track, and (for a timer) a per-waiter timer goroutine holding the
  deadline. Ten thousand instances each waiting a week pin thirty thousand
  goroutines doing nothing but occupying stacks.
- **A wait is not execution.** BPMN's execution semantics say a catching
  Intermediate Event / Receive Task / boundary event **waits** for its trigger
  and, when the trigger arrives, **fires** — binding any payload and activating
  its outgoing flow. The standard is **silent on the execution medium**: whether
  a wait is a blocked thread, a subscription, or a persisted record is an engine
  choice. gobpm's choice (ADR-001): goroutines are the execution medium,
  **state is the record** — so a wait, which is *not executing*, has no claim on
  a goroutine.
- **The continuation state is already fully externalizable** (ADR-001 §4.7,
  preserved in §3): a track's future is described by its **position (node),
  track/step state, Scope data, and lineage** — there is no hidden state on a
  token object, and a node's resumable state (a timer's deadline, a
  subscription's key) rides a **per-node state contract**, never the shared
  immutable node definition. SRD-070 made this concrete for restart recovery:
  the checkpoint records exactly this, and `Restore` rebuilds a waiting instance
  from it. Dehydration is the same rebuild, triggered by idleness rather than by
  a crash.

## 2. Decision

### 2.1 The dehydrated track state — the goroutine exits

A new **terminal track state, `TrackDehydrated`**, marks a track whose long wait
has been externalized and whose **goroutine has ended**. When an instance
dehydrates, each of its parked long-wait tracks flips from `TrackWaitForEvent`
to `TrackDehydrated`, and the track's run routine **returns** instead of
continuing to block. `TrackDehydrated` is *terminal for the goroutine* but
**not terminal for the flow**: the track has not completed its node, it has
released its thread — the wait outlives the goroutine as bookkeeping (the loop's
wait registry) and, durably, as the checkpoint record.

Reusing the state machine (rather than a bespoke channel protocol) keeps the
release on the same rails as every other track transition: a state change the
loop already serializes, observable as a fact, checkpointable by the existing
SRD-070 capture.

### 2.2 Two residency levels

An instance has two residency levels, and dehydration is the transition between
them:

- **Resident** — the instance loop and its track goroutines are live; the
  instance holds its own waits (today's behavior). An instance stays resident
  while it has *any* running work.
- **Dehydrated** — every track is on a long wait, the instance is **fully
  idle**, and it has released **all** its goroutines: the tracks (`§2.1`) and
  the instance loop itself. The instance costs memory (its checkpoint) and an
  entry in the engine's wait registry, but **zero goroutines**.

An instance dehydrates when it reaches the **fully-idle condition**: all live
tracks are parked on long waits and no short-lived internal synchronization is
outstanding (no join barrier mid-merge, no in-flight step). Partial dehydration
— releasing some track goroutines while the loop stays resident — is a valid
intermediate the mechanism permits (it already helps the many-waits-one-instance
case, e.g. a wide parallel multi-instance), but the headline win is the
fully-idle instance releasing its loop.

### 2.3 The wake is a continuation fork

When a trigger arrives for a dehydrated wait, the instance does **not** resume
the vanished track. It **hydrates** (rebuilds the released goroutines from the
record — the SRD-070 `Restore` path, now driven by a trigger instead of engine
start) and continues the woken wait as a **fresh track, lineage-parented to the
dehydrated track** (its `prev` chain = the dehydrated track's id and ancestry).
This is a continuation *fork*: it rides the fork/lineage machinery the runtime
already has, and the token/history projection stays coherent — the dehydrated
track shows the wait node as its final visit, the child shows what the trigger
led to, the two linked by lineage.

**The fresh track re-enters the wait node with the trigger present.** It does
not start one node *past* the wait: a wait node does **fire-time work** that
lives on the node — a Message catch **binds the arriving payload** into process
data and derives correlation keys from it; a catch/receive may carry output
mapping. Re-entering the node with the trigger in hand runs that fire path
exactly once and moves to the outgoing flow. This yields the ADR's sharpest
invariant, which also refines restart recovery:

> **Trigger-present continues; trigger-absent re-arms.** A wake-on-trigger
> re-enters the wait node **with** its trigger and fires through it. A cold
> restart (SRD-070) re-enters the wait node **without** a trigger and re-arms
> the wait. The single discriminator is *whether a pending trigger accompanies
> the hydration* — the same node, the same entry, two outcomes.

**A boundary's trigger is the exception that proves the rule.** It does not
belong to the node the token is parked on, so it must NOT travel as that
node's pending trigger: re-entering the guarded node with a boundary's trigger
fires the wrong element. A boundary wake is therefore **trigger-absent** — the
instance rebuilds, its waits re-arm, and the boundary re-arms at its recorded
deadline. That deadline is already behind us (the boundary fired while the
instance was away), so the arm does not wait again: the token **forks at the
boundary event with the guarded track as its parent** — interrupting cancels
that parent, non-interrupting leaves it running — which is what a boundary
fire has always meant. The continuation fork and the boundary fork are the
same machinery pointed at different nodes.

### 2.4 Which waits release, and who holds them

**Eligibility is a capability the element declares, not a kind the runtime
enumerates.** A wait node opts into dehydration by implementing a
**`Dehydratable`** capability the loop consults at park time; a node that does
not implement it is non-dehydratable and keeps the instance resident. This
follows the runtime's established idiom (optional capability interfaces —
`DeadlineHinter`, the wait-node capabilities) and earns three things a hardcoded
per-kind switch cannot:

- **Extensibility** — a custom event/task opts into dehydration without the
  runtime knowing it exists.
- **Per-element, data-driven policy** — the decision may consult the element's
  *own evaluated state*, so it is not a static per-kind constant. A **Timer**
  returns false for a sub-threshold deadline (a twenty-minute wait is not worth
  a checkpoint + hydrate round-trip) and true for a long one; an **external
  worker ServiceTask** returns false unconditionally (a job in flight is
  *active work*, not a passive wait — ADR-021).
- **Safe, incremental rollout** — the default (no capability) is "stay
  resident," so elements gain dehydration one at a time, in lockstep with their
  holders.

Eligibility (the element is *willing* to release) is distinct from and composes
with the **holder** (something can *wake* it): an instance dehydrates a wait
only when the node is `Dehydratable` **and** every armed wait's kind has a
holder. An element that declares itself dehydratable for a kind with no holder
is a mistake the runtime guards — it stays resident and logs, never a lost
trigger. A single-definition catch/receive node answers from its own definition;
a task node answers directly.

**A guarded activity has more waits than its token can see.** An interrupting
boundary event is a wait — "approve within 24 hours **or escalate**" is two
things to wake for, not one — but it hangs off the activity rather than
occupying it, so nothing about the *token's* wait describes it. Releasing an
instance whose task was held while its boundary was not would drop the
escalation silently: the deadline passes, nothing fires, and the record stays
in flight with no way back. A missed business deadline that produces no error
is the worst failure this decision can cause.

So an armed boundary is a **held wait in its own right**, and eligibility is
per-wait, not per-token: an instance releases only when every wait guarding a
track — the one it is parked on *and* every boundary over it — has a holder.
This is the same per-arm rule the Event-Based Gateway already implies, applied
at the granularity a boundary needs. Boundary kinds that resolve directly
rather than waiting (Error, Escalation, Compensation, Cancel — matched at the
throw or failure site, never subscribed) arm nothing and so cost no residency;
a Conditional boundary is loop-owned and therefore never holdable, keeping its
instance resident exactly as a Conditional catch does.

A boundary's **deadline is durable state**, not a value to recompute. Re-arming
reconstructs everything about a boundary from the model except *when* it fires:
re-evaluating "24 hours from now" at restore yields 24 hours from the
**restore**, so a duration-based escalation walks forward on every recovery and
an instance recovered often enough never escalates. The resolved deadline is
therefore recorded and restored — the same rule the runtime already applies to
a track's own timer, which the boundary had escaped.

**The Event-Based Gateway is the wait node, not its arms.** An EBG arms several
catch-events on one track and races them, first-fires-wins, losers
**withdrawn**. The `Dehydratable` decision is a property of the *wait node*, and
for an EBG the wait node is the **gateway** — so the EBG returns
`Dehydratable == true` **unconditionally** and **ignores its arm events' own
policies**: an EBG exists only to wait on a race, so it is always a pure wait
point worth releasing (a plain intermediate timer catch might not be, an EBG
always is). A dehydrated track therefore holds a **set** of arm-holders (one per
armed wait — the singleton case for a plain catch); on the winning arm's
trigger the fork fires that arm and **withdraws the sibling arm-holders**
(releasing their deadlines / subscriptions), exactly the `Withdrawn` the
resident EBG performs today. The holder-existence guard still applies per arm —
an EBG releases only once every arm-kind it carries has a holder (an all-timer
EBG the moment the timer service lands; one with a message arm once the message
holder does).

**Who holds a released wait.** A resident instance holds its own waits — its
live loop is the subscriber the trigger delivers into. A **dehydrated instance
cannot**: its loop is gone, and a trigger delivered at a released instance is
lost. The decision:

> **A releasable wait registers its holder — not the track and not the
> instance — as the `EventProcessor`, tagged with the instance id, the track
> id, and the wait descriptor.** The holder is the *permanent* subscriber; the
> trigger source (the EventHub, or the engine timer service) points at it from
> the moment the wait is armed, so a trigger never reaches a released instance.

On a trigger the holder forks on the instance's **residency**:

- **Resident** (the instance still has its loop, e.g. it dehydrated only some of
  its tracks, or hasn't dehydrated yet) → the holder **resumes the live parked
  track** — today's delivery, reached through the holder.
- **Dehydrated** → the holder **wakes** the instance: `Hydrate` (rebuild from
  the checkpoint) and then, rather than re-arming the woken wait and
  re-delivering, spawn the continuation fork that **prepares the node's input
  from the trigger and executes the wait node's fire path directly** (§2.3) —
  the fork does **not** re-register as an `EventProcessor`, because the trigger
  is already in hand. This closes the delivery-at-a-released-loop gap at the
  root: nothing is ever pushed at a vanished instance; the holder is the one
  that was subscribed.

The holder pattern is **one decision with a per-kind realization**, because each
trigger source is a different subsystem:

- **Timer** → an **engine-level timer service** is the holder. The per-waiter
  timer goroutine (one per waiting instance today) is replaced by a single
  service holding deadlines keyed to instance ids; at the earliest deadline it
  fires. This is the durable timer service ADR-033 §2.5 needs and issue #84
  tracks — the deadline is the wait descriptor SRD-070 already records.
- **Message / Signal** → the **EventHub subscription targets the holder**, keyed
  to the instance id and carrying the checkpoint's conversation keys. The
  subscription survives the instance's release; **correlation
  ([ADR-016 v.1](ADR-016-message-correlation.md)) moves onto the holder** — it
  gates the conversation-key match itself, so a mismatched-key message never
  wakes the instance.
- **Human task** → the **task distributor's completion path is the holder**. A
  parked human task already lives in the distributor's inbox (ADR-020),
  independent of the instance's residency; completion wakes the instance and the
  completion outputs are the fork's node input, where today it pushes a request
  into a live channel.
- **External worker job** → **not dehydratable** (its node's `Dehydratable`
  returns false). A worker job is *active work in flight* (ADR-021's
  fetch-and-lock queue owns it), not a passive wait — the instance stays
  resident until the job reports. The queue's own durability is ADR-021's
  concern.

An instance dehydrates only when **every** live track is parked on a
`Dehydratable` wait whose kind has a holder; anything else — a non-dehydratable
element, a wait kind whose holder has not landed yet, a worker job in flight —
keeps the instance resident. Eligibility and holders both roll out
element-by-element and kind-by-kind, and until both are present that wait simply
stays resident — never a lost trigger.

### 2.5 Effects and idempotency on wake

Waking re-enters the wait node and re-runs its fire path — an **at-least-once**
posture consistent with ADR-033 §2.3. The fire is idempotent by construction: a
timer fires its outgoing once (an overdue deadline collapses to a single firing,
ADR-033 §2.5); a message binds its payload and moves on; correlation dedups a
redelivered message by key (ADR-016). No wake replays a completed step — the
dehydrated track never completed its node, so the wake *is* the node's first and
only fire.

### 2.6 One model, two residency levels

This ADR is the **in-memory** statement; ADR-033 §2.4/§2.5 is its **durable**
projection — one model, two residency levels:

- In memory, dehydration releases goroutines and the wait-holder keeps the wait;
  hydration rebuilds the goroutines from the **live** record.
- Durably, the same record **is** the checkpoint (SRD-070): it survives a
  restart, and restart recovery is hydration whose trigger is "the engine
  started." A crashed engine's dehydrated instances and its resident instances
  recover through the *same* `Restore` path — dehydration did not make them a
  special case, it made every waiting instance already shaped for recovery.

Dehydration therefore adds **no new persistence model**: it reuses SRD-070's
checkpoint as the hydration source and adds only the goroutine-release trigger
(idleness), the wake trigger (an event via a holder), and the continuation-fork
re-entry.

### 2.7 Observability

Residency is an operator-relevant lifecycle property — "how many of my ten
thousand instances are dehydrated" is a real question — so the two residency
transitions are **observable at lifecycle volume** (ADR-013 v.2): a
**`Dehydrated`** fact when an instance releases its goroutines (details: the
parked wait kinds and count) and a **`Hydrated`** fact when a holder wakes it
(details: the waking trigger kind, whether it continued or completed). One fact
per transition — not per checkpoint (the checkpoint accompanies an
already-observable transition) and not per still-armed wait. The residency
transitions ride the existing observable-event stream and the operator-log echo,
masking-clean (names and counts, never payload). The `TrackDehydrated` state
change is visible through the existing track-state/token projection, so a
dehydrated instance's tokens still project their wait positions.

## 3. Runtime invariants this preserves (ADR-001 §4.7)

- A track's continuation is **fully described by position, track/step state,
  Scope data and lineage** — no hidden token state. Dehydration relies on this:
  the child fork rebuilds from exactly these, and the dehydrated track adds only
  a state (`TrackDehydrated`) and its wait descriptor.
- Resumable per-node state (timer deadline, subscription key) rides a **per-node
  state contract**, **never** the shared immutable node definition — so many
  instances dehydrated at the same node share one definition and hold their
  distinct waits in their own records/holders.
- The **loop is the single writer** of instance state (ADR-017): dehydration and
  the flip to `TrackDehydrated` happen on the loop before it exits, so the
  release is a consistent cut, and the wake re-enters through the loop it
  rebuilds.

## 4. Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. **Block the goroutine for the wait** (status quo) | trivial; the track object stays live and resumes in place | violates ADR-001 §4.7; O(waiting) goroutines pinned indefinitely — the very cost this workstream exists to remove | ❌ rejected — the problem statement |
| B. **Release the goroutine but resume the SAME track** (rehydrate the blocked object) | no lineage fork; the token stays one continuous track | requires reconstructing the exact goroutine/stack state, or a bespoke "suspended track" object with its own resume protocol outside the state machine; fights the immutable-continuation model | ❌ rejected — §2.1/§2.3: a fresh lineage-child rebuilds from the externalized state with no bespoke resume |
| C. **Fresh track starting one node PAST the wait** | simplest continuation — no wait-node re-entry | skips the wait node's fire-time work — a Message catch would lose its payload binding and correlation-key derivation; correct only for triggers with no fire-work (timers) | ❌ rejected — §2.3: re-enter the node WITH the trigger so fire-work runs; identical to "next node" for timers, correct for messages |
| D. **`TrackDehydrated` state + continuation-fork re-entering the wait node with the trigger** (chosen) | reuses the state machine, the fork/lineage machinery, and the SRD-070 checkpoint; one model with restart recovery; incremental per-kind rollout | needs an engine-level wait-holder per kind (§2.4) — real but independently landable | ✅ chosen |
| E. **A durable subscription log / event-sourced wakeups** | perfect wake audit | a second persistence model beside the checkpoint; ADR-033 §4 already rejected event sourcing for state | ❌ rejected — §2.6: the checkpoint is the single record; holders are in-memory indices rebuilt from it |

## 5. Deferrals & rollout

- **Per-kind holders roll out independently** (§2.4). Timer (closing #84) is the
  cleanest first holder — no external actor, a deterministic deadline already in
  the record. Message/signal and human-task holders follow; until a kind has a
  holder it stays resident, never losing a trigger.
- **Suspend/resume** (ADR-033 §2.6) is dehydration driven by an *operator*
  rather than by idleness, over the same machinery — a following slice.
- **Multi-node wake** (a trigger arriving at engine B for an instance dehydrated
  under engine A's lease) rides ADR-033 §2.8's ownership fencing and ADR-008's
  distribution — out of scope here; a single-engine dehydrated instance is woken
  by its own holder.

The accompanying **SRD** lands the mechanism (`TrackDehydrated`, the idle
detector, the continuation-fork wake) and the wait-holders, on top of SRD-070's
checkpoint.

## 6. References

- [ADR-001 v.6 Execution Model](ADR-001-execution-model.md) §4.7 — the invariant this realizes.
- [ADR-006 v.4 Events & Subscriptions](ADR-006-events-and-subscriptions.md) — trigger delivery.
- [ADR-033 v.1 Persistence & State](ADR-033-persistence-and-state.md) §2.4/§2.5 — the durable projection.
- [ADR-016 v.1 Message Correlation](ADR-016-message-correlation.md), [ADR-020 v.1](ADR-020-human-interaction-execution-model.md), [ADR-021 v.1](ADR-021-service-task-execution-model.md) — correlation and the wait-holders.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.2 | 2026-07-27 | Ruslan Gabitov | Authored in full as the long-wait workstream lands (the v.1 seed's promise). Decides the in-memory dehydration/wake mechanism: the terminal `TrackDehydrated` state (the track's goroutine exits), two residency levels (resident / fully-idle-dehydrated releasing the loop too), the wake as a **continuation fork** (a fresh track lineage-parented to the dehydrated one, **re-entering the wait node with the trigger present** so fire-time work — message payload binding, output mapping — runs once), and the sharpened invariant **trigger-present continues / trigger-absent re-arms** (which also refines SRD-070 restart recovery). §2.4 makes eligibility a **`Dehydratable` capability the element declares** (data-driven — a Timer stays resident below a deadline threshold, a worker ServiceTask never dehydrates; an **Event-Based Gateway is the wait node and returns true unconditionally, ignoring its arms**, holding a set of arm-holders and withdrawing the losers on wake), composing with the holder (release iff eligible AND every armed kind is held); and **the holder — not the track or instance — is the registered `EventProcessor`** (tagged with instance/track id): the permanent subscriber forks on residency (resident → resume the live track; dehydrated → hydrate + **direct-fire** the wait node from the trigger, no re-register), closing the delivery-at-a-released-loop gap at the root and moving Message correlation onto the holder. The **wait-holder** pattern per kind (engine timer service — #84; hub subscription targeting the holder, id-keyed; distributor completion), rolled out incrementally; worker jobs stay resident (active work, not a wait). One model, two residency levels with ADR-033: no new persistence model — the SRD-070 checkpoint is the hydration source. §2.7 makes residency observable (the `Dehydrated`/`Hydrated` facts at lifecycle volume). Alternatives rejected: block-the-goroutine, resume-the-same-track, start-past-the-wait, event-sourced wakeups. |
| v.2.1 | 2026-07-29 | Ruslan Gabitov | **Boundary events are held waits** — the concept the implementation established, recorded at the layer it belongs to. §2.4: a guarded activity carries more waits than its token can see, so eligibility is per-WAIT, not per-token — an instance releases only when the wait its token is parked on *and* every boundary guarding that track are held. Releasing with only the token's wait held drops the escalation of "approve within 24h or escalate" silently, which is the worst failure this decision can produce. Boundary kinds resolved at the throw/failure site (Error, Escalation, Compensation, Cancel) arm nothing and cost no residency; a Conditional boundary is loop-owned and never holdable. A boundary's resolved **deadline is durable state**: re-evaluating a duration at restore restarts the clock, so an instance recovered often enough never escalates — the rule already applied to a track's own timer, which the boundary had escaped. §2.3: a boundary's trigger does not belong to the parked node, so its wake is **trigger-absent** and the fire is a fork AT the boundary with the guarded track as its parent — the continuation fork and the boundary fork are one machinery pointed at different nodes. Status stays Draft until the branch merges (v.2 was marked Accepted on an unmerged branch; corrected). |
| v.1 | 2026-06-07 | Ruslan Gabitov | Initial Draft seed — in-memory long-wait release model relocated from ADR-001 v.3 §4.7. Durable version remains with the Persistence & State ADR. Not yet implemented. |
