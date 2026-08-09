# SRD-087 — Recovery affinity: a call tree recovers as a unit

| Field | Value |
|---|---|
| Status | Accepted (2026-08-09) |
| Date | 2026-08-09 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-033 v.5](../design/ADR-033-persistence-and-state.md) §2.10 (recovery affinity) |
| Upstream | [ADR-033 v.5](../design/ADR-033-persistence-and-state.md) §2.8 (claim-first, group-scoped recovery — unchanged), [ADR-023 v.3](../design/ADR-023-sub-process-and-call-activity.md) §2.7 (the restart contract affinity keeps satisfiable) |
| Related | [SRD-082](SRD-082-checkpoint-composite-fidelity.md) (durable children and the re-attach seam; frozen one-shot), [SRD-078](SRD-078-postgres-repository-adapter.md) (the engine-group recovery listing) |
| Tracking | #308 (the affinity slice; the cross-engine re-link itself stays open there) |

Recovery claims one instance at a time, so two engines of one group can
split a parent from the child it awaits. Each recovers correctly; the
parent's re-attach then searches only its own engine and refuses. The
failure is manufactured by claim granularity, not by broken state.
This SRD makes a **call tree the unit of a claim**.

## §1 Background (verified)

- **The sweep is per-instance and unordered.** `recoverInstances`
  (`pkg/thresher/recovery.go:21-37`) lists the group's claimable ids
  (`repo.ListInFlight`) and calls `recoverOne` for each, in listing
  order — nothing relates a parent id to its children's.
- **The claim is per-record.** `recoverOne` (`:40-75`) CAS-claims one
  record under a higher incarnation; a lost race is the normal
  outcome, so whichever engine reaches a record first owns it —
  independently for a parent and its child.
- **The re-attach is engine-local.** `reattachChild`
  (`pkg/thresher/invoker.go:171-216`) resolves a recorded child from
  the LOCAL registries — resident instance, the settled registry, or
  a terminal repository record — and otherwise refuses
  (`ObjectNotFound`). A child claimed by another engine is live
  (non-terminal) and not local, so the refusal fires on a healthy
  pair.
- **The child half already refuses too.** `recoverOne` (`:84-96`)
  fails a child whose parent record is gone; with a split pair the
  parent record exists, so the child recovers and then runs while its
  caller's restore has failed — a half-recovered tree.
- **The linkage needed is already recorded.** The parent's document
  carries `Calls []CallRecord{ChildID, NodeID, TrackID}` and the
  child's carries `ParentID`/`CallNodeID` (SRD-082 FR-7, schema 4) —
  affinity needs no new recorded state.
- **Origin.** #308, filed from the #277 review; the persistence guide
  documents the limit ("keep call pairs on one engine, or in one solo
  group, until it lands").

## §2 Requirements

- **FR-1 — a child is never revived on its own.** The sweep drops
  every id whose record names a `ParentID`, unconditionally: recovery
  reaches a child only through its caller's claim (FR-2). This is
  stronger than deferring only when the caller happens to be in the
  same listing — the split class disappears instead of narrowing, and
  no outcome depends on two ids coinciding in one listing.
- **FR-2 — the claim is transitive over the call tree.** After
  claiming a parent record, recovery claims each `CallRecord.ChildID`
  the document names — and recursively theirs — **before** restoring
  the parent, so every awaited child is engine-local when the parent's
  re-attach runs. Each claim is the same CAS under a higher
  incarnation (§2.8), so a genuinely-held child simply is not claimed.
- **FR-3 — an unclaimable child fails the tree loud.** When a child's
  lease is live (another engine is actually running it), the parent's
  recovery fails with a message naming the child, the holding engine
  and the affinity rule — the same per-instance recovery failure
  (§2.5), never a silent half-recovery. The child itself is untouched.
  Under FR-1 no engine claims a child on its own, so in a healthy
  group this is unreachable; it stays as the **defensive guard** for
  records written before affinity and for a misbehaving store.
- **FR-6 — a terminal caller finishes the cascade.** A parent
  completes only after its call returns, and a terminating parent
  terminates its children (ADR-023 v.3 §2.7), so a **terminal** caller
  with a live child is an interrupted cancel cascade. Recovery
  finishes it: the child's record is written terminal
  (`StatusTerminated`) with a loud fact naming the caller — never
  revived (its outcome has no consumer), never left (a permanent
  resident of every later listing). The pre-affinity behavior — the
  child recovered and ran on — is the defect this closes.
- **FR-4 — the restore order follows the claim order.** The claimed
  children are restored (and tracked) **before** the parent, so the
  parent's `reattachChild` finds a resident child rather than a
  lazily-settled one — the strongest of the three re-attach shapes,
  and the one a split made impossible.
- **FR-5 — cycles and repeats are safe.** A tree walk visits each id
  once (a `seen` set): a malformed document naming its own ancestor
  cannot loop the sweep, and a child listed twice is claimed once.
- **NFR-1** — no new recorded state and no `Repository` API change:
  affinity reads what SRD-082 already records.
- **NFR-2** — race-clean; diff-coverage ≥95% (aim 100%).

## §3 Models

No new types. The sweep gains an ordering pass and the claim gains a
recursion:

```go
// pkg/thresher/recovery.go (SRD-087)
// recoverInstances: parents first, children deferred to their parent's
// claim (FR-1); recoverOne: claim → claim the tree (FR-2) → restore
// children → restore the parent (FR-4).
type claimedTree struct {
    records map[string]repository.InstanceRecord // id → claimed record
    order   []string                             // children before parents
}
```

**Worked trace.** Engines A and B share a group; the store holds a
parked caller `P` (its document naming child `C`) and `C` itself, both
leases expired. A's sweep lists `[C, P]`, defers `C` (its `ParentID`
is `P`, and `P` is in the listing), claims `P`, then transitively
claims `C` — B's sweep, arriving after, finds both leases fresh and
recovers nothing. A restores `C` first, then `P`, whose
`reattachChild` finds `C` resident. Before affinity: A claims `C`, B
claims `P`, B's re-attach refuses — `C` runs on, `P` never resumes.

## §4 Analysis & decisions

- **Order by the listing, not by a store query.** The partition uses
  the records the sweep already loads; asking the store for "the
  children of X" would add a `Repository` method (an adapter contract
  change) for information the documents already carry.
- **Claim before restore, restore children before the parent.** The
  claim is cheap and revocable-by-CAS; restoring the parent first
  would re-open the same race inside one engine (its re-attach would
  run before the child exists locally).
- **Fail the tree, not the child.** An unclaimable child is a live
  instance doing legitimate work; tearing it down to satisfy a
  parent's recovery would destroy running state. The parent's
  recovery fails loud and the operator sees a real conflict.
- **Finish the cascade rather than revive or leave (FR-6).** Reviving
  a child whose caller is terminal — today's behavior, since
  `recoverOne` checks the caller's EXISTENCE and not its state — runs
  an instance whose completion nothing will consume. Leaving it makes
  it a permanent listing resident re-reported on every sweep with
  nothing for an operator to decide. Terminating it completes the
  cascade the crash interrupted; recovery already mutates records (the
  claim CAS), and the fact keeps it visible.
- **No distribution policy.** Affinity does not balance, migrate or
  prefer engines — it only widens one claim. Anything else belongs to
  the distribution work (ADR-033 §5).

## §5 API deltas

None. `Repository` is untouched; `recoverInstances`/`recoverOne` are
internal.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | two engines, one call pair (`pkg/thresher`) | FR-1/FR-2/FR-4: the pair lands on ONE engine and the parent resumes through the re-link — the test fails on pre-affinity code |
| T-2 | child listed first (`pkg/thresher`) | FR-1: listing order does not decide — a child met first is deferred, not claimed |
| T-3 | grandchild tree (`pkg/thresher`) | FR-2: the claim is transitive over two levels |
| T-4 | unclaimable child (`pkg/thresher`) | FR-3: a live-leased child fails the parent's recovery loud, naming the child and the holder; the child keeps running |
| T-5 | terminal caller (`pkg/thresher`) | FR-6: a child whose caller is terminal is written terminal with its fact — never revived, never left in flight |
| T-6 | cyclic document (`pkg/thresher`) | FR-5: a document naming its own ancestor terminates the walk |

## §7 Milestones

- **M1 — the ordered sweep + transitive claim.** FR-1…FR-5; T-1…T-6.
  `feat(thresher): a call tree recovers as a unit — recovery affinity (SRD-087 M1)`.
- **M2 — docs.** The persistence guide's limit replaced by the
  affinity rule (with the remaining cross-engine non-goal named),
  CHANGELOG.
  `docs: recovery affinity in the persistence guide and CHANGELOG (SRD-087 M2)`.

## §8 Cross-doc

- Implements **ADR-033 v.5** §2.10 (affinity); upstream §2.8
  (unchanged) and **ADR-023 v.3** §2.7.
- Related: **SRD-082** (the recorded linkage and the re-attach seam),
  **SRD-078** (the group-scoped listing).
- **#308**: closes the affinity slice; the cross-engine re-link
  (remote child handles, cross-engine completion/cancel) stays open
  there as the §5 distribution concern ADR-033 v.5 names.

## §9 Definition of Done

- [x] FR-1…FR-6 implemented; every §6 test exists and passes.
- [x] `make ci` green; diff-coverage ≥95% (aim 100%).
- [x] T-1 demonstrably fails with the affinity ordering reverted.
- [x] The persistence guide no longer tells operators to keep call
      pairs on one engine by hand.
- [x] §10 filled.

## §10 Implementation summary

Landed on `feat/composite-followups` — doc `7a90124`, M1 `db0ee95`
(the partition and the transitive claim), **M1a `a2fb81c`** (the
stronger rule and the defect it exposed), M2 `b158a99` (the guide and
CHANGELOG).

Verification: `make ci` exit 0 end to end; **diff-coverage 96.3% of
592 changed coverable lines** (min 95%); suites race-clean;
golangci-lint incl. tests 0 issues; `recoveryRoots` 100%,
`finishOrphanedChild` 84.6%, `recoverCallTree` 84.2%.

**The DoD's honesty check earned its place.** T-1's first version
booted two engines in sequence and passed WITHOUT affinity — two
sequential sweeps never interleave, so nothing split either way and
the test proved nothing. Rewritten around the real discriminator (a
listing that offers the caller but not the child) it fails with the
transitive claim reverted — a 5.13s timeout on a lazy handle that
never settles — and passes with it.

**FR-1 and FR-6 are M1a, not the original draft.** M1 deferred a child
only when its caller happened to be in the same listing; review made
the rule unconditional (a child is never revived on its own), and that
exposed a defect the weaker rule hid: `recoverOne` checked the
caller's EXISTENCE and never its state, so a child whose caller had
already finished was revived to run into a caller that completed long
ago. Recovery now finishes that interrupted cancel cascade instead.
T-5 was inverted accordingly — it had pinned the old behavior as
correct.

## Open questions

*None — §4 records the resolved design points (ordering source, claim
vs restore order, failing the tree not the child, no distribution
policy).*
