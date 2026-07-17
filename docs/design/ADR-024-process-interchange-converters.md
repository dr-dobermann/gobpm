# ADR-024 — Process Interchange: pluggable import/export converters (BPMN 2.0 first)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-17 |
| Owner | Ruslan Gabitov |
| Refines | [SAD-001 v.1](SAD-001-vision-and-architecture.md) §4 N7 / §5 / §9 / §14, [ADR-002 v.2 Extension Architecture](ADR-002-extension-architecture.md), [ADR-019 v.1 Definition Versioning](ADR-019-definition-versioning.md), [ADR-003 v.1 Module Layout](ADR-003-module-layout.md) |

> **Draft** — decides how a process definition crosses the boundary between
> an **external interchange format** and gobpm's **in-memory model**, in both
> directions, without coupling either to the engine. The decision is a
> **format-agnostic converter seam** — two small interfaces (`Importer`,
> `Exporter`) plus a register-by-format-key registry in core — and a first
> **batteries-included** implementation for **BPMN 2.0 XML**, shipped as the
> separate module SAD-001 §9 already reserves (`doc-source/`), so core stays
> dependency-clean and "accepts pre-built models" (SAD-001 §4 N7). Import
> yields a `*process.Process`; the host registers it with the thresher itself
> (ADR-019 versioning intact, because imported BPMN `id`s are preserved as the
> definition's identity). Export walks the same model back to XML. The seam is
> the extension point (ADR-002): a third party registers an `Importer`/`Exporter`
> for XPDL, a JSON DSL, or a vendor dialect and the façade dispatches by
> format. This ADR fixes the contract and the MVP element subset; the
> accompanying SRD-051 lands BPMN import+export of that subset end-to-end.

---

## 1. Context & problem

gobpm builds a process definition exactly one way today: **programmatically**,
through `process.New(name, …opts)` + `p.Add(node)` + `flow.Link(src, trg)`
(`pkg/model/process/process.go:47,175`, `pkg/model/flow/sequenceflow.go:62`).
There is **no serialization layer of any kind** — a whole-repo search finds no
`encoding/xml`, no `MarshalXML`/`UnmarshalXML`, and no `json:` tags on any model
type. The engine "accepts pre-built models" and nothing else.

That is a deliberate 0.1.0 shape, but it blocks adoption. The governing SAD
names the gap in three places:

- **[SAD-001 v.1 §4 N7](SAD-001-vision-and-architecture.md):** *"BPMN XML
  parser … The parser will exist (it has to, for adoption), but it is a
  **separate module** that constructs the in-memory model the engine consumes.
  Core library accepts pre-built models."*
- **[SAD-001 v.1 §5](SAD-001-vision-and-architecture.md):** the **BPMN modeler**
  stakeholder authors *"BPMN 2.0 XML to be executed by goBpm"* and needs
  *"strict spec conformance; clear feedback on unsupported elements."*
- **[SAD-001 v.1 §9](SAD-001-vision-and-architecture.md):** the module layout
  already reserves `doc-source/ ← FUTURE — BPMN XML parser (own go.mod)` as a
  top-level module sibling to `runtime/` and `adapters/`.

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

// MustRegisterImporter / MustRegisterExporter panic on error — for use from
// a converter package's init() (documented Must* panic, per the public-API
// validation rule).

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

### 2.3 The BPMN converter is a separate, batteries-included module

Per SAD-001 §4 N7 and §9, the BPMN 2.0 XML converter ships as the reserved
top-level module (`doc-source/`, own `go.mod`), **not** inside core. It imports
core (`pkg/convert` for the interfaces, `pkg/model/*` to build/read the model),
holds **all** `encoding/xml` code, and self-registers:

```go
// package bpmn  (the reserved doc-source/ module)
func init() {
	convert.MustRegisterImporter(convert.BPMN, importer{})
	convert.MustRegisterExporter(convert.BPMN, exporter{})
}
```

**"Batteries-included" means first-party, in-repo, zero-config after a blank
import** — the `image`/`image/png` model: `_ "…/doc-source/bpmn"` registers
both directions; `convert.Import(ctx, convert.BPMN, r)` then works. Core users
who never import it get a clean *"unknown format"* error, never a hidden XML
dependency. The examples and (future) `runtime/` blank-import it so the
out-of-the-box experience is BPMN-ready.

> This honours the dependency direction ([SAD-001 v.1 §9.1](SAD-001-vision-and-architecture.md):
> the format module imports core, never the reverse) and N7's "core accepts
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

The MVP maps the **Common Executable Subclass** core
([docs/bpmn-spec/conformance.md §2.1.3](../bpmn-spec/conformance.md)) — the
same fence the engine's own conformance target already draws:

| BPMN XML | Model target | Spec § |
|---|---|---|
| `<bpmn:definitions>` (root) | document envelope | §10 |
| `<bpmn:process>` | `process.New(name, WithID(id))` | §10 |
| `<bpmn:startEvent>` (none) | `events.NewStartEvent` | §13.5.1 |
| `<bpmn:endEvent>` (none) | `events.NewEndEvent` | §13.5.6 |
| `<bpmn:task>` / `<bpmn:manualTask>` | `activities.NewManualTask` (parse-but-no-op, §13.1) | §13.3.3 |
| `<bpmn:userTask>` | `activities.NewUserTask` | §13.3.3 |
| `<bpmn:sequenceFlow>` (`sourceRef`/`targetRef`, `conditionExpression`) | `flow.Link(src, trg, WithCondition)` | §13.2 / §13.3.1 |
| `<bpmn:exclusiveGateway>` (`default`) | `gateways.NewExclusiveGateway` | §13.4.2 |
| `<bpmn:parallelGateway>` | `gateways.NewParallelGateway` | §13.4.1 |

**Explicitly out of MVP** (import must **tolerate and skip**, export must
**omit**): all Diagram Interchange — `bpmndi:*`, `dc:*`, `di:*`,
`BPMNShape`/`BPMNEdge` — which is *"not part of execution conformance"*
([conformance.md](../bpmn-spec/conformance.md), [SAD-001 v.1 §4 N5](SAD-001-vision-and-architecture.md)).
Also out of MVP and reported as **unsupported-element feedback** (SAD-001 §5),
not silently dropped: **`serviceTask`** (it needs `operationRef` →
`<bpmn:interface>`/`<bpmn:operation>` parsing and a `ServiceTask.Operation()`
export getter that does not exist today — the first work of slice 2, per
SRD-051 §4.6), inclusive/complex/event-based gateways, timer/message/signal/
error events, boundary events, sub-processes, call activities, lanes,
collaboration/choreography. Each lands in a later slice (§7).

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

## 3. Standard grounding

All claims cite the vendored KB ([docs/bpmn-spec/](../bpmn-spec/)), which carries
the OMG §-refs.

- **Conformance target.** *"Process Execution Conformance requires an engine to
  implement the operational semantics defined in §13 for … the Common
  Executable Subclass (§2.1.3)"* ([conformance.md:3-5](../bpmn-spec/conformance.md)).
  The MVP element set is exactly the executable core of that subclass.
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

**Engine notes (deliberate divergences):** semantic-only round-trip (§2.8);
BPMN `id` treated as durable versioning identity (§2.5 — a gobpm/ADR-019 choice,
the standard is silent on registry versioning); unsupported in-scope-namespace
elements are a hard import error rather than a lenient skip (§2.7 — stricter
than the standard requires, to serve the SAD-001 §5 feedback need).

## 4. Alternatives considered

| # | Decision point | Options | Chosen — why |
|---|---|---|---|
| A | Home of the BPMN parser | (a) in core; (b) separate module | **(b)** — SAD-001 §4 N7 + §9 mandate a separate module so core "accepts pre-built models" and keeps its stdlib+uuid budget. (a) would drag an XML/parser surface into core against N7. |
| B | Seam wiring | (a) `thresher.WithConverter` single injected impl; (b) standalone register-by-key registry | **(b)** — the requirement is *multiple* pluggable formats; a single injected option (the `WithLogger` idiom) models one impl, not a keyed set. (b) matches `data.SourceProvider` and `image.RegisterFormat`, and keeps convert engine-independent. |
| C | Interface shape | (a) unified `Converter{Import;Export}`; (b) split `Importer`/`Exporter` | **(b)** — a format may support one direction only; independent registration; mirrors `io.Reader`/`io.Writer`. A unified interface would force half-implementations to stub the other half. |
| D | Parser implementation | (a) wrap a third-party Go BPMN lib; (b) hand-rolled `encoding/xml` | **(b)** — the MVP subset is small and stdlib `encoding/xml` covers it with zero deps; existing libs are DI/diagram-heavy and pull weight the module does not need. Revisitable per-format (the seam does not care). |
| E | Imported ids | (a) auto-generate; (b) preserve BPMN `id` | **(b)** — ADR-019 keys versions on the process `id`; auto-ids would make every import a singleton v1 (§2.5). |
| F | Round-trip fidelity | (a) byte-lossless / DI-preserving; (b) semantic-only | **(b)** for MVP — DI is out of execution scope; textual losslessness is not a conformance requirement and would balloon MVP scope (§2.8). |
| G | "Batteries-included" delivery | (a) blank-import (image-style); (b) core-default (no import) | **(a)** for MVP — (b) contradicts SAD-001 N7 (BPMN in core) and would need a SAD revision. Surfaced in Open Questions. |

## 5. Consequences

**Positive**
- Core stays dependency-clean; the XML surface is quarantined in one module
  (SAD-001 §4/§9 upheld).
- The seam is a genuine extension point: XPDL, a JSON DSL, or a vendor dialect
  is a third-party `Importer`/`Exporter` registration — no core change.
- `convert` works with no engine — offline validation, tooling, tests.
- Imported definitions version correctly (§2.5), so import composes with the
  Call Activity / registry line (ADR-019, ADR-023).
- Unsupported-element errors give the modeler the SAD-001 §5 feedback loop.

**Negative / costs**
- BPMN is not a *literal* core default — a host must add the module dependency
  and a blank import to get it (the image-model trade-off).
- No DI round-trip — a file's diagram layout is lost on import→export. Acceptable
  for an execution engine; called out for anyone expecting a modeler-grade
  round-trip.
- A new top-level module (`doc-source/`) enters CI's per-module matrix (tidy,
  lint, race, coverage, govulncheck).

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

- **Slice 1 — SRD-051 (this ADR's landing).** `pkg/convert` seam + registry;
  the `doc-source/` module scaffold (go.mod + doc.go, per SAD-001 §9.2
  "scaffold upfront"); BPMN **import + export** of the §2.6 MVP subset;
  unsupported-element feedback; semantic round-trip test corpus; a
  `examples/bpmn-convert/` example; README/guide + changelog + CI per-module
  wiring.
- **Slice 2+ (own SRDs).** Gateways (inclusive/complex/event-based); events
  (timer/message/signal/error) + boundary events; sub-process & call activity
  (composes with ADR-023); lanes (parse-and-preserve); extension-element
  passthrough; XSD strict mode; additional formats behind the same seam.
- **Post-Slice-1:** `/check-srd`, SRD-051 → Accepted, ADR-024 → Accepted.

## 8. References

**Design (up / sideways, versioned):**
- [SAD-001 v.1](SAD-001-vision-and-architecture.md) §4 N5/N7, §5, §9/§9.1/§9.2, §14 — parser-as-separate-module, modeler feedback, module layout, conformance scope.
- [ADR-002 v.2](ADR-002-extension-architecture.md) — interfaces + compile-time wiring; the extension idiom the seam follows.
- [ADR-019 v.1](ADR-019-definition-versioning.md) — version key = process id; the identity-preservation constraint (§2.5).
- [ADR-003 v.1](ADR-003-module-layout.md) — module boundaries and import-direction rules for the new `doc-source/` module.

> **Note — Draft parents.** SAD-001 v.1 and ADR-003 v.1 are themselves **Draft**;
> their prescriptions may shift before Accepted, so these pins track a moving
> baseline until both are ratified.

**Standard (BPMN 2.0 KB):**
- [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) — §2.1.3 Common Executable Subclass; DI/DC out of scope.
- [docs/bpmn-spec/elements/](../bpmn-spec/elements/) — structural metamodel (foundation, process, events, activities, flows, gateways).
- [docs/bpmn-spec/semantics/](../bpmn-spec/semantics/) — token-flow, tasks, gateways, events, end-events.

**Code (grounding the model targets):**
- `pkg/model/process/process.go:47,175` — `process.New`, `Add`.
- `pkg/model/flow/sequenceflow.go:62` — `flow.Link`.
- `pkg/model/foundation/options.go:36` — `foundation.WithID`.
- `pkg/model/data/source.go:3` + `internal/scope/scope.go:111` — the register-by-key precedent.
- `pkg/thresher/thresher.go:642` — `RegisterProcess` (the host-side consumer of an import).

## Decisions (resolved open questions)

- **Q1 — Module name: DECIDED (proposed, pending SAD-001 §9 ratification).**
  Rename the reserved `doc-source/` module → **`convert/`** to reflect the now-
  bidirectional seam (`io/` rejected — collides with the stdlib package;
  `interchange/` longer, no gain). This is a SAD-001 §9 update (up-ref) for the
  SAD owner to ratify; the ADR records it as **proposed** and refers to "the
  converter module" until ratified.
- **Q2 — Batteries-included: DECIDED.** Ship BPMN as **blank-import**
  (`_ "…/convert/bpmn"`, image-style) — the only path that keeps core dependency-
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

## Document History

| Version | Date | Change |
|---|---|---|
| v.1 | 2026-07-17 | Initial draft — converter seam (`pkg/convert`), BPMN as the batteries-included separate-module converter, MVP element subset, semantic round-trip. |
