# SRD-019 — Instance control & engine lifecycle: Cancel, Shutdown, waiter drain

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-18 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-013 v.1 Instance Observability & Control](../design/ADR-013-instance-observability.md) |

This SRD lands the **control + engine-lifecycle** slice of [ADR-013 v.1](../design/ADR-013-instance-observability.md)
(§2.3 coarse control, §2.5 engine lifecycle) and **realizes the still-open part of
[ADR-006 v.1 §2.5](../design/ADR-006-events-and-subscriptions.md)** — the
`WaitGroup`-synchronized EventHub waiter shutdown that `Thresher.Shutdown`
consumes. It is the sibling of the already-merged observe slice
([SRD-018 v.1](SRD-018-instance-observe-handle.md), the pull handle + observer
event stream). Coarse, explicit, engine-mediated control: `InstanceHandle.Cancel`
(+ reserved `Suspend`/`Resume`), `Thresher.Shutdown`, `Thresher.Forget`, and the
already-present `UnregisterProcess` documented + lightly hardened.

## 1. Background & motivation

### 1.1 Current state (verified against the code)

- **No way to cancel a running instance.** `InstanceHandle` (`pkg/thresher/handle.go:16`)
  is read-only (SRD-018: `ID/State/Tokens/History/Data/WaitCompletion/Observe`); it
  has no `Cancel`. The instance has **no own cancel and no `Cancel()`/`Terminate()`
  method** — termination is driven only by **ctx-cancel**: `Instance.Run(ctx)` stores
  `inst.ctx = ctx` (`internal/instance/instance.go:558-585`); the loop observes
  `ctx.Done()` → `stopAll()` → `setState(Terminating)` → tracks stop →
  `setState(Terminated)` + `close(loopDone)` (`instance.go:603-688`). The only holder
  of an instance's cancel is the Thresher: `launchInstance` does
  `ctx, cancel := context.WithCancel(t.ctx)` and stores `instanceReg{stop: cancel, …}`
  (`thresher.go:679,696`; `instanceReg` at `thresher.go:97-102`) — never invoked today.
- **No `Thresher.Shutdown`.** The `State` enum is `Invalid / NotStarted / Started /
  Paused` (`thresher.go:58-95`) — **no `Stopped`**. `Run(ctx)` sets `Started`, calls
  `eventHub.Start(ctx)` synchronously then spawns `go eventHub.Run(ctx)`
  (`thresher.go:245-299`); `StartProcess` guards `st != Started`
  (`thresher.go:623-647`). There is **no graceful stop**: no Thresher `Shutdown`/`Stop`.
- **EventHub has no shutdown and no `WaitGroup`** (the open ADR-006 §2.5 item).
  `EventHub` (`internal/eventproc/eventhub/eventhub.go:31-38`) holds `waiters
  map[string]EventWaiter` + `m sync.RWMutex` + `started`; **no `sync.WaitGroup`, no
  `Shutdown`/`Close`**. `Run(ctx)` just blocks on `<-ctx.Done()` (`eventhub.go:86-96`).
  Each waiter's `Service(ctx)` spawns a background goroutine (timer:
  `waiters/timer.go:245-276`) and `Stop()` signals it (`timer.go:369-385`). Nothing
  **waits** for those goroutines to exit on stop → a stop leaves live goroutines.
- **Sole-hub waiter ownership is already in place** (FIX-003): a waiter reports a
  terminal fire via `hub.WaiterFired(eDefID)` and **does not remove itself**; the hub
  owns removal (`eventhub.go:343` `WaiterFired`, `:318` `RemoveWaiter`, `:214`
  `UnregisterEvent`). The double-close / TOCTOU / double-removal defects (audit
  1.3/1.5/2.5-ownership) are **fixed**; what remains of ADR-006 §2.5 is the
  **synchronized drain**.
- **`UnregisterProcess` exists but doesn't consider live instances**
  (`thresher.go:450-491`): it removes the `snapshots`/`starters` entries and
  unregisters starters from the hub, with **no check** for running instances of that
  process. The `snapshots` map is freed only here (audit 2.2). The `instances` map
  (`thresher.go:651` `Instance(id)` lookup) is **never pruned** — completed instances
  accumulate.

### 1.2 Problem

A host can observe a running instance (SRD-018) but cannot **act**: no cancel, no
graceful engine shutdown, no way to release a finished instance, and the EventHub
leaks waiter goroutines on stop (ADR-006 §2.5 open). This SRD adds the coarse,
explicit, engine-mediated control + lifecycle ADR-013 §2.3/§2.5 decided, and the
waiter drain ADR-006 §2.5 needs.

## 2. Decision

- **`InstanceHandle.Cancel(ctx)`** requests termination and blocks (ctx-bounded) until
  the instance is terminal. The **instance gains its own cancel**: `Run` derives
  `inst.ctx, inst.cancel = context.WithCancel(ctx)` and exposes `Cancel()`; the handle
  calls it, then waits on the existing `Done()`/`loopDone`. Termination still runs the
  proven ADR-001 cascade (Active→Terminating→Terminated). `Suspend`/`Resume` are
  **declared but reserved** (return a sentinel error) — they need the deferred `Paused`
  subsystem (ADR-013 §2.3).
- **`Thresher.Shutdown(ctx)`** is graceful, engine-mediated: flip to a new terminal
  **`Stopped`** state (so `StartProcess`/`RegisterProcess`/`Run` reject), cancel every
  running instance and wait (ctx-bounded) for them to settle, then drain the EventHub.
- **`EventHub.Shutdown(ctx)`** (realizes ADR-006 §2.5): stop every waiter and **wait
  for their goroutines to exit** via a hub `sync.WaitGroup`, bounded by ctx; remove
  every waiter from the registry **even if its `Stop()` errors** (no leak); unblock the
  hub `Run`.
- **Instance discovery + release.** A single **`Instances(filter)`**
  (`InstancesAll` / `InstancesRunning` / `InstancesCompleted`) lists tracked instance
  ids by liveness (the host reads each one's state via `Instance(id)`);
  **`Thresher.Forget(ids ...string)`** releases
  **terminal** instances in bulk — all-or-nothing (a live or unknown id is an error;
  none are removed on error) — the explicit release for the kept-for-observability
  instances. Because **event-start registrations have no instance to look up yet**,
  they get their own listing — **`Starters() []StarterInfo`** — a read-only projection
  of the registered starters (process + trigger event + start mode). **`UnregisterProcess`**
  keeps its current behaviour (remove definition +
  starters; **live instances keep running** against their built snapshot) — now
  documented, with a note that finished instances are released via `Forget`/`Shutdown`.

Control is coarse and visible (operator action through the engine's state machine),
never a hidden per-node listener (ADR-013 §4, ADR-011).

## 3. Functional requirements

- **FR-1 — instance cancel.** `InstanceHandle.Cancel(ctx context.Context)
  (InstanceState, error)` requests termination and blocks until the instance reaches a
  terminal state or `ctx` is done; returns the terminal state (+ `ctx.Err()` on
  timeout). Idempotent (a second `Cancel`, or `Cancel` of an already-terminal instance,
  is a no-op returning the terminal state).
- **FR-2 — instance self-cancel hook.** `internal/instance`: `Run` derives an internal
  cancellable context (`inst.cancel`); `Instance.Cancel()` cancels it (idempotent),
  driving the loop's existing `ctx.Done()` cascade to `Terminating`→`Terminated`. The
  Thresher's parent ctx (`instanceReg.stop`, engine ctx) still cascades.
- **FR-3 — reserved suspend/resume.** `InstanceHandle.Suspend(ctx)` / `Resume(ctx)`
  exist and return a stable **`ErrNotImplemented`** sentinel (reserved for the `Paused`
  subsystem; the contract is fixed now, ADR-013 §2.3).
- **FR-4 — engine graceful shutdown.** `Thresher.Shutdown(ctx context.Context) error`:
  (a) transition to `Stopped` (so further `StartProcess`/`RegisterProcess`/`Run` are
  rejected); (b) cancel every running instance and wait (ctx-bounded) for them to
  settle; (c) call `EventHub.Shutdown(ctx)`; return the first error / `ctx.Err()` on
  deadline. Idempotent.
- **FR-5 — `Stopped` state.** Add `Stopped` to the Thresher `State` enum;
  `StartProcess` (`thresher.go:623`), `RegisterProcess`, and `Run` reject when `Stopped`.
- **FR-6 — EventHub synchronized shutdown.** `EventHub.Shutdown(ctx context.Context)
  error` stops every registered waiter, **waits for their `Service` goroutines to exit**
  via a hub `sync.WaitGroup` bounded by `ctx`, and removes every waiter from the registry
  **even when a `Stop()` returns an error** (logged, never leaked). It unblocks the hub
  `Run` and rejects further registration. The waiter `Service` goroutine signals exit to
  the hub (it already holds a `hub` reference and calls `WaiterFired`).
- **FR-7 — forget finished instances (batch).** `Thresher.Forget(ids ...string) error`
  removes the listed **terminal** instances from the `instances` map, **all-or-nothing**:
  it first validates every id (known **and** terminal) and removes none if any is
  unknown or still-live, returning an error naming the offending id. `Forget("x")`
  (single) and `Forget(Instances(InstancesCompleted)...)` (sweep) both work.
- **FR-7a — instance discovery.** A single `Thresher.Instances(filter InstanceFilter)
  []string` lists tracked instance ids by an `InstanceFilter` enum:
  `InstancesAll` (every tracked), `InstancesRunning` (non-terminal — Created/Active/
  Terminating), `InstancesCompleted` (terminal — Completed/Terminated; the list that
  feeds batch `Forget`). Read-only, snapshot-consistent under the engine mutex.
- **FR-7b — starter discovery.** `Thresher.Starters() []StarterInfo` lists the
  registered **event-start** registrations (the `starters` map) — each a process awaiting
  an event, with **no instance yet**, so they cannot appear under `Instances`.
  `StarterInfo` is a read-only projection of the internal `instanceStarter` (the process
  it would instantiate, the trigger event-definition it waits on, and the auto/manual
  start mode); exact fields are pinned against `instanceStarter` at implementation.
- **FR-8 — unregister with live instances.** `UnregisterProcess` (`thresher.go:450`)
  removes the definition + starters and **leaves any live instances running** (current
  behaviour, now documented); the per-process `snapshots` entry is freed.

## 4. Non-functional requirements

- **NFR-1 — coarse, engine-mediated control.** Cancel/Shutdown act through the
  instance/engine state machines (ctx-cancel cascade, `setState`), never a back door;
  no mutating per-node listener (ADR-013 §4).
- **NFR-2 — no goroutine leak on shutdown.** After `EventHub.Shutdown` returns
  (within `ctx`), no waiter `Service` goroutine outlives the hub; a failed `Stop()`
  still removes the waiter (NFR realizes ADR-006 §2.5).
- **NFR-3 — bounded + idempotent.** `Cancel`/`Shutdown` honour `ctx` deadlines and are
  idempotent (safe to call twice / after terminal); report stragglers rather than hang.
- **NFR-4 — concurrency-safe.** Map mutations (`instances`/`snapshots`/`waiters`) stay
  under their existing mutexes; the cancel cascade stays the single-owner loop model
  (ADR-001); no new lock on the observation/execution hot path.
- **NFR-5 — coverage.** Touched files finish ≥80% (target 100%) diff-coverage; `make
  ci` green (`-race` especially — shutdown is concurrency-heavy).

## 5. Path analysis (alternatives)

- **Cancel via instance self-cancel (chosen) vs the handle holding `instanceReg.stop`.**
  Chosen: the instance owns its cancel (`inst.cancel` derived in `Run`), `Cancel()`
  triggers it. The handle (which holds `*instance.Instance`) calls `inst.Cancel()` — no
  thresher round-trip, termination is handle-local, and the engine's parent-ctx cascade
  is untouched. Rejected handle-holds-thresher-cancel: couples the handle to thresher
  internals and needs a lookup.
- **`Cancel(ctx)` request-and-wait (chosen) vs fire-and-forget.** Chosen: request +
  ctx-bounded wait for terminal, returning the state — symmetric with `WaitCompletion`,
  gives the caller confirmation. Fire-and-forget would force a separate `WaitCompletion`.
- **EventHub drain via hub `WaitGroup` + waiter goroutine-exit signal (chosen) vs
  synchronous `Stop()` (block until the goroutine exits).** Chosen: the hub `Add`s when
  it starts a waiter and the waiter's goroutine `Done`s on exit (mirrors the existing
  waiter→hub `WaiterFired` callback); `Shutdown` `Stop`s all then `Wait`s bounded by ctx.
  Rejected synchronous-`Stop`: changes every waiter's `Stop()` semantics and serialises
  the drain.
- **Instance retention: keep + `Forget` (chosen) vs auto-evict on completion vs keep
  forever.** Chosen (product decision): completed instances stay looked-up-able
  (`Instance(id)`/`Observe`/`State` work post-completion — ADR-013's live in-memory
  model), with an explicit `Forget(id)` + `Shutdown` to release. Rejected auto-evict:
  loses post-completion observation; rejected keep-forever: unbounded with no release.
- **`Stopped` as a new terminal state (chosen) vs reusing `NotStarted`.** Chosen: a
  distinct terminal `Stopped` so a shut-down engine is not mistaken for a re-runnable
  fresh one.
- **Discovery: one `Instances(filter)` (chosen) vs three methods
  (`Instances`/`RunningInstances`/`CompletedInstances`).** Chosen: a single function
  with a named `InstanceFilter` enum (`InstancesAll`/`InstancesRunning`/
  `InstancesCompleted`) — a 3-way filter, not a boolean, so one function reads cleaner
  than three and extends if liveness categories grow. Starters are listed **separately**
  via `Starters() []StarterInfo` rather than as a fourth `InstanceFilter` value, because
  a starter is **not an instance** (no instance id, no lifecycle state) — folding it into
  `Instances()` would mean returning ids that don't resolve via `Instance(id)`.
- **`Forget(ids ...string)` batch, all-or-nothing (chosen) vs single-id / partial.**
  Chosen: variadic batch (single-id still works) with validate-all-then-remove, so a
  bad id in a sweep leaves the map untouched and the error is actionable.
- **`UnregisterProcess` allows live instances (chosen) vs reject / cancel-them.** Chosen
  (product decision): remove definition + starters, leave live instances running against
  their built snapshot; simplest, no coupling of unregister to termination. The host uses
  `Shutdown`/`Cancel`/`Forget` for instance lifecycle.

## 6. API (public surface, `pkg/thresher`)

```go
// On the existing read-only handle (SRD-018), control is added:
func (h *InstanceHandle) Cancel(ctx context.Context) (InstanceState, error)
func (h *InstanceHandle) Suspend(ctx context.Context) error // reserved -> ErrNotImplemented
func (h *InstanceHandle) Resume(ctx context.Context) error  // reserved -> ErrNotImplemented

// Engine lifecycle:
func (t *Thresher) Shutdown(ctx context.Context) error

// Instance discovery + release:
type InstanceFilter uint8
const (
	InstancesAll       InstanceFilter = iota // every tracked instance
	InstancesRunning                          // non-terminal (Created/Active/Terminating)
	InstancesCompleted                        // terminal (Completed/Terminated)
)
func (t *Thresher) Instances(filter InstanceFilter) []string
func (t *Thresher) Forget(ids ...string) error // batch, terminal-only, all-or-nothing

// Event-start registrations (no instance yet) — fields pinned at implementation:
type StarterInfo struct {
	ProcessID string // the process a matching event instantiates
	Trigger   string // the event-definition it waits on (e.g. message name/id)
	Manual    bool   // manual-start mode (auto-instantiation opted out)
}
func (t *Thresher) Starters() []StarterInfo

// New thresher state (terminal):
const Stopped State = /* iota after Paused */

// Reserved-feature sentinel:
var ErrNotImplemented = errs.New(/* "feature reserved, not yet implemented" */)
```

Internal support: `internal/instance` — `Run` derives `inst.cancel`; `Instance.Cancel()`
(idempotent). `internal/eventproc/eventhub` — `EventHub.Shutdown(ctx) error` + a hub
`sync.WaitGroup`; waiters signal `Service`-goroutine exit to the hub (e.g. a
`hub.waiterDone()` callback paired with `wg.Add(1)` at `registerWaiter`'s
`w.Service(eh.ctx)`).

## 7. Test plan

- **`TestInstanceHandleCancel`** — `Cancel(ctx)` on a parked/long-running instance drives
  it to `Terminated`; a second `Cancel` and a `Cancel` of a completed instance are no-ops
  returning the terminal state (FR-1, FR-2).
- **`TestCancelCtxBounded`** — `Cancel` with a short ctx against an instance that won't
  settle returns `ctx.Err()` and a non-terminal state (FR-1).
- **`TestSuspendResumeReserved`** — both return `ErrNotImplemented` (FR-3).
- **`TestThresherShutdown`** — `Shutdown(ctx)` cancels running instances, flips to
  `Stopped`, and closes the hub; `StartProcess`/`RegisterProcess`/`Run` then reject;
  idempotent on a second call (FR-4, FR-5).
- **`TestShutdownDrainsWaiters`** (`-race`) — with a registered timer waiter,
  `EventHub.Shutdown` returns only after the waiter goroutine exits (no leak); a waiter
  whose `Stop()` errors is still removed (FR-6, NFR-2).
- **`TestForget`** — `Forget(ids...)` removes completed instances in bulk (subsequent
  `Instance(id)` → false); is **all-or-nothing** — a batch containing a live or unknown
  id removes none and errors naming it (FR-7).
- **`TestInstancesFilter`** — `Instances(InstancesAll/InstancesRunning/InstancesCompleted)`
  returns the right id sets across a run (a parked instance under Running, a finished one
  under Completed, both under All); `Forget(Instances(InstancesCompleted)...)` sweeps the
  finished ones (FR-7a).
- **`TestStarters`** — a process registered with a message-start event appears in
  `Starters()` with its process id + trigger; manual-start mode is reflected; a process
  with no event-start has no starter entry (FR-7b).
- **`TestUnregisterProcessWithLiveInstance`** — `UnregisterProcess` succeeds with a live
  instance still running; the definition is gone but the instance completes (FR-8).
- Internal `internal/eventproc/eventhub` + `internal/instance` units for
  `EventHub.Shutdown` / `Instance.Cancel` (cross-package coverage attribution).

## 8. Cross-document consistency

- **Implements** [ADR-013 v.1](../design/ADR-013-instance-observability.md) §2.3
  (coarse control), §2.5 (engine lifecycle: `Shutdown`/`UnregisterProcess`).
- **Realizes** [ADR-006 v.1 §2.5](../design/ADR-006-events-and-subscriptions.md) — the
  `WaitGroup`-synchronized waiter shutdown + no-leak-on-`Stop` (the sole-ownership half
  already landed via FIX-003).
- [ADR-001 v.5](../design/ADR-001-execution-model.md) — the generic ctx-cancellation
  cascade `Cancel` triggers (Active→Terminating→Terminated).
- [ADR-002 v.2](../design/ADR-002-extension-architecture.md) — the §4.7 public-API
  surface these methods join.
- [SRD-018 v.1](SRD-018-instance-observe-handle.md) — the observe slice this builds the
  control surface onto (sibling).
- References up/sideways, version-pinned; no downward refs (ADR-013/ADR-006 do not cite
  SRD-019).

## 9. Definition of Done

- FR-1…FR-8 wired and exercised by the §7 tests (incl. a `-race` waiter-drain test).
- `InstanceHandle.Cancel`/`Suspend`/`Resume`, `Thresher.Shutdown`/`Forget`, the
  `Stopped` state + guards, and `EventHub.Shutdown` + the hub `WaitGroup` all present.
- No waiter goroutine outlives `EventHub.Shutdown` (NFR-2) — proven under `-race`.
- `make ci` green (tidy, lint incl. fieldalignment, build, `-race`, diff-coverage ≥95,
  govulncheck); touched files ≥80% (target 100%).
- All 9 examples smoke-run exit 0 (control/shutdown didn't regress the happy path).
- §10 filled; status → Accepted; RU twin added; linked docs synced.

## 10. Implementation summary

> ⚠️ TODO: fill AFTER landing — commits, key files, V-results, deltas vs this draft.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-18 | Ruslan Gabitov | Draft. Lands the control + engine-lifecycle slice of ADR-013 v.1 (§2.3/§2.5) and realizes the open part of ADR-006 v.1 §2.5: `InstanceHandle.Cancel(ctx)` (instance self-cancel via a `Run`-derived `inst.cancel`, request + ctx-bounded wait, idempotent) + reserved `Suspend`/`Resume`; `Thresher.Shutdown(ctx)` (new terminal `Stopped` state, cancel + settle running instances, drain the hub); `EventHub.Shutdown(ctx)` (hub `sync.WaitGroup` over waiter `Service` goroutines, ctx-bounded wait, remove-even-on-`Stop`-error); `Thresher.Forget(ids ...string)` (batch, all-or-nothing release of terminal instances) + `Instances(filter)` discovery (one function, `InstancesAll`/`InstancesRunning`/`InstancesCompleted`) + `Starters() []StarterInfo` (event-start registrations, which have no instance yet) + `UnregisterProcess` documented (live instances keep running). Code-grounded against `pkg/thresher`, `internal/instance`, `internal/eventproc/eventhub`. Sibling of SRD-018 v.1 (observe). Implements ADR-013 v.1; realizes ADR-006 v.1 §2.5; refs ADR-001 v.5, ADR-002 v.2. |
