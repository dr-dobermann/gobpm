# SRD-059 — Compensation events: the completion ledger and throw-driven undo

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-21 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-026 v.1](../design/ADR-026-compensation-events.md) (the compensation conception: completion ledger, snapshots, direct throw resolution, reverse order, ledger observability), realizing the eligibility window of [ADR-006 v.4](../design/ADR-006-events-and-subscriptions.md) §2.3 |
| Upstream | [ADR-001 v.6](../design/ADR-001-execution-model.md) §4 (single-writer loop), [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md) (boundary attachment), [ADR-023 v.2](../design/ADR-023-sub-process-and-call-activity.md) (scope tree, event-sub handlers), [ADR-013 v.2](../design/ADR-013-instance-observability.md) (fact taxonomy; the reserved `Compensating` slots) |
| Refines | SRD-049 (scope runtime this extends), SRD-052 (event-sub handler arming), SRD-058 (the throw-seam pattern mirrored: `Escalate` → `Compensate`) — sideways |

Epic #90's last event. ADR-026 v.1 decides the conception; this SRD lands its
core: the **completion ledger**, **data snapshots**, the **throw seam**, and
**own-handler compensation** (targeted + scope-wide). The ADR's designed-for
items (recursive default compensation, error-driven sweep,
`compensate-on-terminate` activation) and out-of-scope items
(Transaction/Cancel, Call Activity) are **not** in this SRD.

## §1 Background

The model layer carries a partial stub; nothing executes:

- `CompensationEventDefinition` (`pkg/model/events/compensation.go:17-21`):
  fields `activity flow.ActivityNode` + `waitForCompensation bool` — **no
  getters** (`grep 'func (ced *CompensationEventDefinition)'` → 0), and the
  field name diverges from the spec's `waitForCompletion`
  (`elements/event-definitions.md:142`). Positions: End/IntermediateThrow/Start
  allow-lists already include `TriggerCompensation` (`end.go:21`,
  `intermediate_throw.go:21`, `start.go:20` "only for in-line Sub-Processes"),
  but `boundaryTriggers` does **not** (`boundary.go:16-24`) — a Compensation
  boundary cannot be constructed.
- `isForCompensation` exists on the activity (`activity.go:31`, cloned `:126`,
  set by `WithCompensation()` — `activity_options.go:99-102`) with **no
  getter** and **no flow-exclusion validation**.
- `artifacts.Association` exists (`association.go:21-26`, weakly-typed
  `*foundation.BaseElement` ends) but **no container accepts it** — neither
  `process.Process.Add` nor `SubProcess.Add` handles artifacts (grep empty).
- A compensation throw today flows `emitDefinition → emitEvent →
  PropagateEvent` onto the hub by-ID lookup — a logged no-op drop (the
  pre-SRD-058 state Escalation was in).
- The runtime records **nothing** about completed activities: `scopeEntry`
  (`scope_runtime.go:30-42`) counts live tracks (`active int`) but keeps no
  which-completed-when record, and `completeScope` (`scope_runtime.go:301-345`)
  tears down the data plane (`CloseScope`, `:329`) — no data survives for a
  later handler.

The machinery to mirror is landed: SRD-058's throw seam (`Escalate(code)` on
`renv.RuntimeEnvironment:47-54` → `execEnv.Escalate` `execenv.go:56-62` →
`evEscalate` → `escalation_watch.go`), the event-sub handler arming with the
no-hub-waiter arm (`scope_handler.go:100-113`, Error/Escalation `continue`),
and the MI output-capture precedent for reading a scope's data just before it
closes (`scope_runtime.go:306-327`).

## §2 Requirements

### §2.1 Functional

- **FR-1 — model completeness.** `CompensationEventDefinition` gains getters
  `Activity() flow.ActivityNode` and `WaitForCompletion() bool`; the field and
  constructor parameter align on the spec name **`waitForCompletion`**
  (ADR-026 §2.10 name reconciliation; the constructor keeps validating per the
  public-API rule — a nil `activity` stays legal: it means "default target
  context", §13.5.5). `boundaryTriggers` gains `flow.TriggerCompensation`; a
  Compensation boundary **rejects `cancelActivity=false`** (the flag does not
  apply — ADR-026 §2.3; the Error-boundary guard idiom
  `boundary.go:85-91`).

- **FR-2 — the compensation handler link (realizing the Association).** A
  Compensation boundary is linked to its handler activity by a **typed model
  reference**: `events.WithCompensationHandler(h flow.ActivityNode)` on
  `NewBoundaryEvent` (validated non-nil, marked `isForCompensation`). The
  weakly-typed `artifacts.Association` stays a documentation artifact — in a
  programmatic model the typed link *is* the Association's semantic content
  (engine realization of ADR-026 §2.3; §4.2). The activity side gains the
  missing getter `ForCompensation() bool`; **validation** (process/sub-process
  `Validate`) rejects an `isForCompensation` activity with any incoming or
  outgoing sequence flow, and rejects a Compensation boundary without a
  handler link — fail-fast at registration.

- **FR-3 — the completion ledger.** `scopeEntry` gains an ordered **ledger**:
  entries `{activityID, activityName, ordinal, snapshot, handler}` appended on
  the loop goroutine when a **compensable** activity completes — a leaf
  activity on `evMoved` off it (the loop's `position` map holds the departed
  node) and a composite at `completeScope`. Compensable = carries a
  Compensation boundary (FR-2) or, for a Sub-Process, a compensation Event
  Sub-Process (FR-7). On `completeScope` of a child scope, its ledger **folds
  into the parent's entry** for that Sub-Process (ADR-026 §2.1); when a scope
  closes, its remaining ledger is **discarded**. Non-compensable activities
  never allocate anything (NFR-2).

- **FR-4 — the data snapshot.** At ledger-append time the engine captures a
  **value-copy snapshot** of the data visible at the completed activity's
  scope path (the `frame.paramList` value-copy primitive; for a composite —
  read before `CloseScope`, the MI output-capture point). A handler executes
  with the snapshot as its **read surface** (snapshot-first resolution) while
  **writes** commit to the live scope through the normal frame commit
  (ADR-026 §2.5).

- **FR-5 — the throw seam.** `renv.RuntimeEnvironment` gains
  `Compensate(activityRef string, wait bool)` (the `Terminate`/`Escalate`
  peer); `execEnv.Compensate` emits a new **`evCompensate`** track event
  (throwing track + ref + wait). `throwEvent.emitDefinition` routes
  `*CompensationEventDefinition` to `re.Compensate(...)` — never the hub
  (direct resolution, ADR-006 §2.1). With `wait=false` the throwing token
  continues immediately; with **`wait=true`** (spec default) the throwing
  track **parks** (a loop-owned wait, no hub waiter — the `dispatchToParked`
  resume idiom) until the sweep's last handler drains, then resumes.

- **FR-6 — resolution: targeted and scope-wide, reverse order.** On
  `evCompensate` the loop resolves against the ledgers (`applyCompensate` in a
  new `compensation_watch.go`, the `escalation_watch.go` shape):
  **targeted** (`activityRef` set) → the entry in the throw's enclosing-scope
  ledger chain (own scope, incl. folded child entries); **scope-wide** (empty
  ref) → every entry of the throw's enclosing scope, walked in **reverse
  completion order**, handlers run **sequentially** (ADR-026 §2.4). Handlers
  run as spawned tracks in the throw's scope context with the FR-4 snapshot
  read surface. A handler failure raises a **real fault** through the ADR-006
  §2.6 Error chain (`Compensating → Failed`).

- **FR-7 — the compensation Event Sub-Process.** A Sub-Process containing an
  event sub-process whose start carries `CompensateEventDefinition` records
  that handler in the parent's ledger entry at the Sub-Process's completion.
  `armScopeHandlers` folds `TriggerCompensation` into the **no-hub-waiter**
  arm (`case flow.TriggerError, flow.TriggerEscalation` →
  `..., flow.TriggerCompensation`) — never armed, consumed at ledger-fold
  time.

- **FR-8 — unresolved compensation is logged, never silent, never a fault.** A
  targeted throw with no ledger entry, or a scope-wide throw over an empty
  ledger, logs and emits an `Unresolved` fact; execution continues (the
  cross-cutting uncaught-events-always-log rule; ADR-026 §2.6). No
  `failFromTrack`.

### §2.2 Non-functional

- **NFR-1 — single-writer.** All ledger mutation (append, fold, consume,
  discard) and all resolution run on the loop goroutine (ADR-001 §4); tracks
  never touch a ledger.
- **NFR-2 — pay-for-use.** A model with no compensation handlers allocates no
  ledger entries and captures no snapshots.
- **NFR-3 — ledger observability (ADR-026 §2.7).** New **`KindCompensation`**
  with phases `Thrown / Eligible / Folded / Compensating / Compensated /
  Discarded / Unresolved`; `Compensating`/`Compensated` fill ADR-013's
  reserved names. Details carry scope path + activity + ordinal (+ the
  compensation code path: target ref or "scope-wide"). Echo levels: kind
  default Debug; `Unresolved` → Warn.
- **NFR-4 — coverage.** `make ci` green; diff-coverage ≥95% (aim 100%);
  every touched function ≥80%.

## §3 Models (shapes)

### §3.1 Model layer (`pkg/model/events/`, `pkg/model/activities/`)

```go
// compensation.go — getters + spec-name alignment (FR-1).
func (ced *CompensationEventDefinition) Activity() flow.ActivityNode
func (ced *CompensationEventDefinition) WaitForCompletion() bool

// boundary option (FR-2): the typed handler link realizing the Association.
func WithCompensationHandler(h flow.ActivityNode) BoundaryOption

// activities: the missing read side of isForCompensation (FR-2).
func (a *activity) ForCompensation() bool
```

### §3.2 The ledger (`internal/instance/scope_runtime.go` + new `compensation_watch.go`)

```go
// ledgerEntry — one compensable completion (FR-3). Loop-owned.
type ledgerEntry struct {
	activityID   string
	activityName string
	ordinal      int               // completion order within the scope
	snapshot     []data.Data       // FR-4 value-copy at Completed
	handler      compensationRef   // boundary-handler activity or event-sub node
	folded       []*ledgerEntry    // a completed child Sub-Process's ledger (§2.1)
}

// scopeEntry gains: ledger []*ledgerEntry (append-only, loop goroutine).
```

### §3.3 Throw seam (`pkg/renv`, `internal/instance`)

```go
// renv.RuntimeEnvironment (FR-5) — the Terminate/Escalate peer.
Compensate(activityRef string, wait bool)

// event.go: evCompensate carries compRef string, compWait bool on trackEvent.
// loop.go: case evCompensate: ls.applyCompensate(ctx, ev)
```

### §3.4 Resolution (`internal/instance/compensation_watch.go`, new)

```go
func (ls *loopState) applyCompensate(ctx context.Context, ev trackEvent)
// targeted lookup or reverse-order sweep; sequential handler execution;
// wait-parked thrower resumed when the sweep drains; Unresolved on miss.
```

## §4 Analysis & decisions

### §4.1 Ledger hooks ride existing loop events — no new completion signal
A leaf activity's completion is already visible loop-side: `evMoved` updates
`ls.position` (`loop.go:303-311`), so the **departed** node is at hand exactly
once, race-free; a composite's completion is `completeScope`. Appending there
keeps NFR-1 with zero new cross-goroutine signals. *Alternative — a dedicated
evCompleted emitted by the track:* rejected, a second signal for information
the loop already has.

### §4.2 Typed handler link, not a wired Association artifact
`artifacts.Association` has weakly-typed `*foundation.BaseElement` ends and no
container will accept it (§1). Wiring artifact plumbing into
process/sub-process containers to then runtime-resolve BaseElement pointers
back to activities would be a large, stringly-typed detour for a programmatic
model. The typed `WithCompensationHandler` option carries the Association's
*semantic* content (ADR-026 §2.3's "linked by an Association") fail-fast at
construction. *Alternative — full Association container support:* deferred to
the XML-interchange workstream (ADR-024), where the artifact form actually
arrives from outside.

### §4.3 The wait-park is loop-owned
`waitForCompletion=true` parks the thrower with **no hub waiter** — the loop
itself resumes it when the sweep drains (the loop-local
`dispatchToParked`/conditional idiom, `conditional.go`). *Alternative — a
synchronous channel handshake inside Exec:* rejected — it would block a track
goroutine on loop work and invert the single-writer discipline.

### §4.4 Snapshot scope = the visible surface, value-copied
The snapshot captures what the activity could read at its scope path
(walk-up-visible data), value-copied (`frame.paramList` idiom) so later scope
mutation cannot retro-change it. Reads resolve snapshot-first; writes are
live-scope commits (ADR-026 §2.5). *Alternative — snapshot only the scope's
own locals:* rejected, the handler must see what the work saw (§13.5.5
"original data context restored").

### §4.5 Sequential reverse sweep
The ledger's ordinal is the authority; the sweep walks it backward, one
handler at a time (ADR-026 §2.4 engine choice — a conformant serialization).
Folded child entries compensate within their parent entry's turn, reverse
order inside the fold.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | model + validation (`pkg/model/events`, `pkg/model/activities`) | FR-1: getters, `waitForCompletion` rename, boundary accepts Compensation (interrupting-only); FR-2: handler link required, `isForCompensation` flow exclusion rejected at Validate |
| T-2 | targeted compensation (`internal/instance/compensation_watch_test.go`) | FR-3/4/5/6: complete A (boundary handler) → throw `activityRef=A` → handler runs with A's snapshot as read surface, writes land in live scope; instance completes |
| T-3 | scope-wide reverse order | FR-6: complete A then B → scope-wide throw → handlers run B-then-A, sequentially; `Eligible` ordinals prove the order |
| T-4 | `waitForCompletion` semantics | FR-5: `wait=true` — the token after the throw runs only after both handlers `Compensated`; `wait=false` — it runs concurrently with the sweep |
| T-5 | child folding | FR-3/7: a completed Sub-Process with a compensation event-sub → parent ledger holds the folded entry; compensating the Sub-Process runs the event-sub handler |
| T-6 | unresolved logged, no fault | FR-8: throw at a never-completed / unknown ref → no fault, execution continues, `Thrown`+`Unresolved` facts + log line |
| T-7 | presumed abort | FR-3: a failed (boundary-caught) activity never ledgers → a later scope-wide sweep skips it |
| T-8 | discard at scope close | FR-3/NFR-3: an un-compensated ledger discarded when the enclosing scope completes — `Discarded` fact, snapshots released |
| T-9 | handler failure → Error chain | FR-6: a failing handler faults through the §2.6 chain (catchable by an Error boundary; uncaught → instance fault) |
| T-10 | e2e + example (`pkg/thresher/compensation_test.go`, `examples/compensation-events/`) | full path through the public surface; the fact triple `Thrown → Compensating → Compensated`; example exits 0 |

## §7 Milestones

Each milestone one commit, `make ci` green:

- **M1 — model + validation.** FR-1, FR-2; T-1.
  `feat(events): Compensation boundary, handler link, model completeness`.
- **M2 — the completion ledger + snapshots + ledger facts.** FR-3, FR-4
  (capture side), FR-7 (fold side), `KindCompensation`
  `Eligible/Folded/Discarded`; T-5 (fold/record half), T-7, T-8.
  `feat(instance): the compensation completion ledger`.
- **M3 — throw seam + resolution.** FR-5, FR-6, FR-8; `Thrown/Compensating/
  Compensated/Unresolved`; T-2, T-3, T-4, T-6, T-9.
  `feat(instance): Compensation throw and reverse-order resolution`.
- **M4 — e2e + example + doc sync.** T-10, `examples/compensation-events/`,
  CHANGELOG, conformance-status row 8, README (+ru), roadmap.
  `feat: Compensation events — e2e, example, doc sync`.

## §8 Cross-doc

- Implements **ADR-026 v.1** (whole conception; this SRD = its core scope).
- Upstream: **ADR-006 v.4** §2.1/§2.3/§2.6, **ADR-001 v.6** §4, **ADR-018
  v.1**, **ADR-023 v.2**, **ADR-013 v.2**.
- Sideways: **SRD-049** (scope runtime), **SRD-052** (`armScopeHandlers`),
  **SRD-058** (throw-seam + watch-file pattern).
- Closes the last item of GitHub epic **#90**. The ADR-026 designed-for items
  are *not* here: recursive default compensation, error-driven sweep,
  `compensate-on-terminate` activation, Transaction/Cancel (#91), Call
  Activity.

## §9 Definition of Done

- [ ] FR-1…FR-8 implemented; every §6 test exists and passes.
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); touched functions ≥80%.
- [ ] Ledger lifecycle demonstrably observable (T-3 ordinals, T-8 discard).
- [ ] Unresolved compensation logged, no fault (T-6).
- [ ] `examples/compensation-events/` runs exit 0; binary gitignored.
- [ ] §10 filled; conformance-status row 8 → landed; roadmap/README synced;
      ADR-026 status flip at landing.

## §10 Implementation summary

Landed on `feat/compensation-events`; master (the ADR-025 v.2 off-loop
iteration refactor) merged mid-stream.

| Milestone | Commit | Scope |
|---|---|---|
| ADR | `ae25102` | ADR-026 v.1 (Draft) |
| SRD | `af3b2cf` | this document (Draft) |
| M1 | `eafc711` | FR-1/FR-2 model + validation: getters, `waitForCompletion` alignment, `TriggerCompensation` in `boundaryTriggers`, `NewCompensationBoundaryEvent` + typed handler link + Clone carry, `ForCompensation()`, `ValidateCompensationPlacement` in both containers, entry-seeding exclusions |
| M2 | `3e25f34` | FR-3/FR-4/FR-7(record): the loop-owned `ledgers` map, `recordLeafCompletion` (evMoved) + `recordScopeCompletion` (completeScope, fold) + `discardLedgers` (complete/cancel/loop-exit), `scope.SnapshotAt`, `KindCompensation` `Eligible/Folded/Discarded` |
| M3 | `a660f97` | FR-5/FR-6/FR-8: `Compensate(ref, wait)` on `renv`, `evCompensate`, the wait-throw as a wait node (`CompensationWaitRef` capability, `parkCompensationThrow`, deferred emit, sentinel resume), `compensation_watch.go` sweep (targeted incl. folded / scope-wide reverse, sequential; abort→Error chain), frame-inputs + child-scope-seed read surfaces, `Thrown/Compensating/Compensated/Unresolved` |
| merge | `45c9917` | origin/master (ADR-025 v.2 off-loop decorator) — `compositeIteratorOf`→`drivesOwnIteration` in the MI/loop skip, `enterComposite` folded into the wait-kind dispatch |
| M4 | `696a3e0` | T-10 thresher e2e; `examples/compensation-events/`; CHANGELOG / conformance row 8 / README(+ru) / roadmap sync; the e2e-caught fixes (Compensation excluded from hub boundary-arming; `finishSweep` stopping guard) |

**Verification:** `make ci` green post-commit — diff-coverage **95.3% of 697
changed lines** (min 95%), race tests, govulncheck clean; every touched/new
function ≥80% (most 100%); `examples/compensation-events/` smoked exit 0 with
the reverse order observable.

**Deltas vs this draft (behavior unchanged):**
- §3.1's `WithCompensationHandler` option sketch landed as the dedicated
  **`NewCompensationBoundaryEvent`** constructor (no `BoundaryOption` plumbing
  exists; validate-all-params in one place).
- §3.2's `scopeEntry.ledger` field landed as the loop-state **`ledgers` map**
  keyed by scope path — the root scope (which has no `scopeEntry`) ledgers
  uniformly.
- FR-4 refinement: a **leaf's snapshot is captured on the track goroutine** at
  the completion itself and carried on `evMoved` — a downstream node's commit
  otherwise races the loop's append (caught by T-2).
- FR-5 refinement: the wait-throw's `evCompensate` is **deferred until after
  its `evMoved`**, so the just-completed predecessor's ledger entry is applied
  before the sweep resolves (FIFO).
- The M4 e2e surfaced two fixes the instance-level tolerant hub masked:
  Compensation joined the Error/Escalation exclusion in `armBoundaries`
  (ADR-006 §2.3 — not a live subscription), and `finishSweep` drops the
  resume on a stopping instance.

## Open questions

- **FR-2 realization (typed link vs Association wiring)** — *resolved at the
  doc gate:* the typed `WithCompensationHandler` option realizes the
  Association's semantic content in the programmatic model; full Association
  container support is deferred to the ADR-024 interchange workstream (§4.2).

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-21 | Ruslan Gabitov | Initial draft — lands ADR-026 v.1's core: model completeness (getters, `waitForCompletion` alignment, Compensation boundary + typed handler link realizing the Association, `isForCompensation` flow exclusion), the loop-owned completion ledger on `scopeEntry` (append at `evMoved`/`completeScope`, child folding, discard) with value-copy snapshots, the `Compensate(ref, wait)` throw seam (`evCompensate`, wait-parked thrower), targeted + scope-wide reverse-order sequential resolution in `compensation_watch.go`, the compensation event-sub-process fold, unresolved-logs-no-fault, and `KindCompensation` ledger observability (Thrown/Eligible/Folded/Compensating/Compensated/Discarded/Unresolved). Four milestones. ADR-026's designed-for/out-of-scope items excluded. |
