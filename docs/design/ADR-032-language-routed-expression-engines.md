# ADR-032 — Language-routed expression engines and the text-expression battery

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-25 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-002 v.2](ADR-002-extension-architecture.md) §4.1/§4.2 (the extension pattern; `ExpressionEngine` is one of its named seams), [ADR-031 v.1](ADR-031-script-task-and-script-engine-seam.md) (the multi-engine registry conception this transplants: enumerable claims, core routing, loud conflicts — proven for `scriptFormat`, applied here to the spec's `language`), [ADR-011 v.7](ADR-011-process-data-flow.md) (the data-flow layer expressions evaluate within), [SAD-001 v.1](SAD-001-vision-and-architecture.md) §11 |

Expressions run everywhere — conditional flows, gateways, timers,
Multi-Instance cardinality and completion, Standard Loop conditions,
correlation retrieval, conditional events, data transformations — yet the
expression seam is the engine's least finished: a single, replace-wholesale
engine whose default merely delegates to each expression's own `Evaluate`,
and **no way to write an expression as text**. This ADR completes the
Expression Layer epic's conception: **multiple expression engines register
simultaneously, routed by the spec's own `language` attribute**, a **text
expression kind** carries `language` + `body` for engines to interpret, and
a **stdlib-only lightweight evaluator** ships in core as the text battery.

## 1. Context & problem

What exists (all landed):

- `data.FormalExpression` — the abstract contract (`Language()`,
  `Evaluate(ctx, Source)`, `ResultType()`, …), with **one** implementation:
  the Go-functor kind (`data/goexpr.GExpression`, language
  `gobpm:goexpr`) — a closure over the scope-backed `data.Source`.
- The seam: `expression.Engine` (one method — `Evaluate(ctx, expr, src)`),
  the five-point wiring, and the in-core default
  (`expression/goexpr.Engine`) that **delegates to the expression's own
  `Evaluate`** — a pass-through.
- Scope integration: every runtime consumer (conditions, timers, MI,
  loops, correlation, conditional events) already evaluates through
  `ExpressionEngine()` against the instance's data surface.

What's missing — the epic's open half:

- **Language routing.** The seam is single-engine and replace-wholesale:
  wiring a FEEL engine would silently take over `gobpm:goexpr` functor
  expressions too — a mixed-language model (Go conditions here, FEEL
  there) is impossible, although the metamodel gives every
  `FormalExpression` a `language` attribute (URI, 0..1) for exactly this.
- **Text expressions.** There is no `FormalExpression` kind carrying a
  source body; `total > 100` cannot be written as a string, which blocks
  both low-ceremony modeling and any future XML interchange of
  expressions.
- **A text battery.** With no text kind there is naturally no default
  evaluator for one; FEEL/JUEL are reserved as adapters (roadmap WS-E),
  but a dependency-light in-core option is the epic's explicit "batteries
  included" deliverable.

The multi-engine registry conception is already proven twice — formats
for scripts (ADR-031), decision names for rules (ADR-027) — and the
`language` attribute is its exact expression-layer analog.

## 2. Decision

### 2.1 The seam — multiple engines, language-routed by the core

`expression.Engine` grows from one method to the registry contract:

- **`Type()`** — the `##`-kind for the startup config;
- **`Languages()`** — the enumerable claim list (language URIs, matched
  case-insensitively; enumerable for the same reason as ADR-031's
  `Formats()`: conflicts must be detectable and the routing table
  printable);
- **`Evaluate(ctx, expr, src)`** — unchanged in shape.

The **core registry** (pure Go, in `pkg/model/expression`) folds claims
into a language→engine map at construction: duplicate claims are **loud
construction-time errors naming both kinds**; the startup config prints
the routing table; evaluation routes by the expression's `Language()`.
An unclaimed language is a classified error **listing the registered
claims**; an **empty language is a classified error** too (the metamodel's
0..1 exists for the Definitions-level default-language inheritance — an
interchange concern deferred with interchange itself; programmatic
expressions always know their language).

**`WithExpressionEngine` becomes repeatable** — each call registers
another engine (the ADR-031 semantic; pre-1.0, the old replace-wholesale
meaning is retired and documented). The **zero-config default registry
contains the batteries pair** (`goexpr` + `lite`), so every existing
functor expression works untouched — the compatibility bar for this
change.

**The batteries are removable from the runtime**:
`WithoutDefaultExpressionEngines()` starts the registry **empty** — every
engine then registers explicitly (including re-adding `goexpr`/`lite`,
which stay public constructors). A goexpr-free runtime rejects functor
expressions exactly like any unclaimed language — loud, with the claims
listed. This answers "don't pay for what you don't route" at the registry
level; *physically* extracting `GExpression`/`goexpr` into an adapter
module is rejected (§4): it is stdlib-only (nothing to shed; the linker
already drops unreferenced code), and the whole test/example/model corpus
builds functor conditions — which core tests could no longer import under
the core⊅adapters rule.

### 2.2 Two evaluation loci, one contract

The `FormalExpression` kinds split honestly by where evaluation lives:

- **The functor kind** (`gobpm:goexpr`, existing) **self-evaluates**: its
  logic is the Go closure. Its engine — the registered `goexpr` — remains
  the delegate it always was, now claiming exactly its own language.
- **The text kind** (new) **cannot self-evaluate**: it carries `language`
  + **`body`** and its own `Evaluate` fails loud ("a text expression
  evaluates through the engine registry") — interpretation belongs to the
  routed engine. Engines reach the source through a **`Body()`
  capability** (a small interface the text kind implements; the functor
  kind doesn't) — the `FormalExpression` contract itself stays untouched
  for existing implementations.

The text kind is constructed with a **required language and body**
(fail-fast; the metamodel's 0..1 on `language` is the interchange
affordance, as with `scriptFormat`), and optionally the result type
(`evaluatesToTypeRef`'s programmatic analog).

### 2.3 The battery — `gobpm:lite`, a stdlib-only in-core evaluator

The text battery is a small expression language evaluated by an in-core
engine (pure stdlib — the dependency-light contract holds, which is what
makes in-core placement legitimate where interpreters were not):

- **Values**: numbers (the **numeric family — int, int64, float64 —
  unifies to float64** inside lite: a text language cannot demand Go type
  knowledge, so `total > 100` works for an int datum; the deliberate,
  documented relaxation of the dtable strictness, justified because lite
  has no Go-typed operand side), strings (single or double quoted),
  booleans, `nil`, and **time** (`time.Time` data — the timer layer's
  value type — with a `time("RFC3339")` builtin for literals).
  Cross-family comparisons (string vs number, time vs number, …) stay
  **loud errors** — never a silent false.
- **Data access**: bare names resolve through the scope walk-up; dotted
  and bracketed **structural paths** ride the landed SRD-042 resolver
  (`data.ResolvePath`) — record fields, array indices and map keys with
  one syntax. A **missing datum or dead path fails loud**;
  `has("name-or-path")` probes optional data (the house split, third time
  identical).
- **Operators**: comparisons (`== != < <= > >=`; ordering for numbers,
  strings and times), arithmetic (`+ - * / %`), boolean
  (`and or not`), string concatenation (`+` on two strings),
  parentheses. A deliberately **tiny builtin set** (`has`, `len`,
  `time`) — anything richer belongs to FEEL and its adapter.
- **Kind and language**: the engine registers `gobpm:lite` alongside
  `goexpr` in the zero-config default registry — out of the box a model
  mixes functor and text expressions freely. There is **no single
  "default engine"**: each expression routes by its own language — a
  functor expression to `goexpr`, a text one to whatever it names.
  Lite's convenience constructor (`lite.Expr(body)`) mints text
  expressions pre-tagged `gobpm:lite`, making lite the default *text*
  language in practice without any registry-level fallback.

Worked examples over the actual value model (the `values` package's
kinds — single values, arrays, structures/records, maps):

| Datum (values kind) | Expression | Result |
|---|---|---|
| `total` = 150 (int Variable) | `total > 100 and total <= 500` | `true` — int promoted into the numeric family |
| `rate` = 0.19 (float64 Variable) | `total * (1 + rate)` | `178.5` (float64) |
| `tier` = `"gold"` (string Variable) | `tier == "gold" or tier == "vip"` | `true` |
| `name` (string Variable) | `"Dear " + name` | string concatenation |
| `approved` = true (bool Variable) | `approved and not blocked` | boolean logic |
| `deadline` (time.Time Variable) | `deadline > time("2026-08-01T00:00:00Z")` | time ordering against a literal |
| `created_at`, `updated_at` (times) | `updated_at > created_at` | time-vs-time ordering |
| `items` (Array) | `len(items) > 0 and items[0] == "sku-1"` | length + index access (the path resolver) |
| `order` (Record/structure) | `order.total > 100 and order.customer.tier == "vip"` | nested field paths |
| `rates` (Map) | `has('rates["EUR"]') and rates["EUR"] < 1.2` | map-key path access with an existence probe |

*Why not FEEL in core:* FEEL is a large, evolving standard whose faithful
implementation is an adapter-scale dependency (roadmap WS-E reserves
`adapters/feel`); a "small FEEL subset" in core would be a permanently
misleading claim. `gobpm:lite` claims nothing but itself.

### 2.4 Observability

Expression evaluation is a hot path fanning across every condition — a
per-evaluation fact would be the flood ADR-013's masking/volume rules
exist to prevent (the `DataChange` lesson). Decided: **no per-evaluation
facts**; the registry appears in the startup config (kinds + routing
table), and evaluation failures surface through the consumers' existing
error paths, which already carry node/instance context.

### 2.5 Engine notes (deviations & choices)

| Choice | Standard position | Engine choice |
|---|---|---|
| Language routing | `language` URI, 0..1, with a Definitions-level default | **multiple registered engines**, core-routed by enumerable claims (conflicts loud); empty language is a loud error — the Definitions default inheritance rides interchange (§2.1) |
| Text expressions | `FormalExpression` body + language | the text kind with required language/body and a `Body()` capability; self-evaluation refused loud — the routed engine interprets (§2.2) |
| The default language | Definitions-level `expressionLanguage` | zero-config registry = `goexpr` + `lite`; no implicit default-language rewriting; `WithoutDefaultExpressionEngines()` empties the registry for explicit-only wiring (§2.1/§2.3) |
| The battery language | not mandated (FEEL is DMN's, not BPMN's mandate) | `gobpm:lite` — a tiny stdlib-only language that claims nothing but itself; FEEL/JUEL are adapters (§2.3) |
| Numeric typing in lite | — | the numeric family (int/int64/float64) unifies to float64 inside lite (a text language has no Go-typed operand side); cross-family comparisons stay loud (§2.3) |
| Evaluation facts | — | none per evaluation (the hot-path flood guard); the registry is startup-visible (§2.4) |

## 3. Grounding

| Claim | Source |
|---|---|
| `FormalExpression`: `language` String attr 0..1, `evaluatesToTypeRef` | the vendored extract, `elements/foundation.md` §FormalExpression |
| The default expression language lives on Definitions | the `data.FormalExpression` doc comment (landed, quoting the spec) |
| The current seam: one method, pass-through default, replace-wholesale option | `pkg/model/expression/expression.go`, `pkg/model/expression/goexpr` (landed) |
| The functor kind + its language | `pkg/model/data/goexpr` (`gobpm:goexpr`, landed) |
| Ten-plus runtime consumers over the scope surface | conditions/timers/MI/loops/correlation/conditional (landed; grep `ExpressionEngine()`) |
| The registry conception: enumerable claims, loud conflicts, core routing | ADR-031 v.1 §2.1 (landed: `pkg/script`) |
| The structural-path machinery the lite language reuses | SRD-042 lineage (`data.ResolvePath`, landed) |
| FEEL reserved as an adapter | the roadmap, WS-E |

## 4. Alternatives considered

- **Keep single-engine replace-wholesale** (status quo). Rejected — a
  mixed-language model is impossible; wiring FEEL breaks every functor
  expression silently, the exact shadowing class the registries were
  built to kill.
- **Dispatch inside a composite adapter** (a user-assembled
  multiplexer engine). Rejected — every embedder rebuilds the same
  router; conflicts stay silent; the core owns routing everywhere else.
- **FEEL (or a subset) as the in-core battery.** Rejected — §2.3; the
  honest claim is a tiny language of our own naming.
- **Text expressions that self-evaluate** (embedding the lite parser in
  the model layer). Rejected — couples `pkg/model/data` to an evaluator
  and forecloses engines interpreting the same body differently; the
  split of loci (§2.2) keeps the model layer inert.
- **Per-evaluation observability facts.** Rejected — §2.4, the hot-path
  flood guard.
- **Extracting `GExpression`/`goexpr` into an adapter module.** Rejected —
  stdlib-only code with zero dependency weight (dead-code elimination
  already handles binaries); core tests, examples and model fixtures
  build functor conditions and cannot import adapters (ADR-003 §4.4
  direction). The legitimate removability lives at the registry:
  `WithoutDefaultExpressionEngines()` (§2.1).

## 5. Consequences

**Positive:** the Expression Layer epic closes conceptually — pluggable,
**simultaneous** engines routed by the spec's own attribute; text
expressions unlock low-ceremony modeling and pave interchange; the lite
battery gives `total > 100 and has("tier")` out of the box; FEEL/JUEL
slot in later beside — not instead of — everything else.

**Negative / cost:** the `expression.Engine` interface widens (pre-1.0;
one external implementor class — adapters — none shipped yet); the
`WithExpressionEngine` semantic changes from replace to register
(documented); a new in-core parser to maintain (bounded by the tiny
grammar).

**Follow-ups this conception sets up:** `adapters/feel` (WS-E) beside
`lite`; the Definitions-level default-language inheritance with
interchange; richer lite builtins only if demand proves them.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 (Accepted) | 2026-07-25 | Ruslan Gabitov | Accepted with both landing SRDs: the widened engine contract, the language registry (itself an `Engine` — every consumer untouched), the repeatable/opt-out wiring and the text kind landed first (SRD-066); the `gobpm:lite` stdlib evaluator with the `goexpr`+`lite` batteries default and the three-site mixed-engine example completed the conception (SRD-067). `adapters/feel` remains the noted follow-up. |
| v.1 | 2026-07-25 | Ruslan Gabitov | Draft conception, completing the Expression Layer epic's design. **Multiple expression engines register simultaneously** (repeatable `WithExpressionEngine`; enumerable `Languages()` claims folded into a core routing registry — duplicate claims loud, unclaimed/empty languages loud with the claims listed; zero-config = `goexpr` + `lite`, so functor expressions keep working untouched). **Two evaluation loci**: the functor kind self-evaluates (its engine stays the delegate), the new **text kind** (required language + body, a `Body()` capability) refuses self-evaluation — the routed engine interprets. **`gobpm:lite`** is the stdlib-only in-core text battery covering the full value model — numeric-family promotion to float64 (documented; cross-family stays loud), strings, booleans, **time** (ordering + the `time()` RFC3339 builtin), arrays/records/maps through the SRD-042 path resolver, `has`/`len`/`time` builtins only — with worked §2.3 examples per kind. There is no registry-level default engine — each expression routes by its own language; `lite.Expr(body)` makes lite the default *text* language by construction; `WithoutDefaultExpressionEngines()` empties the zero-config registry (the batteries are removable from routing — physically extracting the stdlib-only goexpr into an adapter is rejected, §4). FEEL/JUEL remain adapters. No per-evaluation facts (the hot-path flood guard); the registry is startup-visible. Implementation rides the accompanying SRDs (routing + the text kind, then the lite evaluator). |
