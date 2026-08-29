# ADR-024 — Process Interchange: pluggable import/export converters (BPMN 2.0 first)

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.7 |
| Date | 2026-08-28 |
| Owner | Ruslan Gabitov |
| Refines | [SAD-001 v.1.3](SAD-001-vision-and-architecture.md) §4 N7 / §5 / §9 / §14, [ADR-002 v.2](ADR-002-extension-architecture.md), [ADR-019 v.1](ADR-019-definition-versioning.md), [ADR-003 v.2](ADR-003-module-layout.md) |

This ADR decides how a process definition crosses the boundary between an
**external interchange format** and gobpm's **in-memory model**, in both
directions, without coupling either to the engine: a format-agnostic
**converter seam** in core, a register-by-format-key registry behind it, and a
batteries-included **BPMN 2.0 XML** converter beside it. It fixes what import
must cover — the whole element set the engine executes — the policy for the
three pluggable languages a definition can carry, and what happens to every
construct outside that set.

---

## 1. Context & problem

gobpm builds a process definition programmatically: a constructor per element,
`Add` to place it, `Link` to connect it. That is the whole authoring surface,
and it blocks adoption — the people who model processes use modelling tools,
and what those tools produce is a `.bpmn` file.

The governing SAD names the gap in three places:

- **[SAD-001 v.1.3](SAD-001-vision-and-architecture.md) §4 N7:** *"BPMN XML
  parser … The parser will exist (it has to, for adoption), but it is a
  **separate concern** that constructs the in-memory model the engine consumes.
  Core library accepts pre-built models."*
- **§5:** the **BPMN modeler** stakeholder authors *"BPMN 2.0 XML to be executed
  by goBpm"* and needs *"strict spec conformance; clear feedback on unsupported
  elements."*
- **§14:** Process Execution Conformance has two requirements, and the second —
  §2.3.2, import of Process diagrams — is the converter's, not the engine's.

Two forces beyond the SAD shape the decision. The requirement is **bidirectional**
— read a file into the model, and write the model back out — so the seam must be
symmetric where N7 speaks only of a parser. And it must be **pluggable across
formats**: BPMN 2.0 XML is what ships, but a host or a third party must be able
to put XPDL, a JSON process DSL or a vendor dialect behind the same contract,
which is the engine's extension philosophy throughout
([ADR-002 v.2](ADR-002-extension-architecture.md)).

The harder half of the problem is the **fence**. A converter that reads part of
what the engine runs is worth much less than the fraction suggests, because a
definition is a *graph*: an importer that understands eight of a file's nine
element kinds imports nothing at all, since the ninth aborts the document. So the
question is not which elements to map first but where import must end up, and the
answer has to be stated as a decision rather than approached one element at a
time.

## 2. Decision

### 2.1 A format-agnostic converter seam in core

The seam is two interfaces and a registry, in the core package `pkg/convert`.
No XML, no format specifics, stdlib-only — so core keeps its "stdlib + uuid
only" dependency budget ([SAD-001 v.1.3](SAD-001-vision-and-architecture.md) §9.1).

```go
// package convert

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
direction, and the two register independently.

### 2.2 A register-by-format-key registry

The registry is Go's own `image.RegisterFormat` / `image.Decode` codec pattern —
package-level maps keyed by `Format`, populated by each converter package's
`init()`, with a thin façade over them: register and unregister per direction,
`Import`/`Export` dispatching by key, and `Formats()` for diagnostics. Because an
`init()` has nowhere to return an error to, registration has an init-time twin
that records a bad registration and surfaces it on first use rather than
panicking at load.

This is a **deliberate deviation** from ADR-002's functional-options norm, under
which an extension is passed to the engine constructor. A package-global registry
is required precisely because `convert` is engine-independent (§2.4): there is no
`thresher.New` to hang a `WithConverter` option on, and a codec table is keyed
state shared by every caller rather than per-engine configuration.

Registration validates every argument on the public boundary — an empty
`Format`, a nil implementation and a duplicate registration of the same
(format, direction) are all refused, with a self-identifying error naming the
function and the offending argument. `Import`/`Export` on an unregistered format
name what *is* registered, so a caller who forgot the blank import sees the cause
rather than a bare failure.

**The format is an explicit argument; the seam sniffs no content.** Detection by
inspection can mis-fire on a vendor dialect or a byte-order mark, and the caller
always knows what it is holding. A `Detect`/`ImportAny` sniffer is additive and
can arrive if a second format ever makes it useful.

### 2.3 The BPMN converter is a separate, batteries-included package

The BPMN 2.0 XML converter is `pkg/convert/bpmn`, a sibling package of the seam.
It imports core — the seam for the interfaces, `pkg/model/*` to build and read
the model — holds **all** `encoding/xml` code, and self-registers both directions
in its `init()`.

**"Batteries-included" means first-party, in-repo, zero-config after a blank
import**, on the `image`/`image/png` model: a blank import registers the format,
and `convert.Import(ctx, convert.BPMN, r)` then works. A core user who never
imports it gets a clean unknown-format error, never a hidden XML dependency. The
examples and the server layer blank-import it, so the out-of-the-box experience
is BPMN-ready.

Two invariants hold this in place. **Core never imports the converter** — the
dependency runs one way, from the format package to the model, which is what N7's
"core accepts pre-built models" means in practice; it is enforced mechanically by
a depguard rule rather than by a module boundary. And **the seam stays
format-agnostic**: nothing in `pkg/convert` knows what XML is.

Making BPMN a *true* core default — no blank import — is not decided here. It
would put a format in core and therefore needs a SAD-001 N7 revision, which is a
SAD decision; the blank import costs a host one line and keeps core clean
meanwhile.

### 2.4 No engine coupling; the host registers the result

Import returns a `*process.Process` and stops there. The host registers it with
the engine itself, so `convert` is usable with no engine at all — offline
validation, tooling, tests — and the engine carries no converter dependency in
either direction.

Where a runnable service needs *"load a `.bpmn` and register it"*, that
composition belongs to the **server layer**, which imports both core and the
converter. A `thresher.ImportAndRegister(format, r)` convenience is therefore
**out of scope**: it would reverse the dependency for a two-line saving, and the
place that genuinely wants it is a product concern with its own workstream.

### 2.5 Imported identity feeds versioning

The BPMN `id` of `<bpmn:process>` and of every flow element is **preserved** as
the model's foundation identity. This is load-bearing:
[ADR-019 v.1](ADR-019-definition-versioning.md) makes the **version key the
process id** — *"Two registrations carrying the same id are two versions of one
logical definition"* — so an importer that minted fresh auto-ids would make every
import a singleton first version and silently defeat versioning. Export writes the
identity back, so a registered definition round-trips it. A missing or blank `id`
is an import error, never a silent auto-id: the standard requires `id` on flow
elements, and inventing one discards the modeller's own naming.

### 2.6 The export element subset

Export covers the executable core: `definitions` and `process`; none start and
end events; the abstract task, manual task, user task and service task with its
operation catalog; sequence flows with conditions; and the exclusive and parallel
gateways.

| BPMN XML | Model target | Grounding |
|---|---|---|
| `<bpmn:definitions>` (root) | document envelope | §10 |
| `<bpmn:process>` | a process carrying the imported id | §10 |
| `<bpmn:startEvent>` (none) | none start event | §13.5.1 |
| `<bpmn:endEvent>` (none) | none end event | §13.5.6 |
| `<bpmn:task>` / `<bpmn:manualTask>` | manual task (non-operational, §13.1) | `semantics/tasks.md` |
| `<bpmn:userTask>` | user task | `semantics/tasks.md` |
| `<bpmn:serviceTask>` (`operationRef`) | service task | `semantics/tasks.md` |
| `<bpmn:interface>` / `<bpmn:operation>` | operation catalog | `elements/service-interfaces.md` |
| `<bpmn:sequenceFlow>` (`sourceRef`/`targetRef`, `conditionExpression`) | a link, conditional where declared | §13.2 |
| `<bpmn:exclusiveGateway>` (`default`) | exclusive gateway | §13.4.2 |
| `<bpmn:parallelGateway>` | parallel gateway | §13.4.1 |

Diagram interchange — `bpmndi:*`, `dc:*`, `di:*` — is **omitted** on export and
tolerated on import: it is *"not part of execution conformance"*
([conformance.md](../bpmn-spec/conformance.md)), and the engine holds nothing to
write back.

Export lagging import is a real asymmetry, and §5 records its cost; §7 records
that closing it is the immediate successor work rather than an open-ended
future.

### 2.7 Namespaces and unsupported-element feedback

The importer matches on **namespace URI plus local name**, never on a prefix
string: a file may bind the BPMN model namespace to any prefix it likes, and a
converter keyed on `bpmn:` reads only the files its author happened to see. An
element in the BPMN namespace that the converter does not map is refused with
the three facts the modeller needs — the **tag**, the element's **id**, and the
**spec section** that defines it — which is the SAD-001 §5 feedback requirement
in concrete form. Foreign namespaces outside execution scope are the subject of
§2.14.

### 2.8 Round-trip is semantic, not byte-lossless

A round-trip over the elements both directions cover yields a process that is
structurally and semantically equivalent — same nodes, ids, flows, conditions and
gateway kinds — and **not** byte-identical XML. Formatting, attribute order,
dropped diagram interchange and namespace-prefix normalisation legitimately
differ.

This is an **engine choice**: the standard requires no lossless textual
round-trip for Process Execution Conformance, and a modeller-grade round-trip is a
different product than an execution engine.

### 2.9 Import covers the whole execution-conformance element set

**Import maps every element the engine executes** — the "In scope" list of
[conformance.md](../bpmn-spec/conformance.md), which enumerates what Clause 13
animates plus the supporting classes it consumes. There is no import subset
inside that list: a file the engine could run is a file the converter can read.

Everything outside that list carries an **explicit disposition**, because "not
mapped" and "not present" must not look alike:

| Family | Disposition on import | Why |
|---|---|---|
| Lane / LaneSet | **parse and preserve**, attach no behaviour | model-only per [conformance.md](../bpmn-spec/conformance.md); §2.3.1 lets execution ignore lanes, §2.3.2 obliges an importer to keep them |
| Artifacts — `textAnnotation`, `group`, the plain `association` | **parse and preserve** into the model-only artifact tier, attach no behaviour | [ADR-039 v.1](ADR-039-standard-artifacts.md): the same two-obligation reading as lanes — §2.3.2 loading and §2.8's round-trip need the model to hold what a diagram states, while execution ignores it. A reference the model cannot resolve degrades that one artifact to the §2.14 report — the file survives |
| `category` / `categoryValue` | **consume** as load-time resolution input — the value a group embeds; no model element is created | [ADR-039 v.1](ADR-039-standard-artifacts.md) §2.3 |
| `association` (compensation shape) | **map** — resolved into the boundary event's handler wiring, not duplicated as an artifact | execution semantics (§8.4.1); one document fact, one model representation ([ADR-039 v.1](ADR-039-standard-artifacts.md) §2.4) |
| Diagram interchange (`bpmndi:` / `dc:` / `di:`) | skip silently | not part of execution conformance |
| `relationship` | skip silently | not execution-related |
| `import` | **map** — collected by namespace and bound to the `<itemDefinition>` whose `structureRef` prefix resolves to it; one nothing refers to is reported | an import declares where a referenced type lives, which is meaningful exactly once item definitions carry a type reference |
| The `GlobalTask` family | **map** — each becomes a process in the document's set (§2.13) | reuse by reference, served by the registry as a process ([ADR-023 v.5](ADR-023-sub-process-and-call-activity.md) §2.7) |
| Choreography / Conversation families | **refuse** | separate conformance sub-classes — a Choreography is not a Process, and silently dropping one would import a different diagram than the modeller drew |
| Collaboration family | **definitional only** — see §2.15 | §2.3.2 names the "definitional Collaboration" as part of the import obligation |

The rule deciding each row is *representation vs. semantics*: an element the
engine will not act on may be skipped only when dropping it leaves the imported
definition **meaning the same thing**. A Choreography fails that test outright. A
text annotation passes it and is carried anyway, because the §2.3.2 loading
obligation cuts across the test the same way it does for lanes — the model holds
what the diagram states.

### 2.10 Expressions: one supported language, one translated dialect

A definition carries expressions in conditions, timers, multi-instance
cardinality, completion conditions, correlation retrieval and data assignments.
The standard makes the language a **per-expression attribute** over a
**document-level default**, and says nothing about which languages a tool must
implement. So the choice is entirely the engine's:

| Language on the wire | Import behaviour |
|---|---|
| the engine's own text language | **passthrough** — carried as the text expression kind, evaluated by the routed engine |
| **JUEL** (`${…}` / `#{…}`, the Camunda idiom) | **translated** into the engine's text language, source to source |
| **FEEL** | **refused**, classified, naming the language |
| XPath (including by way of the schema default) | **refused**, classified |
| anything else | **refused**, classified |

**Why translate JUEL rather than run it.** The engine already routes expressions
by language claim, and its own text language is a C-like infix grammar over the
same data model JUEL addresses — comparison, boolean composition, member and
index access, a small builtin set. The overlap is large enough that translation is
a rewrite of *syntax*, not an interpreter: delimiter stripping, `&&`/`||`/`!` to
their word forms, and the variable-access idioms.

| JUEL on the wire | Translated | Note |
|---|---|---|
| `${total > 100 && tier == "gold"}` | `total > 100 and tier == "gold"` | delimiters stripped, `&&` → `and` |
| `${execution.getVariable("total") > 0}` | `total > 0` | the variable-access idiom collapses to the name |
| `${myBean.check(order)}` | — | **refused by name**: a host-bean invocation has no counterpart |

Adding a second evaluator for a language whose semantics the engine would then
own — coercions, `null` handling, bean property resolution — buys nothing the
rewrite does not, and costs a permanent second implementation of every expression
semantic the engine has.

**Translation fails loudly, never partially.** A JUEL construct with no
counterpart — anything reaching outside the process's own data — is refused by
name. A translator that silently drops what it cannot express produces a
condition that parses, evaluates, and routes the token the wrong way; the failure
then surfaces as a mis-executed process, far from the import. Refusal at import is
the only place the error is still cheap.

**The syntactic tell is decisive, and a declared language does not override
it.** `expressionLanguage` has a *schema* default of XPath, and XPath is refused
— so a literal reading rejects every document that omits the attribute, which is
nearly all of them, including files whose expressions are plainly `${…}`. Worse,
a Camunda file often does not omit it: the tool writes the schema default out
explicitly, so the document *declares* XPath and then writes JUEL in every
condition. Honouring the declaration there refuses the file on its own
mislabelling, which serves nobody — the delimiters are what the author actually
wrote, and the declaration is what a tool emitted on their behalf.

Language is therefore resolved by the body first:

| The expression body | Language |
|---|---|
| carries `${…}` / `#{…}` | **JUEL**, whatever the document or the expression declares |
| anything else | the expression's own `language`, else the document's `expressionLanguage`, else **refused** |

A declaration only decides a body the delimiters do not. This is an engine
choice, recorded in §3, and it is the same judgement as not honouring the schema
default — applied to the case where the default was written out rather than left
implicit.

### 2.11 Scripts: self-contained source only

`ScriptTask` carries a `scriptFormat` hint and its source; the standard mandates
no script language. The engine routes scripts by that format through its
script-engine seam.

**A script imports only when its body is self-contained source** — the file
carries the behaviour and an engine interprets it. A body that instead carries the
*name* of something the host registered does not import, and the distinction is
not reference-by-name as such: §2.13's `calledElement` is a name the registry
resolves too. It is *what* is named. A called process is a definition the standard
lets a document declare or import; a host-registered Go function is a symbol no
BPMN document can carry, so a file naming one is not a description of the
behaviour at all. Accepting it would admit a process whose script does nothing
until an unrelated program happens to register the matching key, and fail at
execution far from the import that let it in.

An **absent** `scriptFormat` is refused like an unroutable one: with nothing to
route on, the alternative to refusing is guessing, and a guessed language runs
someone else's syntax through the wrong interpreter. The refusal is a **deferral,
not a verdict on the file**: the same script task imports the moment the host
registers an engine claiming that format, which is what the seam exists for.

### 2.12 Business rules: an opaque decision reference, never a DMN parser

`BusinessRuleTask` in the standard carries **only** `implementation` — there is
no `decisionRef` attribute, and the spec mandates no rule-engine binding, noting
only that the typical wiring is DMN. gobpm's permanent non-goal is that it will
never embed a DMN engine ([SAD-001 v.1.3](SAD-001-vision-and-architecture.md) N2);
its business-rule task holds an **opaque reference** that the host's configured
rule engine resolves.

Import follows that exactly: it lifts the decision reference — which, having no
home in the standard's attribute set, arrives through the vendor dialect (§2.14)
or `implementation` — into the opaque reference, and **parses no DMN**. A business
rule task whose decision reference cannot be found is refused rather than imported
inert, because a rule task with no decision will fail at its first execution with
far less context than the importer has now.

What "DMN is supported" means here is therefore precise: the **reference**
round-trips and the **engine** decides. A decision the shipped table engine cannot
express is not a converter concern.

### 2.13 Global tasks import as callable processes; a qualified `calledElement` carries its namespace

The `GlobalTask` family (`globalTask`, `globalUserTask`, `globalManualTask`,
`globalScriptTask`, `globalBusinessRuleTask`) is reuse **by reference**: a task
defined once at `definitions` level and invoked through `CallActivity`. The
engine serves that reference through the same registry that serves a called
process — [ADR-023 v.5](ADR-023-sub-process-and-call-activity.md) §2.7 decides
that a global task **is** a callable process whose body is the one task — so the
converter's obligation is to produce that process, not to resolve anything.

**Each global task imports as one more process in the document's set** (§2.15):
its id and name are the global task's, its declared contract is the global task's
`ioSpecification` (the process-level carrier of
[ADR-040 v.2](ADR-040-process-io-contract.md)), and its body is a None Start
Event, the task, and a None End Event joined by two sequence flows. The task is
built by **the same reading as its in-process counterpart**, so the family adds no
second mapping of any construct and inherits unchanged whatever that counterpart
refuses. The callable's `ioSpecification` becomes the **process's** declared contract, and
is not copied onto the task inside it: a task's parameters are filled by data
associations, a callable declares none, and what the contract reads at completion
is the root scope — which is where the task's own work lands.

**A `<callActivity>`'s `<ioSpecification>` imports** as the parameters that carry
data across the call boundary — the direct mapping, with no data associations.
§10.4.1's containment list names only Tasks and CallableElements and so reads as
excluding a Call Activity, but §10.4's own CallActivity row maps its
DataInputs/DataOutputs onto the callable's, which presupposes it has them. Under
the strict reading that row is unreachable and no imported document could hand
data to a callable at all, so "Tasks" is read as the activities that do work. The elements synthesized inside the process take ids
derived from the global task's id, and a document that already uses one of them is
refused for the duplicate rather than silently rewired.

A `callActivity` naming a global task of the same document needs nothing more:
its `calledElement` is the process key the registry serves.

**A `calledElement` is read as a QName — an engine choice.** The standard types
it a plain `String` and says nothing about a prefix
([ADR-023 v.5](ADR-023-sub-process-and-call-activity.md) §2.7 carries the
grounding), so reading one is the converter's decision — taken because a prefix is
what modellers write when the callable lives in another document, and because the
alternative, treating `ns:P` as a key containing a colon, can only call the wrong
thing or nothing.

| `calledElement` | Disposition |
|---|---|
| unprefixed | the key, verbatim |
| prefix bound to the document's own `targetNamespace` | the local part is the key; the qualification was self-reference |
| prefix bound to a namespace an `<import>` declares | the local part is the key, and the namespace rides with it as the call activity's **called namespace**; the engine's resolver maps the pair at call time |
| prefix bound to no declared namespace, or to one no `<import>` declares | **refused**, naming the prefix — the file is malformed, or references a document it never imported |

The converter resolves the *prefix*, never the *callable*: which registration a
foreign namespace maps onto is the host's decision through the engine's seam, and
the converter carries the namespace so the host can make it. Import consults no
registry, and a call activity imports before its callable is registered.

### 2.14 A recognized vendor dialect, mapped where the model has a home

Skipping every foreign namespace in silence is correct for layout and wrong for
configuration: a Camunda file's assignee, its external-task topic and its decision
reference are not decoration, they are the executable content, and the engine has
a model home for each. Two of this ADR's own decisions are unreachable without a
dialect — a decision reference (§2.12) and, in practice, JUEL (§2.10) have no
standard-BPMN carrier at all.

So the converter **recognizes one dialect** (Camunda 7) and maps every construct
that has a home in the engine's model. Three rules bound it:

1. **Map only what already exists.** The dialect never motivates a new model
   type. A construct with no home is not mapped, and the model is not bent to
   receive it.
2. **Never silently drop a recognized construct.** Anything in a recognized
   namespace that is not mapped is **reported** (§2.15) — the vendor-namespace
   half of the SAD-001 §5 feedback requirement, which the flow-element error has
   always served and extension content never did. Constructs whose meaning is the
   *host engine's* rather than the process's — job-executor and
   transaction-boundary hints, listener classes, history controls — are by rule 1
   not mapped and by this rule reported: they describe how a different engine
   schedules work, and gobpm's answer is that it schedules differently, not that
   it will pretend to obey.
3. **An unrecognized namespace stays silent.** The converter cannot report on a
   vocabulary it does not know, and pretending otherwise would make every foreign
   annotation noise.

### 2.15 One optional seam capability: a document yields a set, and a report

Two facts do not fit `Import(ctx, r) (*process.Process, error)`.

**A document carries more than one process.** §2.3.2 obliges an importer to
support Process diagrams *"including its definitional Collaboration"*, and a
collaboration binds participants to several processes. gobpm has no Collaboration
type and needs none — the standard's own inter-process mechanism is message
events, which the engine executes, and each participant process is registered and
versioned on its own. But a single return value cannot carry them.

**Constructs are dropped on purpose.** §2.14 requires reporting what a recognized
dialect carried and the model does not hold, and the signature has nowhere to say
so.

Both are answered by **one optional capability** beside the existing interfaces —
the Go idiom of a capability interface the façade probes for and falls back
without, as `io.ReaderFrom` is to `io.Reader`:

```go
// package convert

type Result struct {
	Processes []*process.Process // in document order
	Dropped   []Dropped          // each recognized construct not mapped
}

type DocumentImporter interface {
	ImportDocument(ctx context.Context, r io.Reader) (*Result, error)
}
```

`Import` keeps its meaning and its signature — **the** process of the document —
and becomes precise about the ambiguous cases: a document with exactly one
executable process returns it; a document with none, or with several, is an error
naming what it found and pointing at the document-level call. Nothing existing
breaks, no format is forced to implement the capability, and the collaboration
obligation is met without modelling Collaboration.

**Collaboration is consumed, not represented.** `<collaboration>` is read for its
definitional content only — which participants exist and which process each refers
to — and `<messageFlow>` is reported as dropped: it is the *drawing* of a message
exchange whose execution the engine performs through message events and
correlation. The graph the engine runs is unchanged by its presence.

### 2.16 Outside coverage: three classes, and a converter that never compensates

Every construct the importer does not map is exactly one of three, and the unit
classified is the **construct, not the tag**: `<association>` holds a mapped
compensation link and a preserved plain one, and a `<transaction>` imports while
one of its attribute values does not.

| Class | What it is | What the refusal says |
|---|---|---|
| **Staged** | mapped work not yet reached | nothing — it is not a boundary, and the plan that schedules it is its only record |
| **Capability-blocked** | executable, expressible in a document, blocked by a capability `pkg/model` lacks | names the **capability** and the issue tracking it, because that name is the specification of the work that removes it, and names the programmatic route available meanwhile |
| **Standing** | the engine will not accept it, and not for want of work | names the reason and what to do instead, and never says "yet" |

A standing boundary is either a constructor taking a Go value no document can
carry — a complex gateway's per-flow token counts against an `activationCondition`
expression, an ad-hoc container's host-supplied `Router`
([ADR-035 v.1](ADR-035-adhoc-sub-process.md) §2.2) — or a **decided non-goal**,
such as a second input/output set per direction. It is not a defect and is never
re-filed as one.

The register of capability-blocked rows lives with the import epic, not in this
document: a decision record that accumulates a ledger stops being a decision
record, and the ledger then has to be bumped on every landing while the rule it
surrounds never moves.

Two rules bound the classes:

1. **The converter never compensates for a missing model capability.** It reports
   and refuses; it grows no private parser, router, type or second copy of a model
   rule. Two implementations of one rule diverge, and then the converter's wins at
   import while the model's wins at run time — the worst possible split, which the
   model layer would later have to supersede while keeping the converter's
   behaviour intact. The rule cuts the other way first: before declaring a
   capability missing, look for the model's **own way in** and for the **decision
   that already governs it**. A timer reaches the engine whole through the model's
   ISO 8601 constructors; a transaction's `method` is read and carried by the
   model itself, with registration — not the converter — deciding whether the
   engine has a coordinator for it
   ([ADR-028 v.2](ADR-028-transaction-sub-process.md) §2.7). A capability that
   cannot be named precisely is usually one that has not been looked for.
2. **A capability lands before the row that consumes it.** An extension point is a
   model change with a model change's obligations — its own decision record where
   it alters a contract, its own landing document — and the converter row
   consuming it is a one-line follow-up, never the vehicle for the capability.
   [ADR-039 v.1](ADR-039-standard-artifacts.md) is the pattern: the artifact tier
   landed first, the `<association>` rows followed.

A reader who cannot tell "not yet" from "not ever" either waits for something that
will not arrive or rebuilds something that is already correct, which is why the
three classes differ in wording and not only in outcome.

## 3. Standard grounding

All claims cite the vendored KB ([docs/bpmn-spec/](../bpmn-spec/index.md)), which
carries the OMG §-refs.

- **Conformance target.** Process Execution Conformance is **§2.3**, with two
  requirements addressed to "the tool": §2.3.1 execution semantics, and **§2.3.2
  import of Process diagrams** — *"The tool claiming Process Execution Conformance
  type MUST support import of BPMN Process diagram types including its definitional
  Collaboration."* That second requirement is **this ADR's reason to exist**
  ([conformance.md](../bpmn-spec/conformance.md),
  [SAD-001 v.1.3](SAD-001-vision-and-architecture.md) §14).
- **Element scope of the import.** The set §2.3.2 applies to is
  [conformance.md](../bpmn-spec/conformance.md)'s in-scope list — Clause 13's
  operational elements plus the supporting classes they consume. §2.9 adopts that
  list verbatim rather than a subset of it.
- **`definitions`/`process` containment.** `Process` is a `rootElements` child of
  `definitions`; flow elements are `flowElements` children of `process`
  ([elements/foundation.md](../bpmn-spec/elements/foundation.md),
  [elements/process.md](../bpmn-spec/elements/process.md)). `isExecutable` is a
  0..1 attribute — the "executable" requirement is a conformance statement, not a
  schema cardinality.
- **None start/end.** A start or end event with zero `eventDefinitions` is the
  none variant; none-start *"starts a new Process instance"* (§13.5.1), none-end
  *"just consumes the token"* (§13.5.6)
  ([semantics/events.md](../bpmn-spec/semantics/events.md),
  [semantics/end-events.md](../bpmn-spec/semantics/end-events.md)).
- **Tasks.** The abstract `task` and `manualTask` are non-operational — an engine
  *"MAY treat it as a no-op pass-through"* (§13.1); `serviceTask` resolves
  `operationRef`, and `implementation` is a string hint
  ([semantics/tasks.md](../bpmn-spec/semantics/tasks.md), whose task-execution
  semantics the extract sources to §13.3).
- **Sequence flow.** `sourceRef`/`targetRef` are ID references;
  `conditionExpression` is a **child element**, an `Expression`
  ([elements/flows.md](../bpmn-spec/elements/flows.md)). `isImmediate` is
  non-operational and MAY be ignored
  ([semantics/token-flow.md:18](../bpmn-spec/semantics/token-flow.md)).
- **Gateways.** Exclusive: *"first condition that evaluates to true … else the
  default sequence flow … if all false AND no default → engine throws"* (§13.4.2);
  parallel takes one token from each incoming and puts one on each outgoing,
  *"cannot throw"* (§13.4.1), and has **no** `default` attribute
  ([semantics/gateways.md](../bpmn-spec/semantics/gateways.md)).
- **Lanes are representation, not semantics.** §2.3.1 permits ignoring
  non-operational elements *at run time*; it does not permit dropping them from the
  model, and §2.3.2 obliges an importer to support the modeller's diagram.
- **Visual artifacts carry nothing executable.** `TextAnnotation`, `Group` and
  `Category` are listed as *"pure visual"*, while `Association` is explicitly kept
  *"because it carries compensation semantics"*
  ([conformance.md](../bpmn-spec/conformance.md)) — the line §2.9 draws between
  preserving and mapping.
- **DI/DC out of scope.** *"BPMNShape, BPMNEdge … all `bpmndi:*` and `dc:*`,
  `di:*` | Visual layout metamodel; not part of execution conformance"*
  ([conformance.md:168](../bpmn-spec/conformance.md)).
- **Expression language is per-expression over a document default.**
  `FormalExpression.language` is 0..1 and `Definitions.expressionLanguage` is 0..1
  defaulting to `http://www.w3.org/1999/XPath`
  ([elements/foundation.md](../bpmn-spec/elements/foundation.md)). The standard
  nowhere requires a tool to implement any particular one.
- **Script language is unmandated.** `ScriptTask.scriptFormat` is 0..1
  ([elements/activities.md](../bpmn-spec/elements/activities.md)) and *"the spec
  does not mandate a script language"*
  ([semantics/tasks.md](../bpmn-spec/semantics/tasks.md)).
- **Business rules carry no decision reference.** `BusinessRuleTask`'s only own
  property is `implementation`, and *"the spec does not mandate a rule engine
  binding. Typical wiring is to DMN."* A `decisionRef` is therefore vendor
  vocabulary by construction, not by our omission — which is why §2.12 depends on
  §2.14.
- **Global tasks are reuse by reference.** The family derives from
  `CallableElement`, is invoked through `CallActivity.calledElement`, and carries a
  `name`, an `ioSpecification` and no flow of its own
  ([elements/activities.md](../bpmn-spec/elements/activities.md)) — which is what
  lets §2.13 import each as a process the registry serves like any other.
- **Collaboration is outside the library's execution scope and inside the
  importer's obligation.** *"Not animated by Clause 13; inter-process messaging is
  covered by Message events. Note §2.3.2 names the 'definitional Collaboration' for
  import — that is a server/converter concern"*
  ([conformance.md](../bpmn-spec/conformance.md)). §2.15 takes both halves of that
  sentence at face value.

**Engine notes (deliberate divergences).** Semantic-only round-trip (§2.8); the
BPMN `id` treated as durable versioning identity (§2.5 — the standard is silent on
registry versioning); an unmapped in-namespace element is a hard import error
rather than a lenient skip (§2.7 — stricter than the standard requires, to serve
the §5 feedback need); a declared expression language — including the `expressionLanguage` schema
default — does not override the `${…}` delimiters (§2.10); a refused script format and a refused expression language are engine
choices, since the standard mandates neither (§2.10, §2.11); a `calledElement`
read as a QName (§2.13); and **recognizing a vendor dialect** (§2.14) is outside
the standard entirely — the standard supplies the `extensionElements` mechanism
and assigns its content no meaning.

## 4. Alternatives considered

| # | Decision point | Options | Chosen — why |
|---|---|---|---|
| A | Home of the BPMN converter | (a) a core package; (b) a separate module | **(a)** — N7 is about **responsibility**, and that is preserved by import direction, not by a `go.mod`: the converter imports the model, never the reverse. A hand-rolled stdlib parser adds nothing to core's dependency budget, so the budget argument for (b) does not survive row D. Against it, (b) costs a module, a `replace`, a release tag and — decisively — invisibility to the diff-coverage gate, which sees only the root module. |
| B | Seam wiring | (a) a single injected implementation on the engine constructor; (b) a standalone register-by-key registry | **(b)** — the requirement is *multiple* pluggable formats; a single injected option models one implementation, not a keyed set, and it would tie a format table to an engine the converter must not know about. |
| C | Interface shape | (a) a unified `Converter{Import;Export}`; (b) split `Importer`/`Exporter` | **(b)** — a format may support one direction only, and a unified interface forces half-implementations to stub the other half. The split also mirrors the `io.Reader`/`io.Writer` asymmetry the engine already speaks. |
| D | Parser implementation | (a) wrap a third-party Go BPMN library; (b) hand-rolled `encoding/xml` | **(b)** — stdlib covers the mapping with zero dependencies, while existing libraries are diagram-heavy and pull weight the module does not need. Revisitable per format; the seam does not care. |
| E | Imported ids | (a) auto-generate; (b) preserve the BPMN `id` | **(b)** — ADR-019 keys versions on the process id, so auto-ids would make every import a singleton first version (§2.5). |
| F | Round-trip fidelity | (a) byte-lossless / DI-preserving; (b) semantic-only | **(b)** — diagram interchange is outside execution scope and textual losslessness is not a conformance requirement; preserving it is a modeller-grade feature with its own model carrier (§2.8, §7). |
| G | "Batteries-included" delivery | (a) blank-import, image-style; (b) a true core default | **(a)** — (b) puts a format in core and contradicts SAD-001 N7, so it needs a SAD revision; the blank import costs one line and keeps core clean (§2.3). |
| H | Import fence | (a) keep slicing element families; (b) the whole execution-conformance set | **(b)** — a definition is a graph, so a partial importer imports *nothing* from any file containing the one kind it lacks. Slicing delivers no usable increment until the final slice. The cost is one large landing; the alternative is several landings that each ship zero working imports (§2.9). |
| I | JUEL support | (a) a JUEL engine registered under its own language claim; (b) source-to-source translation; (c) unsupported | **(b)** — the grammars overlap almost completely, so (a) buys a second permanent implementation of every expression semantic to gain nothing (b) lacks. (c) refuses the corpus, since JUEL is what Camunda files actually contain. Untranslatable constructs are refused by name, never partially rewritten (§2.10). |
| J | Expression language when the declaration and the body disagree | (a) honour the declaration, including the XPath schema default; (b) let the `${…}` delimiters decide, whatever is declared | **(b)** — (a) is the literal reading and rejects nearly every real document: modellers either omit `expressionLanguage` and write `${…}`, or their tool writes the XPath default out explicitly and then emits JUEL under it. Both shapes are the same file, and refusing either on its own mislabelling gains nothing a correct import does not. A declaration still decides every body the delimiters do not touch. Recorded as a deliberate divergence rather than taken quietly (§2.10, §3). |
| K | Unroutable script format | (a) refuse; (b) default to a shipped engine | **(a)** — (b) runs another language's syntax through the wrong interpreter and reports the parse error from inside the engine at execution time, blaming the script rather than the missing format (§2.11). |
| L | DMN | (a) parse DMN XML in the converter; (b) carry an opaque decision reference | **(b)** — (a) contradicts the never-embed-a-DMN-engine non-goal and puts a second standard's parser inside a BPMN converter. The rule-engine seam resolves references; the converter's job ends at carrying one (§2.12). |
| M | Global tasks | (a) inline a copy of the task at every call site; (b) refuse the family; (c) import each as a **process** registered under its id | **(c)** — (a) converts reuse-by-reference into duplication: one global task called from three places imports as three unrelated tasks, and edits to the original stop propagating. (b) is honest only while nothing can serve a callable, and something can: a global task **is** a callable process (ADR-023 v.5 §2.7), so the reference stays a reference — one registration, any number of callers, versioned like a process — and the converter's part is to produce the process (§2.13). |
| N | Vendor extensions | (a) skip silently; (b) recognize Camunda 7 and map what has a home; (c) generic passthrough into a model carrier | **(b)** — (a) loses the executable content of every migrated file and cannot deliver §2.12's decision reference at all. (c) needs a new model type to hold arbitrary foreign XML and re-raises "what does it mean at execution?", which is the question (b) answers by mapping only what the model already understands (§2.14). |
| O | Multi-process documents | (a) refuse collaboration; (b) import the first process and drop the rest; (c) an optional document-level capability | **(c)** — (a) contradicts §2.3.2's explicit words; (b) drops definitions **silently**, the exact failure the feedback requirement exists to prevent. (c) is additive, leaves `Import` and every existing caller untouched, and needs no Collaboration model type (§2.15). |
| P | Reporting dropped constructs | (a) a second return value on `Import`; (b) a logger on the seam; (c) fold into the document-level `Result` | **(c)** — (a) breaks every existing caller; (b) makes the report a side effect a library consumer cannot inspect, only observe. One capability covering both multi-process and diagnostics keeps the seam at two interfaces plus one optional third (§2.15). |

## 5. Consequences

**Positive**

- Core stays dependency-clean; the XML surface is quarantined in one package,
  with the seam's format-agnosticism enforced mechanically.
- The seam is a genuine extension point: XPDL, a JSON DSL or a vendor dialect is a
  third-party registration, with no core change.
- `convert` works with no engine — offline validation, tooling, tests.
- Imported definitions version correctly (§2.5), so import composes with the call
  activity and registry line (ADR-019, ADR-023).
- Unsupported-element errors give the modeller the SAD-001 §5 feedback loop, and
  §2.16 makes the *kind* of refusal readable rather than only its existence.

**Negative / costs**

- BPMN is not a literal core default — a host adds a blank import to get it.
- No diagram-interchange round-trip: a file's layout is lost on import→export.
  Acceptable for an execution engine, and called out for anyone expecting a
  modeller-grade round-trip.
- **Import and export are not the same fence**, so §2.8's round-trip guarantee
  holds only over the export subset: a file can import cleanly and then fail to
  export. This is the price of §2.9 — import is what §2.3.2 obliges and what
  adoption needs — and §7 schedules its closure.
- **The converter owns a translator.** JUEL rewriting (§2.10) is a parser and a
  code generator living inside an import path, with its own correctness surface and
  its own failure mode: a wrong rewrite routes tokens wrongly, silently. The
  refuse-by-name rule bounds it, but the cost is real.
- **Recognizing a dialect is a standing commitment.** Camunda 7 keeps changing,
  and every construct it adds becomes a mapping question the converter must answer
  or report. Rule 1 of §2.14 bounds it to mapping work, never design work.
- **Refusals are a documented interface.** A refused expression language, script
  format, decision reference or callable reference is an outcome a host branches
  on, so their identity is API surface that cannot be reshuffled casually.

## 6. Enterprise-readiness recommendations

- **Conformance fixtures.** Wire the OMG **MIWG** import test set as the
  converter's acceptance corpus.
- **Streaming.** `Import(io.Reader)` / `Export(io.Writer)` are already
  stream-shaped; keep the BPMN implementation streaming so large definitions do
  not force whole-file buffering.
- **XSD / schema validation.** An optional strict mode validating against the OMG
  XSD before mapping, behind an option.
- **Extension-element passthrough.** Preserving unknown in-scope extension
  elements for a lossless custom-namespace round-trip needs a model carrier first.
- **Dialect targeting on export.** Emit plain OMG BPMN by default; a future option
  can target vendor namespaces.

## 7. Scope and deferrals

**In scope:** the converter seam and its registry (§2.1–§2.4); imported identity
as the versioning key (§2.5); import over the whole execution-conformance element
set with an explicit disposition for everything outside it (§2.7, §2.9–§2.15);
export over the subset of §2.6 with a semantic round-trip (§2.8); and the
classification every refusal outside coverage carries (§2.16).

**Deferred — forward pointers, not decided here:**

- **Export parity with the import fence.** The immediate successor, and the only
  thing that closes §5's asymmetry and restores §2.8's guarantee to the full
  fence. A cost is bounded only if it is paid.
- **Diagram-interchange preservation** — a round-trip a modeller would recognize.
  Needs a model carrier for layout, which is a modelling-tool feature rather than
  an execution one.
- **XSD strict mode** (§6) and **a second interchange format** behind the same
  seam: both are additive, and neither changes a decision here.
- **Service-task message bindings** (`inMessageRef`/`outMessageRef`) — parsed and
  recorded, not bound to the message catalog nor re-emitted.
- **BPMN as a true core default** rather than a blank import (§2.3, row G) — a
  SAD-001 N7 revision, and therefore a SAD decision.

## 8. References

**Design (up / sideways, versioned):**

- [SAD-001 v.1.3](SAD-001-vision-and-architecture.md) §4 N5/N7, §5, §9/§9.1/§9.2,
  §14 — the parser gap, modeller feedback, module layout, conformance scope.
- [ADR-002 v.2](ADR-002-extension-architecture.md) — interfaces plus compile-time
  wiring; the extension idiom the seam follows and deviates from once (§2.2).
- [ADR-019 v.1](ADR-019-definition-versioning.md) — version key = process id; the
  identity-preservation constraint (§2.5).
- [ADR-003 v.2](ADR-003-module-layout.md) — module boundaries and import-direction
  rules; the converter stays inside the root module (row A).
- [ADR-032 v.1](ADR-032-language-routed-expression-engines.md) — expressions are
  routed by language claim; §2.10's translation target and refusals are stated in
  that vocabulary.
- [ADR-031 v.1](ADR-031-script-task-and-script-engine-seam.md) — scripts are routed
  by `scriptFormat` through a registry of engines; §2.11 is a statement about what
  a document can carry, not about the seam.
- [ADR-027 v.1](ADR-027-business-rule-task-and-rule-engine-seam.md) — the
  rule-engine seam and the opaque decision reference §2.12 carries.
- [ADR-020 v.4](ADR-020-human-interaction-execution-model.md) — the
  human-interaction model whose assignee / candidate-user / candidate-group
  vocabulary §2.14 maps the dialect onto.
- [ADR-023 v.5](ADR-023-sub-process-and-call-activity.md) §2.7 — the callable
  reference, the resolver seam and the global-task-as-process decision §2.13
  serves.
- [ADR-030 v.1](ADR-030-data-objects-and-store.md) — the data elements the import
  dispositions and the `<import>` row land in.
- [ADR-028 v.2](ADR-028-transaction-sub-process.md) §2.7 — the transaction
  characteristics the model carries, cited by §2.16's never-compensate rule.
- [ADR-035 v.1](ADR-035-adhoc-sub-process.md) §2.2 — the host-supplied Router that
  makes ad-hoc a standing boundary (§2.16).
- [ADR-039 v.1](ADR-039-standard-artifacts.md) — the artifact tier §2.9 preserves
  into, and the capability-lands-first pattern (§2.16).
- [ADR-040 v.2](ADR-040-process-io-contract.md) — the process-level I/O contract a
  global task's `ioSpecification` becomes (§2.13).

SAD-001 is itself **Draft**, so the pins above track a moving baseline until it is
ratified.

**Standard (BPMN 2.0 KB):**

- [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) — §2.3 Process
  Execution Conformance (§2.3.1 semantics / §2.3.2 import); the in-scope element
  list; DI/DC out of scope.
- [docs/bpmn-spec/elements/](../bpmn-spec/index.md) — the structural metamodel.
- [docs/bpmn-spec/semantics/](../bpmn-spec/index.md) — token flow, tasks,
  gateways, events, end events.

## Open questions

None.

## Document History

| Version | Date | Change |
|---|---|---|
| v.1 | 2026-07-17 | Initial draft. The converter seam (`Importer`/`Exporter` plus a register-by-format registry in core), BPMN as the batteries-included converter, an MVP element subset both directions share, and a semantic — not byte-lossless — round-trip. |
| v.2 | 2026-07-30 | Accepted on the first landing. The converter is a package rather than a top-level module (row A reversed): a stdlib parser costs core no dependency, and a module would have been invisible to the diff-coverage gate. `serviceTask` joins the subset, and `documentation`/`extensionElements` are skipped silently rather than refused. |
| v.3 | 2026-08-02 | Accepted. Correction only: the fence had been defined by reference to the Common Executable Subclass, which is a **Modeling** sub-class and never applied. It is re-based on the elements Clause 13 animates, and §2.3.2 is recognized as making the converter the second of Process Execution Conformance's two requirements. The element set itself is unchanged. |
| v.4 | 2026-08-10 | **The import fence moves.** Import takes the whole execution-conformance element set, with an explicit disposition for every family outside it (§2.9), because a partial importer imports nothing from any file containing the one kind it lacks (row H). Adds the three language policies — JUEL translated source-to-source and untranslatable constructs refused by name, the XPath schema default deliberately not honoured (§2.10); self-contained script source only (§2.11); an opaque decision reference, never a DMN parser (§2.12) — the recognized Camunda 7 dialect (§2.14) and the optional `DocumentImporter` capability carrying a document's process set plus a report of dropped constructs (§2.15). Export stays on §2.6's subset, narrowing §2.8's guarantee to it. |
| v.5 | 2026-08-17 | `<import>` is mapped rather than skipped: collected by namespace and bound to the `<itemDefinition>` whose `structureRef` prefix resolves to it, with an unreferenced one reported. A disposition-row correction; no contract change. |
| v.6 | 2026-08-26 | The standard's three artifacts are **parsed and preserved** into the model-only artifact tier and `category`/`categoryValue` consumed as resolution input ([ADR-039 v.1](ADR-039-standard-artifacts.md)), on the same two-obligation reading as lanes. §2.16 absorbs the boundary rule the retired ADR-038 carried: the three classes, the never-compensate and capability-lands-first rules, and what each refusal owes its reader. |
| v.7 | 2026-08-28 | **Global tasks import as callable processes, a qualified `calledElement` carries its namespace, and §2.10 records the delimiter override.** Each global task becomes a process in the document's set — its id, its `ioSpecification` as the [ADR-040 v.2](ADR-040-process-io-contract.md) contract, its task built by the same reading as the in-process form — because [ADR-023 v.5](ADR-023-sub-process-and-call-activity.md) §2.7 decides a global task is a callable process the registry serves; row M records the reversal of v.4's refusal. A `calledElement` is read as a QName: an own-namespace prefix collapses to the key, an imported namespace rides with the call activity for the engine's resolver, an undeclared one is refused. §2.10 is corrected: the `${…}` delimiters decide the language whatever a document declares, which is what the converter has done since the JUEL translator landed while this ADR described the opposite order — a declaration now decides only the bodies the delimiters do not touch (row J restated). The document is also **rewritten for currency** — the version blockquotes, the superseded-fence notes, the "added in v.N" splitters and the resolved-questions ledger are gone, and the standard pins are re-verified. |
