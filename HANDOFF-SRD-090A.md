# SRD-090.A — handoff after M2d

**Branch** `feat/node-execution-model`, worktree
`/home/dober/wrk/development/go/src/gobpm/iter-events` (sibling worktree; the
directory name `iter-events` predates the branch rename — cosmetic only).

**Base** `origin/master` = `8532091d`. Three commits ahead: `M2b`, `M2c`,
`M2d`. Nothing pushed.

**Task** issue **#313** — the iteration decorator should own the decorated
node's event registration. This branch is `Part of #313`, not `Closes`: the
refusal #313 describes is retired by SRD-090.**B**, which cannot start until
SRD-090.A lands.

---

## Where the epic stands

The design for #313 is already authored and accepted-in-draft. Four ADRs
(`ADR-025 v.3`, `ADR-006 v.6`, `ADR-013 v.3`, `ADR-036 v.2`) are `Draft` and
flip to Accepted when **SRD-090.C** lands. The implementation is sliced:

| Slice | Subject | State |
|---|---|---|
| **SRD-090.A** | executor/decorator model + the checkpoint record | M1, M2a landed on master (PR #321); **M2b, M2c, M2d landed here**; M3a, M3b, M3c, M4 remain |
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

## Verification at M2d

`make ci` **PASS**, 14/14 steps, head `8d8dc722`, diff coverage **96.9%** of
350 changed lines (floor 95). `.ci/last-run.json` holds the verdict.

## Remaining milestones

M3 lands in three commits (SRD §7 records the split and its reason — the three
drivers it replaces do not share a mechanism, and one commit would mix a
straight conversion with the removal of the loop-owned barrier):

- **M3a — the scope executor, and residency by what an instance awaits.**
  `scopeExec`: one instance of a composite activity — opens its child scope,
  parks for that scope's drain, reports `awaitScope`. The decorator
  generalizes over `activityExec`, and the sequential composite kinds drive
  through it; `runCompositeLoop` and `runMISequential` go. Residency lands
  here because `awaitScope` is what makes it decidable (FR-8): today an
  iterated Sub-Process host disqualifies its whole instance from dehydration
  via `dehydratableParked`'s default arm, so three iterations holding parked
  User Tasks pin it forever. T-1 (composite), T-4 (composite), T-6, T-7.
- **M3b — the parallel composite, and the loop-owned group retired.**
  Parallel composite MI on the same decorator, each instance holding its own
  scope and its own drain — which removes the reason `miGroup` existed: the
  fan-out/re-arm/complete/re-attach handshake serialized N concurrent drains
  onto a cap-1 park that N executors no longer share. Note
  `maybeDehydrate`'s `len(ls.miGroups) > 0` guard goes with it. T-8.
- **M3c — the call executor.** Iterated Call Activity: N children, each
  recorded against its ordinal with `ChildID`, restore re-linking each child,
  cancellation terminating the remaining children. Tests T-9, T-10 — both
  genuinely new (`NewCallActivity` never appears with `WithLoop` today, so
  T-1 is not an oracle here).
- **M4 — the sweep.** Old symbols absent not orphaned; the Schema-5
  compatibility path proven against documents captured by the previous
  release; the §9 absence check enforced; §10 filled.

Then: `/check-srd`, **`/pr-review` (obligatory)**, sync linked docs, and only
then the PR description.

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
- **Never bare `git stash`/`pop`** — other sessions share the stack. Push with
  a unique tag, capture the SHA, `apply` it, then drop by tag.

## Test edits M2b made (T-1 findings, reported and accepted)

Four tests asserted the record shape FR-6 retires, so they were updated rather
than being regressions: `leafMIRec` + `TestLeafMISequentialKillAndResume`
(`.MI` → `.Iteration`), `TestLeafMIParallelKillAndResume` (off `MIGroups` onto
the executor set), `TestHandleFanOutNonMI` (a leaf can no longer fan out), and
`TestSchemaFiveRoundTrip` (schema stamp 5 → 6).
