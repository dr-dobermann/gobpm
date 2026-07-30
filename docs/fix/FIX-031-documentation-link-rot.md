# FIX-031 — Documentation link rot: retired, renamed and mis-depthed cross-references

**Type:** FIX (documentation hygiene).
**Status:** Implemented (2026-07-30; pending landing).
**Date:** 2026-07-30.
**Author:** —
**Paired doc:** none.
**Upstream:** surfaced while verifying the SRD-051 / FIX-030 documentation
change-set; no prior backlog item.

## §1 Symptoms

78 relative Markdown references across 14 documents pointed at paths that do
not exist. Every one of them silently 404s on GitHub and in any local Markdown
viewer. Nothing detected them: the repository has **no link check** in `make ci`
or in `.github/workflows/check.yml`, so the rot accumulated unnoticed across
several refactors.

The damage was concentrated in the docs a newcomer reads first — both READMEs,
`README_INDEX.md`, the SAD — and in the design corpus a contributor follows to
ground a change (ADR-023, ADR-025, SRD-048/049/050/069).

## §2 Root cause

Three independent causes, none of them a one-off typo:

1. **Documents were retired without a reference sweep.** `57c26a3` ("retire
   migrated flat guides") folded `docs/guides/data.md` and
   `docs/guides/composition.md` into the guides tree
   (`docs/guides/data/index.md`, `docs/guides/subprocesses/index.md`) and left
   the inbound links behind. `8159359` ("remove obsolete docs") deleted
   `docs/analytics/Analysis of the gobpm project.md` and left the SAD's
   reference to it.
2. **ADRs were renamed without a reference sweep.** Four documents changed file
   name while keeping their number and topic, so every inbound link broke while
   still *reading* correctly — the label says `ADR-019 v.1`, and only the href
   is wrong.
3. **A copy-paste depth error propagated.** ADR-025 and its RU twin reference
   the vendored spec as `../../bpmn-spec/…`. From `docs/design/` the correct
   depth is `../bpmn-spec/…`. The wrong prefix was pasted 44 times, including
   the citation-dense §3 grounding tables — precisely the links a reviewer
   follows to verify a standard-claim.

## §3 Solution

Every replacement target was confirmed to exist before the rewrite.

| Class | From | To | Refs |
|---|---|---|---|
| Depth | `../../bpmn-spec/…` | `../bpmn-spec/…` | 44 |
| Renamed | `ADR-019-definition-versioning-and-registry.md` | `ADR-019-definition-versioning.md` | 8 |
| Renamed | `ADR-013-observability.md` | `ADR-013-instance-observability.md` | 6 |
| Renamed | `ADR-011-structured-process-data.md` | `ADR-011-process-data-flow.md` | 2 |
| Renamed | `ADR-027-business-rule-engine-seam.md` | `ADR-027-business-rule-task-and-rule-engine-seam.md` | 1 |
| Retired → successor | `docs/guides/composition.md` | `docs/guides/subprocesses/index.md` | 6 |
| Retired → successor | `docs/guides/data/overview.md` | `docs/guides/data/index.md` | 6 |
| Retired → successor | `docs/guides/data.md` | `docs/guides/data/index.md` | 4 |
| Retired, no successor | `docs/analytics/Analysis of the gobpm project.md` | reference removed | 1 |

Files touched (14): `README.md`, `README.ru.md`, `README_INDEX.md`,
`CHANGELOG.md`, `docs/analytics/gobpm Development Roadmap.md`,
`docs/design/SAD-001-vision-and-architecture.md`,
`docs/design/ADR-023-sub-process-and-call-activity.md` (+ `.ru`),
`docs/design/ADR-025-activity-iteration-loop-and-multi-instance.md` (+ `.ru`),
`docs/srd/SRD-048-conditional-events.md`,
`docs/srd/SRD-049-embedded-subprocess.md`,
`docs/srd/SRD-050-call-activity.md`,
`docs/srd/SRD-069-rules-script-invocation-and-registrar-facts.md`.

Link **labels** were left untouched. The renamed-ADR references are numeric
(`[ADR-013 v.2]`, `[ADR-019 v.1]`), so the visible text stays correct; only the
href moved. The SAD-001 removal is recorded in its v.1.1 history row.

## §4 Deliberately not changed

This section exists so a future sweep — automated or manual — does not "fix"
these and so a link checker can be configured to accept them.

### §4.1 Not links — code inside prose

Eight `](…)` sequences match a naive link regex but are Go signatures inside
tables or sentences. They must not be rewritten:

| File:line | Text |
|---|---|
| `docs/guides/data/native-structs.md:43` | `` `Register[T](build)` `` |
| `docs/guides/data/value-model.md:152` | `` `values.NewMap[T](entries map[string]T)` `` |
| `docs/guides/data/structural.md:105` | `` `values.NewArray[T](vals…)` `` |
| `docs/guides/data/structural.md:106` | `` `values.MustMap[T](map)` `` |
| `docs/guides/data/structural.md:107` | `` `values.NewVariable[T](v)` `` |
| `docs/fix/FIX-014-p3-correctness-sweep.md:35,140` | `` `NewArray[T](a.elements...)` `` |

All of them sit in inline code spans, so a checker that skips fenced and inline
code never sees them. One that does not must carry them as exclusions.

### §4.1a A genuine link that must stay unresolvable

`docs/guides/CONTRIBUTING.md:99` is the guide-page template showing authors the
shape of a design reference — an `ADR-NNN-….md` placeholder, deliberately not a
real file. It is the one entry a code-aware checker still reports, and it needs
an explicit ignore rather than a fix.

### §4.2 Historical records naming a since-retired path

Two references keep `docs/guides/composition.md` on purpose. Both are in
backticks, not links, so a link checker will not see them — but a text sweep
would, and rewriting either would falsify a record:

- `docs/srd/SRD-049-embedded-subprocess.md` §M6 — a milestone→commit table
  stating what commit `d10482b` touched. The path was accurate then.
- `docs/srd/SRD-050-call-activity.md` §FR-11 — a requirement written before
  implementation, naming the deliverable as it was then called.

The same principle governs `SRD-002` §1.1/§4.4 (see FIX-030's sibling edit) and
`SAD-001` §9's authoring-time module tree: **snapshots and plans keep their
original wording; live cross-references track reality.**

## §5 Follow-up — the gap that let this happen

The sweep fixes the symptom, not the cause: there is still no automated link
check. Recorded in `docs/backlog.md` under un-homed items. Any checker adopted
must handle the §4.1 exclusions and decode percent-encoded paths — several
correct links contain `%20` (`gobpm Development Roadmap.md`) and a naive
checker reports them as broken.

## §6 Verification

- Repo-wide scan before: **78** unresolvable relative Markdown references
  (percent-decoded, external URLs and pure anchors excluded).
- After, scanned the way §5 requires (fenced and inline code skipped,
  percent-decoding on): **1** — the §4.1a template placeholder. Zero real
  breaks.
- After, scanned naively: 16 — the 8 pre-existing §4.1 cases plus 8 this
  change-set introduced by *quoting* them (7 in §4.1's table, 1 in the
  `docs/backlog.md` entry). That a document cataloguing the false positives
  immediately produces eight more is the sharpest argument for §5's
  requirement: a checker that does not skip code will fight its own
  documentation.
- Every rewrite target verified present on disk.
- `go build ./...` clean; `make lint-core` 0 issues across all five modules;
  `pkg/convert/…` and `internal/lintcfg` tests green. No non-Markdown file was
  touched by this FIX.
