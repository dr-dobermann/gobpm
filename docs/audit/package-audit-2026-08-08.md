# Package audit — 2026-08-08

**Method:** `/audit-package` — an external reviewer (Antigravity / `agy`,
`gemini-3.1-pro-high`, `--effort high`) over whole packages, **doc-blind**: it
was given non-test source and nothing else, no ADR, SRD or FIX. Three lenses per
unit — architecture & contracts, correctness & concurrency, failure modes &
testability. Findings are raw model output until verified against the source;
this document records what verification found, including what it **refuted**.

**Baseline:** `11a0321` (master, after FIX-036).

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
| `internal/instance` | 14 034 | **1 / 3** | 2 |
| `pkg/model/activities` · `events` · `data` · `gateways` | 15 562 | maintainer audit | 0 confirmed |

**`internal/instance` is under-covered and must be re-run.** At 584 KB of
source the reviewer fell back to a shell command to page through the prompt,
was auto-denied in headless mode, and returned `status: SUCCESS` with an **empty
body**. Only the architecture lens completed. A naive collector reads that as a
clean package; ours flags it. Splitting the unit in half (330 KB) did not help —
it needs four or more parts, or a different feeding strategy.

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

## 5 Pending verification

The remaining findings are **unverified model output** and must not be treated
as defects until checked. Recorded here so the next pass starts from the list
rather than re-running the sweep. Deduplicated across lenses.

### 5.1 `pkg/thresher`

| Sev | Construct | Claim |
|---|---|---|
| blocker | `handle.go` `InstanceHandle.Cancel` | Cancelling a dehydrated instance has no effect and is lost on the next hydration |
| blocker | `tasks.go` task authorization | The engine mutex is held across authorization, serialising task actions under load |
| blocker | `thresher.go` task registry | Parked human tasks are in-memory only, so they are orphaned across a restart |
| blocker/major | `thresher.go` `RegisterProcess` | A failed `registerStarters` after a successful `unregisterStarters` leaves the key with no starters, and the retry fails on the unwired version — bricking the key |
| major | `observer.go` | Instance-scoped observers are orphaned when an instance dehydrates and rebuilds |
| major | `locked.go` `UnregisterProcess` | Deletes snapshots that dehydrated instances still need in order to wake |
| major | `recovery.go` `recoverOne` | A `Save` transport error is treated as a lost CAS, abandoning the instance |

### 5.2 `internal/scope`

| Sev | Construct | Claim |
|---|---|---|
| blocker | `scope.go` `SnapshotAt` | Not atomic — `namesFrom` then per-name `GetData`, each taking the lock separately; a concurrent `CloseScope` aborts or tears the snapshot |
| blocker | `scope.go` | The plane mutex is held across a call into the host's `RuntimeVarsSupplier` |
| major | `scope.go` `GetDataByID`, `frame.go` | Resolution by ItemDefinition ID iterates a map, so two variables of one type resolve non-deterministically |
| major | `datapath.go` `NewDataPath` | Validates a trimmed path but stores the untrimmed one, so a padded path becomes a distinct map key and breaks `DropTail` |
| major | `scope.go` `getData` | O(N) scan under the global mutex where the map key would serve |
| major | `scope.go` `cloneDatum` | Type-switch on concrete `Clone` signatures — the shape behind the `Property`/`DataObject` bug fixed during SRD-079 |
| minor | `frame.go` `Commit` | `outputs` and `puts` are appended without dedup, emitting an intermediate value that never durably existed |

### 5.3 `internal/eventproc/eventhub`

| Sev | Construct | Claim |
|---|---|---|
| blocker | `eventhub.go` | The hub lock is held across unbounded external operations, including `MessageBroker.Subscribe` |
| blocker | `eventhub.go` | TOCTOU between waiter removal and concurrent registration orphans a registration |
| blocker | `waiters/message.go` | A message that fails processing terminally halts a persistent instance-starter |
| major | `waiters/timer.go` | Cancelled timers are retained until expiry; `time.After` in the loop retains stopped waiters; a delivery failure leaks the registry entry |

### 5.4 `internal/instance`

| Sev | Construct | Claim |
|---|---|---|
| blocker | `adhoc.go` | Double host resume when an Ad-Hoc completion condition fires with `CancelsRemaining` |
| major | `activation.go` `guardEval` | The Complex Gateway guard opens a scope frame per evaluation and never discards it |

Both from the architecture lens alone — the other two lenses did not run.

## 6 Next actions

1. **FIX-037** lands C-1…C-5 (in progress, branch `fix/wake-residency-races`).
2. **Re-run `internal/instance`** with four-or-more-way splitting; two thirds of
   the largest package in the engine is currently unaudited.
3. **Verify §5 in unit-sized batches**, routing each to a FIX or to
   `audit-backlog.md` — several §5 items (the in-memory task registry, snapshot
   deletion vs dehydrated instances) change a contract and are backlog-track,
   not defect-track.
4. **Ask the shape question on every FIX.** Two of the five confirmed defects
   were halves of FIX-036 left behind. Neither `/check-srd` nor `/pr-review`
   asks "where else does this shape occur"; that question belongs in the FIX
   template.
