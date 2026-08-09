# SRD-086 — Leaf-activity Multi-Instance execution

| Field | Value |
|---|---|
| Status | Accepted (2026-08-09) |
| Date | 2026-08-08 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-025 v.2](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.2/§2.5/§2.6 (the leaf iteration mechanisms the accepted contract prescribes and the implementation never built) |
| Upstream | [ADR-006 v.5](../design/ADR-006-events-and-subscriptions.md) §2.9 (the delivery contract a waiting leaf iteration rides), [ADR-033 v.4](../design/ADR-033-persistence-and-state.md) §2.10 (composite capture fidelity, which the parallel-leaf realization inherits) |
| Related | [SRD-055](SRD-055-multi-instance-sequential.md) / [SRD-056.A](SRD-056.A-multi-instance-parallel.md) (the composite decorators this extends; frozen one-shots), [SRD-085](SRD-085-in-instance-delivery.md) (iteration-correlated delivery, which parallel leaf catches reuse) |
| Tracking | #305 (the review thread that surfaced it) — the defect gets its own issue at the PR |

**A leaf task decorated with Multi-Instance silently runs ONCE.**
`executeStep` routes MI to a decorator only for a `scopeHost`; a leaf
falls through to plain `executeNode`, and nothing guards the gap — a
3-item collection completes with one execution and no complaint
(empirically probed on this branch). ADR-025 v.2 *prescribes* the leaf
mechanisms; this SRD builds them: sequential in place (the
Standard-Loop leaf pattern), parallel as per-instance scopes each
running one spawned leaf track (the §2.2 exception realized with the
existing fan-out, barrier and checkpoint machinery).

## §1 Background (verified)

- **The silent fall-through.** `executeStep`
  (`internal/instance/std_loop.go:47-73`): a Standard Loop has both
  branches (`runCompositeLoop` composite / `runStandardLoop` leaf); MI
  has only the composite branch — `if _, ok := step.node.(scopeHost)`
  with **no else**, so a leaf MI runs `executeNode` once. `WithLoop`
  (`pkg/model/activities/activity_options.go:138`) attaches MI to any
  activity without a guard. The empirical probe: a 3-item parallel MI
  service task → `state=Completed runs=1`.
- **The contract already decides the mechanisms.** ADR-025 v.2 §2.2:
  a leaf "iterates **in place**: … each pass in a **fresh execution
  frame**"; and "**Parallel Multi-Instance is the exception that
  always needs a distinct per-instance scope**" with identity derived
  from activity + ordinal (§2.5) — with no leaf carve-out. §2.6 binds
  the split item per iteration; the completion condition (§2.7) and
  runtime attributes (§2.9) apply to the family.
- **Everything the parallel realization needs exists.**
  `handleFanOut`/`miGroup`/the re-arm barrier
  (`internal/instance/mi_parallel.go:263-337`) fan out N scopes and
  serialize drains; `openParallelInstance` (`:384-437`) opens a scope,
  binds `loopCounter` + the split item, and seeds via `seedScope` —
  the ONLY composite-specific step. A scope whose single track ends
  drains naturally (`decScope`), `captureParallelOutput` reads the
  output before close, and `MIGroupRecord`+`TrackRecord` already
  capture scopes-with-tracks (SRD-082) — parallel-leaf checkpoint
  fidelity comes from the existing schema.
- **The sequential leaf pattern exists too.** `runStandardLoop`
  (`std_loop.go:76+`) is the in-place leaf iterator (per-pass
  `executeNode`, fresh frame); `runMISequential`'s `miIterator`
  (`internal/instance/mi.go:409+`) carries the MI-specific
  resolve/bind/completion/publish steps.

## §2 Requirements

- **FR-1 — sequential leaf MI runs in place.** A non-`scopeHost` MI
  activity with `IsSequential()` runs N passes on the host track's
  runner: per pass, the split item and the 0-based `loopCounter` bind
  into the pass's fresh frame, `executeNode` runs the activity, the
  output item (when declared) is captured into the staging slot, and
  the completion condition (evaluated after each pass, §2.7) stops
  early. On exit the assembled output publishes once and the single
  outgoing flow is followed once.
- **FR-2 — parallel leaf MI fans out per-instance scopes.** A
  non-`scopeHost` parallel MI reuses `runMIParallel` unchanged; the
  fan-out's per-instance seeding gains the leaf branch: instead of
  `seedScope` (which needs inner nodes), the loop **spawns one track
  at the leaf node itself** inside the instance scope. Everything
  else — the barrier, output capture by ordinal, completion-condition
  cancel, boundary interaction, checkpoint capture/restore — is the
  existing machinery, now exercised for a leaf.
- **FR-3 — the leaf track executes the activity exactly once.** The
  spawned leaf track runs `executeNode` for the activity and ends —
  its own MI decoration MUST NOT recurse (the iteration is driven by
  the group, not by the track), realized by spawning in a mode that
  executes the node plainly.
- **FR-4 — a waiting leaf works under parallel MI.** The canonical
  "MI event node": a ReceiveTask (or other waiting leaf) under
  parallel MI parks its per-iteration track; delivery follows ADR-006
  v.5 §2.9 — signals fan out to every iteration, messages route by
  `WithIterationCorrelation`, and a keyless concurrent message wait
  refuses (SRD-085's machinery, unchanged).
- **FR-5 — checkpoint fidelity from day one.** A parallel leaf MI
  killed mid-flight restores at its position over the EXISTING schema
  (the group record + per-scope leaf tracks); a sequential leaf MI
  restores via `TrackRecord.MI` (the SRD-082 iteration mirror, which
  `drivesOwnIteration` already reports for ANY MI node). A new
  construct that couldn't checkpoint would recreate the silent hole
  SRD-082/083 closed.
- **NFR-1** — race-clean; diff-coverage ≥95% (aim 100%); touched
  functions ≥80%.

## §3 Models

No new checkpoint records and no new public API: the model surface
(`WithLoop(mi)` on any activity) finally means what it declares.

**Worked trace (parallel leaf).** `charge` is a ServiceTask under
parallel MI over `orders=[o1,o2,o3]`. Fan-out opens
`/p/sp-charge-0..2`, each binding `loopCounter`+`order`, each spawning
one track at `charge`. The tracks run concurrently; each op reads ITS
`order`; outputs land in staging by ordinal; the host resumes through
the barrier as each scope drains; after the third drain the assembled
output publishes and `charge` follows its outgoing flow once. A kill
after o2's drain restores two closed ordinals + one open scope with
its leaf track at `charge` — the SRD-082 machinery, no schema change.

**Worked trace (sequential leaf).** The same task sequential: pass k
binds `order=o[k]`/`loopCounter=k` in a fresh frame, runs the op,
stages the output, evaluates the condition; a true condition stops
early; the output publishes once.

## §4 Analysis & decisions

- **Parallel leaf = scope + one spawned track, not in-place
  goroutines.** The accepted §2.2 already prescribes per-instance
  scopes for parallel; a scope's single leaf track gives data
  isolation, an event-processor identity per iteration (the SRD-085
  delivery unit), boundary/cancel semantics and checkpoint fidelity
  for free. In-place N-goroutine invocations would re-need all four.
  (Rejected: in-place parallel; rejected: minted per-iteration event
  definitions — settled during design, superseded by per-track
  processors.)
- **Sequential leaf = in place, not scopes.** §2.2's rationale stands:
  a Task is not a scope container, and serial passes need no
  concurrent isolation — the fresh frame is the isolation. The host
  track serially reuses its own processor identity for waits (one wait
  in flight at a time).
- **No validation refusal.** The gap closes by implementation, not by
  guard: `WithLoop(mi)` on a leaf now means N executions, exactly what
  the model declares.
- **`executeStep` grows the two leaf branches** mirroring the
  Standard-Loop split — the routing stays a readable four-way table.

## §5 API deltas

None. Behavioral: a leaf MI executes N times instead of silently once.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | sequential leaf runs N passes (`internal/instance`) | FR-1: 3 items → 3 runs, per-pass item/loopCounter, output assembled in order |
| T-2 | sequential completion condition (`internal/instance`) | FR-1: a true condition stops early; completed outputs stand |
| T-3 | parallel leaf fans out (`internal/instance`) | FR-2/FR-3: 3 items → 3 concurrent runs, each reading ITS item; outputs land by ordinal; the activity's flow follows once |
| T-4 | parallel completion condition cancels (`internal/instance`) | FR-2: the §2.7 cancel path over leaf scopes |
| T-5 | waiting leaf under parallel MI (`pkg/thresher`) | FR-4: a ReceiveTask MI with iteration correlation — out-of-order envelopes serve the matching iterations |
| T-6 | parallel leaf kill-and-resume (`pkg/thresher`) | FR-5: restore at position over the existing schema; completed ordinals never re-run |
| T-7 | sequential leaf kill-and-resume (`internal/instance`) | FR-5: the TrackRecord.MI mirror resumes at pass k |
| T-8 | the silent-single-run regression (`internal/instance`) | §1's probe inverted: the 3-item leaf MI now runs 3 times |

## §7 Milestones

- **M1 — sequential leaf in place.** FR-1; T-1, T-2, T-8.
  `feat(instance): a sequential leaf Multi-Instance runs in place (SRD-086 M1)`.
- **M2 — parallel leaf as per-instance scopes.** FR-2, FR-3; T-3, T-4.
  `feat(instance): a parallel leaf Multi-Instance fans out per-instance scopes (SRD-086 M2)`.
- **M3 — waits + fidelity + docs.** FR-4, FR-5; T-5, T-6, T-7; the MI
  guide's leaf sections, CHANGELOG.
  `feat(instance): waiting leaves and checkpoint fidelity for leaf MI; docs (SRD-086 M3)`.

## §8 Cross-doc

- Implements **ADR-025 v.2** §2.2/§2.5/§2.6 — no ADR change: the
  contract prescribed this; the implementation catches up.
- Upstream: **ADR-006 v.5** §2.9, **ADR-033 v.4** §2.10.
- Related: **SRD-055**/**SRD-056.A** (frozen), **SRD-085**.

## §9 Definition of Done

- [x] FR-1…FR-5 implemented; every §6 test exists and passes.
- [x] `make ci` green; diff-coverage ≥95% (aim 100%); touched
      functions ≥80%.
- [x] The MI guide documents leaf execution; the CHANGELOG records the
      fixed silent single-run.
- [x] §10 filled.

## §10 Implementation summary

Landed on `feat/composite-followups` in three milestones — doc
`19223c3`, M1 `66458aa` (the in-place sequential leaf), M2 `3d71bf3`
(the parallel leaf's per-instance scopes), M3 `be4e0df` (the waiting
leaf, checkpoint fidelity, docs) — plus a coverage round adding the
`AddEventKey` passthrough, blank-iteration-key and instance-ProcessID
pins.

Verification: `make ci` exit 0 end to end; **diff-coverage 96.7% of
511 changed coverable lines** (min 95%); all suites race-clean;
golangci-lint incl. tests 0 issues. Every §6 scenario has its named
test (T-1/T-8 `TestLeafMISequentialRunsAllPasses`, T-2
`…CompletionStops`, T-3 `TestLeafMIParallelFansOut`, T-4
`…CompletionCancels`, T-5 `TestLeafReceiveTaskMIRouting`, T-6
`TestLeafMIParallelKillAndResume`, T-7
`TestLeafMISequentialKillAndResume`), plus five error-path pins.

Adjustments the implementation required, each folded here rather than
deferred: `runMIParallel`'s exits ran `executeNode` to follow the
outgoing flow — harmless for a composite host, but for a leaf that IS
the activity (an extra execution at the host scope), so `miParallelExit`
follows the declared flow directly; the fan-out's corrupt-graph guard,
keyed on "not a composite", was re-keyed on the real corruption (a node
with no MI decoration); the sequential leaf needed a persist point of
its own (`scopeLeafPass` — a leaf opens no scope, so the run emitted
none); restore re-marks a leaf instance track `leafPlain`; and the MI
HOST must not register its waits (only the per-instance tracks do).

## Open questions

*None — §4 records the resolved design points (scope-per-parallel-leaf
vs in-place, sequential in place, implement-not-guard, the four-way
routing).*
