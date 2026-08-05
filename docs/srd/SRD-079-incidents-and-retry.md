# SRD-079 — Incidents: the raise path, the durable record, retry and resolution

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-08-04 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-036 v.1](../design/ADR-036-incidents-and-fault-tolerance.md) — the whole decision |
| Upstream | [ADR-033 v.2](../design/ADR-033-persistence-and-state.md) §2.1–§2.3 (the checkpoint the incident rides); [ADR-021 v.1](../design/ADR-021-service-task-execution-model.md) §2.6–§2.8 (job retry below the loop, the deferred Incident); [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) (the loop as sole state owner — resolution ops are loop events); [ADR-013 v.2](../design/ADR-013-instance-observability.md) (the reserved `Incident` phase); [ADR-007 v.2.1](../design/ADR-007-in-memory-long-waits.md) (wake mechanics a scheduled retry reuses); [ADR-025 v.2](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.2 (per-instance scopes); [ADR-026 v.1](../design/ADR-026-compensation-events.md) §2.1 (the ledger records completions only) |
| Closes | [#80](https://github.com/dr-dobermann/gobpm/issues/80) |

## §1 Background

ADR-036 v.1 decides that a technical failure becomes a durable incident
instead of an instance death, with continuation by respawn. This SRD lands
it. The code baseline it lands against:

- **The failure path is fatal.** `internal/instance/track.go` classifies a
  step error: context-cancelled → `TrackCanceled`; a typed `*events.BpmnError`
  emits a `Thrown` fact; everything lands in `TrackFailed`. The loop's
  `applyFailed` tries `matchErrorBoundary` / `matchErrorScopeChain` (typed
  errors only — `boundary_watch.go`: *"an untyped failure → fault, as
  before"*), and on no catch calls `reportUncaught` + `failFromTrack` →
  `inst.fail(err)` + `stopAll()`: every sibling track is stopped, the
  instance faults.
- **The failure is invisible to persistence.** `evFailed` is not in
  `checkpointTransitions`; `TrackFailed` is excluded from `liveTrackStates`;
  `checkpoint.Document` carries no error field; `repository.Status` has no
  non-terminal failure value; `memrepo.ListInFlight` filters
  `Status == StatusActive` exactly.
- **Job retry exists below the loop** (`pkg/tasks/retrypolicy.go`,
  `localdispatcher.retryOrExhaust`): technical worker faults re-enqueue with
  backoff, the parked track unmoved; exhaustion delivers a terminal
  `WorkerFault` that surfaces through `service_task.go` as an **untyped**
  error — i.e. the fatal path above.
- **The vocabulary is half-ready.** `observability.PhaseIncident` is declared
  and unused; `PhaseRetryScheduled` / `PhaseRetriesExhausted` are emitted only
  by the dispatcher.
- **Respawn machinery exists twice.** `loopState.spawnForks` births tracks on
  a boundary catch (`newTrack(node, inst, prevTrack)` — lineage and scope
  inherited from the predecessor); `restore.go` respawns tracks from
  `TrackRecord`s (scope binding is the recorded `ScopePath` string; scopes are
  reopened before tracks). A scheduled wake rides `thresher`'s
  `HoldTimer` / `wakeInstance` pair.

Three defects sit *on* this path and are folded in per the
no-pre-existing-errors rule: `StepFailed` is declared but never assigned
(the per-step failure record does not exist); the `trackPhase` map lacks a
`TrackDehydrated` entry (its node phase projects as the zero value); and
`memrepo.ListInFlight`'s `== StatusActive` comparison would hide any new
non-terminal status from recovery.

## §2 Requirements

### Functional

| # | Requirement |
|---|---|
| **FR-1** | A technical failure of a node ends its track in a new terminal state **`TrackIncident`** and opens an **incident record** on the instance: node, scope path/segment, lineage, cause chain, cause class, attempt count, first/last failure times, and a **failure-time data snapshot** — the variables visible in the node's scope chain, captured at each raise. The open incident **pins its scope open**. Siblings keep executing; the instance stays alive. |
| **FR-2** | An **uncaught BPMN error thrown by a failing activity** (no boundary, no scope-chain catcher) opens an incident at the throwing node instead of faulting the instance. Three failures keep today's fatal path: an **invariant violation** (`errs.BrokenInvariant`); an **Error End Event** reaching the root uncaught — the modeled outcome; and **any uncaught failure in a child instance** — it propagates across the call boundary, and the incident arises only at the **top-level** caller's Call Activity node, whose retry re-runs the whole child (ADR-036 §2.1). The uncaught-must-log rule holds: every raised incident logs once at `Error`. |
| **FR-3** | **Job-retries-exhausted** (ADR-021 dispatcher gave up) opens an incident at the service task carrying the job diagnostic (topic, attempts, last error) as its cause. |
| **FR-4** | While an incident is open, the instance's token views show the token **at the failing node** in a new `TokenIncident` state; on close (resolved / dead-lettered / overtaken) it shows consumed. |
| **FR-5** | Raising an incident is a **persist point**. Incidents serialize as an instance-level `Incidents` list in the checkpoint document (schema 2→3, additive). The persisted status of an instance with open incidents is a new non-terminal **`StatusActiveIncidents`**; recovery lists it as in-flight and re-arms a scheduled retry's deadline. |
| **FR-6** | **Retry** — by policy or by the `RetryIncident` primitive — spawns a fresh track at the failed node with the failed track as predecessor. Armed boundaries are **not re-armed**. The retry policy (engine default + per-activity `ActivityOption`) schedules off the loop as a persisted deadline; the engine default is **no automatic retry**. |
| **FR-7** | **Resolve** closes the incident and spawns the continuation from the node's **outgoing flows** without re-executing the node. |
| **FR-8** | **Give up** closes the incident as **dead-lettered**; the record is retained durably with its full history; the instance may then be cancelled by the operator. |
| **FR-9** | Open incidents are queryable on the handle (`Incidents()`); an interrupting boundary firing on an incident-holding node closes the incident as **overtaken**. |
| **FR-10** | Every incident action emits a fact through the existing stream: `Kind: KindFault, Phase: PhaseIncident`, with the action (`raised`, `retry-scheduled`, `retried`, `resolved`, `dead-lettered`, `overtaken`) and cause class in `Details`. |
| **FR-11** | The three §1 defects are fixed: `StepFailed` is assigned on the step failure path; `trackPhase` gains `TrackDehydrated`; `ListInFlight` recognises every non-terminal status. |

### Non-functional

| # | Requirement |
|---|---|
| **NFR-1** | No new dependency; core stays stdlib + `uuid`. |
| **NFR-2** | The loop remains the sole state mutator (ADR-017 v.1): resolution primitives enter as loop events, never touch instance state directly. |
| **NFR-3** | No sleeping goroutine per scheduled retry — a deadline plus the existing hold/wake mechanics. |
| **NFR-4** | Diff coverage ≥95% (`COVER_MIN`) on touched lines; `make ci` green including `-race`. |

## §3 Models

### §3.1 `internal/instance` — the incident (new file `incident.go`)

```go
type incidentState uint8

const (
	incidentOpen incidentState = iota
	incidentRetryScheduled
	incidentResolved
	incidentDeadLettered
	incidentOvertaken
)

type incident struct {
	id         string
	nodeID     string
	trackID    string        // the TrackIncident track (last failed attempt)
	scopePath  scope.DataPath
	scopeSeg   string
	prev       []string      // lineage of the failed track
	cause      string        // rendered chain of the failing error
	causeClass string        // errs class of the failing error
	attempts   int           // raises for this node+scope, across retries
	retryAt    time.Time     // zero unless incidentRetryScheduled
	firstAt    time.Time
	lastAt     time.Time
	data       json.RawMessage // failure-time snapshot: visible scope at last raise
	state      incidentState
}
```

The snapshot is captured with the checkpoint's existing scope encoding
(the `ScopeRecord.Data` path) over the chain visible from the incident's
scope, at every raise — it always shows the **last** failed attempt's view.
The open incident pins its scope: scope disposal skips a scope while an
open incident references it (retry re-enters against that data).

Held as `incidents map[string]*incident` on `Instance`, mutated only by the
loop (NFR-2). A repeated failure of the same node+scope after a retry
updates the existing open incident (attempts++, lastAt, cause) rather than
opening a second one.

### §3.2 `internal/instance` — the raise path

`trackState` gains `TrackIncident` (appended after `TrackDehydrated`; its
`String()` slice entry and `trackPhase` entry — `PhaseIncident` — added; the
missing `TrackDehydrated → PhaseDehydrated` entry added beside it, FR-11).

`applyFailed` reclassifies, in order:

1. typed `BpmnError` with a boundary / scope-chain catcher → caught, as today;
2. `errs.HasClass(errs.BrokenInvariant)` (walked through the wrap chain), an
   **Error End Event's own throw** (the modeled outcome), or a **child
   instance** (`parentInstanceID` set — the failure propagates across the
   call boundary, FR-2) → `failFromTrack`, as today;
3. everything else → `raiseIncident(ev.track)`: the track ends in
   `TrackIncident` **keeping its recorded position** (no `clearPosition`;
   boundary watches stay armed — no `disarmBoundaries`), the incident is
   opened or re-armed (§3.1), the `raised` fact emits, and the checkpoint
   fires (a new `evIncident` entry in `checkpointTransitions`, FR-5).

The loop's exit gains the **incident park**: when the last active track ends
and open incidents remain (and the instance is not stopping), the loop
checkpoints and returns without settling — no handler teardown, no ledger
discard, the instance stays `Active`. The single-task call contract rides the
invoker: `childProcess.Done()` reports the **settled** signal, not the loop
exit, so a child's park (incident or dehydration) never reads as completion.

`StepFailed` is assigned to the failing step's `stepInfo.state` on this path
(FR-11).

### §3.3 `internal/instance/checkpoint` — the document (schema 3)

```go
type IncidentRecord struct {
	RetryAt    *time.Time `json:"retry_at,omitempty"`
	ID         string     `json:"id"`
	NodeID     string     `json:"node_id"`
	TrackID    string     `json:"track_id"`
	ScopePath  string     `json:"scope_path"`
	ScopeSeg   string     `json:"scope_seg,omitempty"`
	Prev       []string   `json:"prev,omitempty"`
	Cause      string     `json:"cause"`
	CauseClass string     `json:"cause_class,omitempty"`
	State      string     `json:"state"`
	Attempts   int        `json:"attempts"`
	FirstAt    time.Time  `json:"first_at"`
	LastAt     time.Time  `json:"last_at"`
	Data       json.RawMessage `json:"data,omitempty"` // failure-time snapshot
}
```

`Document` gains `Incidents []IncidentRecord` (`json:"incidents,omitempty"`);
`CurrentSchema` bumps 2→3 (the 1→2 precedent: additive `Boundaries`,
older schemas readable). Closed incidents (`resolved` / `dead-lettered` /
`overtaken`) persist too — the dead-letter record *is* the durable artifact
(FR-8); a future history store may take over pruning.

`repository.Status` gains `StatusActiveIncidents` (appended after
`StatusSuspended`; `IsTerminal()` unchanged). `persistedStatus` becomes
incident-aware — an `Active`/`Dehydrated` instance with ≥1 open incident
persists as `StatusActiveIncidents`. `memrepo.ListInFlight` switches from
`== StatusActive` to `!rec.Status.IsTerminal() && rec.Status != StatusSuspended`
(FR-11), so recovery adopts incident-holding instances.

### §3.4 `internal/instance` — retry, resolve, give up (loop events)

Handle primitives enqueue loop events (NFR-2); the loop applies:

- **retry** — `newTrack(node, inst, failedTrack)` at the incident's node
  (lineage and scope inherited from the failed track, exactly the
  `spawnForks` shape), spawn; incident stays open, `attempts` carries;
  `retried` fact.
- **resolve** — `spawnForks` over the node's outgoing flows from the failed
  track (the boundary-catch shape: continuation without re-execution);
  incident → `incidentResolved`; `resolved` fact.
- **give up** — incident → `incidentDeadLettered`, the `dead-lettered` fact;
  the token view shows consumed (FR-4). No track spawns.
- **scheduled retry** — the policy (§3.5) sets `retryAt`; the deadline rides
  the existing hold/wake mechanics (`HoldTimer` when the instance
  dehydrates; a loop timer while resident). At the deadline the loop applies
  **retry** above. Recovery re-derives the hold from `RetryAt` (FR-5).
- **overtaken** — an interrupting boundary firing on the incident's node
  closes the incident (`overtaken` fact) as part of the boundary's normal
  cancellation of the host.

### §3.5 Policy options

```go
// pkg/model/activities (ActivityOption — applies to every activity type)
func WithIncidentRetryPolicy(p tasks.RetryPolicy) ActivityOption

// pkg/thresher (engine-wide default)
func WithIncidentRetryPolicy(p tasks.RetryPolicy) Option
```

Both follow the `WithRetryPolicy` idiom: nil → `errs.EmptyNotAllowed`
naming the option. `tasks.RetryPolicy` is reused as-is (`Retry(attempt,
cause) (backoff, bool)`). **Engine default: no policy** — every incident
waits for an operator unless an activity or the engine opts in (FR-6,
ADR-036 §2.3's conservative default).

### §3.6 `pkg/thresher` — the handle surface

```go
type IncidentView struct {
	ID         string
	NodeID     string
	NodeName   string
	Cause      string
	CauseClass string
	State      string
	Attempts   int
	RetryAt    time.Time // zero if none scheduled
	FirstAt    time.Time
	LastAt     time.Time
	Data       json.RawMessage // failure-time snapshot (visible scope at last raise)
}

func (h *InstanceHandle) Incidents() []IncidentView
func (h *InstanceHandle) RetryIncident(ctx context.Context, id string) error
func (h *InstanceHandle) ResolveIncident(ctx context.Context, id string) error
func (h *InstanceHandle) DropIncident(ctx context.Context, id string) error
```

`TokenState` gains `TokenIncident`; `tokenStateFor` maps `TrackIncident` →
`TokenIncident` while the incident is open, `TokenConsumed` after close
(FR-4). Ops on a dehydrated instance wake it first (`wakeInstance`), then
enqueue; on an unknown id → `errs.ObjectNotFound` naming the incident.

## §4 Analysis

### §4.1 Why the incident lives on the Instance, not the track

The track ends (ADR-036 §2.2); anything stored on it dies with the loop's
live views. An instance-level map is what the loop already owns exclusively,
what `captureDocument` already serializes from, and what `Incidents()` can
project without touching track internals. The track keeps only its terminal
state and history — the same division as scopes vs tracks.

### §4.2 Why `persistedStatus` becomes incident-aware instead of adding a runtime state

The runtime `State` vocabulary (`Active`, `Dehydrated`, …) describes the
execution engine's own condition, and an instance with an open incident *is*
active — ADR-036 §2.2 calls the condition a predicate, not a state. Only the
**persisted** vocabulary needs the distinction, because the store must answer
"what needs an operator" without loading payloads. One derivation function
changes; no state machine grows.

### §4.3 Why closed incidents stay in the document

The dead-letter obligation (ADR-036 §2.5) is that the record survives
*giving up*. Splitting closed incidents into a separate artifact invents a
second persistence channel before the history store (its actual home)
exists. The document is per-instance and bounded by the model's node count
times attempts; pruning is deferred to the history-store work, and stated
there rather than silently.

### §4.4 Why the retry deadline reuses hold/wake instead of a scheduler

A scheduled retry is semantically a timer wait owned by the instance. The
dehydration slice already built deadline holds and wake-on-trigger
(ADR-007 v.2.1); a second scheduling mechanism would duplicate it with a
different failure surface. Resident instances use a loop timer, dehydrated
ones the existing `HoldTimer` — both re-derivable from `RetryAt` on
recovery, per ADR-033's minimal-state rule.

### §4.5 Why the snapshot is the visible scope, captured at raise

Three cuts were weighed. A **full scope-chain copy** duplicates values the
pinned live scope already offers and enlarges every incident. A **marker
mechanism** (declared data tagged for postmortem retention) is the mature
answer — it also handles the inverse, *excluding* sensitive values from a
durable record — but it is a modeling-surface feature deserving its own ADR
after real operator feedback, and is tracked separately. The chosen middle:
the **variables visible from the incident's scope** — exactly the failing
node's input universe, encoded by the machinery `ScopeRecord.Data` already
uses, refreshed per raise so it shows the last attempt's view. The retention
caveat (a snapshot in a dead-lettered incident outlives the instance's data
lifecycle) is stated in ADR-036 §2.5 rather than hidden.

### §4.6 Why `attempts` lives on the incident, not the track chain

Lineage reconstructs attempts (each retry is a track), but counting by
walking `prev` chains makes the policy's `Retry(attempt, cause)` call — the
hot decision — O(history) and forces the loop to retain terminal tracks
indefinitely for correctness rather than convenience. A counter on the
incident is the checkpointable, O(1) truth; lineage stays the audit trail.

## §5 API

Added: `TrackIncident` (internal), `TokenIncident`, `IncidentView`,
`InstanceHandle.{Incidents,RetryIncident,ResolveIncident,DropIncident}`,
`activities.WithIncidentRetryPolicy`, `thresher.WithIncidentRetryPolicy`,
`repository.StatusActiveIncidents`, `checkpoint.IncidentRecord`,
checkpoint schema 3.

Changed: none breaking — every constructor and handle method keeps its
signature; `repository.Status` is append-only (wire-compatible, no
`String()`/switch consumers beyond the two updated call sites).

## §6 Tests

| # | Test | Asserts |
|---|---|---|
| T-1 | `TestTechnicalFailureRaisesIncident` | untyped error → `TrackIncident`, incident open, sibling branch completes, instance not faulted (FR-1) |
| T-2 | `TestUncaughtBpmnErrorRaisesIncident` | no catcher → incident, one `Error` log, `Uncaught` semantics preserved (FR-2) |
| T-2a | `TestErrorEndEventFaultsInstance` | an Error End Event uncaught at the root keeps the fatal path — the modeled outcome, no incident (FR-2; the pre-existing test, re-pinned to this contract) |
| T-3 | `TestInvariantViolationFaultsInstance` | `BrokenInvariant` class → today's fatal path (FR-2) |
| T-4 | `TestJobExhaustionRaisesIncident` | dispatcher exhaustion → incident with job diagnostic as cause (FR-3) |
| T-5 | `TestIncidentTokenVisible` | `Tokens()` shows `TokenIncident` at the node while open; consumed after drop (FR-4) |
| T-5a | `TestIncidentDataSnapshot` | the snapshot holds the failing attempt's visible variables; a sibling's later write does not alter it; a failed retry refreshes it; the scope stays open while the incident is (FR-1) |
| T-6 | `TestIncidentCheckpointRecovery` | raise checkpoints (schema 3, `StatusActiveIncidents`); kill+restore keeps the incident and re-arms `RetryAt` (FR-5) |
| T-7 | `TestIncidentRetryRespawns` | retry track's `prev` ends with the failed track; node re-executes; attempts increments; repeat failure updates the same incident (FR-6) |
| T-8 | `TestIncidentRetryPolicySchedules` | per-activity policy sets `RetryAt` on the injected clock; deadline fires the retry; engine default schedules nothing (FR-6) |
| T-9 | `TestIncidentResolveContinues` | resolve → outgoing flow taken, node not re-executed, incident closed (FR-7) |
| T-10 | `TestIncidentDropDeadLetters` | drop → `dead-lettered` state persisted in the document (FR-8) |
| T-11 | `TestBoundaryAcrossIncident` | non-interrupting timer boundary fires while incident open; retry does not re-arm; interrupting boundary closes it as overtaken (FR-6, FR-9) |
| T-12 | `TestLedgerExcludesFailedAttempts` | compensation ledger holds one entry after fail→retry→complete (ADR-036 §2.4) |
| T-13 | `TestMIIncidentPerInstance` | parallel MI: one inner fails, siblings complete, retry re-runs only the failed instance; sequential MI blocks behind it (ADR-036 §2.4) |
| T-14 | `TestIncidentFacts` | `PhaseIncident` facts with action detail for raise/retry/resolve/drop (FR-10) |
| T-15 | `TestListInFlightNonTerminal` | `ListInFlight` returns `StatusActiveIncidents` records; suspended excluded (FR-11) |
| T-16 | `examples/incident-retry` | end-to-end: failure → incident visible → retry-now → completion; asserts its own outcome |

## §7 Milestones

| # | Milestone | Contents |
|---|---|---|
| **M1** | The entity and the raise path | §3.1, §3.2; `StepFailed` + `trackPhase` fixes; T-1…T-4, raise half of T-14 |
| **M2** | Visibility | `TokenIncident` projection, `Incidents()` view, the data snapshot + scope pinning; T-5, T-5a |
| **M3** | Persistence | §3.3; T-6, T-15 |
| **M4** | Retry | §3.4 retry + scheduled retry, §3.5 options; T-7, T-8 |
| **M5** | Resolve, drop, overtaken | §3.4 remainder, handle ops; T-9, T-10, rest of T-14 |
| **M6** | Interaction contracts | T-11, T-12, T-13 (tests over M1–M5 machinery; fixes if they surface) |
| **M7** | Example + docs | `examples/incident-retry`, guides, linked-docs sync |

## §8 Cross-doc references

| Doc | Version | Why |
|---|---|---|
| [ADR-036](../design/ADR-036-incidents-and-fault-tolerance.md) | v.1 | the decision this lands, in full |
| [ADR-033](../design/ADR-033-persistence-and-state.md) | v.2 | §2.1 document shape, §2.2 re-entry atomicity, §2.3 at-least-once effects |
| [ADR-021](../design/ADR-021-service-task-execution-model.md) | v.1 | §2.7 job retry unchanged below; §2.8's deferral closed |
| [ADR-017](../design/ADR-017-channel-based-event-processing.md) | v.1 | resolution ops as loop events |
| [ADR-013](../design/ADR-013-instance-observability.md) | v.2 | `PhaseIncident` activates; stream stays announcement |
| [ADR-007](../design/ADR-007-in-memory-long-waits.md) | v.2.1 | hold/wake reuse for scheduled retries |
| [ADR-025](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) | v.2 | §2.2 per-instance scope identifies an MI incident |
| [ADR-026](../design/ADR-026-compensation-events.md) | v.1 | §2.1 ledger untouched by failed attempts |

## §9 Definition of Done

- [x] FR-1…FR-11 implemented and demonstrated by §6
- [x] NFR-1: `go.mod` unchanged
- [x] NFR-2/NFR-3: no state mutation off the loop; no per-retry goroutine
- [x] NFR-4: diff coverage ≥95% on touched lines; `make ci` green incl. `-race`
- [x] ADR-036 status flip to Accepted at the PR handover (with its RU twin)
- [x] `examples/incident-retry` runs under the CI timeout asserting its outcome
- [x] `/check-srd` PASS

## §10 Implementation summary

Landed on `feat/incidents-retry`, the seven planned milestones plus the
linked-docs sync:

| Milestone | Commit | What landed |
|---|---|---|
| Docs | `400b687`, `a2d9b5e` | ADR-036 v.1 (with the scoping amendments), this SRD |
| M1 — the raise path | `928914d` | `TrackIncident`, the incident entity, the three-way taxonomy, the incident park, `KindFault`/`PhaseIncident` facts; FR-11's `StepFailed` and `trackPhase` fixes |
| M2 — visibility | `2420add` | `TokenIncident`, `Incidents()` views, the failure-time snapshot, scope pinning |
| M3 — persistence | `4fa2e99` | checkpoint schema 3, `StatusActiveIncidents`, restore, `ListInFlight` (FR-11) |
| M4 — retry | `713dc1c` | both policy options, the single-timer scheduler, the respawn with transferred watches, close-by-progress |
| M5 — resolution | `be9cfbb` | the loop's operator channel, resolve/drop/overtaken, the handle ops with the parked-instance rebuild (`WithPendingIncidentOp`) |
| M6 — contracts | `36cd001` | T-11/T-12/T-13 green through the real machinery |
| M7 — example + docs | `573d3f6`, `8e13275` | `examples/incident-retry`, the incidents guide, the front-door sync |

Deltas against the draft, decided at their gates:

- **The taxonomy grew three carve-outs** (§2, ADR-036 §2.1): an Error End
  Event's uncaught throw stays fatal (the modeled outcome), and a **child
  instance propagates every uncaught failure** across the call boundary —
  incidents exist only at top-level instances, the caller's Call Activity
  being the retry unit ("the whole child is a single task").
- **A dropped incident blocks completion**: the dead letter keeps its scope
  pin and the instance parks `Active` until the operator's next act — it
  never silently settles `Completed` past a dead letter.
- **Ops on a parked instance ride the rebuild** as a pending request (the
  `PendingTrigger` shape), applied before the fresh loop's park decision.
- **The incident carries its node object** (with a recursive `nodeByID`
  fallback after restore): nodes inside a sub-process are not in the
  snapshot's top-level map — the MI contract test caught that, along with a
  pin-release-before-spawn ordering bug in the retry.
- Fixed on the way, per the no-pre-existing-errors rule: the `SnapshotAt`
  clone-shape gap (`Property`/`DataObject` shadow `Clone`), the call
  watcher's loop-exit-as-completion read (`childProcess.Done` → the settled
  signal), `StepFailed` never assigned, `trackPhase` missing
  `TrackDehydrated`, and `ListInFlight`'s exact-status filter.

**Scoped out, recorded**: boundary watches over incident nodes do not yet
persist across a restart (`boundaryRecords` skips non-live hosts) — in-memory
they stay armed (T-11); the cross-restart re-arm belongs with the wait-holder
integration of a later slice. The dehydration of an instance holding only a
*scheduled* retry is likewise deferred: such an instance stays resident (one
loop goroutine total; NFR-3's no-per-retry-goroutine holds).

Verification: `make ci` green end to end; per-milestone diff-coverage
100.0 / 98.7 / 96.7 / 98.4 / 96.7 / 96.1%, all above the 95% gate;
`go.mod` unchanged (NFR-1); `/check-srd` audit PASS.

## Open questions

_None._
