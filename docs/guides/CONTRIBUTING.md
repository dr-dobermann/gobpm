# Authoring the gobpm user guides

These guides are the **developer manual + reference** for gobpm — the counterpart
to `docs/design/` (SAD/ADR/SRD, which record *how and why the code was built*).
The audience is **software developers embedding the engine**. A page must give
the real API surface — taxonomy, constructor, options, the interfaces you
implement, methods, behavior — grounded in `go doc`, not narrated from an
example. Usage (a minimal build + real run) is secondary, not the whole page.

This file is the authoring standard, **not** a published page.

## Depth: deep, but readable

Cover the element completely, but keep every page skimmable in ~30 seconds:

- **Curate, then complete.** Lead each catalog (options, methods) with a short
  "what most people use" table, then the full table(s). Essential first so a
  reader can stop early.
- **Tables, not prose, for catalogs** — options, methods, interface members go
  in scannable tables.
- **Don't mirror godoc — point to it.** End with
  `go doc github.com/dr-dobermann/gobpm/pkg/<pkg>` for the exhaustive,
  always-current symbol list (prefer it over pkg.go.dev, which lags master).
- **Concentrate on public packages/interfaces** (`pkg/…`). Describe `internal/…`
  only as observable behavior, never as an API to call.

## Pure, raw-readable Markdown

The raw `.md` must read cleanly. The body is Markdown only:

- **No embedded HTML** — no `<details>`, no `<br>` walls, no inline tags. Depth
  comes from curation + ordering, not collapsibles.
- **HTML / YAML / config / JSON appears ONLY inside a fenced code block** (masked
  as a quotation) — never raw in the body.
- **The one metadata exception** is a minimal YAML **frontmatter** block at the
  very top (delimited by `---`): `title` and `description` only. No
  generator-specific keys.
- **Links** relative, to the `.md` (`../events/boundary.md`) or repo paths
  (`../../design/ADR-021-….md`). Never Obsidian `[[wikilinks]]`.
- **Diagrams** as fenced ` ```mermaid ` (quote a label with special chars).
- **Admonitions** as a plain blockquote lead — `> Boundary events are …` — no
  `!!! note` / `:::note` directives.
- **Reading order** lives in each `index.md` nav list, not in frontmatter.

## Grounding rule

Every symbol, signature, option, and interface member is verified with
`go doc pkg.Type` / `go doc pkg.Func` before it goes on the page. Every code
snippet is real lines from a runnable `examples/<name>/` or the package source;
every "Run it" block is real captured output (skip the banner/config dump). No
invented APIs; if something isn't there, say so.

## Page template — element / feature reference

```markdown
---
title: <Short title>
description: <One sentence — what the reader can do after this page.>
---

# <Title>

<Lead sentence(s): what it is and when a developer reaches for it.>

## Taxonomy

<Table: BPMN category · package · type · inherits/embeds · implements · the work.
Link the family taxonomy index.>

## Constructor

<The exact signature in a fenced go block + a parameter table + the error/panic
contract.>

## Options

<A short "most uses need only these" table, then the full option table(s)
grouped by their typed family (e.g. ActivityOption vs SrvTaskOption). End with
the go doc pointer.>

## The <X> contract   (when the developer implements an interface)

<The interface in a fenced go block + which members you implement and why.>

## <Usage: Build it / Run it, or execution modes>

<Minimal real code from examples/<name> + real captured output. Secondary to
the reference above — keep it tight.>

## Methods & runtime behavior

<Curated method table + the behavior a developer must know (gating, parking,
ordering, gotchas).>

## See also

- Examples: `examples/<name>/`
- Related guides: …
- Design: [ADR-NNN — title](../../design/ADR-NNN-….md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/<pkg>`
```

## Other page shapes

- **Taxonomy page** (per family: activities, events, gateways) — the class tree,
  a table of every member with its one-line role + link, and the shared
  attributes/options. A mermaid tree helps.
- **Concept / runtime page** (Part 2) — how a thing works at the developer's
  level (the engine, execution, event processing, scope). Explain observable
  behavior + the public contracts (`renv`, `exec`, handles); link `docs/design/`
  for the internal rationale rather than restating it.
- **Extension page** (Part 6, "add your own X") — the seam interface (fenced),
  the registration call (`foundation.SetGenerator`, `adapters.Register`,
  `thresher.WithXxx`), a minimal real implementation, and how the engine uses it.
