# ADR-001 — Execution Model: Instance + Tracks + Tokens

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.2 |
| Date | 2026-05-29 |
| Owner | Ruslan Gabitov |
| Supersedes | v.1 (the initial single-layer Orchestrator + Token sketch) |
| Refines | [SAD-001 v.1 §10 Execution Model](SAD-001-vision-and-architecture.md) |

## 1. Context

goBpm executes BPMN 2.0 processes per the spec's token-flow model. The engine MUST handle:

- **Many concurrent Process Instances** (one engine, N instances of different Process definitions running in parallel).
- **Many concurrent execution threads within one instance** (Parallel Gateway split, Multi-Instance Activity, Event-Sub-Process activations).
- **Wildly different time scales** in the same instance — a ServiceTask in milliseconds; a UserTask waiting days; a multi-day Timer.
- **Mandatory persistence and recovery** (per [SAD-001 §6 Quality Attributes](SAD-001-vision-and-architecture.md), P0). The engine MUST be restartable mid-flight without losing state.
- **Predictable state mutation** with no data races; transitions match BPMN normative state machines per [docs/bpmn-spec/state-machines/activity-lifecycle.md](../bpmn-spec/state-machines/activity-lifecycle.md).
- **Graceful termination** (Terminate End Event cancels all in-flight work in the instance, per [docs/bpmn-spec/semantics/end-events.md](../bpmn-spec/semantics/end-events.md)).
- **Extensibility at distribution boundaries** (per [SAD-001 §13 Distribution & Scale](SAD-001-vision-and-architecture.md), preliminary): task-level remote execution, instance-level pinning, future cluster-shared state.

The execution model is the load-bearing decision that all of the above hinge on. The current codebase already has a developed model in `internal/instance/` (Instance + track + token + stepInfo with their state machines). This ADR formalizes the conception that those types serve, and identifies where conception requires the code to evolve.

## 2. Decision

**Three-layer ownership: an Instance owns one or more tracks; a track owns exactly one token (track:token is 1:1 for the track's lifetime). The track is the unit of goroutine binding; the token is the BPMN-spec control position. Mutation of instance-scoped state happens through the Instance's serialized channel-input loop. A fork does not add tokens to a track — it creates a new track per branch, each with its own token, registered with the Instance; the parent track ends. The Instance holds the track registry only, not a token registry. `context.Context` is the cancellation contract. Long-wait states release their goroutines and rehydrate from `Repository` when their trigger arrives.**

In summary form:

| Concern | Mechanism |
|---|---|
| Instance state ownership | `Instance` registers and coordinates tracks; mutates instance-scoped state on its serialized event loop |
| Per-thread execution | `track` runs in a dedicated goroutine; drives a step state machine over a sequence of nodes |
| BPMN-spec semantic position | `token` represents control flow position; has a simple lifecycle aligned with BPMN |
| Fork mechanics | `Instance.Fork(parentTrack, n) -> []*track` creates N new tracks (each its own goroutine), each carrying one of the split tokens. Parent track ends. |
| Join mechanics | Joining tracks emit `TokenMerged` events to Instance; Instance coordinates the synchronization point per gateway semantics; the join successor lives on a continuation track |
| State mutation safety | Instance state mutated only inside Instance's event loop; track state mutated only inside its goroutine; tokens carry immutable snapshots |
| Cancellation | `context.Context` cascade: Engine → Instance → track. Cancel at any level propagates downward |
| Persistence checkpoint | At every observable BPMN lifecycle transition (per `bpmn-spec/state-machines/`) |
| Long-wait handling | Token state → `WaitForEvent`; track ends (goroutine exits); Instance persists subscription; on trigger arrival, Instance spawns a fresh track |
| Restart recovery | Runtime queries `Repository` for in-flight Instances → each Instance rehydrated → tracks reconstructed from persisted token positions |

## 3. Alternatives Considered

### 3.1 Layering alternatives

| Option | Description | Verdict |
|---|---|---|
| **Single-layer (Instance + Token only)** | Drop the track concept; each token has its own goroutine and state machine. | Rejected. Loses the natural execution-stage decomposition (prologue / execute / epilogue / await-results) the current `stepState` model captures. Token is overloaded with both BPMN semantics and goroutine driving. |
| **Two-layer (Instance + Track only)** | Drop the token concept; track owns BPMN position directly. | Rejected. Loses explicit BPMN-spec semantics — token is the spec's vocabulary, not an implementation invention. Loses fork lineage as a first-class concept. |
| **Three-layer (Instance + track + token)** — chosen, tentative | Track owns goroutine + step state machine; token owns BPMN position + lineage. Track:token is 1:1 throughout the track's lifetime. | Selected for v.2 with a caveat: formally, `track` is a transport for `token` (or `token` is a state-reflection of `track`). The three-layer split may collapse to two layers (Instance + track-with-embedded-token) if implementation shows the token-as-separate-type doesn't carry its weight. The validation happens during implementation; if collapse is warranted, this ADR will be bumped to v.3. For now, three layers remains the working model. |
| **Four-layer (add explicit Scope as ownership level)** | Scope as an ownership layer above token, below track. | Rejected for v.1. Scope hierarchy exists (`internal/scope/`) but as data-resolution context, not ownership. Re-evaluable in a future ADR if needed. |

### 3.2 Fork mechanics alternatives

| Option | Mechanics | Verdict |
|---|---|---|
| **A. Fork creates N tokens on the same track** | One goroutine drives parallel branches sequentially under the hood | Rejected. No actual parallelism. Defeats SAD-001 §10's intent. The current code's `token.split(n)` keeps tokens on the same track — this ADR's conception requires this to change. |
| **B. Fork creates N new tracks, parent track ends** — chosen | Each parallel branch gets its own goroutine; parent's resources released | Selected. True parallelism; matches BPMN's "concurrent threads of execution"; goroutine cost is negligible. |
| **C. Instance chooses whether to fork as tracks or as tokens** | Policy-driven | Rejected. Adds policy surface area without clear win. Implementations can apply concurrency limits via semaphore on top of Option B without changing the conception. |

### 3.3 State-mutation alternatives (Instance state)

| Option | Mechanics | Verdict |
|---|---|---|
| **Shared state + mutex** | Tracks hold pointer to Instance, mutate fields under lock | Rejected. Lock-discipline bug class; race-detector dependency to catch slip-ups; mutex contention under heavy fan-out. |
| **Per-track local state, periodic sync** | Each track has a copy; periodic reconciliation. | Rejected. Reconciliation semantics for BPMN state are unclear; risks divergence. |
| **Event-loop serialization in Instance** — chosen | Instance owns state. Tracks send events on a channel; Instance applies them in order in a single goroutine. | Selected. Same single-owner-mutation principle as v.1, but applied at Instance level rather than at a phantom Orchestrator. |

### 3.4 Goroutine-driver alternatives

| Option | Mechanics | Verdict |
|---|---|---|
| **Per-instance OS process** | Process isolation per Instance | Rejected. Process start cost; IPC overhead; defeats embeddability. |
| **Single event loop / reactor** | One goroutine over all tokens across all instances | Rejected. Defeats Go's strength; one slow node blocks all instances. |
| **Persisted-only execution** (no in-memory tracks) | Every transition writes to Repository, reads back | Rejected. Catastrophic perf for short-lived instances. |
| **Goroutine-per-track** — chosen | Each track binds one goroutine for its lifetime | Selected (per Option B above). Native Go concurrency; aligns with BPMN's concurrent-thread model. |

## 4. Decision Detail

### 4.1 Roles

#### Instance

The Process Instance is the ownership boundary. One goroutine per Instance runs the **event loop**.

Responsibilities:
- Hold the registry of live tracks (the map of all active execution threads).
- Hold **no** token registry — tokens are reached only through their tracks (`track.Token()`, per §4.2). Token lineage (`prevs` / `nexts`) is carried by the tokens themselves and persisted to `Repository`.
- Receive `TokenEvent`s from tracks; apply state transitions.
- Receive `ExternalSignal`s from `EventHub` (Message arrived, Timer fired, Signal received); route to the appropriate track or spawn a new one.
- Spawn new tracks at fork points (`Instance.Fork(parent, n)`).
- Coordinate join points: when N tokens arrive at a join, decide when to advance.
- Drive persistence checkpoints at observable BPMN lifecycle transitions.
- Own the root `context.Context` for the instance; cancel cascades to all tracks.
- Decide instance termination (Terminate End Event, parent SubProcess cancellation, runtime shutdown).

#### track

A track is a single thread of execution within an Instance. One goroutine per track.

Responsibilities:
- Drive the **step lifecycle** for the current node being executed: prologue → execute → epilogue → await results.
- Hold per-step state (`stepInfo`) and the track-level state machine.
- Execute the node's BPMN behavior (call the appropriate `NodeExecutor` per node type).
- Read node inputs from scope; write outputs back; emit `TokenEvent` to Instance on transition.
- Handle interruption (interrupting boundary events, instance cancellation) via `ctx.Done()`.
- End cleanly on completion, failure, merge, or cancellation.

#### token

A token is the BPMN-spec control flow position. NOT a goroutine driver. **A track always owns exactly one token; track:token is 1:1 throughout the track's lifetime.**

Responsibilities:
- Carry the BPMN-spec semantic position (which node, which incoming flow, lineage to prev / next tokens).
- Hold the token-level state (`Alive` / `WaitForEvent` / `Withdrawn` / `Consumed`).
- Be constructed alongside its owning track — a track's constructor takes `(startNode, token)`; the token is bound to the track from the start and never migrates.
- Be immutable from outside its owning track (snapshots passed elsewhere).

**Instance does NOT hold tokens directly.** Instance owns the registry of tracks; tokens are accessed only through tracks (`track.Token()`). This is a deliberate departure from current code where Instance has its own `addToken` / `tokenConsumed` calls — see §6.

### 4.2 State machines

#### Instance lifecycle (new — to be defined explicitly)

`InstanceCreated → InstanceActive → InstanceCompleting → InstanceCompleted` (with branches `InstanceFailing → InstanceFailed`, `InstanceTerminating → InstanceTerminated`).

#### track lifecycle (existing, preserved)

```
TrackCreated → TrackReady → TrackExecutingStep → TrackProcessStepResults → TrackWaitForEvent
                                                      ↓
                                          [TrackMerged | TrackEnded | TrackCanceled | TrackFailed]
```

#### step lifecycle (existing, preserved)

```
StepCreated → StepStarted → StepPrologued → StepExecuting → StepEpilogued → StepAwaitsResults → StepEnded
                                                                                                  ↓
                                                                                              StepFailed
```

#### token lifecycle (revised — adds Withdrawn)

```
TokenAlive ──→ TokenWaitForEvent ──→ TokenConsumed
   │                                       ↑
   └────────→ TokenWithdrawn ──────────────┘     (Event-Based Gateway race loss → consumed via withdrawn path)
```

The `TokenWithdrawn` state is new vs the current code (which has only `Alive / WaitForEvent / Consumed`). It is required to honor the BPMN Withdrawn semantics from [activity-lifecycle.md](../bpmn-spec/state-machines/activity-lifecycle.md) without conflating "raced out" with "naturally consumed."

### 4.3 Channel topology

Sketch — illustrative, not the final API:

```go
type Instance struct {
    ctx        context.Context           // root context for this instance
    cancel     context.CancelFunc

    tokenEvents chan TokenEvent          // tracks   -> Instance
    trackEvents chan TrackEvent          // tracks   -> Instance (lifecycle events)
    external    chan ExternalSignal      // EventHub -> Instance (Message / Timer / Signal arrival)

    tracks  map[trackID]*track            // mutated only in Instance.loop()
    tokens  map[tokenID]*token            // mutated only in Instance.loop()
    repo    Repository
    // ...
}

type track struct {
    ctx     context.Context              // derived from Instance.ctx
    cancel  context.CancelFunc
    out     chan<- TokenEvent            // back to Instance (only write side)
    trackOut chan<- TrackEvent           // back to Instance (only write side)
    tk      *token                       // current token under this track
    step    *stepInfo                    // step state
    // ...
}
```

Instance main loop:

```go
func (i *Instance) loop() {
    defer i.shutdown()
    for {
        select {
        case ev := <-i.tokenEvents:
            i.applyTokenEvent(ev)        // may mutate state, spawn tracks (fork), advance tokens, checkpoint
        case ev := <-i.trackEvents:
            i.applyTrackEvent(ev)        // track lifecycle accounting
        case sig := <-i.external:
            i.applyExternalSignal(sig)   // may spawn tracks for waiting subscriptions
        case <-i.ctx.Done():
            return                       // shutdown path — cancel all tracks, await exit, final checkpoint
        }
    }
}
```

Track main loop:

```go
func (t *track) run() {
    defer t.cleanup()                    // sends TrackEnded / TrackFailed / TrackMerged via trackOut
    for t.state != TrackEnded && t.state != TrackFailed && t.state != TrackMerged && t.state != TrackCanceled {
        select {
        case <-t.ctx.Done():
            t.updateState(TrackCanceled)
            return
        default:
            t.executeStep()              // drives stepState transitions; emits TokenEvent at each BPMN-observable boundary
        }
    }
}
```

### 4.4 TokenEvent taxonomy (BPMN-observable stream)

| Event | Meaning |
|---|---|
| `TokenCreated{trackID, nodeID}` | Initial token at a Start Event, or new token created by a fork |
| `TokenArrived{nodeID}` | Token reached node (begins Ready per BPMN spec) |
| `TokenSplit{nodeID, count}` | Fork at gateway / activity with multiple outgoing flows. Instance handles new-track creation. |
| `TokenMerged{nodeID, consumedTokenIDs}` | Join at gateway. Multiple tokens consumed; one continues on the join's outgoing flow. |
| `TokenAdvanced{from, to}` | Token moved along a single sequence flow (no fork, no join) |
| `TokenWaiting{nodeID, trigger}` | Long-wait state. Track ends after this event; Instance persists the subscription. |
| `TokenWithdrawn{nodeID}` | Event-Based Gateway race loss; token consumed via withdrawn path |
| `TokenConsumed{nodeID, reason}` | Token reached terminus (End Event / Terminate / merge into another token / withdrawal completion) |

These are the events relevant to persistence and BPMN-observable audit. Subscribers: Repository (checkpoint), audit emitter, conformance test verifier.

### 4.5 TrackEvent taxonomy (execution-observable stream)

| Event | Meaning |
|---|---|
| `TrackSpawned{trackID, parentTrackID}` | Track created (either initial track at Start Event or fork child) |
| `StepStarted{trackID, nodeID}` | Track entered step on a node |
| `StepPrologued / StepExecuting / StepEpilogued` | Per-stage progress within a step (uses existing `stepState` values) |
| `StepEnded / StepFailed` | Step concluded |
| `TrackMerged{intoTrackID}` | Track ended at join — its token merged into another track's continuation |
| `TrackEnded / TrackCanceled / TrackFailed` | Track lifecycle terminus |

These are the events relevant to diagnostics, profiling, per-element latency. Subscribers: `Tracer`, `MetricsRecorder`, diagnostic dump endpoints.

Persistence does NOT subscribe to track events — track is implementation detail; only token events trigger checkpoints.

### 4.6 Fork mechanics

A **fork point** is any FlowNode with N>1 outgoing sequence flows that all become active. This is NOT gateway-exclusive — per [token-flow.md](../bpmn-spec/semantics/token-flow.md), an Activity with multiple outgoing flows is also a fork point (uncontrolled split). The mechanics below apply uniformly.

1. Track A is executing the fork node. The node's outgoing sequence flows are F1, F2, …, FN (in declaration order).
2. The token splits: **the existing token continues on F1** (the first outgoing flow); **N−1 new tokens** are created for F2…FN with proper lineage (`prevs` / `nexts`).
3. **Track A continues** — its token's position advances to the target node of F1; track A's next step executes that target node. Track A does not end.
4. For each remaining flow Fₖ (k=2..N):
   - Instance constructs a new track with `(target_of_Fk, new_tokenₖ)` via the track's `(startNode, token)` constructor.
   - The new track is registered in `Instance.tracks`.
   - The new track's goroutine is started.
5. After fork: N tracks run independently (1 original + N−1 new), each with its own token, each on its own goroutine.

The fork point's type (parallel split / inclusive split / activity uncontrolled split) determines which outgoing flows are activated (per [gateways.md](../bpmn-spec/semantics/gateways.md) / [token-flow.md](../bpmn-spec/semantics/token-flow.md)). The Instance applies the appropriate selection rule before the fork mechanics above; only activated flows participate.

`TokenSplit{nodeID, count: N, survivorTrackID, newTokenIDs[]}` is emitted to the TokenEvent stream after step 4 completes, BEFORE the new tracks' first node-execution step runs (so persistence sees the fork before any further state advances).

### 4.7 Join mechanics

A **join point** is any FlowNode with N>1 incoming sequence flows. This is NOT gateway-exclusive — an Activity with multiple incoming flows is also a join point (uncontrolled merge per [token-flow.md](../bpmn-spec/semantics/token-flow.md), behaves as implicit XOR). The mechanics below cover both synchronizing and non-synchronizing joins.

1. Tracks T₁, T₂, …, T_M arrive at the join node from different incoming flows. Each track emits `TokenArrived{joinNode}` to Instance on arrival.
2. Instance applies the join's synchronization rule:
   - **Parallel Gateway**: wait for ALL expected M tokens.
   - **Inclusive Gateway**: wait for the EXPECTED subset per graph-reachability rule ([gateways.md](../bpmn-spec/semantics/gateways.md)).
   - **Exclusive Gateway / Activity uncontrolled merge**: no wait — each arrival proceeds independently as if the join were just pass-through.
3. **Synchronizing joins** (Parallel, Inclusive): once the join condition is satisfied:
   - **The first-arrived track (T₁) survives and continues** — its token advances to the outgoing flow's target node; T₁'s next step executes there.
   - **All other arrived tracks (T₂…T_M) end** — `TrackMerged` event; their tokens enter `Consumed` state.
   - `TokenMerged{joinNode, survivorTrackID, consumedTokenIDs: [T₂.token, …]}` emitted to the TokenEvent stream.
4. **Non-synchronizing joins** (XOR, activity uncontrolled merge): each arriving track just continues — no merge event, no token consumption, no track termination at the join itself.

Event-Based Gateway is NOT a join point in this taxonomy — it has a single inbound flow and races on the outbound side. Race-loss semantics (sibling tracks → `TokenWithdrawn`) is handled at the Event-Based Gateway's outbound coordination, not at a downstream join.

No new track is created at any join. Continuation always rides on an existing arriving track. This is a 1:1 track:token discipline preserved across joins.

### 4.8 Context cancellation cascade

- **Engine context** owns all Instance contexts.
- **Instance context** derived from Engine context: `instanceCtx, cancel := context.WithCancel(engineCtx)`.
- **Track context** derived from Instance context: `trackCtx, cancel := context.WithCancel(instanceCtx)`.

Cancellation effects:
- Engine shutdown → cancel all Instance contexts → cascade.
- Terminate End Event → Instance calls `cancel()` on its context → all tracks see `Done()` → exit via `TrackCanceled`.
- Interrupting boundary event on activity X → Instance cancels only the track currently executing X (not other tracks of the same instance).

### 4.9 Persistence checkpoint policy

Checkpoint triggers (all align with normative BPMN lifecycle transitions):

- `TokenArrived` at a node (Ready state).
- `TokenAdvanced` along a sequence flow.
- `TokenSplit` → Instance persists the surviving parent token's new position AND all N−1 new tokens (with their lineage) BEFORE constructing the new tracks. New tracks receive `(startNode, token)` at construction; they have no "before-start" phase separate from their first step.
- `TokenMerged` → consumed tokens' final state + new continuation token checkpointed.
- `TokenWaiting` → wait subscription + token state checkpointed BEFORE the track goroutine exits.
- `TokenWithdrawn` / `TokenConsumed` → final state checkpointed.

Non-checkpoint events (track-level): `StepStarted`, `StepPrologued`, `StepExecuting`, `StepEpilogued`, `StepAwaitsResults`, `StepEnded`, `StepFailed`. These are diagnostic-only; not checkpointed. Bounds write volume.

The `Repository` interface defines the checkpoint contract; default in-memory impl is non-durable; production impls (e.g., `adapters/postgres/`) provide durability.

### 4.10 Long-wait release-and-rehydrate

A UserTask waiting on human action, a multi-day Timer, a ReceiveTask awaiting external Message — these MUST NOT hold a track goroutine for the wait duration.

Sequence:

1. Track executes the wait node's pre-wait setup. Emits `TokenWaiting{nodeID, trigger}` to Instance.
2. Instance receives the event:
   - Persists token state (TokenWaitForEvent) + subscription spec to Repository.
   - Sends a stop signal to the track (or the track is already done at this point).
3. Track's goroutine exits via `TrackEnded` (clean) or `TrackWaitForEvent` final state if the track may be reused.
4. Instance may itself exit its event loop if no other tracks are active — instance becomes idle.
5. When trigger arrives:
   - **Same runtime, instance still in memory:** Instance receives `ExternalSignal` from EventHub. Instance reads token state from Repository, transitions token `WaitForEvent → Alive`, spawns a fresh track at the wait node.
   - **Different runtime / restart:** runtime queries Repository for instances with matching wait subscriptions. Rehydrates Instance. Then spawns the track.

**Critical invariant:** the `TokenWaiting` event MUST be persisted before the track goroutine exits. Otherwise a crash between exit and persist loses the wait. Mechanism: Instance's event loop processes `TokenWaiting` synchronously, persists, then acknowledges back to the track (or simply requires the track to wait for `ctx.Done()` post-emit).

### 4.11 Runtime start / restart recovery

```
Runtime starts
  ├─> Query Repository for in-flight Process Instances
  ├─> For each:
  │     ├─> Instantiate Instance, restore its persisted state into in-memory registries
  │     ├─> Re-subscribe to wait triggers via EventHub
  │     └─> Spawn Instance.loop() goroutine
  └─> Done — instances are ready to receive external signals
```

Tracks are NOT spawned during rehydration unless their tokens are in `Alive` state with a continuation flow to follow. `WaitForEvent` tokens have no track until their trigger arrives.

Recovery MUST be:
- **Bounded** — recovery time scales with in-flight instance count, not historical event count.
- **Idempotent** — rerunning rehydration produces the same in-memory state.
- **Observable** — emits recovery events through `Logger` / `Tracer` per SAD-001 §11.

## 5. Consequences

### 5.1 Pros

- **Faithful to BPMN vocabulary.** Token is the spec's concept; track is the implementation primitive that drives it. Implementation reads like the spec, with the spec/impl boundary explicit.
- **Native to Go.** Goroutines + channels + contexts. No actor library, no scheduler, no framework code.
- **Two event streams, two consumers.** Token events serve BPMN-observable concerns (persistence, audit, conformance verification). Track events serve implementation-observable concerns (diagnostics, profiling). Each subscriber gets the granularity it needs.
- **No locks on instance state.** Single-owner mutation in Instance event loop; race detector (now in CI) catches accidental sharing.
- **Persistence + distribution are additive.** Same execution model; pluggable Repository, pluggable WorkerDispatcher per SAD-001 §11 / §13.
- **Reuses existing structural decisions.** `track`, `stepInfo`, `trackState`, `stepState` from the current codebase carry forward unchanged.

### 5.2 Cons

- **Active goroutine count = sum of active tracks across all instances.** Bounded by BPMN structure but unbounded in pathological models. Mitigation: implementations MAY apply per-instance or engine-wide concurrency caps via semaphore as an optimization — doesn't change the conception.
- **Channel discipline is mandatory.** Every track MUST emit a final track event (Ended / Failed / Merged / Canceled) before its goroutine exits. Forgotten exit = leaked goroutine. Mitigation: `defer t.cleanup()` pattern; tests assert `runtime.NumGoroutine()` returns to baseline.
- **Long-wait/rehydration has subtle invariants.** `TokenWaiting` MUST be persisted before track goroutine exits. Mechanism described in §4.10 makes this explicit but it MUST be enforced in code review.
- **No fairness guarantee between tracks.** Go's runtime scheduler determines execution order. Tests of token-flow MUST NOT depend on goroutine scheduling order.
- **Fork creates real goroutines.** Heavy parallel models spawn lots of goroutines. Go can handle this (M:N runtime), but observability needs to surface the count.

### 5.3 Implications for adjacent decisions

- **ADR-002 Extension Architecture** (planned): `Repository`, `Logger`, `Tracer`, `MetricsRecorder` interfaces consumed by Instance; Instance hands references to tracks at construction.
- **ADR-003 Module Layout** (planned): Instance / track / token types stay in `internal/instance/`; Engine façade in `pkg/thresher/`; extension interfaces in `pkg/` (exported).
- **ADR-004 Runtime Environment Contract** (planned): runtime layer hosts Repository implementation; runtime bridges external HTTP/gRPC trigger arrivals to `EventHub` → Instance `ExternalSignal` channel.
- **SAD-001 §13 Distribution & Scale** (preliminary): instance-level distribution = Instance owns one runtime node at a time, persistence-based handoff for migration. Task-level remote execution = `WorkerDispatcher` interposed at the track level when executing a remoteable Activity.

## 6. Conception vs Current Code — Deliberate Departures

This ADR is conception-first. Where the current code disagrees with the conception, code changes follow. The following departures are deliberate; the code audit phase (post-ADR-acceptance) plans the per-file changes.

| Topic | Current code | This ADR | Required change |
|---|---|---|---|
| Fork mechanics | `token.split(n)` keeps new tokens on the **same track** (`tt[i] = newToken(t.inst, t.trk)`) | Fork: parent track continues on F1; N−1 new tracks created for F2…FN, each constructed as `(startNode, token)` | Refactor `token.split` semantics. Add `Instance.SpawnTrack(startNode, token) → *track` (or equivalent) that handles construction + registration + goroutine start. Survivor parent track just advances normally. |
| Track:token cardinality | `token.split` produces multiple tokens that share a track ref (`t.trk`) | **1:1 always** — every track owns exactly one token throughout its lifetime; tokens never migrate between tracks | Remove `token.trk` mutation paths. New tokens at fork are bound to NEW tracks at the new tracks' construction, not to the parent's. |
| Instance ↔ token ownership | Instance holds tokens directly (`addToken`, `tokenConsumed`); token holds back-ref to Instance (`t.inst.tokenConsumed(t)`) | Instance holds **tracks only**; tokens are reached via `track.Token()`. No direct Instance ↔ token relationship. | Remove `Instance.addToken` / `Instance.tokenConsumed` (or repurpose as private internals called from track-level events). Remove `token.inst` back-ref. Token consumption is signaled to Instance via `TokenConsumed` event on the token stream, not via direct method call. |
| Join mechanics | Existing `TrackMerged` state exists but coordination is ad-hoc | First-arrived track survives and continues; all other arriving tracks end (`TrackMerged`); their tokens enter `Consumed`. No new track at join. | Make Instance the single coordinator of join arrivals. Formalize "first-arrived survives" as the universal join contract. |
| Token states | `TokenAlive` / `TokenWaitForEvent` / `TokenConsumed` (3 active states + Invalid) | Add `TokenWithdrawn` | Add `TokenWithdrawn` to `TokenState`. Update `Validate()` and `String()`. Wire withdrawn-path in Event-Based Gateway race handling. |
| Instance state machine | Not explicit (Instance is a struct; lifecycle implicit in method calls) | `InstanceCreated → InstanceActive → InstanceCompleting → InstanceCompleted` + failure / termination branches | Add an `InstanceState` enum; transitions emitted as InstanceEvent stream parallel to TrackEvent. |
| Event taxonomy | Implicit — `ProcessEvent` on track + ad-hoc state updates | Two explicit streams: `TokenEvent` (BPMN-observable, persistence subscribes) + `TrackEvent` (implementation-observable, diagnostics subscribes) | Define both event interfaces; refactor `ProcessEvent` callers to emit typed events; route through Instance's event loop. |
| Instance event loop | Not present in the same form (Instance is reactive — methods called from outside) | Instance runs an explicit `loop()` goroutine selecting on TokenEvent / TrackEvent / ExternalSignal / ctx.Done() | Add `Instance.loop()` and channel topology. Existing API methods enqueue events instead of mutating directly. |
| Long-wait release | Track may remain in `TrackWaitForEvent` state (goroutine held) | Track ends; goroutine released; persistence checkpointed before exit | Refactor `TrackWaitForEvent` final-state behavior. Define explicit pre-exit checkpoint contract. |
| Fork/join at activities | Implicit — handled via flow-checking logic | Explicit — fork/join can occur at any FlowNode with N>1 outgoing/incoming flows, not just gateways | Document the activity-as-fork-point case in the new event handling; reuse the gateway path. |
| track-token link design | Current implementation is cumbersome (token holds inst + trk; multiple cross-references) | Clean: track owns token; token has no Instance back-reference | Comprehensive rewrite of `token` and `track` structures during the implementation phase. May warrant a dedicated SRD. |

Each departure becomes a targeted SRD-class change after this ADR flips to Accepted. The code audit (next phase) maps each change to file-level scope.

## 7. Verification

How we'll know the implementation matches the conception:

| What | How |
|---|---|
| **Race-freedom** | All tests run with `-race` (enforced in `check.yml` per `chore/ci-audit`). Any data race on Instance state is a CI-blocking failure. |
| **Goroutine-leak-free** | Test helper asserts `runtime.NumGoroutine()` returns to baseline after instance completion. Applied to every integration test. |
| **Fork creates new tracks** | Test: process with Parallel Gateway split into 3 branches. Assert: post-fork, Instance has 3 active tracks, each with one token; parent track ended. |
| **Join consumes tokens correctly** | Test: process with Parallel Gateway merge of 3 incoming flows. Assert: TokenMerged event emitted with 3 consumed token IDs; exactly one continuation track spawned. |
| **Withdrawn semantics** | Test: Event-Based Gateway with 3 catching events; first arrival wins. Assert: 2 sibling tokens transition to TokenWithdrawn; their tracks end with TrackCanceled. |
| **Restart recovery** | Integration test: start instance → checkpoint mid-execution → kill Instance goroutine → spawn fresh Instance via `Rehydrate(instanceID)` → assert execution resumes from checkpoint with no duplicated side-effects. |
| **Long-wait release** | Test: start UserTask, assert track goroutine exits within N ms after `TokenWaiting`; assert `runtime.NumGoroutine()` drops. Trigger task completion; assert fresh track spawns and instance progresses. |
| **Termination cascade** | Test: instance with multiple active tracks; trigger Terminate End Event; assert all track goroutines exit within N ms; assert no persistence writes after termination. |
| **Boundary interrupt isolation** | Test: instance with track A executing an Activity with interrupting boundary Timer + track B running a parallel Activity. Advance time past timer. Assert: track A canceled, track B unaffected. |
| **TokenEvent stream completeness** | Test: process with known token transitions; subscribe to TokenEvent stream; assert all expected events received in valid order. |

**Acceptance gate for this ADR** (Draft → Accepted): the verification tests above MUST exist and pass against the implementation. Until then the ADR remains Draft.

## 8. References

- [SAD-001 Vision & Architecture](SAD-001-vision-and-architecture.md) — §6 Quality Attributes; §10 Execution Model overview (this ADR refines); §11 Extension interfaces; §13 Distribution & Scale (preliminary)
- [docs/bpmn-spec/state-machines/activity-lifecycle.md](../bpmn-spec/state-machines/activity-lifecycle.md) — normative Activity state transitions this ADR's persistence checkpoint policy aligns with
- [docs/bpmn-spec/state-machines/process-lifecycle.md](../bpmn-spec/state-machines/process-lifecycle.md) — Process instantiation and termination conditions
- [docs/bpmn-spec/semantics/token-flow.md](../bpmn-spec/semantics/token-flow.md) — token-flow rules driving Token spawn/exit decisions
- [docs/bpmn-spec/semantics/gateways.md](../bpmn-spec/semantics/gateways.md) — gateway evaluation rules (fork/join semantics this ADR implements)
- [docs/bpmn-spec/semantics/end-events.md](../bpmn-spec/semantics/end-events.md) — termination triggers
- [docs/bpmn-spec/semantics/event-handling.md](../bpmn-spec/semantics/event-handling.md) — interrupting vs non-interrupting handlers
- Existing code: `internal/instance/instance.go`, `internal/instance/track.go`, `internal/instance/token.go` — the structural starting point this ADR formalizes and selectively departs from

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.2 | 2026-05-29 | Ruslan Gabitov | Initial Draft (in-cycle supersession of a v.1 sketch that pre-dated reading the existing `internal/instance/` code). Pre-acceptance iteration ongoing; amendments folded into this Draft without per-round history rows (per project doc-history discipline — history is for version snapshots, not brainstorming). When v.2 flips to Accepted, this row records the Accepted state. |
