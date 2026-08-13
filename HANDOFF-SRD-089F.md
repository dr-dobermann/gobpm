# Handoff — gobpm, `feat/bpmn-import-elements`, SRD-089.F M3

Date: 2026-08-13 · Branch is **local only** (no upstream, no remote contains HEAD).

---

## Opening prompt for the next session

> Continue SRD-089.F on branch `feat/bpmn-import-elements` in the worktree
> `/home/dober/wrk/development/go/src/gobpm/bpmn-import-elements`. Read
> `docs/srd/SRD-089.F-bpmn-import-data-elements.md` first — it is the approved
> spec. M1 and M2 are landed. M3 (`<dataObject>` / `<dataObjectReference>`) is
> **code-complete and uncommitted**: it builds, lints clean and every existing
> test passes, but it has no tests of its own yet. Write M3's tests, get the
> commit message approved, land it, then M4–M6.

---

## Where the work is

The engagement: full BPMN XML → gobpm import, split across **SRD-089.A/.B/.C/.D/.E/.F**
(+ a planned **.G**), all landing on **one branch** with **one PR**.

- **.A–.E — landed** on this branch (parser spine, dialect, flow nodes, typed
  events, containers/lanes). `.E` §10 is filled.
- **.F — in progress.** Scope: the data *elements*. Stage `.G` (the data
  *flow* — `ioSpecification`, `dataInput`/`dataOutput`, both association kinds)
  is a separate SRD on the **same branch and same PR**; the split was approved
  this session.

45 commits ahead of `origin/master`. The last five:

| SHA | What |
|---|---|
| `8acc7d25` | M2a — closed 13 error paths, converted 6 unreachable guards to `errs.Invariant` |
| `8fb92337` | M2 — `<itemDefinition>` + `<import>` |
| `97ee0179` | M1 — § pins corrected (`§10.4.1`), invented pins dropped |
| `e19110f6` | SRD-089.F authored |
| `372878ea` | SRD-089.E §10 implementation summary |

**Working tree:** four files of uncommitted M3 work —

```
 M pkg/convert/bpmn/bpmn.go        # the five tag/attr constants
 M pkg/convert/bpmn/dispatch.go    # ctxData + the init() registration
 M pkg/convert/bpmn/importer.go    # assembly.datas + the build() call
?? pkg/convert/bpmn/dataobject.go  # the milestone itself, ~340 lines
```

It **builds**, `make lint_all` is **0 issues**, and every existing test passes.
An ad-hoc smoke run (written, executed, deleted) confirmed the whole path
works end to end on this document:

```xml
<bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>
<bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>
<bpmn:dataObjectReference id="dor1" dataObjectRef="do1">
  <bpmn:dataState name="Approved"/>
</bpmn:dataObjectReference>
```

→ `DataObjects()` has **one** entry, `"order"`, id `do1`, item `idOrder`, value
`""`; `Dropped` has exactly one entry, `dor1 / dataState`. That is FR-3, FR-4
rules 1/3/4 and §4.7 all behaving as specified.

---

## Milestones (SRD-089.F §7)

| # | Scope | State |
|---|---|---|
| M1 | The § pins: `sections` and the extract's index (FR-8) | ✅ `97ee0179` |
| M2 | `<itemDefinition>` and `<import>` (FR-1, FR-2, FR-7) | ✅ `8fb92337` |
| M2a | *(unplanned)* error paths + unreachable guards | ✅ `8acc7d25` |
| M3 | `<dataObject>` and `<dataObjectReference>` (FR-3, FR-4) | 🚧 **here** |
| M4 | `<dataStore>` and `<dataStoreReference>` (FR-5) | ⬜ |
| M5 | `<property>` on process, activity and event (FR-6) | ⬜ |
| M6 | `<dataState>` reporting + the refusal-wording sweep (§4.9) | ⬜ |

---

## Finishing M3 — what is left

The code and its wiring are done. `dataobject.go` holds `dataSpec`,
`parseDataElement`, `parseDataBody`/`parseDataChild`, `buildDataElements`,
`dataBuilders`, `buildDataObject`, `buildDataObjectReference`, `itemFor`,
`copyItem` and `dataStateLoss`; the three edited files carry the constants,
`ctxData`, the `init()` registration, `assembly.datas` and the `build()` call.

What remains is **`dataobject_test.go`**, then `make lint_all`,
`make test-all && make cover-check`, then the approved commit.

How the registration works, since it is not obvious: the two elements are
registered into `processParsers` from `init()` (`for local := range
dataElements`), *not* into a table of their own. A container's children are
dispatched through `processParsers` too, so a `<dataObject>` inside a
`<subProcess>` works with no second registration and the two cannot drift.

### Design decisions already made in that file (do not re-litigate)

- **A reference contributes no element.** `buildDataObjectReference` returns
  `(nil, nil)` and `buildDataElements` skips a nil — that is SAD-001 §14.1
  rules 1 and 3 (collapse into the referenced object). It still **fails** on a
  missing/dangling/wrong-kind `dataObjectRef`.
- **Each data element gets its OWN `ItemDefinition` copy** (`copyItem`), same
  id, cloned structure. Sharing the pointer would make two data objects one
  variable — the structure *is* the value (ADR-010). The id is preserved
  because `DataObject.AssociateTarget` matches on it (`data_object.go:151-160`),
  which is what .G will need.
- **An element naming no `itemSubjectRef`** gets `emptyItem(s.id)` — every
  constructor refuses a nil `ItemDefinition`, and BPMN permits `itemSubjectRef`
  at 0..1.
- **`itemSubjectRef` on a *reference*** is reported, not obeyed: BPMN says a
  reference takes its type from the object (`semantics/data.md:64`).
- **`<dataState>`** is reported via `dataStateLoss` and never mapped (§4.7).
- **The builders take `*dataSpec`, not `dataSpec`**, and `buildDataElements`
  indexes rather than ranges by value — `gocritic/rangeValCopy` flagged the
  136-byte copy, and `buildNodes` already indexes its specs for the same
  reason. Keep it that way.

### Test scenarios still owed for M3 (SRD-089.F §6)

T-8 (object on a process), T-9 (unnamed → id), T-10 (inside a `<subProcess>`),
T-11 (two references → one object), T-12 (dangling `dataObjectRef`), T-13
(`<dataState>` reported), T-19 (name collides with a property — deferred to M5),
**T-19a** (`<dataObject name="Order.v2">` → the model's reserved-character
message, `data/name.go:23-32`), T-22 (duplicate id vs a node), T-23 (dialect
attribute on a `<dataObject>`).

---

## Verification — the exact commands, and two traps

```bash
cd /home/dober/wrk/development/go/src/gobpm/bpmn-import-elements
go build ./...
make lint_all                      # golangci-lint, must be "0 issues"
make test-all && make cover-check  # NOTE the ordering — see trap 2
make ci                            # the full 14-step gate before any PR
```

**Trap 1 — the gate verdict lives in `.ci/last-run.json`, never in an exit
code.** Absent file = the run did not finish; it is never a pass. Do not use
`pgrep -f 'make ci'` for liveness (the pattern matches the checking command).

**Trap 2 — `make cover-check` reads `coverage.txt` and does NOT regenerate
it.** A stale profile is a well-formed profile: it reports a confident number
for code it never ran and still says PASS. This cost a false premise in this
very session — a bare `cover-check` said `95.3%, lanes.go 81.4%` off a
12-hour-old profile; after `make test-all` the same commit measured **97.1%,
lanes.go 88.5%**. Also remember covercheck is **HEAD-based**: it diffs the
*committed* branch, so measure after committing a milestone.

Current gate state at `8acc7d25`: diff-coverage **98.6% of 1596 changed lines**
(min 95). `make ci` has not been run since M1 — run it before the PR.

---

## Conventions that bind this work

- **Never `git push`, never open a PR.** Supply the command text; the user runs
  it. PR body goes to a scratchpad markdown file, delivered via file, with a
  ready `gh pr create --title "…" --body-file <path>`.
- **Show the full commit message and wait for approval** before every
  `git commit`. Approval of a plan is not approval of a message.
- **No watermarks** anywhere — no Claude/Anthropic/AI/"generated"/"co-authored"
  in commits, code, docs, identifiers, string literals or the PR body.
- **`--amend` only while unpushed.** This branch has no upstream, so amending
  is currently safe; re-check with `git status -sb` each time.
- **No pre-existing errors, no deferrals.** Anything found mid-milestone gets
  fixed in this branch as its own milestone (that is what M2a was), then the
  remaining plan is re-evaluated.
- **Evidence-first docs.** Every standard-claim cites `docs/bpmn-spec/` by
  section or a repo `file:line`. An un-pinned claim is a review finding.
- **Doc-reference convention:** SAD/ADR references and code comments drop the
  version pin; **SRD and FIX references keep theirs**.
- **Data declaration over code** — a fixed enum→value mapping is a
  package-level map, not a `switch`.
- **`rtk proxy <cmd>`** whenever output is piped or redirected.

---

## Before the PR (SRD-089.F §9 + the standing rules)

1. Finish M3–M6, then author and land **SRD-089.G** (data flow).
2. Fill **SRD-089.F §10** (implementation summary) — and .B/.C/.D §10 are
   still `_Filled at landing._`.
3. `make ci` green, judged by `.ci/last-run.json`.
4. **`/pr-review`** — obligatory before every PR, no exceptions. Its findings
   land *before* the PR description is written.
5. Update `CHANGELOG.md` `[Unreleased]`.
6. Sync linked docs (grep for changed symbols, renumbered sections, doc numbers).
7. `git fetch && git merge origin/master` — the user asked for master to be
   merged and conflicts resolved **before** preparing the PR. It was done once
   this session (clean, at `4acebc00`); repeat it, master may have moved.
8. Then the PR body file + the `gh pr create` command, handed over.

---

## Open items

- **Issue #324** — asked twice whether to close as won't-do or re-scope after
  ADR-028 §2.7 turned out to have already decided the transaction `method`
  question. **No answer yet.** Do not touch the issue without the user's word.
- **Lane/LaneSet and Collaboration § pins** — deliberately removed in M1
  because the vendored extract pins neither and the BPMN NotebookLM notebook
  needs an interactive Google login (`get_health` → `authenticated: false`).
  Recorded in SRD-089.F §4.10. If the user runs `setup_auth`, pin them
  properly and restore the rows.
