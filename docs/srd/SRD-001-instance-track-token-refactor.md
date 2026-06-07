# SRD-001 — Instance / Track / Token Runtime Refactor

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-06 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-001 v.3 Execution Model](../design/ADR-001-execution-model.md) |
| Refines | [SAD-001 v.1 §10 Execution Model](../design/SAD-001-vision-and-architecture.md) |

This SRD specifies the **Group-A** landing that brings `internal/instance/` to the two-layer runtime defined in [ADR-001 v.3](../design/ADR-001-execution-model.md): a `track` is the operational thread of execution; `token` becomes a **logical projection** of a track's current step rather than a stored type; instance state is mutated only inside a single event-loop goroutine. Completing this landing closes ADR-001 v.3's §7 acceptance gate.

## 1. Background & motivation

### 1.1 Current state (verified against the code)

The current `internal/instance/` implements the three-layer model ADR-001 v.3 supersedes:

- **`token` is a stored, cross-linked object** — `type token struct { inst *Instance; trk *track; …; prevs []*token; nexts []*token; state TokenState }` (`token.go:36`). It back-references both the Instance and its track.
- **Instance owns a token registry** — `tokens []*token` (`instance.go:101`) under a `sync.RWMutex` (`instance.go:103`); `newToken` self-registers via `inst.addToken` (`token.go:67`, `instance.go:440`).
- **Lower layer drives instance lifecycle** — `token.updateState` calls up into `t.inst.tokenConsumed(t)` when a token is consumed (`token.go:81`, `instance.go:452`); `tokenConsumed` scans the token registry for any `TokenAlive` and stops the instance when none remain.
- **Fork keeps tokens on one track, then reassigns** — `token.split(n)` creates N tokens all bound to the same track (`newToken(t.inst, t.trk)`, `token.go:89–95`); `checkFlows` then reassigns N−1 of them to freshly-created tracks (`track.go:454–512`, split called at `track.go:470`).
- **A track has no single token** — the current token lives in `stepInfo.tk` (`track.go:140–142`); the track is `type track struct { …; steps []*stepInfo; m sync.RWMutex; state trackState }` (`track.go:148`).
- **State mutation is lock-based and reactive** — tracks call Instance methods directly (`addTrack`, `track.go` callers) guarded by `RWMutex`.
- **Lineage is duplicated** — on the track (`track.prev`) and on the token (`token.prevs/nexts`).
- **Token state enum** — `TokenInvalid / TokenAlive / TokenWaitForEvent / TokenConsumed` (`token.go:15–22`); no `Withdrawn`.

### 1.2 Why change

[ADR-001 v.3 §3.1/§6](../design/ADR-001-execution-model.md) records the decision: at 1:1 with no migration, the stored token only duplicates the track (forced back-references, a second registry, a duplicate lineage chain) and the lock-based reactive model is the race-prone class the project is moving away from (cf. FIX-001). The refactor removes the token type, makes the token a projection, and serializes all instance-state mutation through a single event-loop goroutine.

## 2. Goals & scope

### 2.1 Goals (in scope)

G1. `token` is no longer a stored entity — it is a value **projected** from a track's current step.
G2. A `track` owns exactly one position+state; exposes it via `track.Token()`.
G3. The Instance holds a **track registry only** — no token registry, no `token→Instance`/`token→track` back-references.
G4. Instance state is mutated **only** inside a single event-loop goroutine; tracks report progress as events on a channel; no locks on instance state.
G5. **Fork** constructs a new track per additional active flow (parent continues on the first flow); lineage on `track.prev`.
G6. **Instance completion** is decided from the track registry (all tracks ended), not by scanning token states.
G7. Token **state** is a projection of track/step state (+ a track end-reason).
G8. **No external behavior change** for currently-implemented elements: None Start/End events, Service/User tasks, Exclusive gateway, sequence flows (incl. conditions/default), and timer events continue to execute as today (existing tests + `examples/*` pass unchanged).
G9. The Instance can report the **token-flow path history** — the ordered node sequence each token traversed — reconstructed from tracks (live and ended) and their steps.
G10. Steps carry **timestamps**, so the path history (G9) is a timeline (per-node entered/left, duration). Time comes from an injectable seam, forward-compatible with the ADR-002 `Clock` extension (not built here).

### 2.2 Non-goals (explicitly deferred)

N1. **Persistence / rehydration** — Scope/timer/compensation/error/activity state, the per-node state contract, checkpoints, restart recovery → dedicated Persistence & State ADR (see ADR-001 v.3 §4.7).
N2. **Synchronizing joins and the join seam** (Parallel / Inclusive / Complex policies, the `JoinController` / `JoinerContext` seam, OR-join semantics) → the gateway SRD, **built together with the first synchronizing consumer**. *In scope here:* only the **non-synchronizing pass-through** merge (FR-8) — a node with multiple incoming flows is handled by per-track execution; each arriving track continues independently. *Deferred (revised at landing):* the join seam itself (originally FR-12) — building a seam with only a fake consumer was dropped in favour of building it with its real consumer in the gateway SRD. The §4.2 policy/mechanism design is retained as forward conception. See §7 Implementation Summary.
N3. **Event-Based Gateway** and a `Withdrawn` **producer** → gateway SRD. The token-state projection (G7) defines `Withdrawn`, but nothing produces it in this landing.
N4. **New BPMN elements** (Parallel/Inclusive/Complex gateways, sub-processes, message correlation, etc.).
N5. **Public API promotion** — `Token` / `GetTokens` stay in `internal/instance/`; moving execution types to `pkg/` is ADR-003 module-layout work.

## 3. Functional requirements

| # | Requirement | Acceptance |
|---|---|---|
| FR-1 | The stored `token` type is removed; no struct holds `inst`/`trk` back-references or a token `state` field. | `grep` shows no `token` struct with Instance/track back-refs; build passes. |
| FR-2 | `track` owns its single position+state and exposes `track.Token() Token`, where `Token` is a computed read-model (node position, projected state, lineage). | `track.Token()` returns the current step's projection; the value is computed, not stored as a cross-linked object. |
| FR-3 | `Instance` holds only a track registry and exposes `Instance.GetTokens() []Token` projected from active tracks. No `tokens []*token`, `addToken`, or `tokenConsumed`. | Those identifiers are gone; `GetTokens()` returns one projection per active track. |
| FR-4 | Instance state (track registry, lifecycle) is mutated only inside `Instance.loop()`; tracks communicate via a channel; no `sync.RWMutex` guards instance state. | Code review + `-race`: instance-state fields written only in `loop()`. |
| FR-5 | A fork at a node with N>1 active outgoing flows: the executing track continues on the first flow; one new track is constructed per remaining flow with `track.prev` lineage; `token.split` is removed. | Fork test (FR mapped to §5): N independent tracks, each its own position; parent did not end at the fork. |
| FR-6 | The instance completes (`InstanceCompleted`) exactly when **no active track remains** — decided in the event loop from the track registry. Ended tracks are retained as history (FR-10), so completion keys off *active* tracks, not record deletion. | Completion test passes; no token-alive scan exists. |
| FR-7 | Token state is a pure projection: `Alive` ← track ready/executing; `WaitForEvent` ← `TrackWaitForEvent`; `Consumed` ← `TrackEnded`/`TrackMerged`; `Withdrawn` ← `TrackCanceled` + end-reason=withdrawn. | Projection unit test over each track/step state. |
| FR-8 | Non-synchronizing merge preserved: a node with N>1 incoming flows lets each arriving track continue independently (no wait, no consumption at the node). | Merge test: 3 tokens through an Exclusive/uncontrolled merge → 3 continuations. |
| FR-9 | No regression for implemented elements (G8). | Full existing test suite + `examples/*` build and pass; `make ci` green. |
| FR-10 | Each track records its step-state transitions in an **append-only list** (record, don't overwrite). `Instance` exposes a **token-flow path history** (e.g. `TokenHistory()`) **derived** from those lists across all tracks (live and ended), stitched by `track.prev` lineage — path, current position, and terminal state are all read from the entries. | For a forked process, the history shows the shared pre-fork path and one path per branch, each with correct parentage and terminal state. |
| FR-11 | Each step-state-update entry carries a **timestamp**, so timing (per-node entered/duration) is derived from the same list. The time source is an **injectable seam** (deterministic in tests), forward-compatible with the ADR-002 `Clock` extension (not built here). | History timestamps are monotonic non-decreasing; under an injected fixed/fake clock the values are deterministic. |
| ~~FR-12~~ | **DEFERRED to the gateway SRD** (revised at landing — N2 / §7). The `JoinController`/`JoinerContext` seam is built with its first synchronizing consumer rather than with a fake one. Group A delivers only the non-synchronizing merge (FR-8). | n/a — superseded. |

**Non-functional**

| # | Requirement | Acceptance |
|---|---|---|
| NFR-1 | Race-free under the detector. | `make ci` (race-gated) green; `go test -race -count=N` stress on fork/instance tests clean. |
| NFR-2 | Goroutine-leak-free. | Helper asserts `runtime.NumGoroutine()` returns to baseline after instance completion. |
| NFR-3 | Coverage not reduced for `internal/instance`. | Post-refactor coverage ≥ pre-refactor for the package. |

## 4. Design & implementation plan

### 4.1 Target shapes (illustrative)

```go
// Token — a computed read-model, never stored as a live cross-linked object.
type Token struct {
    Node  flow.Node    // current flow position (node pointer, from the snapshot)
    State TokenState   // projected from track/step state
    // lineage exposed via track.prev projection
}

// The track records each step-state transition as an entry in an ordered,
// append-only list (record, don't overwrite). Token projection, path history,
// and timing are ALL derived from these lists — there are no separately
// maintained projection structures to keep in sync.
type stepUpdate struct {
    Node  flow.Node   // the node executed at this step (pointer; the snapshot holds all nodes)
    State trackState  // track state at this transition (the token-state projection source)
    At    time.Time   // from the injectable time seam (→ ADR-002 Clock later)
}
// each track holds: updates []stepUpdate  +  track.prev (fork lineage). Track owns this
// list (Option B); live current-position reads are served by a lock-free atomic snapshot.

// track→loop events (the loop's contract) carry the track + node POINTER, not a string ID.
// Built in Group A:
//   Fork(track, active []*flow.SequenceFlow)   // evFork — loop builds one new track per extra flow
//   Ended(track)                               // evEnded — track's run() returned
// Deferred:
//   JoinArrival(...)  → gateway SRD (synchronizing joins)
//   Wait(...)         → persistence ADR (long-wait release)

func (t *track) Token() Token          // derived: latest update (position + projected token state)
func (i *Instance) GetTokens() []Token // derived: latest update of each ACTIVE track
func (i *Instance) TokenHistory() ...  // derived: the update lists of all tracks (live + ended),
                                       // stitched by track.prev — caller reads path, timing,
                                       // and terminal state straight from the entries

// Join policy seam (§4.2) — DEFERRED to the gateway SRD (revised at landing,
// see §7). The design below is retained as forward conception: the loop owns
// the mechanism (accounting + action); a join node supplies the policy as a
// PURE method; the Instance implements JoinerContext, handing the policy FACTS
// (not decisions). It is built with its first synchronizing consumer, not now.
type JoinerContext interface {        // implemented by the Instance; grows as policies need
    JoinNode() flow.Node              // the join being evaluated
    Arrived() []*flow.SequenceFlow    // incoming flows currently holding a token
    // (future: live-track positions / history, for the Inclusive reachability rule)
}
type JoinVerdict struct {
    Fire     bool                  // false => keep waiting
    Consumed []*flow.SequenceFlow  // incoming flows consumed when firing
}
type JoinController interface {        // optional; synchronizing nodes implement it
    EvaluateJoin(ctx JoinerContext) JoinVerdict
}
// loop: node implements JoinController -> consult; else -> fire immediately (pass-through)

type Instance struct {
    ctx    context.Context
    cancel context.CancelFunc
    trackEvents chan trackEvent   // tracks -> loop()
    external    chan ExternalSignal
    tracks map[string]*track      // mutated ONLY in loop()
    state  InstanceState
    // no tokens[]; no RWMutex on instance state
}
```

### 4.2 Fork & join mechanics (event-driven)

Both fork and join are reactions of `Instance.loop()` to events emitted by tracks — a track never mutates the instance registry itself.

**Fork** — a node with N>1 active outgoing flows (Parallel/Inclusive split, or an activity with multiple outgoing flows = uncontrolled split):

1. The executing track determines its active outgoing flows F1…FN (selecting *which* flows are active is the gateway/node's job; the mechanics below act on the already-selected set) and emits one **fork event** carrying them plus its own ID (for lineage).
2. The track advances its own position onto F1's target and continues — it does **not** end at the fork.
3. `loop()` receives the event and, for each remaining flow F2…FN, constructs a new track at that flow's target with `track.prev` = the forking track, registers it, and spawns its goroutine.
4. Result: N tracks run independently (original on F1 + N−1 new), each with its own position and goroutine.

**Join** — a node with N>1 incoming flows:

1. A track arriving at a join emits a **join-arrival event**. For a non-synchronizing merge it then simply continues; for a synchronizing join it ends its goroutine pending the loop's decision.
2. `loop()` coordinates by merge type:
   - **Non-synchronizing** (Exclusive merge, uncontrolled merge) — the only kind in Group-A scope: no wait, no consumption; each arriving track continues on the outgoing flow independently (several tracks may legitimately cross the same node).
   - **Synchronizing** (Parallel/Inclusive) — out of Group-A scope (gateway SRD): the loop holds arrivals until the expected set is complete, then one track continues and the others end (`TrackMerged`).
3. No new track is created at a join — continuation always rides an arriving track.

**Join coordination ownership.** Flow synchronization at a join is the **Instance's** responsibility, not the node's. Per-join runtime state (which incoming flows have arrived) — needed only by synchronizing joins — lives in `loop()`, keyed by node ID, mutated only there (single-writer, lock-free). The node definition stays **immutable and shared** across instances and tracks, so it never holds arrival state — putting it on the node would reintroduce the cross-track shared-mutable-state race this design removes. The join-arrival event is the attachment point where synchronizing-join logic (gateway SRD) will plug in.

**Policy vs mechanism — who decides the join.** The Instance owns the *mechanism*: it holds the cross-track arrival accounting and performs the action (continue one track, merge/end the others). The *policy* — "is the awaited set complete, and which arrivals are consumed?" — belongs to the join **node** (its gateway type/config), because only the node knows the join semantics and only the process graph defines the expected incoming set. The arriving track cannot decide (it knows only itself; it just reports). So on each join-arrival, `loop()` **consults the node through a pure, read-only `JoinController.EvaluateJoin`**, handing it a `JoinerContext` the Instance implements — **facts only**: the arrived incoming flows now, plus live-track positions / history for the Inclusive rule later. The policy computes a verdict — *wait*, or *fire* with the consumed flows — and the loop acts on it. Facts, not decisions: the ambiguous Inclusive reachability is computed inside the policy from those facts, never by the loop. The node stores nothing; it is a pure policy evaluated against state the Instance supplies (node immutability preserved). Consequently the expected count is **not** hardcoded in the Instance: for a Parallel join it is the node's static incoming flows; for an Inclusive join the node's policy derives it from the live execution.

**Deferred at landing (§7):** the join seam — the `JoinController` interface, the consult-or-default mechanism, and per-join accounting — is **not** built in Group A. With no synchronizing consumer yet (Parallel/Inclusive are gateway-SRD), it is built there, with its first real consumer, rather than with only a fake test now. Group A's non-synchronizing merges work via per-track execution (FR-8). The generic design (`arrivals + context → wait | fire(consumed)`) above stands as the forward conception; building it later does not pull in the deferred OR-join semantics — those live inside a future policy implementation.

### 4.3 Per-area changes

- **`token.go`** — remove the stored `token` struct (back-refs, `state`, `prevs/nexts`), `newToken`'s `addToken` call, `updateState`'s `tokenConsumed` call, and `split`. Keep `TokenState` as the projected enum (add a `Withdrawn` value, no producer yet). Add the `Token` projection value.
- **`track.go`** — the track owns its position+state directly; add `Token()` projecting the current step; add a track **end-reason** (to distinguish withdrawn vs canceled, FR-7); rework `checkFlows` to **emit a fork event** instead of `split`-then-reassign — the loop constructs the new tracks (§4.2, FR-5); lineage stays on `track.prev`; record each step-state transition as a timestamped entry in an append-only list (record, don't overwrite — FR-10/11), stamped from an injectable time seam (→ ADR-002 `Clock`) — `Token()` / `GetTokens()` / `TokenHistory()` and all timing derive from these lists; remove the instance-state `RWMutex` usage in favor of emitting events.
- **`instance.go`** — remove `tokens[]`, `addToken`, `tokenConsumed`; add the `loop()` goroutine + channel topology (FR-4) as the sole state mutator and the **fork/join coordinator** (§4.2); convert `addTrack`/track-spawn/fork/join-arrival/completion into events applied in `loop()`; decide completion from the track registry (FR-6); add `GetTokens()` and `TokenHistory()` (FR-10). **Retain ended tracks** (their step-update lists + lineage + end-reason) for the instance's lifetime so history can be reconstructed; the registry distinguishes active vs ended, and completion (FR-6) keys off active tracks, not record deletion. (Unbounded retention under loops / multi-instance is a later concern — those elements are out of scope here; durable/bounded history belongs to the Persistence & State ADR.)
- **Mocks** — regenerate via `make gen_mock_files` if any mocked interface signature changes.

### 4.4 Milestones (each independently buildable and CI-green)

1. **M1 — infra: time seam + test helpers.** Injectable internal time seam (→ ADR-002 `Clock` later); fake clock + goroutine-leak assert helper. (FR-11 source.)
2. **M2 — Instance event loop + serialized mutation.** `Instance.loop()` + channel topology becomes the sole mutator of instance state and the fork coordinator; existing fork path rerouted through an event (still `split`-based here); drop `RWMutex` on instance state. (FR-4)
3. **M3 — step-state list + projections.** Append-only step-state-update list with timestamps; `track.Token()` / `Instance.GetTokens()` / `TokenHistory()` derivations + token-state projection, coexisting with the current token type. (FR-2, 7, 10, 11)
4. **M4 — fork rework.** The fork-event handler in `loop()` constructs one new track per extra active flow (parent continues on F1, `track.prev` lineage); remove `token.split`; new tracks self-create their token on execution (token type still present). (FR-5)
5. **M5 — token-type removal.** Remove the stored `token` type + `Instance.tokens` / `addToken` / `tokenConsumed` / `token.inst` / `trk`; token state purely via the M3 projection; completion decided from active tracks. (FR-1, 3, 6)
6. **M6 — acceptance verification.** Full ADR-001 v.3 §7 suite + non-synchronizing merge pass-through (FR-8) + race-stress (`-count=N`) + examples + coverage; `make ci` green. (FR-9, NFR-1..3)

Sequencing rationale: stand up the event loop first (M1/M2) so the ownership changes (M4/M5) land on the serialized-mutation foundation rather than racing the old lock-based paths; projections (M3) precede token-type removal (M5) so the projection is the source of token state before the type is deleted.

## 5. Verification (Definition of Done)

Maps to [ADR-001 v.3 §7](../design/ADR-001-execution-model.md):

| Test | Asserts |
|---|---|
| Race-freedom | `-race` CI-gated; instance state mutated only in `loop()`; `-race -count=N` stress on fork/instance clean. |
| Goroutine-leak-free | `runtime.NumGoroutine()` returns to baseline after completion. |
| 1:1 fork | 3-way split → 3 independent tracks, each own position; parent continued on the first flow. |
| No token registry | Instance exposes tokens only via `GetTokens()`; no `tokens` field; `track.Token()` reflects the current step. |
| Instance completion | `InstanceCompleted` iff all tracks ended (no token-alive scan). |
| Non-synchronizing merge | 3 tokens through Exclusive/uncontrolled merge → 3 continuations; none consumed/merged at the node. |
| Token-state projection | each track/step state maps to the expected `Token.State`. |
| Termination cascade | Terminate End Event → all track goroutines exit via `ctx.Done()`. |
| Token path history | After a forked run completes, `TokenHistory()` returns the shared pre-fork path and one path per branch, each with correct lineage and terminal state. |
| Step timing | History timestamps are monotonic non-decreasing; under an injected fake clock the per-node entered/left values are deterministic. |
| No regression | existing `internal/instance` + engine tests and `examples/*` pass unchanged. |

**DoD:** all FR/NFR satisfied; the table above green; `make ci` green; coverage ≥ baseline. Completing this DoD closes ADR-001 v.3's §7 acceptance gate — flipping ADR-001 Draft → Accepted is a **separate** follow-up (not part of this SRD).

## 6. Risks & regressions

- **Event-loop rewrite is invasive.** Risk of deadlock or leaked goroutines. Mitigation: `defer` cleanup emitting a terminal track event; NFR-2 leak test; staged milestones (M1/M2 before M3/M4).
- **Completion-semantics change** (track-registry vs token-alive scan). Risk: instances that never complete or complete early. Mitigation: FR-6 test + existing `TestMonitoring` (`instance_test.go`) must still pass.
- **Event handling during waits.** Current waiting tracks use `ProcessEvent`; the event-loop respawn model (ADR-001 §4.7) must preserve EventHub signal delivery for timer/message waits. Mitigation: timer-event tests + `examples/simple-timer`, `examples/timer-event` pass.
- **Time source.** Using `time.Now()` directly is untestable and bypasses the future `Clock` extension. Mitigation: a single injectable internal time seam now (deterministic tests), to be swapped for the ADR-002 `Clock` extension in WS-B — do not scatter `time.Now()` calls.
- **Mock/interface churn.** Mitigation: regenerate mocks; `tidy-check-all`.
- **Scope creep into deferred areas.** Synchronizing joins / persistence / new gateways are explicitly N-goals; keep them out.

## 7. Implementation summary

Landed on branch `refactor/instance-track-token` as the SRD doc commit plus
four implementation commits:

| Milestone | Commit | Delivers |
|---|---|---|
| SRD | `12c7829` | this document |
| M1 | `414a0ce` | injectable time seam + test helpers (fakeClock, leak assert) |
| M2 | `dbd1c43` | `Instance.loop()` sole-owner event loop; no locks on lifecycle state; state/track-count via atomics; completion via active-track count (FR-4, FR-6) |
| M3 | `c25e7a6` | append-only step-state list; `track.Token()` / `GetTokens()` / `TokenHistory()` projections + timestamps, lock-free (FR-2, 7, 10, 11) |
| M4+M5 | `f86d592` | fork rework (no `split`); removal of the `token` type, `Instance.tokens`, `addToken`, `tokenConsumed`, `stepInfo.tk` (FR-1, 3, 5, 8) |

**Result:** two-layer runtime (Instance + track); "token" is purely a derived
projection. Files: `internal/instance/{instance,track,token}.go` + tests
`{clock,leakcheck,m2_loop,m3_projection,m4_fork}_test.go`.

**Deviations from the spec (resolved at landing):**

- **M4 and M5 merged.** A working fork requires track-based completion, which
  requires removing the token-based stop (`tokenConsumed`), which cascades to
  deleting the whole token type — so the two milestones are one change-set.
- **FR-12 (join seam) deferred** to the gateway SRD (N2): a `JoinController`
  seam with only a fake consumer was dropped in favour of building it with its
  first synchronizing gateway. The in-scope non-synchronizing merge (FR-8)
  works via per-track execution. §4.1/§4.2 retain the design as forward
  conception.
- **`stepUpdate` records `trackState`** (the token-state projection source),
  not `stepState` as the §4.1 sketch first showed.
- **Events built: `evFork` + `evEnded`** only; `JoinArrival` (synchronizing
  joins) and `Wait` (long-wait release) are deferred to the gateway SRD /
  persistence ADR.
- **Added `inst.lastErr` / `LastErr()`** to record a fatal fork-construct error
  (not in the original sketch).
- **Pre-run `Token()` returns the zero projection** — the initial `TrackReady`
  seed was dropped; history is recorded once a track runs (so it uses the
  running clock and stays deterministic under an injected one).

**Verification (M6):** `make ci` green end to end — tidy, lint (all 6 modules),
build-all, race tests (all modules), govulncheck clean; fork/projection tests
race-stressed at `-count=20`; no regression (existing suite + `examples/*`).

**Status:** kept **Draft** on the branch. Per the project pattern (cf. FIX-001)
and the bilingual rule (translation on Accepted), flip Draft → Accepted and add
the Russian twin as a **post-merge** step.

## 8. References

- [ADR-001 v.3 Execution Model](../design/ADR-001-execution-model.md) — §2 decision, §4 component model, §6 departures (the per-symbol change list this SRD implements), §7 acceptance gate.
- [SAD-001 v.1 §10 Execution Model](../design/SAD-001-vision-and-architecture.md).
- [docs/bpmn-spec/semantics/token-flow.md](../bpmn-spec/semantics/token-flow.md) — fork/merge semantics.
- Deferred companions (out of scope): Persistence & State ADR (per ADR-001 v.3 §4.7); the gateway SRD (synchronizing joins / OR-join).

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-06 | Ruslan Gabitov | Initial Draft. Specifies the Group-A two-layer runtime refactor against ADR-001 v.3; persistence, synchronizing joins / OR-join, Event-Based Gateway, and new elements explicitly out of scope. |
| v.1 | 2026-06-07 | Ruslan Gabitov | Landed (M1–M6) and reconciled against the code: §7 Implementation Summary added; M4+M5 merged; FR-12 join seam deferred to the gateway SRD; `stepUpdate` records `trackState`; events `evFork`/`evEnded` (JoinArrival/Wait deferred); `LastErr` added. `make ci` green. Status remains Draft pending merge (Accepted + RU twin post-merge). |
