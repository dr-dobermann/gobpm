# ADR-027 — The Business Rule Task and the pluggable rule-engine seam

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-22 |
| Owner | Ruslan Gabitov |
| Refines | [SAD-001 v.1](SAD-001-vision-and-architecture.md) §11 (extension architecture) and non-goal **N2** ("no DMN engine; may integrate via `BusinessRuleTask` calling an external DMN engine"), [ADR-002 v.2](ADR-002-extension-architecture.md) §4.1/§4.2 (the engine-level extension pattern this ADR instantiates: interface + in-core default + injection), [ADR-012 v.1](ADR-012-execution-layering.md) (the `exec.NodeExecutor` contract the task implements) |

The Business Rule Task is the last conformance-scope task type with no
execution semantics. This ADR decides how it executes: a **pluggable Business
Rule Engine (BRE) seam** in the ADR-002 shape — a minimal interface, a small
**batteries-included in-core default**, and one injection point — with the
task itself reduced to the standard's own contract: *call the rule, complete
on its return, put the result into process data*.

## 1. Context & problem

The model layer carries a bare stub — a `BusinessRuleTask` type with a single
`implementation` field and no constructor, no execution, not even a
`flow.TaskType` registration. Nothing can build or run one.

The standard is deliberately minimal here (§13.3.3): upon activation "the
associated business rule is called"; on its completion "the Business Rule
Task completes." The element's **only own attribute is `implementation`** — a
string hint for the invocation mechanism (the `##WebService` /
`##Unspecified` convention; this engine already mints its own `##GoOper` for
in-process Go operations). Critically, the base metamodel carries **no
decision reference and no rule-engine binding** — how a task names its
decision and what evaluates it is vendor territory, and the extract's engine
note says exactly that: "the spec does not mandate a rule engine binding.
Typical wiring is to DMN."

Two prior decisions frame the answer:

- **SAD-001 N2** (permanent non-goal): gobpm will never embed a DMN engine.
  Decisions are evaluated by an **external** (or embedder-supplied) engine;
  the Business Rule Task is the integration point.
- **ADR-002 §4.1/§4.2**: an engine service is an **interface with a bundled
  in-core default**, registered once at engine construction via a functional
  option, reached by nodes through the runtime environment. Its
  interface-design principle: "stick as tightly as possible to the
  established industry interface" — for decision evaluation the industry
  shape (DMN) is *evaluate a named decision against an input context,
  producing an output context*.

## 2. Decision

### 2.1 The seam — `rules.Engine`, one method, industry-shaped

A new engine-level extension point, the **Business Rule Engine**:

- **Interface**: a single evaluation method — *evaluate the named decision
  against the process-data context, return the decision result*. The input
  surface is the engine's read-only data reader (the same walk-up surface an
  in-process Go operation receives); the output is **one structured result
  value** (a single item — the engine's structural values, records/lists/
  maps, carry arbitrarily shaped decision outputs). The engine also names its
  kind (the `##`-convention type string) for the task's `implementation`
  attribute and the startup-config printout.
- **Placement**: a top-level engine-service package (the `WorkerDispatcher`
  neighborhood, not `pkg/model/*`): unlike `FormalExpression` evaluation, a
  business rule is not a BPMN-modeled artifact the engine evaluates — the
  standard's binding is open, so the seam is engine infrastructure, not a
  spec concept.
- **Wiring**: the ADR-002 five-point pattern — the engine-config field, the
  in-core default, a `WithRuleEngine`-style injection option (nil-guarded per
  the validate-all-params rule), a runtime accessor on the engine-services
  surface nodes already receive, and the startup-config line that makes the
  chosen engine operator-visible.

*Why one method:* every extension precedent that aged well here is a thin,
single-purpose interface (expression evaluation, clock, broker). A wide
DMN-flavored surface (decision tables, hit policies, model management) would
bake one vendor's model into the seam — exactly what N2 forbids. Deployment,
versioning, and hit policies live behind the engine.

### 2.2 Decision addressing — a reference string, resolved by the engine

A Business Rule Task names its decision by a **decision reference** — an
opaque string the configured engine resolves (a DMN decision id/key for an
external engine; a registered name for the in-core default). This is an
**engine choice**: the base metamodel has no such attribute (vendor
extensions carry it in every mainstream engine), and a by-reference decision
matches the industry reality that decisions are engine-resident artifacts
with their own lifecycle, not process-model content.

The task's `implementation` attribute holds the configured engine's kind
string (the `##GoOper` precedent) — resolved at execution, not construction,
so a model registered once runs under whichever engine the embedder wired.

### 2.3 Execution semantics — call, complete, commit the result

Exactly the standard's clause, realized on the existing task machinery:

- **On activation**: the task calls the configured engine with its decision
  reference and the read surface. The call is synchronous from the token's
  point of view — the task's token waits exactly as it does for an
  in-process service operation.
- **On return**: the result value is committed to process data through the
  execution frame (the node-result path every task uses) — the result item's
  name addresses the variable, so downstream nodes and gateway conditions
  read it by the ordinary data walk-up. This is the result-variable mapping:
  no special mapper, the frame commit *is* the mapping.
- **On failure**: an evaluation error fails the task through the ordinary
  fault path — a typed business error travels the Error machinery (boundary,
  scope chain) like any activity failure; an unknown decision reference is a
  loud classified error, never a silent no-op. Boundary events, loops and
  Multi-Instance markers, `isForCompensation` — everything an activity
  carries — apply unchanged: the Business Rule Task is a standard activity
  whose "work" is one engine call.

### 2.4 Batteries included — the in-core default engine

The bundled default is a **decision registry**: the embedder registers named
decisions as Go functions (the read-surface-in, structured-value-out shape —
the "gooper of rules"). Properties:

- **Small and bounded**: a static map populated only by explicit
  registration — no growth at runtime, satisfying ADR-002's bounded-default
  rule by construction. Duplicate registration and empty names are rejected;
  evaluating an unregistered decision is a classified error (fail loud,
  never a silent default).
- **Genuinely useful, not a mock**: in-Go decision logic is the natural
  batteries-included tier for a library embedder (the `goexpr` /
  `localdispatcher` precedent — defaults that *work*, not stubs), and it
  makes the Business Rule Task testable and example-able with zero external
  dependencies.
- **Replaceable wholesale**: an embedder wires a DMN adapter (or any rules
  service client) through the same one-method interface; the task and the
  model are untouched by the swap.

### 2.5 Script Task — the Script Engine will be pluggable too; conception deferred

The Script Task (the stub's sibling under the same epic) follows the same
*conceptual* shape — "invoke the associated script, complete on return", the
language deliberately open (`scriptFormat` is a MIME-type hint). One thing IS
decided here: **the Script Engine is a pluggable seam of the same
interface-plus-default shape as this ADR's rule engine** — script
interpreters are swappable engine services, never baked in. Everything else
about it — the interface's exact surface, its relationship to the
expression-layer engine, the batteries-included default (if any) — is
**deferred to its own conception** on the script/expression workstream; this
seam is its template.

### 2.6 Engine notes (deviations & choices)

| Choice | Standard position | Engine choice |
|---|---|---|
| Decision reference on the task | the base metamodel defines no decision binding (only `implementation`) | an opaque `decisionRef` string resolved by the configured engine — the vendor-extension slot made explicit (§2.2) |
| `implementation` value | a free string hint (`##WebService`, `##Unspecified`) | the configured engine's `##`-kind, reported at execution (§2.2) |
| Result shape | silent (the rule's output is unspecified) | one structured result item committed to process data under its name (§2.3) |
| Rule-engine binding | open ("typical wiring is to DMN") | the pluggable `rules.Engine` seam; DMN stays external per SAD-001 N2 (§2.1) |
| Default engine | — | the in-core Go decision registry, batteries included (§2.4) |

## 3. Standard grounding

| Claim | Source |
|---|---|
| BRT semantics: call on activation, complete on the rule's completion | [tasks.md §BusinessRuleTask](../bpmn-spec/semantics/tasks.md) (§13.3.3) |
| The binding is open; DMN is "typical", not mandated | [tasks.md §BusinessRuleTask — engine notes](../bpmn-spec/semantics/tasks.md) |
| `implementation` is the element's only own attribute; no decisionRef in the base metamodel | [activities.md §BusinessRuleTask](../bpmn-spec/elements/activities.md) (§10.2.5 model) |
| `##`-string convention for `implementation` | [tasks.md §ServiceTask — engine notes](../bpmn-spec/semantics/tasks.md); the engine's own `##GoOper` precedent |
| BRT in the executable conformance scope | [conformance.md](../bpmn-spec/conformance.md) Activities table |
| No DMN engine, external integration via BRT | [SAD-001 v.1](SAD-001-vision-and-architecture.md) non-goal N2 |
| Extension shape: interface + bundled default + injection; industry-tight interfaces; bounded defaults | [ADR-002 v.2](ADR-002-extension-architecture.md) §4.1/§4.2 |
| Task failure → the fault machinery | [tasks.md §Faults](../bpmn-spec/semantics/tasks.md); ADR-006 v.4 §2.6 lineage |

## 4. Alternatives considered

- **Per-task engine binding (the ServiceTask/`gooper` shape)** — the task
  constructor takes a decision *object* instead of a reference, no engine
  seam. Rejected: it models decisions as process-model content, contradicting
  the industry reality (decisions are engine-resident, independently
  versioned artifacts) and SAD-001 N2's external-integration framing; it also
  duplicates what a plain ServiceTask + `gooper` already does today.
- **No default — fail fast without an injected engine.** Rejected by the
  batteries-included decision: the library must be usable (and its examples
  runnable) without external services, per the `goexpr`/`localdispatcher`
  default philosophy. Fail-loud lives one level down instead: an
  *unregistered decision* is a classified error.
- **A DMN-shaped wide interface** (decision tables, hit policies, model
  deployment on the seam). Rejected — bakes one vendor model into a permanent
  engine surface; N2 keeps DMN external, and everything beyond
  evaluate-by-reference belongs behind the adapter.
- **Placing the seam under `pkg/model`** (the `expression` precedent).
  Rejected: expression evaluation interprets a BPMN-modeled artifact
  (`FormalExpression`); a business rule is deliberately *not* modeled by the
  standard — the seam is engine infrastructure, the `WorkerDispatcher`
  neighborhood.

## 5. Consequences

**Positive:** the last model-only conformance task gains execution on a seam
that is one interface + one small default; DMN (or any rules service) plugs
in without touching the model layer; the pattern doubles as the template for
the Script Task's future interpreter seam.

**Negative / cost:** a new public extension surface to keep stable (one
method — deliberately minimal); the in-core registry adds one more bundled
default to maintain (bounded by construction).

**Follow-ups this conception sets up:** a DMN adapter as an out-of-tree (or
`adapters/`) module consuming the seam; the Script Task on the
expression-layer workstream; `GlobalBusinessRuleTask` stays out of scope with
the other GlobalTask variants.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-22 | Ruslan Gabitov | Draft conception. The Business Rule Task executes the standard's minimal clause (§13.3.3 — call on activation, complete on return) against a **pluggable Business Rule Engine seam** in the ADR-002 shape: a one-method, industry-tight `rules.Engine` interface (evaluate a **decision reference** against the read-only data surface → one structured result item), wired through the five-point extension pattern (config field, default, injection option, runtime accessor, startup printout). **Batteries included**: the in-core default is a bounded Go **decision registry** (named Go decision functions — useful, testable, zero external deps); any DMN/rules service replaces it wholesale behind the same interface (SAD-001 N2 keeps DMN external). Decision addressing by opaque ref and the single-result shape are explicit engine choices (the base metamodel is silent). Result commit rides the ordinary frame path; failures ride the ordinary fault machinery. Script Engine decided pluggable as well (same interface-plus-default shape); its conception deferred to the script/expression workstream, with this seam as the template. Standard-grounded against the vendored extract (§13.3.3, §10.2.5 model, the `##`-hint convention). Implementation rides the accompanying SRD. |
