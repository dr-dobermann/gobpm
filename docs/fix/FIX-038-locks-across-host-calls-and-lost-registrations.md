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

### 1.9 The plane lock spans the runtime-variable supplier

```go
// scope.go:94-99
p.m.Lock()
defer p.m.Unlock()

if p.rt != nil && from == p.rtPath {
    return p.rt.RuntimeVar(name)
}
```

`RuntimeVarsSupplier` is engine-internal rather than host code, so this is
milder than §1.1 — but it is the same shape, and here it is also gratuitous:
a runtime variable comes from the SUPPLIER, not from the plane's maps, so the
branch reads nothing the lock protects. It was found while fixing §1.6, in the
function that fix touches.

`EventHub.Shutdown` has the same thing, and this one IS host-facing:

```go
// eventhub.go:398-411
eh.m.Lock()
…
eh.rt.Reporter().Report(observability.Fact{
    Kind:  observability.KindHubState,
    Phase: observability.PhaseStopped,
})
```

`Reporter()` is the engine's producer: it fans the fact out to host observers
and through the host's log redactor. Under `eh.m` that is §1.1 exactly — an
embedder's code deciding how long the hub stays locked — in the same file as
§1.1 and §1.3, and it survived the first pass over that file.

### 1.10 A cancel does not reach a parked instance

`InstanceHandle.Cancel` calls `h.current().Cancel()`, which cancels the
instance's CONTEXT. A dehydrated instance has no loop reading that context: its
goroutines are gone and its state lives in a checkpoint. The request therefore
vanishes, and the next wake rebuilds the instance and carries on as if it had
never been made — while `WaitCompletion` blocks until the caller's deadline.

Incident operations solved this in SRD-079 §3.6 by riding the rebuild
(`WithPendingIncidentOp`); `Cancel` never got the same rail.

### 1.11 An incident operation drops the handle's observers

§1.8's remedy — the handle-owned observer registry, re-attached after a rebuild
— landed in `cancelParked` and in the wake path, but **not** in
`wakeForIncidentOp`, which adopts the rebuilt instance object and stops there.
An operator's retry or resolve therefore silenced a host's subscription exactly
as a dehydration used to, while its `Subscription` still reported itself live.

Found while implementing §1.10, whose new path was a near-copy of
`wakeForIncidentOp`: the copy is what let one of the two callers keep the bug.

### 1.12 An operator request after shutdown reports success

`engineContext` reports `running` for the whole life of the process once `Run`
has been called — `t.engine` is stored at startup (`thresher.go:655`) and never
cleared — so `rebuildAndContinue`'s `!running` guard cannot fire after a
shutdown. An incident operation or a cancel arriving then rebuilt the instance
from its checkpoint, watched the fresh loop tear straight back down on the dead
engine context, and returned **nil** to the operator.

That is the same silent loss as §1.10 one step further out: the caller is told
its request landed when nothing ever observed it.

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
| 7 | §1.7 ambiguous ids | (a) **resolve deterministically**: nearest scope first (as the by-name lookup already does), then lowest name within a scope; (b) refuse an ambiguous lookup with a classified error naming the candidates | **(a)** — (b) was chosen first and is WRONG. It rejects the send/receive pattern, where a message's payload ItemDefinition is legitimately bound to both the message variable and the variable receiving it; `TestSendReceiveMidFlow` fails against it with *"`order_in` is ambiguous — it names 2 data (order_in, received order)"*. Refusing looked principled until it refused correct BPMN. Determinism is the achievable improvement: the nearest-scope rule is meaningful, the lowest-name tiebreak is arbitrary but stable, and an id naming several data in one scope remains a modelling smell the engine now answers the same way every time |
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

#### 3.2.5 `internal/scope/scope.go` — one locked walk, and a total id rule

`SnapshotAt` reads names and data under a single acquisition of `p.m`
(`namesFromLocked` + the unlocked `getData` the by-name path already uses).
`GetDataByID` resolves nearest-scope-first, lowest-name-within-a-scope — total
and deterministic, where it previously depended on Go's map iteration order.

#### 3.2.6 `pkg/thresher/observer.go`, `handle.go` — observers belong to the handle

The handle keeps its observer registrations and re-attaches them when `adopt`
re-points it at a rebuilt instance, so a subscription taken before a dehydration
keeps delivering after it.

#### 3.2.7 `internal/scope/scope.go`, `eventhub.go` — the last two

`GetData` serves a runtime variable before taking `p.m`; `p.rt` and `p.rtPath`
are set at construction and never reassigned, so the branch needs no lock at
all. `Shutdown` reports its stopped fact after releasing `eh.m` — the registry
is already cleared by then, so nothing depends on the order.

#### 3.2.8 `internal/instance`, `pkg/thresher` — a cancel rides the rebuild

`WithPendingCancel` mirrors `WithPendingIncidentOp`, and `applyPendingOps`
applies both at the same seam: after the scope handlers are armed, before the
park decision. The cancel is applied through `stopAll`, **not** `inst.Cancel` —
`stopAll` sets `ls.stopping`, which `maybeDehydrate` checks, so the instance
cannot park again before observing the request. A bare context cancel would race
the re-park and be lost exactly as the pre-rebuild one was.

`InstanceHandle.Cancel` routes a dehydrated instance through
`Thresher.cancelParked`, which takes the wake latch (`awaitClaim`) like every
other rebuild path. A resident instance keeps today's direct path.

#### 3.2.9 `pkg/thresher/incident_ops.go` — one rail for a request that rides a rebuild

`wakeForIncidentOp` and `cancelParked` were near-copies: claim the wake latch,
rebuild with the request as a pending option, adopt the new object, wait for the
verdict. They collapse into `rebuildForOp`, so the handle re-attachment (§1.11)
exists once and cannot land in one caller only, and `awaitOpVerdict` isolates
the bounded wait.

`rebuildForOp` refuses before claiming anything when the engine context is
already cancelled (§1.12). The check lives here rather than in
`rebuildAndContinue` because this is the path with a caller waiting for a
verdict: a timer wake arriving after shutdown rebuilds and terminates with
nobody misinformed, while an operator told "cancelled" needs it to be true.

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
| T-7 | `TestGetDataByIDIsDeterministic` | two data sharing an ItemDefinition id resolve to the same one on every run — nearest scope, then lowest name (§1.7) |
| T-8 | `TestHandleObserverSurvivesARebuild` | an observer registered before a dehydration receives facts after the rebuild (§1.8) |
| T-9 | `TestRuntimeVarIsServedOutsideThePlaneLock` | while the supplier is working, the rest of the plane stays usable (§1.9) |
| T-10 | `TestCancelReachesADehydratedInstance` | cancelling a parked instance terminates it, and advancing past its timer does not resurrect it (§1.10) |
| T-11 | `TestApplyPendingCancel`, `TestApplyPendingOpsWithNothingPending` | the rebuild-borne cancel stops the loop before its park decision and answers its caller; an ordinary rebuild is untouched (§1.10) |
| T-12 | `TestRebuildForOpReportsAFailedClaim`, `TestRebuildForOpReportsAFailedRebuild` | a request that cannot take the latch or cannot rebuild is reported, names the operation, and releases the latch (§1.11) |
| T-13 | `TestCancelOnAParkedInstanceReportsAStoppedEngine`, `TestAwaitOpVerdictHonoursTheCallerContext` | a request arriving after shutdown fails instead of reporting success; the verdict wait is bounded by the caller's context (§1.12) |

### 4.2 Gate

`make ci` green end to end, `-race` included; diff-coverage ≥95% on the touched
lines (`COVER_MIN`).

## 5 Prevention

- The hub, the task registry and the scope plane get the sentence
  `pkg/thresher/locked.go` already carries: the lock covers registry mutation,
  and nothing else runs inside it. Where the rule is written down it has held;
  where it was not, it broke four times — the producer (FIX-036 §1.5), the hub,
  the task path, and the plane.
- A mechanical sweep for the shape is worth more than reading the sites an audit
  names: the one run for this FIX found `setOwner`'s two callback-mediated calls,
  which no reviewer reported. But it took **three** attempts, and the failures
  are the lesson.

  The first treated `defer m.Unlock()` as closing the critical section. Every
  lock in this codebase is written that way, so it reported **clean** on a
  package with three live instances. The second was function-scoped and flagged
  five ALREADY-FIXED sites, because it could not tell "in the function" from "in
  the critical section". The third tracks lock state, understands `defer`, and
  is the only one worth running.

  A scanner that reports clean is the dangerous failure mode — it looks exactly
  like the answer you wanted. Two of the findings in this document were reported
  as "swept, nothing left" before a re-check found them: `Shutdown`'s host
  Report was missed because the pattern list had dropped `Report`. Re-run the
  sweep with the fix in place and confirm it FAILS on the pre-fix code, the same
  discipline every regression test here follows.
- Every path fixed here reported success while failing. A registration that
  cannot fire, a recovery that did not happen and an observer that will not be
  called now each produce an error or a log.

## 6 Regressions and side effects

- `GetDataByID` becomes DETERMINISTIC, not stricter. A model with two variables
  sharing an ItemDefinition id used to get a random one of them and now gets a
  stated one — nearest scope, then lowest name. No model that worked starts
  failing; a model that worked *by luck* now works reliably, and one that was
  silently picking the wrong variable will start doing so consistently, which is
  what makes it findable. Called out in the CHANGELOG.
- `SnapshotAt` holds the plane lock for the whole walk rather than per datum.
  The walk is bounded by the visible surface and takes no host call.
- `RegisterProcess`'s failure path now mutates the registry (removing the
  version it appended). Callers already treat a returned error as "not
  registered"; this makes that true.
- An incident operation or a cancel against a parked instance now FAILS after
  the engine's context is cancelled, where it previously returned nil (§1.12).
  A caller that shut the engine down and then issued an operator request was
  already getting nothing done; it is now told so. Nothing in the wake or timer
  paths changes — the guard is scoped to the two operator entry points.

## 7 Related

One confirmed finding is **not** fixed here:

- **`msgIdx` overwrites concurrent tracks for one message definition** (C-15) is
  the subject of issue **#305**, "per-iteration event payload routing for shared
  catch nodes in parallel MI", filed with SRD-082. The audit rediscovered it
  independently; the issue is the right home.

**§1.10 was itself a deferral, and that was the mistake.** This section
originally parked `InstanceHandle.Cancel` as "a design decision, not a patch —
it needs its own document". It was not blocked on anything: the rail existed
(`WithPendingIncidentOp`), the latch existed (`awaitClaim`), and "large" is not
"blocked". It is fixed here as §1.10. The global rule now says so explicitly:
*out of scope* is the same deferral as *not mine*, only justified by a schedule
instead of by authorship.

The same rule ran again during §1.10's own implementation: the new
`cancelParked` was a near-copy of `wakeForIncidentOp`, and reading the two side
by side exposed §1.11 (the observer re-attachment that had landed in one caller
only) and §1.12 (an operator request reporting success after shutdown). Both are
fixed here, in this branch, rather than recorded as "noted for the next pass" —
and the copy that hid §1.11 is gone with them.

Prior art: [FIX-036](FIX-036-thresher-lifecycle-races-and-reservations.md) §1.5
(host code under an engine lock — the shape §1.1 and §1.2 repeat) and its M6
(the wiring claim §1.5 completes).

## 8 Implementation summary

_To be filled after the milestones land._

## 9 Open questions

None.
