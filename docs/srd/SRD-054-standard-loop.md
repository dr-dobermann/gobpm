# SRD-054 — Standard Loop

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-07-21 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-025 v.2](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.2–§2.3 (the Standard Loop slice), §2.12 (composite iteration as an off-loop decorator); epic #88 |
| Upstream | [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) (the single-writer loop the decorator requests scope operations from), [ADR-010 v.2](../design/ADR-010-process-data-model.md) (the execution frame that isolates each leaf iteration), [ADR-023 v.2](../design/ADR-023-sub-process-and-call-activity.md) (the composite child-scope open/drain/close lifecycle), [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md) (a boundary arms once and guards the whole loop), [ADR-001 v.6](../design/ADR-001-execution-model.md) (the loop owns node execution) |
| Refines | — |

## §1 Background

BPMN 2.0 §13.3.6 lets an activity carry `StandardLoopCharacteristics` — a
structured `while`/`until` loop that re-runs the inner activity **sequentially**
while a boolean `loopCondition` holds. This SRD lands Standard Loop on **both** a
leaf **Task** and a **composite** (Sub-Process / Call Activity).

**This is the first slice of the ADR-025 v.2 decorator re-landing** (§2.12). The
prior landing drove composite iteration *control* — resolve the continuation,
re-open the scope — **on the per-instance loop goroutine**, via a
loop-side `compositeIterator` seam (`firstOpen`/`afterDrain`) called from
`onScopeOpen`/`resumeScopeHost`. ADR-025 v.2 §2.12 decided that control belongs on
the **activity's own off-loop execution** — an *iteration decorator* — which
**requests** scope operations from the single-writer loop rather than performing
them. Standard Loop is the simplest composite iteration, so it is the slice that
introduces the decorator engine (the request/response scope protocol); the
sequential-MI, parallel-MI, and behavior slices re-land on it afterward.

**The leaf-Task loop is unchanged** — it already runs in place on the task's own
runner goroutine (a bounded `for` around `executeNode`), which is off the loop
goroutine and needs no protocol. §2.12's scope is composite activities only.

This SRD **deletes and reuses** the SRD-054 slot: its prior (loop-goroutine-driven
composite) content is replaced in place with the decorator design; the model and
leaf-Task requirements below are the landed reality, restated so the document is
self-sufficient.

ADR-025 §2.2 chose the iteration **mechanism by activity kind**: a leaf Task
iterates **in place** (fresh frame per pass, ADR-010); a composite iterates by
**re-opening its child scope per iteration** (the ADR-023 open/drain/close
lifecycle). §2.12 fixes *who drives* the composite mechanism — the decorator, off
the loop. Both kinds follow the activity's single outgoing flow **once**, at loop
exit, and let a boundary event on the looped activity arm **once** and guard every
iteration.

## §2 Requirements

### Functional — the model (landed, unchanged)

- **FR-1 — `StandardLoopCharacteristics` type.** `LoopCharacteristics` is a sealed
  marker interface; `StandardLoopCharacteristics` carries `loopCondition` (a
  `data.FormalExpression`, bool), `testBefore` (bool, default `false`), and an
  optional `loopMaximum` (`*int`, nil = unbounded); it embeds
  `foundation.BaseElement`.
- **FR-2 — construction validates all inputs.** `NewStandardLoop(loopCondition,
  opts…)` rejects a nil `loopCondition`, a non-`bool` `ResultType()`, and a
  `loopMaximum ≤ 0`, at construction, with a self-identifying error.
- **FR-3 — `Activity.Validate` cross-check.** An activity carrying **both** a
  Standard-Loop and a Multi-Instance characteristic is rejected (ADR-025 §2.1).
- **FR-3a — an Event Sub-Process rejects iteration.** A `triggeredByEvent`
  `SubProcess` carrying any `LoopCharacteristics` fails validation (an
  event-instantiated handler has no token-driven activation to iterate).

### Functional — leaf-Task execution, in place (landed, unchanged)

- **FR-4 — in-place re-execution.** A leaf activity carrying
  `StandardLoopCharacteristics` is re-executed once per pass; each pass opens a
  **fresh execution frame**, so iterations are isolated with no new construct.
- **FR-5 — `testBefore` semantics.** `false` (default) → post-tested (`do…while`);
  `true` → pre-tested (`while`, zero iterations possible).
- **FR-6 — `loopMaximum` cap.** At most `loopMaximum` iterations run when set.
- **FR-7 — single outgoing flow at exit.** The activity's outgoing flow is
  followed **once**, after the loop terminates.

### Functional — composite execution, the off-loop decorator (reworked)

- **FR-8 — the composite host drives its own iteration off the loop.** A looped
  composite activity iterates on its **own runner goroutine** (a new
  `runCompositeLoop`, invoked from `executeStep` before the park path, mirroring
  how `runStandardLoop` intercepts a leaf loop). Per pass the decorator: (a)
  **requests** a scope-open and blocks for the loop's acknowledgement; (b) **parks
  for drain** — the inner scope drains and the loop delivers `scopeDone` on the
  host's `evtCh` (the existing mechanism); (c) evaluates the continuation
  (`loopCounter`++, `loopMaximum`, `loopCondition` with `testBefore`) **off the
  loop**; (d) repeats, or completes and follows the outgoing flow **once**. The
  host no longer *parks for control* between passes — only for each pass's drain.
- **FR-8a — the request/response scope protocol.** The decorator never mutates
  loop-owned state (scopes, positions, arming) directly. It sends a `scopeRequest`
  on a new loop-serviced `scopeReq` channel and blocks on a per-request buffered
  reply channel; the loop performs the mutation on its own goroutine and replies.
  For Standard Loop the only roundtrip is **`reqOpenScope`** (open the child scope,
  seed the inner tracks, arm the scope handlers, reply with the opened path).
  **Scope close stays on the existing drain path** (`completeScope`), so no
  close-roundtrip is needed and there is no double-close. The protocol clones the
  existing `taskReq`/`taskRoundtrip` (and `callReq`) pattern verbatim.
- **FR-9 — boundary arms once.** A boundary event on the looped composite arms
  **once** and guards every iteration: the host still **parks** between passes and
  emits no `evMoved`/`evEnded` until loop exit, so `armBoundaries` fires once (on
  arrival) and `disarmBoundaries` once (at exit) — unchanged from today.

### Functional — `loopCounter`, observability & front door (landed, unchanged)

- **FR-10 — `loopCounter`.** A 0-based per-iteration ordinal is published so the
  `loopCondition` and the inner activity's expressions read it by name; read-only,
  engine-maintained, each iteration sees its own value.
- **FR-11 — observability.** An iteration Fact per pass (loop enter / each
  iteration with `loopCounter` / loop exit) through the ADR-013 v.2 reporter.
- **FR-12 — front door.** `examples/standard-loop/`, the iteration guide,
  `CHANGELOG.md`, the conformance tracker row, and the READMEs (EN + RU) reflect
  the capability (already landed; the decorator rework does not change the
  user-visible behavior, so these need no user-facing change beyond a CHANGELOG
  note).

### Non-functional

- **NFR-1 — no new event kinds for the leaf path.** The leaf loop stays a bounded
  `for` around `executeNode`; no `trackEvent` kind, no loop round-trip.
- **NFR-2 — reuse the expression mechanism.** `loopCondition` is evaluated through
  the existing `ExpressionEngine().Evaluate` + `bool` path via a transient frame
  (`evalLoopCond`), which already runs **off** the loop goroutine — the decorator
  needs no loop coordination to test the condition.
- **NFR-3 — the single-writer invariant is preserved (ADR-017 v.1).** The loop
  remains the sole writer of `ls.scopes`, the data plane, `ls.waiting`, positions,
  and boundary/handler arming. The decorator only *requests* mutations; those
  methods stay reachable only from loop-goroutine code. No lock, no shared mutable
  state.
- **NFR-4 — deadlock-free by construction.** The decorator blocks only on channels
  the **loop** writes (the reply channel, `evtCh`), both buffered and both honoring
  `ctx.Done()` / `inst.loopDone`; the loop never blocks on the decorator (the reply
  send is to a cap-1 buffer). The wait graph is a DAG (decorator→loop), never a
  cycle.
- **NFR-5 — the existing suites are the safety net.** The landed Standard-Loop
  tests (leaf + composite, unit + thresher e2e + the example smoke) stay **green
  throughout** the rework; behavior is unchanged, only the composite execution
  mechanism moves.
- **NFR-6 — coverage.** Every file this SRD creates/updates finishes at ≥95%
  diff-coverage (aim 100%), delivered with the change; `make ci` green.

## §3 Models

### §3.1 Model type family (`pkg/model/activities/loop.go`) — landed, unchanged

`LoopCharacteristics` is a sealed marker interface; `StandardLoopCharacteristics`
(embedding `foundation.BaseElement`) carries `loopCondition` / `testBefore` /
`loopMaximum`, built by `NewStandardLoop(loopCondition, opts…)` with the FR-2
guards and the `WithTestBefore()` / `WithLoopMaximum(n)` options. Accessors
`LoopCondition()` / `TestBefore()` / `LoopMaximum() (int, bool)` expose them to the
runtime. `Activity.Validate` carries the loop⊕MI-exclusivity guard (FR-3);
`subprocess.go`'s event-sub validator carries FR-3a. **No model change in this
SRD** — the decorator rework is runtime-only.

### §3.2 Runtime deltas (`internal/instance/`) — the decorator engine

- **`scope_decorator.go` (new) — the protocol types + the runner.**
  ```go
  type scopeRequest struct {
      host  *track
      node  flow.Node
      reply chan scopeReply
  }
  type scopeReply struct {
      err       error
      scopePath scope.DataPath // opened child path
  }
  ```
  A single request shape (open) is all Standard Loop needs — the scope close stays
  on the drain path (§4.3), and there is no counter-bind roundtrip: the decorator
  binds `loopCounter` itself off the loop (§4.6). No request-kind enum is needed
  yet; the sequential/parallel-MI slices add kinds as they need them.
  `(*track).runCompositeLoop(ctx, step, sl) ([]*flow.SequenceFlow, error)` — the
  off-loop await-each driver (FR-8): for each pass it sets `t.loopCounter = pass`
  and `bindLoopCounterAt(t.scopePath, pass)` (off the loop, like the leaf), pre-
  tests if `testBefore` (reuse `evalLoopCond`), `requestScope{}` (**one**
  open-roundtrip), `awaitScopeDrained` (park on `evtCh` for `scopeDone`), then the
  `loopMaximum` cap; on exit it calls `executeNode` once so the composite selects
  its single outgoing flow (`SubProcess.Exec` → `selectOutgoing`). `requestScope`
  clones `taskRoundtrip` (send on `inst.scopeReq`, block on the cap-1 reply, select
  on `ctx`/`loopDone`); `awaitScopeDrained` mirrors `run()`'s `evtCh` park (honors
  `ctx`/channel-close for interrupt/terminate).

- **`instance.go` — the request channel.** `scopeReq chan scopeRequest` added
  beside `taskReq`/`jobReq`/`callReq` and initialized in the constructor.

- **`loop.go` — the loop-side handler.** A `case req := <-inst.scopeReq:` arm in
  the loop `select`, dispatching to `handleScopeRequest` (loop goroutine), which
  `OpenScope`s the child, records the `scopeEntry`, marks the host `waiting`,
  `seedScope`s the inner tracks, `armScopeHandlers`, and replies with the opened
  path — the exact single-writer shape of `handleTaskRequest`. (The counter is
  already bound by the decorator, §4.6.)

- **`track.go` — the interception (checkNodeType).** A `scopeHost` node that also
  carries `standardLoopOf(node) != nil` **does not park** (`return nil`) — it
  drives itself via `runCompositeLoop`. Every other composite (plain, Multi-
  Instance) still `parkScopeHost`s for the loop-driven scope. Leaf loops and
  non-composites are untouched.

- **`std_loop.go` — the executeStep route.** `executeStep` routes a
  `standardLoopOf(node) != nil` node that is a `scopeHost` to `runCompositeLoop`
  and a leaf to `runStandardLoop` (unchanged). The Standard-Loop continuation
  logic (`evalLoopCond`, `loopMaximum`, `testBefore`) is reused by
  `runCompositeLoop`. **Leaf `runStandardLoop` is untouched.**

- **`scope_runtime.go` — the drain delivers to the decorator.** `resumeScopeHost`
  gains a top guard: for a `standardLoopOf(entry.node) != nil` composite it just
  `dispatchToParked(scopeDone)` (no `afterDrain` — the decorator drives re-entry),
  before the Multi-Instance `compositeIterator`/`afterDrain` seam. `completeScope`
  still closes the drained scope (FR-8a). The old-seam removal (the `onScopeOpen`
  `firstOpen` short-circuit, the `standardLoopIterator` callbacks — now dead for a
  Standard-Loop composite) is **M2** (kept live here only for sequential MI until
  it re-lands). A **plain** (non-looped) composite keeps the current
  `evScopeOpen`→`onScopeOpen`→`resumeScopeHost` path unchanged.

## §4 Analysis

### §4.1 The decorator realizes ADR-025 v.2 §2.12

§2.12 prescribes that composite iteration control runs on the activity's own
off-loop execution, requesting scope operations from the single-writer loop.
`runCompositeLoop` runs on the host's runner goroutine (where every node's `Exec`
already runs); it drives the loop with ordinary control flow (an await-each `for`)
and touches loop-owned state only through `scopeReq`. This is the locus §2.12
dictates — the SRD realizes the ADR, it does not deviate.

### §4.2 The protocol clones an existing pattern (FR-8a)

The engine already round-trips a runner→loop request and blocks for a
single-writer reply: `taskReq`/`taskRoundtrip` (UserTask distribution) and
`callReq`/`callRequest` (Call Activity completion). `scopeReq` is the same shape —
a request channel serviced in the loop `select`, a per-request cap-1 reply channel,
the caller selecting on `ctx.Done()`/`loopDone`. No new synchronization primitive
is invented; the decorator reuses the proven roundtrip.

### §4.3 Only `reqOpenScope`; the scope close stays on the drain path

A pass opens a scope, the inner graph runs, the scope drains (inner tracks
`decScope` → `completeScope`), and the host resumes. The **drain and close already
happen on the loop** in `completeScope`; the decorator learns of the drain via the
existing `scopeDone` delivery. So the decorator needs no `reqCloseScope` — adding
one would double-close (the drain path already closed the scope). The only
mutation the decorator must *initiate* is the **open** (there is no drain-path
trigger for it), hence a single `reqOpenScope` roundtrip (plus a `reqBindCounter`
for the loop-owned counter write). This is the minimal correct protocol.

### §4.4 Deadlock-freedom (NFR-4)

The decorator waits on: (i) the reply channel — written only by the loop, cap-1
buffered; (ii) `evtCh` for `scopeDone` — written only by the loop via
`dispatchToParked` (the buffered slot guarantees the loop's send never blocks).
Both waits select on `ctx.Done()` and `inst.loopDone`, so a terminate/interrupt
unblocks the decorator. The loop, in `handleScopeRequest`, never blocks on the
decorator (its reply send is non-blocking into the cap-1 buffer). The wait graph
is therefore a DAG rooted at the decorator pointing to the loop — no cycle, no
self-emit-on-the-loop-goroutine (the class of bug §2.12 removes). The loop's own
`emit` into `inst.events` remains guarded by `<-inst.loopDone`.

### §4.5 Boundary arms once across iterations (FR-9)

A boundary arms on `evMoved` onto the composite and disarms on `evEnded`. Because
the looped host **parks** on `evtCh` for each pass's drain and stays on the same
step (no `evMoved`/`evEnded` mid-loop), `armBoundaries` fires once on arrival and
`disarmBoundaries` once when `runCompositeLoop` returns the outgoing flows at loop
exit — the desired BPMN semantic (a boundary timer spans the whole loop),
unchanged from the prior landing.

### §4.6 The continuation test runs off the loop; `loopCounter` binds at the host scope (NFR-2)

`evalLoopCond` evaluates `loopCondition` against a transient read-only frame
(`openFrameAt` + `Discard`); it performs no loop-owned mutation and already runs on
the runner goroutine. The decorator calls it directly between passes — no protocol
roundtrip for the test. `loopMaximum` and `testBefore` are plain arithmetic/branch
on the decorator.

`loopCounter` must be bound at the **host** scope, not (only) the child: a
post-tested loop evaluates `loopCondition` *after* the pass's child scope has
drained and closed, so a counter bound only in the child would be gone by the test.
The **decorator binds it itself, off the loop** — `runCompositeLoop` calls
`bindLoopCounterAt(host.scopePath, pass)` at the top of each pass, exactly as the
leaf `runStandardLoop` does. This is a data-plane write (mutex-protected), not a
scope-lifecycle mutation, so it is safe off the loop; and it *must* be off the loop
because the continuation test reads `loopCounter` **before** the scope-open request
(the bind→test→open order, matching `runStandardLoop`). Only the scope-lifecycle
operations (open / seed / arm) go through the `reqOpenScope` roundtrip; the counter
bind does not. This is where the design refined during implementation from "the
loop binds on open" to "the decorator binds off-loop," keeping the leaf and
composite loops symmetric.

### §4.7 Scope guard — Standard-Loop composite only

`runCompositeLoop` is entered only for a **looped composite Standard Loop**
(`standardLoopOf(node) != nil` and the node is a `scopeHost`). A **plain**
(non-looped) composite keeps `parkScopeHost` → `onScopeOpen` → single resume; the
**Multi-Instance** seam (`mi.go`, `mi_parallel.go`) is untouched by this SRD and
re-lands on the decorator in the sequential/parallel slices. This bounds the blast
radius to the one path the decorator proves.

## §6 Test scenarios

| Test | Level | Covers |
|---|---|---|
| `TestScopeRequestRoundtripOpens` | instance | FR-8a — `handleScopeRequest(reqOpenScope)` opens the scope, seeds tracks, arms handlers, replies with the path (mirrors the `handleTaskRequest` unit tests) |
| `TestScopeRequestBindCounter` | instance | FR-8a — `reqBindCounter` binds `loopCounter` at the host path on the loop goroutine |
| `TestScopeRequestUnblocksOnTerminate` | instance | NFR-4 — a pending roundtrip returns on `ctx`/`loopDone` cancel, no goroutine leak |
| `TestLoopedSubProcessReopensPerIteration` | instance | FR-8 — the decorator re-opens the child scope N times (existing test, stays green) |
| `TestLoopedSubProcessPreTestedZero` | instance | FR-5/FR-8 — a pre-tested false condition runs zero passes (existing, green) |
| `TestLoopedSubProcessMaximumCaps` | instance | FR-6/FR-8 — `loopMaximum` caps composite passes (existing, green) |
| `TestLoopedSubProcessEmitsIterationFacts` | instance | FR-11 — one iteration Fact per pass (existing, green) |
| `TestLoopedSubProcessBoundarySpansIterations` | instance | FR-9 — a boundary on a looped composite arms once and fires across ≥2 passes |
| `TestLoopedSubProcessInterruptMidIteration` | instance | NFR-4 — an interrupting boundary / terminate mid-pass unblocks the decorator and tears the inner scope down |
| `TestStandardLoopSubProcessE2E` | thresher | FR-8/FR-10 end-to-end through the public engine (existing, green) |
| `TestStandardLoopLeafE2E` | thresher | FR-4–FR-7 leaf loop unchanged (existing, green) |
| `examples/standard-loop` smoke | example | FR-12 — runs to completion, exits 0 (existing) |

The leaf-path unit tests (`TestStandardLoopRunsWhileConditionHolds`, …) must be
**untouched** — the leaf path does not change.

## §7 Milestones

| # | Scope | Files |
|---|---|---|
| **M1** | The **protocol + the decorator runner, together** (folded — the protocol has no production caller until the runner, so landing it alone leaves the loop-side `scopeReq` arm uncoverable): the `scopeReq` channel, `scopeRequest`/`scopeReply` types, the `scopeReq` `select` arm + `handleScopeRequest` (loop goroutine); **plus** `runCompositeLoop` + `requestScope`/`awaitScopeDrained` on `track`, wiring the interception gated on a looped composite Standard Loop (reusing `evalLoopCond`/`loopMaximum`). The existing composite-loop tests + e2e go green on the decorator end-to-end, which exercises the whole protocol. | `scope_decorator.go` (new), `instance.go`, `loop.go`, `track.go`, `std_loop.go` |
| **M2** | Remove the loop-side composite-loop seam (`onScopeOpen` `firstOpen` short-circuit, `resumeScopeHost` `afterDrain`/reopen branch, `standardLoopIterator` callbacks); confirm the whole suite + `examples/standard-loop` green; CHANGELOG note; update ADR-025 cross-refs / conformance tracker. | `scope_runtime.go`, `std_loop.go`, `composite_iter.go`, docs |

> **Milestone fold note:** the original M1 (protocol only) / M2 (runner) split was
> merged because the protocol plumbing has no production caller until the runner,
> so an M1-only landing leaves the loop-side `scopeReq` arm at 0% coverage. Landing
> them together lets the existing `TestLoopedSubProcess*` suite exercise the whole
> protocol end-to-end. The old-seam removal (now M2) stays separate.

## §8 Cross-doc

- **Implements** [ADR-025 v.2](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.2–§2.3 (Standard Loop), §2.12 (the off-loop decorator).
- **Upstream** [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) (single-writer loop), [ADR-023 v.2](../design/ADR-023-sub-process-and-call-activity.md) (scope lifecycle), [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md) (boundary arm-once), [ADR-010 v.2](../design/ADR-010-process-data-model.md) (leaf frame), [ADR-001 v.6](../design/ADR-001-execution-model.md).
- Direction: SRD → ADR only (up), all version-pinned. No downward reference.

## §9 Definition of Done

- FR-1…FR-12 wired; FR-8/FR-8a via `runCompositeLoop` + the `scopeReq` protocol;
  FR-4–FR-7 leaf path unchanged.
- §6 tests exist and pass; the landed composite + leaf + e2e suites stay green
  (NFR-5); `examples/standard-loop` runs and exits 0.
- The loop-side composite-loop seam is removed (M3); a plain composite and the MI
  seam are unaffected.
- Single-writer invariant preserved (NFR-3): no decorator-side mutation of
  loop-owned state; deadlock-freedom argued (NFR-4) and exercised by the
  terminate/interrupt tests.
- `make ci` green (tidy · lint · build · `-race` · diff-coverage ≥95% on touched
  files · govulncheck); CHANGELOG `[Unreleased]` notes the internal rework.
- `/check-srd` PASS before flipping status; ADR-025 v.2 stays Draft until the whole
  re-landing completes (owner: flip after implementation).

## §10 Implementation summary

> ⚠️ TODO: fill AFTER landing — stage commits + empirical findings vs this draft.

### §10.1 Stages by commit (branch `feat/loop-mi-decorator-engine`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|

### §10.2 Empirical findings vs the draft

### §10.3 Backlog

## Open questions

None.
