# FIX-038 — engine locks held across host calls, and registrations that are silently lost

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Draft.
**Date:** 2026-08-08.
**Author:** Ruslan Gabitov.
**Branch:** `fix/audit-round2` — the confirmed defect track of the 2026-08-08 package audit.
**Upstream:** [ADR-002 v.2](../design/ADR-002-extension-architecture.md) §4.2 (the host-extension boundary these calls cross), [ADR-006 v.4](../design/ADR-006-events-and-subscriptions.md) (the hub's waiter registry), [ADR-013 v.2](../design/ADR-013-instance-observability.md) §5 (the observer stream a rebuild must survive), [ADR-033 v.4](../design/ADR-033-persistence-and-state.md) §2.8 (the incarnation fence recovery claims under).

**Grounded in (internal artifacts, verified at `c34fcab`):**
- `internal/scope/scope.go:113-139` — `SnapshotAt` calls `namesFrom` (`:309` locks) then `GetData` per name (`:94` locks); `internal/instance/track.go:1496` — the snapshot runs "on the track goroutine" and "commits bypass the loop".
- `internal/scope/scope.go:340-360` — `GetDataByID` resolves by iterating a map.
- `internal/eventproc/eventhub/eventhub.go:243` — `registerWaiter` holds `eh.m` through `:284` `w.Service(ctx)`; `waiters/message.go:222` — that calls `MessageBroker().Subscribe`.
- `internal/eventproc/eventhub/eventhub.go:397-440` — `UnregisterEvent` reads under `RLock`, releases, then check-and-removes.
- `pkg/thresher/recovery.go:71-73` — `if saveErr := repo.Save(...); saveErr != nil { return nil }`.
- `pkg/thresher/thresher.go:1061-1083` — `RegisterProcess`'s supersede-then-register sequence.
- `pkg/thresher/tasks.go:219-228` — `t.m` held across `rec.eligible.Authorize(...)`.
- `pkg/thresher/observer.go:85` — `h.current().AddObserver(...)`.

---

## 1 Symptoms

Confirmed findings of the first `/audit-package` sweep — an external reviewer
over whole packages, doc-blind, every finding then verified against the source
([the audit record](../audit/package-audit-2026-08-08.md) §5.1). Eight are
remediated here; two are routed elsewhere (§7).

### 1.1 The event hub holds its global lock across a broker subscription

```go
// eventhub.go:243
eh.m.Lock()
defer eh.m.Unlock()
…
// :284 — still holding it
if err := w.Service(eh.ctx); err != nil {
```

`messageWaiter.Service` subscribes to the host's broker
(`waiters/message.go:222`). The hub's **one** lock therefore spans a call into
host-supplied, possibly remote, possibly blocking code: while a broker is slow,
no waiter can be registered, unregistered or looked up anywhere in the engine.

### 1.2 Task authorization runs under the engine's registry lock

```go
// tasks.go:219-228
t.m.Lock()
defer t.m.Unlock()
…
if err := rec.eligible.Authorize(taskID, actor); err != nil {
```

`Authorize` itself is engine-owned and in-memory: `Eligibility` holds
already-RESOLVED slots and `permits` compares them. What crosses the host
boundary is the **actor**: `permits` calls `actor.UserID()` and
`actor.Groups()` (`eligibility.go:127,136`), and `hi.Actor` is an interface the
embedder implements. Their contract is "return the id / the groups", so the
normal implementation is a trivial getter — but the engine cannot assume that,
and it runs them holding `t.m`, the lock every registration, launch and
discovery call needs.

This is weaker than §1.1: a blocking `Groups()` is a misbehaving embedder
rather than an expected one. It is fixed for the same reason the producer's
hooks were (FIX-036 §1.5) — the engine does not get to decide how long someone
else's code takes — and because the SHAPE is what keeps recurring.

**§1.1 and §1.2 are the same defect as FIX-036 §1.5**, which found host code
running uncontained inside the producer. That landing fixed the producer and
nothing asked where else the shape occurred. It occurred twice more.

### 1.3 A concurrent register orphans itself against an unregister

`UnregisterEvent` reads the waiter under `RLock`, **releases**, then removes the
processor, tests `len(w.EventProcessors()) == 0`, stops the waiter and calls
`RemoveWaiter`. `registerWaiter` holds the write lock for its whole body and can
land in that window: it finds the waiter still mapped and adds a processor to
it. The unregister then stops and unmaps it. The new registration is attached to
a stopped waiter that no longer exists in the registry — its events **never
arrive**, with no error anywhere.

### 1.4 A transient repository error abandons an instance at startup

```go
// recovery.go:71-73
if saveErr := repo.Save(ctx, rec); saveErr != nil {
    return nil //nolint:nilerr // a lost claim race is the normal outcome
}
```

A lost CAS *is* a normal outcome. A connection reset, a timeout, or a store
outage is not, and this cannot tell them apart: every failure is read as "someone
else claimed it", `recoverOne` returns **nil**, and `recoverInstances` logs
nothing. An in-flight business process silently never resumes.

The wake path disagrees with itself here: `claimForWake` (`wake.go:285-289`)
**retries** the identical failure.

### 1.5 A failed registration bricks a process key

```go
// thresher.go:1065-1082
if prevLatest != nil {
    if err := t.unregisterStarters(prevLatest.starters); err != nil { … }
    t.releaseWiringLocked(prevLatest)          // prevLatest is now OFF the hub
}

if err := t.registerStarters(starters); err != nil {
    t.releaseWiringLocked(reg)
    return nil, err                            // and the NEW one never went on
}
```

When the second call fails the key has **no live starters at all** — the
previous version was already withdrawn. Worse, the failed version stays in the
registry as `latest`, so a retry tries to `unregisterStarters` a set that was
never registered, gets `ObjectNotFound`, and fails at the first branch. Every
subsequent `RegisterProcess` for that key fails: the key is **permanently
bricked**.

FIX-036 M6 added the claim bookkeeping on exactly this path and did not ask what
happens to the hub when the second call fails.

### 1.6 A scope snapshot is not atomic

`SnapshotAt` reads the visible names (`namesFrom`, locking), releases, then reads
each datum (`GetData`, locking). `track.go:1496` states this runs "on the track
goroutine right after its own commit" and that "commits bypass the loop", so
other goroutines mutate the plane while the snapshot walks it. The result is a
world that never existed at any instant — precisely what the snapshot exists to
prevent — or a spurious failure when a datum is removed mid-walk.

### 1.7 Resolution by ItemDefinition id is non-deterministic

`GetDataByID` iterates a map and returns the first match. An ItemDefinition is a
*type*, so two variables of one type share its id, and Go randomizes map
iteration: the same lookup returns different variables on different runs.
`events/event.go:790` resolves by id on the event data path.

### 1.8 A handle's observers do not survive a rebuild

`InstanceHandle.Observe` registers on `h.current()` — the instance **object**.
SRD-071 made the handle's identity outlive its object (`adopt` re-points it), and
the observer registration was not carried across. After the first dehydration a
host observer stops receiving facts, silently, while its `Subscription` still
reports itself live. The method's own comment notes the instance is "swappable"
— for the logger.

## 2 Root cause analysis

**A lock whose scope was never stated.** §1.1, §1.2 and §1.3 are one cause: the
engine's locks protect *registries*, and in each case the critical section grew
to include work that is not registry work — a broker subscription, a policy
lookup, a waiter teardown. `pkg/thresher/locked.go` states the rule and enforces
it by construction for one map; nothing states it for the hub or the task
registry, so it held only where someone remembered.

**Two failures that report success.** §1.3, §1.4 and §1.8 all end with the
caller believing it succeeded: a registration that will never fire, a recovery
that never happened, an observer that will never be called. None logs. A defect
that reports failure is a bug; one that reports success is a bug that survives
release.

**Rollback is only half-written.** §1.5 tears down the old before installing the
new and has no path back when the second step fails.

## 3 Solution

### 3.1 Alternatives considered

| # | Decision | Alternatives | Chosen |
|---|---|---|---|
| 1 | §1.1 the hub lock | (a) a second lock for waiter *creation* — two locks over one registry invites the ordering bugs the package has already had; (b) **build and Service the waiter OUTSIDE the lock, then insert under it**, tearing the waiter down if the insert loses a race | **(b)** — the same shape `HoldSubscription` uses since FIX-036: the expensive, foreign work happens unlocked, the registry mutation is a short critical section |
| 2 | §1.2 authorization | (a) copy the record under the lock and authorize outside; (b) leave it — the actor's accessors are expected to be getters, so the exposure is small | **(a)** — the eligibility policy is written once and read-only, so reading it separately cannot go stale, and the ownership decision still happens under the lock on a freshly read record. `setOwner` additionally stops taking a CALLBACK it runs under the lock, which is the shape `locked.go` forbids by construction and the reason two of the three call sites went unnoticed |
| 3 | §1.3 the unregister race | (a) re-check `len(EventProcessors())` after re-acquiring — still a window; (b) **hold the write lock across the whole check-and-remove** | **(b)** — it is registry work, which is exactly what the lock is for; the waiter's `Stop` moves out of the critical section |
| 4 | §1.4 recovery | (a) retry like `claimForWake` — hides a genuine outage behind attempts; (b) **classify: a lost CAS is `nil`, anything else is reported** — the repository already distinguishes them | **(b)** |
| 5 | §1.5 the bricked key | (a) re-register `prevLatest` on failure — restores auto-start but leaves the failed version as `latest`, so the retry still misbehaves; (b) **remove the failed version from the registry AND restore the previous latest's starters** | **(b)** — the key returns to exactly its pre-call state |
| 6 | §1.6 the snapshot | (a) a coarse "snapshot lock" held by every mutator — a second lock over the same state; (b) **one locked walk**: a `snapshotAtLocked` that reads names and data under a single acquisition | **(b)** |
| 7 | §1.7 ambiguous ids | (a) pick deterministically (sorted by name) — makes the result stable and still arbitrary; (b) **refuse an ambiguous lookup with a classified error naming both candidates** | **(b)** — two variables of one type resolved by type is a modelling error, and answering it silently is how it stays unnoticed |
| 8 | §1.8 observers | (a) re-register in `adopt` — the handle would need to remember its observers; (b) **register the observer on the HANDLE, and have the handle's fan-out follow the current instance** | **(a)** — the handle already owns identity across rebuilds (SRD-071); it keeps its observer set and re-attaches on adopt, which is the smaller change |

### 3.2 Changes by file

#### 3.2.1 `internal/eventproc/eventhub/eventhub.go` — the lock covers the registry, not the world

`registerWaiter` builds and services the waiter before taking `eh.m`, then
inserts under it; if another registration won the race meanwhile, the loser
stops its own waiter and joins the winner. `UnregisterEvent` takes the write lock
for the whole check-and-remove, and stops the waiter after releasing it.

#### 3.2.2 `pkg/thresher/tasks.go` — authorize outside the registry lock

The task record's authorization inputs are copied under `t.m`; `Authorize` runs
after the release.

#### 3.2.3 `pkg/thresher/recovery.go` — a lost claim is not an outage

The `Save` failure is classified: a CAS/version conflict returns `nil` (someone
else recovered it), anything else is reported through `reportRecoveryFailure`
like every other per-instance recovery error.

#### 3.2.4 `pkg/thresher/thresher.go` — a failed registration leaves no trace

On a failed `registerStarters`, the version just appended is removed from the
registry and the previous latest's starters are put back on the hub, so the key
is exactly as it was before the call and a retry behaves like a first attempt.

#### 3.2.5 `internal/scope/scope.go` — one locked walk, and an ambiguous id refuses

`SnapshotAt` reads names and data under a single acquisition of `p.m`.
`GetDataByID` refuses when more than one datum carries the id, naming both.

#### 3.2.6 `pkg/thresher/observer.go`, `handle.go` — observers belong to the handle

The handle keeps its observer registrations and re-attaches them when `adopt`
re-points it at a rebuilt instance, so a subscription taken before a dehydration
keeps delivering after it.

## 4 Verification

### 4.1 Regression tests

| # | Test | Asserts |
|---|---|---|
| T-1 | `TestRegisterWaiterDoesNotHoldTheLockAcrossService` | a waiter whose `Service` blocks does not block a concurrent hub operation (§1.1) |
| T-2 | `TestTaskAuthorizationDoesNotHoldTheEngineLock` | a blocking `Authorize` does not block a concurrent registry call (§1.2) |
| T-3 | `TestUnregisterRacingRegisterKeepsTheRegistration` | a register landing inside an unregister leaves a live, mapped waiter (§1.3) |
| T-4 | `TestRecoveryReportsATransportError` | a non-CAS `Save` failure is reported, not swallowed; a CAS conflict still returns silently (§1.4) |
| T-5 | `TestFailedRegisterRestoresThePreviousStarters` | after a failed `registerStarters` the previous version is live again, the failed version is gone, and a retry succeeds (§1.5) |
| T-6 | `TestSnapshotAtIsAtomic` | a concurrent commit cannot tear a snapshot (§1.6) |
| T-7 | `TestGetDataByIDRefusesAnAmbiguousID` | two data sharing an ItemDefinition id produce a classified error naming both (§1.7) |
| T-8 | `TestHandleObserverSurvivesARebuild` | an observer registered before a dehydration receives facts after the rebuild (§1.8) |

### 4.2 Gate

`make ci` green end to end, `-race` included; diff-coverage ≥95% on the touched
lines (`COVER_MIN`).

## 5 Prevention

- The hub and the task registry get the sentence `pkg/thresher/locked.go`
  already carries: the lock covers registry mutation, and nothing else runs
  inside it. Where the rule is written down it has held; where it was not, it
  broke twice.
- Every path fixed here reported success while failing. A registration that
  cannot fire, a recovery that did not happen and an observer that will not be
  called now each produce an error or a log.

## 6 Regressions and side effects

- `GetDataByID` becomes stricter: a model with two variables sharing an
  ItemDefinition id now gets a classified error instead of a random answer. That
  is the point, and it is a behavioural change called out in the CHANGELOG.
- `SnapshotAt` holds the plane lock for the whole walk rather than per datum.
  The walk is bounded by the visible surface and takes no host call.
- `RegisterProcess`'s failure path now mutates the registry (removing the
  version it appended). Callers already treat a returned error as "not
  registered"; this makes that true.

## 7 Related

Two confirmed findings are **not** fixed here:

- **`InstanceHandle.Cancel` is lost on a dehydrated instance** (audit §5.1
  C-11). Making it durable means giving Cancel the pending-operation rail that
  incident ops ride (`WithPendingIncidentOp`), and deciding what cancelling a
  *parked* instance means for its checkpoint. That is a design decision, not a
  patch — it needs its own document.
- **`msgIdx` overwrites concurrent tracks for one message definition** (C-15) is
  the subject of issue **#305**, "per-iteration event payload routing for shared
  catch nodes in parallel MI", filed with SRD-082. The audit rediscovered it
  independently; the issue is the right home.

Prior art: [FIX-036](FIX-036-thresher-lifecycle-races-and-reservations.md) §1.5
(host code under an engine lock — the shape §1.1 and §1.2 repeat) and its M6
(the wiring claim §1.5 completes).

## 8 Implementation summary

_To be filled after the milestones land._

## 9 Open questions

None.
