# SRD-050 — Call Activity: a child instance through the registry

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-16 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-023 v.1](../design/ADR-023-sub-process-and-call-activity.md) §2.7 (the Call Activity slice — completes epic #85; the embedded slice landed with the prior SRD) |
| Upstream | [ADR-019 v.1](../design/ADR-019-definition-versioning-and-registry.md) (the registry the call resolves against: latest-at-launch / pinned versions), [ADR-001 v.6](../design/ADR-001-execution-model.md) (the loop owns the call protocol), [ADR-010 v.2](../design/ADR-010-process-data-model.md) (the isolated child data plane; root binding), [ADR-021 v.1](../design/ADR-021-service-task-execution-model.md) (the async park/resume + fault-classification idioms the caller reuses), [ADR-013 v.2](../design/ADR-013-observability.md) (the linkage attributes) |
| Refines | — |

## §1 Background

ADR-023 v.1 §2.7 decides the Call Activity as the **reuse boundary**: a
**child instance** of a separately registered process — by reference, not
containment — with latest-at-launch version binding (pinned as an option),
an async caller park/resume, the standard's direct I/O mapping, an
isolated child data plane, and a terminate cascade. The embedded slice
landed everything scope-side; this slice adds the cross-instance protocol.

The code today:

- **No invocation seam.** An instance receives engine services via
  `engrenv.EngineRuntime` (`renv/engineruntime.go:23-58`) and the
  `TaskDistributor` (`instance.New`, `thresher.go:1126`), but nothing lets
  it ask the engine to launch another process. The thresher owns the
  registry (`registrations map[string][]*ProcessRegistration` +
  `nextVersion`, `thresher.go:153-156`), the three launch modes
  (`StartLatest`/`StartVersion`/`StartProcess`, `thresher.go:1036-1094`),
  `launchInstance` (`thresher.go:1125-1151`) and the running-instance
  tracking (`instances map[string]instanceReg`).
- **The caller's park/resume template exists**: the external-worker job —
  the track parks, the loop binds input and enqueues, a report re-enters
  through a dedicated channel and resumes the track via `evtCh`
  (`jobs.go`; `jobReq`), with `cleanupJob` on track end. The embedded
  slice added the synthetic-completion resume (`scopeDone`) the call
  completion mirrors.
- **The child-root injection precedent exists**:
  `instanceScope.bindEventPayload` commits extra data into a fresh
  instance's root scope (`internal/instance/scope.go:101-120`) — the
  input-binding shape.
- **The model is a stub**: `flow.CallActivity` is a type constant;
  `bpmncommon.CallableElement` is name-only. `activity.IoSpec` +
  `WithParameters` (`activity_options.go:163-191`) already model named
  input/output parameters.

## §2 Requirements

### Functional — model layer

- **FR-1 — `activities.CallActivity`** (new
  `pkg/model/activities/call_activity.go`): embeds the `activity` base;
  `ActivityType() = flow.CallActivity`; the **concrete-type `Node()`
  override** (the SubProcess lesson — flow targets unwrap through it).
  Construction: `NewCallActivity(name, calledKey string, opts...)` — the
  registry key of the callable, non-empty (validated); option
  `WithCalledVersion(v int)` pins an exact version (≥1), default 0 =
  **latest-at-launch** (ADR-019 semantics, ADR-023 §2.7). I/O parameters
  ride the existing `activities.WithParameters` /
  `data.InputOutputSpecification` surface — the CallActivity's declared
  Inputs/Outputs ARE the call contract (§10.4: no explicit data
  associations). Accessors: `CalledKey()`, `CalledVersion()`.
- **FR-2 — Model validation.** `CallActivity.Validate()` (the per-node
  hook): a non-empty key; a pinned version ≥ 1 when set. Registry
  existence is deliberately NOT checked at model build — resolution is at
  **call time** (ADR-023 §2.7; the callable may be registered later or
  re-versioned). The runtime-facing surface: `ProcessEvent` accepts the
  completion delivery; `Exec` runs the standard outgoing selection after
  the resume (the composite precedent).

### Functional — the invoker seam

- **FR-3 — `exec.ProcessInvoker`.** A new capability interface (in
  `pkg/exec`, beside the executor contracts):

  ```go
  // ProcessInvoker launches a registered process as a CHILD instance on
  // behalf of a Call Activity (ADR-023 §2.7). Implemented by the engine
  // (the thresher); consumed by the instance loop.
  type ProcessInvoker interface {
  	// InvokeProcess resolves key (version 0 = latest-at-launch, else the
  	// pinned 1-based version), binds inputs into the child's root scope,
  	// launches it, and returns its watch handle. Resolution failures are
  	// classified errors (a missing key/version fails the CALL, not the
  	// engine).
  	InvokeProcess(ctx context.Context, call ProcessCall) (ChildProcess, error)
  }
  ```

  with `ProcessCall{Key string; Version int; Inputs []data.Data;
  ParentInstanceID, CallNodeID string}` and `ChildProcess` exposing
  `ID() string`, `Version() int` (the RESOLVED 1-based version bound — the
  audit point FR-10 records for a latest-at-launch call), `Done()
  <-chan struct{}`, `Failed() error` (the terminal fault, or nil on a
  normal/cancelled end), `Outputs(names []string) ([]data.Data, error)`
  (read the child's root data by name after completion) and `Terminate()`.
  `Instance.New` gains the invoker the way it carries the `TaskDistributor`
  (nil = calls fail fast with a classified no-invoker-configured error — a
  library embedder without a thresher). *(M2 refinement: `Version()` was
  added — the resolved version is otherwise unreachable through the
  interface for a version-0 call, which FR-10 needs; `Failed()` dropped its
  redundant `bool` — `err != nil` is the fault signal.)*
- **FR-4 — The thresher implements it.** `Thresher.InvokeProcess`:
  resolve per FR-3 through the existing lookup paths
  (`latestSnapshotLocked` / the version scan — ADR-019 gap handling);
  build the child through the `launchInstance` path with two additions:
  the **input binding** — the call's `Inputs` committed into the child's
  root scope at creation (the `bindEventPayload` pattern; an instance
  option `withRootData`) — and the **linkage details** on the child's
  facts (`parent_instance_id`, `call_activity_node_id` — new
  `observability.Attr*` keys) plus a call-level fact pair from the caller
  (FR-10). The child is tracked in `t.instances` as any instance
  (shutdown semantics unchanged). `ChildProcess.Outputs` reads through
  the child's observe reader by name.

### Functional — runtime (the caller's loop)

- **FR-5 — Classification & park.** `checkNodeType` recognizes the
  CallActivity between the composite and the external-worker checks (a
  capability assert — `interface{ CalledKey() string }` +
  `flow.ActivityNode`, keeping the runtime model-agnostic): the track
  parks on `evtCh` (`TrackWaitForEvent`) and emits the new
  **`evCallWaiting`** (mid-run; the born-parked twin rides
  `recordBornWaiter`, the SRD-048/-049 construction-never-emits rule).
- **FR-6 — The loop launches.** On `evCallWaiting`: resolve the call's
  **inputs in the caller's scope** — each declared Input parameter of the
  CallActivity's IoSpec is resolved **by name** at the caller track's
  scope path (a transient frame; §4.2 decides by-name) and cloned for the
  hand-off (the isolation contract: no live sharing across the call
  boundary); `InvokeProcess`; register the call in the loop-owned
  registry `calls map[callID]*callEntry{track, node, child}`; start ONE
  watcher goroutine per call that waits `child.Done()` and reports into
  the instance's **`callReq` channel** (the `jobReq` pattern — the loop
  stays the single writer). Launch/resolution failure → the call fails →
  the caller track faults with the classified error (the §2.6 chain at
  the CallActivity node — an Error boundary on it can catch a
  registry-resolution fault only if typed; a technical fault otherwise).
- **FR-7 — Completion & output binding.** On the `callReq` completion:
  child `Completed` → read the child's outputs **by the CallActivity's
  declared Output parameter names** (`ChildProcess.Outputs`), commit them
  into the CALLER's scope at the caller track's path (the loop-side
  commit; one atomic batch), then resume the parked track with the
  synthetic call-completion (the `scopeDone` idiom — a `callDone`
  sentinel); the track executes `CallActivity.Exec` → the standard
  outgoing selection. A missing declared output in the child's root is a
  classified error (the call contract broken → the caller faults through
  the §2.6 chain).
- **FR-8 — Child failure propagation.** Child `Terminated`/failed: if the
  child's terminal error is a typed `BpmnError`, the caller track fails
  WITH that error — `matchErrorBoundary`/the scope chain then catch it at
  the CallActivity node per the existing `applyFailed` path (ADR-023
  §2.7: "a fault enters the caller's §2.6 chain at the Call Activity
  node"); an untyped termination is a technical fault (uncaught → the
  instance faults).
- **FR-9 — The cancel cascade.** The caller's side ends the child
  whenever the call's episode dies (ADR-023 §2.7 — the engine choice):
  `cleanupCall(track)` hooks the same sites as `cleanupJob` — the
  terminal-event paths (`evEnded`/`evFailed`) and `stopAll`'s `drop` —
  calling `child.Terminate()` and dropping the entry. A call parked
  inside a **cancelled sub-process scope** needs no direct `cancelScope`
  hook: the scope-cancel walk stops and cancels the host track, whose
  own terminal `evEnded` then drains `cleanupCall` through the same path
  as any track end. The watcher goroutine's late report finds no entry
  and is dropped (benign). Instance shutdown needs nothing new — the
  child is a first-class instance under the thresher's own shutdown.
- **FR-10 — Observability.** One new kind **`KindCall`** ("call-activity
  lifecycle") with phases `Started`/`Completed`/`Failed`/`Terminated`,
  emitted by the caller with details `called_key`, `called_version` (the
  RESOLVED version — the latest-at-launch audit point ADR-023 §6
  recommends), and `child_instance_id`; the CHILD's own facts carry
  `parent_instance_id` + `call_activity_node_id` (FR-4), stitching the
  trace across the boundary. `KindCall` echoes at Info (a lifecycle
  milestone). A new kind — rather than overloading `KindJobState` (a
  worker-queue vocabulary) or `KindScope` (a call is not a scope) — keeps
  each kind's phase set semantically closed.

### Functional — front door

- **FR-11** — thresher e2e (call completes with I/O round-trip; a pinned
  version beats a later registration; child `BpmnError` caught by an
  Error boundary on the CallActivity; the cascade on caller termination);
  example `examples/call-activity/`; `docs/guides/composition.md` gains
  the Call Activity section; changelog; conformance tracker — the
  `CallActivity` row flips and **epic #85 closes**; READMEs (EN/RU) +
  examples index. ADR-023 flips **Accepted** in the closing docs
  change-set (+ the RU twin — the bilingual policy).

### Non-functional

- **NFR-1 — Isolation is the contract.** Nothing crosses the call
  boundary except the declared inputs (cloned in) and the declared
  outputs (read back at completion): no scope walk-up, no live value
  sharing, no event visibility. A called process runs identically however
  it is reached.
- **NFR-2 — Single-writer preserved**, both sides: the caller's call
  registry is loop-owned (`callReq` re-enters the loop, the `jobReq`
  twin); the child is an ordinary instance with its own loop. The watcher
  goroutine only waits and reports.
- **NFR-3 — Call-free processes pay nothing**: no calls registry traffic,
  no channel, no watcher goroutines unless a CallActivity executes.
- **NFR-4 — Camunda-aligned defaults**: latest-at-launch binding; the
  child terminates with the caller.
- **NFR-5 — Coverage**: touched files 100% (min 80%); diff-coverage ≥95%;
  `make ci` per milestone (frozen-tree discipline).

## §3 Models

### §3.1 `activities.CallActivity`

```go
// CallActivity invokes a separately registered process as a CHILD
// instance (ADR-023 §2.7): the reuse boundary. The caller parks while the
// child runs; the declared Input/Output parameters are the call contract
// (§10.4 direct mapping — no data associations).
type CallActivity struct {
	activity

	calledKey     string
	calledVersion int // 0 = latest-at-launch (ADR-019)
}
```

### §3.2 `exec.ProcessInvoker` (FR-3, quoted there)

### §3.3 Runtime deltas

- `trackEvent`: kind `evCallWaiting` (+ names row).
- `Instance`: the `callReq chan callRequest` channel (the `jobReq` twin;
  drained in the loop select) + the invoker reference from `New`.
- `loopState`: `calls map[string]*callEntry{track *track; node flow.Node;
  child exec.ChildProcess}` (keyed by an engine-minted call id);
  `cleanupCall` hooked beside `cleanupJob`.
- `observability`: `KindCall` + `AttrParentInstanceID`,
  `AttrCallActivityNodeID`, `AttrCalledKey`, `AttrCalledVersion`,
  `AttrChildInstanceID`; the echo table gains `KindCall: Info`.

## §4 Analysis

### §4.1 Why the invoker is a first-class capability, not an engine-runtime method

`engrenv.EngineRuntime` is the *stateless service* surface every node
evaluation sees (clock, expressions, brokers). Launching a process is a
*stateful engine operation* with lifecycle consequences — exactly the
class the `TaskDistributor` models as a separate `Instance.New`
dependency. A distinct `ProcessInvoker` keeps the node-execution surface
closed (no node code can launch processes — only the loop's call
protocol), makes the library-embedder case explicit (nil invoker = calls
fail fast, classified), and gives tests a trivial fake.

### §4.2 By-name I/O matching — an engine choice this SRD settles

ADR-023 §2.7 permits "positional/by-name direct binding" and left the
mechanism here; this SRD **narrows to by-name only** (an explicit
refinement, not a standard mandate — §10.4 prescribes only the absence of
explicit associations). Matching is **by parameter name**: the
CallActivity's declared Input
names are resolved in the caller's scope and committed under the SAME
names into the child's root; the declared Output names are read from the
child's root and committed under the same names into the caller's scope.
Rejected: positional matching — BPMN parameters are named elements, Go
map iteration would make positions non-deterministic, and the reference
engines (Camunda in/out mappings) are name-keyed. A name the caller
cannot resolve at call time, or a declared output the child did not
produce, is a classified contract error (fail fast — the silent-partial
alternative is the misbehavior class the engine rejects).

### §4.3 Why a watcher goroutine per call (and not hub events)

The child's completion is a cross-instance signal with exactly one
consumer. The hub distributes broadcast/correlated triggers; a
dedicated `Done()`-wait + `callReq` report (the `jobReq` shape) keeps
the protocol point-to-point, needs no correlation surface, and cannot
leak subscriptions — the watcher exits with the child, and a late report
against a cleaned-up call drops benignly (the job-report precedent).

### §4.4 Recursion & the depth guard

A process may call itself (resolution is by key at call time — legal
composition, ADR-023 §2.9). Unbounded recursion exhausts instances, not
the stack; the operational depth guard recommended by ADR-023 §6 stays
future work (an engine option counting the linkage chain) — recorded, not
implemented here.

### §4.5 Rejected shapes

- **Blocking the caller's goroutine on `WaitCompletion`**: wastes a
  goroutine per call AND breaks the cooperative-cancellation model (the
  park must stay interruptible by boundaries/terminate through the loop).
  The park/report/resume shape is the engine's established async idiom.
- **Graph inlining / shared data plane**: rejected in ADR-023 §4 (the
  reuse contract).
- **Registry-existence validation at model build**: would freeze the
  binding earlier than ADR-019's launch-time semantics and break the
  register-later workflow.

## §5 API surface

Public: `activities.NewCallActivity`/`CallActivity` (+
`WithCalledVersion`), `exec.ProcessInvoker`/`ProcessCall`/`ChildProcess`,
the `observability` kind/attr additions. The thresher's `InvokeProcess`
is its `ProcessInvoker` compliance (not a new user-facing API — users
keep `StartLatest`/`StartProcess`). Everything else is `internal/instance`
machinery.

## §6 Test scenarios

Model (M1): `TestCallActivityModel` (construction; empty key rejected;
version pin validated; `Node()` identity; the IoSpec surface),
`TestCallActivityValidate`.

Thresher/invoker (M2): `TestInvokeProcessLatestAndPinned` (latest
resolves the newest version; a pin survives a later registration; a
missing key/version is a classified error), `TestInvokeProcessInputs`
(inputs land in the child's root; the child reads them),
`TestChildOutputsReader`, `TestChildLinkageFacts` (the child's facts
carry the parent attrs).

Runtime (M3, `internal/instance`, a fake invoker): the park + launch
(`TestCallParksAndLaunches`), completion + output binding
(`TestCallCompletionBindsOutputs`), the missing-output contract error,
child `BpmnError` → the Error boundary on the CallActivity catches
(`TestCallChildErrorCaught`), untyped child termination → instance fault,
the cascade (`TestCallCascadeOnTrackCancel`, `...OnScopeCancel`,
`...OnStopAll`), nil-invoker fail-fast, the late-report drop.

E2E (M4, `pkg/thresher`): `TestCallActivityE2E` — a registered callee +
a caller with I/O round-trip; the pinned-version case; the child-error
catch; the example smoke.

## §7 Milestones

| # | Scope |
|---|---|
| M1 | The `CallActivity` model + validation + runtime surface (FR-1/2) + tests |
| M2 | `exec.ProcessInvoker` + the thresher implementation (resolution, input binding via the root-data option, linkage facts, `ChildProcess`) (FR-3/4) + tests |
| M3 | The caller's loop protocol: classification/park, `evCallWaiting`, launch, `callReq` resume with output binding, failure propagation, the cascade, `KindCall` (FR-5..10) + tests |
| M4 | Thresher e2e + `examples/call-activity/` + guide section + changelog + tracker (#85 closes) + READMEs (FR-11) |

Post-M4: `/check-srd`, §10 fill, SRD → Accepted, **ADR-023 → Accepted +
the RU twin refresh**, linked-docs sync (the ADR-018 boundary-on-
CallActivity deferral row gains its landed-elsewhere annotation only if
boundary-on-CallActivity actually lands here — it does: a CallActivity is
an activity, the boundary machinery consumes the base unchanged), PR
handover closing #85.

## §8 Cross-doc

- Implements [ADR-023 v.1](../design/ADR-023-sub-process-and-call-activity.md)
  §2.7 (traced per FR).
- Rides [ADR-019 v.1](../design/ADR-019-definition-versioning-and-registry.md)
  resolution; extends [ADR-001 v.6](../design/ADR-001-execution-model.md)
  loop protocol; keeps [ADR-010 v.2](../design/ADR-010-process-data-model.md)
  isolation; reuses [ADR-021 v.1](../design/ADR-021-service-task-execution-model.md)
  park/report idioms; extends
  [ADR-013 v.2](../design/ADR-013-observability.md) with `KindCall` + the
  linkage attributes.

## §9 Definition of Done

- [ ] All FR/NFR wired and traced to §6 tests.
- [ ] `make ci` green per milestone (frozen tree); diff-coverage ≥95%;
      touched files 100% (min 80%).
- [ ] Example runs to completion (exit 0), binary gitignored.
- [ ] Conformance tracker: the CallActivity row flips; **#85 closes**.
- [ ] Changelog `[Unreleased]` before the PR description.
- [ ] `/check-srd` PASS; §10 filled; SRD Accepted; ADR-023 Accepted + RU
      twin; linked docs synced.

## §10 Implementation summary

> ⚠️ TODO: fill after landing.

## Open questions

None.
