# Authoring the gobpm user guides

These guides teach **how to use gobpm** — the counterpart to `docs/design/`
(SAD/ADR/SRD), which records *how and why the code was built*. A guide is
usage-first: a developer should be able to read it, copy the code, run it, and
see the result. Keep design rationale in `docs/design/`; link to it only when a
reader genuinely needs the "why".

This file is the authoring standard, **not** a published page.

## Portable Markdown (web-publishing ready)

The guides are plain, generator-agnostic Markdown so they drop into MkDocs,
Docusaurus, Hugo, or plain GitHub without rewrites:

- **Frontmatter** — minimal, universal YAML only:
  ```yaml
  ---
  title: Service Task
  description: Run your own Go code as a step in a process.
  ---
  ```
  No generator-specific keys (`weight`, `sidebar_position`, `layout`).
- **Ordering** — reading order lives in the curated nav lists in each
  `index.md`, not in frontmatter. (A generator's sidebar is configured from
  those lists later.)
- **One H1 per page**, matching the frontmatter `title`. Sections are `##`+.
- **Links** — relative, to the `.md` file (`../gateways/exclusive.md`,
  `../../examples/basic-process/`). Never Obsidian `[[wikilinks]]`.
- **Diagrams** — fenced ` ```mermaid ` blocks (avoid special characters in node
  labels; quote a label that needs them: `["a (b)"]`).
- **Admonitions** — plain blockquotes, portable across generators:
  > **Note:** …  /  > **Warning:** …  /  > **Tip:** …

  Do **not** use `!!! note` (MkDocs) or `:::note` (Docusaurus) syntax.
- **Code** — fenced ` ```go ` / ` ```bash `; snippets are **real lines copied
  from a runnable `examples/<name>/`**, never invented. Show the essential
  lines, then point to the full example.

## Page template

Every element/feature guide follows this shape (drop sections that don't apply):

```markdown
---
title: <Short title>
description: <One sentence — what the reader can do after this page.>
---

# <Title>

<1–2 sentences, user framing: what this element does and when you reach for it.>

## What it is

<Concept at the user's level — a short paragraph, plus a mermaid diagram when it
clarifies the flow. Not the ADR depth.>

## Build it

<The minimal Go to construct it, copied from the example — the key lines only.>

## Run it

```bash
cd examples/<name> && go run .
```

<The expected output, in a fenced block.>

## How it works

<The mechanics a user must understand to use it correctly — gating, ordering,
data flow, gotchas. Still usage-level.>

## Options & variations

<Common knobs and their effect.>

## See also

- Full example: [`examples/<name>/`](../../examples/<name>/)
- Related: [<guide>](<relative-path>) · Design: [ADR-NNN](../design/…) *(only if useful)*
```

## Grounding rule

Every claim and every code snippet is backed by a real artifact: the runnable
`examples/<name>/` for code, `docs/reference/`/the source for behavior. If an
example doesn't exist for a capability, say so and show the smallest real
snippet from the tests or package — do not fabricate an API.
