# SRD-051 — BPMN 2.0 converter: import & export of the MVP subset

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-17 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-024 v.1](../design/ADR-024-process-interchange-converters.md) §2.1–§2.8 (the converter seam + the first BPMN slice) |
| Upstream | [ADR-002 v.2](../design/ADR-002-extension-architecture.md) (extension interfaces live in `pkg/`; the seam follows it), [ADR-019 v.1](../design/ADR-019-definition-versioning.md) (version key = process id; the id-preservation constraint), [SAD-001 v.1](../design/SAD-001-vision-and-architecture.md) §4 N7 / §5 / §9 (parser as a separate module; modeler feedback; module layout), [docs/bpmn-spec/](../bpmn-spec/) (the BPMN 2.0 KB) |
| Refines | — |

## §1 Background

gobpm builds a process definition exactly one way today — programmatically,
via `process.New` + `Add` + `flow.Link` (`pkg/model/process/process.go:47,175`,
`pkg/model/flow/sequenceflow.go:62`). There is **no serialization of any kind**:
a whole-repo search finds no `encoding/xml`, no `Marshal/UnmarshalXML`, no
`json:` tags on any model type. The engine "accepts pre-built models"
([SAD-001 v.1 §4 N7](../design/SAD-001-vision-and-architecture.md)) and nothing
else — which blocks the **BPMN modeler** persona
([SAD-001 v.1 §5](../design/SAD-001-vision-and-architecture.md)) who authors
`.bpmn` XML to be executed.

[ADR-024 v.1](../design/ADR-024-process-interchange-converters.md) decides the
answer: a **format-agnostic converter seam** in core (`pkg/convert` —
`Importer`/`Exporter` + a register-by-format-key registry), and a first
**batteries-included** BPMN 2.0 XML converter shipped as the reserved separate
module (SAD-001 §9). This SRD lands **slice 1**: the seam, plus BPMN **import and
export** of the executable-core MVP subset, engine-independent, with a semantic
round-trip and unsupported-element feedback.

The seams this lands on already exist:

- **The model target** — every MVP node has an exported constructor
  (`events.NewStartEvent`/`NewEndEvent` `pkg/model/events/{start,end}.go:54,48`;
  `activities.NewManualTask`/`NewUserTask` `activities/{manual_task,user_task}.go:24,88`;
  `gateways.NewExclusiveGateway`/`NewParallelGateway`
  `gateways/{exclusive,parallel}.go:26,31`; `flow.Link`
  `flow/sequenceflow.go:62`). Identity is set with `foundation.WithID`
  (`foundation/options.go:36`).
- **The read-back for export** — `Process.Nodes()`/`Flows()`/`Name()`
  (`process/process.go:120,161,95`); `SequenceFlow.Source()`/`Target()`/
  `Condition()` (`flow/sequenceflow.go:299,304,309`); `Gateway.Direction()`/
  `DefaultFlow()` (`gateways/gateway.go:213,165`); `BaseElement.ID()`/`Name()`
  (`foundation/base.go:138`, `flow/element.go:109`); node discrimination via
  `NodeType()`/`EType()` (`flow/node.go:193,210`).
- **The registration consumer** — an import feeds `thresher.RegisterProcess`
  (`thresher/thresher.go:642`), which keys the version on the process id
  (ADR-019).

## §2 Requirements

### Functional — the seam (`pkg/convert`, core)

- **§FR-1 — Format & interfaces.** A `Format` string type with a `BPMN` constant;
  split `Importer`/`Exporter` interfaces over `io.Reader`/`io.Writer`,
  producing/consuming `*process.Process`.
- **§FR-2 — Registry.** `RegisterImporter`/`RegisterExporter` (+ `Must…`
  variants for `init()`), the façade `Import`/`Export`, and `Formats()`. Every
  public argument is validated on entry (empty `Format`, nil impl, duplicate
  `(Format, direction)` → a self-identifying error naming the function and the
  offending argument — the ADR-002 / project public-API rule). `Import`/`Export`
  on an unregistered format return a *"unknown format %q (registered: …)"* error
  enumerating `Formats()`.
- **§FR-3 — Unsupported-element feedback.** An `UnsupportedElementError` carrying
  the offending element's tag, id, and the spec § — the SAD-001 §5 *"clear
  feedback on unsupported elements"* requirement, surfaced from `Import`.

### Functional — the BPMN converter (the reserved separate module)

- **§FR-4 — Module scaffold.** A new module (SAD-001 §9 reservation; name
  proposed `convert/` per ADR-024 Q1, pending SAD ratification) with `go.mod` +
  `doc.go`, importing core only (dependency direction inward, SAD-001 §9.1). It
  **self-registers** both directions via `init()` (`MustRegisterImporter`/
  `Exporter`) so a blank import `_ "…/convert/bpmn"` turns BPMN on.
- **§FR-5 — Import.** Parse BPMN 2.0 XML (namespace
  `http://www.omg.org/spec/BPMN/20100524/MODEL`) into a `*process.Process` over
  the §FR-8 element set, via a **namespace-aware token-stream decoder**
  (`xml.Decoder`). BPMN `id`s are **preserved** as foundation identity
  (`foundation.WithID`) — never auto-generated (ADR-019; §4.4). A missing/blank
  `id` on a flow element is an import error.
- **§FR-6 — Export.** Serialize a `*process.Process` back to BPMN 2.0 XML over
  the §FR-8 set, writing `ID()` → `id` and `Name()` → `name`, emitting the
  `bpmn:` namespace. **No** Diagram Interchange is emitted.
- **§FR-7 — Diagram-interchange tolerance.** Import **skips** `bpmndi:*` / `dc:*`
  / `di:*` and other out-of-execution-scope foreign-namespace subtrees silently
  (SAD-001 §4 N5); an **in-`bpmn`-namespace** element the slice does not map is
  an `UnsupportedElementError` (§FR-3), not a silent drop.
- **§FR-8 — Element mapping (MVP subset).** The executable-core subset
  ([docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) §2.1.3):

  | BPMN XML | Model target | Spec § |
  |---|---|---|
  | `<bpmn:definitions>` / `<bpmn:process>` | document envelope / `process.New(name, WithID(id))` | §10 |
  | `<bpmn:startEvent>` (none) | `events.NewStartEvent` | §13.5.1 |
  | `<bpmn:endEvent>` (none) | `events.NewEndEvent` | §13.5.6 |
  | `<bpmn:task>` / `<bpmn:manualTask>` | `activities.NewManualTask` (no-op, §13.1) | §13.3.3 |
  | `<bpmn:userTask>` | `activities.NewUserTask` | §13.3.3 |
  | `<bpmn:sequenceFlow>` (`sourceRef`/`targetRef`, `conditionExpression`) | `flow.Link(src, trg, WithCondition)` | §13.2 / §13.3.1 |
  | `<bpmn:exclusiveGateway>` (`default`) | `gateways.NewExclusiveGateway` | §13.4.2 |
  | `<bpmn:parallelGateway>` | `gateways.NewParallelGateway` | §13.4.1 |

  **`serviceTask` is deferred to the next slice** (§4.6) — it requires
  `operationRef` → `<bpmn:interface>`/`<bpmn:operation>` parsing and a new
  `ServiceTask.Operation()` export getter, materially heavier than the
  no-external-ref tasks above.

### Functional — front door

- **§FR-9 — Example.** `examples/bpmn-convert/` (own module): blank-import the
  BPMN converter, `convert.Import` a bundled `.bpmn`, `RegisterProcess` + run it
  to completion on a thresher, then `convert.Export` it back.
- **§FR-10 — Docs.** A converter section in the user guide/README, a changelog
  entry, and the tracking-issue link.

### Non-functional

- **§NFR-1 — Dependency budget.** `pkg/convert` is stdlib-only (no `xml`); the
  BPMN module uses stdlib `encoding/xml` only — zero third-party deps
  (SAD-001 §9.1).
- **§NFR-2 — Streaming.** Import/export are `io.Reader`/`io.Writer`-shaped and
  stream via `xml.Decoder`/`xml.Encoder` — no whole-file buffering mandated.
- **§NFR-3 — Semantic round-trip.** `Import` then `Export` yields a
  structurally/semantically equivalent process (same ids, nodes, flows,
  conditions, gateway kinds) — **not** byte-identical XML (ADR-024 §2.8).
- **§NFR-4 — Public-API validation.** Every exported constructor/registry
  function validates all parameters with self-identifying errors.
- **§NFR-5 — CI parity.** The new module joins the `make ci` per-module matrix
  (tidy, lint, build, race, diff-coverage ≥ `COVER_MIN`, govulncheck); touched
  files meet the diff-coverage gate.

## §3 Models

### §3.1 `pkg/convert` (core seam)

```go
package convert

type Format string

const BPMN Format = "bpmn"

type Importer interface {
	Import(ctx context.Context, r io.Reader) (*process.Process, error)
}

type Exporter interface {
	Export(ctx context.Context, w io.Writer, p *process.Process) error
}

func RegisterImporter(f Format, imp Importer) error
func RegisterExporter(f Format, exp Exporter) error
func MustRegisterImporter(f Format, imp Importer) // panics on error (init use)
func MustRegisterExporter(f Format, exp Exporter)

func Import(ctx context.Context, f Format, r io.Reader) (*process.Process, error)
func Export(ctx context.Context, f Format, w io.Writer, p *process.Process) error
func Formats() []Format

type UnsupportedElementError struct {
	Tag     string // local element name, e.g. "inclusiveGateway"
	ID      string // the element's id attr, if present
	Section string // spec §, e.g. "§13.4.3"
}

func (e *UnsupportedElementError) Error() string
```

Registry state is package-global maps keyed by `Format`, mutated only by
`Register…` (typically from a converter package's `init()`) — the
`image.RegisterFormat` idiom (ADR-024 §2.2), a deliberate deviation from the
functional-options norm because `convert` is engine-independent.

### §3.2 `convert/bpmn` (the BPMN module)

Import uses a **token-stream decoder** (not struct-unmarshal — §4.3). Export
uses typed XML structs marshalled with `xml.Encoder`:

```go
type xmlDefinitions struct {
	XMLName   xml.Name     `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL definitions"`
	Processes []xmlProcess `xml:"process"`
}

type xmlProcess struct {
	ID            string            `xml:"id,attr"`
	Name          string            `xml:"name,attr,omitempty"`
	IsExecutable  bool              `xml:"isExecutable,attr"`
	StartEvents   []xmlNode         `xml:"startEvent"`
	EndEvents     []xmlNode         `xml:"endEvent"`
	Tasks         []xmlNode         `xml:"task"`
	ManualTasks   []xmlNode         `xml:"manualTask"`
	UserTasks     []xmlNode         `xml:"userTask"`
	ExclusiveGWs  []xmlGateway      `xml:"exclusiveGateway"`
	ParallelGWs   []xmlNode         `xml:"parallelGateway"`
	SequenceFlows []xmlSequenceFlow `xml:"sequenceFlow"`
}

type xmlSequenceFlow struct {
	ID        string `xml:"id,attr"`
	Name      string `xml:"name,attr,omitempty"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
	Condition string `xml:"conditionExpression,omitempty"`
}

type importer struct{}
type exporter struct{}

func init() {
	convert.MustRegisterImporter(convert.BPMN, importer{})
	convert.MustRegisterExporter(convert.BPMN, exporter{})
}
```

### §3.3 Import algorithm (two-pass)

1. **Decode** the token stream: collect nodes and flows; skip foreign-namespace
   subtrees (`di`/`dc`); an unmapped `bpmn:`-namespace start element →
   `UnsupportedElementError`.
2. **Build nodes** first — every node constructor called with `WithID(id)` — so
   the id→node table is complete before wiring.
3. **Link flows** — `flow.Link(nodes[sourceRef], nodes[targetRef], …)`, attaching
   `WithCondition` when `conditionExpression` is present; re-resolve each
   exclusive gateway's `default` by flow id.
4. **`process.Validate()`** the assembled graph before returning.

## §4 Analysis

### §4.1 Why a separate module, engine-independent
Inherited from ADR-024 §2.1–§2.4: SAD-001 §4 N7 mandates the parser as a
separate module so core "accepts pre-built models"; import returns a
`*process.Process` and the host registers it — the engine never imports the
converter.

### §4.2 Why preserve BPMN ids
ADR-019 keys the version lineage on the process **id**; auto-generating ids on
import would make every import a singleton v1 and silently defeat versioning
(ADR-024 §2.5). Hence `foundation.WithID` on every mapped element, and a
missing flow-element `id` is a hard error.

### §4.3 Why a token-stream decoder, not struct-unmarshal
`encoding/xml` struct-unmarshal **silently ignores** any element it has no field
for — which would make §FR-3/§FR-7 impossible (an unsupported `bpmn:` element
would vanish instead of raising `UnsupportedElementError`). A namespace-aware
`xml.Decoder` token loop dispatches on each start element by local-name +
namespace, so it can (a) map known elements, (b) skip DI subtrees by namespace,
and (c) **error** on an unknown `bpmn:`-namespace element. Export has no such
problem — we control the output — so it uses struct marshalling.

### §4.4 Two-pass import
`sourceRef`/`targetRef` are id references and `flow.Link` needs both endpoint
nodes to exist; a single forward pass would fail on a flow whose target appears
later in the document. Nodes are built first, flows linked second (§3.3).

### §4.5 DI skip vs unsupported-element error
DI/DC (`bpmndi:*`, `dc:*`, `di:*`) is out of execution conformance
([conformance.md:155](../bpmn-spec/conformance.md)) — **skipped silently**. An
in-`bpmn`-scope element the slice hasn't mapped yet (inclusive gateway, timer
event, boundary event, sub-process) is **reported**, so the modeler learns what
the engine won't run (SAD-001 §5). The discriminator is the namespace.

### §4.6 Why `serviceTask` is deferred
`NewServiceTask` requires a non-nil `service.Operation`
(`activities/service_task.go:84`), which in BPMN is an `operationRef` into an
`<bpmn:interface>`/`<bpmn:operation>` under `<definitions>`. Faithful import
therefore needs interface/operation parsing, and faithful export needs a
`ServiceTask.Operation()` getter that **does not exist today** (the struct field
`operation` has no exported accessor, `activities/service_task.go`). Both are the
first work of slice 2; landing them half-done (a placeholder operation that
can't execute) would ship a misleading feature. The no-external-ref tasks
(`task`/`manualTask`/`userTask`) round-trip cleanly and carry the MVP.

> **ADR reconciliation:** ADR-024 §2.6 lists `serviceTask` in the MVP table.
> This SRD trims it (discovered during grounding). ADR-024 §2.6 is to be updated
> to move `serviceTask` to "later slice" — a downstream-sync edit on the parent
> ADR, to confirm with the owner.

### §4.7 Rejected shapes
- **Wrapping a third-party BPMN library** — pulls DI/diagram weight the module
  doesn't need for the executable subset; stdlib `encoding/xml` suffices
  (ADR-024 §4-D).
- **Struct-unmarshal import** — silently drops unknowns (§4.3).
- **Byte-lossless round-trip** — DI is out of scope and textual losslessness is
  not a conformance requirement (ADR-024 §2.8 / §4-F).

## §5 API surface

**New (core):** `pkg/convert` — `Format`, `BPMN`, `Importer`, `Exporter`,
`RegisterImporter`/`RegisterExporter`, `MustRegisterImporter`/
`MustRegisterExporter`, `Import`, `Export`, `Formats`, `UnsupportedElementError`.

**New (module):** `convert/bpmn` — no exported surface beyond the `init()`
self-registration (consumers use the `convert` façade); blank-import to enable.

**Unchanged:** the model constructors, `thresher.RegisterProcess`. No engine API
change.

## §6 Test scenarios

- **`TestConvertRegistry`** (`pkg/convert`) — register/lookup happy path;
  duplicate registration rejected; nil impl rejected; empty format rejected;
  `Import`/`Export` on an unknown format returns the enumerating error;
  `Formats()` lists registered.
- **`TestBPMNImportMVP`** (`convert/bpmn`) — each §FR-8 element imports to the
  right constructor with id/name preserved; conditional + default flows attach.
- **`TestBPMNExportMVP`** — a programmatically built process exports to XML with
  correct tags/attrs; no DI emitted.
- **`TestBPMNRoundTrip`** — the §6 worked example: import → export → re-import →
  structural equality (ids, node kinds, flows, conditions, gateway direction &
  default). Semantic, not byte (NFR-3).
- **`TestBPMNPreservesID`** — an imported process registered via
  `RegisterProcess` carries the BPMN process id as its version key (ADR-019).
- **`TestBPMNUnsupportedElement`** — an `<bpmn:inclusiveGateway>` yields
  `UnsupportedElementError{Tag:"inclusiveGateway", …}`; a `<bpmndi:BPMNDiagram>`
  is skipped silently (no error).
- **`TestBPMNImportRegisterRun`** (e2e, `examples/bpmn-convert/` or
  `pkg/thresher`) — import the bundled `.bpmn`, register, run to completion.

**Worked example** (the round-trip fixture — start → userTask → exclusiveGateway
with a conditional branch + a default branch → two ends):

```xml
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="approval" name="Approval" isExecutable="true">
    <bpmn:startEvent id="s1" name="start"/>
    <bpmn:userTask id="u1" name="review"/>
    <bpmn:exclusiveGateway id="g1" name="decide" default="f_no"/>
    <bpmn:endEvent id="e_ok" name="approved"/>
    <bpmn:endEvent id="e_no" name="rejected"/>
    <bpmn:sequenceFlow id="f_su" sourceRef="s1" targetRef="u1"/>
    <bpmn:sequenceFlow id="f_ug" sourceRef="u1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f_ok" sourceRef="g1" targetRef="e_ok">
      <bpmn:conditionExpression>approved == true</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f_no" sourceRef="g1" targetRef="e_no"/>
  </bpmn:process>
</bpmn:definitions>
```

After `convert.Import(ctx, convert.BPMN, r)`: `p.Name() == "Approval"`,
`p.ID() == "approval"`, `len(p.Nodes()) == 5`, `len(p.Flows()) == 4`, the flow
`f_ok` carries a condition, and `g1.DefaultFlow().ID() == "f_no"`. Re-exporting
and re-importing reproduces the same structure (NFR-3).

## §7 Milestones

| # | Scope |
|---|---|
| M1 | `pkg/convert` seam — Format, `Importer`/`Exporter`, registry (+`Must…`, façade, `Formats`), `UnsupportedElementError`; `TestConvertRegistry` (§FR-1..3) |
| M2 | `convert/bpmn` module scaffold + **import** (token-stream, id-preservation, DI-skip, unsupported-element error) of §FR-8; import + preserve-id + unsupported tests (§FR-4/5/7/8) |
| M3 | **export** (`xml.Encoder`) of §FR-8 + semantic **round-trip** tests; the worked-example fixture (§FR-6, NFR-3) |
| M4 | `examples/bpmn-convert/` + guide/README + changelog + tracker + CI per-module wiring + e2e (§FR-9/10, NFR-5) |

Post-M4: `/check-srd`, §10 fill, SRD-051 → Accepted, ADR-024 → Accepted, and the
ADR-024 §2.6 reconciliation (§4.6).

## §8 Cross-doc

- **Implements** [ADR-024 v.1](../design/ADR-024-process-interchange-converters.md)
  §2.1–§2.8 — the seam and the first BPMN slice.
- **Upstream** [ADR-002 v.2](../design/ADR-002-extension-architecture.md) (seam
  idiom), [ADR-019 v.1](../design/ADR-019-definition-versioning.md) (id =
  version key), [SAD-001 v.1](../design/SAD-001-vision-and-architecture.md) §4
  N7 / §5 / §9 (separate module, modeler feedback, layout).
- **Standard** [docs/bpmn-spec/](../bpmn-spec/) — conformance §2.1.3, elements,
  semantics (§ pins in §FR-8).
- **Downstream sync (on landing):** ADR-024 §2.6 `serviceTask` trim (§4.6);
  and — if the SAD owner ratifies Q1 — the module name rename in SAD-001 §9.
- **Direction check:** SRD → ADR/SAD (up), all pins versioned. No downward ref.

## §9 Definition of Done

- Every §FR wired to real code; §6 tests present and green.
- `make ci` green across all modules **including the new `convert/bpmn`
  module** (tidy, lint, build, race, diff-coverage ≥ `COVER_MIN` on touched
  files, govulncheck).
- `TestBPMNRoundTrip` and `TestBPMNImportRegisterRun` green; unsupported-element
  feedback and id→version tie demonstrated.
- `examples/bpmn-convert/` runs to completion; guide/README + changelog updated.
- Cross-doc pins consistent (§8); ADR-024 §2.6 reconciliation applied.

## §10 Implementation summary

_(placeholder — filled at landing: milestones-by-commit, deltas vs this draft,
empirical findings, backlog.)_

## Open questions

1. **Worked-example fixture source.** Hand-authored (the §6 snippet) for MVP, or
   vendor a small **MIWG** conformance fixture subset now (SAD-001 §6 names MIWG
   as the P0 conformance tactic)? Recommend hand-authored for slice 1; MIWG as a
   later corpus.
2. **`conditionExpression` language.** MVP imports the expression **body** as a
   `data.FormalExpression`; do we record the `language` attribute (XPath vs the
   engine's expression language) now, or default it and defer language handling?
3. **Module name (inherited ADR-024 Q1).** `convert/` pending SAD-001 §9
   ratification; the SRD uses "the BPMN module" until then.
