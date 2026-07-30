# SRD-051 — BPMN 2.0 converter: import & export of the MVP subset

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.2 |
| Date | 2026-07-30 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-024 v.1](../design/ADR-024-process-interchange-converters.md) §2.1–§2.8 (the converter seam + the first BPMN slice) |
| Upstream | [ADR-002 v.2](../design/ADR-002-extension-architecture.md) (extension interfaces live in `pkg/`; the seam follows it), [ADR-019 v.1](../design/ADR-019-definition-versioning.md) (version key = process id; the id-preservation constraint), [SAD-001 v.1](../design/SAD-001-vision-and-architecture.md) §4 N7 / §5 / §9 (parser outside the engine; modeler feedback; module layout), [docs/bpmn-spec/](../bpmn-spec/) (the BPMN 2.0 KB) |
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
**batteries-included** BPMN 2.0 XML converter. This SRD lands **slice 1**: the seam, plus BPMN **import and
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

### Functional — the BPMN converter (`pkg/convert/bpmn`)

- **§FR-4 — Converter package.** A sibling package of the seam,
  `pkg/convert/bpmn`, in the root module. It imports core only (dependency
  direction inward, SAD-001 §9.1) and adds no third-party dependency — the
  parser is stdlib `encoding/xml`. It **self-registers** both directions via
  `init()` (`MustRegisterImporter`/`Exporter`), so the blank import
  `_ "github.com/dr-dobermann/gobpm/pkg/convert/bpmn"` turns BPMN on.

  > **v.2 change.** v.1 specified a separate top-level module (the SAD-001 §9
  > `doc-source/` reservation, renamed `convert/` by ADR-024 Q1). It shipped as
  > a package instead — see §4.1.
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

  **Exception — non-executable annotations.** `<bpmn:documentation>` and
  `<bpmn:extensionElements>` are skipped silently wherever they appear, despite
  being in the BPMN namespace. Both are universal in bpmn.io / Camunda output
  and carry no execution semantics; erroring on them would reject essentially
  every real modeler file whose *flow graph* is fully inside the subset. The
  error contract therefore covers unmapped **flow elements**, not annotations.
- **§FR-8 — Element mapping (MVP subset).** The executable-core subset
  ([docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) §2.1.3):

  | BPMN XML | Model target | Spec § |
  |---|---|---|
  | `<bpmn:definitions>` / `<bpmn:process>` | document envelope / `process.New(name, WithID(id))` | §10 |
  | `<bpmn:startEvent>` (none) | `events.NewStartEvent` | §13.5.1 |
  | `<bpmn:endEvent>` (none) | `events.NewEndEvent` | §13.5.6 |
  | `<bpmn:task>` / `<bpmn:manualTask>` | `activities.NewManualTask` (no-op, §13.1) | §13.3.3 |
  | `<bpmn:userTask>` | `activities.NewUserTask` | §13.3.3 |
  | `<bpmn:serviceTask>` (`operationRef`) | `activities.NewServiceTask` | §13.3.2 |
  | `<bpmn:interface>` / `<bpmn:operation>` | `service.NewOperation` (catalog stub) | §10.4 |
  | `<bpmn:sequenceFlow>` (`sourceRef`/`targetRef`, `conditionExpression`) | `flow.Link(src, trg, WithCondition)` | §13.2 / §13.3.1 |
  | `<bpmn:exclusiveGateway>` (`default`) | `gateways.NewExclusiveGateway` | §13.4.2 |
  | `<bpmn:parallelGateway>` | `gateways.NewParallelGateway` | §13.4.1 |

  > **v.2 change.** v.1 deferred `serviceTask` to slice 2. It landed in slice 1
  > together with the `ServiceTask.Operation()` accessor it needs for export —
  > see §4.6.

### Functional — front door

- **§FR-9 — Example.** `examples/bpmn-convert/` (own module): blank-import the
  BPMN converter, `convert.Import` a bundled `.bpmn`, `RegisterProcess` + run it
  to completion on a thresher, then `convert.Export` it back.
- **§FR-10 — Docs.** A converter section in the user guide/README, a changelog
  entry, and the tracking-issue link.

### Non-functional

- **§NFR-1 — Dependency budget.** `pkg/convert` is stdlib-only and carries **no
  serialization of its own** — no `encoding/xml`, no `encoding/json`; enforced
  by the `convert-seam-format-agnostic` depguard rule, scoped to
  `pkg/convert/*.go` so the format packages below it stay free to serialize.
  `pkg/convert/bpmn` uses stdlib `encoding/xml` only. Neither adds a
  third-party dependency, so core's "stdlib + uuid" budget (SAD-001 §9.1) is
  untouched.
- **§NFR-2 — Streaming.** Import/export are `io.Reader`/`io.Writer`-shaped and
  stream via `xml.Decoder`/`xml.Encoder` — no whole-file buffering mandated.
- **§NFR-3 — Semantic round-trip.** `Import` then `Export` yields a
  structurally/semantically equivalent process (same ids, nodes, flows,
  conditions, gateway kinds) — **not** byte-identical XML (ADR-024 §2.8).
- **§NFR-4 — Public-API validation.** Every exported constructor/registry
  function validates all parameters with self-identifying errors.
- **§NFR-5 — CI parity.** The converter is part of the root module, so it is
  covered by `make ci-core` (tidy, lint, build, race, govulncheck) with no
  Makefile or workflow change — and, unlike a separate module, its code **does**
  reach the `cover-check` diff-coverage gate (`coverage.txt` is written for the
  root module only). Touched files meet ≥ `COVER_MIN`.

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

### §3.2 `pkg/convert/bpmn` (the BPMN converter)

No exported surface: the package is reached through the `convert` façade and
switched on by a blank import. Import uses a **token-stream decoder** (not
struct-unmarshal — §4.3); export uses typed XML structs marshalled with
`xml.Encoder`:

```go
// One element type carries every flow node; Tag picks the concrete name, and
// the unused attributes are omitempty. A per-element struct (v.1's shape) would
// have forced Elements into per-kind slices and lost document order on export.
type xmlNode struct {
	XMLName        xml.Name
	ID             string `xml:"id,attr"`
	Name           string `xml:"name,attr,omitempty"`
	Direction      string `xml:"gatewayDirection,attr,omitempty"`
	Default        string `xml:"default,attr,omitempty"`
	Implementation string `xml:"implementation,attr,omitempty"`
	OperationRef   string `xml:"operationRef,attr,omitempty"`
}

// Elements is []any so nodes and flows marshal each under their own tag, in
// the order the model reports them.
type xmlProcess struct {
	XMLName      xml.Name `xml:"bpmn:process"`
	ID           string   `xml:"id,attr"`
	Name         string   `xml:"name,attr,omitempty"`
	Elements     []any
	IsExecutable bool `xml:"isExecutable,attr"`
}

// The condition is a child element, not an attribute: it carries chardata plus
// its own id and language URI.
type xmlSequenceFlow struct {
	XMLName   xml.Name
	Condition *xmlCondition `xml:"bpmn:conditionExpression,omitempty"`
	ID        string        `xml:"id,attr"`
	Name      string        `xml:"name,attr,omitempty"`
	SourceRef string        `xml:"sourceRef,attr"`
	TargetRef string        `xml:"targetRef,attr"`
}

type importer struct{}
type exporter struct{}

func init() { //nolint:gochecknoinits // §FR-4 self-registration
	convert.MustRegisterImporter(convert.BPMN, importer{})
	convert.MustRegisterExporter(convert.BPMN, exporter{})
}
```

Field order in these structs is dictated by `govet/fieldalignment`, which the
repo enforces; it does not affect the emitted document, because `encoding/xml`
collects attributes in a pass of their own before writing children.

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

### §4.1 Why a package, not a separate module
ADR-024 §4-A chose a separate module on two grounds: SAD-001 §4 N7 ("core
accepts pre-built models") and keeping core's "stdlib + uuid" dependency budget.
The second ground does not survive ADR-024's own §4-D, which chose a
**hand-rolled `encoding/xml`** parser: `encoding/xml` *is* stdlib, so the
converter adds nothing to the budget — its `go.sum` requirement set is empty.
What remains of N7 is a statement about **responsibility**, and that is
preserved by direction, not by a module boundary: the converter imports the
model, the engine never imports the converter, and `Import` returns a
`*process.Process` the host registers itself.

Against that, a module costs a `go.mod`, a `replace`, its own release tag — and,
decisively, **invisibility to the diff-coverage gate**: `make test-all` writes
`coverage.txt` for the root module only, so ~1 600 lines of parser would never
have been measured. Moving it in-tree put it under the 95 % gate, which is what
surfaced the dead `operationRef` export path (§4.6).

One constraint had to be handled explicitly. `internal/lintcfg/muststyle_test.go`
(FIX-026) fails the build on any `Must*` **call** under `pkg/`, and §FR-4's
`init()` calls `MustRegisterImporter`/`MustRegisterExporter`. Rather than dodge
the guard, `pkg/convert/bpmn/bpmn.go` is listed in that test's `exemptFiles`
with the reason: the arguments are compile-time constants of the package, the
call happens once at load time, and the only reachable failure is a duplicate
registration — a programming error an `init()` cannot return.

### §4.1a Why the seam is core and the format is not
`pkg/convert` is engine-facing contract (ADR-002: extension interfaces live in
`pkg/`), so it belongs in core and must stay format-agnostic — enforced by
depguard (§NFR-1). `pkg/convert/bpmn` is one implementation of that contract;
further formats (XPDL, a vendor dialect) become sibling packages, or
out-of-tree packages in the user's own module, with no core change.

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

### §4.6 `serviceTask` — deferred in v.1, landed in v.2
`NewServiceTask` requires a non-nil `service.Operation`, which in BPMN is an
`operationRef` into an `<bpmn:interface>`/`<bpmn:operation>` under
`<definitions>`. v.1 deferred the whole element because faithful export also
needs a `ServiceTask.Operation()` accessor that did not exist.

Both halves shipped in slice 1:

- **Import** parses the definitions-level catalog before the process, so
  `operationRef` resolves to a `service.Operation` built by `service.NewOperation`
  with **no implementor** — a catalog stub the host binds after import. A
  `serviceTask` with no `operationRef` gets a synthetic operation id
  `<taskID>:operation`; an `operationRef` naming an unknown operation is a hard
  import error (`ObjectNotFound`).
- **Export** writes `operationRef` and reconstructs a single synthetic
  `<bpmn:interface>` per process (id `<processID>-services`) holding every
  operation the process references, so the ref resolves on re-import.
- **`activities.ServiceTask.Operation()`** was added as a read-only accessor.
  It is the one core API addition in this SRD.

**Why the accessor could not be avoided.** The converter first shipped a
structural workaround — `any(st).(interface{ Operation() service.Operation })` —
so it would compile against a gobpm without the getter and light up against one
with it. Inside the repo that assertion could never succeed, which made the
whole `operationRef`/interface-catalog export path unreachable: exported
documents silently lost the service binding, and `TestServiceTaskRoundTrip`
did not notice because it only asserted the node's *type* survived. The
diff-coverage gate (§4.1) is what exposed it. The workaround is gone; the
round-trip test now asserts `Operation().ID()` survives.

**Message bindings remain deferred.** `inMessageRef`/`outMessageRef` are parsed
and recorded on the operation spec but not bound to `bpmncommon.Message`, and
not re-emitted on export. That is the first work of the next slice.

### §4.7 Rejected shapes
- **Wrapping a third-party BPMN library** — pulls DI/diagram weight the package
  doesn't need for the executable subset; stdlib `encoding/xml` suffices
  (ADR-024 §4-D). It would also have breached core's dependency budget, which
  the stdlib parser leaves untouched (§4.1).
- **Struct-unmarshal import** — silently drops unknowns (§4.3).
- **Byte-lossless round-trip** — DI is out of scope and textual losslessness is
  not a conformance requirement (ADR-024 §2.8 / §4-F).

## §5 API surface

**New (core):** `pkg/convert` — `Format`, `BPMN`, `Importer`, `Exporter`,
`RegisterImporter`/`RegisterExporter`, `MustRegisterImporter`/
`MustRegisterExporter`, `Import`, `Export`, `Formats`, `UnsupportedElementError`.

**New (core):** `pkg/convert/bpmn` — no exported surface beyond the `init()`
self-registration (consumers use the `convert` façade); blank-import to enable.

**Changed (core):** `activities.ServiceTask.Operation() service.Operation` — a
read-only accessor for the operation the task was constructed with, needed by
export (§4.6). Additive; never nil, since `NewServiceTask` rejects a nil
operation.

**Unchanged:** the other model constructors, `thresher.RegisterProcess`. No
engine API change.

## §6 Test scenarios

- **`TestConvertRegistry`** (`pkg/convert`) — register/lookup happy path;
  duplicate registration rejected; nil impl rejected; empty format rejected;
  `Import`/`Export` on an unknown format returns the enumerating error;
  `Formats()` lists registered.
- **`TestBPMNImportMVP`** (`pkg/convert/bpmn`) — each §FR-8 element imports to the
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
| M2 | `pkg/convert/bpmn` + **import** (token-stream, id-preservation, DI-skip, unsupported-element error) of §FR-8; import + preserve-id + unsupported tests (§FR-4/5/7/8) |
| M3 | **export** (`xml.Encoder`) of §FR-8 + semantic **round-trip** tests; the worked-example fixture (§FR-6, NFR-3) |
| M4 | `examples/bpmn-convert/` + guide/README + changelog + e2e (§FR-9/10, NFR-5) |

Post-M4: `/check-srd`, §10 fill, SRD-051 → Accepted, ADR-024 → Accepted, and the
ADR-024 §4-A / §2.6 reconciliation (§4.1, §4.6).

## §8 Cross-doc

- **Implements** [ADR-024 v.1](../design/ADR-024-process-interchange-converters.md)
  §2.1–§2.8 — the seam and the first BPMN slice.
- **Upstream** [ADR-002 v.2](../design/ADR-002-extension-architecture.md) (seam
  idiom), [ADR-019 v.1](../design/ADR-019-definition-versioning.md) (id =
  version key), [SAD-001 v.1](../design/SAD-001-vision-and-architecture.md) §4
  N7 / §5 / §9 (parser outside the engine, modeler feedback, layout).
- **Standard** [docs/bpmn-spec/](../bpmn-spec/) — conformance §2.1.3, elements,
  semantics (§ pins in §FR-8).
- **Downstream sync (applied at landing):** ADR-024 §4-A re-decided to a package
  (§4.1) and Q1 withdrawn — there is no module left to name; ADR-024 §2.6
  `serviceTask` restored to the MVP set (§4.6); SAD-001 §9 `doc-source/`
  reservation retired.
- **Direction check:** SRD → ADR/SAD (up), all pins versioned. No downward ref.

## §9 Definition of Done

- Every §FR wired to real code; §6 tests present and green.
- `make ci` green (tidy, lint, build, race, diff-coverage ≥ `COVER_MIN` on
  touched files, govulncheck).
- The round-trip and import→register→run tests green; unsupported-element
  feedback and id→version tie demonstrated.
- `examples/bpmn-convert/` runs to completion; guide/README + changelog updated.
- Cross-doc pins consistent (§8); the ADR-024 / SAD-001 reconciliation applied.

## §10 Implementation summary

Landed on `feat/bpmn-converter` (2026-07-30). The converter was first built
out-of-tree against the published `gobpm v0.9.0` and merged in; every §FR is
wired, `make ci-core` is green, and the example runs end to end.

### Deltas vs v.1

| # | v.1 said | Shipped | Where |
|---|---|---|---|
| 1 | BPMN lives in a separate top-level module | a package, `pkg/convert/bpmn`, in the root module | §4.1 |
| 2 | `serviceTask` deferred to slice 2 | landed in slice 1, with the `ServiceTask.Operation()` accessor | §4.6 |
| 3 | any unmapped in-`bpmn` element errors | `documentation` / `extensionElements` skip silently | §FR-7 |
| 4 | per-element XML structs (`StartEvents []xmlNode`, …) | one `xmlNode` + `Elements []any`, preserving document order | §3.2 |
| 5 | `conditionExpression` as a string attribute | a child element carrying chardata + `id` + `language` | §3.2, Q2 |

### Empirical findings

- **The diff-coverage gate earns its keep.** Moving the parser in-tree put it
  under `cover-check`, which immediately exposed `interfacesXML` and half of
  `setServiceTaskAttrs` as unreachable — the dead `operationRef` export path of
  §4.6. A separate module would have kept that invisible and shipped a
  silently lossy round-trip.
- **`xml.Encoder.Flush` after `Encode` is dead code.** `Encode` ends with
  `enc.p.w.Flush()`, so a following explicit `Flush` can only ever drain an
  empty buffer. The redundant block was removed.
- **`UserTask` needs a placeholder output.** gobpm requires at least one output
  resource parameter, so an imported `<userTask>` gets a synthesized optional
  output that is **not** written back on export. Asymmetric but semantically
  neutral; see the backlog below.
- Diff coverage at landing: **96.3 % of 830 changed lines** (min 95 %).
  `pkg/convert` 100 %, `pkg/convert/bpmn` 92.5 % of statements.

### Test-name mapping (§6 → shipped)

`TestConvertRegistry` kept its name. The BPMN cases dropped the redundant
`BPMN` infix, since they live in `pkg/convert/bpmn`:
`TestBPMNImportMVP` → `TestImportSubset`, `TestBPMNExportMVP` → `TestExportMVP`,
`TestBPMNRoundTrip` → `TestRoundTrip` (+ `TestWorkedExample` for the §6
fixture), `TestBPMNPreservesID` → `TestPreservesID`,
`TestBPMNUnsupportedElement` → `TestImportInvalidFixtures` +
`TestExportUnsupportedNode`, `TestBPMNImportRegisterRun` → `TestImportRegisterRun`.

### Backlog (next slices)

1. **Operation message binding** — `inMessageRef`/`outMessageRef` → `bpmncommon.Message`, and re-emitting them on export (§4.6).
2. **Non-text conditions** — export currently refuses a condition with no source text (Q2); revisit when a compiled expression engine can round-trip one.
3. **`UserTask` placeholder output** — either relax the ≥1-output rule in the model or promote the workaround to a documented rule.
4. **`FuzzImport`** on the untrusted-XML boundary.
5. **MIWG conformance corpus** (Q1) once the element set grows past the MVP.
6. **Next element slices**, in ADR-024's order: message/timer intermediate events → inclusive and event-based gateways → boundary events and sub-process → send/receive tasks.
7. **Per-module coverage profiles** so `runtime/` and `adapters/*` reach `cover-check` too — the gap this SRD sidestepped by staying in the root module (§4.1). Not converter work: tracked in [docs/backlog.md](../backlog.md) under un-homed items.

## Open questions — all resolved at landing

1. **Worked-example fixture source — RESOLVED: hand-authored.** The §6 snippet
   plus eight files under `pkg/convert/bpmn/testdata/` (three valid, five
   invalid) — the repo's first `testdata/` directory. An MIWG corpus is
   backlog item 5: it only pays off once the element set outgrows the MVP.
2. **`conditionExpression` language — RESOLVED: recorded.** The importer keeps
   the `language` attribute and the expression `id` alongside the body on an
   inert `formalExpression`, and export writes all three back, so the attribute
   round-trips. Nothing *evaluates* it — that stays the expression engine's
   job — but discarding it would have made the round-trip lossy for every
   XPath-flavoured file.
3. **Module name (inherited ADR-024 Q1) — WITHDRAWN.** There is no module to
   name: the converter is `pkg/convert/bpmn` (§4.1). The SAD-001 §9
   `doc-source/` reservation is retired rather than renamed.
