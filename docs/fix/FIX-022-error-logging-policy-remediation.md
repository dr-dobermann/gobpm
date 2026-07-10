# FIX-022 «Bring error handling and logging up to the ADR-022 policy»

**Type:** FIX (one-shot remediation; not rewritten after landing).
**Status:** Draft v.1 (2026-07-10, branch `fix/silent-error-discards`, not yet implemented).
**Date:** 2026-07-10.
**Author:** Ruslan Gabitov.
**Branch:** `fix/silent-error-discards` (the discard sweep that motivated the policy; the log audit rides along).
**Implements:** [ADR-022 v.1](../design/ADR-022-error-propagation-and-logging-policy.md) — the error-propagation and logging policy this FIX brings the codebase up to.
**Upstream:** [ADR-002 v.2](../design/ADR-002-extension-architecture.md) (the `observability.Logger` seam), [ADR-013 v.1](../design/ADR-013-instance-observability.md) (the ObsEvent stream kept separate from logs).

**Grounded in:** the full ADR-022 remediation census (this branch, HEAD `793b2aa`) — 11 silent discards (A), 1 log-beside-return (B), 4+2 level misfits (C), ~25 attribute-key drift sites (D), 4 silent handling boundaries (E), across 16 files.

---

## §1 Symptoms

The code predates the policy, so it drifts from ADR-022 in five measurable ways. None is a live crash — they are diagnosability defects (the class ADR-022 §2.6 calls "worse than noise: silence is undiagnosable").

### §1.1 Silent error discards (ADR-022 §2.1/§2.3)

Eleven production sites drop an error with a bare `_ =`. The exemplar, on the message waiter's fire path (`internal/eventproc/eventhub/waiters/message.go`):

```go
_ = mw.hub.WaiterFired(mw.eDef.ID()) // terminal → the hub removes it
return err
...
_ = mw.hub.WaiterFired(mw.eDef.ID()) // the hub removes iff terminal
return nil
```

A hub-bookkeeping failure vanishes; the waiter's fate diverges from the hub's registry with no record. Full inventory in §3.2 (census A1–A11).

### §1.2 A failure reported twice (ADR-022 §2.1)

`pkg/model/activities/service_task.go:317` logs a ServiceTask timeout at `Warn` *and* returns an error that the instance-fault boundary logs again — the same failure, two records, neither complete (census B1).

### §1.3 Wrong levels for the reader (ADR-022 §2.4)

`internal/instance/activation.go:54` logs "instance failing" — the whole-instance fault, ADR-022's canonical `Error` example — at `Warn`. Plus judgment sites (census C).

### §1.4 Attribute-key drift (ADR-022 §2.5)

The same entity is logged under different keys: `instance` (5×) vs `instance_id` (3×); `track`/`node`/`message`/`task` vs their canonical forms; a `key` attr holding correlation *values* while `correlation_key` holds *names* (census D, ~25 sites).

### §1.5 Silent handling boundaries (ADR-022 §2.3)

Four goroutine tops / fault paths handle a failure with no record — most seriously `internal/instance/loop.go:445` (`spawnForks`), where a track-build error stores `lastErr` directly, **bypassing `Instance.fail`** — the instance terminates with **no log at all** (census E1–E4).

## §2 Root Cause Analysis

### §2.1 There was no policy until ADR-022

The codebase leaned the right way by convention (visible-by-default logging, `Warn` for best-effort degradation, `Debug` for flow) but nothing was written down, so each subsystem re-decided and drift accumulated. ADR-022 v.1 is now the contract; this FIX reconciles the code to it. The RCA per category:

- **Discards (§1.1):** `_ = f()` is exactly the idiom that *silences* `errcheck`, so the linter never flagged them; no house rule forbade the pattern (now ADR-022 §2.3(3) does).
- **Duplicate / level / key (§1.2–§1.4):** with no §2.4 level contract and no §2.5 vocabulary, "log it" meant "log it however this file already does," and the flood/synonym drift followed.
- **Silent boundaries (§1.5):** goroutine tops had no "handler of last resort" discipline; a failure with nobody above to return to simply fell off the end.

### §2.2 Where the tests for this are

None — these are observability defects, invisible to behavior tests by construction (a swallowed error changes nothing a passing assertion checks). The remediation makes the error paths *reachable and asserted* for the first time (§4).

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Decision |
|---|---|
| A. Bulk `sed` — mechanically rewrite every `_ =` to `if err != nil { log }` and every key | ❌ rejected: several discards must *propagate* (behavior change), levels need judgment, and a blanket log next to every error rebuilds the flood ADR-022 forbids. |
| B. Two FIXes — discards (behavioral) vs log-audit (mechanical) | ❌ rejected: census A and D coincide on the same lines (a discard that becomes a log must carry canonical keys immediately); splitting double-touches those lines and forces a rebase. |
| C. One FIX, sliced by **layer** into 5 milestones — each fixes one layer's whole error+log story atomically | ✅ chosen. Each milestone is independently committable and testable; a milestone that proves oversized in its per-milestone plan gets peeled out then, not now. |

### §3.2 Changes by file — grouped into the five milestones

Each row is a census site; the remediation is the ADR-022 classification.

#### M1 — eventproc layer (`internal/eventproc/eventhub` + `waiters`)

The dense, behavior-changing core.

##### §3.2.1 `internal/eventproc/eventhub/waiters/message.go` — A2, A3, A4, E1

- **A2 (:310)** / **A3 (:326)** — `_ = mw.hub.WaiterFired(...)` on the two failure paths where an `err` is already in flight → `return errors.Join(err, mw.hub.WaiterFired(mw.eDef.ID()))` (ADR-022 §2.2 join).
- **A4 (:332)** — the success path. **Not** a plain `return WaiterFired(...)`: the caller `runMessageService` (:288) treats *any* non-nil return as a terminal waiter failure, and a bookkeeping-report failure must not terminate a healthy waiter. → classify as **best-effort log at `Warn`** (§2.3(2)) with `waiter_id`, `message_name`, `error`; keep `return nil`.
- **E1 (:288)** — `runMessageService` returns on a terminal error with no record → add an `Error` log ("message waiter terminally failed", `waiter_id`, `message_name`, `error`) at the goroutine top (§2.3(1)/§2.4).

##### §3.2.2 `internal/eventproc/eventhub/waiters/timer.go` — A5, E2

- **A5 (:384)** — `_ = tw.hub.WaiterFired(...)` during terminal-cycle cleanup, one frame below a caller that swallows everything → **log at `Warn`** (§2.3(2)), the log is the handling.
- **E2 (:332)** — `runTimerService` swallows both the "timer completed" control-flow sentinel and real delivery failures. → **discriminate the sentinel** (`errors.Is`): a real failure logs `Error` (`waiter_id`, `error`); the completion sentinel is silent (or `Debug`). The sentinel-error design itself is a smell → §8.3 backlog, not refactored here.

##### §3.2.3 `internal/eventproc/eventhub/eventhub.go` — A1

- **A1 (:525)** — `_ = w.Process(eDef)` in `broadcastSignal`. `signalWaiter.Process` always returns nil and logs per catcher itself, so the discard is inconsequential, but the bare `_ =` is forbidden → `if err := w.Process(eDef); err != nil { Debug(...) }` (defensive; keeps the :521 comment).

#### M2 — instance layer (`internal/instance`)

##### §3.2.4 `internal/instance/loop.go` — E3 + D (keys)

- **E3 (:447)** — `spawnForks` does `ls.inst.lastErr.Store(&err)` directly, bypassing the single logging fault path → route through `ls.inst.fail(err)` (restores the ctx-cancel *and* the one fault record). Every other fault site already goes through `fail()` — `boundary_watch.go:96` (arm failure) and `failFromTrack` (loop.go:435) — so this makes `spawnForks` consistent, not novel.
- **D** — two log sites: the "track event" Debug (:111/:113) — `instance`→`instance_id`, `track`→`track_id`; and the "synchronizing join fired" Debug (:648–651) — `instance`→`instance_id`, `node`→`node_id`, `survivor`→`survivor_track_id`. `merged` there is `len(merged)` — a genuine count, free-form, keep (§2.5).

##### §3.2.5 `internal/instance/activation.go` — C (level) + D

- **C misfit (:54)** — "instance failing" `Warn` → **`Error`** (ADR-022 §2.4 canonical example). Key `instance`→`instance_id`; raw `err`→`err.Error()` (§2.5).

##### §3.2.6 `internal/instance/correlation.go` — C/D (error attrs)

- **(:146)** Warn omits the `DeriveKey` error → add `error`. **(:212)** Debug "extend receiver subscription failed" omits the `AddEventKey` error → add `error`; consider `Warn` (real failure, §2.3(2) permits Debug — keep Debug with the error content, judgment noted).

##### §3.2.7 `internal/instance/boundary_watch.go` — A6

- **A6 (:119)** — `_ = ls.inst.UnregisterEvent(...)` in the void `disarmBoundaries` (loop goroutine). An idempotent miss is an expected no-op → `if err := ...; err != nil { Debug(reason) }` (§2.4 corollary), keeping the :118 comment; a *non*-miss error is now visible.

##### §3.2.8 `internal/instance/tasks.go` — D

- Keys `instance`→`instance_id` (:239, :305).

#### M3 — thresher layer (`pkg/thresher`)

##### §3.2.9 `pkg/thresher/thresher.go` — A7, A8, C/D

- **A7 (:748)** / **A8 (:779)** — the best-effort rollback loops discard `UnregisterEvent` / `RegisterPersistentEvent` while a teardown error is in flight → `errors.Join` the rollback failures into the returned error (§2.2), so a partial-rollback failure is not silent.
- **D (:376)** — the hub-run-loop `Error` passes a raw `err` → `err.Error()`.
- **D (:831, :845)** — the instantiation-decision Debug logs a `key` attr holding the derived correlation **value** (`msg.CorrelationKey`) → `correlation_value` (§2.5 name/value split), **not** `correlation_key`.

##### §3.2.10 `pkg/thresher/instance_starter.go` — C (level) + D

- **C (:59)** — the parallel-start "not instantiating" Warn returns `nil` on a standard-mandated (BPMN §10.6.6) **expected no-op** → **`Debug`** with the drop reason (§2.4 corollary). Keys (:64/:77/:78): `message`→`message_name`; `key_name` (the key **name**)→`correlation_key`; `key` (the derived **value**)→`correlation_value` (§2.5 split).

#### M4 — tasks / messaging layer (`pkg/tasks/localdispatcher`, `pkg/messaging/membroker`)

##### §3.2.11 `pkg/tasks/localdispatcher/localdispatcher.go` — C (judgment) + D + E4

- **D** — `report_error`→`error` where it is the sole error in the record (:651, :754; keep the two-error record at :642 with a named second key per §2.5); `prev_worker`→`worker_id` (:238); `attempt`/`attempts` — distinct meanings (current-attempt vs total-exhausted), keep both but confirm the labels read clearly.
- **C judgment (:238)** — "expired job lock reclaimed" (a worker missed its deadline) at Debug → consider `Warn`; decide at implementation.
- **E4 (:621)** — `runWorker`'s silent `FetchAndLock`-error exit is OK today (ctx-only) but fragile → a one-line comment pinning the ctx-only invariant, or a `Debug` exit line.

##### §3.2.12 `pkg/messaging/membroker/membroker.go` — D

- Keys `name`→`message_name` (:120,177,185,198,205,235); `key` holds `msg.CorrelationKey` (the routing **value**) → `correlation_value` (:103,177,185,198,205), §2.5 split. `keys` (:235) and `drained` (:103) are counts — keep. The cap-drop Warn (:273) is already once-guarded — keep.

#### M5 — model / interactor layer (`pkg/model/flow`, `pkg/model/activities`, `pkg/interactor/console`)

The logger-less carve-out (ADR-022 §2.3) applies here.

##### §3.2.13 `pkg/model/flow/sequenceflow.go` — A9, A10

- **A9 (:190)** / **A10 (:192)** — `_ = src.AddFlow(...)` / `_ = trg.AddFlow(...)` in `CloneFlow`, which already returns `(*SequenceFlow, error)` → `if err := ...; err != nil { return nil, err }`. No logger needed; propagation is the remediation (no behavior change if the "cannot fail here" invariant holds — and if it ever doesn't, the error now surfaces instead of vanishing).

##### §3.2.14 `pkg/model/activities/service_task.go` — B1 + D

- **B1 (:317)** — kill the Warn beside the timeout `return` (§2.1). Fold its unique nuance ("operation goroutine may still be running") into the returned error's message / an `errs.D`, so the one record at the fault boundary is complete. The Warn's `task` key (which held `st.Name()`) is dropped with it; the returned `errs.New` (:321) carries `service_task_id` via `errs.D` and the name in its message string — **add `errs.D("service_task_name", …)`** so the surviving record carries the name as a canonical attr too.

##### §3.2.15 `pkg/interactor/console/console.go` — A11

- **A11 (:110)** — `_, _ = fmt.Fprintf(d.w, ...)` in the best-effort progress writer. The console driver *is* an output channel with no logger (ADR-022 §2.3 carve-out) → keep a **why-comment** (already present at :107) but make the ignore explicit rather than a bare discard, e.g. assign-and-comment or a named `//nolint`-free helper; no behavior change.

## §4 Verification

### §4.1 Regression tests (mandatory) — the newly-reachable error paths

| # | Test | Asserts |
|---|---|---|
| §4.1.1 | message-waiter terminal fault (M1) | a failing `WaiterFired` / `ProcessEvent` now surfaces: `errors.Join` carries both; `runMessageService` logs `Error` (log-capture handler) |
| §4.1.2 | timer real-failure vs sentinel (M1) | a real delivery error logs `Error`; the "completed" sentinel does not (`errors.Is` discrimination) |
| §4.1.3 | `spawnForks` fault (M2) | a track-build failure routes through `Instance.fail` — instance reaches `Terminated`, `LastErr` set, one "instance failing" `Error` record |
| §4.1.4 | thresher rollback join (M3) | a rollback `UnregisterEvent`/`RegisterPersistentEvent` failure is joined into the returned error, not dropped |
| §4.1.5 | sequenceflow clone propagation (M5) | `CloneFlow` returns an `AddFlow` error instead of discarding it (inject a rejecting source/target) |

### §4.2 Level / key normalization — verified by the existing suite + targeted capture

The re-leveling and re-keying are behavior-preserving for control flow, so the **existing `-race` suite staying green** is the primary guard. Where a level or key change is material (activation.go Warn→Error; the `instance`→`instance_id` class), a focused log-capture assertion (the `capHandler` pattern already used in `thresher/options_test.go`) pins the new level and key so a future regression is caught.

### §4.3 Observability

The fix *is* observability: after it, every error is either returned or logged exactly once, at the right level, under canonical keys — grep-verifiable (no bare `_ =` on error calls; no `"instance"`/`"track"`/`"node"`/`"message"` log keys outside the vocabulary).

## §5 Prevention

- **Doc comments**: every remediated site whose behavior changes (join/return of a previously-swallowed error) gets a comment naming *why* it now propagates.
- **Style-sweep house rules** (ADR-022 §5, `/check-style`): flag bare `_ =` on error-returning calls; flag log-and-return; check log keys against the §2.5 vocabulary. Applied going forward.
- **Lint tightening (backlog)**: with discards remediated, `errcheck`'s `check-blank` (forbid `_ =` on error returns) becomes adoptable without a red wall — §8.3.
- **Reference docs**: none affected (no public API contract changes except the intended error-surfacing).

## §6 Regressions / side-effects

### §6.1 What relied on the old (silent) behaviour

By construction, nothing *depends* on a swallowed error — but surfacing one **is** the behavior change, per site:

- **A2–A4 / A7–A8 (join/return)**: a hub-bookkeeping or rollback failure that used to vanish now reaches a caller / a log. A4 specifically is classified log-not-return precisely so it does **not** terminate a healthy waiter (the caller's any-error-is-terminal contract).
- **E3 (spawnForks → fail)**: a track-build failure now cancels sibling tracks (via `fail`'s ctx-cancel) and logs — previously it stored `lastErr` and let the instance settle less deterministically. This is the *correct* fault behavior (matches every other build-failure site); verified by §4.1.3.
- **Level changes** (activation Warn→Error, instance_starter Warn→Debug) shift what a level-filtered handler emits — intended, and the point of the audit.

### §6.2 Rollback path

Per-milestone, independently revertable (M1…M5 are separate commits). No migration, no data.

### §6.3 Cross-team backlog

None (sole-maintainer project). Out-of-scope follow-ups → §8.3.

## §7 Related

- [ADR-022 v.1](../design/ADR-022-error-propagation-and-logging-policy.md) — the policy this implements; §7 rollout step 2 (discards) + step 3 (log audit) are both this FIX.
- [ADR-002 v.2](../design/ADR-002-extension-architecture.md), [ADR-013 v.1](../design/ADR-013-instance-observability.md) — the logger seam and the ObsEvent stream.
- FIX-021 (this session, merged) — surfaced the same "silence is undiagnosable" class in test harnesses; ADR-022 generalized it to production.
- Graduates the `docs/backlog.md` "silent-error-discard remediation (repo-wide)" entry.

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

> ⚠️ TODO: fill AFTER landing.

### §8.1 Stages by commit (branch `fix/silent-error-discards`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| doc | `1d84a2e` (ADR-022) + `<sha>` (this) | policy + this FIX | — |
| M1 | `<sha>` | eventproc layer | §4.1.1–§4.1.2 |
| M2 | `<sha>` | instance layer | §4.1.3 |
| M3 | `<sha>` | thresher layer | §4.1.4 |
| M4 | `<sha>` | tasks/messaging layer | — (keys/levels) |
| M5 | `<sha>` | model/interactor layer | §4.1.5 |

### §8.2 Empirical findings — where reality diverged from the §3 draft

> ⚠️ TODO after landing.

### §8.3 Backlog (out of FIX-022 scope)

- The timer "completed" **sentinel-error** design (a control signal sharing the `error` channel) — replace with a proper terminal-state return; a small refactor of its own.
- **`errcheck check-blank`** lint setting once the codebase is clean of `_ =` error discards.
- A **`gofmt`/`gofumpt`-enforcing** linter setting (carried from FIX-021 §8.3) so formatting drift fails `make lint`.
- The §2.5 vocabulary candidates that stayed **out** (count/descriptive attrs) — revisit only if a real need to canonicalize a count arises.

## §9 Open questions

None. The four cross-cutting decisions the census surfaced are resolved: the vocabulary additions and the `correlation_key`/`correlation_value` split are in ADR-022 v.1 §2.5; the logger-less carve-out is in §2.3; the timer sentinel is discriminated here and its refactor deferred to §8.3.
