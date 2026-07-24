# SRD-061 — Transaction Sub-Process: ACID-like abort by Cancel

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-07-23 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-028 v.1](../design/ADR-028-transaction-sub-process.md) §2.1–§2.9 (the Transaction variant + the loop-local Cancel abort); epic #91 |
| Upstream | [ADR-026 v.1](../design/ADR-026-compensation-events.md) §2.2/§2.4 (the completion ledger + reverse-order sweep the abort consumes in wait mode), [ADR-023 v.3](../design/ADR-023-sub-process-and-call-activity.md) §2.5 (the scope-wide cancel + `terminateScope` loop-local resolution this mirrors), [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md) §2.7 (the Cancel boundary trigger this un-defers), [ADR-006 v.4](../design/ADR-006-events-and-subscriptions.md) (the `CancelEventDefinition` + direct-resolution class), [ADR-013 v.2](../design/ADR-013-instance-observability.md) (the `KindScope`/`PhaseCanceled` fact the abort reports; the phase set is open), [ADR-001 v.6](../design/ADR-001-execution-model.md) §4 (the single-writer loop) |
| Refines | — |
| Related | SRD-059 (compensation ledger + sweep this reuses), SRD-049 (scoped Terminate — the loop-local End-event resolution this mirrors), SRD-052 (the Event Sub-Process marker + shape-rule pattern) |

## §1 Background

BPMN's **Transaction Sub-Process** (§10.7, [sub-processes.md](../bpmn-spec/semantics/sub-processes.md))
is a plain Sub-Process **plus** one behavior: reaching a **Cancel End Event**
inside triggers an ACID-like **abort** — compensate the successfully-completed
inner activities, terminate the still-running ones — after which control leaves
through a **Cancel intermediate boundary event** on the Transaction. ADR-028
decides the conception; this SRD lands it against the current substrate.

Everything the abort needs is already implemented (this is a **composition**, not
a new mechanism):

- **`CancelEventDefinition`** (`pkg/model/events/cancel.go`, `Type()` →
  `flow.TriggerCancel`) and Cancel as a valid **End Event** trigger
  (`endTriggers` in `pkg/model/events/end.go`, `WithCancelTrigger`) already exist.
- The **completion ledger** + the reverse-order **scope-wide sweep** in **wait
  mode** (`internal/instance/compensation_watch.go` `applyCompensate` →
  `collectCompensation` → `runNextCompensation` → `finishSweep`; the thrower parks
  until the sweep drains, SRD-059) — the "compensate all completed **before**
  control leaves" half.
- The **scope-wide cancel** (`internal/instance/scope_runtime.go` `cancelScope`)
  — the "terminate all running" half.
- The **loop-local scoped End-event resolution** (`evScopeTerminate` →
  `terminateScope`, `internal/instance/event.go` + `scope_runtime.go:496`) — the
  exact pattern a Cancel End Event follows (a direct-resolution End event resolved
  against its enclosing scope, no hub).

**Missing (this SRD adds):** the `isTransaction` marker + its shape rules, the
Cancel-boundary un-defer, and one loop-local abort handler that sequences the
landed primitives. **In scope:** the Transaction variant, the Cancel End abort,
the Cancel boundary exit, `method = compensate` only. **Deferred (ADR-028 §2.8):**
`method = store|image`, nested Transactions, error-driven presumed-abort
auto-compensation, and **deep/recursive** compensation of nested sub-processes'
inner activities (the landed sweep is single-level — §4.4; recursion is an
ADR-026 §2.9 follow-up, orthogonal to Transaction).

## §2 Requirements

### Functional — the model

- **FR-1 — the `isTransaction` marker.** `SubProcess` gains an `isTransaction`
  bool (mirroring `triggeredByEvent`), set by a new `WithTransaction()`
  `SubProcessOption`, read by a new `IsTransaction() bool` getter. No new node
  type — a Transaction **is** an embedded Sub-Process (ADR-028 §2.1). The marker
  is mutually exclusive with `WithTriggeredByEvent()` (a handler is not a
  transaction).
- **FR-2 — Cancel boundary un-defer (narrow).** `flow.TriggerCancel` is added to
  the boundary trigger allow-list (`boundaryTriggers`, `pkg/model/events/boundary.go`),
  but **only** legal on a Transaction Sub-Process host and **always
  interrupting** — a Cancel boundary on any other activity, or a non-interrupting
  Cancel boundary, is rejected (FR-4).
- **FR-3 — validation: Cancel is Transaction-scoped.** A **Cancel End Event** is
  rejected unless it lies within a Transaction Sub-Process's graph; a **Cancel
  boundary** is rejected unless its host `IsTransaction()`. Nested Transactions
  (a Transaction within a Transaction's graph) are rejected. Enforced fail-fast at
  registration (the SRD-052 shape-rule pattern), self-identifying errors.

### Functional — the runtime abort

- **FR-4 — a Cancel End Event inside a Transaction resolves loop-locally.** When a
  track reaches a Cancel End Event, the loop — recognizing `flow.TriggerCancel`,
  as it recognizes `TriggerTerminate` today — emits a new loop event
  (`evTransactionCancel`), mirroring `evScopeTerminate`. It carries the track +
  the resolved enclosing Transaction scope path. The Cancel is **never** handed to
  the EventHub (ADR-028 §2.2). A root-level / non-Transaction Cancel cannot occur
  (rejected at FR-3), so the resolution always finds its Transaction.
- **FR-5 — the abort sequence (order is load-bearing).** The loop handler aborts
  the Transaction scope in order (ADR-028 §2.3):
  1. **Compensate completed** — start the ADR-026 scope-wide sweep in **wait
     mode** over the Transaction scope's ledger (reverse completion order),
     tagged with the Transaction host (`compSweep.txHost`). The Transaction
     scope is flagged **aborting** so a residual track draining to zero
     mid-sweep does not resume the host normally (`decScope`) — the finalize
     owns the teardown, driven off the sweep's own completion, so every
     compensation handler runs **before** the abort proceeds (the ACID-like
     barrier).
  2. **Terminate running** — when the sweep drains, `finishSweep` routes to
     `finalizeTransaction`, which calls `cancelScope(transactionPath,
     PhaseCanceled)` to tear down the residual live tracks under the Transaction
     scope. This runs **after** the sweep, so the sweep's ledger is intact when
     consumed and the residual (never-completed) tracks are simply terminated.
  3. **Leave via the Cancel boundary** — the Transaction **host** is routed onto
     the Cancel boundary's outgoing flow (`cancelBoundaryOn`); the Transaction
     scope closes, reported `Canceled`.
- **FR-6 — the Cancel boundary is a model-declared exit, not a hub waiter.** The
  Cancel boundary is **not** armed as a hub subscription (ADR-028 §2.4). The abort
  handler routes the host onto the boundary's single outgoing flow directly
  (the boundary supplies the exit path; the loop drives it). A Transaction with
  **no** Cancel boundary aborts to no outgoing token — the host is torn down and
  no token continues; the instance settles if no other work remains. Unlike a
  scoped Terminate, the host does **not** resume on its normal outgoing (the
  Transaction did not complete) — Camunda-aligned.
- **FR-7 — observability.** The abort reports the Transaction scope `Canceled`
  (`KindScope`/`PhaseCanceled`, at teardown — consistent with every sibling
  scope-cancel); each compensation runs through the existing `KindCompensation`
  facts (SRD-059), whose `Thrown` fact marks the abort's start. No new phase
  constant — the existing `PhaseCanceled` covers it (ADR-013 v.2 keeps the
  scope-phase set open), so no observability-contract bump.

### Non-functional

- **NFR-1 — composition, not a new mechanism.** No new undo path, no new event
  delivery: the abort reuses `applyCompensate`/`finishSweep` (wait mode),
  `cancelScope`, and the boundary-outgoing routing. The genuinely new code is the
  marker + shape rules + the `evTransactionCancel` handler.
- **NFR-2 — single-writer preserved (ADR-001 v.6).** The abort runs entirely on
  the loop goroutine (resolution, sweep orchestration, cancel, routing); the
  aborting track parks on its `evtCh` for the sweep barrier, as any
  wait-for-completion compensation thrower does.
- **NFR-3 — plain Sub-Process paths unchanged.** A non-Transaction Sub-Process and
  a Transaction that never reaches a Cancel behave identically to today; the
  SRD-049/052/059 suites stay green.
- **NFR-4 — deferred surfaces stay out (ADR-028 §2.8).** No `store`/`image`, no
  nested Transactions, no error-auto-sweep, no deep recursion.
- **NFR-5 — coverage.** Every touched file ≥95% diff-coverage (aim 100%);
  `make ci` green, `-race` clean.

## §3 Models

### §3.1 Model deltas (`pkg/model/`)

- **`activities/subprocess.go` + `subprocess_options.go`** — `isTransaction bool`
  on `subProcessConfig` + the `SubProcess` struct; `WithTransaction()
  SubProcessOption`; `IsTransaction() bool` getter (parallel to
  `IsEventSubProcess()`). `NewSubProcess` validation rejects
  `WithTransaction()` + `WithTriggeredByEvent()` together.
- **`events/boundary.go`** — add `flow.TriggerCancel` to `boundaryTriggers`; the
  Transaction-only + always-interrupting restriction is enforced where the
  boundary attaches to its host (host `IsTransaction()` check) rather than in the
  trigger set alone.
- **Validation (`activities/subprocess.go` `Validate` + the process/graph
  registration walk)** — Cancel End only inside a Transaction; Cancel boundary
  only on a Transaction; no nested Transactions. Self-identifying errors.

### §3.2 Runtime deltas (`internal/instance/`)

- **`event.go`** — a new `evTransactionCancel` track-event kind (sibling of
  `evScopeTerminate`), carrying the aborting track (its `scopePath` is the
  Transaction scope, since a Cancel End Event lies directly in it).
- **`pkg/renv` + `execenv.go`** — `Cancel()` on `RuntimeEnvironment`; `execEnv.Cancel`
  emits `evTransactionCancel` (no root check — a Cancel End is Transaction-scoped).
- **`end.go`** — `EndEvent.Exec` dispatches a Cancel trigger to `re.Cancel()`,
  checked before the emit loop (a Cancel End wins and emits nothing, like
  Terminate).
- **`transaction.go` (new)** — `cancelTransaction(ctx, t)` resolves the
  Transaction scope from `t.scopePath`, flags it **aborting**, and starts the
  wait-mode `collectCompensation(path, "")` sweep tagged with `txHost`;
  `finalizeTransaction(ctx, sweep)` (driven by `finishSweep` when the sweep
  drains) `cancelScope`s the residuals then routes the host onto the Cancel
  boundary's outgoing; `cancelBoundaryOn(node)` finds that boundary.
- **`compensation_watch.go`** — `compSweep.txHost`; `finishSweep` routes a
  txHost-tagged sweep to `finalizeTransaction` instead of resuming a thrower.
- **`scope_runtime.go`** — `scopeEntry.aborting`; `decScope` returns without
  completing an aborting scope.
- **`loop.go`** — `applyScopeAbort` dispatches `evScopeTerminate` /
  `evTransactionCancel` (one grouped case, keeping `apply` under the gocyclo
  limit).
- **`boundary_watch.go`** — `armBoundaries` skips `TriggerCancel` from hub
  registration (the Error/Escalation/Compensation direct-resolution class).

## §4 Analysis

### §4.1 A marker, resolved loop-locally — mirroring scoped Terminate (FR-1/FR-4)

A Transaction reuses the whole embedded-Sub-Process machinery; the only runtime
divergence is the Cancel. A scoped Terminate already shows the shape: a
direct-resolution End event (`TriggerTerminate`) that the loop resolves against
its enclosing scope via `evScopeTerminate` → `terminateScope`, never touching the
hub. A Cancel End Event is the same shape with a richer body — resolve the
enclosing **Transaction** scope (not just any scope), and run the abort instead
of a plain terminate. So `evTransactionCancel` is `evScopeTerminate`'s sibling,
and `cancelTransaction` is `terminateScope` with a compensation sweep in front of
the teardown.

### §4.2 The wait-mode sweep gives the ACID barrier for free (FR-5)

ADR-026's sweep already supports a **wait-for-completion** throw: the thrower
parks and `finishSweep` resumes it once every handler has drained (SRD-059). A
transaction abort **is** a wait-mode scope-wide sweep whose "thrower" is the
Cancel End Event's track and whose continuation is the teardown + exit. Reusing
wait mode gives "compensate all completed before control leaves" with no new
synchronization — the abort handler simply schedules the teardown on the parked
track's resume.

### §4.3 The ledger-survival ordering (FR-5)

`cancelScope` **discards** the ledger of its subtree (`discardLedgers`) — cancelled
work is not compensable. If the abort cancelled first, the sweep would find an
empty ledger. So the abort **sweeps first** (ledger intact), then cancels the
residual running tracks. The running tracks are, by definition, not in the ledger
(only completed activities are), so cancelling them after the sweep loses nothing
compensable. This ordering is the whole correctness content; both primitives are
otherwise reused verbatim.

### §4.4 Single-level compensation — the recursion boundary (NFR-4)

`collectCompensation(path, "")` reads **only** `ls.ledgers[path]` — the
Transaction scope's own ledger — not the ledgers of descendant scopes. So the
abort compensates the Transaction's **direct** completed activities (including a
completed nested Sub-Process **as a single ledger entry** when it carries its own
compensation handler), **not** recursively into that nested Sub-Process's inner
activities. This matches the engine's current compensation model everywhere
(single-level throughout, SRD-059); **deep/recursive** compensation is an
**ADR-026 §2.9 designed-for follow-up**, applies to *all* compensation (not just
Transaction), and is explicitly out of scope here — called out rather than
silently narrowed.

### §4.5 The Cancel boundary as a declared exit (FR-6)

Arming the Cancel boundary as a hub waiter would (a) route a direct-resolution
event through a broadcast path, and (b) reach the host through the
interrupting-boundary teardown, which discards the ledger (§4.3) — both wrong. So
the Cancel boundary is **not** armed; it is a static exit the abort handler reads
for its outgoing flow, exactly as `terminateScope` resumes a parked host but here
onto the boundary's flow rather than the composite's. The boundary's value is on
the diagram (the abort's exit is visible and routable), not as a runtime waiter.

## §6 Test scenarios

| Test | Level | Covers |
|---|---|---|
| `TestTransactionMarkerAndShapeRules` | model | FR-1/FR-2/FR-3 — `WithTransaction`/`IsTransaction`; Cancel End only in a Transaction; Cancel boundary only on a Transaction + interrupting; nested-Transaction + Transaction⊕EventSub rejected |
| `TestCancelBoundaryAllowedOnTransaction` | model | FR-2 — a Cancel boundary builds on a Transaction, rejected elsewhere |
| `TestTransactionCancelCompensatesThenTerminates` | instance | FR-4/FR-5 — a Cancel End aborts: completed inner activities compensate (reverse order) before the running ones are torn down |
| `TestTransactionCancelExitsViaCancelBoundary` | instance | FR-5/FR-6 — control leaves on the Cancel boundary's outgoing after the abort |
| `TestTransactionCancelNoBoundaryCompletesLocally` | instance | FR-6 — a boundary-less Transaction abort emits no outgoing token; the parent continues |
| `TestTransactionSuccessIsPlainSubProcess` | instance | NFR-3 — a Transaction that never cancels completes on its normal outgoing, unchanged |
| `TestTransactionCancelOrderingUnderRace` | instance | FR-5/NFR-2 — the sweep barrier precedes the teardown (`-race`) |
| `TestTransactionCancelE2E` | thresher | FR-1–FR-7 — a booking transaction (reserve → charge) that cancels, compensating the reservation, exiting the Cancel boundary to a "booking failed" flow |

## §7 Milestones

| # | Scope | Files |
|---|---|---|
| **M1** | Model: `isTransaction` marker + `WithTransaction`/`IsTransaction`; Cancel boundary un-defer; shape-rule validation. | `pkg/model/activities/subprocess*.go`, `pkg/model/events/boundary.go` |
| **M2** | Runtime abort: `evTransactionCancel` + `cancelTransaction` (wait-mode sweep → `cancelScope` → route via Cancel boundary); Cancel End Event emits it. | `internal/instance/event.go`, `transaction.go` (new), `end.go` handling, `scope_runtime.go` |
| **M3** | e2e + `examples/transaction-sub-process/` (booking-cancel) + docs (CHANGELOG, iteration/composition guide, conformance tracker row 3, README EN+RU) + ADR-018 §2.7 / ADR-023 §2.9 link annotations; flip SRD-061 + ADR-028 Accepted. | `pkg/thresher/…`, `examples/…`, docs |

## §8 Cross-doc

- **Implements** [ADR-028 v.1](../design/ADR-028-transaction-sub-process.md) §2.1–§2.9.
- **Upstream** [ADR-026 v.1](../design/ADR-026-compensation-events.md), [ADR-023 v.3](../design/ADR-023-sub-process-and-call-activity.md), [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md), [ADR-006 v.4](../design/ADR-006-events-and-subscriptions.md), [ADR-013 v.2](../design/ADR-013-instance-observability.md), [ADR-001 v.6](../design/ADR-001-execution-model.md) — all up/sideways, version-pinned.
- **Related** SRD-059, SRD-049, SRD-052 (sideways, number-only). No downward references.
- **M3 also annotates** ADR-018 §2.7 (Cancel boundary un-deferred by ADR-028) and ADR-023 §2.9 (Transaction fulfilled by ADR-028) in place — link, not bump (the deferral-closed convention).

## §9 Definition of Done

- FR-1…FR-7 wired and covered by §6; the e2e green; the SRD-049/052/059 suites
  stay green (NFR-3).
- `make ci` green (diff-coverage ≥95% touched; `-race`; govulncheck; all modules).
- `examples/transaction-sub-process/` runs and exits 0 (binary gitignored).
- Conformance tracker row 3 advanced (Transaction ✅; `CancelEventDefinition`
  execution ✅); CHANGELOG `[Unreleased]`; guide note; README EN+RU.
- `/check-srd` PASS. ADR-028 flips **Accepted** with this landing; ADR-018 §2.7 /
  ADR-023 §2.8 link-annotated.

## §10 Implementation summary

### §10.1 Stages by commit (branch `feat/transaction-subprocess`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| M1 (model) | `580c9e5` | `WithTransaction()`/`IsTransaction()`; Cancel End + Cancel-boundary shape rules; validation (Cancel-only-in-Transaction, Cancel-boundary-only-on-Transaction, no nested Transaction) | `TestTransactionMarkerAndShapeRules`, `TestValidateCancelEndPlacement`, `TestCancelBoundaryRules`, `TestProcessRejectsTopLevelCancelEnd` |
| M2 (runtime) | `be51edd` | `renv.Cancel` + `end.go` Cancel dispatch; `evTransactionCancel`; `execEnv.Cancel`; `applyScopeAbort`; `transaction.go` (`cancelTransaction` / `finalizeTransaction` / `cancelBoundaryOn`); `compSweep.txHost`; `scopeEntry.aborting` + `decScope` guard; `armBoundaries` Cancel-skip | `TestTransactionCancelAbort` / `…NoBoundary` / `…NoCompensation` / `…AbortGuards`; `TestEndEventCancelWins` |
| M3 (e2e + example) | `b165c3a` | thresher e2e; `examples/transaction-sub-process/` | `TestTransactionCancelE2E` |

`make ci` green on the landing: diff-coverage 99.0% of 193 changed lines (min
95%), `-race` clean, govulncheck clean, all modules.

### §10.2 Empirical findings vs the draft

- **The abort holds the scope, not the aborting track.** The draft (FR-5) parked
  the aborting Cancel-End track and resumed it via `finishSweep`. Empirically the
  aborting track lives *inside* the doomed scope — `cancelScope` tears it down, so
  resuming it is meaningless; the **host** is the continuation. Implemented as a
  `scopeEntry.aborting` flag: `decScope` returns without completing an aborting
  scope, so a residual draining to zero mid-sweep cannot resume the host normally,
  and `finalizeTransaction` (driven off the sweep) owns the teardown + host
  routing. Same behavior, cleaner mechanism, single-writer preserved.
- **No `enclosingTransaction` walk-up.** A Cancel End Event lies *directly* in the
  Transaction scope (validated), so `cancelTransaction` resolves it as
  `t.scopePath` — no scope-tree walk.
- **No-boundary shape is Camunda-aligned, not scoped-Terminate.** The draft (FR-6)
  said the parent "continues past" a boundary-less Transaction. Empirically the
  host is torn down with **no** continuation (the Transaction did not complete, so
  it does not resume on its normal outgoing) — corrected in FR-6.
- **No new observability phase (Option A).** The abort reuses `KindScope` /
  `PhaseCanceled` (every sibling cancel already emits it) + the `KindCompensation`
  sweep facts — avoiding an ADR-013 flip.

### §10.3 Backlog

- **Deep (recursive) scope compensation on abort** — `collectCompensation` is
  single-level (exact scope), so a Transaction containing nested sub-processes
  compensates one level; recursive descent is the ADR-026 §2.9 follow-up.
- `store` / `image` transaction outcomes, nested Transactions, and error-driven
  presumed-abort auto-compensation stay designed-for, out of scope per ADR-028.

## Open questions

None.
