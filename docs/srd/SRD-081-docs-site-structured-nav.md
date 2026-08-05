# SRD-081 — Structured navigation for the documentation site

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-05 |
| Owner | Ruslan Gabitov |
| Implements | — (follow-up tooling landing; revisits the navigation decision recorded in SRD-080 §4.3, which explicitly deferred a curated nav "until the auto sidebar proves inadequate in practice" — it has) |

## §1 Background

SRD-080 shipped the site with a fully auto-derived sidebar. In practice the
result is unstructured: every top-level directory renders as a peer section
in alphabetical order, so `design/` (61 files), `srd/` (110+) and `fix/`
compete with the Developer Manual at the top level; the guides' parts sort
alphabetically (`concepts` before `getting-started`), contradicting the
reading order `guides/index.md` prescribes; and the 25 Russian ADR twins
interleave with their English originals, doubling the apparent length of the
design list. The owner has reviewed the live site and rejected the flat
tree.

A second, related cleanup rides along. The repo's twin convention is that
**only SAD/ADR documents carry Russian twins** — SRDs and FIXes are one-shot
landing records and do not. The tree contradicts the convention today:

- `docs/srd/*.ru.md` — **32** files (early-era SRD twins);
- `docs/fix/*.ru.md` — **6** files;
- `docs/design/*.ru.md` — **25** ADR twins (no SAD twin exists), mixed into
  the English listing.

Inbound-link exposure is minimal (measured): no kept file links an SRD/FIX
ru twin except `SRD-001.md`, which links its own twin; `README.ru.md` links
none; the ADR twins link each other (same directory) and English docs via
`../`-relative paths.

## §2 Requirements

### Functional

- **FR-1** — the sidebar is curated at the *section* level while page lists
  stay derived (no 337-entry hand nav to rot). Mechanism:
  **`mkdocs-awesome-nav==3.3.0`** (pinned) + per-directory `.nav.yml` files.
- **FR-2** — top-level order and titles (`docs/.nav.yml`):
  1. Home (`index.md`)
  2. **Developer Manual** → `guides/`
  3. **Design documents** → `design/`
  4. **BPMN 2.0 extract** → `bpmn-spec/`
  5. **Landing records** → section grouping `srd/` + `fix/`
  6. **Project** → section grouping `analytics/`, `audit/`, `backlog.md`
- **FR-3** — `guides/.nav.yml` orders the parts per `guides/index.md`:
  getting-started → concepts → data → events → gateways → tasks →
  subprocesses → iteration → operating → extending → foundation → reference,
  with the part titles the manual uses.
- **FR-4** — `design/.nav.yml`: `index.md`, then SAD-001 pinned first, then
  the ADRs (derived, numeric order = alphabetical), then a **Russian**
  group → `design/ru/`.
- **FR-5** — the 25 ADR Russian twins move to **`docs/design/ru/`** (git
  `mv`, history preserved); their out-of-directory relative links gain one
  `../` level. English↔twin cross-links, if any, are retargeted.
- **FR-6** — the **32 SRD and 6 FIX Russian twins are deleted** — the twin
  convention grants twins to SAD/ADR only, and these are stale one-shot
  translations nobody maintains. The single inbound link (`SRD-001.md` §,
  frozen Accepted doc) gets the mechanical dead-link fix: the twin link
  becomes prose (same treatment as SRD-051's retargets in SRD-080 M1).
- **FR-7** — the convention becomes standard behaviour, recorded where
  future work will hit it: the repo `CLAUDE.md` design-docs section states
  "Russian twins: SAD/ADR only, living in `docs/design/ru/`; SRD/FIX never";
  `docs/design/index.md` mentions the `ru/` group; the account-level
  `/sdd-fix` skill's linked-docs step gains the same rule for its
  translated-twins sweep.
- **FR-8** — toolchain parity: the plugin joins the workflow install line,
  the Makefile pin block (`MKDOCS_AWESOME_NAV_VERSION`, enforced by
  `require-mkdocs`), and the Monday pin sweep (`pypi:` entry).

- **FR-9** — the READMEs' status line drops its hardcoded release number
  (`README.md:11` and `README.ru.md:13` say `v0.9.0`; `.version` is
  `v0.11.0`): the sentence keeps only the prose ("active development, not
  yet production-ready") — the GitHub Tag badge four lines above already
  reports the live version and cannot rot.

### Non-functional

- **NFR-1** — `mkdocs build --strict`, `make link-check`, and `make ci` all
  green after the moves/deletes; zero dead links introduced.
- **NFR-2** — deleted twins remain retrievable from git history; the commit
  message names the convention as the reason.
- **NFR-3** — no Go code changes; diff-coverage gate trivially green.

## §3 Models

`docs/.nav.yml` (top level):

```yaml
nav:
  - index.md
  - Developer Manual: guides
  - Design documents: design
  - BPMN 2.0 extract: bpmn-spec
  - Landing records:
      - SRDs: srd
      - FIXes: fix
  - Project:
      - analytics
      - audit
      - backlog.md
```

`docs/design/.nav.yml`:

```yaml
nav:
  - index.md
  - SAD-001-vision-and-architecture.md
  - "ADR-*.md"
  - Russian: ru
```

`docs/guides/.nav.yml` lists the twelve part directories in reading order
(titles: "Getting started", "Architecture & runtime", …, matching
`guides/index.md`). `mkdocs.yml` adds `plugins: [search, awesome-nav]`
(listing `search` explicitly because declaring `plugins:` disables MkDocs'
defaults).

## §4 Analysis

- **awesome-nav over a hand `nav:`** — a full nav list over 300+ pages goes
  stale at the first added page and `omitted_files: warn` + `--strict` would
  then fail the build for every new doc; awesome-nav keeps listings derived
  and curates only order/titles/grouping. Chosen.
- **awesome-nav over literate-nav** — literate-nav wants a SUMMARY.md-style
  file per scope and wildcard syntax inside prose files; `.nav.yml` is
  data-as-declaration next to the directory it orders (the house
  data-over-code preference) and is the maintained successor
  (`mkdocs-awesome-pages` is deprecated in its favor). Chosen.
- **`design/ru/` over `docs/ru/`** — the twins are design documents; a
  top-level `ru/` would imply the whole site has a Russian edition (it does
  not). The subdirectory keeps them one `../` from their originals and gives
  the nav group a natural anchor. Chosen.
- **Deleting vs archiving SRD/FIX twins** — moving them to an `attic/` keeps
  dead weight in the published site and in linkcheck's scope; git history
  already archives them perfectly (NFR-2). Deletion chosen.

## §5 API

User-facing surface: the restructured sidebar at
<https://dr-dobermann.github.io/gobpm/>; `.nav.yml` as the knob future
sections are ordered with; unchanged `make docs-build` / `docs-serve`.

## §6 Tests / Verification

| # | Check | How |
|---|---|---|
| V-1 | Strict build green with the plugin | `make docs-build` exit 0 |
| V-2 | Nav structure as specified | built `site/index.html` nav: section titles/order match FR-2; guides parts in FR-3 order; SAD first and По-русски group present (FR-4) |
| V-3 | No orphan ru twins | `ls docs/srd/*.ru.md docs/fix/*.ru.md` → none; `ls docs/design/*.ru.md` → none (all under `design/ru/`) |
| V-4 | No dead links | `make link-check` green; `--strict` green (covers moved-twin link depth) |
| V-5 | Repo gate | `make ci` green |
| V-6 | Deploy | post-merge: site redeploys, sidebar shows the new tree (owner confirms) |

## §7 Milestones

1. **M1 — twin cleanup**: delete SRD/FIX ru twins, `git mv` ADR twins to
   `design/ru/` + link-depth fixes, `SRD-001.md` prose fix; linkcheck green.
2. **M2 — structured nav**: plugin pin (workflow, Makefile, sweep),
   `mkdocs.yml` plugins block, the three `.nav.yml` files; V-1…V-5.
3. **M3 — convention record + README hygiene**: `CLAUDE.md` twin rule,
   `design/index.md` note, `/sdd-fix` skill update, README en/ru status-line
   version drop (FR-9), CHANGELOG entry, linked-docs sweep.

## §8 Cross-doc references

None pinned: no ADR/SAD contract is touched (SRD-080 is an Accepted one-shot
and stays frozen; its §4.3 explicitly anticipated this follow-up).

## §9 Definition of Done

- [ ] V-1…V-5 green; V-6 confirmed after merge.
- [ ] Zero `.ru.md` outside `docs/design/ru/`.
- [ ] Convention recorded in all three FR-7 locations.
- [ ] §10 filled with commit SHAs.

## §10 Implementation summary

*Placeholder — filled at landing.*

## Open questions

None — resolved with the owner (2026-08-05): the 6 FIX ru twins are deleted
alongside the SRD twins (a FIX is a one-shot landing record); the nav group
is titled **Russian**.
