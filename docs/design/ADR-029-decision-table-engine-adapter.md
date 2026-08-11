# ADR-029 — The Decision Table engine: a pluggable adapter with functor rules

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-24 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-027 v.1](ADR-027-business-rule-task-and-rule-engine-seam.md) §2.4 (the named follow-up: the table-shaped tier — hit policies, output names, rules; and its two pre-made decisions: a rule is a **behavior contract** — match + yield — under a **data-declared table**; the seam's rows result and the deploy+evaluate component split already accommodate this tier), [ADR-002 v.2](ADR-002-extension-architecture.md) §4.1/§4.2 (the interface-with-default extension shape; the adapter tier), [ADR-003](ADR-003-module-layout.md) §4.4 (adapters as separate modules the core never imports), [SAD-001 v.1](SAD-001-vision-and-architecture.md) N2 (no DMN engine in core; decision evaluation is external/embedder-supplied) |

The Business Rule Engine seam landed with a function registry as its in-core
default. This ADR decides the **table tier**: a minimal Decision Table engine
— hit policies over an ordered rule list — shipped as a **pluggable adapter
module** (`adapters/dtable`), with **Go functors as the rule expressions**.
DMN's *structure* is honored (tables, hit policies, output names, the
list-of-rows result); DMN's *languages* (FEEL, serialized definitions) stay
out, per N2.

## 1. Context & problem

ADR-027 landed the seam (`rules.Engine`: evaluate a decision reference
against the data reader → result rows) and the in-core `gorules` registry —
one Go function per decision, a single implicit hit. That covers arbitrary
decision logic but models nothing: a registry function is opaque — no rule
structure, no declarative review surface, no multi-hit semantics.

The DMN-minimal component contract (recorded in ADR-027 §2.1/§2.4 at the
seam's landing) names what the table tier adds:

- an **executable decision table**: a **Hit Policy** (Unique, First, Any,
  Rule Order, Collect) deciding how multiple matching rows resolve; **output
  names** keying the result entries; **rules** as an ordered list of
  condition-set → output-set rows;
- the **result** as a list of records — which the seam already returns and
  the Business Rule Task already folds;
- **deploy + evaluate** as the two-operation component API — with deploy
  already split off the task-facing seam onto the `rules.Deployer`
  capability.

Two prior decisions bind this ADR: a **rule is a behavior contract** (an
interface — *match against the input context, yield outputs*), and the
**table stays data** (ADR-027 §2.4). What remains open — and is decided here
— is placement, the rule-expression language, the hit-policy semantics, and
the deploy question.

## 2. Decision

### 2.1 Placement — a pluggable adapter module, not core

The engine ships as **`adapters/dtable`** — its own Go module in the
adapters tier (the ADR-003 §4.4 layout: adapters import the core, the core
never imports adapters). Consequences, deliberately accepted:

- The core's wired-by-default battery **remains `gorules`** — a zero-option
  engine keeps working with no new dependency weight. The table engine is
  the **second battery**: shipped in the repository, separately versioned,
  opted in with one line (`thresher.WithRuleEngine(dtable.New(...))`).
- The adapter is the seam's **external proof**: a module outside the core
  driving the Business Rule Task through nothing but the public
  `rules.Engine` contract demonstrates the pluggability ADR-027 promised —
  the same role the consumer-smoke plays for the library surface.

*Why not in-core (a `pkg/rules/dtable` sibling):* every in-core package is
weight every embedder carries; the seam exists precisely so decision
machinery beyond the minimal default lives outside. The adapter tier is the
architecture's designated home for exactly this (ADR-002 §4.2, roadmap
WS-E).

### 2.2 Rule expressions — Go functors, the house language

A rule's condition and its (optionally computed) outputs are **plain Go
functions over the engine's read surface** — the same
`service.DataReader` walk-up a `gooper` operation and a `gorules` decision
receive. No expression language is introduced, parsed, or interpreted.

This choice **dissolves DMN's input-expression layer**: DMN separates
"input expressions" (extract values from the context) from "input entries"
(match them); with functor conditions the rule reads whatever process data
it needs directly through the reader. The table keeps DMN's *decision
structure* (policy, ordered rules, named outputs) and delegates *value
access* to the language the embedder already writes — the exact trade
`gooper` made for operations and `gorules` for decisions.

A rule row is built from vocabulary, not code: the adapter provides a
small set of **condition constructors** (equals / greater-than / between /
in-set / any, plus the raw-predicate escape hatch) so a table reads as a
declarative grid (the data-over-code house rule) while each cell compiles
to a functor underneath.

### 2.3 The table — data; the rule — behavior

Exactly the ADR-027 §2.4 split:

- **The table is a data declaration**: a name (the decision reference it
  answers to), a hit policy, and an ordered rule list. It carries no
  evaluation logic of its own.
- **A rule is the ADR-027 behavior contract**: *match against the input
  context* (the reader), *yield outputs* (a result row keyed by output
  names). The adapter's functor-backed row is the first rule kind; a
  compiled-definition row (a future deployable engine's) implements the
  same contract without touching the table machinery — the polymorphism
  the interface exists for.

### 2.4 Hit policies — all five, DMN semantics

The engine implements the five policies of the minimal contract, resolving
the matched rows into the seam's `[]rules.Row`:

| Policy | Result | Deviation guard |
|---|---|---|
| **Unique** | the single matching row | more than one match → a classified evaluation error (the table is contradictory) |
| **First** | the first matching row in rule order | — |
| **Any** | any one matching row | matching rows with *differing* outputs → a classified error (Any asserts they agree) |
| **Rule Order** | all matching rows, in rule order | — |
| **Collect** | all matching rows | no aggregation operators (C+, C<, …) — deferred with the table's own note; bare Collect equals Rule Order's row set |

No match under any policy yields an **empty result** (the seam's "nothing
committed" case — the task proceeds; whether that is an error is the
model's business, not the engine's). Single-row results fold to scalars on
the task side exactly as with `gorules` (ADR-027 §2.3) — a table with a
single-output Unique/First policy drives a gateway condition with zero
ceremony.

### 2.5 Missing input — fail loud by default; DMN tolerance per condition

DMN is null-tolerant: an input expression over an absent context entry
evaluates to null, comparison tests against null simply don't match, and
the rule falls through — missing data degrades silently to "no rule
fired". That is precisely the failure mode this engine's house rules
reject (an unknown decision reference is a loud error; unresolved events
log; silent fall-through hides a typo'd datum name as a process quietly
taking its default path).

Decided: a condition reading an **absent datum fails the evaluation** with
a classified error naming the decision, the rule ordinal, and the missing
name — the task faults through the ordinary machinery, catchable like any
activity failure. Where DMN's tolerance is genuinely wanted, it is
**opted into per condition**: the `IfPresent(cond)` combinator makes an
absent datum a plain no-match for that cell (and `Any()` matches
regardless, like DMN's `-` irrelevant test). No table-wide tolerance
switch in this conception — per-cell visibility keeps the choice in the
reviewable grid; a table-wide option can be added compatibly if demand
appears.

The no-match outcome itself stays DMN-shaped (§2.4): when rules don't
match, the result is empty — never an implicit error.

### 2.6 Deploy — mechanics owned by the engine, the format pluggable

Deployment decomposes into two layers, and the functor constraint blocks
only one of them:

- **Deployment mechanics** — ingest a definition artifact, validate it,
  build an executable table, cache it under its decision name — are
  format-agnostic. The engine owns them and **implements
  `rules.Deployer`** honestly.
- **The definition format** — the artifact → table translation — is a
  **pluggable Decoder seam** on the engine (a `WithDecoder` option; the
  ADR-002 interface-plus-option shape one level down). With no decoder
  configured, `Deploy` fails loud; a future DMN-XML or alternative-format
  decoder plugs in without touching the engine.

The **batteries decoder** is the named-functor hybrid, now decided: a JSON
artifact carrying **structure only** — table name, hit policy, the rule
grid — where each condition cell **references a Go-registered functor by
name** and outputs are JSON literals (or a named yield functor for
computed rows). Go carries all **behavior**: the embedder registers a
condition vocabulary next to the process model — the exact `gooper` mode,
artifacts addressing named Go code. Operations teams re-wire *which rules,
in what order, under which policy* by redeploying the artifact; every
predicate stays compiled, reviewable Go. No expression language enters: an
unresolved name is a classified deploy-time error, never an interpreted
fallback.

**Redeploy semantics:** `Deploy` of an existing decision name **replaces**
the table (deployment is a lifecycle operation — redeploy-updates is the
industry posture); programmatic `Register` keeps **rejecting duplicates**
(construction-time wiring, where a duplicate is a bug). Both are explicit
contract.


### 2.7 Engine notes (deviations & choices)

| Choice | DMN-minimal contract position | Engine choice |
|---|---|---|
| Rule expressions | FEEL entries over an input context | Go functors over the data reader; the input-expression layer is subsumed (§2.2) |
| Deployment | deploy(definition) parses/caches the model | not this engine — programmatic registration; Deployer stays for serialized-definition adapters (§2.6) |
| Collect aggregations | C+, C<, C>, C# operators | deferred; bare Collect only (§2.4) |
| Missing input | FEEL: absent → null → the test doesn't match (silent fall-through); `-` matches anything | **fail loud by default** — an absent datum is a classified evaluation error; DMN tolerance is per-condition opt-in via `IfPresent` (§2.5) — a deliberate deviation per the fail-loud house rule |
| Placement | an embeddable component | an adapter module — the core's default battery stays `gorules` (§2.1) |
| Kind string | — | the adapter mints its own `##`-kind in the ADR-027 §2.2 convention |

## 3. Grounding

| Claim | Source |
|---|---|
| The seam contract: `Evaluate(ref, reader) → []Row`; the engine names its `##`-kind | ADR-027 v.1 §2.1/§2.2 (landed: `pkg/rules`) |
| Rule = behavior contract (match + yield) under a data table; the table tier named as the follow-up | ADR-027 v.1 §2.4 |
| Deploy split onto the `Deployer` capability; the task never deploys | ADR-027 v.1 §2.1 (landed: `pkg/rules`) |
| Hit policy set (Unique, First, Any, Rule Order, Collect); result = list of records | the DMN-minimal component contract recorded at the seam's landing (ADR-027 v.1 §1/§2.6) |
| Adapters are separate modules; core never imports them | ADR-003 §4.4 (enforced: the `.golangci.yml` depguard rules) |
| The functor precedent — in-process Go bodies over the read surface | ADR-011's `gooper` operations; `gorules` decisions (ADR-027 §2.4) |
| No DMN engine in core | SAD-001 v.1 N2 |

## 4. Alternatives considered

- **In-core package (`pkg/rules/dtable`).** Rejected — §2.1: unavoidable
  weight for every embedder; the adapter tier is the designated home; the
  external-proof value is lost.
- **A serialized condition language (JSON operators / comparison
  mini-grammar).** Rejected: it reintroduces an interpreted expression
  language through the back door — a second thing to validate, version,
  and document. The accepted §2.6 shape is strictly narrower: artifacts
  carry *structure* (grid, policy, names); every condition remains a
  named, compiled Go functor. An unresolved name fails deployment loud.
- **Conditions as `data.FormalExpression` (the expression-layer seam).**
  Rejected for now: couples the adapter to the expression workstream's
  in-flight evolution (Script Engine, #74) and buys nothing over functors
  for Go-authored tables. A future rule kind can adapt expressions to the
  Rule contract without touching the table (§2.3).
- **No `Deployer` at all** (the first draft's position: functors aren't
  deserializable, so don't claim the capability). Superseded by the §2.6
  decomposition — the mechanics/format split lets the engine claim the
  capability honestly while the functor decision stands untouched.

## 5. Consequences

**Positive:** decision logic gains a reviewable, declarative table form
with real multi-hit semantics; the seam gets its first external
implementation (pluggability proven); the core stays exactly as light as
ADR-027 left it; the Rule interface's polymorphism is exercised by a
second kind, paving the deployable engine's path.

**Negative / cost:** a new module to version and CI (the module loops pick
it up automatically); five policy semantics to test precisely; embedders
wanting serialized rules still wait for the deployable adapter.

**Follow-ups this conception sets up:** a DMN-XML Decoder plugging into
the §2.6 seam (reusing this engine's mechanics wholesale); Collect
aggregation operators if demand appears; a table-wide absent-tolerance
option if per-cell `IfPresent` proves noisy (§2.5).

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 (Accepted) | 2026-07-25 | Ruslan Gabitov | Accepted with the accompanying SRD's landing: the adapter module, the functor rules with the condition vocabulary, all five hit policies, the fail-loud/`IfPresent` missing-input split, and the Decoder-seam Deploy with the named-functor JSON batteries decoder are all in the engine as conceived. |
| v.1 | 2026-07-24 | Ruslan Gabitov | Draft conception. The ADR-027 table tier lands as **`adapters/dtable`** — a pluggable adapter module (the core's default battery stays `gorules`; the adapter is the seam's external proof) with **Go functors as rule expressions** (conditions and computed outputs read the same `service.DataReader` the gooper/gorules functors do; DMN's input-expression layer is deliberately subsumed; condition constructors keep tables declarative). The table is data (name, hit policy, ordered rules); a rule is the ADR-027 behavior contract, functor-backed as its first kind. All five hit policies with DMN semantics (Unique contradiction and Any disagreement are classified errors; no-match = empty result; Collect aggregations deferred). **Missing input fails loud by default** (an absent datum is a classified evaluation error naming decision/rule/datum — a deliberate deviation from DMN's null-tolerant fall-through, per the fail-loud house rule) with per-condition `IfPresent` opting into the DMN no-match semantics; `Any()` matches like DMN's `-`. **Deploy decomposed and claimed**: the engine owns the deployment mechanics and implements `rules.Deployer`; the definition format is a pluggable **Decoder seam** (`WithDecoder`; no decoder → loud failure); the batteries decoder is structure-only JSON wiring **named Go-registered functors** (behavior stays compiled Go; no condition language in artifacts; unresolved names fail deploy). Deploy replaces an existing decision, programmatic Register rejects duplicates. Implementation rides the accompanying SRD. |
