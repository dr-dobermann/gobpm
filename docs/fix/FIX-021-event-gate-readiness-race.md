# FIX-021 «Event-gate test readiness race: token parked ≠ waiters registered»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted (2026-07-10, branch `refactor/instance-internal-structure`, landed — §8.1 stages `dc95cd2`…`8fbea18`).
**Date:** 2026-07-10.
**Author:** Ruslan Gabitov.
**Branch:** `refactor/instance-internal-structure` (the failure surfaced on this branch's PR CI run; the fix lands with it).
**Paired doc:** none (test-harness + build-infra fix; no engine code touched).
**Upstream:** [ADR-001 v.6](../design/ADR-001-execution-model.md) (single-writer loop), [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) (channel-based event processing), [ADR-006 v.2](../design/ADR-006-events-and-subscriptions.md) (a fired event with no catcher is a benign no-op drop) — all upheld, none changed.

---

## §1 Symptoms

### §1.1 CI-only stress-test timeout: the instance never completes on the winning arm

`TestEventGatewayConcurrentFiresStress` (500 iterations: park an Event-Based
gateway on two signal arms, fire both definitions concurrently, require exactly
one winner) failed on GitHub Actions (`make test-all`: `-race -coverprofile`,
ubuntu-latest) while passing every local run:

```
--- FAIL: TestEventGatewayConcurrentFiresStress (3.04s)
    event_gateway_internal_test.go:276:
            Error Trace:    .../internal/instance/event_gateway_internal_test.go:135
                            .../internal/instance/event_gateway_internal_test.go:276
                            .../internal/instance/event_gateway_internal_test.go:290
            Error:          Condition never satisfied
            Test:           TestEventGatewayConcurrentFiresStress
            Messages:       the instance must complete on the winning arm
```

Observed vs expected: the instance stayed parked forever (never `Completed`)
instead of completing on whichever arm's fire won. The failure hit an early
iteration (3.04s total = one 2s `require.Eventually` timeout + startup), so it
is a lost delivery, not slowness. **Not reproducible locally**: 5,500+
iterations green on a many-core dev box, including `GOMAXPROCS=2` and the exact
CI flag set (`-race` + coverage instrumentation).

In code (pre-fix): `internal/instance/event_gateway_internal_test.go` —
`startEventGate` polled only the token projection for readiness before the test
fired:

```go
require.Eventually(t, func() bool {
    for _, tk := range inst.GetTokens() {
        if tk.State == TokenWaitForEvent {
            return true
        }
    }
    return false
}, ...)
```

## §2 Root Cause Analysis

### §2.1 The park-to-register window in `checkNodeType`

The engine parks a waiting track in a deliberate three-step order
(`internal/instance/track.go:387-417`):

```go
t.updateState(TrackWaitForEvent)          // 1. state first — ProcessEvent only
                                          //    accepts while WaitForEvent, so a
                                          //    waiter that delivers synchronously
                                          //    ON registration is not refused
if t.instance.State() == Active {
    t.instance.emit(trackEvent{kind: evWaiting, ...})  // 2. loop records the wait
}
...
if err := t.instance.RegisterEvent(proc, d); err != nil {  // 3. hub waiters arm
```

Each step's position is load-bearing (steps 1→2 for the synchronous-delivery
race; steps 2→3 so an `evDeliver` can never beat the `waiting` record — `emit`
is a synchronous handoff to the single-writer loop). **The engine ordering is
correct.** But it means the **token projection flips to `WaitForEvent` at step
1, before the hub has any waiter (step 3)**.

### §2.2 The test's readiness signal watched step 1, the fire needed step 3

`startEventGate` treated "a token shows `TokenWaitForEvent`" as "the arms are
ready to receive". In the window between steps 1 and 3, `eh.PropagateEvent`
finds **no registered catcher** — and per ADR-006 v.2 §2.4 that is a benign
no-op drop (signals are not durable; verified behavior,
`TestSignalThrownIntoVoid`). When BOTH concurrent fires land in the window,
both are dropped, nothing ever wakes the track, and `requireCompleted` times
out. One fire in the window still passes (the other arm wins) — which is why
the test survives thousands of fast-machine iterations.

### §2.3 Why CI and not local

The window is a few instructions wide on an idle many-core box. Under `-race`
+ coverage instrumentation on the 4-vCPU shared runner, the parked track's
goroutine (between steps 1 and 3) is routinely descheduled long enough for the
test goroutine to poll, observe `WaitForEvent`, and fire twice. Local
`make ci` ran with the host's full core count — a **conditions** divergence
from CI, though steps and flags were already identical (the workflow calls the
same make targets).

### §2.4 Where the tests for this case are — and the sibling occurrence

The readiness race had no test — it *is* a test defect. The sweep for the exact
pattern: `grep -rln "TokenWaitForEvent" --include="*_test.go" internal/ pkg/`
→ 4 files, of which only `event_gateway_internal_test.go` also fires through a
hub (`PropagateEvent`) — the other three (`observer_test.go`,
`m3_projection_test.go`, `handle_internal_test.go`) assert projections without
hub fires.

The broader sweep (all 17 test files firing via `PropagateEvent`, each
inspected for its readiness idiom) found **one sibling**:
`pkg/thresher/signal_test.go` gated four fires with `time.Sleep(150ms)` —
three pre-fire "catcher parks" sleeps (`TestSignalCatchThrow`,
`TestSignalBroadcast`, `TestSignalSingleShotConsume`) and one inverse
"throw settled" sleep (`TestSignalThrownIntoVoid`). Same class, weaker guard:
probabilistic instead of racy-deterministic; not yet failed visibly, fixed
here (§3.2.2–§3.2.5). The hub-unit tests register waiters explicitly in the
test body (no window by construction); the remaining files fire on
mocks/fakes.

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. Reorder the engine: flip `TrackWaitForEvent` only after `RegisterEvent` | test unchanged | breaks the documented synchronous-delivery race (`track.go:387-391`): a waiter may deliver ON registration and `ProcessEvent` accepts only in `WaitForEvent`; engine change for a test problem | ❌ rejected |
| B. Fire-retry loop in the test (re-`PropagateEvent` until delivered) | small | masks the semantics under test (deferred choice on a *single* fire per arm), still nondeterministic, hides real delivery bugs | ❌ rejected |
| C. Readiness = hub registrations (count `RegisterEvent` through a wrapping producer) + parked token | deterministic; asserts the actual precondition ("a fire now reaches a live waiter"); reusable pattern | a test-only wrapper type | ✅ chosen |
| D. (companion, prevention) pin local `make ci`'s CPU budget to the runner's | the whole flake class becomes locally reproducible | slightly slower local ci | ✅ chosen alongside C |

### §3.2 Changes by file

#### §3.2.1 `internal/instance/event_gateway_internal_test.go` — registration-aware readiness

New `registrationCounter` wrapping the live `eventhub.EventHub` as the
instance's parent producer: delegates `RegisterEvent`/`UnregisterEvent`/
`PropagateEvent`, counting successful registrations (`atomic.Int32`).
`startEventGate` builds the instance over the wrapper and its readiness poll
becomes:

```go
// both arms registered at the hub — a fire now reaches a live waiter…
if rc.n.Load() < 2 {
    return false
}
// …and the gate's token is parked (the loop recorded the wait strictly
// before the registrations could run — evWaiting precedes RegisterEvent).
for _, tk := range inst.GetTokens() { ... }
```

Registration count ≥ 2 also implies the loop applied `evWaiting` (step 2
strictly precedes step 3 on the track goroutine, and the emit is a synchronous
handoff), so a post-readiness fire can neither miss the hub nor beat the
`waiting` record. The fired hub stays the returned `eh` — fires take the same
path as before.

#### §3.2.2 `internal/eventproc/eventhub/eventhub.go` + `waiters/signal.go` — the `SignalCatchers` readiness probe

The signal tests are black-box (`package thresher_test`) and the hub is
engine-internal, so the §3.2.1 wrapper cannot be injected there. Instead the
concrete hub gains a read-only probe — **deliberately NOT added to the
`eventproc.EventHub` interface** (no contract growth, no mock churn; callers
type-assert):

```go
func (eh *EventHub) SignalCatchers(name string) int   // Σ processors over signalIdx[name]
func (sw *signalWaiter) ProcessorCount() int          // the broadcast-set size
```

It counts **processors, not waiters**: a second instance catching the same
shared-id signal JOINS the existing waiter (`registerWaiter`'s
`AddEventProcessor` branch), so a waiter count under-reports live catchers —
empirically caught when the first probe version hung `TestSignalBroadcast`'s
two-catcher wait (§8.2 at landing). Lock order hub→waiter matches every
existing path (no inversion).

#### §3.2.3 `pkg/thresher/export_test.go` — the black-box bridge (new file)

The standard `export_test` pattern: `func SignalCatchers(th *Thresher, name
string) int` type-asserts the engine's hub and delegates; compiled only into
test binaries — the production API surface is unchanged.

#### §3.2.4 `pkg/thresher/signal_test.go` — four sleeps become deterministic gates

The three pre-fire sleeps become `waitSignalCatchers(t, th, "GO", n)`
(`require.Eventually` over the bridge; n = 1, 2, 1). The inverse sleep in
`TestSignalThrownIntoVoid` becomes the thrower handle's own `WaitCompletion`
— the throw fires during the thrower's run, so its completion proves the
signal was propagated (and correctly dropped) before the catcher exists.

#### §3.2.5 `internal/eventproc/eventhub/eventhub_signal_test.go` — probe unit test

`TestSignalCatchersCount`: 0 for an unknown name; distinct waiters count each;
a processor that JOINS an existing waiter counts (the case a waiter count
misses); per-name isolation; counts fall as catchers unregister. Direct unit
coverage in the hub's own package (cross-package runs don't attribute
coverage, and the diff gate measures the hub's changed lines).

#### §3.2.6 `Makefile` — `TEST_CPUS` CI-parity budget (prevention, §3.1-D)

```make
TEST_CPUS ?= 4
...
(cd $$dir && GOMAXPROCS=$(TEST_CPUS) $(GO) test -race -count=1 ...)
```

`test-all` (used by both local `make ci` and the workflow) now runs with
`GOMAXPROCS` pinned to the ubuntu-latest public-runner budget (4 vCPUs);
`GOMAXPROCS` also drives `go test`'s package parallelism (`-p`), so both knobs
sync. On the runner the pin is a no-op (4 = its cores) — one definition, both
sides identical. `make ci TEST_CPUS=` restores the host default.

## §4 Verification

Current coverage: the defect is in a test; the "regression test" is the
stress test itself, now deterministic.

### §4.1 Regression verification (mandatory)

| Check | Setup | Assertion |
|---|---|---|
| `TestEventGatewayConcurrentFiresStress` | `GOMAXPROCS=2` and `4`, `-race`, `-coverprofile`, `-count=5` (2,500 iterations each) | green — the exact conditions that exposed the window |
| the four `TestSignal*` tests (§3.2.4) | `GOMAXPROCS=4`, `-race`, `-count=20` | green — deterministic gates replace the sleeps |
| `TestSignalCatchersCount` (§3.2.5) | `-race` | probe semantics incl. the joined-waiter case |
| full `make ci` (with `TEST_CPUS=4`) | the synchronized budget | exit 0 end-to-end |
| every other `startEventGate` consumer in `event_gateway_internal_test.go` | same run | unchanged semantics, green |

### §4.5 Observability

None needed — no engine path changed; the fix is observable as CI stability.

## §5 Prevention

- **The pattern**: any event test that fires through a hub must gate readiness
  on the **hub registrations**, never on the token projection or a sleep —
  `TokenWaitForEvent` precedes `RegisterEvent` by design. Two reference
  implementations, by test level: white/instance-level —
  `registrationCounter` (a counting producer wrapper,
  `event_gateway_internal_test.go`); black/engine-level — the
  `SignalCatchers` probe via the `export_test` bridge
  (`waitSignalCatchers`, `signal_test.go`). Counting **catchers, not
  waiters** matters: a same-id catch joins an existing waiter.
- **The invariant, stated once**: *"token parked" is a track-state fact;
  "deliverable" is a hub fact; the projection is not a delivery guarantee.*
  A fire before registration is a correct no-op drop (ADR-006 v.2 §2.4,
  non-durable events).
- **The infra**: `TEST_CPUS` keeps local `make ci` conditions synchronized with
  the GitHub runner so this flake class reproduces before push, not after.

## §6 Regressions / side-effects

### §6.1 What may rely on the old behaviour

Nothing relies on the racy readiness. Pre-landing re-check for other
occurrences of the pattern (run at landing; initial sweep found only this
file):

```bash
grep -rln "TokenWaitForEvent" --include="*_test.go" internal/ pkg/   # readiness polls
grep -rln "PropagateEvent"   --include="*_test.go" internal/ pkg/    # hub fires
# intersection = candidates; each must gate on registrations, not tokens
```

A future test matching both greps without a registration gate re-opens the
class — cite this FIX in review.

### §6.2 Rollback path

Single-commit revert per change (test harness / Makefile) — independent and
trivially revertible.

### §6.3 Cross-team backlog

None (sole-maintainer project).

## §7 Related

- [ADR-001 v.6](../design/ADR-001-execution-model.md) — the single-writer loop
  whose `emit` handoff makes step 2→3 ordering reliable.
- [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) — the
  channel-park delivery model; the `updateState → evWaiting → RegisterEvent`
  sequence is its landing (SRD-027, one-shot).
- [ADR-006 v.2](../design/ADR-006-events-and-subscriptions.md) §2.4 — no-catcher
  no-op drop, the semantics that make the window lose events *correctly*.
- SRD-040 — the refactor branch this landed on (the failure surfaced on its PR
  CI run; the engine paths SRD-040 moved are not implicated — `track.go` was
  untouched by it).

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

### §8.1 Stages by commit (branch `refactor/instance-internal-structure`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| 1 | `dc95cd2` | this doc (Draft) | — |
| 2 | `73636a1` | §3.2.1 event-gate harness (`registrationCounter` readiness) | stress ×5,000 green under the exposing CI conditions (GOMAXPROCS=2/4, `-race`, coverage) |
| 3 | `884a7a7` | §3.2.2–§3.2.5 signal readiness (`SignalCatchers` + `ProcessorCount` + bridge + four sleep→gate rewires + unit tests in each owning package) | `TestSignal*` ×20 `-race` green; probe + waiter methods at 100% coverage |
| 4 | `57aff73` | §3.2.6 `TEST_CPUS=4` CI-parity budget | full `make ci` exit 0 under the pinned budget |
| 5 | `8fbea18` | companion hygiene: gofmt drift in 10 untouched files (whitespace-only, fixed on sight) | build + owning-package tests green |

Full-stack gate: `make ci` exit 0; diff-coverage **97.2%** of 782 changed lines
(min 95), every FIX-021-touched function at 100%.

### §8.2 Empirical findings — where reality diverged from the §3 draft

- **Waiters under-count catchers.** The first probe version counted
  `signalIdx` *waiters* and deterministically hung `TestSignalBroadcast`'s
  two-catcher wait: a second instance catching the same shared-id signal JOINS
  the existing waiter (`registerWaiter`'s `AddEventProcessor` branch) instead
  of creating one. The probe became catcher-counting (`ProcessorCount` summed
  per waiter) — the §3.2.2 design as landed. The hang was itself a
  deterministic readiness gate doing its job: the old sleep would have hidden
  the mistake.
- **Cross-package coverage attribution bit twice.** Statements in
  `waiters/signal.go` and `eventhub.go` exercised only through
  `pkg/thresher`'s tests attribute no coverage to their own packages, so the
  diff gate saw `ProcessorCount` at 0% — each owning package needed its own
  unit test (`TestSignalWaiterProcessorCount`,
  `TestSignalCatchersFallbackCount`). Same lesson as SRD-040's runtimevars
  observation: the gate measures per-package, plan unit tests per owning
  package.
- **Amend targets HEAD, not "the commit you mean".** Folding the two coverage
  tests via `--amend` landed them in the tip (gofmt) commit instead of stage 3;
  recovered by `reset --soft` to stage 2 and rebuilding stages 3–5 with correct
  scopes (all five commits were verified local-only first, per the
  amend-after-push rule).
- **A repo-wide `gofmt -l` found 10 drifted files** the lint config does not
  police — the §8.1 stage-5 hygiene commit; a `gofmt`-enforcing linter setting
  is a candidate improvement (backlog, §8.3).

### §8.3 Backlog (out of FIX-021 scope)

- `pkg/thresher/thresher_events_test.go` carries several short settle-sleeps
  (10–50ms, post-fire/timer contexts). Not audited in depth here — they did
  not match the fire-readiness class on inspection, but deserve the same
  deterministic-gate treatment in a future sweep.
- Consider enabling a `gofmt`/`gofumpt` linter in `.golangci.yml` so
  formatting drift fails `make lint` instead of accumulating silently.

## §9 Open questions

None.
