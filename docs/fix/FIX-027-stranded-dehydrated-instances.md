# FIX-027 «A failed wake destroys the hold that was the dehydrated instance's only way back»

**Type:** FIX (one-shot defect remediation; not rewritten after landing).
**Status:** Draft v.1 (2026-07-29, branch `feat/dehydration`, not yet implemented).
**Date:** 2026-07-29.
**Author:** Ruslan Gabitov.
**Branch:** `feat/dehydration` — the defect was introduced by SRD-071 M3–M5 on
this same **unmerged** branch, so the remediation lands beside it rather than in
a separate `fix/*` branch (a feature's code and its corrections are one
reviewable change-set).
**Paired doc:** [SRD-071 v.1](../srd/SRD-071-instance-dehydration-and-wake-on-trigger.md)
(Draft — the slice this defect lives in).
**Upstream:** [ADR-007 v.2](../design/ADR-007-in-memory-long-waits.md) §2.4 (the
holder model and its "never a lost trigger" invariant), §5 (multi-node wake
deferred),
[ADR-033 v.2](../design/ADR-033-persistence-and-state.md) §2.5 (overdue collapses
to one firing) and §2.8 (leases, CAS fencing),
[SRD-070 v.1](../srd/SRD-070-instance-checkpoint-and-restart-recovery.md)
(checkpoint/restore + restart recovery at engine start).

**Grounded in (internal artifacts):**
- An empirical probe on `feat/dehydration` @ `6dd7a5a` (2026-07-28) against a
  checkpoint whose pinned process version is not registered — the realistic
  deployment-parity mismatch SRD-070 already warns about. Verbatim output in §1.
- The landed wake path: `pkg/thresher/timer_service.go:132-160`,
  `pkg/thresher/wake.go:99-155`.

---

## §1 Symptoms

A dehydrated instance has **no goroutines**. Its hold — an entry in the timer
service's `holds`, in the engine's `subs`, or in `taskTracks` — is the *entire*
mechanism by which it can ever run again. The engine currently discards that
hold **before** attempting the wake, and the wake is fallible. When it fails,
the instance is left in the store as in-flight with nothing that will ever wake
it, while the engine keeps running normally.

Probe — a held 1h timer on an instance whose pinned version is not registered,
with the clock advanced past the deadline so the service fires:

```
PROBE hold armed before:                    true
WARN  wake failed for instance instance_id=probe-inst
      error="wake: the pinned process version isn't registered
             (process never-registered-proc v1)"
ERROR InstanceState Failed reason=wake instance_id=probe-inst
PROBE hold still armed AFTER the failed wake: false
PROBE record present=true status=Active
```

The `Failed`/`reason=wake` fact makes the failure **visible**; nothing makes it
**survivable**. Expected: a wake that does not succeed leaves the instance
exactly as wakeable as it was — ADR-007 v.2 §2.4's invariant is *"never a lost
trigger"*, and a discarded deadline is a lost trigger with extra steps. ADR-007
v.2 §5 states the contract this breaks directly: *"a single-engine dehydrated
instance is woken by its own holder"* — after this defect fires, it has none.

**Blast radius.** Every wake kind, because they share `rebuildAndContinue`: a
timer loses its deadline; a message/signal wait loses its subscription; an
Event-Based Gateway loses its whole armed set. The only escape is an engine
restart, whose `recoverInstances` re-arms from the checkpoint (SRD-070) — so the
defect converts a recoverable in-process hiccup into "stranded until someone
restarts the process".

---

## §2 Root Cause Analysis

### §2.1 The timer service discards the hold before firing it

`pkg/thresher/timer_service.go:132-160` — `fireDue` deletes each due hold inside
the scan loop, *then* wakes:

```go
	for k, h := range ts.holds {
		if !h.deadline.After(now) {
			due = append(due, h)

			delete(ts.holds, k)      // ← 143: the only wake source, dropped
		}
	}

	ts.mu.Unlock()

	for _, h := range due {
		...
		ts.wake(h.instanceID, &instance.PendingTrigger{   // ← 156: and only now, try
```

The delete is correct for the *success* case — a fired one-shot timer must not
fire twice (ADR-033 v.2 §2.5) — but it is applied unconditionally, before the
outcome is known. `ts.wake` is `func(string, *instance.PendingTrigger)`
(`timer_service.go:46-49`): it returns **nothing**, so the service could not act
on the outcome even if it wanted to.

### §2.2 The rebuild withdraws every hold before the fallible part

`pkg/thresher/wake.go:99-155` — `rebuildAndContinue` withdraws the woken track's
entire hold set, then attempts the rebuild:

```go
	if pending != nil {
		t.ReleaseWaits(instanceID, pending.TrackID)      // ← 133
	}
	...
	inst, err := instance.Restore(...)                    // ← 144, can fail
	if err != nil {
		return wakeErr("the instance doesn't rebuild", err)
	}

	runCtx, cancel := context.WithCancel(t.ctx)
	if err := inst.Run(runCtx); err != nil {              // ← 151, can fail
		cancel()

		return wakeErr("the rebuilt instance doesn't run", err)
	}
```

Withdrawing the siblings is right *once the wake commits* (it is M5's
withdraw-the-losing-arms step, SRD-071 FR-3a); doing it at 133 means a failure
at 147 or 154 takes the subscriptions and the task hold with it.

### §2.3 The shared mistake

§2.1 and §2.2 are one error in two places: **the irreversible cleanup is ordered
before the operation that can fail.** Neither is a race — it is a plain ordering
bug, deterministic and reproducible (§1).

There are exactly three ways a hold leaves the engine; only one is a defect:

| Path | Intentional? |
|---|---|
| `ReleaseWaits` after a **successful** wake | ✅ the wait is genuinely over |
| `stopAll` on instance teardown (`loop.go`) | ✅ the instance is finishing |
| **a failed wake** (§2.1/§2.2) | ❌ **the defect** |

That inventory matters for §3: close this one path and no hold is ever lost
while the engine lives — so no compensating machinery is needed anywhere else.

### §2.4 Why the tests missed it

The wake-failure paths **are** covered — `TestRebuildAndContinueFailures`,
`TestRebuildRefusesABrokenRecord` and `TestClaimForWakeExhausts`
(`pkg/thresher/wake_internal_test.go`) all assert that a bad record reports
loudly. None asserts the **state of the holder registry afterwards**: they check
the error and stop. The registry is asserted only on the success path
(`TestWakeWithdrawsTheTracksHolds`). The defect sat in the gap between "the
failure is reported" and "the failure is survivable".

Generalized in §5: *a test that asserts an operation reports a failure is not a
test that the failure is survivable.*

---

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. **Release only after the wake commits**, and on failure push the hold's next attempt out by a backoff | Ordering matches the semantics — "the wait is over" becomes true only once it is; no new subsystem; the instance self-heals the moment the cause clears (the operator registers the missing version); reuses the existing timer wheel | The hold must be marked in-flight so a failing wake is not re-entered while in progress, and its deadline moved so it is not re-fired immediately | ✅ **chosen** |
| B. **Re-hold on failure** — keep the current order, re-register in the error path | Smallest diff | Compensating-action design: re-registration is itself fallible and racy (a concurrent re-arm may have replaced the hold), and rebuilding a subscription after `ReleaseWaits` needs data the error path no longer has. Not destroying the thing is strictly better than restoring it | ❌ rejected |
| C. **Periodic orphan sweep** — a background goroutine re-running `recoverInstances` on an interval to reclaim lapsed-lease records | Would also catch instances orphaned by another engine's crash | **Patrols for damage instead of preventing it.** A scan is a symptom of the real problem, and it would be implementing ADR-008's deferred multi-node story by the back door — the wrong layer for that decision (a FIX). With A in place there is nothing for it to find (§2.3), and the crash case is already handled at engine start | ❌ rejected |

**Why no scan is needed at all** (the §2.3 inventory, applied):

- *While the engine lives* — with A, the only unintentional loss is closed, so
  every dehydrated instance still holds its own wake source. ADR-007 v.2 §5:
  *"a single-engine dehydrated instance is woken by its own holder."*
- *If the engine dies* — its in-memory holds die with it, and
  `recoverInstances` re-arms from the checkpoint at the next `Run`
  (SRD-070). That is a bounded start-up step at a known lifecycle point, not a
  scanner.
- *Another engine's orphans while we run* — multi-node wake, **explicitly
  deferred** by ADR-007 v.2 §5 and SRD-071 v.1 §4.2 to ADR-008. Out of scope
  here by decision, not by omission.

### §3.1a Scope boundary: the instance starter is NOT a holder

Considered and rejected during review: unifying the wait holder with the
**`instanceStarter`** (`instance_starter.go:23-30`), on the observation that both
are permanent engine-level `EventProcessor`s registered against the hub.

The resemblance is the *subscription*, not the semantics. The two differ in
referent and lifetime:

| | `instanceStarter` | wait holder |
|---|---|---|
| bound to | a process **definition/version** | one **instance + track + conversation** |
| lives | while the process is registered | while that instance is parked/dehydrated |
| refers to | nothing — no instance exists yet | a checkpoint (the hydration source) |
| on fire | an instance is **born** | an instance is **revived** |

A hold exists to preserve the *one path back* to an instance that already
exists. **A starter has nothing to hold on to** — no checkpoint, no identity, no
prior state; a missed start message loses nothing, because nothing existed. Its
doc comment records the property that merging would destroy: it *"owns no
Instance state"*. Coupling instantiation to the holder machinery would also drag
the deferred multi-node concern (ADR-007 v.2 §5) into the instantiation path,
which has no business with it.

They already coexist without arbitration: for a correlated message the starter
resolves `key seen → join, no duplicate` and drops it
(`thresher.go:1051-1056`), while the instance's own keyed subscription delivers
or wakes. Two subscriptions, two jobs. **The starter is left exactly as it is**;
§4.1.2 adds the missing test of the one point where they observe the same event.

### §3.2 Changes by file

#### §3.2.1 `pkg/thresher/timer_service.go` — fire without discarding

`timerHold` gains two fields: `firing bool` (a wake is in progress for this
hold — later scans skip it, so a slow or failing wake is never re-entered) and
nothing else; the retry is expressed by **moving `deadline`**, so the existing
`nearest()`/`After()` machinery schedules it with no new loop.

`fireDue` selects due holds and marks them firing instead of deleting them; a
hold is removed only when its wake reports success, and on failure its deadline
is pushed out by `wakeRetryBackoff`:

```go
// before: delete inside the scan, wake afterwards, outcome ignored
for k, h := range ts.holds {
    if !h.deadline.After(now) {
        due = append(due, h)
        delete(ts.holds, k)
    }
}
...
for _, h := range due { ts.wake(...) }

// after: claim → wake → settle by outcome
for k, h := range ts.holds {
    if !h.deadline.After(now) && !h.firing {
        h.firing = true
        ts.holds[k] = h
        due = append(due, h)
    }
}
...
for _, h := range due {
    if ts.wake(...) {                 // wake now reports success
        ts.release(h.instanceID, h.trackID)
        continue
    }

    ts.deferHold(h, now.Add(wakeRetryBackoff))   // still armed, tried again later
}
```

`newTimerService`'s `wake` parameter changes from
`func(string, *instance.PendingTrigger)` to
`func(string, *instance.PendingTrigger) bool`.

**`wakeRetryBackoff` must be non-trivial.** Without moving the deadline the hold
is still due, so `run()` recomputes `nearest()`, gets a past instant,
`clk.After(<=0)` fires at once and the service spins — retrying as fast as the
loop turns and hammering the repository (`Load`+`Save` per attempt) for as long
as the cause persists. The backoff converts that into a bounded cadence.

#### §3.2.2 `pkg/thresher/wake.go` — withdraw after the rebuild commits

`rebuildAndContinue` moves the `ReleaseWaits` call from before `Restore`
(line 133) to after `inst.Run` has succeeded, so a failed rebuild leaves the
whole hold set intact. The withdraw still precedes the `Hydrated` fact, so the
observable order on the success path is unchanged.

`hydrateFromTimer` returns the success verdict for §3.2.1's callback;
`wakeFromSubscription` keeps its current classification (a correlation drop is
not a failure) and reports success for both "delivered" and "benignly dropped" —
a foreign-conversation message must not keep a subscription retrying.

#### §3.2.3 `pkg/thresher/options.go` — `WithWakeRetryBackoff`

Mirrors `WithLeaseTTL` (`options.go:379-391`) — positive-duration validation, a
classified `InvalidParameter` error otherwise. Default derived from `leaseTTL`
rather than a fixed constant, so an operator who lengthens the lease does not
get a retry cadence that fights it.

---

## §4 Verification

Current coverage of the wake-failure paths: `TestRebuildAndContinueFailures`,
`TestRebuildRefusesABrokenRecord`, `TestClaimForWakeExhausts` — all assert the
**error**, none the **holder registry afterwards** (§2.4).

### §4.1 Regression tests (mandatory)

**New:** `pkg/thresher/wake_retry_test.go`.

| Test | Setup | Assertion |
|---|---|---|
| `TestFailedWakeKeepsTheHold` | a held timer on a record whose pinned version is not registered; advance past the deadline | the hold is **still armed** after the failed wake and the record is still `Active` — the §1 probe, inverted into an assertion |
| `TestFailedWakeBacksOff` | same, with the service loop running and the clock held still | the wake is attempted **once**, not spun: a second attempt requires advancing the clock by the backoff (guards the §3.2.1 spin) |
| `TestFailedWakeRetriesAndSucceeds` | as above, then register the missing version and advance by the backoff | the instance is woken and completes — it **self-heals** with no restart and no scan |
| `TestFailedRebuildKeepsTheSubscriptionSet` | a track holding a timer + a subscription; force `Restore` to fail | **both** holds survive (`subs` non-empty, `nearest()` ok) |
| `TestSuccessfulWakeStillWithdraws` | the healthy path | the whole set is withdrawn as before — the §3.2.2 reorder must not regress M5's withdraw-the-losing-arms |
| `TestWithWakeRetryBackoffValidates` | `WithWakeRetryBackoff(0)` / negative | a classified `InvalidParameter` error, mirroring `WithLeaseTTL` |

### §4.1.2 The starter and the holder over ONE message name

A gap found while reviewing the holder-vs-starter boundary, not by this defect —
carried here because it is the one place the two subscription mechanisms observe
the same event, and nothing covers it today.

BPMN permits a message name to be **both** a process's instantiating start
trigger **and** an in-flight instance's wait. Two independent subscriptions then
see the same published message:

- the **`instanceStarter`** (definition-scoped, `instance_starter.go:23-30`),
  which resolves `key seen → join, no duplicate` and drops it
  (`thresher.go:1051-1056`);
- the dehydrated instance's **own keyed holder** (instance-scoped), which must
  still wake it.

The existing message tests use two distinct names (`order placed` /
`payment received`, `dehydration_message_test.go`), so the starter never observes
the instance's message and this interaction is unexercised.

**New:** `pkg/thresher/dehydration_starter_overlap_test.go`.

| Test | Setup | Assertion |
|---|---|---|
| `TestSameMessageNameStartsAndWakes` | one process whose keyed message start AND whose mid-flow catch use the SAME message name; instance for key `K` running, then dehydrated | a message for key `K` **wakes** the dehydrated instance (does not start a second one); a message for an **unseen** key `K2` **starts a new instance** (is not swallowed by the wake path) |
| `TestSameMessageNameNoDuplicateInstance` | as above, key `K` | exactly one instance exists for `K` afterwards — the starter's join-don't-duplicate and the holder's wake do not both act on one message |

If either assertion fails, the finding is a **separate defect** from FIX-027 —
it gets its own FIX rather than being folded in silently (the §3 scope here is
the ordering bug only).

### §4.2 Verification commands

`make ci` (exit 0, incl. `-race` and the diff-coverage gate ≥95% on touched
files), run repeatedly (≥4) — both the defect and the fix live on timing paths,
and this branch has already produced two order-dependent flakes.

### §4.3 Observability

No new facts. A failed wake already emits `InstanceState/Failed` with
`reason=wake`; the difference after this fix is that the fact now describes a
**retryable** condition rather than a terminal stranding. A subsequent
successful retry emits the ordinary `Hydrated`, so the recovery is visible in
the existing stream.

---

## §5 Prevention

- **Doc comments** on `fireDue` and `rebuildAndContinue` stating *why* the order
  is what it is — "the irreversible withdraw happens only after the fallible
  part commits" — so a future refactor that "simplifies" the order has to argue
  with the comment first.
- **Regression canaries:** name `TestFailedWakeKeepsTheHold` and
  `TestFailedWakeBacksOff` in those comments; if either falls, the fix regressed.
- **The general rule this defect teaches:** *a test that asserts an operation
  reports a failure is not a test that the failure is survivable.* Where a
  failure path destroys state, assert the state.
- **The design rule that kept the fix small:** when tempted to add a scanner that
  looks for damage, first enumerate how the damage occurs (§2.3) and close those
  paths. A periodic sweep here would have patrolled for a condition that, once
  the ordering was right, cannot arise.
- **Doc updates after landing:** SRD-071 §4.2's lease-lapse reasoning gains a
  sentence that a live engine never loses a hold, so "restart recovery reclaims
  it" applies only to an engine that actually died.

---

## §6 Regressions / side-effects

### §6.1 What may rely on the current behavior

- `grep -rn "ReleaseWaits" --include=*.go` — `track.deliver` and `loop.stopAll`
  release on paths that cannot fail and are unaffected; only the wake path moves.
- `grep -rn "fireDue\|newTimerService" --include=*_test.go` — the `wake` callback
  signature change forces a compile-level edit to every test constructing a
  service. `TestTimerServiceReleaseAndIdle` asserts a fired hold is one-shot on
  the **success** path; it must keep passing once its closure returns `true`.
- No persisted-format change: `firing` and the moved `deadline` are in-memory
  only; the checkpoint's `TimerDescriptor` is untouched.

### §6.2 Cost

One extra bool per hold and a bounded retry cadence per *failing* wake. Nothing
is added to the healthy path — a successful wake does exactly what it does today,
one map delete later.

### §6.3 Rollback path

Single-commit revert; no migration, no persisted-format change.

---

## §7 Related

- [SRD-071 v.1](../srd/SRD-071-instance-dehydration-and-wake-on-trigger.md) — the
  slice this defect belongs to (FR-3/FR-6/FR-7 holders, FR-3a withdraw-siblings).
- [ADR-007 v.2](../design/ADR-007-in-memory-long-waits.md) §2.4 — the
  "never a lost trigger" invariant the defect violates; §5 — multi-node wake
  deferred, which is why no cross-engine reclamation belongs in this fix.
- [ADR-033 v.2](../design/ADR-033-persistence-and-state.md) §2.5 (overdue
  collapses to a single firing — preserved by releasing on success), §2.8
  (leases/fencing, unchanged).
- [SRD-070 v.1](../srd/SRD-070-instance-checkpoint-and-restart-recovery.md) —
  `recoverInstances` at engine start, the crash-case counterpart left untouched.
- **Promote-to-ADR candidate:** *irreversible cleanup is ordered after the
  fallible operation it belongs to* is a general engine invariant. On a second
  site violating it, promote to an ADR rather than a third FIX.

---

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

> ⚠️ TODO: fill AFTER landing; records the implementation history and empirical
> findings vs the §3 draft.

### §8.1 Stages by commit (branch `feat/dehydration`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| 1 | `<sha>` | §3.2.1–§3.2.3 | §4.1 |

### §8.2 Empirical findings — where reality diverged from the §3 draft

> ⚠️ TODO.

### §8.3 Backlog (out of FIX-027 scope)

> ⚠️ TODO.

---

## §9 Open questions

None. The scope is the single ordering defect; the orphan-sweep idea was
considered and rejected (§3.1.C) in favour of removing the reason to scan. The
one tunable — `wakeRetryBackoff`'s default, proposed as a fraction of `leaseTTL`
— is settled at the approval gate.
