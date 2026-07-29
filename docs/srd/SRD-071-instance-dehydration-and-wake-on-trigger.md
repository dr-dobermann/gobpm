# SRD-071 — Instance dehydration & wake-on-trigger

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.2.3 |
| Date | 2026-07-29 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-007 v.2](../design/ADR-007-in-memory-long-waits.md) (the whole mechanism) + [ADR-033 v.2](../design/ADR-033-persistence-and-state.md) §2.4/§2.5 (the durable projection, the wait-holders) |
| Upstream | [ADR-001 v.6](../design/ADR-001-execution-model.md) §4.7, [ADR-006 v.4](../design/ADR-006-events-and-subscriptions.md), [ADR-013 v.2](../design/ADR-013-instance-observability.md), [ADR-016 v.1](../design/ADR-016-message-correlation.md), [ADR-020 v.1](../design/ADR-020-human-interaction-execution-model.md), [ADR-021 v.1](../design/ADR-021-service-task-execution-model.md) |
| Refines | [SRD-070 v.1](SRD-070-instance-checkpoint-and-restart-recovery.md) (the checkpoint/Restore this reuses as the hydration source) — sideways |

Lands ADR-007's in-memory dehydration/wake mechanism on SRD-070's
checkpoint: the `TrackDehydrated` state, the **`Dehydratable`
capability** the wait node declares, the fully-idle detector that
releases an instance's goroutines, wake-on-trigger via hydration + a
direct-fire continuation fork, **the holder as the registered
`EventProcessor` (a set-per-track, so the Event-Based Gateway fits)**,
and an engine-level holder per kind (timer → closes #84; message/signal;
EBG; human task). Eligibility (`Dehydratable`) and holders both roll out
independently (ADR-007 §2.4), so the tree is shippable after the timer
holder — message/signal, EBG, and human-task stack on top with no broken
intermediate.

## §1 Background (grounded)

- **A parked track holds its goroutine.** `track.run` blocks on
  `select { case <-t.evtCh … }` (`internal/instance/track.go:793-814`)
  for the whole wait; the loop resumes it with `tr.evtCh <- eDef`
  (`loop.go:565`, `dispatchToParked`). The instance loop runs
  `for ls.active > 0` (`loop.go:174`); `ls.active` decrements only on
  the three terminal track events (`evEnded`/`evAwaiting`/`evFailed`,
  `loop.go:371/398/727`). A parked track never emits one, so the loop
  stays resident — **one goroutine per instance plus one per parked
  track**, and for a timer a third (the per-waiter `runTimerService`,
  `waiters/timer.go:311`).
- **The wait target set** (`evtCh`-held long waits, resume = pure
  `evtCh <- …` from the loop): Message/Signal/Timer catch
  (`checkNodeType`, `track.go:445-490`), UserTask (`parkHumanTask`,
  `track.go:664-681`), worker ServiceTask (`parkServiceTask`,
  `track.go:691-706`). **Not** the `parkCh` join barriers
  (`track.go:959-969`) — short internal sync, must stay resident.
- **Subscriptions survive the goroutine's exit.** The hub holds a
  Message subscription in `waiters map[string]EventWaiter` keyed by
  `eDef.ID()` (`eventhub.go:51`) with the registered processor =
  `t.instance` (`track.go:477`); Signal/Timer register the track object
  `t` whose `ProcessEvent` only calls `t.instance.emit` (`track.go:1375`).
  All are reachable while the instance object is — **but `emit` DROPS
  into a closed `loopDone`** (`instance.go:429-434`): delivery to a
  released loop is lost unless it hydrates first.
- **The checkpoint is the hydration source** (SRD-070). Capture already
  records the `TrackWaitForEvent` track + timer descriptor + scope +
  conv-keys (`checkpoint_capture.go:214-242`), and its three
  defer-guards — `len(ls.calls)>0`, `len(ls.miGroups)>0`,
  `len(ls.sweeps)>0` (`checkpoint_capture.go:126-131`) — are exactly the
  "not fully idle" conditions dehydration must also refuse. `Restore`
  (`restore.go:41`) rebuilds a waiting instance re-arming its waits
  (trigger-absent) and keeps the recorded track id (`restore.go:217`).
- **The engine's instance table**: `instances map[string]instanceReg`
  (`thresher.go:159`), `instanceReg{stop, inst, handle}`
  (`thresher.go:123`), set by `trackInstanceLocked` (`locked.go:232`).
  Message routing to a running instance is broker-keyed
  (`waiters/message.go:199-221`) → `Instance.ProcessEvent`
  (`correlation.go:97`). Human completion routes
  `Thresher.Complete → instanceForTask → t.instances[id] → inst.Complete`
  (`thresher/tasks.go:60-114`) → `taskRoundtrip` which **requires the
  live loop** (`tasks.go:114-125`, guarded by `<-inst.loopDone`). The
  timer holder replaces `runTimerService`; the engine timer service
  reuses the recorded `TimerDescriptor`.
- **State enum** `Created/Active/Completed/Terminating/Terminated`
  (`lifecycle.go:20`) — no idle/dehydrated state yet; `TrackDehydrated`
  and an instance `Dehydrated` state are new.

## §2 Requirements

### §2.1 Functional — the mechanism (shared)

- **FR-1 — `TrackDehydrated`.** A new track state (goroutine-terminal,
  flow-non-terminal). `track.run` returns when a track is flipped to it
  (a `stopIt`-style signal the loop raises before/at the flip); the
  return emits a new **`evDehydrated`** track event that decrements
  `ls.active` like the other terminal events but marks the track
  released-not-ended (retained as a live record, `liveTrackStates` gains
  `TrackDehydrated`). No hub unregistration — the subscription stays on
  the holder (FR-3).
- **FR-1a — the `Dehydratable` capability.** A new optional wait-node
  capability `Dehydratable(ctx, re) bool` (the runtime idiom — an
  opt-in interface like `DeadlineHinter`). The loop consults the parked
  track's node at park time; a node not implementing it is
  non-dehydratable (resident). Implementations: a **Timer** catch node
  returns true iff its stashed deadline exceeds an engine threshold
  (`WithDehydrationThreshold`, a sensible default — a sub-threshold wait
  isn't worth checkpoint+hydrate); a **UserTask** returns true; a
  **worker ServiceTask** returns false (active work); an
  **Event-Based Gateway** returns **true unconditionally, ignoring its
  arm events' policies** (the EBG is the wait node, always a pure wait
  race — FR-3a). A single-definition catch answers from its definition.
- **FR-2 — the fully-idle detector + loop release.** After the loop
  applies a checkpoint transition, if **every** live track is parked on
  a `Dehydratable` wait whose kind(s) are held (FR-6/7/8) AND the
  SRD-070 capture guards are clear (`calls`/`miGroups`/`sweeps` empty)
  AND the instance is checkpoint-armed (`cpOwner != ""`), the loop
  **dehydrates**: flip each such track to `TrackDehydrated`, take the
  SRD-070 consistent-cut checkpoint, set the instance state to the new
  **`Dehydrated`**, and **exit the loop** (close `loopDone`) — distinct
  from Completed/Terminated (a new loop-exit reason, guarded so
  `settleFinalState` is NOT called). A non-`Dehydratable` node, or a
  `Dehydratable` wait whose kind has no holder yet, keeps the instance
  resident (never dehydrates a wait it can't wake — the runtime guard,
  logged).
- **FR-3 — the holder is the `EventProcessor`; a track holds a holder
  set.** Each armed wait registers its **holder** with the trigger
  source (the hub, or the engine timer service), tagged with
  `(instance id, track id, wait descriptor)` — **not** the track and
  **not** the instance object. A dehydrated track holds a **set** of
  arm-holders (one per armed wait — the singleton for a plain catch, the
  multi-arm set for an EBG, FR-3a). The holder is the permanent
  subscriber, so a trigger never reaches a released instance's `emit`
  (closing the `loopDone`-drop at the root, `instance.go:429-434`). On a
  trigger the holder forks on residency: **resident + parked** → resume
  the live parked track (today's `evtCh` path, reached through the
  holder); **dehydrated** → wake (FR-4). Correlation for a Message
  holder moves onto the holder — it carries the checkpoint's
  conversation keys and gates `validateAndAssociate` itself, so a
  mismatched-key message never wakes the instance.
- **FR-3a — the Event-Based Gateway.** An EBG track arms several
  catch-events and races them (first-fires-wins, losers `Withdrawn`).
  It is `Dehydratable` unconditionally (FR-1a) and dehydrates as a track
  with a **holder set** (one arm-holder per armed catch), released only
  once **every arm-kind has a holder** (the runtime guard — an all-timer
  EBG at M3, a message-armed EBG at M4). On the winning arm's trigger,
  the wake fires that arm's node (FR-4) and **withdraws the sibling
  arm-holders** (releases their deadlines / subscriptions) — the same
  `Withdrawn` the resident EBG performs. The armed-arm set rides the
  checkpoint (the record already carries `msgDefIDs`; extended to the
  full armed-wait set).
- **FR-3b — a wait's registrations end with the wait, on EVERY exit path;
  a holder's self-removal is not a failure (v.2, corrected v.2.1).** A
  held wait registers in two places: the **holder** with the trigger
  source (the hub for a message/signal — the holder itself is the
  registered `EventProcessor`, `subscription_holder.go:92`, so the
  instance registers nothing of its own — or the engine timer service),
  and, for a human task, a routing entry. Both must end when the wait
  ends, whichever way it ends: fired, released by dehydration (where the
  holder deliberately *stays* — it is the wake source), cancelled by an
  interrupting boundary, or torn down with the instance. As landed at
  v.1 only two paths withdraw (`track.go:1619` on delivery, `loop.go:341`
  on teardown), which leaves two gaps: `evEnded` (`loop.go:414-425`)
  drops a boundary-cancelled track without releasing its holds, so a
  deadline or subscription belonging to a cancelled wait stays live and
  can wake a later cycle — exactly the stale-hold hazard `track.go:1614`
  warns against; and the `taskTracks` registry (`tasks.go:133`) is
  written for every held human task and never read by any path nor ever
  deleted, an unbounded map. FR-3b makes withdrawal total: one routine
  owns it, every exit path calls it, and a registry nothing reads is
  removed rather than maintained.

  The second half is an **ordering** rule at the hub. A holder that fires
  wakes the instance *synchronously* inside its own `ProcessEvent`, and
  the wake's `ReleaseWaits` unregisters that holder — so by the time the
  hub waiter reports its fire, the waiter it is reporting for is already
  gone, and `WaiterFired` (`eventhub.go:666-683`) answers `ObjectNotFound`,
  which the waiter treats as terminal and logs at ERROR. Every successful
  message wake therefore prints a failure. `UnregisterEvent` already
  treats "already gone" as the desired end state for precisely this
  reason (`eventhub.go:401-409`, FIX-003 B: the fired-timer path
  self-removes before the track unregisters); `WaiterFired` needs the
  same idempotency. Nothing leaks here — the waiter is correctly removed;
  it is the *report* that arrives after the removal.
- **FR-4 — wake = hydrate + a direct-fire continuation fork.** When the
  holder receives a trigger for a **dehydrated** instance it: (a)
  `Hydrate`s — rebuilds the instance from its checkpoint (the SRD-070
  `Restore` path, re-registered in `t.instances`, `Run`); (b) spawns a
  **fresh continuation-fork track at the wait node that does NOT
  re-register / re-arm** — instead it **prepares the node's input from
  the trigger** (a Message binds its payload + derives correlation keys;
  a UserTask binds the completion outputs; a Timer has none) and
  **executes the node's fire path directly** → the outgoing flow. The
  still-waiting **sibling** tracks (a partial wake) re-arm normally once
  resident, then re-dehydrate when idle again (FR-5). The continuation
  fork's **persisted `prev` inherits the dehydrated track's genuine fork
  lineage** (parallel-split ancestry) — it does **not** append the
  dehydrated track id — so repeated dehydrate/wake cycles do not grow
  the lineage (§4.1); the parent→child dehydration link lives only in
  the current-residency history projection. Wake is single-flight per
  instance (§4.6): two triggers racing hydrate it once, the second
  delivering into the now-resident loop.
- **FR-5 — re-dehydration.** After a wake processes its trigger, if the
  instance is fully idle again (FR-2), it re-dehydrates — an instance
  oscillates resident↔dehydrated across its waits, each cycle costing
  one checkpoint + one hydrate, never accumulating goroutines or lineage.

### §2.2 Functional — the wait-holders (per-kind, independent)

- **FR-6 — the timer holder (engine timer service; closes #84).** The
  holder (FR-3) for a timer wait is an engine-level service holding
  deadlines keyed to `(instance id, eDef id)` — a min-ordered set
  reusing the checkpoint's `TimerDescriptor` — one service goroutine
  (or a per-nearest-deadline timer) replacing the O(waits) per-waiter
  `runTimerService`. It fires at the earliest deadline and wakes that
  instance (FR-4). Overdue collapses to one firing (SRD-070/ADR-033
  §2.5).
- **FR-7 — the message/signal holder.** The holder for a message/signal
  wait is the retained hub subscription, now targeting the **holder**
  (FR-3), keyed to the instance id + carrying the checkpoint's
  conversation keys. A matching, correlation-gated trigger wakes the
  instance (FR-4) and the continuation fork binds the payload; a
  mismatched key does not wake it. A redelivered message dedups by key
  (ADR-016).
- **FR-8 — the human-task holder.** The holder is the distributor
  path: `Thresher.Take`/`Complete` for a task whose instance is
  dehydrated wakes the instance (FR-4) — the parked task already lives
  in the distributor inbox, independent of residency — then delegates to
  the now-resident `inst.Take`/`Complete`; the completion outputs are the
  fork's node input. `instanceForTask` resolves a dehydrated instance
  (it stays registered in `t.instances` with a dehydrated marker).
- **FR-9 — worker jobs keep the instance resident.** A `parkServiceTask`
  wait (external worker job in flight) is active work, not a passive
  wait (ADR-007 §2.4): its node's `Dehydratable` returns false (FR-1a),
  so the idle detector never releases a job-parked track — the instance
  stays resident until the job reports.

- **FR-9a — armed boundary events are durable state (v.2).** A wait node
  can be guarded by an **interrupting boundary event** — "approve within
  24h or escalate" is the canonical BPMN pattern, and it is precisely the
  long wait this feature exists to serve. At v.1 an armed boundary is
  invisible to dehydration: `dehydratableParked` (`loop.go:1042`) walks
  `inst.tracks` only, and a boundary watch is not a track, so the
  instance releases and the watch's hub waiter dies with it. Probed
  empirically on a 24h interrupting boundary timer: the instance
  dehydrates, the clock passes the deadline by an hour, **nothing
  fires**, and the record sits `Active` with no way back — a silently
  lost business deadline, the worst failure this feature can produce.
  The second half is equally wrong: nothing records a boundary's
  deadline (the checkpoint document carries `Tracks`/`Scopes`/`Ledgers`
  and no boundary at all), so a track respawned by *any* hydration
  re-arms through `armBoundaries` (`loop.go:312`) and **re-evaluates**
  the timer expression — a duration-based boundary silently restarts its
  clock on every cycle. This is the same trap SRD-070 §4.2 already
  closed for a track's own timer ("the recorded absolute deadline
  overrides re-evaluation at restore — a Duration would otherwise
  restart"); the boundary timer needs the identical treatment.

  FR-9a therefore makes an armed boundary a first-class durable wait:
  its arm is **recorded in the checkpoint** (host track, boundary node,
  event definition, absolute deadline), it takes a **holder** like any
  other wait so its deadline can wake a dehydrated instance, restore
  **re-arms it at the recorded deadline** rather than re-evaluating,
  and — the eligibility rule — an instance is released only when every
  armed boundary is held, exactly as FR-3a already requires of an EBG's
  arms. Only hub-armed boundary kinds are in scope: Error, Escalation,
  Compensation and Cancel boundaries are resolved directly rather than
  waited on (`boundary_watch.go:105-110`), so they arm nothing and hold
  nothing. This closes the restart-recovery half of the gap as well —
  a recovered instance's boundary deadlines stop restarting.

### §2.3 Functional — observability

- **FR-10 — residency facts.** `KindInstanceState` gains two open
  phases (ADR-007 §2.7, ADR-013 v.2 taxonomy): **`Dehydrated`** (Info —
  the instance released its goroutines; details: the parked wait kinds +
  count, and — key to the whole feature — that it now holds **zero
  goroutines**) and **`Hydrated`** (Info — a holder woke it; details:
  the **waking trigger kind** (timer/message/signal/task), the woken
  track, and whether the wake **continued** the flow or **completed**
  the instance). One fact per residency transition — never per
  checkpoint (the checkpoint rides an already-observable transition) nor
  per still-armed arm. Echo level Info (a lifecycle milestone, the
  `ProcessLifecycle` analog); both ride the existing observer stream +
  operator-log echo, masking-clean (names/counts, never payload). A
  dehydrated instance's tokens still project their wait positions
  through the retained `TrackDehydrated` records, so `Tokens()`/history
  stay meaningful across the cycle.

### §2.4 Non-functional

- **NFR-1 — no lost trigger, no double-run.** A trigger during the
  dehydration cut (the race window between "decide idle" and "loop
  exited") is not lost: the cut runs on the loop, so a trigger arriving
  as `evDeliver` is applied first (aborting the dehydration that cycle);
  a trigger after the loop exits routes through the holder → `Hydrate`.
  Wake is single-flight per instance (FR-3).
- **NFR-2 — validate-all-params; no `Must*` in library paths; loud
  classified errors.**
- **NFR-3 — the zero-config engine is unchanged**: dehydration arms only
  with `WithRepository` (the checkpoint is the hydration source); an
  un-armed engine keeps every instance resident exactly as today.
- **NFR-4 — coverage**: `make ci` green (incl. `-race` — this is
  concurrency-heavy); diff-coverage ≥95% (aim 100%); touched functions
  ≥80%.

## §3 Models (shapes)

```go
// internal/instance — the new track state + event
TrackDehydrated trackState // goroutine-terminal, flow-live
evDehydrated    trackEventKind

// pkg/model/flow (or the node packages) — the eligibility capability (FR-1a)
type Dehydratable interface {
    // Dehydratable reports whether this wait node releases the instance's
    // goroutines when parked here. Data-driven: a Timer weighs its deadline;
    // an EBG returns true unconditionally; a worker ServiceTask false.
    Dehydratable(ctx context.Context, re renv.RuntimeEnvironment) bool
}

// internal/instance/lifecycle — the new instance state
Dehydrated State // released; not terminal

// internal/instance/restore.go — wake-with-trigger (the direct-fire fork)
type PendingTrigger struct {
    TrackID string               // the dehydrated track being woken
    EDef    flow.EventDefinition // the trigger; nil = trigger-absent (cold restart)
    Data    []data.Data          // prepared node input (message payload / task outputs); nil for timer
}
func Restore(doc, s, parentRoot, er, ep, td,
    pending *PendingTrigger, opts ...Option) (*Instance, error)

// the holder — the EventProcessor for a dehydratable wait (FR-3)
type WaitHolder interface {
    eventproc.EventProcessor        // the trigger source registers THIS
    // ProcessEvent: resident→resume the parked track; dehydrated→wake (FR-4)
}

// pkg/thresher — the dehydrated registry + wake
type dehydratedReg struct { doc *checkpoint.Document; /* holder waits */ }
func (t *Thresher) Hydrate(ctx, id string, tr *instance.PendingTrigger) (*InstanceHandle, error)

// pkg/thresher — the engine timer service (FR-6)
type timerService struct { /* (instanceID,eDefID)->deadline heap; one goroutine */ }

// pkg/observability
PhaseDehydrated Phase = "Dehydrated" // InstanceState
PhaseHydrated   Phase = "Hydrated"

// internal/instance/checkpoint — the armed-boundary record (FR-9a, v.2).
// Schema-2: a Schema-1 document simply carries none, so an instance written
// before the bump restores exactly as it does today.
type BoundaryRecord struct {
    // Timer pins the arm the same way TrackRecord.Timer pins a track's own
    // wait: the recorded ABSOLUTE deadline overrides re-evaluation at
    // restore, so a duration-based boundary cannot restart its clock
    // (SRD-070 §4.2, applied to the boundary).
    Timer      *TimerDescriptor `json:"timer,omitempty"`
    HostTrack  string           `json:"host_track"`  // the guarded track
    BoundaryID string           `json:"boundary_id"` // the boundary node
    // The definition's INDEX within its boundary, not its id (v.2.2): an
    // event-definition id is minted per model build, so two engines
    // constructing the same process hold different ones and a recorded id
    // never resolves on the recovering engine. Node ids are the operator's
    // parity contract (ADR-033 §2.8) and a boundary IS a node; definition
    // ids are not, so keying on them would silently drop the deadline of
    // any model whose author had not hand-pinned them. Definition order
    // within a boundary is model order — identical on both engines.
    DefIndex   int              `json:"def_index"`
}

// internal/instance — total withdrawal (FR-3b, v.2). One routine owns every
// registration a wait made; every exit path calls it.
func (t *track) withdrawWait(reason withdrawReason)
```

Worked trace (T-timer): a process parks on a 2h timer → the wait
transition checkpoints AND (fully idle, timer-held) dehydrates: the
track flips `TrackDehydrated`, the loop exits, the engine timer service
takes the deadline, `Dehydrated` fact emitted — **zero goroutines**.
2h later the service fires → `Hydrate` → `Restore` with the timer
`PendingTrigger` → the continuation fork re-enters the timer node,
fires through, the flow continues → `Hydrated` fact. If the process now
has no more waits it completes; if it hits another wait it re-dehydrates.

## §4 Analysis & decisions

- **§4.1 Bounded lineage across cycles.** A pure fresh-child-per-wake
  fork would grow `prev` by one per dehydrate/wake cycle — unbounded for
  a long-lived poller. Decision: the continuation child's **persisted**
  `prev` inherits the dehydrated track's genuine BPMN fork lineage (real
  parallel-split ancestry) and does not append the dehydration parent;
  the parent→child dehydration link is a current-residency history/
  projection detail, discarded on re-dehydration. Cycles thus cost no
  durable lineage; the ADR-007 fork model holds for the live single-cycle
  view. (Surfaced for confirmation — the alternative, re-materialize the
  same track id à la SRD-070 cold-restart with dehydration shown only via
  facts, is simpler but drops the fork semantics the ADR chose.)
- **§4.2 Lease-while-dehydrated (single-engine scope).** A dehydrated
  instance has no loop to renew its lease, so the lease lapses after
  `leaseTTL`. Single-engine (this SRD's scope): harmless — the in-memory
  holder owns the wake, and on an engine crash the lapsed lease correctly
  lets restart recovery reclaim the checkpoint (SRD-070) — a fallback that
  applies only to an engine that actually **died**. A live engine never
  loses a hold: a wake that fails KEEPS its hold and retries it after a
  backoff, so the instance recovers in place once the cause clears, with
  no scan of the store and no restart. A **second
  engine** claiming a lapsed-lease dehydrated instance is multi-node
  coordination — deferred to ADR-008 (ADR-007 §5). The dehydrated
  registry is in-memory, single-engine-owned.
- **§4.3 Restore is the one hydration path.** Wake-on-trigger and cold
  restart both call `Restore`; the only difference is the
  `PendingTrigger` (trigger-present continues vs trigger-absent re-arms,
  ADR-007 §2.3). No parallel "wake" rebuild — one code path, two callers.
- **§4.4 The idle detector reuses the capture guards.** "Fully idle" is
  exactly "every live track is a holdable wait AND the capture guards are
  clear" — the same `calls`/`miGroups`/`sweeps` conditions SRD-070
  already computes, so dehydration never fires when a checkpoint can't be
  taken. No new "is it safe" logic.
- **§4.5 Holders roll out independently (ADR-007 §2.4).** The idle
  detector consults a per-wait-kind "is this kind held?" predicate;
  until a holder exists, its kind returns false and the instance stays
  resident. So M3 (timer) ships a correct, green tree where only timer
  instances dehydrate; M4/M5 flip message and human-task on. No broken
  intermediate.
- **§4.6 Single-flight wake.** Two triggers for one dehydrated instance
  (e.g. a timer fires as a message arrives) must hydrate once. A
  per-instance wake latch in the dehydrated registry serializes: the
  first `Hydrate` transitions the registry entry to "hydrating," the
  second observes it and delivers into the (soon-)resident loop via the
  normal path.
- **§4.7 EBG — the wait node is the gateway (ADR-007 §2.4).** A track's
  wait is not always one arm: an EBG arms a set and races them. The
  `Dehydratable` decision lives on the *wait node*, so the EBG (the
  gateway) decides — unconditional true — and its arm events' own
  policies are ignored. The holder becomes a **set per track** (§FR-3),
  a strict generalization that degenerates to a singleton for every
  non-EBG wait, so the core wake path is written once. The one
  EBG-specific step is **withdraw-siblings on wake** — the winning arm's
  holder wakes, the fork fires it, the losing arm-holders are released —
  which mirrors the resident EBG's existing `Withdrawn`. The
  holder-existence guard applies per arm, so an EBG dehydrates only when
  every arm-kind is held (§4.5's incremental rollout, at the arm
  granularity).
- **§4.8 A boundary is a wait of the guarded track, not a track of its
  own (v.2).** FR-9a could have been met by giving each armed boundary
  its own durable identity — a pseudo-track in the checkpoint, released
  and woken independently. Rejected: a boundary has no token and no
  lineage, so a track record for it would be a lie the projection layer
  (`Tokens()`, history) then has to filter back out, and two independent
  wake paths for one activity re-open the interrupted-versus-completed
  race the loop currently owns by construction. Decision: an armed
  boundary is recorded and held **as part of its host track's wait
  set** — the same generalization FR-3a already made for EBG arms,
  which is why the eligibility rule reads identically ("released only
  when every armed wait is held"). The wake, however, is *not* the
  continuation fork: a boundary firing does not re-enter the guarded
  node, it **interrupts** it — the token forks AT the boundary event
  with the guarded track as its parent (interrupting cancels that
  parent, non-interrupting leaves it running), which is exactly what
  the loop's `fireBoundary` produces.

  So a boundary's hold wakes the instance **trigger-absent** (`WaitKind`
  on the hold says which; the trigger belongs to the boundary, not to
  the parked node, and re-entering that node with it would fire the
  wrong element). The rebuild then re-arms the boundary at its recorded
  deadline (FR-9a) — and since that deadline is by definition already
  past, the arm does not register a waiter or take a hold at all: it
  marks the watch **due** and runs the fork immediately, through the
  same `fireBoundary` a resident fire takes. This is the arm-time-true
  Conditional boundary's route (SRD-048 FR-9/FR-15), reused: an event
  that has already happened fires as the watch list lands, rather than
  arming a waiter to notice a deadline that is behind us. Waiting for
  such a waiter would also leave a window in which the instance could
  release again with the fire still pending.
- **§4.9 Checkpoint Schema-2 is additive (v.2).** FR-9a adds
  `Boundaries` to the document. A Schema-1 record decodes with none,
  which is exactly the v.1 behaviour, so no migration is required and a
  store written before the bump keeps restoring. The bump exists to
  record the *intent*: a reader of a Schema-2 document may rely on the
  boundary set being complete, while a Schema-1 document makes no such
  promise.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | `TrackDehydrated` + run-exit (`internal/instance`) | FR-1: a flipped track's `run` returns, emits `evDehydrated`, `ls.active` decrements, the record is retained live |
| T-2 | idle detector + loop release (`internal/instance`) | FR-2: a fully-idle timer-armed instance dehydrates (tracks flipped, checkpoint taken, loop exits, state `Dehydrated`); a job-parked or guard-blocked instance does NOT |
| T-3 | Restore-with-pending-trigger (`internal/instance`) | FR-4: the continuation fork re-enters the wait node firing through (message payload bound); a still-waiting sibling re-arms; the persisted `prev` does not grow |
| T-4 | engine timer service (`pkg/thresher`) | FR-6: a dehydrated timer instance is held by the service, fires at the deadline, hydrates and continues; overdue fires once; **zero goroutines while dehydrated** (goroutine-count assertion) |
| T-5 | message/signal wake (`pkg/thresher`) | FR-7: a dehydrated message-waiting instance wakes on a correlated send, binds the payload, continues; a mismatched key doesn't wake it |
| T-6 | human-task wake (`pkg/thresher`) | FR-8: `Complete` on a dehydrated instance's task hydrates then completes; `instanceForTask` resolves the dehydrated instance |
| T-7 | re-dehydration + oscillation (`pkg/thresher`) | FR-5: an instance with two sequential timer waits dehydrates, wakes, re-dehydrates; lineage bounded across cycles |
| T-8 | no-lost-trigger race (`pkg/thresher`, `-race`) | NFR-1/§4.6: a trigger during the cut aborts that cycle's dehydration; concurrent timer+message wake hydrates once |
| T-9 | crash-while-dehydrated recovery (`pkg/thresher`) | §4.2: an abandoned dehydrated instance is reclaimed by restart recovery after lease lapse (the SRD-070 path, unchanged) |
| T-10 | facts (`pkg/thresher`) | FR-10: `Dehydrated`/`Hydrated` at Info with details (trigger kind, wait kinds, continued-vs-completed) |
| T-EBG | event-based gateway (`pkg/thresher`) | FR-1a/FR-3a: an EBG with a timer + message arm dehydrates (unconditional `Dehydratable`, holder set); the message arm firing wakes+continues and the timer arm-holder is withdrawn; a not-yet-held arm keeps it resident |
| T-11 | example smoke | the dehydration example runs, exit 0, observably dehydrates+wakes |
| T-12 | a successful wake reports no failure (`pkg/thresher`) | FR-3b: a message wake logs no terminal waiter failure — the holder's fire, reported after its own wake unregistered it, is the benign "already gone" case, not `ObjectNotFound` |
| T-13 | withdrawal on boundary-cancel (`internal/instance`) | FR-3b: a track cancelled by an interrupting boundary releases its holds — its deadline/subscription is gone from the engine's registries, so it cannot wake a later cycle |
| T-14 | a boundary survives dehydration (`pkg/thresher`) | FR-9a: an instance parked on a human task under a 24h interrupting boundary timer dehydrates, and the deadline **wakes it and fires the boundary** — the escalation path runs, the task is withdrawn (the v.1 probe's exact scenario, inverted) |
| T-15 | a boundary deadline is recorded, not re-evaluated (`internal/instance`) | FR-9a/§4.9: a duration-based boundary re-armed by restore uses the RECORDED absolute deadline; N dehydrate/wake cycles do not move it; a Schema-1 document restores with no boundaries and behaves as v.1 |

## §7 Milestones

Sliced so the tree is green and correct at each; timer-first is shippable
(ADR-007 §2.4 — un-held kinds stay resident).

- **M1 — `TrackDehydrated` + `evDehydrated` + run-exit + the
  `Dehydratable` capability.** FR-1/FR-1a; T-1.
  `feat(instance): the TrackDehydrated state and goroutine release (SRD-071 M1)`.
- **M2 — the idle detector (via `Dehydratable`) + loop release + the
  instance `Dehydrated` state.** FR-2; T-2 (with a test-driven manual
  hydrate).
  `feat(instance): the fully-idle detector releases the loop (SRD-071 M2)`.
- **M3 — the continuation-fork wake (Restore-with-trigger) + the engine
  dehydrated registry + `Hydrate` + the holder set + the timer service +
  facts.** FR-3/FR-4/FR-5/FR-6/FR-10; T-3/T-4/T-7/T-8/T-9/T-10.
  **Closes #84; shippable here** (timer instances dehydrate; all else
  resident).
  `feat(thresher): the engine timer service and wake-on-trigger (SRD-071 M3)`.
- **M4 — the message/signal holder.** FR-7; T-5.
  `feat(thresher): message/signal wake for dehydrated instances (SRD-071 M4)`.
- **M5 — the Event-Based Gateway (holder set + withdraw-siblings on
  wake).** FR-3a; T-EBG. (After M4 — an EBG's message arms need the
  message holder.)
  `feat(thresher): event-based-gateway dehydration (SRD-071 M5)`.
- **M6 — the human-task holder.** FR-8; T-6.
  `feat(thresher): human-task wake for dehydrated instances (SRD-071 M6)`.
- **M7 — e2e + example + docs.** T-11; the `examples/dehydration` (an
  instance dehydrating on a timer, waking, with a goroutine-count
  observation); the persistence guide gains a dehydration section;
  CHANGELOG; conformance-status #84 closed.
  `feat(thresher): dehydration example and docs (SRD-071 M7)`.
- **M8 — total withdrawal + the boundary safety guard (v.2).** FR-3b +
  FR-9a's eligibility half; T-12/T-13. One routine owns every
  registration a wait made; the boundary-cancel path calls it; the unread
  `taskTracks` registry goes; and `WaiterFired` becomes idempotent for a
  holder that removed itself during its own fire. Ships with the
  eligibility rule as an interim **guard**: an instance with an armed
  hub-registered boundary stays resident. That closes the silent deadline
  loss immediately under the SRD's own "never release a wait nothing can
  wake" rule, at the cost of residency — which M10 gives back. Surfaced
  by running the M7 example: every wake worked, and every message wake
  logged a terminal failure for the holder that had just served it.
  `fix(instance): a released wait withdraws every registration (SRD-071 M8)`.
- **M9 — the boundary arm becomes durable (v.2).** FR-9a's durability
  half; T-15. The checkpoint records armed boundaries (Schema-2,
  additive) and restore re-arms them at the **recorded** deadline
  instead of re-evaluating. No dehydration behaviour changes here — the
  M8 guard still keeps those instances resident — but this alone fixes
  the **restart-recovery** half of the gap, where a recovered
  duration-based boundary silently restarts its clock today.
  `feat(instance): boundary arms ride the checkpoint (SRD-071 M9)`.
- **M10 — the boundary holder; dehydration re-enabled (v.2).** FR-9a's
  liveness half; T-14. An armed boundary takes a holder like any other
  wait, its trigger hydrates and delivers to `fireBoundary` (§4.8), and
  the M8 guard is lifted to the real rule — released only when every
  armed boundary is held. Last, because it consumes M8's withdrawal
  routine, M9's recorded deadline, and the holder-set shape M5
  generalized.
  `feat(instance): armed boundaries survive dehydration (SRD-071 M10)`.

## §8 Cross-doc

- Implements **ADR-007 v.2** (whole) + **ADR-033 v.2** §2.4/§2.5.
- Upstream: ADR-001 v.6, ADR-006 v.4, ADR-013 v.2, ADR-016 v.1,
  ADR-020 v.1, ADR-021 v.1.
- Sideways: **SRD-070 v.1** (the checkpoint/Restore reused).
- Closes **#84** (timer durability — the engine timer service, M3).
- SRD-072 (suspend/resume) is operator-driven dehydration over this
  machinery.

## §9 Definition of Done

- [ ] FR-1/FR-1a/FR-2…FR-3a…FR-10 implemented; every §6 test (incl.
      T-EBG) exists and passes.
- [ ] `make ci` green incl. `-race`; diff-coverage ≥95% (aim 100%);
      touched functions ≥80%.
- [ ] A dehydrated instance holds **zero goroutines** (T-4 assertion).
- [ ] The zero-config engine is unchanged (NFR-3); crash-while-dehydrated
      recovers (T-9).
- [ ] The example runs; guide/CHANGELOG/conformance-status synced.
- [ ] A wait's registrations end with it (FR-3b): the example's own run
      is ERROR-free, a cancelled wait's holds are gone, and no engine
      registry grows across cycles.
- [ ] An armed boundary survives a dehydration cycle and fires at its
      RECORDED deadline (FR-9a); a Schema-1 document still restores.
- [ ] §10 filled.

## §10 Implementation summary

*Filled at landing.*

## Open questions

- **§4.1 bounded lineage** — confirm the inherit-not-append decision (vs
  same-track-id re-materialize). Resolved at the approval gate.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-27 | Ruslan Gabitov | Initial draft — implements ADR-007 v.2 on SRD-070's checkpoint: the `TrackDehydrated` goroutine-terminal state, the **`Dehydratable` capability** the wait node declares (data-driven — Timer weighs its deadline, worker false, **EBG true unconditionally ignoring its arms**), the fully-idle detector (`Dehydratable` + holder-existence guard, reusing the capture guards) that releases the loop, the engine dehydrated registry + `Hydrate` reusing `Restore` (one hydration path, trigger-present continues / trigger-absent re-arms), the direct-fire continuation fork with bounded lineage (§4.1), **the holder as the registered `EventProcessor` in a set-per-track** (singleton for a plain wait, multi-arm for an EBG with withdraw-siblings-on-wake), and a wait-holder per kind rolled out independently — timer (engine service, closes #84), message/signal (id-keyed subscription), EBG, human task (distributor completion) — worker jobs stay resident. Residency facts (`Dehydrated`/`Hydrated` at Info). Single-engine scope; multi-node wake deferred to ADR-008 (§4.2). Seven milestones, timer-first shippable. |
| v.2 | 2026-07-29 | Ruslan Gabitov | Two gaps found while implementing, both amended in rather than split into their own documents — the doc is still Draft, so its requirement set is open. **FR-3b — a wait's registrations end with the wait**: v.1 withdraws on only two of the four paths a wait can exit by, so a boundary-cancelled track keeps its holds and the `taskTracks` registry is written but never read or deleted; plus an ordering rule at the hub, so a holder that removes itself during its own fire does not then report that fire as a failure. **FR-9a — durable armed boundaries**: an armed interrupting boundary is invisible to the release decision and absent from the checkpoint, so a dehydrated instance's boundary never fires (probed: a 24h escalation timer, the clock advanced an hour past it, nothing — the record left `Active` with no way back) and any hydration re-evaluates the deadline instead of restoring it, silently restarting a duration-based boundary — the same trap SRD-070 §4.2 closed for a track's own timer. Adds §4.8 (a boundary is a wait of its host track, not a track of its own — its wake interrupts rather than continues), §4.9 (Schema-2 is additive), T-12…T-15, and milestones M8/M9/M10 — sliced so the silent deadline loss is closed by a stay-resident guard in M8, durability lands in M9 (which alone fixes the restart-recovery half), and M10 gives residency back once boundaries are held. |
| v.2.3 | 2026-07-29 | Ruslan Gabitov | §4.8's wake route corrected during M10, and one defect older than it folded in. The wake is **trigger-absent**, selected by a `WaitKind` carried on the HOLD (`exec.WaitNode` / `exec.WaitBoundary`) rather than on the trigger: a boundary's trigger belongs to the boundary, and re-entering the parked node with it fires the wrong element — the woken instance faulted with "couldn't use past time as a timer" until this was found. The rebuild then re-arms the boundary at its recorded deadline, which is by definition already past, so the arm registers no waiter and takes no hold: it marks the watch DUE and runs the fork at once through `fireBoundary`, reusing the arm-time-true Conditional boundary's route (SRD-048 FR-9/FR-15). Also widens the timer-hold key from `(instance, track)` to include the definition — a pre-M10 defect: an Event-Based Gateway racing TWO timers overwrote the first hold with the second, so if the lost one was the earlier deadline the gateway fired late. `WaitKind` is a named constant rather than a boolean because the set is closed by the BPMN object model and `HoldTimer(…, WaitBoundary)` says at the call site what `true` does not. |
| v.2.2 | 2026-07-29 | Ruslan Gabitov | §3's `BoundaryRecord` keys the arm by the definition's INDEX within its boundary, not by the definition's id — corrected during M9, where the id version failed end to end. An event-definition id is minted per model build, so the recovering engine's model carries different ones and the recorded id never resolves; keying on it would silently drop the deadline of any model whose author had not hand-pinned definition ids, which is the same silent loss FR-9a exists to close. Node ids remain the operator's parity contract (ADR-033 §2.8) and a boundary event is a node, so `BoundaryID` stays. Also records that §4.9's additive-schema claim requires `Unmarshal`'s guard to become an UPPER bound (`1..CurrentSchema`) rather than equality — as written it rejected every Schema-1 document, the opposite of "nothing migrates". |
| v.2.1 | 2026-07-29 | Ruslan Gabitov | Corrects FR-3b's first defect, which v.2 diagnosed wrongly. v.2 claimed a released instance leaves its hub waiter armed and broker-subscribed, inferring a leak from the ERROR the example logs on every message wake. It is not a leak: `armWaiters` (`track.go:512-539`) registers an in-hub waiter only for a definition **no holder took**, and for a held message wait the holder *is* the hub-registered processor (`subscription_holder.go:92`) — the instance registers nothing of its own. The actual defect is ordering: the holder wakes the instance synchronously inside its own `ProcessEvent`, the wake's `ReleaseWaits` unregisters that holder, and the hub waiter then reports a fire for a waiter already removed — `WaiterFired` answers `ObjectNotFound` and the waiter logs it as terminal. The fix is the idempotency `UnregisterEvent` already has for the same reason (`eventhub.go:401-409`), not a withdrawal. The requirement stands on its other two defects (the `evEnded` gap, the unread `taskTracks`); the "goroutine outlives `goroutines=0`" and message-stealing claims are withdrawn as unfounded, and T-12 is re-aimed at the real observable. |
