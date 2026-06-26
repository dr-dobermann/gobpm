# SRD-028 — Loop-owned token positions (ADR-017 outbound slice)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-26 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-017 v.1 Channel-based event processing](../design/ADR-017-channel-based-event-processing.md) §2 Rule 2 (outbound slice) |

This SRD lands the **outbound slice** of ADR-017: the per-instance **loop becomes the sole owner of
the token-position / join view** that reachability and joins consult. A track **emits** every
position move to the loop instead of leaving its position for the loop to read; the loop maintains
its own `position` and `parked-at-join` maps and the join machinery reads those maps, never another
track's live `currentStep()`/`state`. This removes the loop-reads-track-state cross-goroutine reads
**by construction** — the residual race class behind the Complex / OR-join transient spurious abort
that SRD-027 §3.8 cured only with a single-snapshot band-aid (which still cross-read track state).
The **inbound slice** (channel-park delivery — ADR-017 Rule 1) is SRD-027, already landed on this
branch.

---

## 1. Background & current state (verified against the code)

ADR-001 v.5 makes the per-instance **loop** the single writer of lifecycle state, and SRD-027 made
it the single **dispatcher** of inbound events. One seam still violates the single-writer model in
the *read* direction: when a reachability/Complex join is rechecked, the loop **reads other tracks'
mutable position and state cross-goroutine** to compute which nodes hold live tokens.

- **`joinPositions` scans every track's live state (the core read).** `joinPositions`
  (`internal/instance/reachability.go:83-105`) iterates `inst.tracks` and, per track, reads
  `t.inState(TrackMerged, TrackEnded, TrackCanceled, TrackFailed)` and `t.currentStep().node.ID()`
  — the position and liveness of tracks running on **other** goroutines. It feeds both the
  occupied-node set for reachability and the in-transit guard.
- **`recheckAwaitingJoins` scans for parked joins by reading state.**
  (`internal/instance/instance.go:997-1013`) iterates `inst.tracks`, reads
  `t.inState(TrackAwaitSync)` and `t.currentStep().node` of every track to find which reachability
  joins currently hold a parked token.
- **`recheckParked` reads the just-parked track's position.**
  (`internal/instance/instance.go:1020-1031`) `node := t.currentStep().node`.
- **`fireOrJoin` reads the survivor's position (logging).**
  (`internal/instance/instance.go:1089-1104`) `survivor.currentStep().node.ID()`.

All four reads are guarded by `t.m` (`track.go:188`, via `inState`/`currentStep`), so they are not
*data* races — but they **are cross-goroutine reads of another track's mutable state**, which is
exactly what ADR-017 Rule 2 forbids. Guarding them does not make the *view* consistent: a token
slipping from a branch (reachable) to the join (arrived-pending) between two such reads is what
produced the spurious "activation rule unsatisfiable" abort. SRD-027 §3.8 narrowed that to a single
`joinPositions` snapshot (`fixedFlowChecker`, `reachability.go:27-37`), but the snapshot is *still*
built by cross-reading every track — the band-aid removed the *double* read, not the read.

**The loop learns lifecycle, never position moves.** The eight `trackEventKind`s
(`internal/instance/event.go:67-100`) report fork / ended / awaiting / merged / parked / failed /
waiting / deliver — **no position move**. A single-flow advance appends a step in `checkFlows`
(`track.go:799-810`) and emits **nothing** (only an *extra* forked flow emits `evFork`,
`track.go:831-833`); an Event-Based-gateway arm advance appends in `advanceToArm`
(`track.go:996-1002`) and emits nothing. So today the loop has no choice but to read
`currentStep()` on demand.

**The live `FlowChecker` path is already dead.** After SRD-027, `recheckJoin` builds a
`fixedFlowChecker` from one `joinPositions` snapshot and passes **that** to every `j.Recheck(...)`
(`instance.go:1046-1082`). The only `FlowChecker.CheckFlows` call sites
(`pkg/model/gateways/inclusive.go:146`, `pkg/model/gateways/complex.go:319`) receive that fixed
checker. Nothing constructs the **live** `inst.CheckFlows` (`reachability.go:15-20`) →
`occupiedNodes` (`reachability.go:69-73`) path anymore; the `exec.FlowChecker = (*Instance)(nil)`
assertion (`reachability.go:143`) is the only thing keeping it compiled. It is dead surface this
slice removes (cf. the audit-stale-interfaces house rule).

**Track state is already near-local.** Post-SRD-027, `ProcessEvent` only `emit`s (it no longer
calls `updateState`/`record` — `track.go:898-905`, `instance.go:375-389`), so the "a synchronous
waiter writes `t.steps` from its own goroutine" concern noted at `record` (`track.go:196`) and
`checkFlows` (`track.go:805`) **no longer applies**. After this slice removes the four loop reads
above, `t.steps`/`t.state` are touched only by the track's own goroutine, plus the `parkCh`-mediated
merge handoff (`applyMerged`/`recheckParked` write `TrackMerged` then signal `parkCh`; the track
reads its state only after `<-parkCh` — a channel happens-before). That makes the `t.m` guard
reducible (§3.6).

ADR-017 (Draft) decided the structural fix. This SRD implements its **outbound** half.

## 2. Requirements

### Functional

- **FR-1 — The loop owns the token-position view.** `loop()` maintains a loop-local
  `position map[trackID]flow.Node` (the current node of every **live** track) and a loop-local
  `parked map[trackID]flow.Node` (the join node of every track parked at a reachability/Complex
  join, `TrackAwaitSync`). Both are loop-goroutine-only — no lock, like `waiting`/`msgIdx` (SRD-027).
  The loop **never reads another track's `currentStep()`/`inState()`** to build them.
- **FR-2 — A track emits its position moves.** Whenever a track advances onto a new node it emits an
  `evMoved` `trackEvent` **carrying that node** (the track knows it — it just appended the step on
  its own goroutine). The two move sites are `checkFlows` (the ordinary advance, `track.go:799-810`)
  and `advanceToArm` (the Event-Based-gateway arm advance, `track.go:996-1002`). The loop sets
  `position[track] = node` on `evMoved`. The **initial** position is seeded by the loop at `spawn`
  before the track's goroutine starts (a sequential construction-time read, not a concurrent one —
  §3.3).
- **FR-3 — The loop owns the parked-at-join view from `evParked`.** On `evParked` the loop records
  `parked[track] = position[track]` (the track moved onto the join via `evMoved` before it parked —
  FIFO on `inst.events` guarantees that order). A track leaves `parked` when it resumes and moves
  (`evMoved` clears it), is merged away (`evMerged`), or ends. `recheckAwaitingJoins` iterates
  `parked`, not `inst.tracks`.
- **FR-4 — `joinPositions` is a pure function over the loop-owned maps.** It derives the
  occupied-node set and the in-transit flag from `position`/`parked` only — no `inst.tracks` scan,
  no `currentStep()`, no `inState()`. Membership and timing are **identical** to today's snapshot
  (§4): occupied = every live track's node; in-transit = a track whose node is the join but which is
  not in `parked`.
- **FR-5 — `recheckParked` and `fireOrJoin` read the loop-owned position.** The parked track's join
  node comes from `parked[track]` (or `position[track]`); the survivor's node for logging comes from
  `position[survivor]`. Neither calls `currentStep()`.
- **FR-6 — Liveness comes from lifecycle events, not state reads.** A track is removed from
  `position` (and `parked`) when it dies: `evEnded` / `evFailed` (the track), and the absorbed ids on
  `evMerged`. `evAwaiting` (Parallel-join `TrackAwaitingMerge`) keeps the track in `position` (its
  token is still Alive at the join) but does **not** add it to `parked` (that is `TrackAwaitSync`
  only). This reproduces today's `joinPositions` dead-filter exactly (`reachability.go:89`).
- **FR-7 — The dead live `FlowChecker` is removed.** Delete `inst.CheckFlows`
  (`reachability.go:15-20`), `occupiedNodes` (`reachability.go:69-73`), and the
  `exec.FlowChecker = (*Instance)(nil)` assertion (`reachability.go:143`). `fixedFlowChecker`
  (built from the loop-owned occupied snapshot) stays the **only** `FlowChecker`; `checkFlowsWith` /
  `reachesOccupied` are unchanged.

### Non-functional

- **NFR-1 — No cross-goroutine track-state read remains in the loop.** After this slice, no loop-side
  function calls `currentStep()`/`inState()`/reads `t.steps`/`t.state` of a track other than via the
  loop-owned maps. Verified by an audit (grep) recorded in §10 and by `-race`.
- **NFR-2 — Reachability / join semantics byte-for-byte unchanged.** The OR-join death-trigger
  (SRD-022) and the Complex fire/abort (SRD-023) decide identically: the loop-owned occupied/
  in-transit view has the same membership and the same observable timing as today's `joinPositions`
  snapshot (§4 proves the equivalence). All existing gateway tests pass unmodified except those that
  call the removed/relocated internals directly.
- **NFR-3 — `t.m` reduced to what is still shared.** With `t.steps`/`t.state` now track-goroutine-
  local plus the `parkCh`-mediated merge handoff, the `t.m` guard on those fields is reducible
  (§3.6). This slice removes the guard **only** where the happens-before argument is explicit and the
  `-race` suite stays clean; anything not provably local keeps its guard (conservative).
- **NFR-4 — Diff coverage ≥ COVER_MIN (95%) on touched functions**, aiming 100%.

## 3. Models

### 3.1 `trackEvent` — `evMoved` kind and a `node` field (`internal/instance/event.go`)

Add one kind and one field:

```go
type trackEvent struct {
	track     *track
	node      flow.Node            // for evMoved: the node the track just advanced onto
	eDef      flow.EventDefinition
	flows     []*flow.SequenceFlow
	mergedIDs []string
	msgDefIDs []string
	kind      trackEventKind
}

const (
	evFork trackEventKind = iota
	// …existing kinds…
	evDeliver
	// evMoved: the track advanced onto a new node (node carries it). The loop updates its
	// own position view; it never reads the track's currentStep to learn the move.
	evMoved
)
```

`node` is carried **in the event** precisely so the loop need not read `ev.track.currentStep()` —
that read is the violation being removed. The loop keeps the `flow.Node` (not just its id) because
the join machinery type-switches on it (`node.(exec.ReachabilityJoin)` / `exec.ActivationJoin`) and
reads `node.ID()`. Field order keeps `govet`/`fieldalignment` happy (interface/pointer fields first).

### 3.2 Track emits on every move (`internal/instance/track.go`)

`checkFlows`, right after it appends the next step under `t.m` (`track.go:808-810`):

```go
t.m.Lock()
t.steps = append(t.steps, &nextStep)
t.m.Unlock()

// Report the advance to the loop, the sole owner of the position view (ADR-017 Rule 2).
t.instance.emit(trackEvent{kind: evMoved, track: t, node: nextStep.node})
```

`advanceToArm` emits the same after its append (the winning arm node). Both run only while the
instance is Active (`checkFlows`/`advanceToArm` are reached only from `run`/`deliver`), so — unlike
`evWaiting` — no construction-time gating is needed; `emit`'s `<-loopDone` arm still bounds the send.
The track does **not** emit for its *initial* node (it has no prior position to leave); the loop
seeds that at `spawn` (§3.3).

### 3.3 `loop()` — the position and parked maps (`internal/instance/instance.go`)

Two loop-locals next to `waiting`/`msgIdx`, owned by the loop goroutine (no lock):

```go
position := map[string]flow.Node{} // live trackID → current node
parked   := map[string]flow.Node{} // trackID → join node, for AwaitSync-parked tracks
```

`spawn` seeds the initial position before starting the run goroutine:

```go
spawn := func(t *track) {
	inst.tracks[t.ID()] = t
	inst.addToSnap(t)
	active++

	// Seed the initial position on the loop goroutine, BEFORE the run goroutine starts
	// (the `go` below). This read is sequential — the track has no other goroutine yet —
	// so it is not a Rule-2 cross-read; every later move arrives as evMoved.
	position[t.ID()] = t.currentStep().node

	if t.inState(TrackWaitForEvent) { … } // unchanged (SRD-027)
	go func(t *track) { … }(t)
}
```

`applyEvent` threads `position`/`parked` like `waiting`/`msgIdx` and updates them:

| event | position | parked |
|---|---|---|
| `evMoved` | `position[t] = ev.node` | `delete(parked, t)` (moving ⟹ not parked) |
| `evParked` | — | `parked[t] = position[t]` (the join node) |
| `evAwaiting` | keep (alive at join) | — (AwaitingMerge ≠ AwaitSync) |
| `evMerged` | `delete` absorbed ids | `delete` absorbed ids |
| `evEnded` / `evFailed` | `delete(position, t)` | `delete(parked, t)` |

`stopAll` clears both (like `waiting`/`msgIdx`). The recheck helpers (`recheckAwaitingJoins`,
`recheckParked`, `recheckJoin`, `fireOrJoin`) take `position`/`parked` as parameters — the same
loop-local-threading pattern `applyEvent`/`dispatchToParked` already use for `waiting`/`msgIdx`.

### 3.4 `joinPositions` — pure over the maps (`internal/instance/reachability.go`)

```go
// joinPositions derives the occupied-node set and the imminent-arrival flag for a join recheck
// from the loop-owned position/parked maps — no track is read. occupied = every live track's node;
// inTransit = a live token already on joinNode but not yet parked there (between its evMoved onto
// the join and its evParked). Identical membership/timing to the old cross-read snapshot.
func joinPositions(
	joinNode flow.Node,
	position, parked map[string]flow.Node,
) (occupied map[string]bool, inTransit bool) {
	occupied = make(map[string]bool, len(position))

	for id, n := range position {
		occupied[n.ID()] = true

		if joinNode != nil && n.ID() == joinNode.ID() {
			if _, isParked := parked[id]; !isParked {
				inTransit = true
			}
		}
	}

	return occupied, inTransit
}
```

`recheckJoin` calls `joinPositions(node, position, parked)` and builds `fixedFlowChecker{occupied}`
as today (`instance.go:1046-1051`); the in-transit defer is unchanged.

### 3.5 Remove the dead live `FlowChecker` (`internal/instance/reachability.go`)

Delete `inst.CheckFlows` and `occupiedNodes` (no caller after SRD-027 — §1) and the
`exec.FlowChecker = (*Instance)(nil)` assertion. `fixedFlowChecker`, `checkFlowsWith`, and
`reachesOccupied` stay. The `exec.FlowChecker` interface (`pkg/exec/exec.go:40`) is unchanged —
`fixedFlowChecker` still implements it.

### 3.6 `t.m` reduction (`internal/instance/track.go`)

After §3.1–§3.5 the loop no longer reads any track's `steps`/`state`. The remaining accessors are:

- `t.steps` — appended in `checkFlows`/`advanceToArm` and read in `currentStep`/`record`/`deliver`,
  **all on the track's own goroutine**. No other goroutine touches `t.steps`.
- `t.state` — written/read by the track's own goroutine (`run`/`synchronize`/`checkNodeType`/
  `trackEndKind`), plus the loop writing `TrackMerged` in `applyMerged`/`recheckParked` immediately
  **before** signalling `parkCh`; the track reads that state only after `<-parkCh`. The channel send
  establishes happens-before, so this hand-off needs no mutex.

So `t.m` is reducible. This slice takes the **conservative** path: drop the `t.m` guard from the
step accessors that are now provably single-goroutine (`currentStep`, the `record`/`deliver` step
reads, the `checkFlows`/`advanceToArm` appends), keep any guard whose locality is not provable, and
**gate the change on the `-race` suite staying clean** (NFR-1/NFR-3). The stale doc comments at
`record` (`track.go:196`) and `deliver` (`track.go:922`) — which justify the guard by "path()/
occupiedNodes read it concurrently" — are corrected: `occupiedNodes` is gone and `path()`/`Token()`
read the lock-free `hist` projection, never `t.steps`.

## 4. Analysis

**Why emit-on-move (chosen).** Reachability needs the current node of **every live token**, including
in-flight (not-parked) ones — an actively-moving upstream token is a potential arrival the join must
wait for. Most moves emit no event today (§1), so for the loop to own the occupied view it must learn
each move; ADR-017 §3 already accepts this ("the track's ordinary post-delivery `emit` reporting its
advance back to the loop … that outbound notify is unavoidable in any design"). Token-gathering has
the same need — the instance's live tokens *are* the per-track positions — so the loop-owned view is
the natural home for both (a forward synergy; this slice does not re-point `GetTokens`, which keeps
reading the lock-free `hist` projection — §5).

**Equivalence to the old snapshot (NFR-2).** Today `joinPositions` includes a track iff it is not in
`{Merged, Ended, Canceled, Failed}` and reads its `currentStep()`. The loop-owned `position` map
holds a track from its `spawn` seed until an `evEnded`/`evFailed`/`evMerged-absorbed` removes it —
i.e. exactly the not-dead set (Canceled only happens under `stopAll`, which clears the maps). Each
move updates the node via `evMoved`, and `inst.events` is FIFO, so the loop's view of a track's node
is its real node as of the last move it has drained — the same value `currentStep()` would return at
the moment the loop processes the triggering lifecycle event. The in-transit window (node = join,
not yet `parked`) is the interval between the track's `evMoved` onto the join and its `evParked`,
which the loop sees in that order — the same window `!inState(TrackAwaitSync)` detected. Membership
and timing therefore match.

**Alternatives considered.**

- **A — Emit-on-move, loop-owned `position`/`parked` maps (chosen).** Each advance emits `evMoved`;
  the loop derives occupied/in-transit from its own maps. Removes all four cross-reads and the dead
  live `FlowChecker`. Cost: one `emit` per node step (cheap — the loop only updates a map; node
  execution stays on the track goroutine).
- **B — Carry the position only in the existing lifecycle events.** Rejected: most advances emit no
  lifecycle event, so the loop would miss in-flight tokens between joins — precisely the tokens
  reachability must see. It cannot reconstruct the occupied set from lifecycle events alone.
- **C — Maintain positions only when the process graph has a reachability/Complex join.** A real
  optimisation (a parallel-only or join-free process never consults occupancy), but speculative —
  it conditions a hot path on a graph property to save a map write. Deferred behind a forward note;
  add only on measured cost (cf. the no-speculative-universality house rule).

## 5. Public API surface

**None.** `position`/`parked` are loop-locals; `evMoved`/the `node` field are package-internal.
`GetTokens` / `TokenHistory` (`instance.go:1126-1158`) are unchanged — they derive from the
lock-free `hist` snapshot, independent of the loop's position view. Re-pointing token-gathering at
the loop-owned view is a possible future simplification, explicitly **out of scope** here.

## 6. Test scenarios

- **T-1 — `evMoved` updates the loop position.** Drive a track through two nodes; assert the loop's
  `position[track]` follows each `evMoved` and that no `currentStep()` is read by the loop (the
  helper observes only the maps).
- **T-2 — `joinPositions` is pure over the maps.** Table test on the free function: given
  `position`/`parked` maps (no `*Instance`, no tracks), assert `occupied` and `inTransit` for: token
  on the join + not parked → in-transit; token on the join + parked → not in-transit; token
  elsewhere → occupied, not in-transit; empty maps → empty/false. Replaces
  `TestJoinPositionsInTransit` (`reachability_loop_test.go:11-30`), which built the state by mutating
  real tracks.
- **T-3 — OR-join fires identically (regression).** The SRD-022 diamond: assert fire timing and
  survivor/merged outcome are unchanged with the loop-owned view.
- **T-4 — `recheckAwaitingJoins` iterates `parked`.** With two tracks parked at one OR-join recorded
  in `parked`, a token death triggers exactly one `recheckJoin` for that node — no `inst.tracks`
  scan, no `inState` read.
- **T-5 — Complex fire/abort unchanged (regression).** SRD-023 scenarios
  (`TestComplexRequiredGate`, `TestComplexAbortOnDeath`, `TestComplexAbortInstance`): fire on
  satisfied rule, abort + deterministic terminate on unsatisfiable, under the new view.
- **T-6 — `-race` stress, no cross-read.** `pkg/thresher` under `-race` ×40 stays green (the SRD-027
  baseline), confirming the removed reads introduced no regression and the `t.m` reduction is clean.
- **T-7 — Dead `FlowChecker` removed.** Compile-time: `inst.CheckFlows`/`occupiedNodes`/the
  `(*Instance)` assertion are gone; `fixedFlowChecker` remains the sole `exec.FlowChecker`.

## 8. Cross-doc

- **Implements** [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) §2 Rule 2
  (outbound slice), §3 (race eliminated by construction), §7 (slice 2 of 2).
- **Reachability machinery** is [ADR-005 v.4](../design/ADR-005-gateways-and-joins.md) §2.10
  (OR-join) / §2.11 (Complex `ActivationJoin`); this slice changes only *where the occupied set comes
  from*, not the `FlowChecker` contract (`pkg/exec/exec.go`).
- **Relates to** SRD-022 (OR-join death-trigger), SRD-023 (Complex gateway), and SRD-027 (the inbound
  slice) — this slice **supersedes SRD-027 §3.8's single-snapshot band-aid** by removing the
  cross-read entirely. (SRD refs carry no version pin — SRD/FIX are single-shot.)
- Hierarchy: SRD → ADR | SAD | SRD only (up/sideways); version pins on ADR/SAD refs only.

## 9. Definition of Done

- [ ] FR-1…FR-7 wired; `position`/`parked` loop-owned; `evMoved` emitted at both move sites; live
      `FlowChecker` removed.
- [ ] NFR-1 audit: no loop-side `currentStep()`/`inState()`/`t.steps`/`t.state` read of another track
      remains (grep recorded in §10).
- [ ] §6 tests added and passing; existing gateway/instance suites green (NFR-2).
- [ ] `make ci` green across all modules; diff-coverage ≥ 95% on touched functions (NFR-4), aiming
      100%.
- [ ] Examples build **and run** (the SRD-027 runtime-smoke discipline).
- [ ] §10 filled (files/lines, V-results, milestone SHAs); status flip is the owner's call.

## 10. Implementation summary

_(filled at landing)_

## Open questions

None.
