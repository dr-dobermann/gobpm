# SRD-034 — UserTask wait-node execution, human-task authorization, ManualTask

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-03 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-020 v.1 Human-Interaction Execution Model](../design/ADR-020-human-interaction-execution-model.md) |

Lands [ADR-020 v.1](../design/ADR-020-human-interaction-execution-model.md) on `feat/human-interaction-model`:
re-implements **UserTask** as a wait node on the existing park/resume core (no new pause/resume
mechanism), adds the Camunda-style **triad authorization** (`assignee` / `candidateUsers` /
`candidateGroups`) enforced at both read (`Take`) and write (`Complete`), lands **ManualTask** as a
no-op pass-through, fixes the renderer-multiplicity defect, and ships a batteries-included **console**
`TaskDistributor` + renderer with a runnable example. Closes audit finding **AB-002**.

## 1. Background (verified against the code)

### 1.1 The defects (AB-002)

**Blocking activation / uncancellable park (audit §2.9).** `UserTask.Exec`
(`pkg/model/activities/user_task.go:176`) registers a renderer channel and **blocks** on it:

```go
// user_task.go:200-202 — the blocking loop
for d := range rCh {
    dd = append(dd, d)
}
```

`rCh` comes from `re.RenderRegistrator().Register(ut)`. The loop **ignores `ctx`**, so a waiting
UserTask cannot be cancelled — on instance abort or an interrupting boundary its track goroutine stays
blocked forever, and it bypasses the instance loop's single-writer discipline. Every other wait node
parks cooperatively on the loop-fed channel; UserTask does not.

**Renderer multiplicity (audit §2.8).** `WithRenderer` (`user_task_options.go:66`) deduplicates by:

```go
return r2c.ID() == r.ID() || r2c.Implementation() == r.Implementation()
```

The `Implementation()` clause rejects a second renderer of the same implementation *kind* — but two
renderers of the same kind (e.g. two `##html` forms) are legitimately different renderings (BPMN
`Rendering` is repeatable). Distinct renderers must be distinguished by `ID()` only.

**No runtime authorization.** The `ResourceRole` model is declared (`activity.Roles()`
`pkg/model/activities/activity.go:121`) but never evaluated; there is no runtime acting identity and no
membership check. The interactor contract (`pkg/interactor/interactor.go`) is **interface-only** — no
production implementation — and the `Registrator` is passed **`nil`** into every instance
(`pkg/thresher/thresher.go:830`, `:977`).

**ManualTask has no execution.** No `manual_task.go` exists; `flow.ManualTask` is not even a declared
`TaskType` (`pkg/model/flow/activity.go:26-36` lists only Receive/Script/Send/Service/User).

### 1.2 The decision this SRD lands

[ADR-020 v.1](../design/ADR-020-human-interaction-execution-model.md) decides: UserTask is a wait node
whose completion is an external event; a pluggable `TaskDistributor` boundary; `Take`/`Complete`
authorization-gated engine entry points; a Camunda triad expressed as a façade over `ResourceRole`
resolved at authorization time (static or `FormalExpression`); an `Actor` runtime identity; the
`Authorizer` + `OutputValidator` checks owned by the UserTask with the `Instance` as orchestrator; a
typed `TaskView` return; renderer multiplicity by identity; ManualTask no-op. This SRD reconciles that
concept with the codebase.

### 1.3 The rails UserTask must ride (existing mechanism)

- **Wait-node classification** — `track.checkNodeType` (`internal/instance/track.go:337`) parks a node
  iff it is both `flow.EventNode` and `eventproc.EventProcessor`: it sets `updateState(TrackWaitForEvent)`
  (`:368`), emits `evWaiting` (`:377`), and registers a waiter per definition. UserTask is neither today,
  so it is skipped → the blocking path.
- **Cooperative park** — a parked track blocks in `track.run` on `select { <-ctx.Done(); <-t.evtCh }`
  (`track.go:492-513`); the instance loop is the **sole sender/closer** of `evtCh`
  (`evtCh chan flow.EventDefinition`, `:186`). Zero CPU, cancellable.
- **Delivery** — a fired trigger reaches the loop as an `evDeliver` trackEvent
  (`internal/instance/event.go:92`); the loop routes it to the parked track's `evtCh`; the track wakes
  and runs `deliver` → `ep.ProcessEvent(ctx, eDef)` on its own goroutine (`track.go:968+`).
- **Expression resolution** — `expression.Engine.Evaluate(ctx, expr data.FormalExpression, src data.Source)`
  (`pkg/model/expression/expression.go:21`) over a `data.Source` (`Find(ctx, name) (Data, error)`,
  `pkg/model/data/data.go:29`); the instance scope already presents as a `data.Source` via `execEnv.Find`
  (`internal/instance/execenv.go`), and correlation resolves the same way (`msgflow` `payloadSource`,
  `pkg/model/msgflow/correlation.go:27`).
- **Engine option pattern** — `WithMessageBroker` (`pkg/thresher/options.go:125`) validates non-nil,
  sets a `thresherConfig` field (`:27`), read via an accessor (`:215`), defaulted in `defaultConfig`
  (`:228`, which sets `membroker.New()` at `:235`). `WithTaskDistributor` mirrors this exactly.

## 2. Requirements

### Functional

- **FR-1 — UserTask parks as a wait node.** `checkNodeType` gains a UserTask branch: transition to
  `TrackWaitForEvent`, park the goroutine on `evtCh`, announce the task to the `TaskDistributor`, and
  register the parked task in an engine task index keyed by **task id** so `Take`/`Complete` route back
  to it. The blocking `Exec`/`Registrator` path is removed. The goroutine is held in memory (as all wait
  kinds are) and is cooperatively cancellable via `ctx.Done()` / `evtCh` close.
- **FR-2 — `Actor` runtime identity.** `hi.Actor` interface (`UserID() string`, `Groups() []string`) in
  `pkg/model/hinteraction` — distinct from the BPMN `hi.Performer` element (`resources.go:133`).
- **FR-3 — Triad declaration options.** Six options on UserTask building `ResourceRole`s
  (`PotentialOwner`/`HumanPerformer`) via the existing `AddRole` path: `WithAssignee(id)` /
  `WithAssigneeExpr(expr)`, `WithCandidateUsers(...id)` / `WithCandidateUsersExpr(expr)`,
  `WithCandidateGroups(...id)` / `WithCandidateGroupsExpr(expr)`. Static and expression forms for one
  member are mutually exclusive (the `NewResourceRole` resource-XOR-expression guard, `resources.go:53`).
- **FR-4 — `Authorizer` on UserTask.** `Authorize(ctx, actor Actor, src data.Source, eng expression.Engine) error`:
  resolve each triad member (static set, or evaluate its `FormalExpression` over `src` → `[]string`), then
  decide membership — `assignee` set ⇒ `actor.UserID ∈ assignee`; else `actor.UserID ∈ candidateUsers` **or**
  `actor.Groups ∩ candidateGroups ≠ ∅`; no member declared ⇒ authorized (open). A failed expression
  resolves to an empty set (BPMN: failed resource query ⇒ empty), i.e. authorizes no one.
- **FR-5 — `OutputValidator` on UserTask.** `ValidateOutputs(outputs []data.Data) error`: every required
  `Outputs()` parameter (`user_task.go:126` → `[]*bpmncommon.ResourceParameter`, each `Name()`/`Type()`/
  `IsRequired()`, `resource.go:135-145`) is present and type-conformant; unknown/extra outputs are rejected.
- **FR-6 — `Take`.** `Take(ctx, taskID string, actor Actor) (TaskView, error)`, serviced by the instance
  loop (request/reply, §4.1): authorize `actor`; on success snapshot and return the `TaskView`; on failure
  return the error and expose **no** data. Does not resume the token.
- **FR-7 — `Complete`.** `Complete(ctx, taskID string, actor Actor, outputs []data.Data) error`, serviced
  by the loop: authorize → `ValidateOutputs`; on success bind the outputs and resume the parked track
  (via a synthetic completion event on `evtCh`), reply `nil`; on failure reply the error and leave the
  task **parked** (non-terminal — waits for the right actor / corrected outputs).
- **FR-8 — `TaskView` / `TaskInfo` / `TaskRef`.** A shared embedded `TaskRef` (the four ids); `TaskInfo`
  (announcement, `Distribute`) = `TaskRef` + `Roles`, **no data**; `TaskView` (authorized read, `Take`) =
  `TaskRef` + `Renderers` + `Data`. `Data` carries the task's input data **and** its `Property`s (a
  `Property` is a `data.Data`, `property.go:178`); `FORM_ID` is a userland `Property` convention, not an
  engine field. The absent `Data` field on `TaskInfo` makes the pre-authorization boundary compile-time.
- **FR-9 — `TaskDistributor` boundary + option.** Interface `{ Distribute(ctx, TaskInfo) error;
  Withdraw(ctx, taskID string) error }`; `WithTaskDistributor(d)` engine option mirroring
  `WithMessageBroker` (config field + non-nil-guard + accessor). Optional: when unset, tasks still park
  and are completable by id (no distribution announcement).
- **FR-10 — Renderer multiplicity by identity.** `WithRenderer` dedups by `ID()` only (drop the
  `Implementation()` clause, `user_task_options.go:66`).
- **FR-11 — ManualTask no-op.** Add `flow.ManualTask` TaskType (`flow/activity.go`); new
  `pkg/model/activities/manual_task.go` whose `Exec` returns `Outgoing()` immediately (no descriptor, no
  wait) — BPMN §13.1 non-operational pass-through.
- **FR-12 — Batteries-included console interaction + example.** `pkg/interactor/console`: a console
  `hi.Renderer` and a console `TaskDistributor`; a runnable `examples/usertask/` that builds a
  one-UserTask process, wires the console distributor, drives `Take`/`Complete` programmatically (fixed
  actor + outputs), prints the console view, and exits 0.

### Non-functional

- **NFR-1 — Single-writer preserved.** All scope reads/writes and token resume for `Take`/`Complete`
  occur on the instance-loop goroutine (request/reply, §4.1). No foreign-goroutine scope mutation.
- **NFR-2 — No goroutine leak.** A parked UserTask is cancellable via `ctx.Done()` / `evtCh` close
  (`track.go:492`); an interrupting boundary ([ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md))
  tears the parked waiter down and `Withdraw`s the task.
- **NFR-3 — No new pause/resume mechanism.** Reuse `TrackWaitForEvent` / `evtCh` / `evDeliver`; UserTask
  completion is one more event kind flowing through the ADR-017 core.
- **NFR-4 — Layering.** `pkg/model/activities` self-checks using only model-layer abstractions
  (`data.Source`, `expression.Engine`, `hi.Actor`); it must not import `internal/`.
- **NFR-5 — Observability.** Emit task lifecycle signals (`distributed` / `taken` / `completion.rejected`
  with reason / `completed` / `withdrawn`) via the instance channel
  ([ADR-013 v.1](../design/ADR-013-instance-observability.md)); never log task payload.
- **NFR-6 — Coverage.** Touched files finish at ≥95% diff-coverage (aim 100%) under `make ci`
  (`COVER_MIN=95`, `Makefile:15`).

## 3. Models

### 3.1 `hi.Actor` (`pkg/model/hinteraction`)

```go
// Actor is the authenticated human acting on a task — the runtime identity the
// TaskDistributor supplies and the engine authorizes. Distinct from the BPMN
// Performer element (a ResourceRole subtype).
type Actor interface {
    UserID() string
    Groups() []string
}
```

### 3.2 Triad + `Authorizer`/`OutputValidator` (`pkg/model/activities`, `pkg/model/hinteraction`)

The standard `hi.ResourceRole` holds one `Resource` ref **or** an `assignmentExpression` — it cannot
carry the triad's user/group distinction, static id-**lists**, or slot marker. So the triad is a **typed
structure** (`pkg/model/hinteraction`), the single source of truth, exposed via a typed accessor and read
by `Authorize`; it **coexists** with the generic `Roles()` rather than being projected into it (§4.3):

```go
// hinteraction — one triad member: static identifiers XOR an expression → a set.
type AssignmentSlot int // Assignee | CandidateUsers | CandidateGroups
type Assignment struct {
    slot   AssignmentSlot
    static []string              // XOR expr
    expr   data.FormalExpression
}
```

The UserTask holds up to one `Assignment` per slot (private fields) and implements both check interfaces;
`Authorize` reads the typed `Assignment`s directly (not by re-parsing `Roles()`):

```go
type Authorizer interface {
    Authorize(ctx context.Context, actor hi.Actor, src data.Source, eng expression.Engine) error
}
type OutputValidator interface {
    ValidateOutputs(outputs []data.Data) error
}
```

### 3.3 `TaskRef`, `TaskInfo`, `TaskView`, `TaskDistributor` (engine boundary)

`TaskInfo` (the announcement) and `TaskView` (the authorized read) are **distinct by lifecycle**, not
duplicates: `TaskInfo` is handed to `Distribute` at park time — *before* any authorization — so it must
**not** carry the task's `Data`; `TaskView` is returned by `Take` *after* authorization, so it does. The
shared identity is factored into an embedded `TaskRef`, and the differing fields encode the boundary — an
announcement type with no `Data` field means instance variables cannot reach the distributor by
construction. `Renderers` live on `TaskView` only: the distributor obtains the form by calling `Take` on
the human's behalf, not from the announcement.

```go
// TaskRef identifies a parked task across the boundary (embedded in both types).
type TaskRef struct {
    TaskID, InstanceID, NodeID, ProcessID string
}

// TaskInfo — the pre-authorization announcement handed to Distribute: identity +
// who may claim (for inbox routing/filtering). No data, no form.
type TaskInfo struct {
    TaskRef
    Roles []*hi.ResourceRole
}

// TaskView — the post-authorization snapshot returned by Take: the form to render
// and the self-describing data, for an actor who has passed authorization.
type TaskView struct {
    TaskRef
    Renderers []hi.Renderer
    Data      []data.Data
}

type TaskDistributor interface {
    Distribute(ctx context.Context, task TaskInfo) error
    Withdraw(ctx context.Context, taskID string) error
}
```

`Take`/`Complete` are engine methods (on the Thresher), routed by `taskID` through an engine task index
to the owning instance loop.

### 3.4 ManualTask (`pkg/model/activities/manual_task.go`)

```go
type ManualTask struct{ task }
func (mt *ManualTask) Exec(_ context.Context, _ renv.RuntimeEnvironment) ([]*flow.SequenceFlow, error) {
    return mt.Outgoing(), nil // BPMN §13.1 non-operational — pass-through
}
func (mt *ManualTask) TaskType() flow.TaskType { return flow.ManualTask }
```

### 3.5 Console reference impl (`pkg/interactor/console`)

A `Renderer` that prints its fields and returns collected `data.Data`, and a `TaskDistributor` that
prints the `TaskInfo` on `Distribute` (and renders the `TaskView` when it drives `Take`) and clears on
`Withdraw`. Reference/checking quality; not the zero-config default.

## 4. Analysis

### 4.1 `Take`/`Complete` are loop-serviced request/reply (decided)

`Complete` must return a synchronous verdict, yet all scope access and token resume must stay on the
instance-loop goroutine (ADR-017 single-writer). So the engine entry points enqueue a request carrying a
reply channel onto the target instance's loop (new inbound trackEvent kinds, alongside `evDeliver`). The
loop runs `UserTask.Authorize` (over the instance's `execEnv` `data.Source`) and, for `Complete`,
`ValidateOutputs`, then replies the verdict; on a successful `Complete` it delivers a synthetic
completion event (carrying outputs) to the parked track's `evtCh`, so the track binds the outputs in its
`ProcessEvent` and advances — exactly the message-catch path. **Rejected:** running the checks on the
caller's goroutine — it races the scope against the loop and violates single-writer.

Worked flow (Complete; Take is the same minus validate/bind/resume):

```
1. Thresher.Complete(ctx, taskID, actor, outputs)
     → look up taskID in the engine task index → the owning instance
     → enqueue completeReq{actor, outputs, replyCh} onto that instance's loop; block on replyCh
2. Instance loop (single-writer goroutine) dequeues completeReq, resolves the parked UserTask node:
     a. task.Authorize(ctx, actor, execEnv /*data.Source*/, engine)
          → for each triad member: static set, or engine.Evaluate(expr, src) → []string
          → membership verdict (assignee-restrictive / candidate / open)
          → non-nil error ⇒ reply err on replyCh; task stays parked; STOP
     b. task.ValidateOutputs(outputs)  → non-nil error ⇒ reply err; task stays parked; STOP
     c. emit a synthetic completion event (carrying outputs) to the parked track's evtCh
     d. reply nil on replyCh
3. Parked track wakes from <-t.evtCh → deliver → UserTask.ProcessEvent binds outputs to scope,
     Withdraw(taskID), advances the token onto Outgoing()
4. Thresher.Complete returns the replyCh verdict (nil on success)
```

### 4.2 A UserTask wait-node branch, not a synthetic `EventNode` (decided)

A UserTask is not a BPMN event, so it should not masquerade as a `flow.EventNode`. `checkNodeType` gets
an explicit UserTask branch that parks the track and registers it in the engine task index (keyed by
task id) instead of registering hub waiters. Completion still travels through `evtCh` as a synthetic
`flow.EventDefinition` so the wake/deliver path is unchanged. **Rejected:** giving UserTask a fake
message/signal definition — it would pollute correlation and the event hub with a non-event.

### 4.3 Typed triad, exposed via a typed accessor (decided; refines ADR-020 §2.5)

Grounding showed `hi.ResourceRole` cannot natively carry the triad (one `Resource` ref XOR an
expression; no user/group distinction, no static id-list, no slot). So the **typed `Assignment`** (§3.2)
is the single source of truth: the six options set the UserTask's per-slot `Assignment`s, `Authorize`
reads them directly, and the UserTask exposes them via a typed accessor (`Assignments()`). The triad
**coexists** with the generic `Roles()` (`WithRoles`) — it is **not** projected into it: a projection
would be lossy (static id-lists collapse to single-`Resource` roles; slot/kind is unrepresentable), and
Camunda itself keeps the triad in extension attributes, separate from BPMN `ResourceRole`. **Rejected:**
storing/projecting the triad *as* generic `ResourceRole`s.

### 4.4 Checks live on the UserTask; Instance orchestrates (decided, from ADR-020)

`Authorizer`/`OutputValidator` are on the UserTask (it declares the triad + output spec). The loop only
orchestrates `authorize → validate → bind → resume`; the `TaskDistributor` holds no check logic. Keeps
`pkg/model/activities` free of `internal/` imports (NFR-4).

### 4.5 Bundled console impl, opt-in; zero-config default stays optional (decided)

No bundled default can auto-complete a UserTask (completion needs a human/driver), so console I/O must
not be a silent default. The console `TaskDistributor` is opt-in (`WithTaskDistributor(console.New())`);
absent any distributor, tasks still park and are completable by id (FR-9). This is the batteries-included
reference + the smoke artifact, mirroring `membroker` as a bundled-but-explicit impl.

### 4.6 What stays the same (decided)

`Outputs()` / `Renderers()` accessors, the `ResourceRole` object model, the `evtCh`/`evDeliver` machinery,
and the single-writer instance loop are reused unchanged. Only the UserTask activation path and the
option/authorization surface change.

## 5. API / contract surface

- Engine (Thresher) methods: `Take(ctx, taskID, actor) (TaskView, error)`, `Complete(ctx, taskID, actor,
  outputs) error`.
- Engine option: `WithTaskDistributor(TaskDistributor) Option`.
- Model options: the six triad `WithX`/`WithXExpr`; `WithRenderer` (dedup by ID); UserTask now satisfies
  `Authorizer` + `OutputValidator`.
- New elements: `hi.Actor`, `TaskRef`, `TaskInfo`, `TaskView`, `TaskDistributor`, `activities.ManualTask`,
  `flow.ManualTask`, `pkg/interactor/console`.

## 6. Test scenarios

- **FR-2/3/4** `TestUserTaskAuthorize` — assignee-restrictive; candidateUsers match; candidateGroups
  intersection; no-triad open; expression-resolved candidate set (over a `data.Source`); failed
  expression ⇒ deny.
- **FR-5** `TestUserTaskValidateOutputs` — required present/missing; type mismatch; extra output rejected.
- **FR-1/6/7** `TestUserTaskParkTakeComplete` — parks (no goroutine blocked on a foreign channel); `Take`
  unauthorized ⇒ error + no data; authorized ⇒ `TaskView` with ids+renderers+data; `Complete`
  unauthorized ⇒ non-terminal (still parked); invalid outputs ⇒ non-terminal; valid ⇒ binds + resumes to
  `Outgoing()`.
- **NFR-2** `TestUserTaskCancelWhileParked` — `ctx` cancel / interrupting boundary tears down the parked
  task and `Withdraw`s it; no leaked goroutine.
- **FR-9** `TestWithTaskDistributorNil` — rejects nil; `TestDistributeWithdraw` — announced on park,
  withdrawn on complete/cancel.
- **FR-10** `TestWithRendererDedupByID` — two renderers of the same `Implementation()` but different
  `ID()` both retained; same `ID()` rejected.
- **FR-11** `TestManualTaskPassThrough` — token flows straight to `Outgoing()`, no wait.
- **FR-12** console: `TestConsoleRendererCollects`, `TestConsoleDistributor`; `examples/usertask/` runs
  to exit 0 under timeout (smoke).

## 7. Milestones

1. **M1 — `Actor` + triad authorization (model layer).** `hi.Actor`; the six triad options; `UserTask.Authorize`
   (static + `FormalExpression`); `UserTask.ValidateOutputs`. Unit-tested in isolation.
2. **M2 — UserTask wait-node redesign.** `checkNodeType` UserTask branch; engine task index; `Take`/`Complete`
   loop request/reply + synthetic completion event; `TaskView`; `TaskDistributor` interface +
   `WithTaskDistributor`; remove blocking `Exec`/`Registrator`.
3. **M3 — Renderer multiplicity fix.** `WithRenderer` dedup by `ID()`.
4. **M4 — ManualTask no-op.** `flow.ManualTask` + `manual_task.go` pass-through.
5. **M5 — Console impl + example.** `pkg/interactor/console` renderer + distributor; runnable
   `examples/usertask/` (smoke-able).

## 8. Cross-doc

- **Implements** [ADR-020 v.1](../design/ADR-020-human-interaction-execution-model.md).
- References (up/sideways): [ADR-001 v.6](../design/ADR-001-execution-model.md),
  [ADR-006 v.2](../design/ADR-006-events-and-subscriptions.md),
  [ADR-010 v.2](../design/ADR-010-process-data-model.md),
  [ADR-011 v.5](../design/ADR-011-process-data-flow.md),
  [ADR-013 v.1](../design/ADR-013-instance-observability.md),
  [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md),
  [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md).
- Direction is SRD → ADR (up); no downward references. This SRD is number-only in any future citation.

## 9. Definition of Done

- FR-1…FR-12 implemented and wired; NFR-1…NFR-6 upheld.
- Every FR/NFR covered by ≥1 named §6 test, all green under `-race`.
- The blocking `Exec` path and the `nil` `Registrator` wiring are removed.
- `make ci` green (tidy · lint · build · `-race` · diff-coverage ≥95% on touched files · govulncheck).
- `examples/usertask/` runs to exit 0 under a timeout; its built binary is gitignored.
- ADR-020 (+ RU twin) and SRD-034 flip to Accepted; linked docs synced (audit backlog AB-002, backlog).

## 10. Implementation summary

_(filled at landing: files/lines, V-results, milestone SHAs.)_

## Open questions

None.
