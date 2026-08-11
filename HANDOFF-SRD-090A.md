# SRD-090.A — handoff after M2b

**Branch** `feat/node-execution-model`, worktree
`/home/dober/wrk/development/go/src/gobpm/iter-events` (sibling worktree; the
directory name `iter-events` predates the branch rename — cosmetic only).

**Base** `origin/master` = `8532091d`. One commit ahead: `M2b`. Nothing pushed.

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
| **SRD-090.A** | executor/decorator model + the checkpoint record | M1, M2a landed on master (PR #321); **M2b landed here**; M3a, M3b, M4 remain |
| SRD-090.B | registration ownership; the refusal retired — #313's literal subject | not authored |
| SRD-090.C | token / incident surfaces | not authored |
| SRD-090.D | declared result strategies, runtime iteration values | not authored |

Only `docs/srd/SRD-090.A-node-execution-model.md` exists. Its §7 is the
milestone list; §10 (implementation summary) is still `*Filled at landing.*`
and must be filled before the branch is done.

## What M2b did (commit 1 on this branch)

A parallel MI **leaf** activity's instances are no longer tracks and get no
scope apiece. The decorator holds one `nodeExec` per instance and runs the
N-of-N barrier as ordinary control flow. Key mechanisms, all in
`internal/instance/activity_exec.go` unless noted:

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

## Verification at the end of M2b

- `internal/instance` green at `-race -count=5`, `GOMAXPROCS=4`.
- `make lint` 0 issues; `gofmt` clean.
- **Diff coverage 96.1%** of 283 changed lines (floor 95) — PASS.
- `make ci` **RED at `test-core`** — see the blocker below.

## BLOCKER — the next milestone (M2b-a)

`TestIterationCorrelatedRouting` and `TestIterationRoutingKillAndResume`
(`pkg/thresher/delivery_payload_test.go`) fail intermittently with a **3.01s
timeout**. **Measured pre-existing**: on base `8532091d` untouched, 1 failure
in 3 runs of `GOMAXPROCS=4 go test -race -count=1 ./pkg/thresher/`. On M2b, 2
in 3 — noise at n=3.

It is NOT M2b's: the fixture is a parallel MI **Sub-Process** (an iterated
composite), whose execution path M2b does not modify.

Fix it here as its own milestone before anything else — it blocks the gate and
makes every future gate result ambiguous.

Already ruled out: `awaitParked` waits on token state rather than on the
subscription, but the broker **buffers** an unmatched envelope and drains it on
`Subscribe` (`pkg/messaging/membroker/membroker.go:204-206`, `:230`), so an
early publish is not lost. Cause still open.

## Remaining milestones after M2b-a

- **M3a — the sub-process executor.** Composite Standard Loop and composite MI
  (both kinds) onto executors; `runCompositeLoop`, `runMISequential`,
  `runMIParallel`, `runStandardLoop` (leaf Standard Loop) and `miGroup` retired
  with the loop-side drain accounting; residency by `awaits()` (FR-8);
  composites into `IterationRecord`. Tests T-1, T-4 (composite), T-6, T-7, T-8.
- **M3b — the call executor.** Iterated Call Activity: N children, each
  recorded against its ordinal with `ChildID`, restore re-linking each child,
  cancellation terminating the remaining children. Tests T-9, T-10 — both
  genuinely new (`NewCallActivity` never appears with `WithLoop` today, so T-1
  is not an oracle here).
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
- **Never bare `git stash`/`pop`** — other sessions share the stack. Push with
  a unique tag, capture the SHA, `apply` it, then drop by tag.

## Test edits M2b made (T-1 findings, reported and accepted)

Four tests asserted the record shape FR-6 retires, so they were updated rather
than being regressions: `leafMIRec` + `TestLeafMISequentialKillAndResume`
(`.MI` → `.Iteration`), `TestLeafMIParallelKillAndResume` (off `MIGroups` onto
the executor set), `TestHandleFanOutNonMI` (a leaf can no longer fan out), and
`TestSchemaFiveRoundTrip` (schema stamp 5 → 6).
