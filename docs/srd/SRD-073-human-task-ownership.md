# SRD-073 — Human-task ownership: claim / unclaim / reassign

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-07-30 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-020 v.2](../design/ADR-020-human-interaction-execution-model.md) §2.1.1, §2.4.1, §2.4.2, §2.5.1–§2.5.3, §2.7 |
| Upstream | [ADR-007 v.2.1](../design/ADR-007-in-memory-long-waits.md) §2.4, [ADR-011 v.7](../design/ADR-011-process-data-flow.md), [ADR-013 v.2](../design/ADR-013-instance-observability.md), [ADR-033 v.2](../design/ADR-033-persistence-and-state.md) |
| Refines | [SRD-034 v.1](SRD-034-user-task-execution.md) (the user-task landing this extends), [SRD-071 v.2.5](SRD-071-instance-dehydration-and-wake-on-trigger.md) §FR-8 (the task-action hydrate/replay path) — sideways |

Lands ADR-020 v.2's ownership lifecycle: BPMN's `actualOwner` instance attribute
(§10.3.4.1 Table 10.14), the `Claim` / `Unclaim` / `Reassign` operations, strict
owner-only completion, and a durable `completedBy`. The central structural move is
**splitting eligibility *resolution* from the eligibility *rule*** — the `UserTask`
resolves its own triad once at distribution and still owns the verdict rule, while the
resolved snapshot lives in the engine-level task registry so ownership operations run
without hydrating a released instance.

---

## §1 Background (grounded)

Every claim below is grep-verified against the tree at `6705c0b`.

### 1.1 Ownership does not exist today

`handleTaskRequest` is the whole of the human-task decision path
(`internal/instance/tasks.go:153`). For a `reqTake` it authorizes, emits `PhaseTaken`,
returns a `TaskView` and returns — touching no holder state (`:186-198`):

```go
if req.kind == reqTake {
    // The actor took the task for work (SRD-041 §3.4).
    ls.inst.report(observability.Fact{...Phase: observability.PhaseTaken...})
    req.reply <- taskReply{view: ls.inst.buildTaskView(req.taskID, entry.node)}
    return
}
```

The loop-owned registry value carries only the track and the node — there is no owner
field (`internal/instance/tasks.go:50-53`):

```go
type taskEntry struct {
    track *track
    node  flow.Node
}
```

So the only gate on completing is triad membership, and it admits **every** eligible
actor, repeatedly: `completeTask` validates outputs and resumes without consulting who
acted (`:223-259`). FR-1/FR-4 close this.

### 1.2 The engine-level registry that survives dehydration

`Thresher` holds `tasks map[string]string` — taskID → instanceID
(`pkg/thresher/thresher.go:164`), guarded by `m sync.Mutex` (`:191`), initialized at
`:257`. It is written by the `routingDistributor` decorator as tasks are announced and
withdrawn (`pkg/thresher/tasks.go:24-41`), i.e. **outside** any instance's lifetime:

```go
r.thr.registerTask(task.TaskID, task.InstanceID)   // tasks.go:28
r.thr.unregisterTask(taskID)                       // tasks.go:38
```

`HoldTask` documents exactly why this is the right home for ownership
(`pkg/thresher/tasks.go:121-126`): *"A task already lives in the distributor's inbox
independent of residency (ADR-020), and an action on it routes by task id through the
distributor's own taskID → instanceID entry — which is what hydrates the instance."*
FR-2 grows this map's value into a record.

### 1.3 Every task action currently hydrates

`onTaskInstance` resolves a resident instance and replays on release
(`pkg/thresher/tasks.go:153-187`), reached by both public entry points — `Take`
(`:86-102`) and `Complete` (`:107-115`). `residentForTask` (`:212`) hydrates via
`hydrateForTask` (`:250`), and a mid-release action is refused with `TaskRetryClass`
(`internal/instance/tasks.go:117`, raised at `:159-165`) so the caller replays. ADR-020
v.2 §2.1.1 requires ownership operations **not** to take this path; FR-3 gives them a
registry-only route while `Complete` keeps it.

### 1.4 Authorization resolves the triad per call, against live scope

`UserTask.Authorize` implements the verdict rule
(`pkg/model/activities/user_task_authz.go:55-97`): open when no slot is declared
(`:68-72`), assignee short-circuits (`:74-82`), otherwise candidateUsers **or**
candidateGroups (`:84-94`). Each slot is resolved at call time through
`hi.Assignment.Resolve` (`pkg/model/hinteraction/assignment.go:114`).

The resolution needs instance data, so it runs on a transient scope frame
(`internal/instance/tasks.go:206-219`):

```go
frame, err := inst.sc.openFrame("task-authz", ht.ID())
defer frame.Discard()
return ht.Authorize(ctx, actor, newExecEnv(inst, frame, nil), inst.ExpressionEngine())
```

This is precisely what ADR-020 v.2 §2.7 changes. FR-5 moves resolution to distribution;
§4.1 explains how the *rule* stays on the `UserTask`.

### 1.5 The announcement already exists and already carries roles

`buildTaskInfo` runs on the loop goroutine while scope is available
(`internal/instance/tasks.go:415`) and produces (`pkg/interactor/taskview.go:24-27`):

```go
type TaskInfo struct {
    TaskRef
    Roles []*hi.ResourceRole
}
```

It is handed to `Distribute` from `addTask` (`internal/instance/tasks.go:279`), the same
call that emits `PhaseAnnounced` (`:285-291`). This is the one point where "the instance
is resident **and** the task is becoming visible" coincide — the natural home for FR-5's
resolution.

### 1.6 Observability has the Kind but not the phases

`KindTaskState` exists (`pkg/observability/fact.go:26`) with `PhaseAnnounced` /
`PhaseTaken` / `PhaseWithdrawn` (`:97-99`) plus the shared `PhaseCompleted` (`:65`), and
`AttrTaskID` (`:192`). There is no phase for acquiring, releasing or transferring
ownership. FR-8 adds three.

### 1.7 The durable-write path for `completedBy`

`Scope.Commit(at DataPath, dd ...data.Data)` (`internal/scope/scope.go:342`) is the
durable write; `Scope.Root()` (`:73`) is the instance-root path. Task outputs today take
a different route — `UserTask.Exec` calls `re.Put(ut.completedOutputs...)`
(`pkg/model/activities/user_task.go:199-208`) onto the node's frame
(`internal/instance/execenv.go:141` → `internal/scope/frame.go:201`). A node-frame write
dies with the node, so FR-7 commits at **root**.

### 1.8 A parked UserTask is dehydratable

`UserTask.Dehydratable` returns `true` unconditionally
(`pkg/model/activities/user_task.go:251-253`), and ADR-007 v.2.1 §2.4 names the
distributor's completion path as its wake-holder. Ownership operations therefore must
assume the instance is **absent**, not merely idle.

---

## §2 Requirements

### Functional

| # | Requirement |
|---|---|
| **FR-1** | A parked UserTask has an **actual owner**: at most one actor identity, or none. It is per-task-per-instance runtime state, never written back into the process definition (ADR-020 v.2 §2.5.1). |
| **FR-2** | The engine-level task registry value grows from a bare instance id into a record holding `{instanceID, eligibility, owner}`, mutated under the existing `Thresher.m` mutex (§1.2). |
| **FR-3** | `Claim`, `Unclaim` and `Reassign` are served **from the registry alone** — no `onTaskInstance`, no hydration, no instance roundtrip — and never advance, resume or cancel execution (ADR-020 v.2 §2.1.1, §2.5.2). |
| **FR-3a** | `Claim(taskID, actor)` succeeds iff the actor is eligible **and** the task is not held by a **different** actor; over another's hold it fails with a distinct, non-terminal "already held" error. Re-claiming a task the actor already owns is an **idempotent no-op** — the guard protects against taking another's work, and refusing a same-owner claim would make the operation unsafe to retry and leave a born-owned task (FR-6) uncompletable by its own assignee. |
| **FR-3b** | `Unclaim(taskID, actor)` succeeds iff the actor **is** the current owner; the task returns to unowned and any eligible actor may claim it. |
| **FR-3c** | `Reassign(taskID, nominee)` performs **no check on the caller** and overrides any existing owner, but refuses a nominee who fails eligibility (ADR-020 v.2 §2.5.2). |
| **FR-4** | `Complete` refuses any actor that is not the current owner, and refuses outright on an **unowned** task. The refusal is non-terminal — the task stays parked (ADR-020 v.2 §2.4.1). The thresher gates **eligibility then ownership, in ADR-020 §2.4's stage order, before hydrating**, so a doomed completion costs no hydration; the instance's own eligibility check still runs afterwards (FR-5c). |
| **FR-5** | The triad is resolved **once**, in `buildTaskInfo` at distribution, into a materialized eligibility snapshot that preserves slot separation (assignee / candidateUsers / candidateGroups / open). No path re-resolves an `hi.Assignment` after distribution (ADR-020 v.2 §2.7). |
| **FR-5a** | The snapshot is **write-once and read-only** after distribution, and is therefore stored in **both** `taskEntry` (instance-side checks) and the thresher's task record (registry-side checks). Two readers of an immutable value cannot disagree, so this is not state duplication in the hazardous sense (§4.1, NFR-1). |
| **FR-5b** | `UserTask.Authorize` keeps its **exact signature, call contract and observable behaviour**; internally it resolves the triad and delegates to the snapshot's membership rule, so the existing verdict precedence and its tests are unchanged (§4.6). |
| **FR-5c** | The instance-side eligibility check **stays on the instance**, where ADR-020 §2.4/§5 places it — reading the frozen snapshot from `taskEntry` instead of re-resolving through a throwaway scope frame. The `openFrame("task-authz")`/`Discard` pair (`internal/instance/tasks.go:206-219`) is retired as a simplification, not a relocation. |
| **FR-5d** | The **denial error has exactly one author**: `Eligibility.Authorize` (§3.1). Every caller — `UserTask.Authorize`, the instance loop, the thresher's gate — surfaces the identical error, so an embedder cannot tell which internal component refused. `UserTask.unauthorized` is folded into it and deleted (§4.7). The message and the `task_id`/`user_id` details are carried over verbatim; the **class string moves** from `ACTIVITIES_ERRORS` to `INTERACTOR_ERRORS`, since the error is now authored in `pkg/interactor` — a deliberate, documented change. `errs.ConditionFailed` is unchanged, so class-kind checks are unaffected. |
| **FR-5e** | A triad that **cannot be resolved fails closed**: `resolveEligibility` returns `DeniedEligibility()` — authorizing nobody — never the zero `Eligibility`, which would read as an *open* task and silently authorize every actor. The failure is logged **and** reported as a `KindTaskState` / `PhaseFailed` fact carrying `reason=eligibility_resolution_failed` and `effect=denied_all_actors`, so a silent authorization bypass is impossible and an operator can see why a task became uncompletable. |
| **FR-6** | Where the resolved assignee slot yields **exactly one** identifier, the task is registered already owned by it; several identifiers, or none, register unowned (ADR-020 v.2 §2.5.3). A born-owned task accepts `Unclaim` and `Reassign` like any other. |
| **FR-7** | On accepted completion the engine records the acting actor in the **performer register**, keyed by node name, readable by later expressions through `RUNTIME/COMPLETED_BY` and outliving the task (ADR-020 v.2 §2.4.2). It is engine-written and **read-only to the process**; a caller-supplied value is never trusted. The register is carried across a hydrate on the instance checkpoint. |
| **FR-8** | Three new `KindTaskState` phases — `Claimed`, `Unclaimed`, `Reassigned` — carry the task id and actor; `Reassigned` carries **both** parties. A completion refused for non-ownership is observable distinctly from one refused for authorization. The eligibility-resolution failure of FR-5e is reported on the same `KindTaskState` stream. |
| **FR-9** | Cancellation is unchanged by ownership: `cleanupTask` and `withdrawAllTasks` tear down and withdraw an owned task exactly as an unowned one, and drop its registry record (ADR-020 v.2 §2.1.1). |
| **FR-10** | The vendored extract gains instance-attribute coverage for Table 10.14 (`actualOwner`, `taskPriority`), so the layer this SRD implements stops being invisible to a reviewer (ADR-020 v.2 §3 pin note, §7 step 5). |
| **FR-11** | The reference console distributor and every runnable example carrying a UserTask move to the claim-then-complete flow. |

### Non-functional

| # | Requirement |
|---|---|
| **NFR-1** | **Mutable** ownership state lives in exactly one place — the thresher's task record, mutated under the existing `Thresher.m` (§1.2) — and the instance loop stays the single writer for everything it already owns. The **immutable** eligibility snapshot may be held in both places (FR-5a); write-once data has no consistency hazard. *One new lock is accepted:* the performer register (§3.5) guards its own map, because it is written on the loop goroutine at completion and read during expression evaluation on track goroutines. It is a short critical section over a small map, with no I/O. |
| **NFR-2** | An ownership operation on a **dehydrated** instance completes without hydrating it — asserted by a test that dehydrates, claims, and checks residency is unchanged. |
| **NFR-3** | Concurrent `Claim`s on one task yield exactly one winner, proven under `-race`. |
| **NFR-4** | `make ci` green: tidy, lint, build, race tests, diff-coverage ≥ `COVER_MIN` (95, `Makefile:15`), govulncheck. |
| **NFR-5** | Every exported addition carries a doc comment naming the ADR §it implements, per the project's Go house rules. |
| **NFR-6** | Public API parameters are validated on entry with self-identifying errors (`Claim: an empty task id isn't allowed`), extending `checkTaskArgs` (`internal/instance/tasks.go:95-110`) rather than duplicating it. |

---

## §3 Models (shapes)

### 3.1 `interactor.Eligibility` — the materialized triad

New types in `pkg/interactor`, resolved by the `UserTask` and carried on `TaskInfo`. The
three slots stay separate so ADR-020 §2.5's precedence survives verbatim, and the verdict
rule is a method — authored where v.1 put it (§4.1).

**Each slot records whether the model *declared* it, separately from what it *resolved
to***. This is load-bearing rather than cosmetic: today's verdict
gates on `ut.assignee != nil` — the slot being **declared** — not on the resolved set being
non-empty (`pkg/model/activities/user_task_authz.go:75`). So a declared assignee whose
expression resolves to nothing **denies**, because BPMN treats a failed resource query as an
empty result set. A flat `Assignee []string` cannot express that: `len() == 0` would fall
through to the candidate slots and wrongly authorize. Encoding it as nil-vs-empty slice
works in Go but is a footgun on a public struct, so the flag is explicit:

```go
// ResolvedSlot is one triad member resolved to identifiers, keeping *whether the
// model declared it* distinct from *what it resolved to*. A declared slot that
// resolves to nothing authorizes no one; an undeclared slot is absent from the
// verdict.
type ResolvedSlot struct {
    Declared bool
    IDs      []string
}

// Eligibility is the triad resolved to identifier sets at distribution time
// (ADR-020 v.2 §2.7). It is the frozen input to every later authorization check:
// once materialized it never re-resolves, so an actor's right to act cannot be
// revoked by an unrelated data change while the task waits.
type Eligibility struct {
    Assignee        ResolvedSlot
    CandidateUsers  ResolvedSlot
    CandidateGroups ResolvedSlot
}

// Open reports a task no triad member was declared for — authorized for any actor
// (BPMN's unspecified performer). Derived, not stored, so it cannot disagree with
// the slots.
func (e Eligibility) Open() bool

// Authorize reports whether actor may act on taskID, applying ADR-020 v.2 §2.5's
// verdict to the frozen sets: open → anyone; an assignee → only a matching UserID;
// otherwise a matching candidate user OR an intersecting candidate group. A nil
// error means authorized; a non-nil error is the NON-TERMINAL denial, and it is
// authored here so that every caller — the UserTask, the instance loop and the
// thresher's gate — produces the identical error (§4.7).
func (e Eligibility) Authorize(taskID string, actor hi.Actor) error

// permits is the membership predicate Authorize wraps; unexported so the denial
// error has exactly one author.
func (e Eligibility) permits(actor hi.Actor) bool

// DeniedEligibility returns an Eligibility that authorizes NOBODY — a declared
// assignee slot resolving to no one. It is the FAIL-CLOSED value for a triad that
// could not be resolved (FR-5e).
func DeniedEligibility() Eligibility
```

### 3.2 `TaskInfo` gains the snapshot

```go
type TaskInfo struct {
    TaskRef
    Roles []*hi.ResourceRole
    // Eligible is the resolved triad (ADR-020 v.2 §2.7), carried so the engine-level
    // registry can authorize ownership operations without a resident instance.
    Eligible Eligibility
}
```

### 3.3 `HumanTask` gains resolution, keeps `Authorize` unchanged

```go
// ResolveEligibility resolves the task's assignment triad against src via eng into a
// frozen Eligibility (ADR-020 v.2 §2.7). Called once, at distribution, while the
// instance is resident and its data scope is available.
ResolveEligibility(ctx context.Context, src data.Source, eng expression.Engine) Eligibility
```

`Authorize` stays on the interface with its signature and behaviour intact (FR-5b, §4.6).
Its body becomes a delegation, so the verdict lives in exactly one implementation:

```go
func (ut *UserTask) Authorize(
    ctx context.Context, actor hi.Actor, src data.Source, eng expression.Engine,
) error {
    if actor == nil { /* unchanged nil guard */ }

    return ut.ResolveEligibility(ctx, src, eng).Authorize(ut.ID(), actor)
}
```

`UserTask.unauthorized` (`pkg/model/activities/user_task_authz.go:102-109`) folds into
`Eligibility.Authorize` and is deleted — its `errs.ConditionFailed` class and its
`task_id`/`user_id` details move verbatim, so the denial an embedder observes is unchanged
(§4.7).

### 3.3a `taskEntry` carries the snapshot

```go
type taskEntry struct {
    track *track
    node  flow.Node
    // eligible is the frozen triad resolved at distribution (FR-5a) — read-only,
    // so holding a copy here and in the thresher record is safe.
    eligible interactor.Eligibility
}
```

### 3.4 The thresher task record

`tasks map[string]string` becomes:

```go
// taskRecord is the engine-level state of a parked human task: where it lives, who
// may act on it, and who currently holds it. It outlives its instance's residency
// (ADR-020 v.2 §2.1.1), so ownership operations never hydrate.
type taskRecord struct {
    instanceID string
    eligible   interactor.Eligibility
    // owner is the BPMN actualOwner (§10.3.4.1 Table 10.14) — a user-id literal,
    // empty when the task is unowned.
    owner string
}
```

### 3.5 The performer register

Engine state on the `Instance`, served read-only through the reserved `RUNTIME` subtree
and carried across a hydrate on the checkpoint (ADR-020 v.2 §2.4.2):

```go
// performers records who actually completed each of the instance's human tasks.
// Guarded by its own mutex: written on the instance-loop goroutine at completion,
// read during expression evaluation on track goroutines.
type performers struct {
    byNode map[string]string // node name (or id) → completer's user id
    m      sync.Mutex
}
```

Exposed as **one map-valued runtime variable**, so the runtime name set stays closed:

```
RUNTIME/COMPLETED_BY  →  values.Map[string]   // node name → user id
```

and persisted alongside the conversation keys:

```go
// checkpoint.Document
CompletedBy map[string]string `json:"completed_by,omitempty"`
```

## §4 Analysis & decisions

### 4.1 Splitting *resolution* from the *rule* — and why nothing has to move

ADR-020 v.2 §2.1.1 requires ownership operations to work with no resident instance; v.1
§2.4/§5 place the check on the model element with the `Instance` orchestrating. Naively
these conflict: the check needs the triad, the triad needs scope, scope needs an instance.

They stop conflicting once **resolution** and **rule** are separated. Today's `Authorize`
already contains both (§1.4): it resolves each slot through `hi.Assignment.Resolve` and
then applies a pure membership test in strict precedence. Extract the resolved sets into
`Eligibility` and the rule becomes a function of frozen data — no scope, no node — while
resolution stays on the `UserTask`, run once at distribution where scope is available
(§1.5).

**An earlier draft of this SRD then posed a false choice** — pass the snapshot down into
the instance, or relocate the whole check to the thresher — on the premise that only one
component may hold the snapshot. That premise was an over-applied caution: the snapshot is
**write-once and read-only after distribution**, and two readers of immutable data cannot
drift. Duplication is hazardous for *mutable* state only. Dropping the bad premise removes
the choice: both components hold a copy (FR-5a), and consequently

- the instance keeps checking eligibility exactly where ADR-020 §2.4/§5:763 says it does
  (FR-5c) — it merely reads `taskEntry.eligible` instead of re-resolving,
- the thresher can gate eligibility **and** ownership before hydrating (§4.2),
- `Authorize`'s public contract is untouched (FR-5b),
- **no ADR amendment is needed**, and no check changes owner.

The disruption reduces to an internal refactor of one method plus a read-only field on two
structs.

**Rejected — ownership (the mutable half) in `taskEntry`.** `internal/instance/tasks.go:50`
is loop-owned and dies with residency, so every claim would hydrate a released instance —
contradicting §2.1.1 and making the cheapest operation the most expensive. Ownership lives
in the thresher record; only the frozen snapshot is shared.

**Rejected — re-implementing the verdict on the thresher.** Would work, but duplicates
`Authorize`'s precedence away from the element declaring the triad, and guarantees the two
drift. One rule, authored as an `Eligibility` method, consumed by both sides.

**Rejected — keeping live per-call resolution for `Take`/`Complete`** while ownership uses
the snapshot. Least code churn, and identical in behaviour whenever the triad is static.
But an expression-backed triad could then admit an actor at claim time and deny them at
completion, so an owner could lose the ability to finish work they legitimately hold —
precisely the failure ADR-020 v.2 §2.7's freeze exists to prevent. Rejected on correctness.

### 4.2 The thresher's early gate — **chosen: eligibility then ownership, pre-hydration**

`Complete` must hydrate to bind outputs and resume (§1.3), but neither of its first two
stages needs an instance once the snapshot is at hand. The thresher therefore applies them
in ADR-020 §2.4's own order — eligibility, then ownership — before `onTaskInstance`, so a
wrong-actor or unowned completion is refused without hydrating anything. That is the common
case during a long park, which is exactly when hydration is most expensive.

The instance's own eligibility check (FR-5c) then runs again on the resident path. This is
**deliberate defence in depth**: it costs one membership test over frozen data, it can
never disagree with the thresher's verdict, and it keeps ADR-020 §2.4/§5 literally true
rather than requiring an amendment. Removing it would buy nothing and cost a documentation
change to a contract two other documents pin.

**Rejected — ownership checked only inside `handleTaskRequest`.** Symmetrical with the
existing authorization call, but it forces a hydration to learn the answer and it inverts
§2.4's stage order relative to a pre-hydration ownership gate.

### 4.3 Where the performer record lives — **chosen: the `RUNTIME` subtree, as a map**

The record is **engine-published**, and that decides where it belongs. A process must be
able to READ who performed a task; it must NOT be able to overwrite the answer, nor
collide with it by naming a variable the same way. The reserved `RUNTIME` subtree is
read-only by construction (`Commit` and `OpenScope` reject it — `internal/scope/runtimevars.go:8-11`),
so it grants exactly the first power and withholds the second.

`RUNTIME` already serves recorded constants rather than only live state — `STARTED_AT`
is `inst.startTime`, retained on the Instance (`internal/instance/runtimevars.go:37`) —
so a performer register is a new *entry*, not a new *category*.

Exposed as **one map-valued variable** (node → completer), not one variable per task, so
the supplier's name set stays closed: an open per-task namespace would force prefix
matching in `RuntimeVar` and make `RuntimeVarNames` grow with every completion.

**It must ride the checkpoint.** The register is Instance state, and the checkpoint
persists the data plane plus a few explicit fields — `startTime` is NOT among them, so
retained fields are otherwise lost on a hydrate. A human task is the wait most likely to
dehydrate, so an in-memory-only register would vanish precisely in the case it exists
for. It is therefore captured and restored alongside the conversation keys, whose
`snapshotKeys`/`restoreKeys` pattern it mirrors (`internal/instance/correlation.go:257-287`).

**Rejected — a data-plane commit at root scope** (implemented and then withdrawn during this landing). It works and is durable for free, but it hands the process both powers the
record must not grant: a modeler can overwrite the audit record, and can collide with it
by naming an ordinary variable the same way. It also forced two accidental constraints —
the name could not contain `.` (reserved in a data name), and the datum had to be a
`Parameter` over an `ItemAwareElement`, because a `Property` does not satisfy the scope's
`Clone() (*ItemAwareElement, error)` requirement and committing one silently deferred
every subsequent checkpoint.

**Rejected — a flat `completedBy`.** Collides across tasks; the last writer wins and the
approval pattern silently reads the wrong performer.

**Rejected — keyed by task id.** More faithful to the task, but task ids are
engine-generated and unknown to a modeler writing an expression, so nothing could
reference it. Node name is the handle a modeler actually has.

### 4.4 Reassign takes an identity, not an `Actor` — **chosen**

`Claim`/`Unclaim` act on behalf of the acting human, so they take `hi.Actor` like
`Take`/`Complete`. `Reassign` nominates a **third party** who is not present and cannot
self-report groups. It therefore takes the nominee's user id, and eligibility is checked
against the frozen assignee/candidateUsers sets. A group-only-eligible nominee cannot be
reassigned to, since group membership is authenticated by the embedder for a *present*
actor and cannot be asserted for an absent one — surfaced as an explicit limitation
(§9), not silently.

### 4.5 Why no new lock (NFR-1)

Mutable ownership lives in exactly one map, already guarded by `Thresher.m` (§1.2), and
every ownership operation is a short critical section with no I/O. The frozen eligibility
snapshot needs no guarding at all — it is written once at distribution and read-only
thereafter (FR-5a). The instance loop is untouched, so its single-writer discipline is
preserved by construction rather than by convention.

### 4.6 `Authorize` is retained although the engine no longer calls it — **chosen**

Once the instance reads the frozen snapshot (FR-5c), no engine path calls
`UserTask.Authorize`. It is nevertheless **kept** on `interactor.HumanTask` with its
signature and behaviour intact, delegating to the same rule (§3.3).

Reasons: it is **public API** an embedder may reasonably call to pre-flight an actor
against a task, so removing it is a breaking change bought for nothing; and it remains the
readable statement of the verdict — its existing table tests
(`pkg/model/activities/user_task_authz_test.go`) go on proving the precedence that
`Eligibility.Authorize` now enforces, which is exactly the regression net this refactor
wants.

This is a deliberate, narrow exception to §9's "no dead surface" clause: the surface is
live, tested public API, merely no longer on the engine's hot path. It is called out here
so a later reader does not mistake it for an oversight and delete it.

**Rejected — drop `Authorize` from the interface.** Cleaner call graph, at the price of a
breaking public-API change and the loss of the precedence tests' subject.

---

### 4.7 The denial error lives on `Eligibility` — **chosen**

Extracting the verdict leaves the *denial* homeless: a `bool` predicate cannot carry
`errs.ConditionFailed`, the `task_id`/`user_id` details, or the message. Two placements
were possible.

**Chosen — `Eligibility.Authorize(taskID, actor) error` owns it.** One author, so the error
is identical whichever component asks: the `UserTask`, the instance loop, or the
thresher's pre-hydration gate. (The class string therefore becomes `INTERACTOR_ERRORS`
rather than the activities package's — see FR-5d.) That matters precisely because this SRD adds a *new* caller
(the gate): an embedder must not be able to tell, from the error, which internal component
refused. `UserTask.unauthorized` folds in and is deleted, so the construction exists once
rather than twice. Cost: `pkg/interactor` takes a dependency on `pkg/errs` — already
pervasive across the tree, and it lets the predicate stay unexported.

**Rejected — each caller builds its own error.** The instance would construct an equivalent
denial with its own package's `errorClass`, so the *same* refusal would report a different
class depending on where it was caught, and the two constructions would drift the first time
one is edited. Discovering that from the outside would look like a bug in the engine, not a
design choice.

Per the project's public-API rule, `Eligibility.Authorize` validates both parameters on
entry — an empty `taskID` and a nil `actor` are refused with self-identifying errors.

### 4.8 Failing closed on an unresolvable triad — **chosen**

Moving resolution to distribution introduces a failure mode v.1 of this SRD did not
consider: the scope frame the expressions read may fail to open. The zero `Eligibility` is
the wrong answer there, and dangerously so — no slot declared means **open to any actor**,
so an infrastructure failure would silently become an authorization bypass. That is a
fail-open default hidden inside a value's zero state, the same class of defect as a
constructor letting `nil` overwrite a working default.

`resolveEligibility` therefore returns `DeniedEligibility()` — a declared assignee slot
resolving to nobody, which the verdict refuses for everyone. The task stays parked and
uncompletable: **visible and recoverable**, where the alternative is invisible and
unrecoverable. And because a task that silently stops being completable is itself a
support incident, the failure is not merely logged but reported as a fact (FR-5e), so an
operator can tell "denied all actors because resolution failed" from "denied because you
are not a candidate".

**Rejected — return the zero value and log.** What the first implementation did. A log line
is not a guard: the task would have been open to everyone, and the log would have been the
only evidence.

**Rejected — abort distribution when resolution fails.** Refusing to announce the task at
all is also fail-closed and arguably tidier. But it loses the task from the distributor's
inbox entirely, so nobody — not even an administrator — can see that it exists and is
stuck, whereas a parked-but-denying task remains visible and can be rescued by `Reassign`
once the underlying cause is fixed.

---

## §5 API (public surface)

Three additions on `Thresher`, alongside the existing `Take`/`Complete`
(`pkg/thresher/tasks.go:86,107`):

```go
// Claim makes actor the task's actual owner (BPMN actualOwner, §10.3.4.1 Table 10.14),
// so that only actor may complete it (ADR-020 v.2 §2.5.2). It fails if the task is
// already owned, or if actor is not eligible. Non-terminal: the task stays parked and
// the instance is not hydrated.
func (t *Thresher) Claim(ctx context.Context, taskID string, actor hi.Actor) error

// Unclaim releases actor's hold, returning the task to the eligible pool. Only the
// current owner may unclaim (ADR-020 v.2 §2.5.2).
func (t *Thresher) Unclaim(ctx context.Context, taskID string, actor hi.Actor) error

// Reassign moves ownership to nomineeUserID, overriding any current owner. It performs
// NO check on the caller — authorizing an administrative handover is the embedder's
// responsibility (ADR-020 v.2 §2.5.2) — but refuses a nominee the process says may not
// perform the task.
func (t *Thresher) Reassign(ctx context.Context, taskID, nomineeUserID string) error
```

`Complete`'s contract tightens (FR-4): it now also refuses a non-owner, and refuses on
an unowned task. `Take` is unchanged in shape and still authorizes — now against the
frozen snapshot.

---

## §6 Test scenarios

| V | Scenario | Asserts |
|---|---|---|
| V1 | `Eligibility.Authorize` table test — open / assignee-set / candidate-user / candidate-group / assignee-overrides-candidates / empty-everything | FR-5, the verdict matches `Authorize`'s v.1 truth table exactly |
| V1a | The denial from `Eligibility.Authorize` carries `errs.ConditionFailed` plus `task_id` and `user_id`, and is identical whether reached via `UserTask.Authorize`, the instance loop or the thresher gate | FR-5d, §4.7 |
| V1b | Empty `taskID` / nil `actor` into `Eligibility.Authorize` | NFR-6; self-identifying errors |
| V2 | `ResolveEligibility` on static and expression-backed triads; a failing expression | FR-5; failure → empty set, authorizes nobody (§1.4) |
| V2a | The **existing** `user_task_authz_test.go` table passes unchanged against the delegating `Authorize` | FR-5b, §4.6; the refactor is behaviour-preserving |
| V2b | The instance-side check denies an ineligible actor even when the thresher gate is bypassed (calling `Instance.Take`/`Complete` directly) | FR-5c, §4.2; defence in depth is real, not ceremonial. **M2** — at M1 there is no gate to bypass, and the existing end-to-end already proves the instance denies an ineligible actor through the frozen snapshot (`pkg/thresher/user_task_test.go`) |
| V2c | An expression-backed triad is evaluated **exactly once**, at distribution, and never again — proven by a counting expression engine across Take / Claim / Unclaim / Claim / Complete | FR-5; the evaluation count is the evidence, and it is stronger than the originally-specified "underlying data changes after distribution": no API mutates instance data mid-park, and a count of one proves no path re-resolves at all. **M2** |
| V2d | A declared slot that resolves to **nobody** stays `Declared` and denies, never falling through to the candidate slots | FR-5, §3.1; the distinction a flat `[]string` would have lost |
| V2e | `DeniedEligibility()` authorizes no actor, and is **not** `Open()` | FR-5e, §4.8 |
| V2f | An unresolvable triad denies all actors and emits `PhaseFailed` with `reason=eligibility_resolution_failed` | FR-5e, FR-8; the fail-open bypass cannot recur |
| V3 | Claim an unowned task as an eligible actor | FR-3a; owner set, `PhaseClaimed` emitted |
| V4 | Claim an already-owned task | FR-3a; distinct "already owned" error, owner unchanged |
| V5 | Claim as an ineligible actor | FR-3a; refused, task unowned |
| V6 | Unclaim as owner, then re-claim by a different eligible actor | FR-3b; `PhaseUnclaimed`, then a successful second claim |
| V7 | Unclaim as a non-owner | FR-3b; refused, owner unchanged |
| V8 | Reassign over an existing owner, to an eligible nominee | FR-3c; owner replaced, `PhaseReassigned` carries both parties |
| V9 | Reassign to an ineligible nominee | FR-3c; refused, owner unchanged |
| V10 | Complete as owner | FR-4; outputs bound, token resumes, `completedBy` committed |
| V11 | Complete as an eligible non-owner | FR-4; refused non-terminally, task still parked, still owned |
| V12 | Complete an unowned task | FR-4; refused; claim-then-complete then succeeds |
| V13 | Single resolved assignee | FR-6; task registered owned; `Claim` fails as already-owned; `Unclaim` succeeds |
| V14 | Multi-identifier assignee expression, and no triad | FR-6; both register unowned |
| V15 | `completedBy` read by a downstream expression after the task is gone | FR-7; the approver pattern of ADR-020 v.2 §1.4 end-to-end |
| V16 | Reassign, then complete as the new owner | FR-7; `completedBy` is the actual finisher, not the first assignee |
| V17 | Dehydrate, then claim/unclaim/reassign | **NFR-2**; operations succeed and residency is unchanged — no hydration |
| V18 | Concurrent claims under `-race` | NFR-3; exactly one winner |
| V19 | Interrupting boundary event cancels an owned task | FR-9; withdrawn, registry record dropped, no leak |
| V20 | Instance teardown with an owned task | FR-9; `withdrawAllTasks` clears it |
| V21 | Empty task id / nil actor / empty nominee on each new operation | NFR-6; self-identifying errors |

Existing tests that must stay green or be updated deliberately:
`internal/instance/tasks_internal_test.go`, `internal/instance/user_task_internal_test.go`,
`pkg/model/activities/user_task_authz_test.go`, `pkg/interactor/distributor_test.go`,
`pkg/interactor/console/console_test.go`.

---

## §7 Milestones

Each milestone is one commit, independently verifiable, `make ci` green.

| M | Scope | Verifies |
|---|---|---|
| **M1** *(landed)* | `interactor.ResolvedSlot`/`Eligibility` + `Authorize`/`permits`/`Open`/`DeniedEligibility` (the denial's single author, `unauthorized` folded in and deleted); `ResolveEligibility` on `UserTask` with `Authorize` refactored to delegate (contract intact); resolution wired into `buildTaskInfo`, failing closed with a fact; `TaskInfo.Eligible` + `taskEntry.eligible`; the instance check reads the snapshot and `authorizeTask`'s throwaway frame retires | FR-5, FR-5a–e, V1, V1a, V1b, V2, V2a–V2f |
| **M2** *(landed)* | `taskRecord` replaces `tasks map[string]string`; `Claim`/`Unclaim`/`Reassign` on `Thresher`, registry-only; the pre-hydration gate; the three ownership phases. **Absorbed M3 and FR-4** — see the note below | FR-1, FR-2, FR-3a–c, **FR-4**, **FR-6**, FR-8 (ownership phases), NFR-1, NFR-2, NFR-3, NFR-6, V2b, V2c, V3–V9, V13, V14, V17, V18 |
| ~~**M3**~~ | ~~Birth-ownership from a single resolved assignee~~ — **landed in M2**: `registerTask` is the only place a task enters the registry, so the born-owner rule belongs to the same function and splitting it would have meant writing the record twice | FR-6, V13, V14 |
| **M4** *(landed)* | `completedBy` committed at root scope by `completeTask`, keyed `<node>_completedBy`; the `TaskUnclaimedClass` / `TaskNotOwnerClass` refusal classes plus a reported `reason` per refusal | FR-7, FR-8 (remainder), V10, V15, V16 |
| **M5** *(landed)* | Cancellation/teardown parity and registry-record cleanup for owned tasks | FR-9, V19, V20 |
| **M6** *(landed)* | `docs/bpmn-spec/` instance-attribute coverage (Table 10.14) — a hand-authored `semantics/instance-attributes.md`, since `elements/` is generated from an XML metamodel that structurally cannot emit instance attributes | FR-10 |
| **M7** *(landed)* | Console distributor + examples on claim-then-complete; smoke each end-to-end | FR-11 |
| **M8** *(landed)* | ADR-020 Russian twin brought to v.2.3 | ADR-020 doc-set completeness |

---

## §8 Cross-doc

- **Implements** [ADR-020 v.2](../design/ADR-020-human-interaction-execution-model.md)
  §2.1.1, §2.4.1, §2.4.2, §2.5.1–§2.5.3, §2.7 — upward, versioned.
- **Upstream** [ADR-007 v.2.1](../design/ADR-007-in-memory-long-waits.md) §2.4
  (dehydratable human task — the constraint behind FR-3/NFR-2),
  [ADR-011 v.7](../design/ADR-011-process-data-flow.md) (instance data — FR-7's surface),
  [ADR-013 v.2](../design/ADR-013-instance-observability.md) (FR-8's fact stream),
  [ADR-033 v.2](../design/ADR-033-persistence-and-state.md) (owns the
  restart-durability deferral this SRD does **not** close).
- **Sideways** [SRD-034 v.1](SRD-034-user-task-execution.md) (the landing this extends —
  not retro-edited; it is a correct snapshot of ADR-020 v.1),
  [SRD-071 v.2.5](SRD-071-instance-dehydration-and-wake-on-trigger.md) §FR-8 (the
  hydrate/replay path `Complete` keeps and ownership operations bypass).
- No downward references. `SRD-073` was free at authoring time; the reservations on
  record (063 datastore, 068 dataobjects) are both landed.

**Downstream docs to sync before merge** (reverse-direction sweep): the guides
`docs/guides/tasks/user-task.md` (its `TaskState` log walkthrough at lines 166-176 gains
the ownership phases), `docs/guides/operating/human-tasks.md`,
`docs/guides/extending/task-distributor.md`, plus `README.md` and its translated twins if
the claim-then-complete flow reaches the feature tour.

---

## §9 Definition of Done

1. FR-1 … FR-11 each wired to real, reachable code — no dead surface, with the single
   documented exception of `UserTask.Authorize`, retained deliberately as tested public API
   (§4.6).
2. NFR-1 … NFR-6 demonstrated, NFR-2 and NFR-3 by named tests (V17, V18).
3. V1 … V21 exist and pass.
4. `make ci` green across modules: tidy-check, lint, build, `-race` tests,
   `make cover-check` ≥ `COVER_MIN` (95) on touched lines, govulncheck.
5. Touched-function coverage ≥ 80% (target 100%), measured per function on the diff, not
   as a package aggregate. **One recorded exception:** `instance.resolveEligibility`'s
   fail-closed branch (FR-5e). It is unreachable from any constructible state —
   `openFrame` dereferences `sc.plane.Root()` before `scope.NewFrame`'s nil guard, so a nil
   plane panics rather than erroring, and tripping `checkContained` means contorting scope
   internals. Adding an injection seam was rejected: reshaping production code for a
   three-line defensive branch costs more clarity than the coverage buys. The *consequence*
   is fully covered — `DeniedEligibility()` at 100%, and V2e proves it denies every actor.
   The function therefore lands at 62.5%; every other touched function is at 100%.
6. Every runnable example executes end-to-end under a timeout and exits 0; built example
   binaries stay gitignored.
7. ADR-020's Russian twin is at v.2 (M8).
8. Downstream docs synced (§8) — guides and README twins included.
9. §10 filled with files/lines, V-results and milestone SHAs; **then** ADR-020 v.2 and
   this SRD flip to Accepted at the PR handover — not before.

**Explicitly out of scope**, inherited from ADR-020 v.2 §7: `taskPriority`; escalation;
WS-HumanTask's delegate-vs-forward and suspend/resume; cross-instance or bulk ownership
operations; ownership surviving an engine restart; an identity/directory subsystem. Also
out of scope by §4.4: reassignment to a nominee eligible only through a **group**.

---

## §10 Implementation summary

### Milestone commits

| M | SHA | Scope | Diff |
|---|---|---|---|
| M1 | `21508b0` | `interactor.Eligibility`/`ResolvedSlot` + the verdict rule and its denial error; `UserTask.ResolveEligibility` with `Authorize` delegating; resolution wired into `buildTaskInfo`; `TaskInfo.Eligible` + `taskEntry.eligible`; `authorizeTask` and its throwaway frame retired | 8 files, +503/−87 |
| M2 (+M3, FR-4) | `836eaa1` | `taskRecord` replaces `tasks map[string]string`; `Claim`/`Unclaim`/`Reassign`; the pre-hydration gate; birth-ownership; the three ownership phases | 16 files, +937/−54 |
| M4 | `e1b00ab` | The performer register on the `Instance`, served read-only as `RUNTIME/COMPLETED_BY` and carried across a hydrate; the two refusal classes and a reported reason per refusal | 16 files, +671/−52 |
| M5–M7 | `7c65fba` | Teardown parity for held tasks; `docs/bpmn-spec/semantics/instance-attributes.md`; example narration | 9 files, +238/−12 |
| M8 | `b7cebbb` | ADR-020 Russian twin to v.2 (519 → 958 lines) | 2 files, +499/−58 |

Doc commits: `6705c0b` (ADR-020 v.2), `c5e9985` + `439163f` (this SRD).

### Beyond the milestones

Three later commits belong to this landing but are not milestones, and the connection
would otherwise be unrecoverable once this document freezes:

| SHA | What | Why it is here |
|---|---|---|
| `7068cc8` | The doc-versioning rule in `CLAUDE.md`, plus the four `/check-srd` findings | This branch churned ADR-020 through v.2→v.2.3 and this SRD through v.1→v.1.8, none of them accepted by anyone. The rule that only SAD/ADR carry versions — and that a Draft is simply edited — was written because of that churn, and the versions were then collapsed. |
| `c39e5b6` | `inst.startTime` is now carried across a hydrate | A **pre-existing defect outside this SRD's scope**, found by it: adding the performer register to the checkpoint proved that retained `Instance` fields are lost on hydrate, and `startTime` was the same trap one field over. Every rebuild silently restamped the instance's start time, so `RUNTIME/STARTED_AT` reported the age of the latest rebuild rather than of the process. |
| `a747a77` | `TestParallelMultiInstanceCompletionCancelsRemainder` de-flaked | Master's test, surfaced when this branch merged 29 commits of master mid-flight. It counted a cancellation only from the body's `ctx.Done()` branch, so an instance canceled before its body was scheduled never counted itself — ~1 failure in 10. |

The branch also merged `origin/master` (`eaf6921`) partway through, which is why the
conformance tracker and roadmap were re-checked afterwards (`d5eba8f`): master had
moved those same documents, and ownership belonged in its existing **Human interaction**
row rather than the separate row this branch first added.

### Where each requirement landed

| Requirement | Code |
|---|---|
| FR-1, FR-2 | `pkg/thresher/tasks.go` — `taskRecord` (`:52`), `registerTask` (`:63`) |
| FR-3, FR-3a–c | `Claim` (`:261`), `Unclaim` (`:284`), `Reassign` (`:318`), `setOwner` (`:350`), `claimGuard`/`ownerOnlyGuard` — registry-only, no `onTaskInstance` |
| FR-4 | `Complete` (`:155`) calls `gateComplete` (`:191`) → `completeVerdict` before resolving the instance |
| FR-5, FR-5a–c | `pkg/interactor/eligibility.go`; `UserTask.ResolveEligibility` (`user_task_authz.go:59`); `buildTaskInfo`/`taskEntry.eligible`; `handleTaskRequest` (`internal/instance/tasks.go:184`) |
| FR-5d | `Eligibility.Authorize` (`eligibility.go:68`) — sole author of the denial |
| FR-5e | `DeniedEligibility` (`:51`) + `denyByResolutionFailure` (`internal/instance/tasks.go:497`) |
| FR-6 | `bornOwner` (`pkg/thresher/tasks.go:79`) |
| FR-7 | `internal/instance/performers.go`; `recordCompletedBy` (`tasks.go:266`); `RuntimeVar`/`RuntimeVarNames` (`runtimevars.go`); `checkpoint.Document.CompletedBy` + capture/restore |
| FR-8 | `PhaseClaimed`/`PhaseUnclaimed`/`PhaseReassigned` + `AttrUserID`/`AttrFromUserID`/`AttrToUserID` (`pkg/observability/fact.go`); `TaskUnclaimedClass`/`TaskNotOwnerClass` (`pkg/thresher/tasks.go:180,185`) |
| FR-9 | `cleanupTask`/`withdrawAllTasks` (`internal/instance/tasks.go:403,413`), unchanged by ownership — proven rather than modified |
| FR-10 | `docs/bpmn-spec/semantics/instance-attributes.md` + index and `semantics/tasks.md` pointers |
| FR-11 | `pkg/interactor/console/console.go`; `examples/dehydration/cases.go`; example narration |

### V-results

All green. `TestEligibilityAuthorize` + `TestEligibilityDenial…`/`Open`/`DeniedEligibility…`/`…ValidatesParameters` (V1, V1a, V1b) · `TestUserTaskResolveEligibility` (V2, V2d) · the pre-existing `user_task_authz_test.go` table unchanged (V2a) · `TestEligibilityIsResolvedOnceAtDistribution` (V2c) · `TestDenyByResolutionFailure` (V2e, V2f) · `TestClaimUnclaimReassign` (V3–V9) · `TestBornOwnership` (V13, V14) · `TestCompletedByRoutesTheNextTask` (V10, V15, V16) · `TestCompletionRefusalsAreDistinguishable` (FR-8) · `TestDehydrationUserTaskCompleteWithoutTake` (V17/NFR-2 — asserts a claim does **not** hydrate) · `TestClaimIsExclusiveUnderRace` (V18/NFR-3) · `TestOwnedTaskTeardownDropsTheRecord` + `TestUserTaskCancelWhileParked` (V19, V20) · `TestRuntimeVarCompletedBy` · `TestOwnershipValidatesParameters` (NFR-6).

`make ci` green: lint 0 issues, `-race` tests, **diff-coverage 98.0% of 295 changed lines (min 95%)**, govulncheck clean, and the ~35-example sweep executed end-to-end.

### Deviations from §2/§3, and why

1. **M3 folded into M2, and FR-4 arrived with it.** `registerTask` is the only place a task enters the registry, so birth-ownership belongs to that function; and the pre-hydration gate *is* strict completion — there is no gate that checks ownership without enforcing it. Scheduling them apart was a fiction.
2. **`Claim` is idempotent for the current holder** — §2's first form guarded on "task unowned". Found by running `examples/expression-routing`, whose task is born owned: an embedder claiming before completing was refused, and the instance hung. Carried into ADR-020 §2.5.2.
3. **The performer record moved from a data-plane commit to `RUNTIME`** (§3.5, §4.3). The record is engine-published: a process must read it and must not overwrite or collide with it, which a root-scope commit granted by construction.
4. **One new lock**, contrary to NFR-1's first wording — the register is written on the loop goroutine and read during expression evaluation. Recorded in NFR-1 rather than hidden.
5. **V2c proven by evaluation count** rather than by mutating process data mid-park, for which no API exists. The stronger claim: the triad is evaluated exactly once.
6. **One coverage carve-out**: `resolveEligibility`'s fail-closed branch is unreachable from a constructible state; the mapping it delegates to (`denyByResolutionFailure`) is at 100% and directly tested (§9).

---

## Open questions

None.

---

## Document record

| Date | Note |
|---|---|
| 2026-07-30 | Landed ADR-020 v.2's ownership lifecycle across eight milestones (§7, §10). A one-shot document: not versioned, and once Accepted it is a snapshot of this landing — later work gets its own SRD rather than an edit here (see the versioning rule in `CLAUDE.md`). |
