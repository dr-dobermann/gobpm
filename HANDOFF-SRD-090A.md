# SRD-090.A — handoff after M3b, before M3c

## Opening prompt for the next session

> Continue SRD-090.A on branch `feat/node-execution-model`, in the worktree
> `/home/dober/wrk/development/go/src/gobpm/iter-events` (run everything
> from there; do not `cd` to the main checkout). Read
> `HANDOFF-SRD-090A.md` and `docs/srd/SRD-090.A-node-execution-model.md`
> §7 first. M1–M3b are landed; **M3c is next** — residency by what an
> instance awaits (FR-8), including folding the plain composite in as
> instance zero-of-one and merging `onScopeOpen` into `handleScopeOpen`.
> Then M3d (the call executor), M4 (the sweep + §10), and **SRD-090.B**,
> which is what closes #313. Nothing is pushed and no PR exists; the
> branch's job is not done until B lands, `/check-srd` passes and
> `/pr-review` has been run with its findings addressed.

**Branch** `feat/node-execution-model`, worktree
`/home/dober/wrk/development/go/src/gobpm/iter-events` (sibling worktree; the
directory name `iter-events` predates the branch rename — cosmetic only).

**Base** `origin/master` = `252cbcab` (merged in at `d3e62bf0`, 21 commits
including FIX-041 and #326). Twenty-nine commits ahead, 0 behind, tip
`80d76166`. Nothing pushed, no PR opened.

**Verification** — `make ci` is the gate, and it is judged by
`.ci/last-run.json`, never by an exit code (an absent file means the run did
not finish, which is not a pass). Last full run before this handoff: **PASS**
14/14 at `86a24d4d`, diff-coverage 96.4% of 636 changed lines. For a quick
loop, `rtk proxy go test ./internal/instance/ -count=1` plus `make lint_all`
catch most of what breaks here; `-race` on `./internal/instance/` and
`./pkg/thresher/` is worth running after anything that touches the fan-out.

**M2c is gone — master fixed the same defect first.** `94b88765` on master
("a joining processor's correlation key never reached the broker") is the
same bug this branch's M2c fixed independently, with a fuller design and 651
lines of tests, so M2c was dropped at the merge: `syncWaiterKeys`, the
waiter's `SyncKeys` and both M2c test files are gone, and
`waiters/message.go` is master's. **M2d survives** — master has no
`reflect`/`Comparable` guard, so registering an uncomparable
`EventProcessor` still panics there; its test is now
`eventhub_comparable_test.go`.

**Merge master often from here.** The reconciliation cost real work only
because the divergence ran long enough for one bug to be fixed twice. With
M3b–M4 and SRD-090.B still ahead, merging on every master move is cheaper
than one reconciliation at PR time.

**Settled — the ADR pinning convention.** Master's `1109f750` + `8ffdc288`
say the CITING document decides: SAD/ADR and code comments cite unpinned,
SRD/FIX keep their pins as the one-shot snapshot of what the author read.
SRD-090.A is an SRD, so its pins stand and no branch work is owed.

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
| **SRD-090.A** | executor/decorator model + the checkpoint record | M1, M2a landed on master (PR #321); **M2b, M2d, M3a and M3b landed here** (M2c dropped — master fixed it first); M3c, M3d, M4 remain |
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

**M3b — LANDED.** The parallel composite is decorator-driven and the
loop-owned group is gone. Kept below because the reasoning is load-bearing
for M3c and M3d, which face the same questions.

**What made the group necessary, and what removed it.** A drain used to be
delivered by RESUMING THE HOST TRACK, and a track has one `evtCh` — so
`miGroup` plus its fan-out/re-arm/complete handshake was, at bottom, a queue
feeding one drain at a time into a channel only one waiter could read
(`grp.pending` counted the ones that arrived while the runner was busy).
Restructuring the barrier could never remove that; the delivery target had to
change first. `scopeEntry` now carries the channel of the INSTANCE that
opened it, `completeScope` closes that, and `scopeExec.awaitDrain` waits on
its own — honoring ctx and `loopDone`. Every composite pass in the engine
drains this way now, sequential included.

The four sections below are the order it was built in, and each records a
trap worth not re-entering.

### The seam, landed unrouted at `94de2664`

Problems 1 and 2 plus the seam: `scopeExec` carries `segment`, `binds` and
`capture`; `handleScopeOpen` applies them (falling back to `scopeSegment`
when no segment is named, so the sequential path stayed byte-identical);
`completeScope` calls `captureInstanceOutput` before closing the scope;
`awaitParallel` reads the cell after the instance reports and stages
through the leaf's own `instanceOutputs`.

It landed with **nothing routing a parallel composite to it**, which cost a
red gate at `2cb7fd3a`: diff-coverage 90.5% of 529 changed lines against a
floor of 95, because inert code has no caller and cannot be covered. Lint
and the suite were clean the whole time. Cleared at `dc375cee` with eleven
white-box tests (96.3% of 508) — NOT by relaxing `COVER_MIN` or excluding
the paths. **The lesson worth keeping: do not land a slice ahead of the
thing that routes it unless you will test it white-box in the same
commit.**

### Problems 3 and 5 — landed at `6a5ac97f`, as one lookup

They asked the same question — which scopes does this host still have open
— so `instanceScopesOf(host)` answers both, in ordinal order because a
teardown feeds the ledger the reverse-order compensation sweep reads.
`handleCancelInstances` tears them down and reports the count; the
decorator's `stopRemaining` calls it after `cancelRest()`.

**The trap, avoided:** an instance cannot close its own scope on the way
out. What woke it IS the canceled context, and `scopeRoundtrip` honors
ctx, so the request fails and leaks the very scope it meant to close. The
teardown belongs to whoever cancelled.

### Problem 4 — landed at `86a24d4d`

Restore needs no record of its own. The ordinal is in the scope's segment
(`sp-<id>-<ord>`), so `restoredScopeHost` derives it; N and the staging
already ride the `IterationRecord`. `MIGroupRecord.Open` is therefore dead
weight, not a fact — one less table that can disagree with the scope table.

Four things that reading needs, each of which was a way to get it wrong:

- **Precedence.** `sp-a-1` is both instance 1 of node `a` and the own
  scope of a node named `a-1`; only one can be open, but a single pass
  answered whichever the track table listed first. Own scopes are matched
  across ALL candidates first, and only a `fansOut` node can own an
  instance.
- **The re-attach adopts the output cell**, not just the drain channel —
  a restored entry has neither, and without the cell a resumed instance's
  output is read from nowhere and its slot stays nil.
- **The set is posted BEFORE the instances start** (`runParallel` →
  `postPosition`). The window between activation and the first completion
  recorded an empty set, which restores as "all N still to run". The leaf
  fan-out had the same hole.
- **An instance's drain no longer advances the mirror** (`markIterDrain`
  skips `entry.instance`): the host's loopCounter stands still for the
  whole fan-out.

A document whose scope table and executor set disagree — an instance
recorded completed whose scope is still open — is refused at adoption
rather than half-restored. The reverse window does not exist, because the
drain closes the scope before the decorator reports it.

Also swept, being the same shape as the open-side ordinal fix: the
Completed and Canceled facts reported the host's shared loopCounter for a
fanned-out instance. Both now ask `scopeFactOrdinal`.

### The flip — landed at `80d76166`, and what it exposed

Two edits: `executeStep` (`std_loop.go`) stops routing a parallel composite
to `runMIParallel`, and `execFor` drops the `!composite || mi.IsSequential()`
guard. Every Multi-Instance now reaches `newIterDecorator`.

Doing it last was right — it exposed two defects that only exist once
something routes there, and both came from the same root: the decorator
sets `miState` for a shape that never had one.

1. **`captureSequentialOutput` staged every instance at slot 0.** It keys
   on the HOST's `loopCounter`, which stands still for a whole fan-out, so
   the last instance to drain overwrote the first — `[8,6,8]` where
   `[4,6,8]` was expected, varying run to run. A fanned-out instance stages
   through its own cell, and the serial capture now skips `entry.instance`.
2. **A missing declared `outputDataItem` stopped faulting.** My
   `captureInstanceOutput` tolerated it (matching the leaf's frame capture)
   where the sequential composite faults. Publishing a nil slot makes the
   assembled collection lie about what the instances returned, so it faults
   — the softer rule was mine and it was wrong.

The retirement went in the same commit, because unreachable code is a lint
failure rather than a follow-up: `miGroup` and its registry, the four ops
(`scopeFanOut`/`scopeReArm`/`scopeComplete`/`scopeReAttach`) with their
handlers, `runMIParallel` and its barrier, `openParallelInstance`,
`captureParallelOutput`, `cancelOpenInstances`, `cancelParallelGroup`,
`miParallelSeed`, `awaitScopeDrained`, `scopeEntry.group`, the `MIGroups`
WRITE side and `maybeDehydrate`'s group guard. Net **-570 lines**.

**Schema-5 documents are translated, not refused.** `adoptRestoredGroups`
now reads the retired `MIGroupRecord` and re-expresses it as the instance
entries plus the executor set the decorator expects. The record stays
readable and is documented as write-dead; it can go when schema-5 leaves
support. Because nothing writes it any more, the test that exercises that
path builds it — `asSchemaFive` in `composite_restore_test.go`.

**Tests that drove the retired substrate** are gone where an executor-level
equivalent already exists, and rewritten where the BEHAVIOR survives its
mechanism (the boundary teardown, the publish and bind error paths, the
drain-before-attach hold, the parallel restore suite). One of them,
`pkg/thresher`'s `TestIterationRoutingKillAndResume`, waited on
`doc.MIGroups` and would have burned its full 3s timeout on every run.

### What is left in M3b

Nothing. M3c is next.

**M3c — residency by what an instance awaits (FR-8).** Not a predicate
change. Confirmed by reading the release path:

- `dehydrateCh` is observed ONLY in `awaitTrigger` (`track.go:1124`), so an
  instance parked in `scopeExec.awaitDrain` cannot be released at all today
  — and after M3b that is EVERY composite pass, sequential and fanned-out
  alike, since they all wait on their own channel now.
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
compatibility path proven against documents captured by the previous release
— that path is now `adoptRestoredGroups`, which TRANSLATES an `MIGroupRecord`
into instance entries plus an executor set, and `asSchemaFive` in
`composite_restore_test.go` is what builds such a document to test it, since
nothing writes one; the §9 absence check enforced; §10 filled.

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

## SRD-090.B — ground truth for authoring it (verified at `5e72f073`)

B is what closes #313, and its design is already accepted-in-draft as
**ADR-006 v.6 §2.9.5** (the decorator as subscriber). What follows is the code
it has to be written against — gathered by grep, not from memory.

**The refusal it lifts.** `internal/instance/snapshot/snapshot.go:365` — the
message names #313 verbatim and tells the modeller to "Model it as an iterated
Sub-Process containing the wait". It refuses **leaves only**: a composite
returns nil at `:357`, which is why an iterated Sub-Process containing a User
Task works today and an iterated User Task does not.

**Who owns a wait today — the track, not the instance executor.**
`track.armWaiters` (`track.go:640`) registers the processor:

```go
proc := eventproc.EventProcessor(t)      // the TRACK
if d.Type() == flow.TriggerMessage {
    proc = t.instance                    // ...except a MESSAGE, which
}                                        //    registers the INSTANCE
```

Two consequences B must answer, and they are the substance of it:

1. **A message wait is registered by the shared `Instance`,** not per waiter.
   So N iteration instances parking at one message catch present ONE processor
   to the hub — the joining-processor case, and the reason routing to the right
   ordinal is a CORRELATION question rather than a subscription question. The
   iteration key mechanism already exists (`correlator.iterKeys`, SRD-085
   FR-3), and this session's M2c fixed the two ways a key was silently lost
   in exactly this path. B has to say how an envelope reaches instance *k* and
   not its sibling.
2. **The residency hold is per-TRACK.** `t.held.Store(allHeld && len(defs) > 0)`
   (`track.go:677`) — all of a node's arms must find a holder or the track
   counts unheld. Under executors this becomes per-instance, which is FR-8's
   "releasability over a decorator is the conjunction of its executors'". B and
   M3c meet here, so do M3c first.

**What is already in place**, so B is not starting cold: `awaitEvent` is an
`awaitKind` (its comment already reads "a hub subscription (SRD-090.B arms
it)"); `iterDecorator.refuseIfParked` is the guard written for the day the
refusal lifts, tested for its decision and unreachable until then; and
`holdWait` (`track.go:689`) already offers a definition to durable holders
before any in-hub waiter is made — B changes WHO offers, not the offering.

## Discipline that cost time here — do not relearn it

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
- **A test that waits on a retired record shape HANGS instead of failing.**
  Four of M3b's casualties burned a 3s `require.Eventually`/`select` timeout
  each rather than saying what was wrong, and one lived in `pkg/thresher`,
  a package the `internal/instance` loop never touches. After retiring a
  document field, grep the WHOLE tree for it — including the packages you
  are not working in — and expect timeouts, not assertion diffs.
- **A mechanism swap is not equivalent until it is routed.** Two real
  defects in M3b were invisible while the new path was unrouted and
  appeared the moment it was: both came from the decorator setting
  `miState` on a shape that never had one. Neither lint nor the suite
  could have found them earlier. Flip early enough that the defects
  surface while the mechanism is still fresh in your head.
- **`go test` output is summarized by the rtk hook** — use `rtk proxy go
  test … > log` when you need the actual `--- FAIL` lines and their
  messages, not the count.

## Test edits M2b made (T-1 findings, reported and accepted)

Four tests asserted the record shape FR-6 retires, so they were updated rather
than being regressions: `leafMIRec` + `TestLeafMISequentialKillAndResume`
(`.MI` → `.Iteration`), `TestLeafMIParallelKillAndResume` (off `MIGroups` onto
the executor set), `TestHandleFanOutNonMI` (a leaf can no longer fan out), and
`TestSchemaFiveRoundTrip` (schema stamp 5 → 6).
