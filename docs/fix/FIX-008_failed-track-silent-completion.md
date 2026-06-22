# FIX-008 «A failed track silently completes the instance (track error lost)»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted v.1 (2026-06-22, branch `fix/surface-failed-track`, implemented).
**Date:** 2026-06-22.
**Author:** Ruslan Gabitov.
**Branch:** `fix/surface-failed-track` (surface a `TrackFailed` track as an instance failure).
**Tracking issue:** [#165](https://github.com/dr-dobermann/gobpm/issues/165) (bug).
**Paired doc:** none (local to `internal/instance` loop lifecycle).
**Upstream:** [FIX-007](FIX-007_event-gateway-concurrent-fire.md) / [#164](https://github.com/dr-dobermann/gobpm/issues/164) (its diagnosis found this hole — sideways FIX→FIX), [ADR-001 v.5](../design/ADR-001-execution-model.md) (the single-writer loop owns lifecycle state + `lastErr`), [ADR-017 v.1](../design/ADR-017-single-writer-event-delivery.md) (the broader single-writer direction).

---

## 1. Symptoms

A track that ends in `TrackFailed` (its node's execution returned an error) is **not surfaced** to the instance. The instance can reach the terminal **`Completed`** state with **`lastErr == nil`** — the track's error is silently swallowed, and a host observing `WaitCompletion`/`State()` sees a clean completion for a process that actually failed mid-flight.

There is no always-on reproduction on current `master`: the one path that drove a live track to `TrackFailed` was the FIX-007 gate-self-execution race, and FIX-007 closed it. The defect was **observed during the FIX-007 diagnosis** — the gate node executed out of turn, failed loudly (`TrackFailed`), and the instance completed with the winning arm short of its end and no error reported. The hole remains for **any future** `run()`-path node failure; §4.1 reproduces it deterministically with a node that errors on purpose.

```
instance: state=Completed  lastErr=nil   ;  a track ended TrackFailed, its error lost
```

---

## 2. Root cause analysis

### 2.1 The loop's spawn wrapper maps every non-AwaitingMerge end to `evEnded`

`internal/instance/instance.go`, the per-track goroutine wrapper (`:639-644`):

```go
t.run(ctx)

kind := evEnded
if t.inState(TrackAwaitingMerge) {
    kind = evAwaiting
}
inst.emit(trackEvent{kind: kind, track: t})
```

Only `TrackAwaitingMerge` is distinguished. A track that returned in **`TrackFailed`** (or `TrackCanceled`) is reported as a plain `evEnded`.

### 2.2 The `evEnded` handler only decrements `active`

The loop (`:692-693`):

```go
case evEnded:
    active--
    inst.recheckAwaitingJoins()
```

No failure check. So a `TrackFailed` track → `evEnded` → `active--` → when `active == 0` the loop falls through to the terminal decision (`:715-718`):

```go
if stopping {
    inst.setState(Terminated)
} else {
    inst.setState(Completed)   // <- a failed track lands here: Completed, lastErr never set
}
```

### 2.3 `run()` records the error on the *track*, but it never reaches the instance

`run()` does set the track's own error on a node failure (`internal/instance/track.go`):

```go
nextFlows, err := t.executeNode(ctx, step)
if err != nil {
    t.lastErr = err
    t.updateState(TrackFailed)
    return
}
```

But `t.lastErr` is a per-track field; nothing propagates it to `inst.lastErr` (`atomic.Pointer[error]`, `instance.go:96`). `inst.lastErr` is set only at `instance.go:733` (`spawnForks` build errors) and via `Instance.fail` (`activation.go:56`), neither of which is on the `run()` failure path.

### 2.4 Contrast: the working failure path (`activation.go` `fail`)

`Instance.fail` (`activation.go:56-65`) is exactly the surface-and-terminate the `run()` path is missing:

```go
func (inst *Instance) fail(err error) {
    inst.Logger().Warn("instance failing", "instance", inst.ID(), "error", err)
    inst.lastErr.Store(&err)
    if inst.cancel != nil {
        inst.cancel()           // ctx.Done -> the loop's stopAll() -> Terminated
    }
}
```

It records the error and cancels the instance ctx, which the loop's `case <-done: stopAll()` turns into `Terminated`. The `run()`-path `TrackFailed` simply never calls it.

### 2.5 Why no test caught it

The loop tests assert completion/termination but not "a failed track ⇒ instance failed with `lastErr`", and (until FIX-007's race) no exercised path drove a live track to `TrackFailed`. §4.1 adds the missing canary.

---

## 3. Solution

Distinguish a failed track in the spawn wrapper and surface it through the **existing** `Instance.fail` path, so a `TrackFailed` track fails the instance (`Terminated` + `lastErr`) instead of silently completing it.

There is no dedicated `Failed` instance state — the open vocabulary is `Created / Active / Completed / Terminating / Terminated` (`pkg/thresher/handle.go:145-149`). `Terminated` + a non-nil `lastErr` is already how an engine-detected fatal error is reported (the `activation.go` `fail` path), so this fix is consistent with it: a failed track yields `Terminated` with the track's error.

### 3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| **A. Distinct `evFailed` kind → `inst.fail(track err)` in the loop** | Reuses the existing surface-and-terminate (`fail`); keeps the kinds honest; one clear handler | A new `kind` value (small) | ✅ **chosen** |
| B. Inline `if ev.track.inState(TrackFailed)` inside the `evEnded` case | No new kind | Conflates "ended" and "failed" in one case; the loop reads less clearly; easy to overlook | ❌ rejected |
| C. Leave it | — | Silent data loss: a process that failed reports `Completed`/`lastErr=nil`. BPMN: an unhandled activity error must fault the process, not complete it. | ❌ rejected |

### 3.2 Changes by file

#### §3.2.1 `internal/instance/event.go` — the `evFailed` kind

Add an `evFailed` value to the `trackEventKind` enum (beside `evEnded`/`evAwaiting`/`evMerged`/`evParked`/`evFork`) + its `String` case: a track whose `run()` returned in `TrackFailed` (its node execution errored).

#### §3.2.2 `internal/instance/instance.go` — classify + surface a failed track

Two helpers (extracted to keep `loop()` under the `gocyclo` limit, which the inline switch + new case would have exceeded):

- **`trackEndKind(t)`** classifies a returned track — `TrackAwaitingMerge` → `evAwaiting`, **`TrackFailed` → `evFailed`**, else `evEnded`. The spawn wrapper (`:639`) calls it: `inst.emit(trackEvent{kind: trackEndKind(t), track: t})`.
- **`failFromTrack(t, stopAll)`** surfaces the failure. The loop's new `case evFailed:` (`:706`) calls it, then `active--`:

```go
case evFailed:
    inst.failFromTrack(ev.track, stopAll)
    active--
```
```go
func (inst *Instance) failFromTrack(t *track, stopAll func()) {
    err := t.lastErr
    if err == nil { // defensive: run() sets lastErr before TrackFailed
        err = errs.New(errs.M("track %s failed", t.ID()),
            errs.C(errorClass, errs.OperationFailed))
    }
    inst.fail(err) // store lastErr + cancel the instance ctx
    stopAll()      // EXPLICIT — see below
}
```

**`stopAll()` must be called explicitly** — the draft assumed `inst.fail` (→ `cancel` → `<-done` → `stopAll`) would terminate, but that is wrong for the **last** track: `case evFailed: …; active--` drops `active` to 0, and the loop exits *before* the next `select` iteration can take `case <-done:`, so `stopping` stays false and the instance settles **`Completed`**, not `Terminated`. Calling `stopAll()` synchronously sets `Terminating`, so the loop ends `Terminated` (`:715`). `failFromTrack` runs on the loop goroutine (the `evFailed` case *is* the loop), preserving `fail`'s single-writer-of-`lastErr` contract. The defensive nil-guard wraps a `nil` `t.lastErr` (shouldn't happen — `run()` sets it before `TrackFailed`) so the instance still fails rather than completing.

**`TrackCanceled` stays `evEnded`** — a canceled track only occurs during an already-in-progress terminate (ctx cancel / `stopAll`), where `stopping` is set and the instance is heading to `Terminated` anyway; re-failing it would be wrong (a deliberate cancel is not a fault).

---

## 4. Verification

Current coverage: the loop's terminal-state tests cover `Completed` and ctx-cancel `Terminated`, but none asserts "a failed track ⇒ `Terminated` + `lastErr`".

### 4.1 Regression tests (mandatory)

**New:** `internal/instance/failed_track_test.go` (external `package instance_test`) + `failed_track_internal_test.go` (in-package, for the nil-guard).

| Test | Setup | Assertion |
|---|---|---|
| `TestFailedTrackFailsInstance` | a process whose node's `Exec` returns an error | the instance ends **`Terminated`** (not `Completed`); `inst.LastErr()` is the node's error (not nil) |
| `TestNormalCompletionUnaffected` (regression) | an ordinary process that runs clean | ends **`Completed`** with `lastErr == nil` — the `evFailed` path doesn't perturb the success case |
| `TestFailFromTrackNilErr` (in-package) | `failFromTrack` with a track carrying a `nil` `lastErr` | the defensive guard substitutes a generic "track failed" error; the instance still fails (no nil deref, not `Completed`) |

Run under `-race`. (A multi-track variant — one track fails while another is live — confirms `stopAll` stops the sibling and the instance still ends `Terminated`.)

### 4.5 Observability

`Instance.fail` already logs `WARN instance failing … error=…`, so a surfaced track failure is visible in logs without new instrumentation.

---

## 5. Prevention

- Doc-comment the spawn-wrapper classification and the `case evFailed:` handler: **a track that ends `TrackFailed` faults the instance (error surfaced via `fail`), it does not silently complete** — name `TestFailedTrackFailsInstance` as the canary.
- The `evFailed` kind makes the loop's intent explicit, so a future reader won't reintroduce the "everything is `evEnded`" conflation.

---

## 6. Regressions / side-effects

### 6.1 What may rely on the old behaviour

- **Normal completion** (`evEnded` → `active--` → `Completed`) is unchanged — only `TrackFailed` is rerouted.
- **`TrackCanceled`** stays `evEnded` — unchanged; canceled tracks during a terminate still just decrement, and the instance ends `Terminated` because `stopping` is already set.
- **The `activation.go` `fail` path** (join/activation failures) is unchanged — this fix reuses the same `fail`, so both failure sources converge on one surfacing mechanism.
- **No double-terminate:** `stopAll` is guarded by `if stopping { return }`, and `fail`→`cancel` is idempotent; a second failed track calling `fail` re-stores `lastErr` (last-writer) without re-terminating destructively.
- **A pre-existing test relied on the bug — `TestCloneRaceTwoInstances`.** Its `ServiceTask` used an operation with **no implementation**, which errored on every run — silently `Completed` pre-fix. FIX-008 correctly surfaced it (→ `Terminated`), breaking the test's `Eventually(Completed)` / `NoError(LastErr)` assertions (master passed; the FIX-008 branch failed, reproducible in isolation). The test's intent is the clone data-race (per-instance node graphs, no shared mutation), not task failure, so its op was replaced with a succeeding `gooper` no-op. This is exactly the masked-failure class this fix targets — a real validation, not a workaround.

### 6.2 Rollback path

Single-commit revert (the `evFailed` kind, the spawn-wrapper branch, the loop case, and the tests).

---

## 7. Related

- [FIX-007](FIX-007_event-gateway-concurrent-fire.md) / [#164](https://github.com/dr-dobermann/gobpm/issues/164) — the diagnosis that surfaced this hole (it masked the gate-self-execution race as a silent partial completion). Sideways FIX→FIX.
- [ADR-001 v.5](../design/ADR-001-execution-model.md) — the single-writer loop is the sole owner of instance lifecycle state and `lastErr`; this fix keeps `fail` on the loop goroutine. Up.
- [ADR-017 v.1](../design/ADR-017-single-writer-event-delivery.md) — the broader single-writer direction; orthogonal to this fix but the same principle (one goroutine owns the instance's state). Sideways/up.

---

## 8. Implementation summary (stage-by-stage actual landings + deltas)

### 8.1 Stages by commit (branch `fix/surface-failed-track`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| Doc | `58c0c98` | FIX-008 (this doc, Draft) | — |
| Fix | `0cf6442` | `event.go` (`evFailed` kind + `String`); `instance.go` (`trackEndKind` + `failFromTrack` helpers, the `case evFailed:`); `clone_race_test.go` (bug-reliant op replaced) | `TestFailedTrackFailsInstance`, `TestNormalCompletionUnaffected`, `TestFailFromTrackNilErr` |

### 8.2 Empirical findings — where reality diverged from the §3 draft

- **`stopAll()` must be explicit (§3.2 corrected).** The draft assumed `inst.fail` (→ `cancel` → `<-done` → `stopAll`) would terminate; for the **last** track `active` hits 0 and the loop exits *before* `<-done` is selected, settling `Completed`. `failFromTrack` now calls `stopAll()` synchronously → `Terminated`.
- **A pre-existing test relied on the bug** (`TestCloneRaceTwoInstances`, §6.1) — its no-implementation op errored every run and silently `Completed`; FIX-008 surfaced it, so the test was given a succeeding no-op op. A direct validation of the fix.
- **`gocyclo` extraction.** The inline spawn-switch + the new `case evFailed:` pushed `loop()` over the `gocyclo` limit; extracting `trackEndKind`/`failFromTrack` brings it back under.

### 8.3 Verification (V-results)

- `make ci` PASS: build, lint, `-race`, diff-coverage **100% of 25 changed lines**, govulncheck.
- Touched funcs `trackEndKind` / `failFromTrack` / `String` (the `evFailed` case) → 100%.
- Full `internal/instance/ -race` green (incl. Complex + clone-race + the new tests); `FailedTrack|NormalCompletion|CloneRace` `-race -count=10` green.

## 9. Open questions

- **None.** The terminal state is `Terminated` (+ `lastErr`): there is no `Failed` state in the open `InstanceState` vocabulary, and `Terminated` + `lastErr` is already the engine's fatal-error report (the `activation.go` `fail` path), so a failed track converges on it.
