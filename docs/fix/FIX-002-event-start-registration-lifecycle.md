# FIX-002 — StartProcess deadlock: re-entrant engine mutex + event-start Created-guard

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-09 |
| Owner | Ruslan Gabitov |
| Related | [ADR-001 v.4 §4.2 Execution Model](../design/ADR-001-execution-model.md) (lifecycle); [ADR-006 v.1 Events & Subscriptions](../design/ADR-006-events-and-subscriptions.md); [FIX-001](FIX-001-thresher-eventhub-startup-race.md) (EventHub Start/Run split) |

## 1. Symptoms

Every `StartProcess` call **deadlocks** (`fatal error: all goroutines are asleep - deadlock!`). Two examples surface it — both call `engine.StartProcess`:

- **`examples/basic-process`** (plain Start → ServiceTask → End): deadlocks, no useful output.
- **`examples/timer-event`** (timer **on the start event**): first fails to *construct* —

  ```
  Failed to start process: couldn't create an Instance for process "…"
    Error: couldn't register event definitions
      node_name: timer-start
      Error: instance isn't active (current state: Created)
  ```

  and once that construction-time guard is relaxed, it then **deadlocks** like basic-process.

`examples/simple-timer` does **not** call `StartProcess` (only `RegisterProcess` + `Run`), so it hits neither path and exits cleanly — which is why it looked "fine".

Masked from CI because CI only **builds** the example modules, never **runs** them (`make build-all` has no run step) — see §5.

## 2. Root-cause analysis

There are **two** defects on the `StartProcess` path. A plain process hits RC2; an event-start process hits RC1 first, then RC2.

### RC1 — `RegisterEvent` rejects construction-time registration (`Created`)

Event-start nodes register their definitions during `instance.New`, while the instance is `Created`, but `Instance.RegisterEvent` requires `Active`:

- `instance.go:161` — `New` leaves state `Created`; `createTracks()` runs inside `New` (`instance.go:172`).
- `track.go:271` / `track.go:281-293` — `newTrack` → `checkNodeType` registers an event node's definitions: `t.instance.RegisterEvent(t, d)` ("couldn't register event definitions").
- `instance.go:570-576` — `is := inst.State(); if is != Active { return "instance isn't active (current state: Created)" }`.
- `instance.go:260` — the instance only becomes `Active` later, in `Run`.

The `Active`-only guard (added with the ADR-001 v.4 `Created→Active` reconciliation) is too strict: it conflates "don't register on a **finished** instance" (correct) with "don't register during **construction**" (wrong — that's exactly when event-start nodes register).

### RC2 — re-entrant `Thresher.m` deadlocks `StartProcess` (the deeper bug)

`StartProcess` holds the engine mutex **across** `launchInstance`, which re-acquires the same non-reentrant `sync.Mutex`:

- `thresher.go:368-378` — `StartProcess` does `t.m.Lock(); defer t.m.Unlock()`, then `return t.launchInstance(s)` — still holding `t.m`.
- `thresher.go:403` — `launchInstance` itself does `t.m.Lock()` (instance registry) → **re-entrant self-deadlock**. This is `basic-process` (no event-start needed).
- For an event-start process the deadlock surfaces earlier: with RC1 relaxed, `instance.New` → `RegisterEvent` → `Thresher.RegisterEvent` (`thresher.go:266`) → `Thresher.State()` (`thresher.go:184`, `t.m.Lock()`) → deadlock. This is `timer-event`.

Observed deadlock stack (RC1 relaxed): `main → StartProcess(:378, holds t.m) → launchInstance → instance.New → createTracks → newTrack → checkNodeType → Instance.RegisterEvent(:597) → Thresher.RegisterEvent(:266) → Thresher.State(:184) → sync.Mutex.Lock [blocked forever]`. `sync.Mutex` is not reentrant; the second acquire on the same goroutine blocks. `simple-timer` avoids it only because it never calls `StartProcess`.

## 3. Solution

Both RCs must be fixed; either alone leaves a broken example.

### 3.1 — RC1: permit registration in non-terminal states

`Instance.RegisterEvent` rejects only a **terminal** instance; permit `Created`/`Active`. Enum order: `Created(0) < Active(1) < Completed(2) < Terminating(3) < Terminated(4)` (`instance.go:56-67`).

```go
// internal/instance/instance.go — RegisterEvent
is := inst.State()
if is != Created && is != Active {
    return errs.New(
        errs.M("instance is terminal, can't register events (state: %s)", is),
        errs.C(errorClass, errs.InvalidState),
        errs.D("requester_id", proc.ID()))
}
```

Registration is legitimate at construction (start events) and at runtime (boundary / intermediate catch events, `Active`); refuse only when the instance can no longer act on a fired event (terminal). The EventHub is already started by `Thresher.Run` (FIX-001), so the hub accepts it; only this guard blocked.

### 3.2 — RC2: `StartProcess` must not hold `t.m` across `launchInstance`

Narrow the engine lock to the snapshot lookup; release it before `launchInstance` (which takes `t.m` itself for the registry):

```go
// pkg/thresher/thresher.go — StartProcess
func (t *Thresher) StartProcess(processID string) error {
    if t.State() != Started { return /* not started */ }

    t.m.Lock()
    s, ok := t.snapshots[processID]
    t.m.Unlock()
    if !ok { return /* not found */ }

    return t.launchInstance(s) // launchInstance locks t.m on its own
}
```

This removes **both** re-entrancy paths: the direct `launchInstance:403` re-lock (basic-process) and the `State()` re-lock during event-start registration (timer-event). Also replace the unguarded `t.state` read at `thresher.go:361` with `t.State()`.

### 3.3 Alternatives considered

- **Make `Thresher.State()` lock-free (atomic state), like `Instance`.** Removes the `State()` re-entry (timer-event) but NOT the direct `launchInstance:403` re-lock (basic-process) — insufficient alone. A reasonable *complementary* hardening; optional, can follow.
- **Recursive mutex.** Rejected — not idiomatic in Go; re-entrant locking masks design problems.
- **Defer event-start registration to first track execution (after `Active`).** Rejected for RC1 — more invasive, and orthogonal to RC2 (which would still deadlock basic-process).

## 4. Verification

| # | Check | Expectation |
|---|---|---|
| V1 | Unit: a `Created` instance registers an event-start definition without error (RC1). | succeeds on `Created`. |
| V2 | Unit: `RegisterEvent` on a terminal (`Completed`/`Terminated`) instance still errors (RC1). | guard preserved. |
| V3 | Unit/integration: `StartProcess` of a plain process **and** of an event-start process returns without deadlock (RC2). | no re-entrant lock; `t.m` not held across `launchInstance`. |
| V4 | `examples/basic-process` **and** `examples/timer-event` run to completion. | exit 0; no deadlock, no "instance isn't active". |
| V5 | No regression: `make ci` green (race tests + diff-coverage + vuln); `examples/simple-timer` still runs. | all existing tests pass. |

## 5. Prevention

- **CI runs examples, not just builds them.** The whole class of failure hid because `make build-all` never executes the example modules. Add a CI step (and a `make` target) that runs each example with a timeout and asserts exit 0. Tracked as a follow-up; see [[project_examples_runtime_broken]].

## 6. Regressions / scope notes

- **`examples/basic-process` is in scope** — it is the same RC2 re-entrancy (earlier mis-attributed to a no-op `ServiceTask`). Both broken examples are fixed by §3.1 + §3.2.
- The guard change (§3.1) must not weaken terminal-state protection (V2).
- Narrowing the `StartProcess` lock (§3.2) must keep the snapshot read and the registry write each mutex-guarded — just not held across `launchInstance`. Concurrent `StartProcess` calls remain safe.

## 7. Related

- [ADR-001 v.4 §4.2](../design/ADR-001-execution-model.md) — the lifecycle (`Created → Active → Completed`, `Terminating → Terminated`) whose guard this fix corrects.
- [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) — event delivery / subscription semantics (the eventual owner of registration timing).
- [FIX-001](FIX-001-thresher-eventhub-startup-race.md) — established that the EventHub is started before instances register.

## 8. Implementation summary

> ⚠️ TODO: filled at landing — files/lines, V-results, commit SHA.

## 9. Open questions

- None blocking. (Whether registration timing should eventually move to runtime-only is an ADR-006 question, not this FIX.)

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-09 | Ruslan Gabitov | Draft. `StartProcess` deadlocks via two defects: **RC1** — `RegisterEvent` requires `Active` but event-start nodes register during `New` (`Created`); **RC2** — `StartProcess` holds `t.m` across `launchInstance`, which re-locks it (re-entrant `sync.Mutex` deadlock — hits `basic-process` directly via `launchInstance:403`, and `timer-event` via `Thresher.State()`). Fix: permit non-terminal states for registration (RC1) + narrow `StartProcess` to release `t.m` before `launchInstance` (RC2). Both broken examples in scope. (RCA expanded from the initial guard-only draft after reproducing the deadlock — Draft amendment, no separate row.) |
