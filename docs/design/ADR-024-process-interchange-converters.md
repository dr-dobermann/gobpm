# ADR-024 — Process Interchange: pluggable import/export converters (BPMN 2.0 first)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.5 |
| Date | 2026-08-17 |
| Owner | Ruslan Gabitov |
| Refines | [SAD-001 v.1](SAD-001-vision-and-architecture.md) §4 N7 / §5 / §9 / §14, [ADR-002 v.2 Extension Architecture](ADR-002-extension-architecture.md), [ADR-019 v.1 Definition Versioning](ADR-019-definition-versioning.md), [ADR-003 Module Layout](ADR-003-module-layout.md) |

> **v.4 (Draft)** — the fence moves. v.1–v.3 fixed the seam and a deliberately
> small MVP element subset; v.4 decides that **import** covers the whole
> execution-conformance element set (§2.9), states the policy for the three
> pluggable languages a definition can carry — expressions (§2.10), scripts
> (§2.11), business rules (§2.12) — refuses the reuse-by-reference family
> outright (§2.13), admits a **recognized vendor dialect** so a real modeler
> file arrives whole (§2.14), and widens the seam with one optional capability
> so a document carrying several processes, or constructs the converter chose
> not to map, has somewhere to land (§2.15). Export deliberately stays on the
> v.1 subset for this slice, which narrows §2.8's round-trip guarantee — stated
> as a cost in §5. The accompanying SRD lands it.

> **Accepted (v.1–v.3)** — decides how a process definition crosses the boundary between
> an **external interchange format** and gobpm's **in-memory model**, in both
> directions, without coupling either to the engine. The decision is a
> **format-agnostic converter seam** — two small interfaces (`Importer`,
> `Exporter`) plus a register-by-format-key registry in core — and a first
> **batteries-included** implementation for **BPMN 2.0 XML** in the sibling
> package `pkg/convert/bpmn`, which adds no dependency to core because the
> parser is stdlib `encoding/xml`, so core still "accepts pre-built models"
> (SAD-001 §4 N7). Import
> yields a `*process.Process`; the host registers it with the thresher itself
> (ADR-019 versioning intact, because imported BPMN `id`s are preserved as the
> definition's identity). Export walks the same model back to XML. The seam is
> the extension point (ADR-002): a third party registers an `Importer`/`Exporter`
> for XPDL, a JSON DSL, or a vendor dialect and the façade dispatches by
> format. This ADR fixes the contract and the MVP element subset; the
> accompanying SRD-051 lands BPMN import+export of that subset end-to-end.

---

## 1. Context & problem

> **Reading note (v.4).** This section is the **v.1 baseline** — the state that
> motivated the ADR — kept verbatim because it is why the seam exists. It is no
> longer a description of today: the seam and a BPMN converter landed with v.2,
> so the engine now reads and writes BPMN over the §2.6 subset. The problem v.4
> addresses is the *successor* one, stated at the end of this section.

**As of v.1**, gobpm built a process definition exactly one way:
**programmatically**, through `process.New(name, …opts)` + `p.Add(node)` +
`flow.Link(src, trg)` (`pkg/model/process/process.go:47,175`,
`pkg/model/flow/sequenceflow.go:62`). There was **no serialization layer of any
kind** — a whole-repo search found no `encoding/xml`, no
`MarshalXML`/`UnmarshalXML`, and no `json:` tags on any model type. The engine
"accepts pre-built models" and nothing else.

That was a deliberate 0.1.0 shape, but it blocked adoption. The governing SAD
names the gap in three places:

- **[SAD-001 v.1 §4 N7](SAD-001-vision-and-architecture.md):** *"BPMN XML
  parser … The parser will exist (it has to, for adoption), but it is a
  **separate module** that constructs the in-memory model the engine consumes.
  Core library accepts pre-built models."*
- **[SAD-001 v.1 §5](SAD-001-vision-and-architecture.md):** the **BPMN modeler**
  stakeholder authors *"BPMN 2.0 XML to be executed by goBpm"* and needs
  *"strict spec conformance; clear feedback on unsupported elements."*
- **[SAD-001 v.1 §9](SAD-001-vision-and-architecture.md):** the module layout
  reserved `doc-source/ ← FUTURE — BPMN XML parser (own go.mod)` as a
  top-level module sibling to `runtime/` and `adapters/`. *(v.2: that
  reservation is retired — see §4-A and SAD-001 v.1.1.)*

Two forces beyond the SAD shape this decision:

1. **Bidirectional, not just a parser.** The requirement is import **and**
   export — read a `.bpmn` into the model, and write the model back out. N7
   speaks only of a "parser"; the seam must be symmetric.
2. **Pluggable across formats, BPMN first.** BPMN 2.0 XML is the
   batteries-included implementation, but the architecture must let a host or
   a third party plug a different interchange format (XPDL, a JSON process DSL,
   a vendor dialect) behind the same contract — matching the engine's whole
   extension philosophy ([ADR-002 v.2](ADR-002-extension-architecture.md):
   *"Users implement Go interfaces; pass impls to engine constructor"*).

The problem this ADR solves: **define the converter contract and its home in
the module graph, decide the BPMN 2.0 XML mapping and its conformance fence,
and keep every choice consistent with SAD-001 §4/§9 (core stays clean) and
ADR-019 (imported definitions must version correctly).**

**The v.4 problem.** The seam and its BPMN converter exist; the fence does not
hold. The engine executes the whole element set of
[conformance.md](../bpmn-spec/conformance.md), while the converter reads nine
elements of it — so the file a modeller produces is, in the ordinary case, a
file this engine refuses, and refuses at the first unmapped element rather than
partially. Three further gaps are structural rather than incremental: a
definition's expressions, scripts and rule references arrive in languages the
ADR never chose a policy for; a document carrying more than one process has no
representable result; and the vendor configuration that real files carry is
discarded without a word. v.4 decides all four.

## 2. Decision

### 2.1 A format-agnostic converter seam in core

The seam is two interfaces and a registry, in a new core package `pkg/convert`.
No XML, no format specifics, stdlib-only — so core keeps its "stdlib + uuid
only" dependency budget ([SAD-001 v.1 §9.1](SAD-001-vision-and-architecture.md)).

```go
// package convert  (github.com/dr-dobermann/gobpm/pkg/convert)

// Format identifies an interchange format in the registry.
type Format string

const BPMN Format = "bpmn"

// Importer builds an in-memory process definition from an external
// representation read off r.
type Importer interface {
	Import(ctx context.Context, r io.Reader) (*process.Process, error)
}

// Exporter serialises an in-memory process definition to w.
type Exporter interface {
	Export(ctx context.Context, w io.Writer, p *process.Process) error
}
```

The interfaces are **split, not unified**: a format may support only one
direction, they register independently, and the pair mirrors the
`io.Reader`/`io.Writer` asymmetry the engine already speaks.

### 2.2 A register-by-format-key registry

The registry is Go's own `image.RegisterFormat` / `image.Decode` codec pattern —
package-level maps keyed by `Format`, populated by each converter package's
`init()`. The in-project precedent for package-global, swappable registration is
`foundation.SetGenerator` / `GenerateID` — a package-global `IDGenerator` swapped
under a mutex (`pkg/model/foundation/idgen.go:36,52`).

This is a **deliberate deviation** from ADR-002 v.2's functional-options norm
(engine-level extensions *"registered once at `Thresher` construction via
functional options"*): a package-global registry is required because `convert`
is engine-independent (§2.4) and has no `thresher.New` to hang a `WithConverter`
option on. (`data.SourceProvider` is *not* the precedent — it is populated
per-`Scope`-instance in `New` and never mutated, `internal/scope/scope.go:112`,
so it models per-instance injection, not a global codec table.) A thin façade
tops the maps:

```go
func RegisterImporter(f Format, imp Importer) error
func RegisterExporter(f Format, exp Exporter) error

// RegisterImporterAtInit / RegisterExporterAtInit are the init()-time
// twins: an init() has nowhere to return an error to, so a bad registration
// is recorded and surfaced on first use (v.4 correction — v.1 named these
// MustRegisterImporter / MustRegisterExporter and no such function was
// built).

func Import(ctx context.Context, f Format, r io.Reader) (*process.Process, error)
func Export(ctx context.Context, f Format, w io.Writer, p *process.Process) error

func Formats() []Format // registered formats, for diagnostics / feedback
```

Registration validates every argument on the public boundary — empty `Format`
rejected, nil `Importer`/`Exporter` rejected, duplicate registration of the
same `(Format, direction)` rejected — with a self-identifying error naming the
function and the offending argument. `Import`/`Export` on an unregistered
format return a clear *"unknown format %q (registered: …)"* error, listing
`Formats()` so the caller sees what a blank import would have provided.

### 2.3 The BPMN converter is a separate, batteries-included package

The BPMN 2.0 XML converter ships as `pkg/convert/bpmn`, a sibling package of
the seam. It imports core (`pkg/convert` for the interfaces, `pkg/model/*` to
build/read the model), holds **all** `encoding/xml` code, and self-registers:

```go
// package bpmn  (github.com/dr-dobermann/gobpm/pkg/convert/bpmn)
func init() {
	convert.RegisterImporterAtInit(convert.BPMN, importer{})
	convert.RegisterExporterAtInit(convert.BPMN, exporter{})
}
```

**"Batteries-included" means first-party, in-repo, zero-config after a blank
import** — the `image`/`image/png` model: `_ ".../pkg/convert/bpmn"` registers
both directions; `convert.Import(ctx, convert.BPMN, r)` then works. Core users
who never import it get a clean *"unknown format"* error, never a hidden XML
dependency. The examples and (future) `runtime/` blank-import it so the
out-of-the-box experience is BPMN-ready.

> **v.2 re-decision.** v.1 placed this in the top-level module SAD-001 §9
> reserved as `doc-source/` (renamed `convert/` by Q1). Slice 1 shipped it as a
> package instead — the module boundary bought nothing measurable and cost the
> coverage gate. See §4-A and [SRD-051 v.2 §4.1](../srd/SRD-051-bpmn-converter.md).
> The invariant the module was standing in for — *core never imports the
> converter* — is preserved by direction, and the seam's format-agnosticism is
> now enforced mechanically by the `convert-seam-format-agnostic` depguard rule
> instead of by a `go.mod`.

> This honours the dependency direction ([SAD-001 v.1 §9.1](SAD-001-vision-and-architecture.md):
> the format package imports core, never the reverse) and N7's "core accepts
> pre-built models". Whether BPMN should instead be a *true* core default
> (no blank import) is deferred to Open Questions — it would require revising
> SAD-001 N7 and is a SAD decision, not an ADR one. (N7 scopes only the import
> *parser*; ADR-024 extends the seam to **export**, on which the SAD is silent —
> a settled open point, not a contradiction.)

### 2.4 No engine coupling; the host registers the result

Import returns a `*process.Process`. The host then calls
`thresher.RegisterProcess(p)` (`pkg/thresher/thresher.go:642`) itself — the
converter never touches the engine. This keeps `convert` usable with no engine
at all (tests, tooling, offline validation), consistent with ADR-002's
two-layer model. The engine is deliberately kept free of any converter
dependency; where a runnable service needs *"load a `.bpmn` and register it,"*
that composition belongs to the **`runtime/` server layer** (its own
workstream), which imports both core and the converter module — never the
engine importing the converter. A `thresher.ImportAndRegister(format, r)`
convenience is explicitly **out of scope** (Decisions §Q4).

### 2.5 Imported identity feeds versioning

The BPMN `id` attribute of `<bpmn:process>` and of every flow element is
**preserved** as the model's foundation identity via
`foundation.WithID(id)` (`pkg/model/foundation/options.go:36`). This is
load-bearing: [ADR-019 v.1](ADR-019-definition-versioning.md) makes the
**version key the process `id`** (*"Two registrations carrying the same id are
two versions of one logical definition"*, ADR-019 §2). An importer that minted
fresh auto-ids would make every import a singleton v1 and silently defeat
versioning. Export writes `foundation.ID()` back to the `id` attribute and
`Name()` to `name`, so a registered definition round-trips its identity.
Missing/blank BPMN `id`s are an import error (BPMN requires `id` on flow
elements), not a silent auto-id.

### 2.6 MVP element subset and the conformance fence

> **Superseded for import by §2.9 (v.4).** This subset was the v.1–v.3 fence and
> remains the fence **export** observes. Incoming pins to "§2.6" from v.2/v.3-era
> documents keep their meaning: they name the MVP subset, which is exactly what
> this section still describes.

The MVP maps the **executable core** of the engine's own scope
([docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md)) — the elements
BPMN 2.0.2 Clause 13 animates, which is the same fence the engine's conformance
target draws:

| BPMN XML | Model target | Spec § |
|---|---|---|
| `<bpmn:definitions>` (root) | document envelope | §10 |
| `<bpmn:process>` | `process.New(name, WithID(id))` | §10 |
| `<bpmn:startEvent>` (none) | `events.NewStartEvent` | §13.5.1 |
| `<bpmn:endEvent>` (none) | `events.NewEndEvent` | §13.5.6 |
| `<bpmn:task>` / `<bpmn:manualTask>` | `activities.NewManualTask` (parse-but-no-op, §13.1) | §13.3.3 |
| `<bpmn:userTask>` | `activities.NewUserTask` | §13.3.3 |
| `<bpmn:serviceTask>` (`operationRef`) | `activities.NewServiceTask` | §13.3.2 |
| `<bpmn:interface>` / `<bpmn:operation>` | `service.NewOperation` (catalog stub) | §10.4 |
| `<bpmn:sequenceFlow>` (`sourceRef`/`targetRef`, `conditionExpression`) | `flow.Link(src, trg, WithCondition)` | §13.2 / §13.3.1 |
| `<bpmn:exclusiveGateway>` (`default`) | `gateways.NewExclusiveGateway` | §13.4.2 |
| `<bpmn:parallelGateway>` | `gateways.NewParallelGateway` | §13.4.1 |

**Explicitly out of MVP** (import must **tolerate and skip**, export must
**omit**): all Diagram Interchange — `bpmndi:*`, `dc:*`, `di:*`,
`BPMNShape`/`BPMNEdge` — which is *"not part of execution conformance"*
([conformance.md](../bpmn-spec/conformance.md), [SAD-001 v.1 §4 N5](SAD-001-vision-and-architecture.md)).
Also out of MVP and reported as **unsupported-element feedback** (SAD-001 §5),
not silently dropped: inclusive/complex/event-based gateways,
timer/message/signal/error events, boundary events, sub-processes, call
activities, lanes, collaboration/choreography. Each lands in a later slice
(§7).

Two in-namespace elements are **skipped silently** rather than reported:
`<bpmn:documentation>` and `<bpmn:extensionElements>`. Both are universal in
bpmn.io / Camunda output and carry no execution semantics, so erroring on them
would reject files whose flow graph is fully inside the subset. The
unsupported-element contract therefore covers unmapped **flow elements**
([SRD-051 v.2 §FR-7](../srd/SRD-051-bpmn-converter.md)).

> **v.2 re-decision — `serviceTask` is in the MVP.** v.1 excluded it, and
> SRD-051 v.1 §4.6 deferred it to slice 2, because export needed a
> `ServiceTask.Operation()` accessor that did not exist. Slice 1 added that
> accessor (one read-only method, the ADR's only core API addition) and landed
> the element with `operationRef` resolution against a definitions-level
> `<interface>`/`<operation>` catalog. Message bindings
> (`inMessageRef`/`outMessageRef`) remain deferred — parsed and recorded, not
> yet bound to `bpmncommon.Message` nor re-emitted.

### 2.7 Namespaces and unsupported-element feedback

The importer binds the `bpmn:` prefix to
`http://www.omg.org/spec/BPMN/20100524/MODEL`
(`docs/bpmn-spec/scripts/bpmn-moddle.json:2-4`) and matches on local name +
namespace, not on a hard-coded prefix string (a file may bind the URI to any
prefix). An in-scope-namespace element the MVP does not yet map produces an
`UnsupportedElementError{Tag, ID, Section}` — the modeler's *"clear feedback"*
requirement (SAD-001 §5). Foreign namespaces outside execution scope (DI/DC,
vendor extension namespaces) are skipped silently.

### 2.8 Round-trip is semantic, not byte-lossless

MVP guarantees a **semantic** round-trip over the in-scope subset:
`Import` then `Export` yields a process that is structurally and semantically
equivalent (same nodes, ids, flows, conditions, gateway kinds) — **not**
byte-identical XML. Formatting, attribute order, dropped DI, and namespace
prefix normalisation legitimately differ. Byte-lossless or DI-preserving
round-trip is a non-goal for MVP (§14.1-style deliberate deviation) and noted
for future work. This is an **engine choice**, stated as such: the standard does
not require a lossless textual round-trip for Process Execution Conformance.

### 2.9 Import covers the whole execution-conformance element set

**Import maps every element the engine executes** — the "In scope" list of
[conformance.md](../bpmn-spec/conformance.md), which enumerates what Clause 13
animates plus the supporting classes it consumes. There is no longer an import
subset inside that list: a file the engine could run is a file the converter can
read.

The reason the fence has to move rather than creep is that a partial importer is
worth less than the sum of its elements. A definition is a *graph*: an importer
that reads eight of a file's nine element kinds imports nothing at all, because
the ninth aborts the document. Element-at-a-time slicing therefore delivers no
usable increment until the last slice — which is the opposite of how the rest of
this engine was built, and the reason §7's "slice 2+" list never started.

Everything outside that list keeps an **explicit disposition**, because "not
mapped" and "not present" must not look alike:

| Family | Disposition on import | Why |
|---|---|---|
| Lane / LaneSet | **parse and preserve**, attach no behaviour | model-only per [conformance.md](../bpmn-spec/conformance.md); §2.3.1 lets execution ignore lanes, §2.3.2 obliges an importer to keep them |
| Visual artifacts — `textAnnotation`, `group`, `category` | **skip silently** | "pure visual" in the out-of-scope table; they carry no semantics, and erroring on them rejects ordinary modeler files for a comment |
| `association` | **map** | kept in scope because it carries compensation semantics, not because it is drawn |
| Diagram interchange (`bpmndi:` / `dc:` / `di:`) | skip silently (unchanged) | not part of execution conformance |
| `relationship` | skip silently | not execution-related |
| `import` | **map** — collected by namespace and bound to the `<itemDefinition>` whose `structureRef` prefix resolves to it; one nothing refers to is reported | the skip row's own code comment scheduled the revisit "when itemDefinition lands with the data stage" — SRD-089.F FR-7 was that revisit |
| Choreography / Conversation families | **refuse** | separate conformance sub-classes — a Choreography is not a Process, and silently dropping one would import a different diagram than the modeller drew |
| Collaboration family | **definitional only** — see §2.15 | §2.3.2 names the "definitional Collaboration" as part of the import obligation |

The distinction that decides each row is *representation vs. semantics*: an
element the engine will not act on may still be skipped only when dropping it
leaves the imported definition **meaning the same thing**. A text annotation
passes that test; a Choreography does not.

### 2.10 Expressions: one supported language, one translated dialect

A definition carries expressions in conditions, timers, multi-instance
cardinality, completion conditions, correlation retrieval and data assignments.
The standard makes the language a **per-expression attribute**
(`FormalExpression.language`, 0..1) over a **document-level default**
(`Definitions.expressionLanguage`, 0..1, defaulting to
`http://www.w3.org/1999/XPath`), and says nothing about which languages a tool
must implement. So the choice is entirely the engine's, and it is:

| Language on the wire | Import behaviour |
|---|---|
| the engine's own text language | **passthrough** — carried as the text expression kind, evaluated by the routed engine |
| **JUEL** (`${…}` / `#{…}`, the Camunda idiom) | **translated** into the engine's text language, source to source |
| **FEEL** | **refused**, classified, naming the language |
| XPath (including by way of the schema default) | **refused**, classified |
| anything else | **refused**, classified |

Three parts of that need their reasoning on the record.

**Why translate JUEL rather than run it.** The engine already routes expressions
by language claim, and its own text language is a C-like infix grammar over the
same data model JUEL addresses — comparison, boolean composition, member and
index access, a small builtin set. The overlap is large enough that translation
is a rewrite of *syntax*, not an interpreter: delimiter stripping, `&&`/`||`/`!`
to their word forms, and the variable-access idioms. Adding a second evaluator
for a language whose semantics we would then own — coercions, `null` handling,
bean property resolution — buys nothing the rewrite does not, and costs a
permanent second implementation of every expression semantic the engine has.

**Why translation must fail loudly, never partially.** A JUEL construct with no
counterpart — a method invocation on a host bean, anything reaching outside the
process's own data — is **refused by name**. A translator that silently drops
what it cannot express produces a condition that parses, evaluates, and routes
the token the wrong way; the failure surfaces as a mis-executed process, far from
the import. Refusal at import is the only place the error is still cheap.

**What the rewrite looks like.** The engine's text language keeps boolean
composition as words (`and` / `or` / `not`) and shares JUEL's comparison,
member and index syntax, so the translation is mechanical over a small set of
forms — and the last row is the whole point of the refuse-by-name rule:

| JUEL on the wire | Translated | Note |
|---|---|---|
| `${total > 100}` | `total > 100` | delimiter stripped |
| `${total > 100 && tier == "gold"}` | `total > 100 and tier == "gold"` | `&&` → `and` |
| `${!approved \|\| blocked}` | `not approved or blocked` | `!` → `not`, `\|\|` → `or` |
| `${order.customer.tier == 'vip'}` | `order.customer.tier == "vip"` | member access unchanged |
| `${items[0] == "sku-1"}` | `items[0] == "sku-1"` | index access unchanged |
| `${execution.getVariable("total") > 0}` | `total > 0` | the variable-access idiom collapses to the name |
| `${myBean.check(order)}` | — | **refused by name**: a host-bean invocation has no counterpart, and guessing one would route tokens on a fiction |

**Why the schema default is not honoured.** `expressionLanguage` defaults to
XPath, and XPath is refused — so a literal reading rejects every document that
omits the attribute, which is nearly all of them, including files whose
expressions are plainly `${…}`. The default is a *schema* default asserting what
an unmarked expression **is**, and in practice it describes almost no real file.
Language is therefore resolved by: the expression's own `language`, else the
document's `expressionLanguage`, else the **syntactic tell** (`${…}`/`#{…}` is
JUEL), else refusal. This is an **engine choice** and a deliberate divergence,
recorded as such in §3.

### 2.11 Scripts: Lua only, and no silent fallback

`ScriptTask` carries `scriptFormat` (a MIME hint, 0..1) and its source; the
standard mandates no script language. The engine routes scripts by that format
through its script-engine seam, and ships **two** batteries — Lua, and a
dependency-free one whose script text names a **Go function the host
registered**.

Only the first is importable, and the reason is not arithmetic. A Lua body is
**self-contained source**: the file carries the behaviour and the engine
interprets it. A `gofunc` body carries a *name*, and the behaviour lives in
host code the document cannot see — reference-by-registry, which is precisely
the shape §2.13 refuses for the `GlobalTask` family. A converter that accepted
it would admit a process whose script does nothing until an unrelated Go
program happens to register the matching key, and would fail at execution far
from the import that let it in.

*(This replaces a v.4 claim that the engine "ships exactly one battery — Lua".
That was true when v.4 was drafted and false within a day, when the
dependency-free engine landed. The decision is unchanged; its reason is now one
that survives a second battery — and a third.)*

Import therefore accepts a script task whose `scriptFormat` names Lua and
**refuses every other format by name**, including an *absent* format: with
nothing to route on, the alternative to refusing is guessing, and a guessed
language runs someone else's syntax through Lua. The refusal is a
**deferral, not a rejection of the file's validity** — the same script task
imports the moment the host registers an engine claiming that format, which is
what the seam exists for.

### 2.12 Business rules: an opaque decision reference, never a DMN parser

`BusinessRuleTask` in the standard carries **only** `implementation` — there is
no `decisionRef` attribute, and the spec mandates no rule-engine binding, noting
only that the typical wiring is DMN. gobpm's permanent non-goal is that it will
never embed a DMN engine ([SAD-001](SAD-001-vision-and-architecture.md) N2); its
business-rule task holds an **opaque reference** that the host's configured rule
engine resolves, and the shipped battery is a decision-table engine covering the
DMN-shaped subset the engine implements.

Import follows that exactly: it lifts the decision reference — which, having no
home in the standard's attribute set, arrives through the vendor dialect (§2.14)
or `implementation` — into the opaque reference, and **parses no DMN**. A
business rule task whose decision reference cannot be found is refused rather
than imported inert, because a rule task with no decision is a task that will
fail at its first execution with far less context than the importer has now.

What "DMN is supported" means here is therefore precise and worth stating so it
is not over-read: the **reference** round-trips and the **engine** decides. A
decision the shipped table engine cannot express is not a converter failure and
not a converter concern.

### 2.13 Global tasks are refused, and say "yet"

The `GlobalTask` family (`globalTask`, `globalUserTask`, `globalManualTask`,
`globalScriptTask`, `globalBusinessRuleTask`) is reuse **by reference**: a task
defined once at `definitions` level and invoked through `CallActivity`. Resolving
a reference needs a registry of callable definitions, and that registry is a
product concern of the server tier, not the library
([conformance.md](../bpmn-spec/conformance.md) — the family is listed exactly
that way).

Import **refuses** every member of the family, and refuses a `callActivity` whose
`calledElement` resolves to one, with an error that says **not supported yet** —
distinct from the unsupported-element error, because the two mean different
things to whoever reads them. An unsupported element is a shape this converter
does not map; a global task is a shape that is *waiting on a subsystem*, and the
day the registry lands the same file imports unchanged.

### 2.14 A recognized vendor dialect, mapped where the model has a home

The converter has, until now, skipped every foreign namespace in silence, which
is correct for layout and wrong for configuration: a Camunda file's assignee, its
external-task topic and its decision reference are not decoration, they are the
executable content, and the engine has a model home for each. Worse, two of this
ADR's own decisions are unreachable without the dialect — a decision reference
(§2.12) and, in practice, JUEL (§2.10) have no standard-BPMN carrier at all.

So the converter **recognizes one dialect** (Camunda 7) and maps every construct
that has a home in the engine's model. Three rules bound it:

1. **Map only what already exists.** The dialect never motivates a new model
   type. A construct with no home is not mapped, and the model is not bent to
   receive it.
2. **Never silently drop a recognized construct.** Anything in a recognized
   namespace that is not mapped is **reported** (§2.15) — the vendor-namespace
   half of the SAD-001 §5 feedback requirement, which the flow-element error has
   always served and extension content never did.
3. **An unrecognized namespace stays silent.** Skipping an unknown vendor's
   subtree without comment remains right; the converter cannot report on a
   vocabulary it does not know, and pretending otherwise would make every foreign
   annotation noise.

Constructs whose meaning is the *host engine's* rather than the process's — job
executor and transaction-boundary hints, listener classes, history controls — are
by rule 1 not mapped and by rule 2 reported. They describe how a different engine
schedules work, and gobpm's answer to them is that it schedules differently, not
that it will pretend to obey.

### 2.15 One optional seam capability: a document yields a set, and a report

Two facts do not fit `Import(ctx, r) (*process.Process, error)`.

**A document carries more than one process.** §2.3.2 obliges an importer to
support Process diagrams *"including its definitional Collaboration"*, and a
collaboration binds participants to several processes. gobpm has no Collaboration
type and needs none — the standard's own inter-process mechanism is message
events, which the engine executes, and each participant process is registered and
versioned on its own. But a single return value cannot carry them.

**Constructs are dropped on purpose.** §2.14 requires reporting what a recognized
dialect carried and the model does not hold. There is nowhere in the signature to
say so.

Both are answered by **one optional capability** beside the existing interfaces —
the Go idiom of a capability interface the façade probes for and falls back
without, as `io.ReaderFrom` is to `io.Reader`:

```go
// package convert

// Result is everything one source document yielded.
type Result struct {
	// Processes are the definitions the document carried, in document order.
	Processes []*process.Process
	// Dropped names each recognized construct the converter did not map.
	Dropped []Dropped
}

// DocumentImporter is the optional capability of an Importer whose format
// can carry more than one definition per document, or can knowingly drop
// content. The convert façade uses it when the registered Importer
// implements it and falls back to Import otherwise.
type DocumentImporter interface {
	ImportDocument(ctx context.Context, r io.Reader) (*Result, error)
}
```

`Import` keeps its meaning and its signature — **the** process of the document —
and becomes precise about the ambiguous cases: a document with exactly one
executable process returns it; a document with none, or with several, is an error
naming what it found and pointing at the document-level call. Nothing existing
breaks, no format is forced to implement it, and the collaboration obligation is
met without modelling Collaboration.

**Collaboration is consumed, not represented.** `<collaboration>` is read for its
definitional content only — which participants exist and which process each
refers to — and `<messageFlow>` is reported as dropped: it is the *drawing* of a
message exchange whose execution the engine performs through message events and
correlation. The graph the engine runs is unchanged by its presence.

## 3. Standard grounding

All claims cite the vendored KB ([docs/bpmn-spec/](../bpmn-spec/index.md)), which carries
the OMG §-refs.

- **Conformance target.** Process Execution Conformance is **§2.3**, and it has
  two requirements addressed to "the tool": §2.3.1 execution semantics, and
  **§2.3.2 import of Process diagrams** — *"The tool claiming Process Execution
  Conformance type MUST support import of BPMN Process diagram types including
  its definitional Collaboration."* That second requirement is **this ADR's
  reason to exist**: it is the converter, not the engine, that satisfies it, and
  it lands with the `gobpm-server` product rather than the library
  ([conformance.md](../bpmn-spec/conformance.md),
  [SAD-001](SAD-001-vision-and-architecture.md) §14). The MVP element set is the
  executable core of the elements Clause 13 animates.
- **`definitions`/`process` containment.** `Process` is a `rootElements` child
  of `definitions`; flow elements are `flowElements` children of `process`
  ([elements/foundation.md:21-24](../bpmn-spec/elements/foundation.md),
  [elements/process.md:22-29](../bpmn-spec/elements/process.md)). `isExecutable`
  is a 0..1 attribute — the "executable" requirement is a conformance
  statement, not a schema cardinality.
- **None start/end.** A start/end event with zero `eventDefinitions` is the
  plain/none variant; none-start *"starts a new Process instance"* (§13.5.1),
  none-end *"just consumes the token"* (§13.5.6)
  ([semantics/events.md:7-9](../bpmn-spec/semantics/events.md),
  [semantics/end-events.md:24](../bpmn-spec/semantics/end-events.md)).
- **Tasks.** Abstract `task`/`manualTask` are non-operational — an engine *"MAY
  treat it as a no-op pass-through"* (§13.1,
  [semantics/tasks.md:51,72](../bpmn-spec/semantics/tasks.md)). `serviceTask`
  resolves `operationRef`; `implementation` is a string hint
  ([semantics/tasks.md:14](../bpmn-spec/semantics/tasks.md)).
- **Sequence flow.** `sourceRef`/`targetRef` are ID references (attributes);
  `conditionExpression` is a **child element**, an `Expression`
  ([elements/flows.md:17-20](../bpmn-spec/elements/flows.md)). `isImmediate`
  is non-operational and MAY be ignored
  ([semantics/token-flow.md:19](../bpmn-spec/semantics/token-flow.md)).
- **Gateways.** Exclusive: *"first condition that evaluates to true … else the
  default sequence flow (referenced by the `default` attribute) … if all false
  AND no default → engine throws"* (§13.4.2,
  [semantics/gateways.md:28-48](../bpmn-spec/semantics/gateways.md)). Parallel:
  one token from each incoming, one on each outgoing; *"cannot throw"* (§13.4.1),
  and has **no** `default` attribute.
- **DI/DC out of scope.** *"BPMNShape, BPMNEdge … all `bpmndi:*` and `dc:*`,
  `di:*` | Visual layout metamodel; not part of execution conformance"*
  ([conformance.md:155](../bpmn-spec/conformance.md)).

Added in v.4:

- **Element scope of the import.** The obligation is §2.3.2 — *"MUST support
  import of BPMN Process diagram types including its definitional
  Collaboration"* — and the element set it applies to is
  [conformance.md](../bpmn-spec/conformance.md)'s in-scope list (§13's
  operational elements plus the supporting classes they consume). §2.9 adopts
  that list verbatim rather than a subset of it.
- **Lanes are representation, not semantics.** §2.3.1 permits ignoring
  non-operational elements *at run time*; it does not permit dropping them from
  the model, and §2.3.2 obliges an importer to support the modeller's diagram
  ([conformance.md](../bpmn-spec/conformance.md), boundary cases).
- **Visual artifacts carry nothing.** `TextAnnotation`, `Group` and `Category`
  are listed out of scope as *"pure visual"*, while `Association` is explicitly
  kept *"because it carries compensation semantics"*
  ([conformance.md](../bpmn-spec/conformance.md)) — which is precisely the line
  §2.9 draws between skipping and mapping.
- **Expression language is per-expression over a document default.**
  `FormalExpression.language` is 0..1
  ([elements/foundation.md:254](../bpmn-spec/elements/foundation.md)) and
  `Definitions.expressionLanguage` is 0..1 defaulting to
  `http://www.w3.org/1999/XPath`
  ([elements/foundation.md:19](../bpmn-spec/elements/foundation.md)). The
  standard nowhere requires a tool to implement any particular one.
- **Script language is unmandated.** `ScriptTask.scriptFormat` is 0..1
  ([elements/activities.md:211](../bpmn-spec/elements/activities.md)) and *"the
  spec does not mandate a script language"*
  ([semantics/tasks.md:65](../bpmn-spec/semantics/tasks.md)).
- **Business rules carry no decision reference.** `BusinessRuleTask`'s only own
  property is `implementation`
  ([elements/activities.md:253](../bpmn-spec/elements/activities.md)), and *"the
  spec does not mandate a rule engine binding. Typical wiring is to DMN."*
  ([semantics/tasks.md:58](../bpmn-spec/semantics/tasks.md)). A `decisionRef`
  is therefore vendor vocabulary by construction, not by our omission — which is
  why §2.12 depends on §2.14.
- **Global tasks are reuse by reference.** The family derives from
  `CallableElement` and is invoked through `CallActivity.calledElement` (a
  `String` reference, 0..1 —
  [elements/activities.md:518](../bpmn-spec/elements/activities.md)); resolving
  it needs a definition registry, which
  [conformance.md](../bpmn-spec/conformance.md) places in the server tier.
- **Collaboration is out of the library's execution scope but inside the
  importer's obligation.** *"Not animated by Clause 13; inter-process messaging
  is covered by Message events. Note §2.3.2 names the 'definitional
  Collaboration' for import — that is a server/converter concern"*
  ([conformance.md](../bpmn-spec/conformance.md)). §2.15 takes both halves of
  that sentence at face value.

**Engine notes (deliberate divergences):** semantic-only round-trip (§2.8);
BPMN `id` treated as durable versioning identity (§2.5 — a gobpm/ADR-019 choice,
the standard is silent on registry versioning); unsupported in-scope-namespace
elements are a hard import error rather than a lenient skip (§2.7 — stricter
than the standard requires, to serve the SAD-001 §5 feedback need). Added in
v.4: the **`expressionLanguage` schema default is not honoured** (§2.10 — the
standard says an unmarked expression is XPath; the converter resolves by
syntactic tell instead, because honouring the default would reject nearly every
real file); a **refused script format and a refused expression language are
engine choices**, since the standard mandates neither (§2.10, §2.11); and
**recognizing a vendor dialect** (§2.14) is outside the standard entirely — the
standard supplies the `extensionElements` mechanism and assigns its content no
meaning.

## 4. Alternatives considered

| # | Decision point | Options | Chosen — why |
|---|---|---|---|
| A | Home of the BPMN parser | (a) a core package; (b) separate module | **(a)** *(v.2 — reversed)*. v.1 chose (b) on two grounds: N7's "core accepts pre-built models", and core's stdlib+uuid budget. The budget ground does not survive decision D below: a hand-rolled `encoding/xml` parser is stdlib, so it adds nothing to the budget. N7 is about **responsibility**, and that is preserved by import direction, not by a `go.mod` — the converter imports the model, never the reverse. Against that, (b) costs a module, a `replace`, a release tag, and — decisively — invisibility to the diff-coverage gate, which only sees the root module. Landed as `pkg/convert/bpmn`; see [SRD-051 v.2 §4.1](../srd/SRD-051-bpmn-converter.md). |
| B | Seam wiring | (a) `thresher.WithConverter` single injected impl; (b) standalone register-by-key registry | **(b)** — the requirement is *multiple* pluggable formats; a single injected option (the `WithLogger` idiom) models one impl, not a keyed set. (b) matches `data.SourceProvider` and `image.RegisterFormat`, and keeps convert engine-independent. |
| C | Interface shape | (a) unified `Converter{Import;Export}`; (b) split `Importer`/`Exporter` | **(b)** — a format may support one direction only; independent registration; mirrors `io.Reader`/`io.Writer`. A unified interface would force half-implementations to stub the other half. |
| D | Parser implementation | (a) wrap a third-party Go BPMN lib; (b) hand-rolled `encoding/xml` | **(b)** — the MVP subset is small and stdlib `encoding/xml` covers it with zero deps; existing libs are DI/diagram-heavy and pull weight the module does not need. Revisitable per-format (the seam does not care). |
| E | Imported ids | (a) auto-generate; (b) preserve BPMN `id` | **(b)** — ADR-019 keys versions on the process `id`; auto-ids would make every import a singleton v1 (§2.5). |
| F | Round-trip fidelity | (a) byte-lossless / DI-preserving; (b) semantic-only | **(b)** for MVP — DI is out of execution scope; textual losslessness is not a conformance requirement and would balloon MVP scope (§2.8). |
| G | "Batteries-included" delivery | (a) blank-import (image-style); (b) core-default (no import) | **(a)** for MVP — (b) contradicts SAD-001 N7 (BPMN in core) and would need a SAD revision. Surfaced in Open Questions. |

Added in v.4:

| # | Decision point | Options | Chosen — why |
|---|---|---|---|
| H | Import fence | (a) keep slicing element families per SRD; (b) the whole execution-conformance set at once | **(b)** — a definition is a graph, so a partial importer imports *nothing* from any file containing the one kind it lacks. Slicing delivers no usable increment until the final slice, which is why §7's "slice 2+" list stood untouched. The cost is one large landing; the alternative is several landings that each ship zero working imports (§2.9). |
| I | JUEL support | (a) a JUEL engine registered under its own language claim; (b) source-to-source translation into the engine's text language; (c) unsupported | **(b)** — the grammars overlap almost completely, so (a) buys a second permanent implementation of every expression semantic (coercion, null, member access) to gain nothing (b) lacks. (c) was rejected because JUEL is what Camunda files actually contain — refusing it refuses the corpus. Untranslatable constructs are refused by name, never partially rewritten (§2.10). |
| J | Unmarked expression language | (a) honour the XPath schema default; (b) resolve by syntactic tell | **(b)** — (a) is the literal standard reading and rejects nearly every real document, since modelers omit `expressionLanguage` and write `${…}`. Recorded as a deliberate divergence rather than taken quietly (§2.10, §3). |
| K | Unroutable script format | (a) refuse; (b) default to the one shipped engine | **(a)** — (b) runs another language's syntax through Lua and reports the resulting parse error from inside the engine at execution time, blaming the script rather than the missing format. Refusal names the real cause at import (§2.11). |
| L | DMN | (a) parse DMN XML in the converter; (b) carry an opaque decision reference | **(b)** — (a) contradicts SAD-001 N2 (never embed a DMN engine) and would put a second standard's parser inside a BPMN converter. The rule-engine seam already resolves references; the converter's job ends at carrying one (§2.12). |
| M | Global tasks | (a) synthesize an inline task per global definition; (b) refuse with a "not supported yet" error | **(b)** — (a) silently converts reuse-by-reference into duplication, so a file with one global task called from three places imports as three unrelated tasks and edits to the original stop propagating. The refusal is honest and reverses cleanly when the registry lands (§2.13). |
| N | Vendor extensions | (a) keep skipping silently; (b) recognize Camunda 7 and map what has a home; (c) generic passthrough into a model carrier | **(b)** — (a) is the status quo and loses the executable content of every migrated file, and it cannot deliver §2.12's decision reference at all. (c) needs a new model type to hold arbitrary foreign XML and re-raises "what does it mean at execution?", which is the question (b) answers by only mapping what the model already understands (§2.14). |
| O | Multi-process documents | (a) refuse collaboration; (b) import the first process and drop the rest; (c) an optional document-level capability | **(c)** — (a) contradicts §2.3.2's explicit words; (b) drops definitions **silently**, the exact failure this ADR's feedback requirement exists to prevent. (c) is additive, leaves `Import` and every existing caller untouched, and needs no Collaboration model type (§2.15). |
| P | Reporting dropped constructs | (a) a second return value on `Import`; (b) a logger on the seam; (c) fold into the document-level `Result` | **(c)** — (a) breaks every existing caller; (b) makes the report a side effect that a library consumer cannot inspect, only observe. One capability covering both multi-process and diagnostics keeps the seam at two interfaces plus one optional third (§2.15). |

## 5. Consequences

**Positive**
- Core stays dependency-clean; the XML surface is quarantined in one package,
  with the seam's format-agnosticism enforced by depguard (SAD-001 §4/§9 upheld).
- The seam is a genuine extension point: XPDL, a JSON DSL, or a vendor dialect
  is a third-party `Importer`/`Exporter` registration — no core change.
- `convert` works with no engine — offline validation, tooling, tests.
- Imported definitions version correctly (§2.5), so import composes with the
  Call Activity / registry line (ADR-019, ADR-023).
- Unsupported-element errors give the modeler the SAD-001 §5 feedback loop.

**Negative / costs**
- BPMN is not a *literal* core default — a host must add a blank import to get
  it (the image-model trade-off).
- No DI round-trip — a file's diagram layout is lost on import→export. Acceptable
  for an execution engine; called out for anyone expecting a modeler-grade
  round-trip.
- The converter rides the root module's existing CI, adding no module to the
  per-module matrix — but its ~1 600 lines now count toward the `cover-check`
  diff gate, so every later element slice must clear `COVER_MIN` too.

Added in v.4:

- **Import and export are no longer the same fence, and §2.8's round-trip
  guarantee narrows to the export subset.** This is the honest cost of §2.9:
  after this slice a file can import cleanly and then fail to export, which is a
  worse asymmetry than the symmetric-but-tiny subset it replaces. It is accepted
  deliberately — import is what §2.3.2 obliges and what adoption needs — and it
  is temporary by plan (§7), not by hope. Until export catches up, "round-trip"
  means *the MVP subset round-trips*, and every document outside it is one-way.
- **The converter now owns a translator.** JUEL→text-language rewriting (§2.10)
  is a parser and a code generator living inside an import path, with its own
  correctness surface and its own failure mode (a wrong rewrite routes tokens
  wrongly, silently). The refuse-by-name rule bounds it, but the cost is real and
  belongs on this list rather than in a footnote.
- **Recognizing a dialect is a standing commitment.** §2.14 admits Camunda 7;
  Camunda 7 will keep changing, and every construct it adds becomes a mapping
  question the converter must answer or report. The bound is rule 1 — the
  dialect never motivates a model type — which keeps the commitment to mapping
  work, not to design work.
- **Refusals are now a documented interface.** "Not supported yet" (§2.13), a
  refused expression language, a refused script format and a refused decision
  reference are outcomes a host will branch on, so their identity is API surface
  that cannot be reshuffled casually.

## 6. Enterprise-readiness recommendations

- **Conformance fixtures.** Wire the OMG **MIWG** import test set (SAD-001 §6
  names MIWG fixtures as the P0 conformance tactic) as the converter's
  acceptance corpus, growing per slice.
- **Streaming.** `Import(io.Reader)` / `Export(io.Writer)` are already
  stream-shaped; keep the BPMN impl streaming (`xml.Decoder` token stream) so
  large definitions do not force whole-file buffering.
- **XSD / schema validation.** Optional strict mode validating against the OMG
  XSD before mapping — future, behind an option; MVP does structural validation
  via `process.Validate()` post-build.
- **Extension-element passthrough.** Preserve unknown in-`bpmn`-scope extension
  elements (`extensionElements`) for lossless custom-namespace round-trip —
  future, needs a model carrier.
- **Dialect targeting on export.** MVP emits plain OMG BPMN; a future option can
  target vendor dialects (Camunda/Zeebe namespaces).

## 7. Rollout plan

- **Slice 1 — SRD-051 (this ADR's landing). Done.** `pkg/convert` seam +
  registry; `pkg/convert/bpmn` with BPMN **import + export** of the §2.6 MVP
  subset (including `serviceTask`); unsupported-element feedback; semantic
  round-trip test corpus; the `examples/bpmn-convert/` example; README/guide +
  changelog.
- **Slice 2+ (own SRDs).** ~~Gateways (inclusive/complex/event-based); events
  (timer/message/signal/error) + boundary events; sub-process & call activity
  (composes with ADR-023); lanes (parse-and-preserve); extension-element
  passthrough~~ — **superseded by v.4**: element-at-a-time slicing delivers no
  working import until the last slice (§2.9, alternative H), so these land
  together as the full-import slice below. XSD strict mode and additional
  formats behind the same seam remain future slices.
- **Post-Slice-1:** `/check-srd`, SRD-051 → Accepted, ADR-024 → Accepted.

Added in v.4:

- **Slice 2 — full import (this ADR's v.4 landing).** The whole
  execution-conformance element set (§2.9), the expression / script /
  business-rule language policies (§2.10–§2.12), the global-task refusal
  (§2.13), the Camunda 7 dialect (§2.14) and the `DocumentImporter` capability
  (§2.15) — plus the converter defects the coverage audit surfaced in the code
  this slice rewrites. Import only.

  The slice lands as **five linked SRDs**, and that needs reconciling with §2.9,
  which rejects slicing. The two are not in conflict, but only because they
  govern different things. §2.9 / row H governs the **released fence**: the
  converter must not *settle* at a subset, which is exactly what the abandoned
  "slice 2+" list did. A landing sequence is a different question, and the rule
  it must satisfy is that each stage completes a coherent **tier** — the parser
  spine, then the languages every later tier consumes, then the flow graph, then
  typed events and composites, then data and the document contract — rather than
  one element of a tier, which is the shape that imports nothing from a file
  containing its neighbour. The fence therefore widens in **announced** stages
  and is not final until the last of the five.
- **Slice 3 — export catches up.** Export the same element set, closing the
  asymmetry §5 accepts and restoring §2.8's round-trip guarantee to the full
  fence. This is the immediate successor, not an open-ended future: the cost
  recorded in §5 is only bounded if it is paid.
- **Slice 4+.** Diagram-interchange preservation (a round-trip a modeller would
  recognize); XSD strict mode; a second format behind the same seam.

## 8. References

**Design (up / sideways, versioned):**
- [SAD-001 v.1](SAD-001-vision-and-architecture.md) §4 N5/N7, §5, §9/§9.1/§9.2, §14 — parser-as-separate-module, modeler feedback, module layout, conformance scope.
- [ADR-002 v.2](ADR-002-extension-architecture.md) — interfaces + compile-time wiring; the extension idiom the seam follows.
- [ADR-019 v.1](ADR-019-definition-versioning.md) — version key = process id; the identity-preservation constraint (§2.5).
- [ADR-003](ADR-003-module-layout.md) — module boundaries and import-direction rules; the converter stays inside the root module (§4-A).

Added in v.4 (sideways — the seams whose vocabulary §2.10–§2.14 map onto):
- [ADR-032 v.1](ADR-032-language-routed-expression-engines.md) — expressions are routed by language claim; the text kind carries a language URI and a body. §2.10's translation target and its refusals are stated in that vocabulary.
- [ADR-031 v.1](ADR-031-script-task-and-script-engine-seam.md) — scripts are routed by `scriptFormat` through a registry of engines. §2.11's Lua-only rule is a statement about which engine ships, not about the seam.
- [ADR-027 v.1](ADR-027-business-rule-task-and-rule-engine-seam.md) — the rule-engine seam and the opaque decision reference §2.12 carries.
- [ADR-020 v.3](ADR-020-human-interaction-execution-model.md) — the human-interaction model whose assignee / candidate-user / candidate-group vocabulary §2.14 maps the dialect onto.
- [ADR-023 v.3](ADR-023-sub-process-and-call-activity.md) — sub-process and call-activity semantics; §2.13's `calledElement` refusal is a statement about what a call may resolve to.

> **Note — Draft parents.** SAD-001 v.1 and ADR-003 are themselves **Draft**;
> their prescriptions may shift before Accepted, so these pins track a moving
> baseline until both are ratified.

**Standard (BPMN 2.0 KB):**
- [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) — §2.3 Process Execution Conformance (§2.3.1 semantics / §2.3.2 import); the in-scope element list; DI/DC out of scope.
- [docs/bpmn-spec/elements/](../bpmn-spec/index.md) — structural metamodel (foundation, process, events, activities, flows, gateways).
- [docs/bpmn-spec/semantics/](../bpmn-spec/index.md) — token-flow, tasks, gateways, events, end-events.

**Code (grounding the model targets):**
- `pkg/model/process/process.go:47,175` — `process.New`, `Add`.
- `pkg/model/flow/sequenceflow.go:62` — `flow.Link`.
- `pkg/model/foundation/options.go:36` — `foundation.WithID`.
- `pkg/model/data/source.go:3` + `internal/scope/scope.go:111` — the register-by-key precedent.
- `pkg/thresher/thresher.go:642` — `RegisterProcess` (the host-side consumer of an import).

## Decisions (resolved open questions)

- **Q1 — Module name: WITHDRAWN in v.2.** v.1 proposed renaming the reserved
  `doc-source/` module → `convert/`, pending SAD-001 §9 ratification. There is
  no module to name: §4-A was reversed and the converter is the package
  `pkg/convert/bpmn`. The SAD-001 §9 `doc-source/` reservation is **retired**
  rather than renamed — a SAD-001 §9 update (up-ref) applied at landing.
- **Q2 — Batteries-included: DECIDED.** Ship BPMN as **blank-import**
  (`_ ".../pkg/convert/bpmn"`, image-style) — it keeps core dependency-
  clean (SAD-001 §9.1) without amending N7. A *true* core default is a clean
  additive follow-up (revise N7 + a thin core default) if ever wanted; deferring
  costs nothing now.
- **Q3 — Format detection: DECIDED.** Keep an **explicit `Format` argument**; no
  content sniffing for MVP (one format — nothing to disambiguate, and sniffing
  can mis-fire on vendor dialects / BOM). A `convert.Detect` / `ImportAny`
  sniffer is an additive follow-up once ≥2 formats exist.
- **Q4 — Engine coupling: DECIDED.** **No** `thresher.ImportAndRegister`. The
  engine stays decoupled from every non-runtime module; the *"import → register"*
  composition is a **`runtime/` server concern** (§2.4), where the converter has
  its first real consumer. The server is its **own workstream (own ADR/SRD)**,
  not part of this landing — and it lives in the `runtime/` module
  (`runtime/cmd/gobpm-server/`), never in core's `cmd/`, since core's dependency
  budget is stdlib + uuid only (SAD-001 §9.1).

**v.4 adds no open questions.** Every decision point it opened is resolved in
§2.9–§2.15 with its rejected alternatives recorded in §4 (rows H–P). The two
items it leaves for later — export catching up to the import fence, and
diagram-interchange preservation — are **scheduled slices** in §7 with a stated
trigger, not questions awaiting an answer.

## Document History

| Version | Date | Change |
|---|---|---|
| v.5 | 2026-08-17 | **The `import` disposition row catches up with SRD-089.F FR-7.** §2.9's table said `<import>` is skipped silently — true when v.4 was written, and scheduled for revisit by the skip's own code comment ("when itemDefinition lands with the data stage, its typeRef makes the declaration meaningful"). The data stage landed: an `<import>` is collected by namespace and bound to the `<itemDefinition>` whose `structureRef` prefix resolves to it, and one nothing refers to is reported rather than dropped. The row is split (`relationship` keeps its skip) and re-stated as **map**. A table-row correction only; no decision changes. |
| v.1 | 2026-07-17 | Initial draft — converter seam (`pkg/convert`), BPMN as the batteries-included separate-module converter, MVP element subset, semantic round-trip. |
| v.2 | 2026-07-30 | Accepted on the SRD-051 slice-1 landing. §4-A reversed: the converter is the package `pkg/convert/bpmn`, not a top-level module — the stdlib parser costs core no dependency, and a module would have stayed invisible to the diff-coverage gate (§2.3). Q1 (module rename) withdrawn; the SAD-001 §9 `doc-source/` reservation retired. §2.6: `serviceTask` restored to the MVP set with the new `ServiceTask.Operation()` accessor, and the `documentation`/`extensionElements` silent-skip carve-out recorded. |
| v.3 | 2026-08-02 | Accepted. **Correction only — no contract change.** This ADR defined the MVP fence by reference to *"the Common Executable Subclass (§2.1.3)"*, in §2.6, §3 and §8. That basis does not exist and never applied. There is no §2.1.3 — §2.1 is *General*, §2.2 is Process **Modeling** Conformance, Process **Execution** Conformance is **§2.3** — and the Common Executable Subclass is a **Modeling** sub-class (§2.2.1) for tools that *emit* executable models, mandating XML Schema, WSDL and XPath, none of which gobpm uses. The fence is re-based on the elements **Clause 13 animates**, which is what the engine's scope list enumerates; the MVP element set itself is **unchanged**, so §FR-8's mapping, §2.8's semantic-round-trip guarantee and every incoming pin to v.2 stand. The correction also **sharpens this ADR's standing**: §2.3.2 obliges a tool claiming Process Execution Conformance to "support import of BPMN Process diagram types", so the converter is not an interchange nicety beside conformance — it *is* the second of the two requirements, and it belongs to the `gobpm-server` product rather than the library ([SAD-001](SAD-001-vision-and-architecture.md) §14). One consequence worth carrying into future slices: `Lane`/`LaneSet` must round-trip, so the model has to hold them even though execution ignores them (conformance-status.md row 14). |
| v.4 | 2026-08-10 | **Draft — the import fence moves.** v.1–v.3 fixed the seam and a deliberately small MVP element subset, and §7's "slice 2+" element list never started, because element-at-a-time slicing delivers no working import until the last slice: a definition is a graph, so an importer missing one element kind imports *nothing* from any file containing it (§2.9, alternative H). v.4 therefore takes the whole execution-conformance element set for **import**, with an explicit disposition for every family outside it (lanes preserved, visual artifacts skipped, choreography refused). It settles the three pluggable languages a definition can carry: expressions — the engine's own text language passes through, **JUEL is translated into it source-to-source** and untranslatable constructs are refused by name, FEEL and XPath are refused, and the XPath *schema default* is deliberately not honoured (§2.10, rows I/J); scripts — **Lua only**, an absent or foreign `scriptFormat` refused rather than guessed (§2.11, row K); business rules — an **opaque decision reference**, never a DMN parser, per the SAD-001 N2 non-goal (§2.12, row L). The `GlobalTask` family is refused with a distinct **"not supported yet"** error, since reuse-by-reference needs the server tier's definition registry and inlining it would silently turn one definition into many (§2.13, row M). A **recognized vendor dialect** (Camunda 7) is admitted under three rules — map only what the model already holds, never silently drop a recognized construct, stay silent on unrecognized namespaces — because two of this ADR's own decisions have no standard-BPMN carrier at all (§2.14, row N). Finally the seam gains **one optional capability**, `DocumentImporter`, returning the document's process set plus a report of dropped constructs: it satisfies §2.3.2's "including its definitional Collaboration" without a Collaboration model type, and gives §2.14's reporting rule somewhere to land, while `Import` and every existing caller stay untouched (§2.15, rows O/P). **Export deliberately stays on the v.1 subset**, which narrows §2.8's round-trip guarantee to that subset — recorded as an accepted cost in §5 and scheduled for closure as slice 3 in §7. §2.6 is superseded for import and retained as the export fence, so incoming v.2/v.3 pins to it keep their meaning. |
