# FIX-002 — Event-start registration rejected by the Created-state guard

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-09 |
| Owner | Ruslan Gabitov |
| Related | [ADR-001 v.4 §4.2 Execution Model](../design/ADR-001-execution-model.md) (lifecycle); [ADR-006 v.1 Events & Subscriptions](../design/ADR-006-events-and-subscriptions.md); [FIX-001](FIX-001-thresher-eventhub-startup-race.md) (EventHub Start/Run split) |

## 1. Symptoms

Any process whose **start node is an event** (timer / message / signal start) fails to *construct* — `StartProcess` returns a building error, the instance is never created:

```
Failed to start process: couldn't create an Instance for process "…"
  Error: couldn't register event definitions
    node_name: timer-start
    Error: instance isn't active (current state: Created)
      requester_id: …
```

Reproduced by `examples/timer-event` (exit 1). `examples/simple-timer` works only because its start node is a plain Start event and the timer is reached *after* the instance is running; `timer-event` puts the timer **on the start event itself**, so registration happens at construction.

This is currently masked from CI because CI only **builds** the example modules, never **runs** them (`make build-all` has no run step) — see §5.

## 2. Root-cause analysis

Event-start registration happens during `instance.New`, while the instance is still `Created`, but `RegisterEvent` requires `Active`.

1. `instance.New` builds the initial tracks and leaves the instance in `Created`:
   - `internal/instance/instance.go:161` — `inst.state.Store(uint32(Created))`
   - `internal/instance/instance.go:159` — `createTracks()` is called inside `New`.
2. `createTracks` → `newTrack` for each entry node:
   - `internal/instance/instance.go:436` — `newTrack(n, inst, nil)`
3. `newTrack` calls `checkNodeType`, which **registers the node's event definitions** for an event node:
   - `internal/instance/track.go:271` — `t.checkNodeType(start)`
   - `internal/instance/track.go:281-293` — `if en, ok := node.(flow.EventNode); ok { for _, d := range en.Definitions() { t.instance.RegisterEvent(t, d) … "couldn't register event definitions" } }`
4. `Instance.RegisterEvent` rejects any non-`Active` state:
   - `internal/instance/instance.go:570-576` — `is := inst.State(); if is != Active { return errs.New(errs.M("instance isn't active (current state: %s)", is), …) }`
5. The instance only becomes `Active` later, in `Run`:
   - `internal/instance/instance.go:260` — `inst.setState(Active)` (after `New` has already returned).

So construction-time registration (step 3, state `Created`) hits the `Active`-only guard (step 4) and fails.

**Why it regressed.** The `Active`-only guard was introduced with the ADR-001 v.4 lifecycle reconciliation (`Created → Active`). It is *too strict*: it conflates "don't register on a **finished** instance" (correct) with "don't register during **construction**" (wrong — that is exactly when event-start nodes must register). Before the guard, construction-time registration worked.

## 3. Solution

### 3.1 Chosen — permit registration in non-terminal states

`RegisterEvent` should reject registration only on a **terminal** instance (`Completed` / `Terminating` / `Terminated`), and permit it while the instance is being built or is running (`Created` or `Active`). The lifecycle enum is ordered `Created(0) < Active(1) < Completed(2) < Terminating(3) < Terminated(4)` (`instance.go:56-67`).

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

Rationale: event registration is legitimate both at construction (start events) and at runtime (boundary / intermediate catch events register when their track reaches them — `Active`). It must be refused only once the instance can no longer act on a fired event (terminal). The EventHub is already started by `Thresher.Run` before any `StartProcess`/`launchInstance` (FIX-001), so the hub accepts the registration; only the instance-state guard was blocking. Minimal change, restores the pre-guard behavior, keeps the protective intent (no registration on a done instance).

### 3.2 Alternatives considered

- **Defer event-start registration to first track execution (after `Active`).** Move `checkNodeType`'s `RegisterEvent` out of `newTrack` into the loop, so registration always happens while `Active`. **Rejected for this FIX** — more invasive (changes *when* the engine subscribes; a start event must be subscribed before it can be triggered, so the registration would have to move into the loop's first step with careful ordering), higher regression risk, and it does not better serve the protective intent than 3.1. Worth revisiting only if a future ADR-006 change makes runtime-only subscription the rule.
- **Special-case the start phase (a `registering` sub-state / a "building" flag).** Adds lifecycle surface for no semantic gain over "non-terminal". Rejected.

## 4. Verification

| # | Check | Expectation |
|---|---|---|
| V1 | Unit: a fresh `Created` instance with an event-start node registers its definition without error. | `RegisterEvent` succeeds on `Created`. |
| V2 | Unit: `RegisterEvent` on a `Completed`/`Terminated` instance still errors. | Guard preserved for terminal states. |
| V3 | `examples/timer-event` runs to completion (the timer fires). | exit 0; no "instance isn't active". |
| V4 | No regression: `make ci` (race tests + diff-coverage) green; `examples/simple-timer` still runs. | All existing instance/eventhub tests pass. |

## 5. Prevention

- **CI runs examples, not just builds them.** The whole class of failure hid because `make build-all` never executes the example modules. Add a CI step (and a `make` target) that runs each example with a timeout and asserts exit 0. Tracked as a follow-up; see [[project_examples_runtime_broken]].

## 6. Regressions / scope notes

- `examples/basic-process` has a **separate** failure (a deadlock from a no-implementation `ServiceTask` plus `launchInstance`'s cancel/run semantics) — **not** covered by this FIX. It is addressed separately; this FIX is scoped to the event-start-registration lifecycle bug.
- The guard change must not weaken the terminal-state protection (V2).

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
| v.1 | 2026-06-09 | Ruslan Gabitov | Initial Draft. Event-start nodes fail to register at construction because `RegisterEvent` requires `Active` but registration happens during `New` (`Created`). Fix: permit non-terminal states (`Created`/`Active`), reject only terminal. |
