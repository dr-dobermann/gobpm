# FIX-006 «OR-join hangs when all branches are taken»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted v.1 (2026-06-20, branch `feat/complex-gateway`, implemented).
**Date:** 2026-06-20.
**Author:** Ruslan Gabitov.
**Branch:** `feat/complex-gateway` (found while implementing the Complex gateway; the OR-join is unaffected by Complex, but the bug surfaced via the shared join machinery, so it lands in the same PR).
**Paired doc:** [SRD-022 v.1](../srd/SRD-022-inclusive-or-join.md) (the Inclusive OR-join this fixes).
**Upstream:** [ADR-005 v.3 §2.10](../design/ADR-005-gateways-and-joins.md) (the synchronizing merge); [SRD-023](../srd/SRD-023-complex-gateway.md) (the Complex gateway, whose trailing-token handling this mirrors for the OR-join).

**Grounded in (internal artifacts):**
- Issue [#155](https://github.com/dr-dobermann/gobpm/issues/155).
- Goroutine dump (`-race`): tracks blocked at `track.go` synchronize park `select` (`parkCh`), instance loop idle on its event `select` — i.e. parked tracks never woken, `active` never reaches 0.
- Reproduced by `TestORJoinAllBranchesArrive` (`pkg/thresher/or_join_test.go`): before the fix it hung ~20–30 % of `-race` runs.

## §1 Symptoms

An Inclusive (OR) diamond where **every** split branch's condition is true — so all
branches fork and arrive at the join — **hangs**: the instance never completes (a
bounded `WaitCompletion` times out).

```
start → OR-split ─┬[x<100]→ a ─┐
                  ├[x>10] → b ─┼ OR-join → end      (x = 50 ⇒ all three true)
                  └[x>=0] → c ─┘
```

The OR-join's *some-branches-taken* cases (fire via reachability) always worked; only
the *all-taken* case hung — which had no test (SRD-022's tests all leave a branch
untaken).

## §2 Root cause analysis

Two distinct defects, both "a parked track is never woken":

### §2.1 Arrival-complete fire never signals parked merged tracks

The OR-join completes two ways. The *reachability* fire goes through
`Instance.fireOrJoin`, which signals every parked track's `parkCh`. The
*arrival-complete* fire (the last arrival marks every incoming flow →
`InclusiveGateway.Arrive` returns `complete` → `track.synchronize` emits `evMerged`)
goes through `Instance.applyMerged`, which flipped the earlier (parked, `AwaitSync`)
arrivals to `Merged` **but never signaled their `parkCh`**. So with all branches
taken, the last arrival completes the join and the earlier two hang on `parkCh`.

A first cut signaled `parkCh` only when the merged track was already `AwaitSync` —
but that **races the track's own transition** into `AwaitSync`: the track returns
from `Arrive` (false), and the loop can process the completing `evMerged` before the
track reaches `updateState(AwaitSync)`, so the gate sees "not parked" and skips it.

### §2.2 A late arrival at an already-fired join is parked, not consumed

A reachability fire can complete the join before a branch that it deemed unreachable
actually arrives (a timing window). That branch's token then reaches the join,
`Arrive` returns "not complete" (the join is `fired`), and `synchronize` **parks** it
on `AwaitSync` — where nothing ever wakes it (it is not in the fired `order`, so no
fire signals it). The OR-join had no **trailing-token** handling — the very thing the
Complex gateway has (`Record` → `firedAlready`, [SRD-023](../srd/SRD-023-complex-gateway.md)).

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Decision |
|---|---|
| Signal merged `parkCh` only when `AwaitSync` (the first cut) | ❌ races the track's transition into `AwaitSync` (§2.1) |
| Add `firedAlready` to `SynchronizingJoin.Arrive` | ❌ churns the contract shared with Parallel + every `Arrive` call site, for an OR-only concern |
| `Fired()` then a separate trailing check off the loop | ❌ racy: a parked in-order track and a trailing one both see "fired" |
| **`applyMerged` wakes merged unconditionally + `ReachabilityJoin.IsTrailing` consumes late arrivals** | ✅ chosen — race-free, OR-scoped |

### §3.2 Changes by file

#### §3.2.1 `internal/instance/instance.go` — `applyMerged` wakes merged unconditionally

`applyMerged` now signals each merged track's `parkCh` **unconditionally** (no
`AwaitSync` gate): a parked track resumes and returns; one not yet at the park
`select` reads the buffered(1) signal when it blocks; a Parallel `AwaitingMerge` track
(goroutine already returned) simply never reads it. This is race-free and covers both
fire paths. `fireOrJoin` drops its now-redundant merged loop (it keeps only the
survivor signal).

#### §3.2.2 `pkg/exec/exec.go` + `pkg/model/gateways/inclusive.go` — `IsTrailing`

`ReachabilityJoin` gains `IsTrailing(arrivingTrackID string) bool`;
`InclusiveGateway.IsTrailing` returns `fired && !slices.Contains(order, trackID)`
under the gateway mutex (so the answer is consistent with the track's own `Arrive`).

#### §3.2.3 `internal/instance/instance.go` — `recheckParked`

The loop's `evParked` case now calls `recheckParked(track)`: if the join `IsTrailing`
for this track (already fired without recording it), the track is consumed — flipped
to `Merged` and woken so its goroutine returns — instead of waiting forever;
otherwise it rechecks the join as before.

## §4 Verification

- **`TestORJoinAllBranchesArrive`** (`pkg/thresher/or_join_test.go`): an OR diamond
  with all three branches taken completes once and runs all three branches. Run under
  `-race` ×25 — 0 hangs (was ~20–30 %).
- Full join suite (`internal/instance`, `pkg/thresher`, `pkg/model/gateways`) green
  under `-race`. Parallel and Complex unaffected (§6).
- **Observability:** the debug-level track-event logging (same PR) makes this class of
  hang self-evident — `parked` events with no following `merged`/`ended`.

## §5 Prevention

- Doc comments on `applyMerged` / `IsTrailing` / `recheckParked` explain the race and
  why the wake is unconditional.
- The all-taken regression test is the canary; the event logging is the standing
  observability for "a join that never fires".

## §6 Regressions / side-effects

- **Parallel** gateway: unaffected — it is a `SynchronizingJoin`, not a
  `ReachabilityJoin`, so `IsTrailing`/`recheckParked` never apply; the unconditional
  `applyMerged` wake is harmless to its `AwaitingMerge` (already-returned) tracks.
- **Complex** gateway: unaffected — it consumes trailing tokens itself in
  `synchronizeActivation` (`Record` → `firedAlready`), so it never parks one.
- Rollback: single-commit revert.

## §7 Related

- [SRD-022 v.1](../srd/SRD-022-inclusive-or-join.md) — the OR-join.
- [ADR-005 v.3 §2.10](../design/ADR-005-gateways-and-joins.md) — the synchronizing merge.
- [SRD-023](../srd/SRD-023-complex-gateway.md) — the Complex gateway, whose
  trailing-token consumption this mirrors for the OR-join.

## §8 Implementation summary

Landed as a single commit on `feat/complex-gateway` (ships in the Complex-gateway PR):

- §2.1 — `Instance.applyMerged` wakes each merged track's `parkCh` unconditionally;
  `fireOrJoin` drops its now-redundant merged loop (survivor-only).
- §2.2 — `exec.ReachabilityJoin.IsTrailing` + `InclusiveGateway.IsTrailing`; the
  loop's `evParked` handler calls the new `Instance.recheckParked`, which consumes a
  late arrival at an already-fired join (else rechecks as before).
- Tests: `TestORJoinAllBranchesArrive` (thresher, `-race` ×25, 0 hangs) +
  `TestORJoinAllArriveInstance` / `TestComplexTrailingInstance` (in-package, for the
  per-package coverage the cross-package thresher tests don't record). `make ci`
  green, diff-coverage 97.4%.

## §9 Open questions

- **None.**
