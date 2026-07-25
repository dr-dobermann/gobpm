# SRD-067 — The `gobpm:lite` text-expression evaluator

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-25 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-032 v.1](../design/ADR-032-language-routed-expression-engines.md) §2.3 (the stdlib-only battery language: the value model with float64 numeric unification, structural-path data access, the tiny builtin set, the `gobpm:lite` claim in the zero-config registry) + §2.4 (no per-evaluation facts) |
| Upstream | [SRD-066 v.1](SRD-066-expression-engine-registry-and-text-expressions.md) (the registry and the text kind this engine plugs into) — sideways; [ADR-011 v.7](../design/ADR-011-process-data-flow.md) §2.9.2 (the structural-path resolver lite rides) |

Lands ADR-032's evaluator half and closes **#74**: the in-core `##Lite`
engine interpreting the `gobpm:lite` text language over process data,
registered beside `goexpr` in the batteries default — out of the box a
model mixes functor and text expressions freely.

## §1 Background

- The routing half is landed (SRD-066 v.1): `expression.Registry` routes
  each expression by its `Language()`; `data.TextExpression` carries
  language + body behind the `data.BodyHolder` capability
  (`pkg/model/data/text_expression.go`); the batteries default today is
  `goexpr` alone (`pkg/thresher/thresher.go` — the registry build in
  `New`; `internal/enginert/enginert.go:72` — `defaultExprRegistry`).
- The runtime data surface already resolves **structural paths**: the
  frame's `GetData` hands any name to `data.ResolvePath`
  (`internal/scope/frame.go:196`; the resolver —
  `pkg/model/data/path.go:189`), so `order.items[0].price` and
  `rates["EUR"]` arrive at a `Source.Find` as plain names. Lite therefore
  needs **no path logic of its own** — it passes the whole path token to
  `src.Find(ctx, path)`.
- The value model lite reads (the `values` package): `Variable[T]`,
  `Array[T]` (`Count()`), `Map[T]` (`Keys()`, `Entry()`), `Record`.
  `Variable[T].Type()` names the payload type via reflection
  (`variable.go:61`) — `bool`, `float64`, `string`, `Time` — the same
  vocabulary `GExpression.ResultType()` exposes and the condition paths
  check (`pkg/model/activities/activities.go:9`: `resultTypeBool =
  "bool"`; the guard runs **before** evaluation, SRD-066 §10).
- The condition consumers assert the evaluated value with an unchecked
  `res.Get(ctx).(bool)` (`pkg/model/activities/flowselect.go:103`) — an
  engine returning a declared-type-mismatched value would panic the
  track. The engine side must therefore fail loud on a result-type
  mismatch instead of returning it.
- The timer layer's value type is `time.Time`; RFC3339 is the repo's
  interchange format for time literals (JSON, dtable precedent).

## §2 Requirements

### §2.1 Functional

- **FR-1 — the language core (lexer + parser).** Literals: numbers
  (`150`, `1.5` — all parse to **float64**, ADR-032 §2.3's numeric
  unification), strings (single- or double-quoted, `\`-escapes for the
  quote and the backslash), `true`/`false`, `nil`. Names: bare
  identifiers and **structural paths** (`order.total`, `items[0]`,
  `rates["EUR"]`) lexed as one data-reference token. Operators, loosest
  to tightest: `or` → `and` → `not` → comparisons
  (`== != < <= > >=`) → additive (`+ -`) → multiplicative (`* / %`) →
  unary `-` → primary (literal, data ref, builtin call, `( )`).
  A syntax error is a classified error naming the **byte offset** and
  what was expected. The parse runs **per evaluation** (stateless
  engine; lite expressions are tiny — §4.3).
- **FR-2 — evaluation semantics.** Operand kinds: float64, string, bool,
  `time.Time`, nil. The numeric family (all Go int/uint widths, float32,
  float64) read from data **unifies to float64**. Comparisons: numbers,
  strings and times order (`< <= > >=`) and equate within their own
  kind; bools and nil equate only (`==`/`!=`); **every cross-kind
  comparison is a loud classified error** — never a silent false.
  `and`/`or` **short-circuit** and require bool operands; `not` requires
  bool. `+` adds two numbers **or** concatenates two strings; `- * /`
  and unary `-` require numbers; `%` is `math.Mod`; division by zero is
  a loud error. Data reads go through `src.Find(ctx, <full path>)`; a
  **missing datum or dead path fails loud** (the resolver's own error
  relayed with the expression context). A whole-expression result of
  `nil` is a loud error — `nil` is a comparison operand, not a value an
  expression may produce (`values.Variable` cannot carry it: its
  `Type()` reflects on the payload).
- **FR-3 — builtins (`has`, `len`, `time` — nothing else).**
  `has(<string>)` probes a name-or-path: resolution success → `true`,
  **any** resolution failure → `false` (the explicit opt-out of
  fail-loud, third time the house split). `len(x)`: an `Array` →
  `Count()`, a `Map` → `len(Keys())`, a string → **rune count**; any
  other operand is a loud error; the result is float64. `time(<string>)`
  parses an **RFC3339** literal to `time.Time`; a malformed literal is a
  loud error. Unknown function names are loud parse-time errors.
- **FR-4 — the engine surface.** Package `pkg/model/expression/lite`:
  `New() *Engine`; `Type() = "##Lite"`; `Languages() =
  {lite.Language}` with `Language = "gobpm:lite"`. `Evaluate(ctx, expr,
  src)` validates all params, requires `expr` to implement
  `data.BodyHolder` (loud error naming the capability otherwise),
  evaluates the body against `src`, and returns the result as a
  `values.Variable` of the produced Go kind. When the expression
  **declares a result type** (`ResultType() != ""`), the produced
  value's `Type()` must match it — a mismatch is a **loud error** (the
  §1 panic guard: a declared-`bool` condition can never hand a string to
  `flowselect.go:103`).
- **FR-5 — conveniences.** `lite.Expr(body string, opts
  ...options.Option)` mints a `data.TextExpression` pre-tagged
  `gobpm:lite` (ADR-032 §2.3 — lite as the default *text* language
  without any registry fallback). `lite.Cond(body string, opts
  ...options.Option)` = `Expr` + `data.WithResultType("bool")` — the
  one-call condition constructor (SRD-066 §10: the declaration is
  load-bearing on the condition paths).
- **FR-6 — the batteries wiring.** The zero-config registry becomes
  **`goexpr` + `lite`** in both builders: `thresher.New` (the prepended
  defaults) and `enginert.defaultExprRegistry`. The startup routing
  table shows both claims (`exprLanguage gobpm:goexpr: ##GoExpr`,
  `exprLanguage gobpm:lite: ##Lite`);
  `WithoutDefaultExpressionEngines()` removes both. SRD-066's T-4
  assertions update to the widened default (`##GoExpr+##Lite`).
- **FR-7 — the example.** `examples/expression-routing/`: one process
  showing the expression layer at **three consumer sites with two
  engines mixed** (per the owner's direction at the M2 gate) — a task's
  outgoing flows carrying a lite text condition (record path) beside a
  `goexpr` functor (map key), an exclusive gateway branching on a lite
  `time()` comparison with a default flow, and a UserTask whose
  **assignee is computed by a lite string expression**
  (`WithAssigneeExpr`); split per the >80-line rule; the examples index
  and both READMEs gain the entry.

### §2.2 Non-functional

- **NFR-1 — stdlib-only.** No new `go.mod` dependency — the in-core
  placement contract (ADR-032 §2.3).
- **NFR-2 — validate-all-params**; no `Must*` calls in library paths
  (the muststyle guard covers the new package).
- **NFR-3 — no per-evaluation facts** (ADR-032 §2.4): no Reporter calls
  anywhere in `lite`.
- **NFR-4 — coverage**: `make ci` green; diff-coverage ≥95% (aim 100%);
  touched functions ≥80%.

## §3 Models (shapes)

```go
// pkg/model/expression/lite (FR-4/FR-5)
const Language = "gobpm:lite"

func New() *Engine
func (e *Engine) Type() string        // "##Lite"
func (e *Engine) Languages() []string // {Language}
func (e *Engine) Evaluate(ctx context.Context, expr data.FormalExpression,
	src data.Source) (data.Value, error)

func Expr(body string, opts ...options.Option) (*data.TextExpression, error)
func Cond(body string, opts ...options.Option) (*data.TextExpression, error)

// internals (unexported): lexer.go (token stream over the body),
// parser.go (recursive-descent → ast nodes), eval.go (ast walk against
// data.Source; operand = one of float64|string|bool|time.Time|nil).
```

Worked full-path trace (the ADR-032 §2.3 table rows are the fixture
set): a process property `order` = `values.Record{total: 150 (int),
customer: Record{tier: "gold"}}`; the flow condition
`lite.Cond(`order.total > 100 and order.customer.tier == "gold"`)`.
The activity's flow-selection guard sees `ResultType() == "bool"`,
the registry routes `gobpm:lite` → `##Lite`, lite lexes
`order.total` as one data-reference token, `src.Find(ctx,
"order.total")` walks the record (`ResolvePath`), the int 150 unifies
to float64, both comparisons hold → `values.NewVariable(true)` — the
lane fires.

## §4 Analysis & decisions

- **§4.1 Paths delegate to the landed resolver.** Lite lexes a
  structural path as one token and hands it to `src.Find` verbatim —
  the frame already routes every name through `data.ResolvePath`
  (§1). No second path grammar, no drift risk against SRD-042.
- **§4.2 Result-type enforcement lives in the engine.** The condition
  paths check the *declaration* before evaluation but assert the
  *value* unchecked after it (§1) — the engine is the only place that
  sees both. `goexpr` is safe by construction (its result type IS the
  functor's ItemDefinition); lite must check explicitly.
- **§4.3 Parse per evaluation, no cache.** Lite expressions are tiny
  (one line); a body-keyed cache adds a concurrent map and an
  invalidation story for no measured need. Revisit only with profile
  evidence.
- **§4.4 `has` swallows all resolution errors.** An existence probe
  that faulted on "exists but not Ready" would force callers back into
  try/catch reasoning; `has` answers exactly one question —
  "will reading this succeed right now" (mirrors the rules/lua
  `IfPresent`/`has` precedents).
- **§4.5 Rune count for `len` on strings.** A text DSL user counting
  characters expects `len("привет") == 6`, not 12 (Go's byte-`len`);
  arrays/maps count elements — consistent "how many things" semantics.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | lexer/parser unit (`lite`) | FR-1: literals, paths-as-tokens, precedence (`a or b and c`, `not` binding, unary `-`), parse errors with offset, unknown function loud |
| T-2 | the ADR table (`lite`) | FR-2/FR-3: all ten ADR-032 §2.3 worked-example rows verbatim over real `values` kinds (int/float/string/bool/time Variables, Array, Record, Map) |
| T-3 | type discipline (`lite`) | FR-2: cross-kind comparisons loud (string vs number, time vs number, bool ordering), non-bool `and`/`not` loud, `/ 0` loud, short-circuit proven (the right side would fault), nil equality, a whole-expression nil result loud |
| T-4 | builtins (`lite`) | FR-3: `has` true/false on present/absent/dead-path, `len` on array/map/string-runes + loud on a number, `time` parse + malformed loud |
| T-5 | the engine surface (`lite`) | FR-4: `##Lite`/claims; non-`BodyHolder` expression loud; declared-`bool` + string result loud (the mismatch guard); nil/missing params rejected |
| T-6 | wiring (`pkg/thresher`, `internal/enginert`) | FR-6: zero-config `##GoExpr+##Lite` + both routing lines; opt-out removes both (SRD-066 T-4 updated) |
| T-7 | e2e (`pkg/thresher`) | a process with a `lite.Cond` text condition and a functor condition side by side, zero extra registration — both lanes route and fire (the ADR's "mixes freely" promise) |
| T-8 | example smoke | FR-7: `timeout 25 go run .` in `examples/expression-routing/` exits 0 — all three sites fire (both intake lanes, the urgent gateway branch, the lite-assigned approval completed by the matching actor) |

## §7 Milestones

- **M1 — the evaluator and the engine.** FR-1…FR-5; T-1…T-5.
  `feat(expression): the gobpm:lite text-expression evaluator (SRD-067 M1)`.
- **M2 — batteries wiring, e2e, the example, doc sync.** FR-6/FR-7;
  T-6…T-8; CHANGELOG (the evaluator half), READMEs + examples index.
  `feat(thresher): gobpm:lite in the batteries registry (SRD-067 M2)`.

## §8 Cross-doc

- Implements **ADR-032 v.1** §2.3/§2.4 — closes the ADR (both halves
  landed → the ADR flips Accepted with this landing).
- Sideways: **SRD-066 v.1** (the registry/text kind), **SRD-042 v.1**
  (rides its resolver via the Source, no direct coupling).
- Closes **#74** (the PR carries `Closes #74`).

## §9 Definition of Done

- [ ] FR-1…FR-7 implemented; every §6 test exists and passes.
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); touched functions
      ≥80%; no new `go.mod` dependency.
- [ ] The example runs (T-8); READMEs/examples index synced; CHANGELOG
      synced.
- [ ] §10 filled; ADR-032 flipped Accepted; linked docs synced.

## §10 Implementation summary

*Filled at landing.*

## Open questions

*None — §4 resolves the design points inline.*

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-25 | Ruslan Gabitov | Initial draft — the ADR-032 §2.3 battery: the stdlib-only `gobpm:lite` evaluator (`##Lite`; float64 numeric unification, loud cross-kind comparisons, short-circuit booleans, structural paths via the landed resolver, `has`/`len`/`time` builtins), result-type enforcement in the engine (the condition-path panic guard), the `Expr`/`Cond` conveniences, `goexpr`+`lite` as the zero-config batteries, an example, and the #74 closure. |
