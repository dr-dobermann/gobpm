# Session handoff — feat/user-task-claim-lifecycle

**Last update:** 2026-07-30
**Goal:** add the human-task ownership lifecycle to gobpm — claim / unclaim / reassign,
strict owner-only completion, and a durable record of who performed each task.

---

## 0. Opening prompt for the new session

> I'm continuing the human-task ownership work in gobpm. Worktree:
> `/home/dober/wrk/development/go/src/gobpm/user-task-claim-lifecycle`, branch
> `feat/user-task-claim-lifecycle` (a sibling worktree, NOT the main checkout — run
> everything from there). Read `docs/analytics/session-handoff-user-task-ownership.md`
> first — full context, what's done, what's left, and the conventions that bit the last
> session. Then verify with `git log --oneline -6` (top commit should be `e1b00ab`
> "feat(instance): M4 — the performer register, served through RUNTIME") and
> `git status` (clean). If anything differs, flag it rather than proceeding. Next task
> is M5 (cancellation/teardown parity for owned tasks) unless I say otherwise.

---

## 1. Project context

gobpm is a BPMN 2.0 engine in Go. This branch implements the ownership half of human
interaction, which ADR-020 v.1 §7 had explicitly deferred ("formal claim/unclaim state
machines — a later human-task-lifecycle ADR if demand appears").

**The key framing, which drove every decision:** BPMN already defines the state being
tracked. §10.3.4.1 **Table 10.14** gives UserTask an **instance attribute**
`actualOwner` — "the 'user' who picked/claimed the User task and became the actual owner
of it". v.1 declared *eligibility* (who may act) but never implemented the attribute
recording who *does*. So this is largely conformance work; only the operations and their
guards are engine choices.

The vendored `docs/bpmn-spec/` extract documents **no instance attributes at all** (it's
generated from an XML metamodel), which is why the gap went unnoticed — a reviewer would
correctly find no ownership slot and wrongly conclude the standard has none. Closing that
extract gap is M6.

---

## 2. Branch & commit history

Base: `origin/master` @ `41c5d0d`. **Not pushed** — no remote contains HEAD.

| SHA | Summary |
|---|---|
| `6705c0b` | ADR-020 **v.2** — the ownership lifecycle decision |
| `c5e9985` | SRD-073 **v.1** — the implementation spec |
| `439163f` | SRD-073 **v.1.1** — corrections M1's implementation forced |
| `21508b0` | **M1** — eligibility frozen at distribution |
| `836eaa1` | **M2** — claim/unclaim/reassign, served without hydration |
| `e1b00ab` | **M4** — the performer register, served through RUNTIME |

Docs are at **ADR-020 v.2** and **SRD-073 v.1**, both still `Draft` — they flip to Accepted at
the PR handover, once all code lands.

**Versioning rule learned here:** a doc that has not been accepted is simply *edited* — no
version bump per change. ADR-020 v.1 was accepted, so v.2 is a real bump; SRD-073 has never
been accepted, so it stays v.1 however many times it changes. Intra-draft version churn was
collapsed away before the flip.

---

## 3. What's done

**M1 — eligibility frozen at distribution** (`21508b0`)

`interactor.Eligibility` + `ResolvedSlot{IDs, Declared}` in `pkg/interactor/eligibility.go`
holds the triad resolved once, at distribution, and owns both the verdict rule and the
denial error. `UserTask.ResolveEligibility` does the resolving; `UserTask.Authorize` keeps
its exact signature and delegates, so its table tests pass untouched. `buildTaskInfo`
resolves; the snapshot rides `TaskInfo.Eligible` into both `taskEntry.eligible` and the
thresher registry. `authorizeTask` and its throwaway scope frame are gone.

*Why frozen:* an owner acquires a **durable** right to complete, so eligibility must stay
true for the task's life. Re-derived from mutable data it isn't a premise — an unrelated
data change could revoke an owner's ability to finish work they hold.

*Fail-closed:* `resolveEligibility` returns `DeniedEligibility()` on failure, never the
zero value — the zero `Eligibility` means "no triad declared", i.e. **open to everyone**,
so a scope failure would have been a silent authorization bypass.

**M2 — the operations** (`836eaa1`)

`Thresher.tasks` grew from `taskID → instanceID` into `taskID → *taskRecord{instanceID,
eligible, owner}`, mutated under the existing `Thresher.m`. `Claim` / `Unclaim` /
`Reassign` are registry-only — no `onTaskInstance`, so they never hydrate a released
instance. `Complete` gained a pre-hydration gate applying eligibility then ownership in
ADR-020 §2.4's order.

*Absorbed more than planned, recorded in SRD v.1.3:* strict completion (FR-4, planned for
M4) lands here because the gate **is** strict completion; birth-ownership (FR-6, planned
as M3) lands here because `registerTask` is the only place a task enters the registry.
**M3 is struck.**

**M4 — the performer register** (`e1b00ab`)

`internal/instance/performers.go` holds node name → completer, exposed read-only as
`RUNTIME/COMPLETED_BY` (one map-valued runtime var, so the name set stays closed), and
carried across a hydrate on the checkpoint. Plus `TaskUnclaimedClass` /
`TaskNotOwnerClass` and a reported `reason` per refusal.

*This was redesigned mid-milestone at the user's suggestion* — it was first a data-plane
commit at root scope. RUNTIME is right because the record is engine-published: a process
must **read** who performed a task and must **not** be able to overwrite it or collide
with it. The rejected design is kept in SRD §4.3 with the two accidental constraints it
forced.

**Verification:** `make ci` green at every commit — lint, race tests, diff-coverage
(98.2%, floor 95), govulncheck, and the ~35-example end-to-end sweep.

---

## 4. What's pending

Nothing is blocked. In order:

| M | Scope | Notes |
|---|---|---|
| **M5** | Cancellation/teardown parity for owned tasks — V19, V20 | Small. `cleanupTask` / `withdrawAllTasks` already drop the registry entry; this is mostly *proving* an owned task is torn down exactly like an unowned one, and that ownership never resists cancellation (ADR §2.1.1) |
| **M6** | `docs/bpmn-spec/` instance-attribute coverage for Table 10.14 | Small, docs-only. The extract's generator (`scripts/gen.py`) only emits model properties — instance attributes need authoring by hand, like `state-machines/` and `semantics/` already are |
| **M7** | Remaining examples on claim-then-complete | Small. `examples/dehydration` and the console driver are already done; check whether any other example completes a user task |
| **M8** | ADR-020 Russian twin → v.2.3 | **Largest remaining, pure translation.** `ADR-020-…ru.md` is 519 lines and must reach ~950 to match. Mechanical — a good candidate for a fresh session |

Then: `/check-srd` audit → fill SRD §10 (files/lines, V-results, milestone SHAs) →
flip **both** docs to Accepted → generate the PR description file → hand over.

---

## 5. Files to read first

1. `docs/design/ADR-020-human-interaction-execution-model.md` — §2.1.1, §2.4.1, §2.4.2,
   §2.5.1–§2.5.3, §2.7 are the ownership decisions. The Document History table explains
   every version bump and why.
2. `docs/srd/SRD-073-human-task-ownership.md` — §7 milestones (M1/M2/M4 marked landed),
   §6 test scenarios, §9 DoD. History rows v.1.1–v.1.6 record every correction the
   implementation forced.
3. `pkg/thresher/tasks.go` — the operations, the gate, the record.
4. `pkg/interactor/eligibility.go` — the frozen triad and the verdict rule.
5. `internal/instance/performers.go` + `runtimevars.go` — the register and its exposure.
6. `pkg/thresher/ownership_internal_test.go`, `ownership_freeze_test.go`,
   `completedby_test.go` — the behavioural contract in executable form.

---

## 6. Conventions that may not transfer

- **Worktree, not the main checkout.** Work in
  `/home/dober/wrk/development/go/src/gobpm/user-task-claim-lifecycle`. The user's
  convention is sibling directories (like `gobpm/dehydration`), **not** `.claude/worktrees/`.
- **Never `git push`.** Absolute, even if asked. Suggest the command and stop. Same for
  `gh pr create` — produce a PR body file and hand over the command.
- **Show every commit message and wait** for approval before `git commit`.
- **No watermarks** anywhere — no Co-Authored-By, no "generated with", no mention of the
  tool in code, docs, or commits.
- **`rtk proxy` for machine-consumable output.** The RTK hook rewrites `git`/`ls`/`grep`
  into token-summarised form. A `git diff > file.patch` produced that way is **not a valid
  patch** — this cost ~400 lines of uncommitted work in this session. Use
  `rtk proxy git diff` when redirecting, and verify saved artifacts before destructive steps.
- **Doc versioning:** bump the version and add a Document History row for every change;
  cross-doc references carry version pins (`ADR-020 v.2`); references go up or sideways
  only — an ADR must never cite an SRD.
- **ADRs get a Russian twin**; recent SRDs and FIXes don't.
- **Status flips at the PR handover**, never mid-implementation.
- **Coverage:** `make ci`'s diff-coverage gate is 95% on changed lines and is a hard gate —
  "defer the branch" is not available; if a branch is hard to reach, restructure so the
  rule is provable in isolation (see `denyByResolutionFailure`).
- Design questions: discuss in prose, not multiple-choice option lists.

---

## 7. Reference-doc status

Not applicable: `doc/reference/` is a Java-project convention and gobpm has none. The
equivalent continuously-current artifacts here are the guides under `docs/guides/`, which
this branch already updated for the ownership lifecycle (`tasks/user-task.md`,
`operating/human-tasks.md`, `extending/task-distributor.md`).

## 8. Verification at session start

```bash
cd /home/dober/wrk/development/go/src/gobpm/user-task-claim-lifecycle
rtk proxy git status -sb                 # clean, on feat/user-task-claim-lifecycle
rtk proxy git log --oneline -6           # top = e1b00ab (M4)
make ci                                  # expect exit 0, diff-coverage ~98% PASS
```

`make ci` takes several minutes (it runs the full example sweep). For a faster check:

```bash
go test ./pkg/thresher/ ./pkg/interactor/... ./internal/instance/
```

---

## 9. Decision tree

- **"Continue"** → M5. Plan it first, get plan approval, implement with tests, `make ci`,
  show the commit message, wait.
- **"Do the twin"** → M8. Pure translation of `ADR-020-…md` into `…ru.md` at v.2.3. Match
  structure section-for-section; a twin is a full translation, not a summary.
- **"Wrap it up"** → run `/check-srd`, fill SRD §10, flip both docs to Accepted, write the
  PR body to a file, hand over `gh pr create --body-file …`.
- **Do NOT** without explicit approval: push, open a PR, merge, rebase, amend a published
  commit, or edit `docs/design/` docs belonging to other features.

---

## 10. Last action

`e1b00ab` — M4 committed, 16 files, +671/−52. `make ci` green. Working tree clean.
Nothing in flight.
