# SRD-080 — Documentation site on GitHub Pages

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-05 |
| Owner | Ruslan Gabitov |
| Implements | — (standalone tooling landing; no parent ADR — the engine concept is untouched) |
| Related | [FIX-034](../fix/FIX-034-gate-blind-spots-and-doc-drift.md) (the blocking `linkcheck` gate this site build complements); [FIX-029](../fix/FIX-029-ci-runs-examples.md) (the CI split whose workflow conventions this follows) |

## §1 Background

`docs/guides/` is a 90-page developer manual (`find docs/guides -name '*.md'` →
90), every page carrying `title:`/`description:` frontmatter, published today
only as raw Markdown browsed on GitHub. GitHub's file view gives no site-wide
search, no navigation sidebar, no readable entry URL — a manual of this size
deserves a real documentation site, and the repository already pays the
authoring cost.

The obstacle to publishing *just* the guides is the link graph. Measured on
the current tree:

- guides → `docs/design/` — **148** links (ADR/SAD rationale pins);
- design → `docs/bpmn-spec/` — **230** links (the vendored BPMN 2.0 extract);
- design → `docs/srd/` — 8, → `docs/analytics/` — 1 (two former links into
  `docs/camunda7/` are paraphrased away in this branch, FR-2a);
- guides → `examples/` (outside `docs/`) — **65** links, plus a handful to
  `pkg/…`, `adapters/…`, and the repository root.

Any published subset of `docs/` leaves hundreds of dead links; links that
escape `docs/` entirely can never resolve on a docs site and must be rewritten
to GitHub URLs at build time. The repository's `make ci` already runs a
blocking relative-link checker (FIX-034), so the source tree is known-clean —
the site build must preserve that property, not erode it.

"Kept up to date" must be a property of CI, not a ritual: the site redeploys
itself whenever a docs-touching change lands on `master`.

## §2 Requirements

### Functional

- **FR-1** — the `docs/` tree publishes as a static site at
  `https://dr-dobermann.github.io/gobpm/`, built by MkDocs with the Material
  theme; `docs/guides/index.md` is the featured entry point, and a new
  top-level `docs/index.md` landing page routes readers to guides / design /
  BPMN extract (MkDocs requires a homepage; `docs/` has none today —
  `ls docs/*.md` → `backlog.md` only).
- **FR-2** — every intra-`docs/` relative link resolves on the published site
  (guaranteed by publishing the whole tree + `mkdocs build --strict`, which
  fails on unresolved links).
- **FR-2a** — `docs/camunda7/` is excluded from the published site
  (`exclude_docs: /camunda7/`) — internal migration-analysis material, not
  developer documentation. Its sources stay in the repo untouched. The two
  inbound links (`design/ADR-005` en + ru, References) are replaced with
  link-free paraphrases in this branch — no version bump, the References
  inventory is not a contract change — so no link into the excluded path
  remains and `--strict` stays green by construction.
- **FR-3** — links escaping `docs/` (`../../examples/…`, `../../../pkg/…`,
  repo root) are rewritten at build time to
  `https://github.com/dr-dobermann/gobpm/tree/master/<path>` by an MkDocs
  hook — zero churn in the Markdown sources, so in-repo browsing, Obsidian,
  and the existing `linkcheck` gate are untouched.
- **FR-4** — a `docs.yml` GitHub Actions workflow (a) on pull requests
  touching `docs/**` or the site config: runs `mkdocs build --strict` as a
  validation check; (b) on push to `master` with the same paths (plus
  `workflow_dispatch`): builds and deploys via `actions/deploy-pages` — no
  `gh-pages` branch. Actions pinned by commit SHA with a release comment,
  matching `check.yml`.
- **FR-5** — the MkDocs toolchain is version-pinned (`mkdocs-material==9.7.7`,
  which pins `mkdocs` transitively) in one place consumed by both CI and the
  local target, per the parity rules.
- **FR-6** — `make docs-build` (strict build) and `make docs-serve` (live
  preview) exist for local use, guarded by `require-command`; they are **not**
  part of the `make ci` umbrella (Python is not a prerequisite of the core
  gate — same reasoning that keeps CI-pinned tool installs out of the
  Makefile, `check.yml` "Install tools" comment).
- **FR-7** — `README.md` (and its translated twins, if any) link the published
  site; `CHANGELOG.md` `[Unreleased]` records the addition.

### Non-functional

- **NFR-1** — the deploy needs no manual step beyond the one-time repository
  setting *Settings → Pages → Source: GitHub Actions* (owner-only; recorded in
  §10 when done).
- **NFR-2** — `.obsidian/` never publishes (MkDocs excludes dot-prefixed
  files/directories by default).
- **NFR-3** — no Go code changes; the diff-coverage gate is trivially green.
  `make ci` stays green across modules.
- **NFR-4** — workflow permissions are minimal (`contents: read`,
  `pages: write`, `id-token: write` on the deploy job only).

## §3 Models

Three new artifacts, one edited:

**`mkdocs.yml`** (repo root):

```yaml
site_name: gobpm
site_url: https://dr-dobermann.github.io/gobpm/
repo_url: https://github.com/dr-dobermann/gobpm
docs_dir: docs
theme:
  name: material
  features: [navigation.sections, navigation.top, search.suggest, content.code.copy]
  palette: # light + dark toggle
exclude_docs: |
  /camunda7/
validation: # harden --strict: these default to `info`, which strict ignores
  omitted_files: warn
  unrecognized_links: warn
  anchors: warn
hooks:
  - scripts/mkdocs_hooks.py
```

No `nav:` key in v1 — MkDocs auto-derives the tree, and the curated
`index.md` pages (`guides/index.md` and the new `docs/index.md`) remain the
primary navigation surface (see §4.3 and Open questions Q3).

**`scripts/mkdocs_hooks.py`** — one `on_page_markdown` hook: for each
relative link, resolve it against the page's location; if the target lies
outside `docs/`, rewrite to
`https://github.com/dr-dobermann/gobpm/tree/master/<repo-relative-path>`.
Links inside `docs/` pass through untouched. Worked example — in
`docs/guides/getting-started/first-process.md`:

```
[`basic-process`](../../../examples/basic-process/)          # source
[`basic-process`](https://github.com/dr-dobermann/gobpm/tree/master/examples/basic-process/)  # built
```

**`.github/workflows/docs.yml`** — two jobs: `build` (PR + push: pinned
`actions/setup-python`, `pip install mkdocs-material==9.7.7`,
`mkdocs build --strict`, upload the artifact) and `deploy`
(`if: github.ref == 'refs/heads/master'`, environment `github-pages`,
`actions/deploy-pages`).

**`Makefile`** — `docs-build` / `docs-serve` targets behind
`require-command`, reading the pin from a `MKDOCS_MATERIAL_VERSION` variable
so `scripts/check-tool-pins.sh` can adopt it later.

Plus the new content page **`docs/index.md`** (landing: what gobpm is, three
doors — Developer Manual / Design docs / BPMN extract) — kept useful for
in-repo browsing too.

## §4 Analysis

### §4.1 Generator — MkDocs Material over the alternatives

- **Jekyll ("Pages from /docs")** — zero workflow, but no search, no sidebar,
  fragile Markdown handling, and no strict-link gate; the out-of-tree links
  stay broken. Rejected.
- **Hugo / mdBook / Docusaurus** — capable, but none consumes the existing
  `title:`/`description:` frontmatter as-is across a plain nested-directory
  tree without restructuring (mdBook wants `SUMMARY.md`; Docusaurus wants a
  JS project). Rejected for churn.
- **MkDocs + Material** — consumes the tree and frontmatter as they are,
  ships search + light/dark theme, `--strict` gives a second link gate, and
  the `hooks:` mechanism solves the escaping-links problem without touching
  sources. Chosen.

### §4.2 Scope — whole `docs/` over a subset

Guides-only strands 148 design links; guides+design strands 230 BPMN-extract
links plus the srd/camunda7/analytics stragglers (§1). Publishing the whole
tree closes the intra-`docs/` link graph by construction and keeps the scope
rule trivial ("everything under `docs/` is public — as it already is in the
repository"). SRD/FIX one-shots publish as the historical records they are.

### §4.3 Navigation — auto-derived in v1

A hand-maintained `nav:` over 337 files is unmaintainable and would go stale
at the first added page (the exact drift class FIX-034 exists to kill).
Plugins (`literate-nav`, `awesome-pages`) can curate ordering but add
dependencies and per-directory dot-files; deferred until the auto sidebar
proves inadequate in practice. The curated index pages already provide the
reading order.

### §4.4 Deploy — `actions/deploy-pages` over a `gh-pages` branch

A pushed `gh-pages` branch is mutable repo state outside review and bloats
clone size with built HTML. The Pages deployment API keeps built output out
of git entirely and its permission surface is job-scoped. Also rejected:
`mkdocs gh-deploy` (same branch-push model, plus it pushes from CI with
`contents: write`).

## §5 API

No library API surface. The user-facing surface is:

- `https://dr-dobermann.github.io/gobpm/` — the site;
- `make docs-build` / `make docs-serve` — local build/preview;
- the `docs.yml` PR check — a red `mkdocs build --strict` on a docs-touching PR.

## §6 Tests / Verification

No Go code, so verification is build-level, each step mechanical:

| # | Check | How |
|---|---|---|
| V-1 | Strict build green | `make docs-build` (→ `mkdocs build --strict`) exits 0 on the current tree |
| V-2 | Escaping links rewritten | grep the built `site/` HTML: zero `href` climbing out of the site root; `examples/`-links point at `github.com/dr-dobermann/gobpm/tree/master/examples/…` |
| V-3 | Intra-docs links resolve | `--strict` (fails on warnings) + spot-check guides→ADR and ADR→bpmn-spec pages in the built HTML |
| V-4 | Exclusions absent | `find site -name '.obsidian'` → empty; `test ! -d site/camunda7`; no `site/` in git status (gitignored) |
| V-5 | Repo gate intact | `make ci` green (link-check, lint, tests, coverage untouched) |
| V-6 | CI validation works | the PR for this branch shows the `docs / build` check green |
| V-7 | Deploy works | post-merge, first `docs.yml` run on `master` deploys; site reachable (user confirms — needs the one-time Pages setting, NFR-1) |

## §7 Milestones

1. **M1 — site skeleton**: `mkdocs.yml`, `scripts/mkdocs_hooks.py`,
   `docs/index.md`, `.gitignore` entry for `site/`; V-1…V-4 green locally.
2. **M2 — CI**: `.github/workflows/docs.yml` (build check + master deploy),
   Makefile targets + pin variable; V-5, V-6.
3. **M3 — surfacing**: README (+ twins) site link, `CHANGELOG.md` entry,
   linked-docs sweep.

## §8 Cross-doc references

Upward/sideways only: FIX-034 and FIX-029 (unversioned one-shots, cited as
historical records). No ADR/SAD contract is touched; no version bumps.

## §9 Definition of Done

- [ ] V-1…V-6 green; V-7 confirmed after merge.
- [ ] The site self-updates: a later docs-touching merge to `master` triggers
      a redeploy with no manual step.
- [ ] README + CHANGELOG updated (FR-7).
- [ ] `make ci` green across modules (NFR-3).
- [ ] §10 filled with commit SHAs and the Pages-setting confirmation.

## §10 Implementation summary

*Placeholder — filled at landing.*

## Open questions

None — all resolved with the owner (2026-08-05):

- **Scope** — the whole `docs/` tree publishes: `guides/` (the manual),
  `design/` (SAD + ADRs, incl. the `.ru` twins), the vendored `bpmn-spec/`
  extract, plus `srd/`, `fix/`, `audit/`, `analytics/`, and `backlog.md`
  (all already public in the repository; required for link-graph closure,
  §4.2). **Excluded: `camunda7/`** (FR-2a) — internal migration-analysis
  material; its two inbound links (ADR-005 en/ru References) are paraphrased
  away in this branch, no version bump.
- **Navigation** — auto-derived sidebar in v1; no curated-nav plugin (§4.3).
- **Local toolchain** — plain `pip install mkdocs-material==9.7.7`; no Docker
  fallback (FR-6).
- **PR check** — the `docs / build` check stays advisory; making it a
  required status check is a possible later branch-protection change
  (owner-side, out of scope).
