# SRD-071 — Instance dehydration & wake-on-trigger

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-27 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-007 v.2](../design/ADR-007-in-memory-long-waits.md) (the whole mechanism) + [ADR-033 v.2](../design/ADR-033-persistence-and-state.md) §2.4/§2.5 (the durable projection, the wait-holders) |
| Upstream | [ADR-001 v.6](../design/ADR-001-execution-model.md) §4.7, [ADR-006 v.4](../design/ADR-006-events-and-subscriptions.md), [ADR-013 v.2](../design/ADR-013-instance-observability.md), [ADR-016 v.1](../design/ADR-016-message-correlation.md), [ADR-020 v.1](../design/ADR-020-human-interaction-execution-model.md), [ADR-021 v.1](../design/ADR-021-service-task-execution-model.md) |
| Refines | [SRD-070 v.1](SRD-070-instance-checkpoint-and-restart-recovery.md) (the checkpoint/Restore this reuses as the hydration source) — sideways |

Lands ADR-007's in-memory dehydration/wake mechanism on SRD-070's
checkpoint: the `TrackDehydrated` state, the fully-idle detector that
releases an instance's goroutines, wake-on-trigger via hydration + a
continuation fork, and an engine-level wait-holder per kind (timer →
closes #84; message/signal; human task). Holders roll out independently
(ADR-007 §2.4), so the tree is shippable after the timer holder — the
message and human-task holders stack on top with no broken intermediate.

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
- **FR-2 — the fully-idle detector + loop release.** After the loop
  applies a checkpoint transition, if **every** live track is in the
  dehydratable-parked set (Message/Signal/Timer/UserTask waits — FR-9
  lists what each holder covers) AND the SRD-070 capture guards are
  clear (`calls`/`miGroups`/`sweeps` empty) AND the instance is
  checkpoint-armed (`cpOwner != ""`), the loop **dehydrates**: flip each
  such track to `TrackDehydrated`, take the SRD-070 consistent-cut
  checkpoint, set the instance state to the new **`Dehydrated`**, and
  **exit the loop** (close `loopDone`) — distinct from
  Completed/Terminated (a new loop-exit reason, guarded so
  `settleFinalState` is NOT called). A wait kind without a holder keeps
  the instance resident (never dehydrates a wait it can't wake).
- **FR-3 — the holder is the `EventProcessor`.** A dehydratable wait
  registers its **holder** with the trigger source (the hub, or the
  engine timer service), tagged with `(instance id, track id, wait
  descriptor)` — **not** the track and **not** the instance object. The
  holder is the permanent subscriber, so a trigger never reaches a
  released instance's `emit` (closing the `loopDone`-drop at the root,
  `instance.go:429-434`). On a trigger the holder forks on residency:
  **resident + parked** → resume the live parked track (today's
  `evtCh` path, reached through the holder); **dehydrated** → wake
  (FR-4). Correlation for a Message holder moves onto the holder — it
  carries the checkpoint's conversation keys and gates
  `validateAndAssociate` itself, so a mismatched-key message never wakes
  the instance.
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
  wait (ADR-007 §2.4): its instance does **not** dehydrate until the job
  reports. The idle detector (FR-2) treats a job-parked track as
  non-dehydratable.

### §2.3 Functional — observability

- **FR-10 — residency facts.** `KindInstanceState` gains **`Dehydrated`**
  (Info — the instance released its goroutines; details: parked-track
  count, wait kinds) and **`Hydrated`** (Info — woken; details: the
  trigger kind, live-track count). Lifecycle volume (one per residency
  transition), masking-clean (ADR-013 v.2).

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
  lets restart recovery reclaim the checkpoint (SRD-070). A **second
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
| T-10 | facts (`pkg/thresher`) | FR-10: `Dehydrated`/`Hydrated` at Info with details |
| T-11 | example smoke | the dehydration example runs, exit 0, observably dehydrates+wakes |

## §7 Milestones

Sliced so the tree is green and correct at each; timer-first is shippable
(ADR-007 §2.4 — un-held kinds stay resident).

- **M1 — `TrackDehydrated` + `evDehydrated` + run-exit.** FR-1; T-1.
  `feat(instance): the TrackDehydrated state and goroutine release (SRD-071 M1)`.
- **M2 — the idle detector + loop release + the instance `Dehydrated` state.** FR-2; T-2 (with a test-driven manual hydrate).
  `feat(instance): the fully-idle detector releases the loop (SRD-071 M2)`.
- **M3 — the continuation-fork wake (Restore-with-trigger) + the engine dehydrated registry + `Hydrate` + the timer service + facts.** FR-3/FR-4/FR-5/FR-6/FR-10; T-3/T-4/T-7/T-8/T-9/T-10. **Closes #84; shippable here.**
  `feat(thresher): the engine timer service and wake-on-trigger (SRD-071 M3)`.
- **M4 — the message/signal holder.** FR-7; T-5.
  `feat(thresher): message/signal wake for dehydrated instances (SRD-071 M4)`.
- **M5 — the human-task holder.** FR-8; T-6.
  `feat(thresher): human-task wake for dehydrated instances (SRD-071 M5)`.
- **M6 — e2e + example + docs.** T-11; the `examples/dehydration` (an
  instance dehydrating on a timer, waking, with a goroutine-count
  observation); the persistence guide gains a dehydration section;
  CHANGELOG; conformance-status #84 closed.
  `feat(thresher): dehydration example and docs (SRD-071 M6)`.

## §8 Cross-doc

- Implements **ADR-007 v.2** (whole) + **ADR-033 v.2** §2.4/§2.5.
- Upstream: ADR-001 v.6, ADR-006 v.4, ADR-013 v.2, ADR-016 v.1,
  ADR-020 v.1, ADR-021 v.1.
- Sideways: **SRD-070 v.1** (the checkpoint/Restore reused).
- Closes **#84** (timer durability — the engine timer service, M3).
- SRD-072 (suspend/resume) is operator-driven dehydration over this
  machinery.

## §9 Definition of Done

- [ ] FR-1…FR-10 implemented; every §6 test exists and passes.
- [ ] `make ci` green incl. `-race`; diff-coverage ≥95% (aim 100%);
      touched functions ≥80%.
- [ ] A dehydrated instance holds **zero goroutines** (T-4 assertion).
- [ ] The zero-config engine is unchanged (NFR-3); crash-while-dehydrated
      recovers (T-9).
- [ ] The example runs; guide/CHANGELOG/conformance-status synced.
- [ ] §10 filled.

## §10 Implementation summary

*Filled at landing.*

## Open questions

- **§4.1 bounded lineage** — confirm the inherit-not-append decision (vs
  same-track-id re-materialize). Resolved at the approval gate.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-27 | Ruslan Gabitov | Initial draft — implements ADR-007 v.2's in-memory dehydration/wake mechanism on SRD-070's checkpoint: the `TrackDehydrated` goroutine-terminal state, the fully-idle detector (reusing the capture guards) that releases the loop, the engine dehydrated registry + `Hydrate` reusing `Restore` (one hydration path, trigger-present continues / trigger-absent re-arms), the continuation-fork wake with bounded lineage (§4.1), and a wait-holder per kind rolled out independently — timer (engine service, closes #84), message/signal (retained id-keyed subscription), human task (distributor completion) — with worker jobs staying resident. Residency facts. Single-engine scope; multi-node wake deferred to ADR-008 (§4.2). Six milestones, timer-first shippable. |
