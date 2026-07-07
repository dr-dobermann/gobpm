# SRD-035 — Service Task in-process timeout & cancellation (`WithTimeout`)

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-06 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-021 v.1 Service Task Execution Model](../design/ADR-021-service-task-execution-model.md) §2.9 |

> **Draft** — first of four SRDs landing [ADR-021 v.1](../design/ADR-021-service-task-execution-model.md) (M1 of
> M1–M8). Lands **M1**: an opt-in **`WithTimeout(d)`** that makes the **in-process** ServiceTask locus
> **time-bounded and cancellable** without changing the locus — the track goroutine runs the `Operation` in a
> sub-goroutine and blocks on a `select { done, ctx.Done(), time.After(d) }`. Independent of the external-worker
> machinery (SRD-036–038). Sibling SRDs: **SRD-036** (job queue + wait-node), **SRD-037** (classification +
> output mapping), **SRD-038** (retry + trust + example).

---

## 1. Background (verified against the code)

### 1.1 The gap (ADR-021 §1 problem 1)

The in-process ServiceTask locus invokes its `Operation` **synchronously on the track goroutine** and blocks
until it returns, with **no time bound and no interruption**:

```go
// pkg/model/activities/service_task.go:146,152 (ServiceTask.Exec)
op := st.operation.Clone()
// ...
out, err := op.Execute(ctx, re)   // :152 — synchronous, unbounded, on the track goroutine
```

A hanging or non-cooperative `Operation` (one that ignores `ctx`) wedges the track, and a boundary timer /
instance-abort ([ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md)) can only reach it
if the operation itself checks `ctx`.

### 1.2 The decision this SRD lands ([ADR-021 v.1](../design/ADR-021-service-task-execution-model.md) §2.9)

`WithTimeout(d)` runs `op.Execute` in a **sub-goroutine**; the track goroutine blocks on a `select` over
`{done, ctx.Done(), time.After(d)}`. This gives the operation **cancellation + a time bound** it lacks today, and
makes it **boundary-interruptible** even when it ignores `ctx`. **Opt-in; default unbounded** (Camunda in-process
delegates run to completion — the Camunda-aligned default).

### 1.3 The rails M1 rides (existing mechanism, verified)

- **`Exec` runs on the track goroutine** — `executeNodeCore → ne.Exec(...)` (`internal/instance/track.go:822`).
  A non-wait ServiceTask executes synchronously there, so wrapping `op.Execute` in a sub-goroutine + `select` on
  the *track* goroutine is a drop-in bound, not a park/resume change.
- **`ctx` is the track's cancellable context** (boundary interrupt / instance abort, ADR-018), so
  `case <-ctx.Done()` is what makes a timed-out or interrupted in-process task responsive.
- **The option pattern** — a ServiceTask-specific option follows the `SndTaskOption` shape
  (`pkg/model/activities/send_task_options.go:21,25` — `type SndTaskOption func(*sndTaskConfig)` +
  `func (SndTaskOption) Option() {}`), split from base options by a type-switch in the constructor
  (`send_task.go:48-61`). ServiceTask currently has **no** such per-task option struct — it is **NEW**.
- **Fault path** — returning a non-nil error from `Exec` transitions the activity `Failing → Failed`
  (the existing task fault path; ADR-021 §3, `tasks.md` §"Faults during execution").

## 2. Requirements

### Functional

- **FR-1** — `ServiceTask` accepts a new option **`activities.WithTimeout(d time.Duration)`**. When `d > 0`, the
  in-process `Exec` runs `op.Execute(ctx, re)` in a **sub-goroutine** and blocks on
  `select { <-done, <-ctx.Done(), <-time.After(d) }`.
- **FR-2** — Outcomes of the `select`:
  - `done` (op returned) → bind `DataOutput` / return the op's error **exactly as today** (unchanged path).
  - `ctx.Done()` (boundary interrupt / instance abort) → `Exec` returns `ctx.Err()`; the track reacts.
  - `time.After(d)` (timeout) → `Exec` **faults** the task (a self-identifying timeout error → `Failing → Failed`).
- **FR-3** — `WithTimeout` is **opt-in**: unset (or `d <= 0`) → behaviour is **exactly as today** (synchronous,
  unbounded, no sub-goroutine). Default unbounded.

### Non-functional

- **NFR-1 (resource honesty — two leaks, one fixable, one inherent)** — the `select` bounds *the track's* wait,
  **not the operation's execution**:
  - **Timer (fixable, MUST fix):** use **`time.NewTimer(d)` + `defer timer.Stop()`**, **never `time.After(d)`**.
    `time.After` keeps its timer alive until it fires, so a task that completes before its timeout would leak the
    timer for the full `d` (compounding across executions). `Stop()` on every exit path removes it.
  - **Sub-goroutine (inherent, Go cannot kill a goroutine):** an op that **eventually returns** does **not** leak
    — the **buffered (cap 1)** `done` lets its send succeed and the goroutine exit; the late result is dropped.
    An op that **never returns** (ignores `ctx`, infinite loop, blocked I/O) leaks its **goroutine and everything
    it captures** (the cloned op, `re`, `ctx`) **permanently** — Go offers no force-terminate. `WithTimeout` is
    best-effort *engine* protection, not op termination.
  - **Visibility:** on timeout the engine **logs a warning** (`re.Logger().Warn(...)`) that the op's goroutine
    may still be running, so a leak surfaces in ops rather than staying silent.
  - **Op contract:** operations **must confine their effects to their returned value** (the track binds it via
    `re.Put`) — a leaked goroutine mutating scope after a timeout would race the track. Doc-commented on
    `WithTimeout`; a truly-cancellable operation honours `ctx`.
- **NFR-2 (backward compatible)** — with `WithTimeout` unset, no behaviour change; existing ServiceTask tests
  stay green.
- **NFR-3 (gate)** — diff-coverage ≥95% on touched files; `make ci` green; the sub-goroutine + `select` is
  race-clean under `-race`.

## 3. Models

### 3.1 The option — `pkg/model/activities/service_task_options.go` (NEW)

Mirrors `send_task_options.go`:

```go
// SrvTaskOption configures a ServiceTask beyond the shared task/activity options.
type SrvTaskOption func(*srvTaskConfig)

// Option marks SrvTaskOption as an options.Option (marker interface, FIX-020).
func (SrvTaskOption) Option() {}

// srvTaskConfig accumulates ServiceTask-specific configuration.
// (Extended by SRD-036–038 with worker topic, error mapper, retry policy, etc.)
type srvTaskConfig struct {
	timeout time.Duration
}

// WithTimeout bounds the in-process Operation execution to d and makes it
// ctx-cancellable. A non-positive d (the default) means no bound. NOTE: the
// bound protects the engine — a non-cooperative Operation that ignores ctx
// keeps running in a leaked goroutine; confine an Operation's effects to its
// returned value.
func WithTimeout(d time.Duration) SrvTaskOption {
	return func(c *srvTaskConfig) { c.timeout = d }
}
```

### 3.2 `ServiceTask` struct + constructor dispatch (`service_task.go` — EXTEND)

`ServiceTask` gains a `timeout time.Duration` field. `NewServiceTask` type-switches `SrvTaskOption` off the
option list (mirroring `send_task.go:48-61`), applies it to a `srvTaskConfig`, and copies `timeout` onto the
task; remaining options pass to `newTask` as today.

### 3.3 The `Exec` select-wrapper (`service_task.go:135-184` — EXTEND)

```go
op := st.operation.Clone()

if st.timeout <= 0 {
	out, err := op.Execute(ctx, re)   // unchanged synchronous path
	// ... existing bind / fault / return Outgoing() ...
}

type opRes struct {
	out *data.ItemDefinition
	err error
}
done := make(chan opRes, 1)                    // buffered (cap 1): an op that eventually
go func() { o, e := op.Execute(ctx, re); done <- opRes{o, e} }()  // returns always sends & exits

timer := time.NewTimer(st.timeout)             // NewTimer + Stop, NOT time.After —
defer timer.Stop()                             // else the timer leaks until it fires

select {
case r := <-done:
	// bind DataOutput / return r.err exactly as the synchronous path does
case <-ctx.Done():
	return nil, ctx.Err()
case <-timer.C:
	// NB: if the op ignores ctx it is still running (NFR-1); log & move on.
	re.Logger().Warn("service task timed out; its operation goroutine may still be running",
		"task", st.Name(), "timeout", st.timeout)
	return nil, errs.New(
		errs.M("service task %q timed out after %s", st.Name(), st.timeout),
		errs.C(errorClass, errs.OperationFailed),
		errs.D("timeout", st.timeout.String()))
}
```

The `done` branch reuses the **existing** output-binding code (`data.MustParameter(out.ID(),
data.MustItemAwareElement(out, data.ReadyDataState))` → `re.Put(res)`, `service_task.go:164-181`) — factored so
both the synchronous and wrapped paths share it.

## 4. Analysis

### 4.1 Select-wrapper on the track goroutine — not park/resume

The in-process locus stays **synchronous on the track goroutine**; the `select` just makes the (already-blocking)
wait interruptible. Making it a loop wait-node (park/resume) is the **external-worker** path (SRD-036) and is
unwarranted for a fast in-process op. (ADR-021 §2.9; §4 alt-row 10.) *Rejected: park in-process ops* — needless
machinery + latency for the common fast case.

### 4.2 Timeout faults now; technical-fault → retry arrives later

M1 has no classification/retry machinery yet (SRD-037/038). A timeout therefore **faults the task**
(`Failing → Failed`) with a self-identifying timeout error. When retry lands (SRD-038), a timeout becomes a
**technical fault** routed through the retry policy (ADR-021 §2.6/§2.7); M1 delivers the *mechanism* and the
fault terminal. This is a forward-compatible increment: the SRD-038 change is to *route* the timeout error, not
to re-plumb the wrapper.

### 4.3 Locus scope

`WithTimeout` bounds the **in-process** locus only. The external-worker locus has its own time bounds (job/lock
timeout, `maxLockDuration`, ADR-021 §2.4) landing in SRD-036/038. `WithWorker` does not exist yet (SRD-036), so
no `WithTimeout`+`WithWorker` interaction is defined here; SRD-036 defines it when `WithWorker` lands.

### 4.4 Goroutine-leak honesty — rejected alternative

*Rejected: forcibly cancel the operation.* Go cannot kill a goroutine; there is no safe forcible cancellation.
The wrapper protects the **engine** (the track proceeds); true cancellation still requires the operation to
honour `ctx`. The buffered `done` channel keeps the leak **bounded and race-free for an op that eventually
returns**; a **never-returning** op leaks its goroutine permanently (NFR-1) — the inherent, honestly-stated
limit. The timer is `NewTimer`+`Stop` (NFR-1), never `time.After`.

## 5. API / contract surface

- **New:** `func activities.WithTimeout(d time.Duration) activities.SrvTaskOption`.
- No change to `ServiceTask`'s existing constructor signature or to any other public surface.

## 6. Test scenarios

New: `pkg/model/activities/service_task_timeout_test.go`.

| Test | FR/NFR | Setup | Assertion |
|---|---|---|---|
| `TestServiceTaskWithTimeoutCompletes` | FR-1, FR-2 | op returns before `d` | normal output bound, `Exec` returns `Outgoing()`, no error |
| `TestServiceTaskWithTimeoutTimesOut` | FR-2, NFR-1 | op sleeps `> d`, ignores `ctx` | `Exec` returns a timeout error (self-identifying); task → `Failing → Failed`; a warning is logged that the op goroutine may still be running |
| `TestServiceTaskWithTimeoutCtxCancel` | FR-2 | `ctx` cancelled mid-op | `Exec` returns `ctx.Err()` |
| `TestServiceTaskWithoutTimeoutUnchanged` | FR-3 | no `WithTimeout` | synchronous path taken; behaviour identical to a pre-M1 ServiceTask (no sub-goroutine) |
| `TestServiceTaskWithTimeoutLeakedGoroutineDropped` | NFR-1 | non-cooperative op returns *after* the timeout fired | late result dropped via the buffered channel; `-race` clean; no panic |

## 7. Milestones

1. **M1** (this SRD) — `WithTimeout` option + `Exec` select-wrapper + tests. **Single commit** on
   `feat/service-task-execution`.

## 8. Cross-doc

- **Implements:** [ADR-021 v.1](../design/ADR-021-service-task-execution-model.md) §2.9.
- **References (up / sideways):** [ADR-001 v.6](../design/ADR-001-execution-model.md) (execution model, track
  goroutine), [ADR-018 v.1](../design/ADR-018-boundary-events-and-activity-interruption.md) (`ctx` cancellation /
  boundary interruption — the `ctx.Done()` path), [SAD-001 v.1](../design/SAD-001-vision-and-architecture.md) §11.
- **Sibling SRDs (this feature, forthcoming):** SRD-036 (job queue + wait-node), SRD-037 (classification + output
  mapping), SRD-038 (retry + trust + example). SRD→SRD is sideways; pins by number only (SRD/FIX are unversioned).
- Direction: SRD → ADR / SAD (up), SRD → SRD (sideways); no downward reference.

## 9. Definition of Done

- FR-1…FR-3 implemented and wired; NFR-1…NFR-3 upheld.
- Every FR/NFR covered by ≥1 named §6 test, all green under `-race`.
- No behaviour change when `WithTimeout` is unset (existing ServiceTask tests stay green).
- `make ci` green (tidy · lint · build · `-race` · diff-coverage ≥95% on touched files · govulncheck).
- SRD-035 flips to Accepted. **ADR-021 stays Draft** until all related SRDs (036–038) are grounded — so
  code-grounding can still feed back into the design — then flips Accepted (owner decision).

## 10. Implementation summary (stage-by-stage actual landings + deltas vs draft)

### §10.1 Stages by commit (branch `feat/service-task-execution`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| M1 | `91d471f` | `WithTimeout` option (`service_task_options.go`: `SrvTaskOption` marker + `WithTimeout`) + `Exec` select-wrapper factored into `execOperation` / `wrapOpErr` (`service_task.go`) | `service_task_timeout_test.go` — 5 tests (completes · times-out + warning · ctx-cancel · zero-unbounded · leaked-goroutine-dropped), green under `-race` |

### §10.2 Empirical findings — deltas vs the §3 draft

- **Timer leak, fixed pre-code.** The initial `Exec` sketch used `time.After(st.timeout)`, which leaks the timer
  until it fires; switched to `time.NewTimer(d)` + `defer timer.Stop()` (NFR-1) after the leak was caught in
  review — before any code was written.
- **Dead `Validate()` dropped.** A `srvTaskConfig.Validate()` (mirroring `sndTaskConfig`) was never called by the
  type-switch dispatch, so it was removed as uncoverable dead code rather than test-covered.
- **Verification.** `make ci` green — diff-coverage **100% of 64 changed coverable lines** (min 95%), lint 0
  issues, `-race` clean, govulncheck clean.
