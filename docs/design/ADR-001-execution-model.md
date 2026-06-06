# ADR-001 — Execution Model: Instance + Tracks

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.3 |
| Date | 2026-06-06 |
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

**Scope of this ADR.** It defines the **runtime execution model** — how an instance and its threads of execution live, fork, join, wait, and cancel *in memory*. Persistence and recovery are a first-class P0 requirement ([SAD-001 §6](SAD-001-vision-and-architecture.md)), but their design is a distinct, heavy concern (Scope state, in-flight timer position, compensation/error-event state, per-activity state, a per-node state contract) and is **deferred to a dedicated Persistence & State ADR**. This ADR defines the runtime that persistence will later checkpoint and rehydrate; §4.7 states only the runtime-level invariants persistence must preserve.

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
| Join mechanics              | per merge type — a *synchronizing* join (Parallel/Inclusive) waits for its expected tokens then continues as one track (others merge/end); a *non-synchronizing* merge (Exclusive / uncontrolled) doesn't wait: each arriving track passes through independently |
| State-mutation safety       | Instance state mutated only inside its event-loop goroutine (no locks); track state mutated only inside its own goroutine      |
| Cancellation                | `context.Context` cascade: Engine → Instance → track                                                                           |
| Lineage                     | carried on the track (`track.prev`); a fork's parentage is track parentage                                                     |
| Long waits (in memory)      | track registers a subscription with `EventHub` and its goroutine ends; the Instance re-spawns a track when the trigger arrives |
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
- Receives **track events** (progress / fork / join-arrival / wait / end) on a channel and applies them in order.
- Receives **external signals** from `EventHub` (Message / Timer / Signal arrival); routes to the right track or spawns a fresh one.
- Spawns new tracks at fork points; coordinates join synchronization.
- Owns the root `context.Context`; cancellation cascades to all tracks.
- Decides instance completion: **the instance is done when all tracks have ended** (no track registry left active), or on Terminate / parent cancellation / shutdown.
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

**Instance lifecycle:** `InstanceCreated → InstanceActive → InstanceCompleting → InstanceCompleted`, with branches `InstanceFailing → InstanceFailed` and `InstanceTerminating → InstanceTerminated`.

**track lifecycle (preserved from current code):**

```
TrackCreated → TrackReady → TrackExecutingStep → TrackProcessStepResults → TrackWaitForEvent
                                                      ↓
                                          [TrackMerged | TrackEnded | TrackCanceled | TrackFailed]
```

A track records an **end-reason** on termination so the token-state projection can distinguish *withdrawn* (Event-Based Gateway race loss) from *canceled* (interrupt) — both end the track.

**step lifecycle (preserved):**

```
StepCreated → StepStarted → StepPrologued → StepExecuting → StepEpilogued → StepAwaitsResults → StepEnded → (StepFailed)
```

**token state as a projection.** Token state is not stored; it is a pure function of track + step state:

| Token (logical) | Projected from |
|---|---|
| `Alive` | track executing / ready (`TrackReady` / `TrackExecutingStep` / `TrackProcessStepResults`) |
| `WaitForEvent` | `TrackWaitForEvent` |
| `Consumed` | `TrackEnded` or `TrackMerged` |
| `Withdrawn` | `TrackCanceled` with end-reason = withdrawn |

This replaces the v.2 stored `TokenState` enum + `TokenWithdrawn` value with a derived view; the "withdrawn vs canceled" distinction lives as a track end-reason.

### 4.3 Channel topology (event loop)

Sketch — illustrative, not final API:

```go
type Instance struct {
    ctx    context.Context
    cancel context.CancelFunc

    trackEvents chan trackEvent      // tracks   -> Instance (progress / fork / join / wait / end)
    external    chan ExternalSignal  // EventHub -> Instance (Message / Timer / Signal arrival)

    tracks map[trackID]*track        // mutated ONLY in Instance.loop()
    state  InstanceState
    // ... no token registry
}

func (i *Instance) loop() {
    defer i.shutdown()
    for {
        select {
        case ev := <-i.trackEvents:
            i.applyTrackEvent(ev)        // mutate registry, spawn fork tracks, coordinate joins, advance, maybe complete
        case sig := <-i.external:
            i.applyExternalSignal(sig)   // spawn a track for a waiting subscription
        case <-i.ctx.Done():
            return                       // cancel all tracks, await exit
        }
    }
}
```

A single in-memory event stream (track → Instance) carries lifecycle and progress; the BPMN-observable, token-worded view (split / merged / waiting / consumed / withdrawn) is **derived** from these events for audit (and, later, for persistence checkpoints) — it is not a second live channel.

### 4.4 Fork mechanics

A **fork point** is any FlowNode with N>1 outgoing sequence flows that become active — not gateway-exclusive (an Activity with multiple outgoing flows is an uncontrolled split, per [token-flow.md](../bpmn-spec/semantics/token-flow.md)).

1. Track A is executing the fork node; its active outgoing flows are F1…FN (declaration order).
2. **Track A continues on F1** — its position advances to F1's target; A does not end.
3. For each remaining Fₖ (k=2…N): the Instance constructs a **new track** at Fₖ's target node, with `track.prev = A` (lineage), registers it, and starts its goroutine.
4. After the fork, N tracks run independently — 1 original + N−1 new — each with its own position, each on its own goroutine.

The fork node's type (parallel / inclusive split / activity uncontrolled split) selects which outgoing flows activate (per [gateways.md](../bpmn-spec/semantics/gateways.md)); only activated flows participate.

### 4.5 Join mechanics

A **join point** is any FlowNode with N>1 incoming flows (gateway or activity uncontrolled merge).

1. Tracks T₁…T_M arrive at the join from different incoming flows; each emits its arrival to the Instance.
2. The Instance applies the join rule **by merge type**:
   - **Synchronizing — Parallel** (wait for all expected) / **Inclusive** (wait for the expected reachable subset): once satisfied, the join **consumes** the arrived tokens and continues on **one** track. Implementation: the first-arrived track survives and advances on the outgoing flow; the others **end** (`TrackMerged`), their token projections reading `Consumed`.
   - **Non-synchronizing — Exclusive**, or any node with N>1 incoming flows (uncontrolled merge): **no wait, no consumption** — **each** arriving track passes straight through and continues on the outgoing flow independently. Several tracks legitimately cross the same node; this is a merge, not a fork.
3. No new track is created at a join — continuation always rides an arriving track (the survivor for a synchronizing join; every arrival for a non-synchronizing merge). The 1:1 track:position discipline holds throughout.

Token **consumption** is not a generic join outcome: tokens are consumed at End Events and Terminate, as the merged-away tokens of a synchronizing join, and on withdrawal — never by a non-synchronizing merge.

Event-Based Gateway is not a join — it has one inbound flow and races on the outbound side; race-loss siblings end as `TrackCanceled` with end-reason = withdrawn.

> **Inclusive (OR-join) — implementation attention.** The converging Inclusive Gateway's synchronization is conditional and **non-local**: per BPMN ("a token *MAY* be synchronized with some other tokens that arrive later", precise rule on spec p.291 / KB §13.4.3), a token waits only for incoming flows that *could still* receive a token — it is **not** "wait for all". This is the hardest join to get right; the join coordinator must support conditional subset-waiting via graph-reachability (ComplexGateway's reset phase reuses the same test). The standard's OR-join treatment is **acknowledged-ambiguous** in the literature — there is the spec's *global* definition (Table 13.3 upstream-reachability) and more efficient *local* characterizations, and published formalizations diverge — so the gateway SRD must **pin one compliant semantics**, not assume. None of Parallel / Inclusive / Complex gateways are implemented yet; this needs explicit care when they land (its own SRD).

### 4.6 Context cancellation cascade

- Engine context owns all Instance contexts; Instance context derives from it; track context derives from the Instance context.
- Engine shutdown → cancel all Instance contexts → cascade.
- Terminate End Event → Instance cancels its context → all tracks observe `Done()` → exit as `TrackCanceled`.
- Interrupting boundary event on activity X → Instance cancels only the track executing X.

### 4.7 Long waits, and what persistence will require (runtime invariants only)

In memory, a long wait must not hold a goroutine:

1. A track reaching a long-wait node (UserTask, Timer, ReceiveTask) registers a subscription with `EventHub` and emits a wait event; **its goroutine ends.**
2. The Instance records the pending subscription and keeps no goroutine for the wait.
3. When the trigger arrives, the Instance receives an `ExternalSignal` and **spawns a fresh track** at the wait node to continue.

The **durable** version of this — surviving a process restart — is out of scope here. This ADR only fixes the runtime invariants the Persistence & State ADR must honor:

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
- **End-reason must be set precisely** so the withdrawn/canceled projection is correct.

## 6. Conception vs Current Code — Deliberate Departures

Runtime-only (persistence departures move to the Persistence ADR). The implementation lands per the SRD for this refactor.

| Topic | Current code | This ADR (v.3) | Required change |
|---|---|---|---|
| Token as a type | `token` struct with `inst`, `trk`, `prevs`, `nexts`, `state`; `Instance.tokens []*token` | No stored token; token is a projection of a track's current step | Delete the `token` type and `Instance.tokens`. Add `track.Token()` / `Instance.GetTokens()` returning a computed `Token` value (read-model). |
| Track:token cardinality | `token.split(n)` makes N tokens on the **same** track (`newToken(t.inst, t.trk)`), then `checkFlows` reassigns | A track *is* one position; a fork makes new tracks | Remove `split`; fork constructs new tracks directly at each extra flow's target. |
| Instance ↔ token ownership | `Instance.addToken` / `tokenConsumed`; `token.inst` back-ref; `token.updateState` calls up into Instance | Instance holds tracks only; no token back-refs | Remove `addToken` / `tokenConsumed` / `token.inst`. "Instance done" = all tracks ended (track registry empty/terminal), decided in the event loop. |
| Lineage | duplicated: `track.prev` **and** `token.prevs/nexts` | Single chain on the track | Keep `track.prev`; drop token lineage. |
| State mutation | reactive methods on `Instance` guarded by `sync.RWMutex` | Single event-loop goroutine; tracks send events; no locks on instance state | Add `Instance.loop()` + channel topology; convert direct mutations to events. |
| Token state | stored `TokenState` enum on the token | Derived projection from track/step state (+ track end-reason for withdrawn) | Remove the enum from the token; compute it in the projection; add a track end-reason. |
| Withdrawn semantics | absent | `TrackCanceled` + end-reason = withdrawn (Event-Based Gateway race loss) | Add the end-reason; wire it when Event-Based Gateway lands. |

## 7. Verification

How we know the implementation matches the conception (the acceptance gate — these MUST exist and pass before this ADR flips Draft → Accepted):

| What | How |
|---|---|
| **Race-freedom** | All tests run with `-race` (CI-gated). Instance state is mutated only in `loop()`; any data race is a CI-blocking failure. |
| **Goroutine-leak-free** | Helper asserts `runtime.NumGoroutine()` returns to baseline after instance completion. |
| **1:1 fork** | Process with a 3-way split: assert 3 independent tracks, each with its own position; the parent continued on F1 (did not end at the fork); no shared track reference. |
| **No token registry** | Assert the Instance exposes tokens only via `GetTokens()` (projected from tracks) and holds no `tokens` field; `track.Token()` reflects the current step. |
| **Instance completion** | Assert the instance reaches `InstanceCompleted` exactly when all tracks have ended — not via a token-alive scan. |
| **Synchronizing join** | Parallel join of 3 flows: assert one continuation track survives, the other two end (`TrackMerged`); token projection of the merged tracks reads `Consumed`. |
| **Non-synchronizing merge** | Exclusive merge (or a node with 3 incoming flows) reached by 3 tokens: assert **all 3 pass through** as 3 independent continuations — none waits, none is consumed/merged at the node. |
| **In-memory long wait** | Start a UserTask/Timer wait: assert the waiting track's goroutine exits (count drops) and a fresh track is spawned on trigger arrival. |
| **Termination cascade** | Terminate End Event: assert all track goroutines exit within N ms via `ctx.Done()`. |

Restart-recovery and durable-checkpoint tests belong to the Persistence & State ADR, not here.

## 8. References

- [SAD-001 v.1 Vision & Architecture](SAD-001-vision-and-architecture.md) — §6 Quality Attributes; §10 Execution Model (this ADR refines); §13 Distribution & Scale (preliminary).
- [docs/bpmn-spec/state-machines/activity-lifecycle.md](../bpmn-spec/state-machines/activity-lifecycle.md), [process-lifecycle.md](../bpmn-spec/state-machines/process-lifecycle.md) — normative lifecycles.
- [docs/bpmn-spec/semantics/token-flow.md](../bpmn-spec/semantics/token-flow.md), [gateways.md](../bpmn-spec/semantics/gateways.md), [end-events.md](../bpmn-spec/semantics/end-events.md) — fork/join/termination semantics.
- **Persistence & State ADR** (to be authored) — checkpoint policy, per-node state contract, long-wait durability, restart recovery, Scope/timer/compensation/error/activity state. Depends on the `Repository` interface ([ADR-002 v.1 Extension Architecture](ADR-002-extension-architecture.md)).
- Existing code: `internal/instance/instance.go`, `track.go`, `token.go` — the structural starting point this ADR evolves (token type removed; event loop added).

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.3 | 2026-06-06 | Ruslan Gabitov | Collapsed the three-layer model to **two layers** (Instance + track); `token` becomes a logical projection of a track's current step, not a stored type (removes `token.inst`/`trk`, `Instance.tokens[]`, duplicate lineage). Adopted the single event-loop goroutine for instance-state mutation (no locks). **Deferred persistence/rehydration** to a dedicated Persistence & State ADR; §4.7 keeps only the runtime invariants persistence must honor. Rewrote §2/§3.1/§4/§6/§7 accordingly. Pre-acceptance Draft iteration; amendments folded in without per-round rows. |
| v.2 | 2026-05-29 | Ruslan Gabitov | Three-layer Instance/track/token model (superseded by v.3). |
