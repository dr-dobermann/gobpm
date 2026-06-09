# FIX-002 — StartProcess deadlock: re-entrant engine mutex + event-start Created-guard

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-09 |
| Owner | Ruslan Gabitov |
| Related | [ADR-001 v.4 §4.2 Execution Model](../design/ADR-001-execution-model.md) (lifecycle); [ADR-006 v.1 Events & Subscriptions](../design/ADR-006-events-and-subscriptions.md); [FIX-001](FIX-001-thresher-eventhub-startup-race.md) (EventHub Start/Run split) |

## 1. Symptoms

Every `StartProcess` call **deadlocks** (`fatal error: all goroutines are asleep - deadlock!`). Two examples surface it — both call `engine.StartProcess`:

- **`examples/basic-process`** (plain Start → ServiceTask → End): deadlocks, no useful output.
- **`examples/timer-event`** (timer **on the start event**): peels back in three layers.
  1. First fails to *construct* (RC1) —

     ```
     Failed to start process: couldn't create an Instance for process "…"
       Error: couldn't register event definitions
         node_name: timer-start
         Error: instance isn't active (current state: Created)
     ```
  2. Once that construction-time guard is relaxed, it deadlocks in `StartProcess` exactly like basic-process (RC2).
  3. With RC1+RC2 fixed, `StartProcess` succeeds, the timer **fires correctly at 5 s**, the service task runs and the instance completes — then the *example* deadlocks (RC3): the deadlock panic lands at **5.01 s** (measured), the moment the timer fires and the engine's last worker goroutine exits, leaving only the example's `main` and `eventHub.Run` blocked on `context.Background().Done()` (a nil channel).

`examples/simple-timer` does **not** call `StartProcess` (only `RegisterProcess` + `Run`), so it hits neither engine path and exits cleanly — which is why it looked "fine".

Masked from CI because CI only **builds** the example modules, never **runs** them (`make build-all` has no run step) — see §5.

## 2. Root-cause analysis

There are **three** independent defects. Two are in the engine on the `StartProcess` path (RC1, RC2); the third is in an example (RC3) and only surfaced once RC1+RC2 were fixed. A plain process hits RC2; an event-start process hits RC1 first, then RC2, then (for `timer-event`) RC3.

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

### RC3 — `examples/timer-event` waits on a nil channel (example bug, not the engine)

With RC1+RC2 fixed the engine runs the process correctly, but the *example* can never terminate:

- `examples/timer-event/main.go:106` — `ctx := context.Background()` is handed to `engine.Run(ctx)`.
- `examples/timer-event/main.go:123-127` — `main` then blocks on `select { case <-ctx.Done(): … }`. `context.Background().Done()` returns `nil`, so this waits on a nil channel forever (the "Press Ctrl+C to exit" banner implies a signal handler that the example never installs).
- `eventhub.go:93` — `EventHub.Run` likewise blocks on `<-ctx.Done()` (same `Background` ctx, nil channel) for the hub's lifetime — normal, it is just the shutdown wait.

So once the timer has fired and the instance has completed, the only two live goroutines are `main` and `eventHub.Run`, both parked on a nil channel; Go's runtime detects "all goroutines are asleep" and panics.

**The engine is proven sound — there is no engine-side timer-delivery defect.** Evidence:

- The deadlock panic lands at **5.01 s** (`time` measured on the built binary) — exactly the timer's `timeDate` (`time.Now().Add(5*time.Second)`, `main.go:37/39`). The timer fired, the one-shot waiter ran `processTimerEvent` and exited (`timer.go:248` `go runTimerService` → fire → `RemoveWaiter`), which is why the goroutine dump shows it gone — not because it never started.
- Replacing the example's wait with a cancelable `context.WithTimeout(…, 8s)` makes the program print `Process completed` and exit 0, with no engine change.

`examples/basic-process` does not have RC3 — it returns from `main` after `StartProcess` rather than parking on `ctx.Done()`, which is why it already runs to completion once RC2 is fixed.

## 3. Solution

RC1 and RC2 are engine fixes; RC3 is an example fix. All three are needed for both broken examples to run.

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

### 3.3 — RC3: give `examples/timer-event` a terminating wait

Hand `engine.Run` a cancelable context and wait on it (so the program exits cleanly after the timer fires), instead of parking `main` on `context.Background().Done()`:

```go
// examples/timer-event/main.go
ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
defer cancel()
err = engine.Run(ctx)
// …
<-ctx.Done() // bounded; the 5 s timer fires well within the window
fmt.Println("Process completed")
```

The 8 s budget gives the 5 s `timeDate` timer room to fire and the instance to complete before the context cancels. Using a plain `<-ctx.Done()` (not a one-case `select`) also clears a `gosimple S1000` lint. Engine code is untouched.

### 3.4 Alternatives considered

- **Make `Thresher.State()` lock-free (atomic state), like `Instance`.** Removes the `State()` re-entry (timer-event) but NOT the direct `launchInstance:403` re-lock (basic-process) — insufficient alone. A reasonable *complementary* hardening; optional, can follow.
- **Recursive mutex.** Rejected — not idiomatic in Go; re-entrant locking masks design problems.
- **Defer event-start registration to first track execution (after `Active`).** Rejected for RC1 — more invasive, and orthogonal to RC2 (which would still deadlock basic-process).

## 4. Verification

| # | Check | Expectation |
|---|---|---|
| V1 | Unit: a `Created` instance registers an event-start definition without error (RC1). | succeeds on `Created`. |
| V2 | Unit: `RegisterEvent` on a terminal (`Completed`/`Terminated`) instance still errors (RC1). | guard preserved. |
| V3 | Unit/integration: `StartProcess` of a plain process **and** of an event-start process returns without deadlock (RC2). | no re-entrant lock; `t.m` not held across `launchInstance`. |
| V4 | `examples/basic-process` runs to completion (RC2). | exit 0; no deadlock, no "instance isn't active". |
| V5 | `examples/timer-event` runs to completion (RC1+RC2 engine, RC3 example): the timer fires at ~5 s, the instance completes, and the program exits. | exit 0; prints `Process completed`; no deadlock panic. |
| V6 | No regression: `make ci` green (race tests + diff-coverage + vuln); `examples/simple-timer` still runs. | all existing tests pass. |

## 5. Prevention

- **CI runs examples, not just builds them.** The whole class of failure hid because `make build-all` never executes the example modules. Add a CI step (and a `make` target) that runs each example with a timeout and asserts exit 0. Tracked as a follow-up; see [[project_examples_runtime_broken]].

## 6. Regressions / scope notes

- **`examples/basic-process` is in scope** — it is the same RC2 re-entrancy (earlier mis-attributed to a no-op `ServiceTask`); fixed by §3.1 + §3.2.
- **`examples/timer-event` needs all three** — the engine fixes (§3.1 + §3.2) plus the example fix (§3.3). An RC4 "engine never schedules the timer" hypothesis was **investigated and disproven**: the timer fires at exactly 5.01 s, so the only remaining defect is the example's nil-channel wait.
- The guard change (§3.1) must not weaken terminal-state protection (V2).
- Narrowing the `StartProcess` lock (§3.2) must keep the snapshot read and the registry write each mutex-guarded — just not held across `launchInstance`. Concurrent `StartProcess` calls remain safe.
- RC3 (§3.3) is an example-only change; no engine behavior changes.

## 7. Related

- [ADR-001 v.4 §4.2](../design/ADR-001-execution-model.md) — the lifecycle (`Created → Active → Completed`, `Terminating → Terminated`) whose guard this fix corrects.
- [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) — event delivery / subscription semantics (the eventual owner of registration timing).
- [FIX-001](FIX-001-thresher-eventhub-startup-race.md) — established that the EventHub is started before instances register.

## 8. Implementation summary

Landed on branch `fix/event-start-registration` (off `master`). RC1 + RC2 are engine fixes; RC3 is the example fix.

**Changes**

- **RC1** — `internal/instance/instance.go:574-580` (`RegisterEvent`): the `Active`-only guard became `is := inst.State(); if is != Created && is != Active { … "instance is terminal…" }`, permitting construction-time (`Created`) and runtime (`Active`) registration and refusing only terminal instances.
- **RC2** — `pkg/thresher/thresher.go` (`StartProcess`, ~:358-380): the engine mutex now wraps only the `t.snapshots[processID]` lookup (`:373-374`) and is released before `t.launchInstance(s)`; the started-check uses `t.State()` instead of an unguarded `t.state` read.
- **RC3** — `examples/timer-event/main.go:106` (`context.WithTimeout(…, 8s)` + `defer cancel()`) and `:126` (plain `<-ctx.Done()` → `Process completed`).

**Tests added**

- `internal/instance/register_event_test.go` — `TestRegisterEventAllowsCreated` (V1), `TestRegisterEventRejectsTerminal` (V2).
- `pkg/thresher/thresher_process_test.go` — `TestStartProcess_NoReentrantDeadlock` (V3, timeout-guarded).

**Verification results**

| # | Result |
|---|---|
| V1 | 🟢 a `Created` instance registers an event-start definition — passes. |
| V2 | 🟢 a terminal instance is still refused — passes. |
| V3 | 🟢 `StartProcess` returns without a re-entrant deadlock — passes. |
| V4 | 🟢 `examples/basic-process` exit 0. |
| V5 | 🟢 `examples/timer-event` exit 0; timer fires ~5 s; prints `Process completed`. |
| V6 | 🟢 `make ci` green (lint 0 issues, race tests, diff-coverage 100 % of 10 touched lines, govulncheck); `examples/simple-timer` exit 0. |

**Milestone commits**

- `b7af364` — M1 (RC1): `RegisterEvent` permits non-terminal states + V1/V2 tests.
- `0ffa77a` — M2 (RC2): `StartProcess` releases `t.m` before `launchInstance` + V3 test.
- `adddea3` — doc: add RC3, disprove RC4.
- `004319e` — M3 (RC3): `examples/timer-event` cancelable context.
- (`9d3e947` — original RCA-completion doc commit on the branch.)

## 9. Open questions

- None blocking. (Whether registration timing should eventually move to runtime-only is an ADR-006 question, not this FIX.)

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-09 | Ruslan Gabitov | Accepted (landed). `StartProcess` deadlocks via three defects: **RC1** — `RegisterEvent` requires `Active` but event-start nodes register during `New` (`Created`); **RC2** — `StartProcess` holds `t.m` across `launchInstance`, which re-locks it (re-entrant `sync.Mutex` deadlock — hits `basic-process` directly via `launchInstance:403`, and `timer-event` via `Thresher.State()`); **RC3** — `examples/timer-event` waits on `context.Background().Done()` (nil channel), so it deadlocks after the timer correctly fires. Fix: permit non-terminal states for registration (RC1) + narrow `StartProcess` to release `t.m` before `launchInstance` (RC2) + give the example a cancelable context (RC3). Both broken examples in scope. (RCA expanded across Draft amendments while reproducing the deadlock: first guard-only → RC1+RC2; then, after measuring the panic at 5.01 s, an RC4 "engine doesn't schedule the timer" hypothesis was disproven and the remaining failure attributed to RC3, the example. Draft amendments, no separate rows.) |
