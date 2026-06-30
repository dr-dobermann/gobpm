# FIX-013 — Thresher registry & starter lifecycle hardening

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-06-30 |
| Owner | Ruslan Gabitov |
| Related | [ADR-019 v.1 Definition versioning](../design/ADR-019-definition-versioning.md), [SRD-031.B Registry concurrency](../srd/SRD-031.B-registry-concurrency.md), [SRD-031.A Definition versioning](../srd/SRD-031.A-definition-versioning.md), [SRD-032 Snapshot starts & instance scope](../srd/SRD-032-snapshot-starts-instance-scope.md), [ADR-006 v.2 §2.5 Waiter lifecycle](../design/ADR-006-events-and-subscriptions.md), [FIX-002 Event-start registration lifecycle](FIX-002-event-start-registration-lifecycle.md) |

One-shot remediation of five defects in the `pkg/thresher` process registry and
instance-starter lifecycle, surfaced by
`docs/audit/code-review-third-pass-2026-06-29.md` (§2.4 P2, §2.6 P2, §2.7 P2,
§3.9 P3) and a build-tooling note: a stale `RegisterProcess` godoc, a missing
rollback when starter registration fails at `Run`, register/unregister loops
that abort mid-way leaving the hub partially wired, a register/unregister
**TOCTOU** that can orphan a live starter, and a swallowed `EventHub.Run` error.

**Relationship to SRD-031.B.** SRD-031.B (the registry-concurrency discipline,
ADR-019 §2.7) already landed: engine `state` is an atomic, lock-free
`atomic.Uint32` with transitional `Starting`/`Stopping` values; `Run`/`Shutdown`
are CAS-driven; every `t.m` critical section is confined to a `…Locked` helper
that returns plain data, and **the EventHub is deliberately touched only with
`t.m` released** (SRD-031.B FR-4 / §4.5). The third-pass audit was taken against
that landed code (same date), so these five are **residuals SRD-031.B did not
target**: it killed the FIX-002 RC2 *re-entrancy* (a goroutine reading `State()`
under `t.m`), not the cross-operation *interleaving* of §1.4, nor the
`registerAllStarters` failure path of §1.2 (CAS rollback covers only
`hub.Start`). Every fix here stays inside SRD-031.B's contract: the new per-key
lock (§1.4) is **not** `t.m` — `State()` does not take it, so it introduces no
RC2 vector — and no fix holds `t.m` across an EventHub call.

> Two neighbouring third-pass findings are **out of scope** here: the
> `ParallelEvents`-instantiating-gate-without-a-CorrelationKey double-instantiation
> (third-pass §2.5) is deferred to **FIX-014** (it spans the gateway model and the
> thresher launch path and needs its own design); and the "snapshots map / missing
> `UnregisterProcess`" leak (architecture-audit §2.5) is **already fixed** by
> ADR-019 (`UnregisterProcess`/`UnregisterVersion` exist; there is no `snapshots`
> map; the version counter resets).

## 1. Symptoms

- **1.1 (P2, doc) `RegisterProcess` godoc contradicts its behaviour.** The
  header comment (`thresher.go:496-497`) reads *"Re-registering an
  already-registered process is idempotent (the first registration wins)."* —
  but ADR-019 made per-call versioning **intended**: re-registering a key mints
  a NEW version (`snapshot.New` + `appendVersionLocked` → `nextVersion++`), and
  the inline comment (`:526-527`) already says so. The godoc is stale pre-ADR-019
  text and misleads callers about the registry's core contract.
- **1.2 (P2) `Run` leaves the engine `Started` when starter registration
  fails.** `Run` rolls the state back to `NotStarted` if `eventHub.Start` fails
  (`thresher.go:308-316`), but if `registerAllStarters()` fails afterwards
  (`:328-333`) it returns an error while the state is already `Started`
  (`:323`) and the hub goroutine is running — an inconsistent half-started
  engine that the asymmetry with the `Start` path makes plain.
- **1.3 (P3) Starter (un)register loops abort mid-way, leaving the hub
  partially wired.** `registerStarters` (`:651-664`) and `unregisterStarters`
  (`:669-682`) `return` on the first failing element. In the latest-supersedes
  path (`:553-561`) a failure on the 2nd of N starters leaves the 1st applied
  and the rest not — registry and hub disagree with no repair.
- **1.4 (P2, race) Register/Unregister TOCTOU can orphan a live starter.** All
  three registry mutators commit the registry change under `t.m`, **release the
  lock, then** do the hub subscription work unlocked (the `t.m`-across-a-hub-call
  deadlock class FIX-002 RC2 forbids): `RegisterProcess` (`:543` then
  `:549-562`), `UnregisterVersion` (`:589` then `:600-610`), `UnregisterProcess`
  (`:634` then `:641-643`). Between the two sections a concurrent operation on
  the same key can interleave: e.g. `RegisterProcess` appends a version (now
  observable via `Registrations()`), a concurrent `UnregisterVersion` removes it
  from the registry and `unregisterStarters` finds nothing on the hub yet, then
  `RegisterProcess` subscribes a starter for a registration no longer in the
  registry — an orphaned live subscription.
- **1.5 (minor) `EventHub.Run` error is swallowed.** `Run` launches the hub
  loop as `go func() { _ = t.eventHub.Run(t.ctx) }()` (`:318-320`); a genuine
  hub-loop error (as opposed to the expected `context.Canceled` on shutdown)
  vanishes silently.

## 2. Root-cause analysis

- **1.1**: the godoc predates ADR-019 and was never updated when per-call
  versioning replaced idempotent dedup; the inline comment was updated, the
  header was not.
- **1.2**: the rollback was added for the `eventHub.Start` failure path only;
  the later `registerAllStarters` failure path was left returning the error
  without undoing the `Started` transition.
- **1.3**: the loops were written for the happy path; a per-element hub failure
  was assumed not to happen, so neither accumulates nor rolls back.
- **1.4**: correctness rests on a single `t.m` critical section for the
  registry, but the hub work is deliberately moved outside `t.m` (FIX-002 RC2),
  splitting one logical operation into two unsynchronised critical sections with
  no per-key serialisation between them.
- **1.5**: the goroutine wrapper discards the return value; only `context.Canceled`
  is expected, but any other error is lost.

## 3. Solution

### 3.1 Considered alternatives
- **1.4 — hub-pending flag under `t.m`** (mark the registration pending so a
  concurrent remove defers): rejected — it threads a new state field through the
  registry and every mutator, and the "defer/cancel" handshake is subtle. The
  per-key lock is a smaller, more local change.
- **1.4 — reconcile the hub from the authoritative registry after every
  mutation**: rejected for now as the largest change (a full idempotent
  add/remove reconcile pass); the per-key lock removes the interleaving with far
  less surface. (Reconcile remains the future option if the registry grows more
  concurrent operations.)
- **1.4 — chosen: per-key serialisation lock.** A per-process-key mutex
  (distinct from `t.m`) held across the **whole** register/unregister operation
  for that key — registry mutation *and* hub work — so two operations on the
  same key cannot interleave. Lock order is per-key (outer) → `t.m` (inner,
  brief, inside the `…Locked` helpers) → hub work (still under the per-key lock,
  never under `t.m`). Consistent ordering (per-key always outer) means no
  deadlock, and the hub call is never made under `t.m`, preserving FIX-002 RC2.
- **1.2 — treat a starter-registration failure at `Run` as non-fatal (log and
  continue)**: rejected — a process that cannot auto-start is a real failure the
  caller must see; returning the error is right, but the state must be rolled
  back to match.
- **1.2 — move `registerAllStarters` *before* `Store(Started)` (run it while
  `Starting`) so a failure rolls back `Starting → NotStarted` like the
  `hub.Start` path**: rejected — during the `Starting` window a concurrent
  `RegisterProcess` sees `State() != Started` and defers its hub work to
  `registerAllStarters` (SRD-031.B gating), but that process may have committed
  to the registry *after* `latestStartersLocked` snapshotted, so its starter is
  neither registered by `Run` nor by `RegisterProcess` — a new orphan. Keeping
  `Store(Started)` before `registerAllStarters` preserves the existing gating;
  the fix rolls back *from* `Started` on failure (§3.2.2).

### 3.2 Per-site changes
- **3.2.1** `thresher.go:496-497` — rewrite the `RegisterProcess` godoc to the
  ADR-019 contract: re-registering a key mints a new version and the **latest**
  version supersedes for auto-start (no longer "idempotent / first wins"). Doc
  only.
- **3.2.2** `thresher.go` `Run` (`:328-333`) — on `registerAllStarters()`
  failure, roll back the already-published `Started` transition before
  returning: cancel the engine context (`t.engineCancel`, stopping the hub
  goroutine) and `t.state.Store(uint32(NotStarted))`, so a half-started engine
  is never left observable and a retry stays possible. With §3.2.3's
  all-or-nothing `registerStarters`, a failed `registerAllStarters` has already
  unwound its own partial subscriptions, so this rollback only has to undo the
  lifecycle transition. (Rolling back *from* `Started` rather than reordering
  `registerAllStarters` into the `Starting` window keeps SRD-031.B's
  RegisterProcess gating intact — see §3.1.)
- **3.2.3** `thresher.go` `registerStarters` / `unregisterStarters`
  (`:651-682`) — make each loop all-or-nothing: on a per-element failure, roll
  back the elements already applied in this call (unsubscribe the ones just
  subscribed / re-subscribe the ones just removed) before returning the error,
  so a partial application never persists.
- **3.2.4** `thresher.go` — add a per-key lock manager (a `keyLock(key string)
  *sync.Mutex`, backed by a small mutex-guarded map) and acquire it at the top
  of `RegisterProcess`, `UnregisterVersion`, and `UnregisterProcess` (keyed by
  the process key), held for the whole method. This serialises register vs.
  unregister for a given key so the §1.4 interleaving cannot occur. The per-key
  lock is a **new lock distinct from `t.m`** that `State()` never acquires, so it
  adds no RC2 re-entrancy vector; `t.m` is still taken only briefly inside the
  `…Locked` helpers, never across a hub call — SRD-031.B's lock contract is
  untouched. Lock order is per-key (outer) → `t.m` (inner); no path takes the
  per-key lock while holding `t.m`, so the two cannot deadlock.
- **3.2.5** `thresher.go:318-320` — log a non-`context.Canceled` `EventHub.Run`
  error instead of discarding it:
  `if err := t.eventHub.Run(t.ctx); err != nil && !errors.Is(err, context.Canceled) { t.cfg.logger.Error("event hub run failed", "error", err) }`
  (the engine logs via `t.cfg.logger`).

## 4. Verification

### 4.1 Tests
| Test | Asserts |
|---|---|
| `TestRegisterProcessGodocMatchesVersioning` *(or doc-review)* | re-registering a key returns a new version (v2) and the latest supersedes — pins the behaviour the corrected godoc now describes |
| `TestRunRollsBackWhenStarterRegistrationFails` | with a hub stub that fails `RegisterPersistentEvent`, `Run` returns an error AND leaves the engine `NotStarted` (re-runnable), not `Started` |
| `TestRegisterStartersRollsBackOnPartialFailure` | a starter set whose k-th registration fails leaves **no** starter subscribed (the first k-1 are rolled back) |
| `TestRegisterUnregisterNoOrphanUnderRace` (`-race`) | concurrent `RegisterProcess`/`UnregisterVersion` on the same key never leave an orphaned hub subscription (final hub state matches the registry's latest) |
| `TestEventHubRunErrorLogged` | a non-context `EventHub.Run` error is surfaced to the logger (observed via a capturing logger), not swallowed |

## 5. Prevention
The per-key lock turns the register/unregister sequence into one serialised
critical section per key, so the TOCTOU class cannot reappear by future edits to
the hub-work ordering. The all-or-nothing loops and the `Run` rollback make
partial-failure states unrepresentable rather than relying on the happy path.

## 6. Regressions
No public API signatures change. The per-key lock adds a brief serialisation of
register/unregister **per key** (independent keys stay concurrent); the hub call
is still made outside `t.m`, so the FIX-002 RC2 deadlock avoidance is preserved.
The `Run` rollback changes a failure-path post-condition (engine ends
`NotStarted` instead of `Started`) — the documented re-runnable contract. The
godoc change is behaviour-neutral.

## 7. Related
ADR-019 v.1 (definition versioning — the per-call versioning and
latest-supersedes lifecycle the registry implements; §1.1's godoc must match it).
SRD-031.B (registry-concurrency discipline — the atomic-state / CAS-lifecycle /
`…Locked`-helper design these residuals sit on; §1.2 and §1.4 are framed against
it). SRD-031.A (the versioning half — the per-call versioning behaviour §1.1
documents). SRD-032 (snapshot starts & instance scope — the `[]*instanceStarter`
these register/unregister loops consume is derived by `scanInstantiatingStarts`
from the precomputed `Snapshot.InstantiatingStarts`; FIX-013 changes the loops'
failure handling, not the derivation, which SRD-032 owns). ADR-006 v.2 §2.5
(waiter/hub lifecycle — the EventHub the starters subscribe to). FIX-002 (the RC2 lock-discipline this FIX must not break — the hub
call stays outside `t.m`). The deferred `ParallelEvents`-no-CorrelationKey
finding (third-pass §2.5) is reserved for **FIX-014**.

## 8. Implementation summary
*(filled at landing.)*

## 9. Open questions
None.
