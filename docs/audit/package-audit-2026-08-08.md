# Package audit — 2026-08-08

**Method:** `/audit-package` — an external reviewer (Antigravity / `agy`,
`gemini-3.1-pro-high`, `--effort high`) over whole packages, **doc-blind**: it
was given non-test source and nothing else, no ADR, SRD or FIX. Three lenses per
unit — architecture & contracts, correctness & concurrency, failure modes &
testability. Findings are raw model output until verified against the source;
this document records what verification found, including what it **refuted**.

**Baseline:** `11a0321` (master, after FIX-036). The remediation branch was
later rebased onto `cdafd20` (SRD-082); none of the five confirmed findings had
been fixed there, and none of §5's pending items were invalidated by it.

**Why an external reviewer.** `/check-srd` asks whether the code matches its
document and `/check-style` whether it matches the house rules. Both are the
author checking the author. FIX-036 passed both at 0 red / 0 yellow with a green
gate, and an external read still found a context leak in `Forget` the whole
chain was structurally unable to look for. This sweep is that read, widened from
a diff to whole packages.

---

## 1 Coverage — and one real gap

| Unit | Non-test LOC | Lenses run | Raw findings |
|---|---:|---|---:|
| `pkg/thresher` | 5 971 | 3 / 3 | 16 |
| `internal/scope` | 1 217 | 3 / 3 | 14 |
| `internal/eventproc/eventhub` (+ `waiters`) | 2 132 | 3 / 3 | 9 |
| `internal/instance` | 14 034 | **3 / 3** (4-way split, round 2) | 28 |
| `pkg/model/activities` · `events` · `data` · `gateways` | 15 562 | maintainer audit | 0 confirmed |

**`internal/instance` was re-run in round 2 and is now fully covered.** The
first attempt fed 584 KB in one prompt; the reviewer fell back to a shell
command to page through it, was auto-denied in headless mode, and returned
`status: SUCCESS` with an **empty body** — which a naive collector reads as a
clean package. Halving it (330 KB) did not help. **Four parts did**: all eight
lenses returned real output, and the package went from 2 findings to 28.

Two thirds of the largest package in the engine had been invisible, and the
signal that it was invisible was a *successful* exit code.

**The model packages were audited by the maintainer, not by `agy`**, on the
reasoning that an external reviewer buys the most where the author's belief is
strongest, and these are the packages the recent work barely touched. Four
candidate defects were investigated and **all four were refuted** (§4).

## 2 What the sweep is worth — read this before trusting a finding

Two properties of the raw output matter more than any individual finding.

**Line citations are unreliable; mechanisms often are not.** Five findings cite
lines that cannot exist — `incident_ops.go:716` in a 44-line file,
`recovery.go:2782` in a 198-line one, `observer.go:1791` in a 193-line one,
`timer.go:1905` in a 537-line one, `handle.go:562` in a 357-line one. In at
least one case the *same* defect was reported by another lens at the correct
location (`incident_ops.go:24`), so the finding was real and the citation
invented. Never route a finding by its line number; find the construct.

**Three lenses duplicate.** `internal/scope`'s non-atomic `SnapshotAt` appears
four times under three titles. Duplication across lenses is a signal of
salience, not of three separate defects — dedupe before counting.

**And convergence is NOT evidence of correctness.** Two independent lenses
reported `restore.go:391` as an inverted correlation check — "missing negation",
"valid triggers rejected" — with high confidence. The function's signature is
`validateAndAssociate(...) (mismatch bool)`: `true` means *belongs to another
conversation*, so refusing on `true` is right. Both lenses reasoned from the
function's NAME, which reads like it returns "valid", and neither read the named
return value.

When lenses share a misleading cue they agree confidently and wrongly. Agreement
raises how much attention a finding deserves; it does nothing for how likely it
is to be true. Only reading the code settles that.

Neither property is a reason to stop using the reviewer. Both are reasons the
skill requires verification before a finding is allowed into this document.

## 3 Confirmed — remediated by FIX-037

Five findings verified against the source at `11a0321`. They reduce to two root
causes and are addressed together in
[FIX-037](../fix/FIX-037-wake-latch-and-retained-handles.md).

| # | Sev | Construct | Confirmed defect |
|---|---|---|---|
| C-1 | blocker | `wake.go` `wakeInstance` | A lost `claimWake` returns `nil` and discards the `PendingTrigger`. The comment claims the in-flight wake will deliver it; that wake carries its own trigger and cannot. Callers read `nil` as success, so a message arriving during a timer wake is permanently lost. |
| C-2 | blocker | `tasks.go` `hydrateForTask` | A lost `claimWake` returns `nil` having neither rebuilt nor pinned, while `residentForTask` documents that the instance "was built already pinned". `onTaskInstance` then unpins a pin it never took. |
| C-3 | blocker | `incident_ops.go` `wakeForIncidentOp` | Rebuilds without taking the latch — the only rebuild path that does not. The repository CAS does not compensate: `claimForWake` **retries** a lost CAS, so two concurrent rebuilds both succeed and two live loops run over one instance. |
| C-4 | major | `locked.go` `trackInstanceLocked` | Overwrites `instanceReg` and drops the previous `stop` without calling it, leaking one child context per dehydration cycle. The rebuild half of the defect FIX-036 §8.2 fixed in `Forget`. |
| C-5 | blocker | `wake.go` `HoldTimer` | Arms `timerSvc.hold` with no confirm-after-arm, so a `ReleaseWaits` landing mid-arm leaves a zombie deadline. The timer half of the defect FIX-036 §1.4 fixed for subscriptions. |

Two of the five are halves that FIX-036 left behind. That is the most useful
thing this sweep produced: a landing that fixes a *shape* should be asked where
else that shape occurs, and neither the FIX process nor the review gate asks it.

## 4 Refuted — with reasons, so they are not re-investigated

| Finding | Verdict |
|---|---|
| `Association.calculate` picks `SourcesIDs()[0]` from a map → nondeterministic source | **Refuted.** `asscConfig.Validate` (`data_options.go:184`) rejects `trans == nil && len(src) != 1`, so the no-transformation path has exactly one source and iteration order is irrelevant. |
| `newAssociation` keys sources by ItemDefinition ID → two same-typed sources silently collapse | **Refuted.** `WithSource` (`data_options.go:158-165`) rejects a duplicate ItemDefinition ID at construction. |
| `NewProperty` does not validate `item` / `state` | **Refuted.** Both are validated one level down in `NewItemAwareElement` (`item.go:187,194`); `state == nil` is a documented default, not an omission. |
| `NewSignal` stores `str *ItemDefinition` unchecked | **Refuted.** A nil structure is legal for a BPMN Signal and is nil-checked at both consumers (`signal.go:122,138`). |
| `pkg/model/data` bypasses `errs` classification in 42 places | **Refuted as a runtime defect.** The sites are model-construction paths, and the one runtime path (`Association.calculate`) is wrapped by `Value()` in a classified `errs.New` with `errs.E(err)`, so the class reaches the caller. Remains a stylistic inconsistency, not a defect. |

`pkg/model/{activities,events,data,gateways}` came through with no confirmed
defect. Complexity — and defects — cluster in `internal/`, not in the
spec-grounded model layer.

## 5 Round-2 verification

Round 2 re-ran `internal/instance` to completion and verified the §5 backlog
against the source. Verdicts below; every finding carries one, including the
refuted.

### 5.1 Confirmed — defect track

| # | Construct | Confirmed defect |
|---|---|---|
| C-6 | `scope.SnapshotAt` | Not atomic: `namesFrom` locks and unlocks, then each `GetData` locks and unlocks. `track.go:1496` states the snapshot runs *"on the track goroutine"* and that *"commits bypass the loop"*, so concurrent mutation is reachable. It defeats the invariant the snapshot exists to provide — "a handler reads the world as the completed activity saw it". |
| C-7 | `scope.GetDataByID` | Resolves by iterating a map, so two scope variables sharing an ItemDefinition ID resolve at random. Reachable: `events/event.go:790` resolves by ID and a model may reuse an ItemDefinition. |
| C-8 | `eventhub.registerWaiter` | Holds the hub's **global lock** across `w.Service(ctx)`, which for a message waiter calls `MessageBroker().Subscribe` (`message.go:222`). A slow or remote broker stalls every hub operation. |
| C-9 | `eventhub.UnregisterEvent` | Releases the lock after its lookup, then check-then-acts (`len(EventProcessors())==0` → `Stop` → `RemoveWaiter`). A concurrent `registerWaiter` can attach a processor in that window; the registration lands on a stopped, unmapped waiter and silently never fires. |
| C-10 | `recovery.recoverOne:71` | Returns `nil` on ANY `Save` error, commented "a lost claim race is the normal outcome". A transport error is indistinguishable from a lost CAS, so a transient failure silently abandons an in-flight instance at startup — and reports success, so nothing logs it. `claimForWake` retries the identical failure; the two paths disagree. |
| C-11 | `InstanceHandle.Cancel` | Calls `h.current().Cancel()` with no durable path. On a dehydrated instance the loop is gone, so the cancel is lost and a later wake resumes as if nothing happened. Incident ops solved this with `WithPendingIncidentOp`; Cancel never got it. |
| C-12 | `RegisterProcess` (`thresher.go:1066-1082`) | If `unregisterStarters(prevLatest)` succeeds and `registerStarters(new)` then fails, the previous version's starters are off the hub and the new version's never went on — **the key has no live starters**. The failed version stays in the registry as latest, so a retry tries to unregister starters that were never registered, gets `ObjectNotFound`, and fails. **The process key is permanently bricked.** |
| C-13 | `tasks.go:219` task authorization | `t.m` is held across `rec.eligible.Authorize(...)`, a host-supplied authorizer. A directory or database lookup there stalls the engine's global registry lock. |
| C-14 | `InstanceHandle.Observe` (`observer.go:85`) | Registers on `h.current()`, the current instance OBJECT. On rebuild the handle is re-pointed but the registration is not carried, so a host observer silently stops receiving facts after the first dehydration while its `Subscription` still looks live. |
| C-15 | `loop.go:681` `msgIdx` | Keyed by message-definition id alone, so two tracks parked on one definition overwrite each other and one never receives. Same class as the "shared catch node" follow-up master filed with SRD-082. |

**C-8, C-13 and FIX-036 §1.5 are one shape in three subsystems**: host code
called while an engine lock is held. Fixing it in the producer did not prompt
anyone to look in the hub or the task path.

**C-12 is a blind spot of FIX-036 M6**, which added the wiring-claim bookkeeping
on exactly this path and never asked what happens to the hub state when the
second call fails.

### 5.2 Refuted — with reasons, so they are not re-investigated

| Finding | Verdict |
|---|---|
| `restore.go:391` inverted correlation check ("missing negation"), reported by **two** lenses | **Refuted.** The signature is `validateAndAssociate(...) (mismatch bool)` — `true` means the trigger belongs to another conversation, so refusing on `true` is correct. Both lenses reasoned from the function's name, not its named return. |
| `DataPath` lacks canonicalization → split-brain scopes | **Refuted as a live defect.** `NewDataPath` does validate the trimmed string and store the untrimmed one, but `Append` trims its tail and every production path goes through `Append`. Only a direct `NewDataPath(" /a ")` reaches it, and nothing does. Latent trap, not a defect. |

### 5.3 Contract track — `audit-backlog.md`, not a FIX

| Finding | Why it is not a defect patch |
|---|---|
| `UnregisterProcess` strands dehydrated instances | It drops every version, while `UnregisterVersion` documents "running instances are unaffected — they keep executing against their own frozen snapshot". True for a RESIDENT instance; a dehydrated one holds no object and needs the registry to rebuild. Deciding whether unregister refuses, retains snapshots, or evicts instances is a contract change. |
| Human tasks are in-memory only, orphaned across a restart | Durable task state is a persistence-model decision (ADR-020 / ADR-033 territory), not a patch. |

### 5.4 Plausible — mechanism credible, not yet settled

Recorded so the next pass starts from the analysis, not from zero.

| Finding | What was established |
|---|---|
| `adhoc.go:208` Ad-Hoc double resume (3 lenses) | The `aborting` guard at `decScope:571` does NOT cover this path — it is set only by the Transaction-abort sweep (`compensation_watch.go:380`). That removes the guard the code might have relied on and makes the mechanism credible. Whether cancelled tracks re-enter `decScope` is untraced. |
| `calls.go:150` unguarded send to `entry.track.evtCh` | `evtCh` IS buffered (`restore.go:359`) and `loop.go:448` shows the loop CLOSES it to wake a parked track — so the likely failure is a panic on send-to-closed, not the reported deadlock. `onWaiting` guards against exactly that hazard elsewhere. The mechanism is real; the reviewer's failure mode is wrong. |

### 5.5 `internal/instance` — 28 findings, unverified

The round-2 re-run produced 28 findings (13 blocker, 15 major) across 8 lenses,
deduplicated. Three cite lines that cannot exist — `escalation_watch.go:1347`
(225-line file), `incident.go:2410` (967), `loop.go:4713` (1361) — the same ~11%
fabrication rate as round 1, so route by construct and never by line.

Two were examined above (§5.2 `restore.go:391` refuted, §5.4 `calls.go:150`
partly). The remaining 25 are **unverified model output** and must not be
treated as defects until checked:

`boundary_watch.go` error catch on an MI host orphaning siblings ·
`loop.go:1151` join re-evaluation deadlock · `escalation_watch.go` multiple
exception tokens, and root-level Event Sub-Processes missed ·
`scope_decorator.go` cancellation leaking requests, and loop-condition
re-evaluation orphaning child scopes · `tasks.go:170` Complete succeeding on a
cancelled track · `transaction.go:25` live tracks running during abort ·
`tasks.go:446` `withdrawAllTasks` blocking the loop · `track.go:721`
`stashTimerPlan` overwriting a node's second timer · `correlation.go:170`
TOCTOU in `validateAndAssociate` · `checkpoint_capture.go` TOCTOU and
non-deterministic payload ordering · `incident.go:490` concurrent retries
duplicating tracks · `mi.go` / `mi_parallel.go` restore-count and fan-out gaps ·
`track.go:287/1021/1045` history race, subscription leak, cancellation
misreported as failure · `std_loop.go:93` `loopCounter` collision.

## 6 Disposition

Landed by [FIX-038](../fix/FIX-038-locks-across-host-calls-and-lost-registrations.md).

| Finding | Route | Where |
|---|---|---|
| C-6 `SnapshotAt` not atomic | fixed | FIX-038 §1.6 |
| C-7 `GetDataByID` non-deterministic | fixed | FIX-038 §1.7 |
| C-8 hub lock across `Service` | fixed | FIX-038 §1.1 |
| C-9 `UnregisterEvent` TOCTOU | fixed | FIX-038 §1.3 |
| C-10 recovery abandons an instance | fixed | FIX-038 §1.4 |
| C-11 `Cancel` on a parked instance | fixed | FIX-038 §1.10 |
| C-12 a failed registration bricks the key | fixed | FIX-038 §1.5 |
| C-13 engine lock across `Authorize` | fixed | FIX-038 §1.2 |
| C-14 `Observe` on the instance object | fixed | FIX-038 §1.8 |
| C-15 `msgIdx` keyed by definition alone | filed | issue #305, with SRD-082 |

Three defects not in this audit were found while fixing the ones that were, and
landed with them: the plane lock spanning the runtime-variable supplier and
`Shutdown` reporting under the hub lock (§1.9), the incident path dropping the
handle's observers (§1.11), and an operator request reporting success after
shutdown (§1.12).

## 7 Next actions

1. ~~**FIX-038 for the confirmed defect track** — C-6…C-15.~~ Landed; see §6. They group naturally:
   the host-code-under-an-engine-lock shape (C-8, C-13), the hub's registration
   TOCTOU (C-9), the silently-abandoned recovery (C-10), the bricked process key
   (C-12), and the identity-vs-object leaks (C-11, C-14).
2. **Verify the 25 `internal/instance` findings** (§5.5) in unit-sized batches,
   routing each to a FIX or to `audit-backlog.md`.
3. **File the contract-track items** (§5.3) as backlog entries with their
   governing docs named.
4. **Ask the shape question on every FIX.** Three of this round's confirmed
   findings are the same shape as a defect already fixed elsewhere — host code
   under an engine lock (FIX-036 §1.5 → C-8, C-13) — and C-12 is a blind spot of
   FIX-036 M6, which touched that exact path. Neither `/check-srd` nor
   `/pr-review` asks "where else does this shape occur"; it belongs in the FIX
   template.
5. **Read a finding's signature before its name.** The one refutation that cost
   real time (§5.2) fooled two lenses because `validateAndAssociate` reads like
   it returns "valid" while its named return is `mismatch`. Convergence across
   lenses is salience, never correctness.
