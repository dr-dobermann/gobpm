# ADR-001 — Execution Model: Instance + Tracks

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.3 |
| Date | 2026-06-07 |
| Owner | Ruslan Gabitov |
| Supersedes | v.2 (three-layer Instance / track / token). v.3 collapses to **two layers** — `token` becomes a logical *projection* of a track, not a stored entity — and **defers persistence / rehydration** to a dedicated ADR. |
| Refines | [SAD-001 v.1 §10 Execution Model](SAD-001-vision-and-architecture.md) |

## 1. Context

goBpm executes BPMN 2.0 processes per the spec's token-flow model. The in-memory **runtime** this ADR defines MUST handle:

- **Many concurrent Process Instances** (one engine, N instances of different Process definitions running in parallel).
- **Many concurrent execution threads within one instance** (Parallel Gateway split, Multi-Instance Activity, Event-Sub-Process activations).
- **Wildly different time scales** in the same instance — a ServiceTask in milliseconds; a UserTask waiting days; a multi-day Timer. Long waits MUST NOT hold a goroutine.
- **Predictable state mutation with no data races** — transitions match BPMN normative state machines per [activity-lifecycle.md](../bpmn-spec/state-machines/activity-lifecycle.md).
- **Graceful termination** (Terminate End Event cancels all in-flight work in the instance, per [end-events.md](../bpmn-spec/semantics/end-events.md)).

**Scope of this ADR.** It defines the **runtime execution-model core** — how an instance and its threads of execution live, **fork**, and **cancel** *in memory*. The concerns that build on this core are owned by dedicated ADRs and are **not** specified here (see §9): join/merge semantics ([ADR-005](ADR-005-gateways-and-joins.md)), event delivery & event-triggered cancellation ([ADR-006](ADR-006-events-and-subscriptions.md)), the in-memory long-wait release model ([ADR-007](ADR-007-in-memory-long-waits.md)). Persistence and recovery are a first-class P0 requirement ([SAD-001 §6](SAD-001-vision-and-architecture.md)), a distinct heavy concern **deferred to a dedicated Persistence & State ADR**; §4.7 states only the runtime-level invariants persistence must preserve.

The current codebase already has a developed model in `internal/instance/` (Instance + track + token + stepInfo). This ADR formalizes the runtime those types serve and collapses `token` from a stored type to a projection — see §3.1 and §6.

## 2. Decision

**Two-layer ownership. An Instance owns one or more tracks. A `track` is the operational thread of execution — one goroutine — that carries its current flow position and state. The `token` is the BPMN-spec control position expressed as a *logical projection* of a track's current step (`track.Token()`, `Instance.GetTokens()`), not a stored object. The Instance holds a track registry only — there is no token registry; "instance done" means all tracks have ended. Instance-scoped state is mutated only inside the Instance's single event-loop goroutine; tracks report progress as events on a channel and never mutate Instance state directly. A fork creates a new track for each additional branch (1:1 between a track and its position), the parent track continuing on the first flow. `context.Context` is the cancellation contract. Persistence and rehydration are specified separately; this ADR is the in-memory runtime they build on.**

In summary form:

| Concern                     | Mechanism                                                                                                                      |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Instance state ownership    | `Instance` registers and coordinates tracks; mutates instance state only on its serialized event loop                          |
| Per-thread execution        | `track` runs in a dedicated goroutine; drives a step state machine over a sequence of nodes                                    |
| BPMN-spec semantic position | `token` is a **projection** of a track's current step (position + derived state + lineage); computed on demand, never stored   |
| Fork mechanics              | parent track continues on the first flow; one new track per additional flow, each its own goroutine                            |
| State-mutation safety       | Instance state mutated only inside its event-loop goroutine (no locks); track state mutated only inside its own goroutine      |
| Cancellation                | `context.Context` cascade: Engine → Instance → track                                                                           |
| Lineage                     | carried on the track (`track.prev`); a fork's parentage is track parentage                                                     |
| Join / events / long waits  | **out of scope here** — ADR-005 / ADR-006 / ADR-007 (see §9)                                                                   |
| Persistence / restart       | **deferred** — dedicated Persistence & State ADR                                                                               |

## 3. Alternatives Considered

### 3.1 Layering alternatives

| Option | Description | Verdict |
|---|---|---|
| **Single-layer (Instance + token only)** | Each token has its own goroutine and state machine. | Rejected. Loses the execution-stage decomposition (prologue / execute / epilogue / await-results) the `stepState` model captures; overloads token with both BPMN semantics and goroutine driving. |
| **Three-layer (Instance + track + token-as-object)** | Track owns a goroutine + step machine; token is a separate live object owning BPMN position + lineage, 1:1 with the track. | **Rejected (was the v.2 choice).** At 1:1 with no migration, a live token object only duplicates the track: it forced bidirectional back-references (`token.inst`, `token.trk`), a second registry (`Instance.tokens[]` alongside `tracks`), and a duplicate lineage chain (`token.prevs/nexts` mirroring `track.prev`). The token's distinct identity is needed only as a *serialized value* at persistence time — handled in the Persistence ADR — not as a live concurrent object. |
| **Two-layer (Instance + track; token as projection)** — **chosen** | Track is the operational thread carrying position + state; token is a read-model projected from the track's current step. | Selected. Removes the cross-reference tangle and duplicate registries/lineage; the 1:1 invariant holds *by construction* (there is nothing to keep in sync). Token survives as BPMN vocabulary and as a projection/serialized value, not as a stored entity. |
| **Four-layer (explicit Scope as an ownership level)** | Scope as an ownership layer between track and position. | Rejected. Scope (`internal/scope/`) is data-resolution context, not an ownership layer. |

### 3.2 Fork mechanics

| Option | Mechanics | Verdict |
|---|---|---|
| **A. Fork keeps N positions on one track** | One goroutine drives parallel branches sequentially | Rejected. No real parallelism. The current `token.split(n)` keeps the split on the same track — this is the behavior v.3 changes. |
| **B. Fork creates a new track per additional branch** — **chosen** | Each parallel branch gets its own goroutine; the parent continues on the first flow | Selected. True parallelism; matches BPMN's "concurrent threads of execution"; goroutine cost is negligible. |
| **C. Policy-driven (tracks or positions)** | Instance chooses | Rejected. Policy surface without a win; concurrency caps can ride a semaphore over Option B. |

### 3.3 Instance state-mutation

| Option | Mechanics | Verdict |
|---|---|---|
| **Shared state + mutex** | Tracks hold a pointer to the Instance and mutate fields under lock | Rejected. Lock-discipline bug class; race-detector dependency; contention under heavy fan-out. (This is the current code's shape.) |
| **Event-loop serialization** — **chosen** | Instance owns its state; tracks send events on a channel; one Instance goroutine applies them in order | Selected. Single-owner mutation — race-free *by construction*, no locks on instance state. |

### 3.4 Goroutine driver

| Option | Verdict |
|---|---|
| Per-instance OS process | Rejected — start cost, IPC, defeats embeddability. |
| Single global reactor over all instances | Rejected — one slow node blocks all instances. |
| **Goroutine-per-track** — **chosen** | Native Go concurrency; aligns with BPMN's concurrent-thread model. |

## 4. Decision Detail

### 4.1 Roles

#### Instance

The ownership boundary. One goroutine runs the **event loop**; it is the only writer of instance state.

- Holds the registry of live **tracks** (the map of active execution threads). **No token registry.**
- Receives **track events** (progress / fork / end) on a channel and applies them in order.
- Spawns new tracks at fork points.
- Owns the root `context.Context`; cancellation cascades to all tracks.
- Decides instance completion: **the instance is done when all tracks have ended** (no track registry left active), or on context cancellation / shutdown.
- Projects tokens on demand: `Instance.GetTokens()` returns the token view of every active track.

#### track

A single thread of execution. One goroutine per track, for its lifetime.

- Carries its **current flow position** (the node it is at) and its track-level + step-level state.
- Drives the step lifecycle for the current node: prologue → execute → epilogue → await results.
- Executes the node's BPMN behavior via the node's `NodeExecutor`.
- Reads inputs from Scope, writes outputs back; emits **track events** to the Instance at each BPMN-observable transition.
- Carries fork lineage (`track.prev`).
- Handles interruption via `ctx.Done()`; ends cleanly on completion, failure, merge, or cancellation.
- Exposes its position as a token projection: `track.Token()`.

#### token (projection — not a stored type)

The token is the BPMN-spec control-flow position, expressed as a **read-model projected from a track's current step** — `node position + derived state + lineage`. It is computed on demand (`track.Token()`, `Instance.GetTokens()`), never stored as a live cross-linked object, and has **no** back-reference to its track or to the Instance. "token" remains the project's and BPMN's vocabulary (events, audit, and — later — the unit serialized by persistence are token-worded); only the *live object* is gone.

### 4.2 State machines

**Instance lifecycle:** `Created → Active → Completed`, with a cancellation branch `Active → Terminating → Terminated` (`instance.State`). The error branch (`Failing/Failed`) and suspend (`Paused`) are owned by their future ADRs and are deliberately absent from the runtime (see §9).

**track lifecycle (preserved from current code):**

```
TrackCreated → TrackReady → TrackExecutingStep → TrackProcessStepResults → TrackWaitForEvent
                                                      ↓
                                          [TrackMerged | TrackEnded | TrackCanceled | TrackFailed]
```

The runtime produces the terminal track states `TrackEnded` / `TrackCanceled` / `TrackFailed`. `TrackMerged` (synchronizing join) and the *withdrawn* end-reason (Event-Based Gateway race loss) are produced by [ADR-005](ADR-005-gateways-and-joins.md); their enum values exist but have no producer in this core.

**step lifecycle (preserved):**

```
StepCreated → StepStarted → StepPrologued → StepExecuting → StepEpilogued → StepAwaitsResults → StepEnded → (StepFailed)
```

**token state as a projection.** Token state is not stored; it is a pure function of track + step state:

| Token (logical) | Projected from (`tokenStateFor`) |
|---|---|
| `Alive` | `TrackReady` / `TrackExecutingStep` / `TrackProcessStepResults` |
| `WaitForEvent` | `TrackWaitForEvent` |
| `Consumed` | `TrackEnded` / `TrackMerged` / `TrackCanceled` / `TrackFailed` |
| `Withdrawn` | **reserved** — Event-Based Gateway race loss; producer in [ADR-005](ADR-005-gateways-and-joins.md), not yet produced |

This replaces the v.2 stored `TokenState` enum with a derived view. The
`TokenWithdrawn` value exists in the projection type but has no producer yet;
the "withdrawn vs canceled" distinction (a track end-reason) lands with ADR-005.

### 4.3 Channel topology (event loop)

The built shape — one inbound event stream (track → Instance):

```go
type Instance struct {
    ctx    context.Context

    events chan trackEvent    // tracks -> loop()  (evFork / evEnded)
    tracks map[string]*track  // mutated ONLY in loop()
    state  atomic.Uint32      // run state; written only by loop(), read lock-free
    // ... no token registry
}

func (i *Instance) loop(ctx context.Context, initial []*track) {
    // spawn initial tracks, then drain until every track has ended:
    for active > 0 {
        select {
        case <-ctx.Done():
            stopAll()              // signal every track; loop keeps draining
        case ev := <-i.events:     // evFork -> build a track per extra flow
            ...                    // evEnded -> active--
        }
    }
    // all tracks ended -> Completed, or Terminated if cancellation drove it
}
```

A second inbound edge (`EventHub → Instance` for Message / Timer / Signal
delivery) is added by [ADR-006](ADR-006-events-and-subscriptions.md); it is not
part of this core. The BPMN-observable, token-worded view (split / merged /
waiting / consumed / withdrawn) is **derived** from these events for audit (and,
later, for persistence checkpoints) — it is not a second live channel.

### 4.4 Fork mechanics

A **fork point** is any FlowNode with N>1 outgoing sequence flows that become active — not gateway-exclusive (an Activity with multiple outgoing flows is an uncontrolled split, per [token-flow.md](../bpmn-spec/semantics/token-flow.md)).

1. Track A is executing the fork node; its active outgoing flows are F1…FN (declaration order).
2. **Track A continues on F1** — its position advances to F1's target; A does not end.
3. For each remaining Fₖ (k=2…N): the Instance constructs a **new track** at Fₖ's target node, with `track.prev = A` (lineage), registers it, and starts its goroutine.
4. After the fork, N tracks run independently — 1 original + N−1 new — each with its own position, each on its own goroutine.

Which outgoing flows activate by gateway type (parallel / inclusive split /
activity uncontrolled split) is defined in
[ADR-005](ADR-005-gateways-and-joins.md); this core forks over whatever flows
the node reports active.

### 4.5 Join mechanics — out of scope

Join/merge semantics (synchronizing join, non-synchronizing merge, OR-join,
Event-Based Gateway) are **not** part of this runtime core. They are defined in
[ADR-005 Gateways & Joins](ADR-005-gateways-and-joins.md). The runtime has no
join accounting today: a node reached by several tracks is executed once per
arrival.

### 4.6 Context cancellation cascade

- Engine context owns all Instance contexts; Instance context derives from it; track context derives from the Instance context.
- Engine shutdown → cancel all Instance contexts → cascade.
- On cancellation the loop signals every track (`stop()`), keeps draining their terminal events, and reaches `Terminated` once all have exited; a normal drain reaches `Completed`.

The BPMN nodes that *trigger* this cascade — **Terminate End Event** (cancels the whole instance) and **interrupting boundary events** (cancel a single track) — are owned by [ADR-006](ADR-006-events-and-subscriptions.md); this core owns the cascade mechanism, not its BPMN triggers.

### 4.7 Runtime invariants for long waits & persistence

The **in-memory long-wait release model** (a waiting track's goroutine ends; a
fresh track is spawned on trigger arrival) is owned by
[ADR-007 In-Memory Long Waits](ADR-007-in-memory-long-waits.md), and the
**durable** version (surviving a restart) by the Persistence & State ADR. This
core fixes only the runtime invariants both must honor:

- A track's continuation state is fully described by its **position (node), track/step state, Scope data, and lineage** — there is no hidden state on a separate token object.
- A node with resumable in-flight state (timer position, correlation subscription, partial activity state) owns the shape of that state; persistence will reach it through a per-node state contract (defined in the Persistence ADR), **not** by storing mutable state on the shared node definition (node definitions are shared across instances and tracks and MUST stay immutable).

## 5. Consequences

**Pros**

- **Faithful to BPMN vocabulary, without the overhead.** Token stays the spec's concept; track is the operational primitive; the token is just the track's current position, projected.
- **1:1 by construction.** No second registry, no duplicate lineage, no token↔track/instance back-references to keep consistent.
- **Race-free instance state.** Single event-loop owner; no locks on instance state; the race detector (now gating CI) backs this up.
- **Native to Go.** Goroutines + channels + contexts; no framework.
- **Reuses existing structure.** `track`, `stepInfo`, `trackState`, `stepState` carry forward; the work is removing the token type and the reactive/lock paths, not inventing machinery.

**Cons / things to enforce**

- **Goroutine count = sum of active tracks.** Bounded by BPMN structure; pathological models can be capped via a semaphore over the fork path (optimization, not a model change).
- **Channel discipline is mandatory.** Every track MUST emit a terminal event before its goroutine exits, or it leaks. Mitigation: `defer` cleanup; tests assert `runtime.NumGoroutine()` returns to baseline.
- **No fairness between tracks.** Go's scheduler decides order; token-flow tests MUST NOT depend on goroutine scheduling order.
- **Terminal events must always be delivered.** A track's `evEnded` is accounted even during cancellation (the loop drains to a terminal state); `emit` drops only after the loop has exited. (The withdrawn/canceled end-reason distinction itself arrives with [ADR-005](ADR-005-gateways-and-joins.md).)

## 6. Conception vs Current Code — Deliberate Departures

Runtime-only (persistence departures move to the Persistence ADR). The implementation lands per the SRD for this refactor.

| Topic | Current code | This ADR (v.3) | Required change |
|---|---|---|---|
| Token as a type | `token` struct with `inst`, `trk`, `prevs`, `nexts`, `state`; `Instance.tokens []*token` | No stored token; token is a projection of a track's current step | Delete the `token` type and `Instance.tokens`. Add `track.Token()` / `Instance.GetTokens()` returning a computed `Token` value (read-model). |
| Track:token cardinality | `token.split(n)` makes N tokens on the **same** track (`newToken(t.inst, t.trk)`), then `checkFlows` reassigns | A track *is* one position; a fork makes new tracks | Remove `split`; fork constructs new tracks directly at each extra flow's target. |
| Instance ↔ token ownership | `Instance.addToken` / `tokenConsumed`; `token.inst` back-ref; `token.updateState` calls up into Instance | Instance holds tracks only; no token back-refs | Remove `addToken` / `tokenConsumed` / `token.inst`. "Instance done" = all tracks ended (track registry empty/terminal), decided in the event loop. |
| Lineage | duplicated: `track.prev` **and** `token.prevs/nexts` | Single chain on the track | Keep `track.prev`; drop token lineage. |
| State mutation | reactive methods on `Instance` guarded by `sync.RWMutex` | Single event-loop goroutine; tracks send events; no locks on instance state | Add `Instance.loop()` + channel topology; convert direct mutations to events. |
| Token state | stored `TokenState` enum on the token | Derived projection from track/step state (`tokenStateFor`) | Done — enum removed from the token; computed in the projection. The withdrawn end-reason is relocated to [ADR-005](ADR-005-gateways-and-joins.md). |
| Instance lifecycle | 9 ad-hoc states (`Created`/`Ready`/`StartingTracks`/`Runned`/`Stopping`/`Paused`/`FinishingTracks`/`Finished`/`Canceled`), several unused | `Created → Active → Completed`; cancel branch `Terminating → Terminated` | Done — enum reconciled to the §4.2 vocabulary; error (`Failing/Failed`) and suspend (`Paused`) relocated to their future ADRs (§9). |

> **Known issue (relocated).** Node execution calls `NodeDataLoader.RegisterData`,
> which currently **mutates the shared node** (e.g. `EndEvent.dataPath`) — a
> violation of §4.7 immutability that races when two tracks cross one node
> (surfaced by a non-synchronizing-merge test). The fix is a per-node state /
> data-loading contract owned by the **Persistence & State ADR**; non-sync merge
> over a shared node is therefore tracked in [ADR-005](ADR-005-gateways-and-joins.md),
> not claimed by this core's §7 gate.

## 7. Verification

How we know the implementation matches the conception — the acceptance gate for
this runtime core. All rows below are **exercised and green** (tests in
`internal/instance/`, run under `-race` in CI); this is the evidence backing the
Accepted status.

| What | How | Exercised by |
|---|---|---|
| **Race-freedom** | All tests run with `-race` (CI-gated). Instance state is mutated only in `loop()`; any data race is a CI-blocking failure. | whole package `-race` |
| **Goroutine-leak-free** | Helper asserts `runtime.NumGoroutine()` returns to baseline after completion. | `leakcheck_test.go` |
| **1:1 fork** | 2-way split: assert independent tracks each with its own position; parent continued on F1 (did not end); child branched after the fork. | `TestM4ForkCompletes` |
| **No token registry** | Assert tokens are exposed only via `GetTokens()` (projected from tracks), no `tokens` field; `track.Token()` reflects the current step. | `TestTokenStateProjection`, `TestM3*` |
| **Instance completion** | Assert the instance reaches `Completed` exactly when all tracks have ended — not via a token-alive scan. | `TestM2LinearCompletes`, `TestM4ForkCompletes` |
| **Termination cascade** | Cancelling the context stops every track and drains its goroutine within a bound; the instance reaches a terminal state. | `TestTerminationCascade` |

Relocated gates are owned by their ADRs: synchronizing join / non-synchronizing
merge → [ADR-005](ADR-005-gateways-and-joins.md); in-memory long wait →
[ADR-007](ADR-007-in-memory-long-waits.md); Terminate End Event / boundary
triggers → [ADR-006](ADR-006-events-and-subscriptions.md). Restart-recovery and
durable-checkpoint tests belong to the Persistence & State ADR.

## 8. References

- [SAD-001 v.1 Vision & Architecture](SAD-001-vision-and-architecture.md) — §6 Quality Attributes; §10 Execution Model (this ADR refines); §13 Distribution & Scale (preliminary).
- [docs/bpmn-spec/state-machines/activity-lifecycle.md](../bpmn-spec/state-machines/activity-lifecycle.md), [process-lifecycle.md](../bpmn-spec/state-machines/process-lifecycle.md) — normative lifecycles.
- [docs/bpmn-spec/semantics/token-flow.md](../bpmn-spec/semantics/token-flow.md), [gateways.md](../bpmn-spec/semantics/gateways.md), [end-events.md](../bpmn-spec/semantics/end-events.md) — fork/join/termination semantics.
- [ADR-005 v.1 Gateways & Joins](ADR-005-gateways-and-joins.md), [ADR-006 v.1 Events & Subscriptions](ADR-006-events-and-subscriptions.md), [ADR-007 v.1 In-Memory Long Waits](ADR-007-in-memory-long-waits.md) — concerns refining this core (see §9).
- **Persistence & State ADR** (to be authored) — checkpoint policy, per-node state contract, long-wait durability, restart recovery, Scope/timer/compensation/error/activity state. Depends on the `Repository` interface ([ADR-002 v.1 Extension Architecture](ADR-002-extension-architecture.md)).
- Existing code: `internal/instance/instance.go`, `track.go`, `token.go` — the runtime this ADR formalizes (token type removed; event loop; lifecycle reconciled).

## 9. Out of scope — owned by future ADRs

This ADR is the runtime **core**. The following build on it and are specified in
dedicated ADRs (sideways refs, hierarchy-consistent); each carries its own
acceptance gate and lands with its own SRD + code:

| Concern | Owner | Status |
|---|---|---|
| Join/merge semantics — synchronizing join, non-synchronizing merge, OR-join, Event-Based Gateway + `Withdrawn` end-reason; fork-flow activation by gateway type | [ADR-005 Gateways & Joins](ADR-005-gateways-and-joins.md) | Draft |
| Event delivery (`EventHub` → Instance), Terminate End Event, interrupting boundary events, wait nodes | [ADR-006 Events & Subscriptions](ADR-006-events-and-subscriptions.md) | Draft |
| In-memory long-wait release model (subscription → goroutine ends → re-spawn) | [ADR-007 In-Memory Long Waits](ADR-007-in-memory-long-waits.md) | Draft |
| Durable persistence & restart recovery; per-node state contract (fixes the shared-node `RegisterData` mutation, §6) | Persistence & State ADR (to be authored) | — |
| Instance error states (`Failing/Failed`) and suspend (`Paused`) | future Error-Handling / Persistence ADRs | — |

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.3 | 2026-06-07 | Ruslan Gabitov | **Accepted.** Collapsed the three-layer model to **two layers** (Instance + track); `token` becomes a logical projection of a track's current step, not a stored type (removes `token.inst`/`trk`, `Instance.tokens[]`, duplicate lineage). Adopted the single event-loop goroutine for instance-state mutation (no locks). Scoped this ADR to the runtime **core** — relocated join/merge (ADR-005), event delivery & triggers (ADR-006), and the in-memory long-wait release model (ADR-007) to dedicated ADRs (§9), so doc equals code. Reconciled the instance lifecycle to `Created → Active → Completed` (+ `Terminating → Terminated`). §7 gate exercised and green (race-freedom, leak-free, fork, projection, completion, termination cascade) — including two bugs the gate surfaced (`track.stopIt` race; `emit` dropping `evEnded` on cancel). Persistence/rehydration deferred to the Persistence & State ADR. Pre-acceptance Draft iteration folded in without per-round rows. |
| v.2 | 2026-05-29 | Ruslan Gabitov | Three-layer Instance/track/token model (superseded by v.3). |
