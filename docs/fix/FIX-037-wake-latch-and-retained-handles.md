# FIX-037 — the wake latch loses what it refuses, and two retained handles are never released

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Draft.
**Date:** 2026-08-08.
**Author:** Ruslan Gabitov.
**Branch:** `fix/wake-residency-races` — remediation of the first `/audit-package` sweep of `pkg/thresher` (2026-08-08).
**Upstream:** [ADR-007 v.2.1](../design/ADR-007-in-memory-long-waits.md) §2.3 (dehydration & wake-on-trigger — the model whose latch this repairs), [ADR-033 v.3](../design/ADR-033-persistence-and-state.md) §2.8 (the incarnation fence, which does NOT fence an in-process duplicate), [ADR-020 v.3](../design/ADR-020-human-interaction-execution-model.md) §2.1.1 (the residency a task action requires).

**Grounded in (internal artifacts, verified at `11a0321`):**
- `pkg/thresher/wake.go:93-97` — `wakeInstance` returns `nil` on a lost `claimWake`, discarding `pending`.
- `pkg/thresher/tasks.go:602-609` — `hydrateForTask` returns `nil` on a lost `claimWake`, having neither rebuilt nor pinned.
- `pkg/thresher/tasks.go:564-598` — `residentForTask` documents "it was built already pinned"; `:513-520` — `onTaskInstance` unconditionally `UnpinResident()`s.
- `pkg/thresher/incident_ops.go:23-25` — `wakeForIncidentOp` calls `rebuildAndContinue` with **no** `claimWake`; the only two callers of `claimWake` are `wake.go:93` and `tasks.go:603`.
- `pkg/thresher/wake.go:263-289` — `claimForWake` **retries** on a lost CAS (`continue // the record moved under us`), so a repository claim does not serialise two rebuilds; it only orders them.
- `pkg/thresher/locked.go:492-496` — `trackInstanceLocked` overwrites `instanceReg`, dropping the previous `stop` without calling it.
- `pkg/thresher/wake.go:25-47` — `HoldTimer` arms `timerSvc.hold` with no confirm, unlike `HoldSubscription` (`subscription_holder.go`, FIX-036 §3.2.4).

---

## 1 Symptoms

Found by the first `/audit-package` run — an external reviewer (Antigravity /
`agy`, `gemini-3.1-pro-high`) over the whole package, doc-blind. Five findings
survived verification against the source. They are not five bugs; they are two.

### 1.1 A refused wake silently discards its trigger

```go
// wake.go:93-97
if !t.claimWake(instanceID) {
    // another wake is already hydrating this instance — it will deliver
    // the trigger into the soon-resident loop; nothing to do here (§4.6).
    return nil
}
```

The comment is false. The concurrent wake carries **its own** `PendingTrigger`
— the one it was called with. It has no access to this goroutine's trigger and
cannot deliver it. `wakeInstance` returns `nil`, which its callers read as
success: `wakeFromSubscription` reports nothing, and the timer service treats a
`nil` as delivered and drops the deadline. The event is **permanently lost**.

An Event-Based Gateway arming a timer and a message on one dehydrated instance
is the ordinary shape that reaches this: the timer fires and claims; the message
arrives, is refused, and is dropped.

### 1.2 A refused task hydration returns an unpinned instance

```go
// tasks.go:602-606
func (t *Thresher) hydrateForTask(instanceID string) error {
    if !t.claimWake(instanceID) {
        return nil // another wake is already rebuilding it
    }
```

`residentForTask` treats that `nil` as "the rebuild happened", and says so:

```go
// tasks.go:580-582
// the rebuild re-registered the instance — resolve it afresh. It was built
// already pinned, so it cannot have released in between.
```

On this path no rebuild happened and **no pin was taken**. `onTaskInstance`
(`:520`) then unconditionally calls `inst.UnpinResident()`, decrementing a
counter this call never incremented. The instance may also still be
`Dehydrated`, so the action fast-fails and burns its retries.

### 1.3 An incident operation rebuilds without claiming

`wakeForIncidentOp` (`incident_ops.go:23`) calls `rebuildAndContinue` directly.
It is the only rebuild path that does not take the latch — `grep` shows exactly
two `claimWake` callers, and this is not one.

The repository claim does not save it. `claimForWake` **retries** a lost CAS
(`wake.go:285-289`) rather than failing, so two concurrent rebuilds both
succeed, at incarnation+1 and incarnation+2. `claimWake` is the *only* thing
preventing two live `Instance` objects for one id, and this path skips it: an
operator's `RetryIncident` racing a timer wake starts a second execution loop
over the same state.

### 1.4 `trackInstanceLocked` drops the previous context-cancel

```go
// locked.go:492-496
t.instances[inst.ID()] = instanceReg{
    stop:   cancel,
    inst:   inst,
    handle: h,
}
```

Every rebuild derives a fresh child context from the engine's and stores its
cancel here, overwriting the previous one **without calling it**. The old child
stays attached to the engine context's children for the engine's lifetime.

This is the same defect FIX-036 §8.2 fixed in `Forget`, reached by the other
path: that landing made `Forget` call the retained cancel and did not notice
that the *rebuild* path discards one on every dehydration cycle. A long-lived
instance that parks and wakes repeatedly leaks one context per cycle, which is
the worse of the two.

### 1.5 `HoldTimer` arms without confirming, as `HoldSubscription` used to

```go
// wake.go:36-45
if t.timerSvc == nil { … }

t.timerSvc.hold(timerHold{…})

return nil
```

`ReleaseWaits` releases the timer first (`subscription_holder.go`), then the
subscriptions. A `ReleaseWaits` that runs between this method's entry and its
`hold` finds no deadline to remove, and the `hold` that follows registers one
for a wait that has been cancelled — a zombie deadline that later wakes the
instance for a track that is gone.

FIX-036 §1.4 identified precisely this shape for subscriptions and gave
`HoldSubscription` a record→arm→**confirm**. The timer half was not examined.

## 2 Root cause analysis

**A try-lock used where a barrier is needed.** §1.1, §1.2 and §1.3 are one
cause. `claimWake` answers "is a wake in flight?" and every caller treats
`false` as "then my work is already being done by someone else" — which is true
of *rebuilding the instance* and false of *everything the caller was carrying*:
a trigger, a residency pin, an operator's operation. The latch protects the
shared resource correctly and silently discards the caller's payload. A refused
claim needs to mean **wait, then do your own part**, not **give up and report
success**.

**A retained handle nobody is required to release.** §1.4 and §1.5 are the
other. `instanceReg.stop` and a `timerSvc` deadline are both handles taken on
behalf of something with a lifetime, and in both cases the code that ends that
lifetime is not the code that took the handle. FIX-036 §8.2 fixed one instance
of this and the pattern remained.

## 3 Solution

### 3.1 Alternatives considered

| # | Decision | Alternatives | Chosen |
|---|---|---|---|
| 1 | §1.1–1.3 the latch | (a) hand the trigger to the in-flight wake (a queue per instance) — the winner must then own delivery for waits it knows nothing about, and the queue needs its own lifecycle; (b) make the latch **waitable**: a loser blocks until the winner finishes, then retries its own path against the now-resident instance | **(b)** — the retry is already written (the resident path at `wake.go:81-91`), each caller keeps ownership of its own payload, and no new lifetime is introduced |
| 2 | §1.3 the incident path | (a) leave it and rely on the repository CAS — refuted in §1.3: `claimForWake` retries, so the CAS orders but does not exclude; (b) take the same waitable latch | **(b)** |
| 3 | §1.4 the cancel | (a) cancel the old context inside `trackInstanceLocked` — runs a cancel under `t.m`, the forbidden shape; (b) return the displaced cancel to the caller and let it run outside the lock, exactly as `Forget` does since FIX-036 §8.2 | **(b)** |
| 4 | §1.5 the timer | (a) mirror `HoldSubscription`'s confirm — needs a record of the hold outside `timerSvc` to confirm against, duplicating the service's own bookkeeping; (b) an **epoch per (instance, track)** in `timerSvc` — correct, but the map is keyed by track and never emptied, so it grows for the engine's lifetime: the §1.4 leak class reintroduced by the §1.5 remedy; (c) an **in-flight arming token**: `beginArm` announces, `hold` refuses a token a `release` has dropped | **(c)** — the same window as (b), with a map bounded by concurrent arms instead of by every track ever run |

### 3.2 Changes by file

#### 3.2.1 `pkg/thresher/wake.go` — the latch becomes waitable

`claimWake` gains a companion that blocks until an in-flight wake completes.
A refused claimant waits, then retries its own path rather than returning.
`wakeInstance` re-enters its resident delivery (`WakeParkedTrack`) with the
trigger it still holds; if the instance parked again in between, it re-claims.
The retry is bounded so a pathological wake storm cannot spin.

#### 3.2.2 `pkg/thresher/tasks.go` — a refused hydration waits, then pins

`hydrateForTask` waits for the in-flight rebuild and then takes its own
residency pin, so `residentForTask`'s documented invariant ("it was built
already pinned") becomes true on every path rather than one. `onTaskInstance`'s
unconditional `UnpinResident` is then correct as written.

#### 3.2.3 `pkg/thresher/incident_ops.go` — the incident path claims like the rest

`wakeForIncidentOp` takes the latch before `rebuildAndContinue`, so the operator
path cannot start a second loop beside a timer wake.

Both go through **one** helper, `awaitClaim` (`wake.go`): claim, and on a loss
wait for the in-flight wake and retry, bounded, refusing when the engine stops.
Writing it twice was how §1.3 happened in the first place — the third rebuild
path simply never grew the code the other two had.

#### 3.2.4 `pkg/thresher/locked.go` — the displaced cancel is returned, not dropped

`trackInstanceLocked` returns the `stop` it displaced along with the handle; the
caller runs it after the lock is released. A first registration returns nil and
the caller does nothing.

#### 3.2.5 `pkg/thresher/timer_service.go` — an arm is announced before it is built

`beginArm` registers an in-flight token for the track and returns it; `hold`
refuses a token that is no longer live; `release` drops the tokens of every arm
in flight for that track. A refused hold is not an error — the wait it belonged
to is gone — so `HoldTimer` logs it at `Debug` and returns nil, exactly as a
withdrawn subscription hold reports success.

**This replaces the epoch-per-(instance, track) design this section first
specified.** An epoch map is keyed by track and never emptied, so it grows for
every track the engine has ever run — the precise leak class §1.4 is about,
reintroduced by the remedy for §1.5. A token keyed by the *arming call* lives
only for the duration of one `HoldTimer`, so the map is bounded by concurrent
arms rather than by history. The window closed is identical: §1.5 is about a
release landing "between this method's entry and its `hold`", which is exactly
the span a token covers.

The four existing test call sites move to the token form rather than gaining a
blind convenience wrapper — one protocol, and no second entry point through
which the defect could return.

## 4 Verification

### 4.1 Regression tests

| # | Test | Asserts |
|---|---|---|
| T-1 | `TestWakeSingleFlightWaitsThenRetries` | a second trigger arriving while a wake is in flight is delivered, not dropped — the instance observes both (§1.1) |
| T-2 | `TestHydrateForTaskWaitsForAnInFlightWake` | a task action racing a wake gets a pinned, resident instance; the residency counter is balanced after `UnpinResident` (§1.2) |
| T-3 | `TestIncidentOpTakesTheWakeLatch` | an incident op racing a timer wake produces exactly one live instance for the id (§1.3) |
| T-4 | `TestRebuildReleasesThePreviousContext` | a dehydrate→rehydrate cycle cancels the displaced context; N cycles leave no accumulation (§1.4) |
| T-5 | `TestReleaseWaitsDuringHoldTimerRefusesTheArm` | a `ReleaseWaits` driven into the middle of `HoldTimer` leaves no deadline registered (§1.5) |

### 4.2 Gate

`make ci` green end to end, `-race` included; diff-coverage ≥95% on the touched
lines (`COVER_MIN`).

## 5 Prevention

- The wake latch's contract is stated where it is defined: refusing a claim
  transfers responsibility for **the instance**, never for the caller's payload.
- Every handle the engine retains on behalf of a lifetime (`instanceReg.stop`, a
  `timerSvc` deadline) is released by the code that ends that lifetime, and the
  two paths that end an instance's — `Forget` and the rebuild — now both do.

## 6 Regressions and side effects

- A refused wake now **blocks** briefly instead of returning immediately. It is
  bounded by the in-flight rebuild it waits on, and that rebuild is already on
  the caller's critical path in the common (uncontended) case.
- `trackInstanceLocked`'s signature changes; it is package-private.

## 7 Related

[FIX-036](FIX-036-thresher-lifecycle-races-and-reservations.md) — §1.4 fixed the
subscription half of §1.5 here and left the timer half; §8.2 fixed the `Forget`
half of §1.4 here and left the rebuild half. This document is the other half of
both, which is why it exists as its own FIX rather than an amendment: FIX-036 is
merged.

## 8 Implementation summary

### 8.1 Milestones

| # | Commit | Scope |
|---|---|---|
| — | `982ef34` | this document and the 2026-08-08 package audit |
| M1 | `2afb305` | §1.4 — `trackInstanceLocked` returns the cancel it displaces (§3.2.4) |
| M2 | `a4af0de` | §1.5 — an arm is announced before it is built (§3.2.5) |
| M3 | `55399d4` | §1.1–1.3 — the waitable latch and its three callers (§3.2.1–3.2.3) |

M3 and M4 of the planned four landed together: they are one root cause reached
through three callers, and the latch's signature change touches all three at
once — splitting them would have left the tree uncompilable between commits.

### 8.2 What the implementation added to the design

- **§3.2.5's epoch became an in-flight token.** An epoch keyed by
  (instance, track) is never emptied, so it grows for every track the engine has
  ever run — the §1.4 leak class, reintroduced by the §1.5 remedy. A token keyed
  by the arming *call* lives only for the duration of one `HoldTimer`. The
  window closed is identical; §3.1 #4 and §3.2.5 are amended.
- **Three existing tests asserted the defective contract.**
  `TestWakeSingleFlightRefusesSecond` documented §1.1 as intended behaviour ("a
  no-op (it will ride the resident loop)"), and its task counterpart did the
  same for the pin. Both are re-pinned to the corrected contract rather than
  relaxed. A defect that a test asserts is a defect the test will defend.
- **A refused arm and an exhausted retry differ.** A `HoldTimer` refused by a
  concurrent release is a success — the wait is gone, nothing to hold. A trigger
  that exhausts `wakeDeliverAttempts` is an error, reported loudly. Both used to
  be `nil`, which is what made §1.1 invisible.

### 8.3 Gate

The first full run at `55399d4` **failed**: diff-coverage 78.2% against a 95%
floor. The uncovered lines were the branches this fix exists to create — most
importantly `hydrateForTask`'s pin path, the actual remedy for §1.2, which the
first T-2 never asserted (it checked only that the call unblocked). A test that
asserts less than it appears to is the failure mode §1.2 itself came from.

Closing it produced a better shape than more tests would have. `hydrateForTask`
and `wakeForIncidentOp` had each hand-rolled the same wait-then-claim loop;
both now call one `awaitClaim` helper, which is the reusable form of "I need to
rebuild this instance and someone else may be rebuilding it right now". The
duplication and the coverage gap went together — `incident_ops.go` and
`tasks.go` are at 100% of changed lines, and there is now a single place where
a rebuild path takes the latch.

`Instance.ResidentPins` was added (internal package) so the pin BALANCE is
observable: the invariant §1.2 breaks could not be asserted from `pkg/thresher`
at all, which is why the original test could only check that it unblocked.

Final: `diff-coverage: 96.5% of 113 changed coverable lines (min 95%) — PASS`.

## 9 Open questions

None.
