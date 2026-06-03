# FIX-001 — Thresher / EventHub startup race

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-03 |
| Owner | Ruslan Gabitov |
| Surfaces from | [SRD-001](../srd/SRD-001-multi-module-scaffold.md) — `-race` now gates CI; pre-existing race becomes visible |
| Related conception | [ADR-001 v.2 Execution Model](../design/ADR-001-execution-model.md) — race-freedom is a P0 verification gate per §7 |

## 1. Symptoms

`TestThresher_EventQueueProcessing/event_queue_processes_registered_events` (in `pkg/thresher/thresher_events_test.go`) is **flaky** under `-race`:

```
go test -race -count=5 -run TestThresher_EventQueueProcessing ./pkg/thresher/
# → 2/5 runs fail with:
#   WARNING: DATA RACE
#   testing.go:1617: race detected during execution of test
```

The race fires non-deterministically — sometimes the test passes, sometimes the race detector flags it. The test logic itself is reasonable (start the Thresher, then register an event); the race is in the engine's startup sequence and was masked by timing on faster runs and by the previous absence of `-race` in CI before commit `d731895` (`chore/ci-audit`).

`-race` is now gating CI per [SRD-001 §3 / §5](../srd/SRD-001-multi-module-scaffold.md). Without addressing this race, master CI fails intermittently.

## 2. Root cause analysis

The race involves two unsynchronized scalar fields on the shared `EventHub` struct: `started bool` and `ctx context.Context`. The map field `waiters` is properly mutex-protected (see `eh.m.Lock/RLock` usage); the two scalars are NOT.

### 2.1 The racing writes (background goroutine)

`internal/eventproc/eventhub/eventhub.go:48-60`:

```go
func (eh *EventHub) Run(ctx context.Context) error {
    if eh.started { ... }     // line 49 — read of started (no mutex)
    eh.started = true         // line 55 — WRITE of started (no mutex)
    eh.ctx = ctx              // line 56 — WRITE of ctx (no mutex)
    <-ctx.Done()
    return ctx.Err()
}
```

This `Run` runs in a goroutine launched by `Thresher.Run`:

`pkg/thresher/thresher.go:184-190`:

```go
// Run eventhub in background
go func() {
    _ = t.eventHub.Run(ctx)   // line 186 — Run executes in this goroutine
}()

// Give eventhub a moment to initialize
time.Sleep(1 * time.Millisecond)   // line 190 — TIMING HOPE, NOT A BARRIER
```

### 2.2 The racing reads (caller goroutine)

After the 1 ms sleep, the caller calls `Thresher.RegisterEvent`, which calls into `EventHub.RegisterEvent`:

`internal/eventproc/eventhub/eventhub.go:70-113`:

```go
func (eh *EventHub) RegisterEvent(...) error {
    if !eh.started { ... }    // line 70 — READ of started (no mutex)
    ...
    if err := w.Service(eh.ctx); err != nil { ... }   // line 113 — READ of ctx (no mutex)
    ...
}
```

### 2.3 The race-detector report (definitive evidence)

```
WARNING: DATA RACE
Read at 0x...4f8 by goroutine 10:
    EventHub.RegisterEvent()  eventhub.go:70
    Thresher.RegisterEvent()  thresher.go:222
    test func1()              thresher_events_test.go:225

Previous write at 0x...4f8 by goroutine 11:
    EventHub.Run()            eventhub.go:55
    Thresher.Run.func1()      thresher.go:186

(second race report at offset ...4b0 — same shape but on eh.ctx,
 reads at eventhub.go:113 vs write at eventhub.go:56)
```

Both racing fields are simple scalars touched across goroutines with no `sync.Mutex`, no `sync/atomic`, no channel handshake. The `time.Sleep(1 * time.Millisecond)` in the caller is a non-synchronization — it gives the background goroutine *time* to run but does not *force* it to, and even if it did, the Go memory model would not guarantee visibility of the writes without proper synchronization primitives.

### 2.4 Why it became visible now (and not earlier)

- `-race` was added to CI in `d731895` (chore/ci-audit, merged before this work).
- The race is timing-dependent. CI runs since then likely got lucky on the timing.
- SRD-001 added `make ci` with race-gated `test-all`, plus the depguard rules. Running `make ci` locally and on CI more reliably exercises the timing where the race fires.

The fragility was always there; SRD-001 just stopped letting it slip through.

## 3. Solution

Two viable approaches. Solution A is preferred (cleaner separation of concerns; matches ADR-001's intent); Solution B is the smaller-diff fallback.

### 3.1 Solution A (preferred) — split `Start` from `Run`

Introduce a new `EventHub.Start(ctx)` method that performs the synchronous initialization (`started = true`, `ctx = ctx`), and reduce `Run(ctx)` to just the blocking event-loop body. `Thresher.Run` calls `Start` synchronously before spawning the background goroutine, and removes the 1 ms sleep.

Sketch:

```go
// internal/eventproc/eventhub/eventhub.go
func (eh *EventHub) Start(ctx context.Context) error {
    if eh.started {
        return errs.New(errs.M("eventHub is already started"), ...)
    }
    eh.started = true
    eh.ctx = ctx
    return nil
}

func (eh *EventHub) Run(ctx context.Context) error {
    if !eh.started {
        return errs.New(errs.M("eventHub not started"), ...)
    }
    <-ctx.Done()
    return ctx.Err()
}
```

```go
// pkg/thresher/thresher.go (excerpt of Run)
t.ctx = ctx

if err := t.eventHub.Start(ctx); err != nil {       // SYNCHRONOUS
    return err
}

go func() {
    _ = t.eventHub.Run(ctx)                          // BACKGROUND, but started already
}()

// time.Sleep(1ms) DELETED — no longer needed.

return t.UpdateState(Started)
```

Why this is the preferred fix:

- `started` and `ctx` are written **synchronously in the parent goroutine, before the background goroutine is spawned**. This makes the writes safe to read from both sides without a mutex:
  - Reads from the spawned goroutine (e.g., `eh.Run` checking `eh.started`) see the writes via the **goroutine-creation happens-before edge** in the Go memory model — every write in the parent before `go f()` is visible to `f`.
  - Reads from the parent goroutine (and any goroutine sharing an EventHub reference that was published *after* `Start` returned — e.g., a Thresher whose `Run` returned successfully) see the writes via plain sequential consistency on the parent's own program order, then via the synchronization that published the EventHub reference.
  - The `waiters` map remains mutex-protected as today; this fix doesn't change its access pattern.
- Removes the `time.Sleep(1 * time.Millisecond)` — that was a code smell and is now demonstrably wrong.
- Cleanly separates concerns: `Start` is an initialization step (returns errors if misconfigured); `Run` is the blocking loop (returns when ctx is done).
- Aligns with ADR-001 §4.3 channel-topology intent: the engine has explicit setup phases before background goroutines spawn.
- The `EventHub` interface in `internal/eventproc/eventproc.go` is internal; adding a method to its default implementation does not break public API.

### 3.2 Solution B (fallback) — atomic protection

If splitting `Start`/`Run` is undesirable for any reason (e.g., the `EventHub` interface itself would need to add `Start`), the smaller-diff alternative protects the two scalars with `sync/atomic`:

```go
type EventHub struct {
    started atomic.Bool
    ctxVal  atomic.Value   // stores context.Context
    ...
}

func (eh *EventHub) Run(ctx context.Context) error {
    if !eh.started.CompareAndSwap(false, true) {
        return errs.New(errs.M("already started"), ...)
    }
    eh.ctxVal.Store(ctx)
    <-ctx.Done()
    return ctx.Err()
}

func (eh *EventHub) RegisterEvent(...) error {
    if !eh.started.Load() { ... }
    ...
    ctx, _ := eh.ctxVal.Load().(context.Context)
    if err := w.Service(ctx); err != nil { ... }
}
```

The 1 ms sleep in `Thresher.Run` still needs to go away — atomic protection makes the race detector happy but doesn't fix the fact that `RegisterEvent` may run *before* `Run` if the goroutine hasn't been scheduled. Replace the sleep with a small handshake (e.g., a buffered channel signal from `Run` once `started.Store(true)` completes; `Thresher.Run` reads-or-times-out).

This solution preserves the existing `EventHub` interface shape but introduces additional handshake complexity. Solution A is strictly better.

### 3.3 Decision

**Adopt Solution A.** The `EventHub` interface (in `internal/eventproc/eventproc.go`) currently exposes only `RegisterEvent` / `UnregisterEvent` / `PropagateEvent` / `Run` / `RemoveWaiter`. Adding `Start` to that interface (and to the default implementation) is a small, internal change. The thresher's `Run` becomes cleaner; the sleep disappears.

## 4. Verification

| What | How |
|---|---|
| **Race is gone** | `go test -race -count=100 -run TestThresher_EventQueueProcessing ./pkg/thresher/` passes 100/100. (Pre-fix: ~40 failures expected on a 100-run loop.) |
| **No regression in other Thresher tests** | `go test -race ./pkg/thresher/...` clean. |
| **Whole core is race-clean under stress** | `make test-all` clean; additionally `go test -race -count=10 ./...` clean (covers the rest of core under repeated runs). |
| **EventHub still rejects double-start** | New unit test: `eh.Start(ctx)` twice → second call returns the "already started" error. |
| **EventHub still rejects pre-Start RegisterEvent** | Existing test path covering "eventHub isn't started" remains green. |
| **No timing-dependent test logic remains** | Code review confirms `time.Sleep(1 * time.Millisecond)` is removed from `Thresher.Run`. |
| **CI passes after fix lands** | `make ci` runs clean locally; GitHub Actions check passes on the merge commit. |

The acceptance gate for flipping this FIX to Accepted: the `-count=100` race-stress test on the regression test is committed (or scripted in CI) and passes.

## 5. Prevention

Two project-wide habits that would have caught this earlier:

1. **`-race` on `go test` from day one of CI.** Now in place per chore/ci-audit. This FIX validates that policy.
2. **Convention: any field touched by a goroutine spawned in a method must be either**:
   - written **before** the goroutine spawn (visible via goroutine-creation happens-before), or
   - protected by a `sync` primitive (Mutex, atomic, channel handshake).

   No `time.Sleep` ever counts as synchronization. Anywhere `time.Sleep` appears in non-test code, code review should ask: "what is this hoping for, and what's the actual synchronization primitive?"

A linter would help enforce #2 but no off-the-shelf one catches this exact pattern. Consider a one-line internal check: grep for `time.Sleep` in non-test Go files and review each occurrence at PR time.

## 6. Regression analysis

The change is `EventHub` adding a `Start` method and `Thresher.Run` calling it synchronously before spawning the background goroutine. Risks:

- **Other callers of `EventHub.Run`** — only one production caller (`Thresher.Run`). Search confirmed via `grep -rn 'eventHub.Run\|EventHub\.\|eh\.Run'` (results: only `thresher.go` and tests). Tests that mock `EventHub` (via mockery) need a `Start` expectation added — small mechanical update.
- **Mockery-generated mocks** — `EventHub` interface gains `Start(ctx) error`. Re-run `mockery` to regenerate `generated/mockeventproc/MockEventHub.go`. Any test that previously asserted only `Run` expectations now also needs a `Start` expectation. The list of such tests is bounded (grep `MockEventHub` in `pkg/thresher/*_test.go` and `internal/eventproc/eventhub/*_test.go`).
- **Other call paths through `eventHub.started` / `eventHub.ctx`** — the existing `eh.started` check in `Run` (line 49) becomes redundant if `Start` is the only entry point that sets it. Cleanup: in `Run`, replace `if eh.started { ... return already-started-error }` with `if !eh.started { ... return not-started-error }`. The two error messages flip — make sure existing tests that match error text are updated.
- **Behavior under double-`Run` calls** — previously double-`Run` would error on the second call via `eh.started` check. With Solution A, `Run` no longer sets `started`; it only checks it. Double-`Run` becomes a different error path. Confirm existing test for "already started" still passes (it should — `Start` now does the check, and `Thresher.Run` calls `Start` once).

## 7. Related

- [ADR-001 v.2 Execution Model §7](../design/ADR-001-execution-model.md) — race-freedom is verification gate #1; this FIX is the first concrete payment on that gate.
- [SRD-001 v.1 §5 Tests / Verification](../srd/SRD-001-multi-module-scaffold.md) — `-race` is the CI gate that surfaces this and similar bugs.
- [ADR-003 v.1 §4.6 step 3](../design/ADR-003-module-layout.md) — when `EventHub` interface moves from `internal/eventproc/` to `pkg/messaging/` (per the migration plan), the `Start` method moves with it. This FIX is upstream of that promotion; the change must happen here first to land the race-free design before the public-interface freeze.
- (Potential follow-up) Audit other engine startup sequences for similar racy "Run-in-a-goroutine then sleep" patterns. Likely none elsewhere, but worth a one-time pass.

## 8. Implementation summary

(To be populated after landing — per project convention, FIX §8 is the post-landing record of what actually changed, with commit hashes and any deviations from §3.)

Pending:

- [ ] Add `Start(ctx context.Context) error` to the `EventHub` interface in `internal/eventproc/eventproc.go`.
- [ ] Implement `Start` on the `*EventHub` default implementation in `internal/eventproc/eventhub/eventhub.go`; reduce `Run` to the blocking-loop body.
- [ ] Remove the `time.Sleep(1 * time.Millisecond)` from `Thresher.Run` in `pkg/thresher/thresher.go`; call `t.eventHub.Start(ctx)` synchronously before the `go func() { eh.Run(ctx) }()` spawn.
- [ ] Regenerate mocks (`make gen_mock_files`).
- [ ] Update affected test expectations (mock `Start` calls; flipped error messages if any).
- [ ] Add the `-count=100` race-stress assertion as a separate test (or document the command in CI).
- [ ] Verify `make ci` passes locally; push; verify CI passes on the branch.
- [ ] Flip this FIX to Accepted after CI is green on master.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-03 | Ruslan Gabitov | Initial Draft. Pre-acceptance iteration ongoing; amendments folded into this Draft without per-round history rows. When v.1 flips to Accepted (after merge and `-count=100` race-stress demonstration), this row records the Accepted state. |
