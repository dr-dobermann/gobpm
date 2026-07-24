# SRD-062 — The Decision Table engine adapter (`adapters/dtable`)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-24 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-029 v.1](../design/ADR-029-decision-table-engine-adapter.md) (the whole conception: adapter-module placement, functor rules, the five hit policies, fail-loud missing input with `IfPresent`, the Decoder-seam Deploy with the named-functor JSON batteries decoder) |
| Upstream | [ADR-027 v.1](../design/ADR-027-business-rule-task-and-rule-engine-seam.md) §2.1/§2.4 (the `rules.Engine` seam this implements; the Rule behavior contract), [ADR-003 v.1](../design/ADR-003-module-layout.md) §4.4 (the adapter module tier + import direction), [ADR-002 v.2](../design/ADR-002-extension-architecture.md) §4.2 |
| Refines | SRD-060 v.1 (the seam + `gorules` landing this builds on) — sideways |

Lands ADR-029 v.1: the `adapters/dtable` module — the first out-of-core
`rules.Engine`, a Decision Table over Go-functor rules with DMN hit-policy
semantics — plus its e2e proof through the Business Rule Task and a runnable
example.

## §1 Background

- The seam is landed and stable: `rules.Engine`
  (`pkg/rules/rules.go` — `Type() string`, `Evaluate(ctx, decisionRef,
  service.DataReader) ([]rules.Row, error)`), `rules.Row`
  (`map[string]data.Value`), the task-side 1×1 fold, and the `##`-kind
  convention. `gorules` is the in-core default (SRD-060).
- The adapter tier exists structurally: `adapters/` with the `sqlite`
  scaffold module (`module github.com/dr-dobermann/gobpm/adapters/sqlite`),
  depguard enforcing core⊅adapters and no cross-adapter imports
  (`.golangci.yml` `core-no-runtime-no-adapters` /
  `adapters-no-cross-adapter`), and the Makefile module loops discovering
  every `go.mod` (`MODULES := $(shell find . -name go.mod ...)` —
  `CORE_MODULES` includes adapters, so tidy/lint/build/test pick the new
  module up with zero Makefile changes).
- The functor read surface: `service.DataReader` (GetData/GetDataByID/
  GetSources/List), satisfied by the runtime environment
  (`internal/instance/execenv.go:119` — the same surface `gooper`/`gorules`
  functors receive).
- Core version for the module's `require`: tag `v0.9.0` exists; in-repo
  builds resolve through `replace ../..` (the examples-module precedent).

## §2 Requirements

### §2.1 Functional

- **FR-1 — the module.** New `adapters/dtable` Go module
  (`module github.com/dr-dobermann/gobpm/adapters/dtable`; `require
  github.com/dr-dobermann/gobpm v0.9.0` + `replace ../..` for in-repo
  builds). No dependencies beyond the core and testify (tests only).
- **FR-2 — the Rule contract and the functor kind.**
  - `dtable.Rule` — the ADR-027 behavior contract:
    `Matches(ctx, r service.DataReader) (bool, error)` and
    `Yield(ctx, r service.DataReader) (rules.Row, error)`.
  - The functor-backed kind, built declaratively:
    `R(conds ...Condition)` → `.Then(out rules.Row)` (static outputs) or
    `.ThenF(f func(ctx, r) (rules.Row, error))` (computed). A rule with no
    conditions matches always (the all-`Any` row). `Then`/`ThenF` validate
    (nil/empty output rejected at build).
- **FR-3 — the condition vocabulary.** `type Condition func(ctx,
  r service.DataReader) (bool, error)` with constructors: `Eq(datum, want)`,
  `NE`, `GT`, `GE`, `LT`, `LE(datum, than)`, `Between(datum, lo, hi)`
  (inclusive), `In(datum, set...)`, `Any()`, and the escape hatch
  `Pred(fn Condition)`. Ordered comparisons support `int`, `int64`,
  `float64`, `string`; a type mismatch between the datum's value and the
  operand is a **classified error** (fail loud — never a silent false).
  Equality (`Eq`/`NE`/`In`) uses deep equality over the datum's
  `Value().Get(ctx)`.
- **FR-4 — missing input (ADR-029 §2.5).** A constructor reading an absent
  datum returns a classified error; the engine wraps it with the decision
  name and the rule ordinal (FR-6), and the task faults ordinarily.
  `IfPresent(c Condition) Condition` converts a failed datum *read* into a
  plain no-match for that cell. `Any()` never reads, so it matches
  regardless.
- **FR-5 — the table.** `NewTable(name string, policy HitPolicy,
  rules ...Rule) (*Table, error)`: non-empty name, a known policy, at least
  one rule, no nil rules. The table is data — name + policy + ordered rule
  list; getters only.
- **FR-6 — the engine.** `New(opts ...Option) (*Engine, error)` — a
  registry of tables keyed by
  their name (= the decision reference); `Register(t *Table) error`
  (nil/duplicate rejected), `MustRegister` fixture twin; `Type() =
  "##DTable"`; `Evaluate` resolves the table (unknown ref → classified
  error, the `gorules` posture), iterates rules **in order** (`Matches`
  first, `Yield` only for matching rules), and resolves via the hit policy.
  Rule-level errors are wrapped with `decision_ref` + `rule` ordinal.
  RWMutex-guarded like `gorules` (register at setup, evaluate from
  concurrent tracks).
- **FR-7 — hit policies (ADR-029 §2.4).** `HitPolicy` string enum:
  `Unique`, `First`, `Any`, `RuleOrder`, `Collect`. Resolution is a
  **data-declared dispatch** (`map[HitPolicy]resolver` — the data-over-code
  house rule):
  - `Unique`: 2+ matches → contradiction error naming the ordinals;
  - `First`: evaluation may short-circuit after the first match;
  - `Any`: all matched rows must be equal (deep equality over extracted
    values) — disagreement is an error; one row returned;
  - `RuleOrder` / `Collect`: all matched rows in rule order.
  No match → `(nil, nil)` — the seam's empty result.

- **FR-8 — the Deploy mechanics + the Decoder seam (ADR-029 §2.6).**
  - `dtable.Decoder` — `Decode(definition []byte) (*Table, error)`;
    configured at construction: `New(WithDecoder(d))` (nil rejected).
  - `Engine` implements `rules.Deployer`: `Deploy(ctx, definition)` — no
    decoder configured → classified error; decode error → wrapped
    classified error; a decoded table **replaces** an existing
    same-name table (redeploy-updates; `Register` keeps rejecting
    duplicates — both contracts documented and tested).
- **FR-9 — the batteries decoder: named-functor JSON.**
  - `dtable.Vocabulary` — the Go-registered behavior: named `Condition`s
    (`AddCondition(name, c) error`) and named yield functors
    (`AddYield(name, f) error`); empty/duplicate/nil rejected;
    `MustAddCondition`/`MustAddYield` fixture twins returning the
    vocabulary for chaining.
  - `NewJSONDecoder(v *Vocabulary) (*JSONDecoder, error)` (nil rejected) —
    decodes the structure-only artifact:

    ```json
    {
      "name": "discount",
      "hitPolicy": "FIRST",
      "rules": [
        {"when": ["gold-tier", "big-order"], "then": {"discount_pct": 15}},
        {"when": [], "thenFn": "default-discount"}
      ]
    }
    ```

    `when` entries resolve against the vocabulary's conditions (empty
    `when` = match-always); `then` literals become `values.NewVariable`
    outputs (JSON scalars; numbers land as `float64` — documented);
    `thenFn` resolves a named yield functor (`then` XOR `thenFn`,
    exactly one). **Any unresolved name, unknown policy, or malformed
    grid is a classified deploy-time error** — never an interpreted
    fallback (no condition language in artifacts).

### §2.2 Non-functional

- **NFR-1 — validate-all-params** on every public constructor/option;
  no `Must*` calls in the module's non-test code (the FIX-026 guard covers
  only `pkg/`+`internal/`, but the rule is repo-wide policy — the adapter
  is written to it; `MustRegister` as a provided twin is fine).
- **NFR-2 — the import direction**: the module imports only the core
  (depguard's no-cross-adapter rule applies structurally).
- **NFR-3 — coverage**: `make ci` green (the module loops pick the module
  up); diff-coverage ≥95%; touched functions ≥80% (aim 100% — the module is
  new and pure).

## §3 Models (shapes)

```go
// adapters/dtable (package dtable)

const DTableType = "##DTable" // the engine kind (ADR-027 §2.2 convention)

type Condition func(ctx context.Context, r service.DataReader) (bool, error)

type Rule interface {
	Matches(ctx context.Context, r service.DataReader) (bool, error)
	Yield(ctx context.Context, r service.DataReader) (rules.Row, error)
}

func R(conds ...Condition) *RuleBuilder
func (b *RuleBuilder) Then(out rules.Row) (Rule, error)
func (b *RuleBuilder) ThenF(
	f func(context.Context, service.DataReader) (rules.Row, error),
) (Rule, error)

type HitPolicy string

const (
	Unique    HitPolicy = "UNIQUE"
	First     HitPolicy = "FIRST"
	AnyMatch  HitPolicy = "ANY"
	RuleOrder HitPolicy = "RULE ORDER"
	Collect   HitPolicy = "COLLECT"
)

func NewTable(name string, policy HitPolicy, rr ...Rule) (*Table, error)

type Engine struct{ /* tables map[string]*Table; sync.RWMutex */ }

func New(opts ...Option) (*Engine, error) // Option: WithDecoder (nil rejected)
func (e *Engine) Register(t *Table) error
func (e *Engine) MustRegister(t *Table) *Engine
func (e *Engine) Type() string
func (e *Engine) Evaluate(ctx context.Context, decisionRef string,
	r service.DataReader) ([]rules.Row, error)

// FR-8: the Deployer half + the format seam.
type Decoder interface {
	Decode(definition []byte) (*Table, error)
}

func (e *Engine) Deploy(ctx context.Context, definition []byte) error

// FR-9: the batteries decoder — structure-only JSON over named Go behavior.
type Vocabulary struct{ /* named Conditions + named yield functors */ }

func NewVocabulary() *Vocabulary
func (v *Vocabulary) AddCondition(name string, c Condition) error
func (v *Vocabulary) AddYield(name string,
	f func(context.Context, service.DataReader) (rules.Row, error)) error

func NewJSONDecoder(v *Vocabulary) (*JSONDecoder, error)
```

(`New` gains `(..., error)` for the nil-decoder guard — the
validate-all-params rule; the seam assert becomes
`var _ rules.Engine = (*Engine)(nil)` **and**
`var _ rules.Deployer = (*Engine)(nil)`.)

(`AnyMatch` avoids shadowing the `Any()` condition constructor; the policy
STRING values follow the DMN table-notation names.)

## §4 Analysis & decisions

- **§4.1 `Rule` lives in the adapter, not `pkg/rules`.** The seam stays
  one interface + one row type; the Rule contract is table machinery — a
  future definition-carrying engine either imports this adapter's table
  layer or declares its own kind. Promoting the interface into the core is
  a compatible later move if a second engine wants it (noted in ADR-029
  follow-ups).
- **§4.2 Absent-vs-failed reads.** The runtime reader reports a missing
  datum as an error (there is no absent-sentinel in `service.DataReader`),
  so `IfPresent` treats **any failed read inside its wrapped condition** as
  no-match — documented on the combinator. This is deliberately coarse:
  distinguishing "not found" from other read failures would couple the
  adapter to internal error classes across the module boundary.
- **§4.3 `Any`-policy equality.** Matched rows are compared by their
  extracted values (`Value().Get(ctx)` per key, `reflect.DeepEqual`) — Row
  holds `data.Value` interfaces whose identities differ even when values
  agree.
- **§4.4 Deploy replaces, Register rejects (ADR-029 §2.6).** Deployment
  is a lifecycle operation — a redeploy carries an updated artifact and
  must supersede; programmatic registration is construction-time wiring
  where a duplicate is a bug. The replace is atomic under the engine's
  write lock; in-flight evaluations that already resolved the old table
  finish on it (the swap is by map entry, not table mutation).
- **§4.5 JSON literals type as `float64` for numbers** (encoding/json's
  untyped-number default) — documented on the decoder; a condition
  comparing a deployed literal against an `int` datum hits FR-3's
  loud type-mismatch error, steering table authors to yield functors or
  matching datum types. (Deliberately NOT auto-coerced — silent numeric
  coercion is the DMN-null class of surprise this ADR line rejects.)
- **§4.6 `First` short-circuits; `Unique` cannot.** `Unique` must scan all
  rules to detect contradiction — the policy resolver receives lazily
  accumulated matches, and the iterator stops early only when the policy
  allows (`First`). Yield runs only for rules whose `Matches` returned
  true (a non-matching rule's outputs are never computed).

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | condition vocabulary unit | FR-3: every constructor over a stub reader (int/int64/float64/string ordered ops; Eq/NE/In deep equality; Between inclusive bounds; Any; Pred); type-mismatch → classified error |
| T-2 | missing input | FR-4: a bare condition on an absent datum → error carrying decision/rule/datum context (via T-4's engine wrap); `IfPresent` → no-match, evaluation proceeds; `IfPresent` + present datum behaves as the inner condition |
| T-3 | rule builder + table validation | FR-2/FR-5: `R().Then` matches-always; nil/empty outputs rejected; `NewTable` rejects empty name / unknown policy / no rules / nil rule |
| T-4 | engine + policies | FR-6/FR-7: per-policy tables — Unique single + contradiction error (ordinals named); First picks first and short-circuits (probe: a later rule's condition not evaluated); Any agreement + disagreement error; RuleOrder/Collect return all in order; no-match → empty; unknown ref → error; duplicate/nil Register rejected; `Type()` |
| T-4a | Deploy mechanics | FR-8: no decoder → error; decode failure wrapped; Deploy registers a new table; Deploy REPLACES an existing one (old rows gone); Register still rejects duplicates; both asserts (`rules.Engine` + `rules.Deployer`) compile |
| T-4b | JSON decoder unit | FR-9: the worked artifact above decodes and evaluates; unresolved `when`/`thenFn` names, unknown policy, `then`+`thenFn` both/neither, malformed JSON → classified errors; empty `when` = match-always; float64 literal typing observable |
| T-5 | e2e through the Business Rule Task (adapter test importing `pkg/thresher`) | the full seam proof: `thresher.New(WithRuleEngine(dtable.New()...))`, a BRT evaluates a Unique table against live process data, the 1×1 fold routes conditional flows; and the fail-loud path — a missing datum faults the instance |
| T-6 | example (`examples/decision-table/`) | smoked exit 0: a discount table (First policy, ≥2 output columns on one row → the row-list commit path) visibly drives the flow |

## §7 Milestones

- **M1 — the adapter module (evaluation half).** FR-1…FR-7; T-1…T-4.
  `feat(adapters): dtable — the Decision Table rule engine (SRD-062 M1)`.
- **M2 — Deploy + the JSON decoder.** FR-8, FR-9; T-4a, T-4b.
  `feat(adapters): dtable — Decoder-seam Deploy + named-functor JSON (SRD-062 M2)`.
- **M3 — e2e + example + doc sync.** T-5, T-6 (the example deploys its
  table from an embedded JSON artifact over a Go vocabulary — showing the
  full deploy+evaluate component);
  `examples/decision-table/` (+ index row, gitignore, mermaid README);
  CHANGELOG; README(+ru) tour update (the BRT paragraph gains the adapter
  sentence); roadmap WS-E (`adapters/dtable` — the first landed adapter);
  ADR-027 §2.4's "deferred to its own conception" stands (frozen doc — no
  retro-edit; ADR-029 fulfils it).
  `feat(adapters): dtable — e2e, example, doc sync (SRD-062 M3)`.

## §8 Cross-doc

- Implements **ADR-029 v.1** (whole conception).
- Upstream: **ADR-027 v.1** §2.1/§2.4, **ADR-003 v.1** §4.4,
  **ADR-002 v.2** §4.2.
- Sideways: **SRD-060 v.1** (the seam landing).
- Tracking: the BRT half of #87 is delivered; this lands the ADR-027
  named follow-up — reference the PR to #87 as `Part of #87` (the epic
  covers "Business Rule Task (DMN)" tooling; Script Task still rides #74).

## §9 Definition of Done

- [ ] FR-1…FR-9 implemented; every §6 test exists and passes.
- [ ] `make ci` green with the new module in the loops (tidy/lint/build/
      test); diff-coverage ≥95% (aim 100%); touched functions ≥80%.
- [ ] The FIX-026 posture holds in the adapter (no `Must*` calls outside
      tests; error-returning constructors throughout).
- [ ] `examples/decision-table/` runs exit 0; binary gitignored; index row.
- [ ] §10 filled; README(+ru)/roadmap/CHANGELOG synced; ADR-029 + this SRD
      flipped at landing.

## §10 Implementation summary

*Filled at landing.*

## Open questions

*None — §4 resolves the design points inline.*

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-24 | Ruslan Gabitov | Initial draft — lands ADR-029 v.1: the `adapters/dtable` module (first out-of-core `rules.Engine`): the `Rule` contract + functor kind (`R(...).Then/ThenF`), the condition vocabulary (`Eq/NE/GT/GE/LT/LE/Between/In/Any/Pred` + `IfPresent`), fail-loud missing input with engine-side decision/rule wrapping, the data-declared five-policy dispatch (`Unique/First/Any/RuleOrder/Collect`, DMN semantics, no-match = empty), the table-registry engine (`##DTable`) implementing `rules.Deployer` through the pluggable **Decoder seam** with the **named-functor JSON batteries decoder** (structure-only artifacts over a Go-registered `Vocabulary`; Deploy replaces / Register rejects), e2e through the Business Rule Task, and `examples/decision-table/` (deploying its table from an embedded artifact). Three milestones. |
