# SRD-090.A — handoff after M3b (first half)

**Branch** `feat/node-execution-model`, worktree
`/home/dober/wrk/development/go/src/gobpm/iter-events` (sibling worktree; the
directory name `iter-events` predates the branch rename — cosmetic only).

**Base** `origin/master` = `8532091d`. Eight commits ahead, tip `0e1a378d`.
Nothing pushed. Last gate: `make ci` **PASS**, 14/14, head `0e1a378d`,
diff-coverage **97.8%** of 506 changed lines.

**Task** issue **#313** — the iteration decorator should own the decorated
node's event registration. **This branch is meant to CLOSE #313.**

An earlier framing had it as `Part of #313`, with the refusal retiring in a
future SRD-090.B. That defers the issue's own subject: SRD-090.A builds the
executor model and the record, and FR-10 deliberately keeps the
`snapshot.go` refusal, so on its own it closes nothing. **SRD-090.B is
therefore in scope for this branch**, and the PR closes the issue.

It is narrower than the four-slice framing suggests. SRD-090.C (token /
incident surfaces) and SRD-090.D (result strategies) do not touch the
refusal — only B does. Its design is already accepted-in-draft as
**ADR-006 v.6 §2.9.5** (the decorator as subscriber), so B is an
implementation spec, not a new design. The model is already built for it:
`awaitEvent` is an `awaitKind` today, and `iterDecorator.refuseIfParked` is
the guard that fires the day the refusal lifts. This session's eventhub
fixes (M2c/M2d) are on the same path — N iteration instances registering
against one shared event definition IS the joining-processor case M2c
fixed.

---

## Where the epic stands

The design for #313 is already authored and accepted-in-draft. Four ADRs
(`ADR-025 v.3`, `ADR-006 v.6`, `ADR-013 v.3`, `ADR-036 v.2`) are `Draft` and
flip to Accepted when **SRD-090.C** lands. The implementation is sliced:

| Slice | Subject | State |
|---|---|---|
| **SRD-090.A** | executor/decorator model + the checkpoint record | M1, M2a landed on master (PR #321); **M2b, M2c, M2d, M3a and M3b's first half landed here**; M3b's fan-out + retirement, M3c, M3d, M4 remain |
| SRD-090.B | registration ownership; the refusal retired — #313's literal subject | not authored |
| SRD-090.C | token / incident surfaces | not authored |
| SRD-090.D | declared result strategies, runtime iteration values | not authored |

Only `docs/srd/SRD-090.A-node-execution-model.md` exists. Its §7 is the
milestone list; §10 (implementation summary) is still `*Filled at landing.*`
and must be filled before the branch is done.

## What landed here

**M2b** (`ebb4a74a`) — a parallel MI **leaf** activity's instances are no
longer tracks and get no scope apiece. The decorator holds one `nodeExec` per
instance and runs the N-of-N barrier as ordinary control flow. Key mechanisms,
all in `internal/instance/activity_exec.go` unless noted:

- **`Frame.BindLocal`** (`internal/scope/frame.go`) — the FR-4 isolation
  vehicle. Binds an instance's `loopCounter` and split input item into the
  frame's props: resolves frame-first, and `Commit` flushes **outputs and puts
  only**, so it never reaches the shared scope.
- **`activityInstance{capture, local, inSet}`** (`internal/instance/track.go`)
  — `executeNodeAs` runs a node AS one instance. `inSet` suppresses the
  track-wide `updateState`/`record`, which the decorator makes once: `record`
  is read-copy-store over an atomic pointer, not a CAS, so N concurrent
  instances would silently lose history entries.
- **`capture`** — a task's `UploadData` resolves its target through the frame's
  scope and mutates it in place, so with no per-instance scope siblings
  overwrite one name. Each instance's output is taken from its own frame
  before commit, and staged by ordinal on the decorator's goroutine alone.
- **Schema 5 → 6** — `IterationRecord`/`IterationInstance` replace the
  `TrackRecord.MI` mirror (sequential leaf) and the `MIGroupRecord` + one
  `TrackRecord` per instance (parallel leaf). Restore relaunches exactly the
  ordinals still running. A Schema-5 document still restores.
- Deleted, not bypassed: `track.leafPlain` and both assignments, the
  parallel-leaf arm of `executeStep`, `openParallelInstance`'s leaf path.

**NFR-1's one named behaviour change**, in the CHANGELOG: an instance's
undeclared writes used to die with its scope and now reach the enclosing one,
last-wins. The declared output collection is unaffected.

**M2c** (`6012af08`) — **the blocker is fixed.** `TestIterationCorrelated`
`Routing` and `TestIterationRoutingKillAndResume` failed with a 3.01s timeout
about 1 run in 3 on untouched `origin/master`. `registerWaiter` subscribes the
broker — reading its processors' declared correlation keys — BEFORE installing
the waiter in the registry, and `AddEventKey` no-ops while the waiter is
uninstalled, so a key a sibling iteration declared inside that window reached
neither and its envelope was buffered unrouted forever. The waiter now re-reads
its processors' keys at both points where the reachable processor set changes
(`syncWaiterKeys` / `messageWaiter.SyncKeys`). The same re-read fixes a second
case that was never a race: a processor **joining** an existing waiter never
contributed its keys at all. A wildcard subscription is deliberately left as
one. 40 consecutive green runs of the pair afterwards.

**M2d** (`8d8dc722`) — found while writing M2c's tests, fixed where found. A
waiter identifies its processors by value (`slices.Index` over the interface),
and Go **panics** rather than reporting false when two interface values of one
uncomparable dynamic type meet, so a host implementing the public
`eventproc.EventProcessor` on a struct with a slice field crashed the hub on
its SECOND registration for a definition. Registration refuses such a processor
at the boundary, naming the type. Identity by `ID()` was rejected: a snapshot
clone preserves element ids, so two instances of one process present distinct
processors carrying the same id.

## Verification at M3a

`make ci` **PASS**, 14/14 steps, head `ff6c1639`, diff coverage **97.7%** of
479 changed lines (floor 95). `.ci/last-run.json` holds the verdict.

## What M3a did (`ff6c1639`)

An instance of a composite activity is a child scope, and it now has an object
that says so.

- **`scopeExec`** (`internal/instance/scope_exec.go`) — opens that instance's
  child scope, parks for its drain, reports `awaitScope` while it holds it.
  The reporting is the part that could not be expressed before: from outside
  the runner's own stack, a host parked for a child's drain is
  indistinguishable from one executing.
- **`leafDecorator` → `iterDecorator`**, indifferent to what an instance IS:
  `buildInstance` returns a `scopeExec` for a composite and a `nodeExec` for a
  leaf. Three leaf-only behaviours are guarded where they occur — the step is
  re-armed per pass only for a leaf, the declared output is taken by the
  decorator only for a leaf (the loop takes a composite's before its scope
  closes), and `exitFlows` differs.
- **`loopDecorator`** (`std_loop.go`) — the composite Standard Loop. A second
  type rather than a flag: the two share no state at all, only the interface.
- **Deleted, not bypassed:** `runCompositeLoop`, `runMISequential`.
  `executeStep` is down to two named exceptions — a LEAF Standard Loop
  (converts with SRD-090.B) and a PARALLEL COMPOSITE MI (M3b).
- **The record followed:** a composite's position is an `IterationRecord`, and
  `iterKindOf` derives the shape from the node instead of the decorator
  posting it. **Nothing writes `TrackRecord.MI` any more** — it survives on
  the read side alone, which IS the schema-5 compatibility path (FR-7).
- **Fixed while here — the restored executor set outlived its activity.**
  `iterSeed` was cleared only inside the parallel fan-out, so a track restored
  on any other iterated kind carried it onward; a restored track finishes its
  recorded activity and walks on, and the next iterated activity's decorator
  would read another activity's ordinals as its own and skip every instance
  recorded complete — silently, producing a shorter result. M2b introduced it
  for sequential leaves; `takeIterSeed` now hands the set over once.

T-1 findings, updated not silently absorbed: `hostMI`
(`composite_restore_test.go`) and the kill-point predicate in
`TestCompositeCallKillAndResume` both read `.MI`; five white-box error-path
tests called the two deleted drivers.

## Remaining milestones

**M3b — the parallel composite, and the loop-owned group retired.** Half
landed (`0e1a378d`); the rest is the fan-out and the retirement.

**Landed — the drain reaches the instance that opened the scope.** The
measured reason the group exists: a drain was delivered by RESUMING THE HOST
TRACK, and a track has one `evtCh`. `miGroup` plus the
fan-out/re-arm/complete handshake is, at bottom, a queue feeding one drain at
a time into a channel only one waiter can read (`grp.pending` counts the ones
that arrived while the runner was busy). Restructuring the barrier could never
remove that — the delivery target had to change first. So `scopeEntry` now
carries the channel of the instance that opened it and `completeScope` closes
that; `scopeExec.awaitDrain` waits on its own channel, honoring ctx and
`loopDone`. Both sequential composite kinds are on it, so every composite pass
in the engine drains this way. A restored pass re-attaches to a loop-rebuilt
entry, which has no channel of its own and adopts the re-attaching executor's.

**Remaining — the fan-out.** `iterDecorator.runParallel` already exists and
drives N leaf instances with an ordinary N-of-N barrier (`awaitParallel`); the
composite case should reach it through `buildInstance`, which M3a put in
place. What still has to be solved, in rough order of care needed:

1. **Per-instance scope segment.** A parallel instance's scope is
   `sp-<id>-<ordinal>` (`openParallelInstance`), a sequential one reuses
   `sp-<id>` every pass. The segment must therefore come from the executor,
   NOT be derived in `handleScopeOpen` — changing the sequential path would
   move data paths, observability facts and restore compatibility.
2. **Output capture.** Loop-side today (`captureParallelOutput`, keyed on
   `entry.ordinal` into `grp.staging`). The entry needs the ordinal and a
   staging target that is not the group — the leaf's `instanceOutputs` is the
   model, but a composite's output lives in its child scope and can only be
   read before that scope closes, so the capture stays loop-side.
3. **Cancellation on a fired completionCondition.** Today one loop-side group
   teardown (`scopeComplete cancel` → `cancelOpenInstances`). It becomes
   per-instance scope cancellation, driven by the decorator the way the leaf
   drives `cancelRest`.
4. **Restore.** `miParallelSeed` + `handleReAttach` → the `IterationRecord`
   the leaf already uses, with each instance re-attaching through
   `handleScopeOpen`'s restored-entry branch (its `entry.host == req.host`
   test needs to become per-path).
5. **Boundary teardown.** `cancelParallelGroup` has a caller in
   `boundary_watch.go` — an interrupting boundary on a fanned-out host
   (SRD-056.A FR-13). It needs the executor equivalent.

Then retire: `miGroup`, `miParallelSeed`, `handleFanOut`, `handleReArm`,
`handleComplete`, `handleReAttach`, `openParallelInstance`,
`captureParallelOutput`, `cancelOpenInstances`, `cancelParallelGroup`,
`markIterDrain`, the `scopeFanOut`/`scopeReArm`/`scopeComplete`/`scopeReAttach`
ops, `scopeEntry.group`/`ordinal`/`awaitAttach`/`drainPending`,
`track.awaitScopeDrained` (only `runMIParallel` still calls it), `doc.MIGroups`
on the WRITE side (the read side stays for schema-5), and `ls.miGroups` with
`maybeDehydrate`'s `len(ls.miGroups) > 0` guard. T-8.

**M3c — residency by what an instance awaits (FR-8).** Not a predicate
change. Confirmed by reading the release path:

- `dehydrateCh` is observed ONLY in `awaitTrigger` (`track.go:1124`), so a
  host parked in `awaitScopeDrained` cannot be released at all today.
- A host must therefore (a) select on `dehydrateCh`, (b) unwind the decorator
  through a sentinel `run()` maps to `TrackDehydrated` rather than
  Canceled/Failed (`discardOrFail` would mis-route it), and (c) be **added to**
  `parked` in `dehydratableParked` rather than skipped — skipping leaves its
  goroutine alive and the instance never fully releases.
- It is releasable by construction: it holds no external wait and its position
  is durable. `waitReleasable` must not be asked — a Sub-Process is not
  `Dehydratable`, which is exactly why it pins today.
- The loop needs to reach the executor: publish it on the track (`t.exec`
  under `t.m`), and call `awaits()` WITHOUT holding the track mutex
  (`nodeExec.awaits` takes it).
- **Restore already works**: `restore.go` does not branch on the recorded
  state, so a dehydrated host rebuilds and re-enters its decorator at
  `miSeed.Completed`, and `handleScopeOpen`'s restored-scope branch
  re-attaches. No new restore code is expected.

T-6, T-7.

**M3d — the call executor.** Iterated Call Activity: N children, each recorded
against its ordinal with `ChildID`, restore re-linking each child, cancellation
terminating the remaining children. T-9, T-10 — both genuinely new
(`NewCallActivity` never appears with `WithLoop` today).

**M4 — the sweep.** Old symbols absent not orphaned; the Schema-5
compatibility path proven against documents captured by the previous release;
the §9 absence check enforced; §10 filled.

Then: `/check-srd`, **`/pr-review` (obligatory)**, sync linked docs, and only
then the PR description.

## A plain composite is instance zero of one (fold into M3c)

A plain (non-iterated) Sub-Process host pins its instance today, and the
reason is a category error rather than a missing capability: it parks as
`TrackWaitForEvent` (`parkScopeHost`), so `dehydratableParked` reaches
`waitReleasable`, which asks the NODE whether its wait kind can be
externalized to a holder. A composite host is not a wait at all — its token
forked into a child scope, and the only real waits are the body's own
event-related tracks, which already answer for themselves. `dehydration_test.go`
covers no composite, so this has never been exercised.

Do NOT answer it by making `SubProcess` implement `Dehydratable`. Route the
plain composite through the executor instead: it is instance zero of one, a
`scopeExec` with ordinal 0 — which is what FR-2 already asks for ("a decorator
when the node carries loop characteristics, a bare executor otherwise"), and
which the plain composite evades today by deciding earlier, in
`enterComposite`. FR-8 then covers iterated and plain composites with one rule
and no special case.

The cost, so it is not assumed free: a plain composite's scope is opened by
the LOOP-driven path (`parkScopeHost` emits `evScopeOpen` → `onScopeOpen`),
not the executor-driven `handleScopeOpen`, and the re-entry queue lives in the
loop-driven one. The two open paths have to merge. That deletes a branch
rather than adding one, but it is real work and belongs in M3c's estimate.

## Discipline that cost time this session — do not relearn it

- **Judge `make ci` by `.ci/last-run.json`, never by an exit code.** A trailing
  `echo` masked a FAIL as exit 0 once here.
- **Use `rtk proxy` for redirected output.** A plain `make ci > log` was
  truncated by the rtk hook and hid the failing test name; the full output was
  in `~/.local/share/rtk/tee/*.log`.
- **Confirm every new test FAILS on the pre-fix code.** Three tests in
  `leaf_iteration_test.go` were vacuous when first written — one asserted
  surviving scopes on a finished run (they are already closed), one read
  `NodeID` from `Fact.Details` where it is a top-level field, and T-11 looked
  fine before it was checked. Stash the implementation, run, restore.
- **`covercheck` reads per-package profiles.** A helper added in package A but
  exercised only by package B's tests counts as uncovered and reddens the
  gate. M2c needed `waiters/message_keysync_test.go` for exactly that reason.
- **`covercheck` is HEAD-based — measure AFTER committing.** A gate run on a
  dirty tree diffs the committed lines against a working-tree profile, and the
  line numbers no longer correspond. M3a's first run reported a bogus 92.2%
  that way; the same tree read 97.7% once committed.
- **Never bare `git stash`/`pop`** — other sessions share the stack. Push with
  a unique tag, capture the SHA, `apply` it, then drop by tag.

## Test edits M2b made (T-1 findings, reported and accepted)

Four tests asserted the record shape FR-6 retires, so they were updated rather
than being regressions: `leafMIRec` + `TestLeafMISequentialKillAndResume`
(`.MI` → `.Iteration`), `TestLeafMIParallelKillAndResume` (off `MIGroups` onto
the executor set), `TestHandleFanOutNonMI` (a leaf can no longer fan out), and
`TestSchemaFiveRoundTrip` (schema stamp 5 → 6).
