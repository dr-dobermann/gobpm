# SRD-092 — The standard artifacts, carried: model tier + BPMN import

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-08-25 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-039 v.1](../design/ADR-039-standard-artifacts.md) (the model-only artifact tier: the carried three, the closed set, the category-embed rule, the compensation exclusion, the degradation-to-report disposition) |
| Upstream | [ADR-024](../design/ADR-024-process-interchange-converters.md) §2.16 (the #323 register row and the capability-lands-first ordering); [ADR-024 v.5](../design/ADR-024-process-interchange-converters.md) §2.8/§2.9/§2.14 (round-trip, the disposition rows, the `Dropped` contract); [SAD-001 v.1.2](../design/SAD-001-vision-and-architecture.md) §14 (the model-only carrier tier statement) |
| Related | [SRD-076](SRD-076-lanes-model-only-carrier.md) (the carrier pattern this mirrors), [SRD-089.E](SRD-089.E-bpmn-import-containers-and-lanes.md) (the compensation reading of `<association>`, preserved unchanged) |
| Closes | [#323](https://github.com/dr-dobermann/gobpm/issues/323) |

## §1 Background

[ADR-039 v.1](../design/ADR-039-standard-artifacts.md) decides that the
standard's three artifacts — `Association`, `Group`, `TextAnnotation`
(BPMN 2.0.2 §8.4.1) — are **carried** by the model under the lanes-style
model-only contract: held on `Process` and `SubProcess` for BPMN loading and
round-trip, never executed, never on an instance. This SRD reconciles that
decision with the codebase and lands both halves: the model tier, and the
import rows that consume it.

**What the converter does today.** Every `<association>` no compensation
boundary consumed is refused (`pkg/convert/bpmn/association.go:121`,
`refusePlainAssociations`), and the refusal names its own unblocking change:

> *"A plain association needs an artifact collection on the container and a
> constructor for `artifacts.Association`, neither of which this model has
> (#323) — the annotation it draws from is imported, the line is not."*

`<textAnnotation>` and `<group>` are skipped at process level and `<category>`
at Definitions level (`pkg/convert/bpmn/dispatch.go:163-165`) — but only
there: inside a `<subProcess>` container children settle under `ctxNode`
(`importer.go:1563-1573`), where no skip row exists, so a nested annotation or
group is **refused**, not skipped. The net effect is the issue's complaint: a
modeller who adds one comment to a supported diagram gets an
`UnsupportedElementError`.

**What the model has today** (verified against the working tree):

- `artifacts.Association` is a struct of exported, nil-tolerant fields with no
  constructor (`pkg/model/artifacts/association.go:21-26`): `Source`/`Target`
  are `*foundation.BaseElement`, and the package's own test
  (`association_test.go:62`) pins that nil ends are accepted.
- No `TextAnnotation` type exists anywhere in the repository.
- `artifacts.Artifact` is a bare concrete struct wrapping
  `foundation.BaseElement` (`artifact.go:26`) — the standard's *abstract*
  supertype reified as a constructible value, which nothing constructs.
- `artifacts.Group` exists (`artifact.go:60`): it embeds
  `foundation.BaseElement` and holds one `*CategoryValue`, which `NewGroup`
  builds from a category-name string with the group's own id
  (`artifact.go:79`). `Category`/`CategoryValue` exist too (`category.go`),
  with empty names/values substituted by placeholder constants.
- No package outside `pkg/model/artifacts` imports it (the only cross-package
  mention is the comment at `pkg/convert/bpmn/association.go:117`), so the
  package's shape is free to change.

**Normative shapes** (vendored extract,
`docs/bpmn-spec/scripts/bpmn-moddle.json`; `Artifact → BaseElement`, no own
properties; both containers carry `artifacts 0..*` —
`docs/bpmn-spec/elements/process.md:24`,
`docs/bpmn-spec/elements/activities.md:433`):

| Element | Own properties |
|---|---|
| `Association → Artifact` | `associationDirection [0..1]` (spec default `None`, §8.4.1), `sourceRef`/`targetRef: BaseElement (ref)` — both schema-`required` (§8.4.1) |
| `TextAnnotation → Artifact` | `text: String (elem) [0..1]`, `textFormat [0..1]`, default `"text/plain"` (§8.4.1) |
| `Group → Artifact` | `categoryValueRef: CategoryValue (ref) [0..1]` (§8.4.1) |
| `Category → RootElement` | `name [0..1]`, `categoryValue: CategoryValue (elem) 0..*` |

## §2 Requirements

### Functional

**FR-1 — `Artifact` becomes the interface, and the set is closed.**
`pkg/model/artifacts` replaces the bare `Artifact` struct with an `Artifact`
interface — `foundation.BaseObject` plus an unexported marker method —
implemented by `*Association`, `*TextAnnotation` and `*Group` (ADR-039 §2.2).
The struct, `NewArtifact` and `MustArtifact` are **removed**: the standard's
`Artifact` is abstract, a constructible bare artifact models nothing, and
nothing outside the package's own tests uses it. The unexported marker keeps
the set closed to exactly the kinds ADR-039 admits.

**FR-2 — `Association` gets its constructor.**

```go
func NewAssociation(
    source, target foundation.Identifyer,
    direction AssociationDirection,
    baseOpts ...options.Option,
) (*Association, error)
```

plus a `MustAssociation` twin. The struct's fields become unexported, with
`Source()`, `Target()`, `Direction()` accessors. Validation, per the
validate-all-params rule: a nil `source` or `target` is refused with a
self-identifying error (both ends are schema-`required`, §8.4.1); any
direction outside `None`/`One`/`Both` is refused; an empty direction defaults
to `None` — the standard's own default (§8.4.1; the vendored extract does not
record defaults, so this one is pinned to the spec text). The ends are typed
`foundation.Identifyer` — the interface, not `*foundation.BaseElement` —
because every model element satisfies it already, and SRD-076 §10.2.2
recorded what concrete-pointer typing costs.

**FR-3 — `TextAnnotation` exists.**

```go
func NewTextAnnotation(
    text, textFormat string,
    baseOpts ...options.Option,
) (*TextAnnotation, error)
```

plus `MustTextAnnotation`; accessors `Text()`, `TextFormat()`. Both
attributes are `0..1`, so an **empty text is accepted** (the
preserve-the-cardinality argument of SRD-076 FR-2), and an empty `textFormat`
defaults to `"text/plain"` — the spec's default (§8.4.1) and the same
treatment as `foundation`'s `Documentation`
(`pkg/model/foundation/base.go:13`).

**FR-4 — `Group` joins the interface, keeping its shape.** `Group` gains the
marker method and nothing else: its existing form — an embedded
`foundation.BaseElement` plus the `CategoryValue` it represents — is already
ADR-039 §2.3's embed-the-value rule. `NewGroup`'s empty-name substitution
(`UNDEFINED_CATEGORY_VALUE`, `category.go`) covers a `<group>` with no
`categoryValueRef`.

**FR-5 — containers hold artifacts.** `Process` and `SubProcess` each gain an
ordered `artifacts []artifacts.Artifact` collection, an
`AddArtifacts(arts ...artifacts.Artifact) error` method, and an `Artifacts()`
accessor returning a copy. `AddArtifacts` refuses a nil artifact (index in
the error details) and a duplicate id against the container's artifacts
(every artifact has one — `foundation.NewBaseElement` generates an id when
none is given, `base.go:99`). Ordered, not keyed: artifacts have no name, and
declaration order is what a round-trip reproduces. There is deliberately
**no `WithArtifacts` construction option** — see §4.3.

**FR-6 — never executed, never cloned.** Artifacts live on the definition
only: not copied by `SubProcess.Clone`
(`pkg/model/activities/subprocess.go:573` — which already drops `laneSets`),
absent from `snapshot.Snapshot` (`internal/instance/snapshot/snapshot.go:22-60`),
and read by no engine decision. Nothing under `internal/` or the execution
packages imports `pkg/model/artifacts` — the import-graph proof of
SRD-076 §10.3.

**FR-7 — import maps `<textAnnotation>` and `<group>` everywhere a container
reads children.** Parsers registered in `processParsers`
(`pkg/convert/bpmn/dispatch.go:402-414` `init` — the same registration that
makes `<dataObject>` work inside a `<subProcess>` with no second row) read
each element's `id` (claimed into the document ledger when declared), the
annotation's `textFormat` attribute and `<text>` child, the group's
`categoryValueRef`, and buffer specs carrying the declaring container. The
`{tagTextAnnotation, ctxProcess}` and `{tagGroup, ctxProcess}` skip rows
(`dispatch.go:163-164`) are removed. This also closes the latent
inconsistency of §1: nested annotations and groups stop being refused.

**FR-8 — `<category>` is consumed as resolution input.** A Definitions-level
parser replaces the `{tagCategory, ctxDefinitions}` skip row
(`dispatch.go:165`): it reads the category's `categoryValue` children (id,
`value`) into a document-level lookup, claiming declared ids. In pass 2 a
group's `categoryValueRef` resolves through that lookup and the group is
built as `artifacts.NewGroup(value, …)` — the value embedded, per ADR-039
§2.3; no `Category` model element is created. A `<group>` without a ref
builds with the empty name (FR-4).

**FR-9 — import builds every association no compensation boundary consumed.**
`assocSpec` (`pkg/convert/bpmn/association.go:18-24`) additionally records
its declaring container. In pass 2, after `linkFlow` (so sequence flows are
resolvable ends), a builder replaces `refusePlainAssociations`
(`importer.go:1652`): each unconsumed association resolves `sourceRef` and
`targetRef` against the assembly's built elements — flow nodes (`asm.byID`),
data elements (`asm.dataElems`), sequence flows, and the artifacts built by
FR-7/FR-8 — constructs `artifacts.NewAssociation` with the declared
direction, and attaches it to its declaring container via `AddArtifacts`.
The `UnsupportedElementError` refusal, its `#323` message and the
`refusalwording_test.go` row that pins them are retired.

**FR-10 — an unresolvable reference degrades that one artifact to a report.**
An association end or a `categoryValueRef` naming nothing the model holds —
a DI id, an undeclared id — drops **that artifact** with a `convert.Dropped`
entry naming the artifact and the unresolved reference (ADR-039 §2.6),
following the skip-then-report precedent of the link-event pairing
(`pkg/convert/bpmn/eventdefs.go:182-194`). The file imports; the host is
told. Never a refusal, never silence.

**FR-11 — compensation associations are consumed, not duplicated.** The
compensation reading (`compensationHandler`,
`pkg/convert/bpmn/association.go:70`) stays exactly as SRD-089.E landed it:
a consumed association becomes the boundary's handler wiring and is **not**
additionally materialized as an artifact (ADR-039 §2.4).

### Non-functional

**NFR-1 — zero behavioural change.** Every existing process registers,
executes and completes exactly as before; adding artifacts changes nothing
observable about execution (§6 T-13 proves it the SRD-076 way).

**NFR-2 — export-ready shape, export deferred.** Ids, text, format,
direction, end ids and category values are all reachable through accessors —
sufficient for the converter to export what it imported — but this SRD builds
**no export support**. The exporter today writes only the six-node MVP subset
and already silently omits lane sets, properties and data objects
(`pkg/convert/bpmn/exporter.go:246-292` reads exactly `Nodes()`, `Flows()`,
`Docs()`); artifact export joins that set and lands with the export half of
[#284](https://github.com/dr-dobermann/gobpm/issues/284), per ADR-039 §2.7.

**NFR-3 — no import cycles.** `artifacts` imports `foundation`, `flow` (it
already does, for `CategoryValue`) and `options`; `process` and `activities`
import `artifacts`. Nothing imports back; `flow` keeps its deliberate
non-import of `artifacts` (`pkg/model/flow/element.go:51-58`).

**NFR-4 — the diff-coverage gate passes at `COVER_MIN`**, every touched
function at 100%.

**NFR-5 — the API removal is total, not partial.** `Artifact` (struct),
`NewArtifact`, `MustArtifact` and the exported `Association` fields disappear
in one change-set with their tests rewritten, so no transitional state ships.

## §3 Models

### §3.1 `pkg/model/artifacts` (reshaped)

```go
// Artifact is one of the standard's three artifacts a container carries: an
// Association, a TextAnnotation, or a Group (ADR-039 §2.2). Artifacts are
// model-only (SAD-001 §14): parsed and preserved for BPMN loading, never
// executed. The marker keeps the set closed to this package's kinds.
type Artifact interface {
    foundation.BaseObject
    artifact()
}

// An Association links two model elements: typically a TextAnnotation to the
// element it annotates. The compensation shape of BPMN's <association> is
// NOT represented here — the model realizes it as the boundary event's
// handler (ADR-039 §2.4) — so an Association in this collection is always
// the plain, non-executable line.
type Association struct {
    foundation.BaseElement

    source    foundation.Identifyer
    target    foundation.Identifyer
    direction AssociationDirection
}

// A TextAnnotation is a modeller's comment. Carried for interchange, never
// read by the engine.
type TextAnnotation struct {
    foundation.BaseElement

    text       string
    textFormat string
}
```

`Group`, `Category`, `CategoryValue` and `AssociationDirection` keep their
shapes; `*Group` gains only the marker. Constructors follow the house shape —
`New*` returning an error with `Must*` twins — and the lanes error idiom:
self-identifying messages (`"NewAssociation: a nil source isn't allowed"`),
`errs.C(errorClass, …)`, base-element failures wrapped with
`errs.BulidingFailed` + `errs.E(err)`. The package's `errorClass`, commented
out since the package was written (`artifact.go:10`), is restored.

### §3.2 Container wiring

```go
// AddArtifacts attaches artifacts to the process. Artifacts are model-only
// carriers (ADR-039): held for BPMN loading, never executed, never cloned
// into an instance. A nil artifact and a duplicate id are refused.
func (p *Process) AddArtifacts(arts ...artifacts.Artifact) error

// Artifacts returns a copy of the process's artifact collection, in the
// order the artifacts were added.
func (p *Process) Artifacts() []artifacts.Artifact
```

The same pair on `*activities.SubProcess`; both store
`artifacts []artifacts.Artifact` beside `laneSets`, cloned on read.

No `Validate()` hook is added: unlike lanes, whose Table 10.135 carries an
explicit MUST to enforce, the standard states no containment or end-type
rule for artifacts, and ADR-039 §2.5 forbids inventing one. The
constructors' nil/enum checks are the whole invariant.

### §3.3 Importer shapes (`pkg/convert/bpmn`)

```go
// annotationSpec is a <textAnnotation> as read, buffered for pass 2 like
// every other spec: its container may not be constructible until then.
type annotationSpec struct {
    id, text, textFormat string
    // container is the id of the declaring container, "" for the process —
    // the same convention as nodeSpec (SRD-089.E §4.1).
    container string
}

// groupSpec is a <group> as read; categoryValueRef resolves in pass 2
// through the document's category-value lookup (FR-8).
type groupSpec struct {
    id, categoryValueRef, container string
}
```

`assocSpec` gains the same `container string` field. The assembly carries
`annots []annotationSpec`, `groups []groupSpec` and the parser a
document-level `categoryValues map[string]string` (id → value). Pass 2 gains
one builder, ordered after `linkFlow`:

```go
// buildArtifacts materializes the document's artifacts: every
// textAnnotation, every group (its categoryValueRef resolved to the value
// it embeds), then every association no compensation boundary consumed
// (FR-7…FR-9). A reference the model cannot resolve drops that one
// artifact with a report (FR-10) — the file survives.
func buildArtifacts(p *parser, asm *assembly) error
```

Sequence flows become resolvable by keeping the built `*flow.SequenceFlow`
per id when `linkFlow` runs — a small map on the assembly, populated where
the flows are created.

### §3.4 Worked example — the issue's failing case, end to end

The document (the exact shape `TestPlainAssociationIsRefused` uses today,
`pkg/convert/bpmn/association_test.go:137-166`):

```xml
<bpmn:process id="p1" name="annotated">
  <bpmn:startEvent id="s"/>
  <bpmn:userTask id="approve" name="Approve"/>
  <bpmn:endEvent id="e"/>
  <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="approve"/>
  <bpmn:sequenceFlow id="f2" sourceRef="approve" targetRef="e"/>
  <bpmn:textAnnotation id="note">
    <bpmn:text>Careful</bpmn:text>
  </bpmn:textAnnotation>
  <bpmn:association id="a1" sourceRef="approve" targetRef="note"/>
</bpmn:process>
```

Today: `ImportDocument` fails with `UnsupportedElementError{Tag:
"association", ID: "a1"}`. After this SRD:

```go
res, err := convert.ImportDocument(ctx, convert.BPMN, r)
// err == nil; res.Dropped is empty — nothing was lost
p := res.Processes[0]

arts := p.Artifacts() // 2 artifacts, in declaration order
ta := arts[0].(*artifacts.TextAnnotation)
// ta.ID() == "note", ta.Text() == "Careful", ta.TextFormat() == "text/plain"
a := arts[1].(*artifacts.Association)
// a.ID() == "a1", a.Source().ID() == "approve", a.Target().ID() == "note",
// a.Direction() == artifacts.None
```

The group path: add `<bpmn:group id="g1" categoryValueRef="cv1"/>` to the
process and `<bpmn:category id="c1"><bpmn:categoryValue id="cv1"
value="critical"/></bpmn:category>` under `<bpmn:definitions>` —
`p.Artifacts()` gains a `*artifacts.Group` whose
`CategoryValue.Value == "critical"`, and an association ending at `"g1"`
resolves.

And the FR-10 degradation: replace `targetRef="note"` with
`targetRef="missing"` (an id the document never declares). The file still
imports; `p.Artifacts()` holds the annotation; `res.Dropped` carries one
entry whose `Element` is `"a1"`, whose `Construct` is `"association"`, and
whose `Reason` names the unresolved `"missing"`.

## §4 Analysis

The concept-level choices — carried-not-dropped, the closed set, Group's
embedded value, the compensation exclusion, no invented containment rule,
degradation-to-report — are decided in [ADR-039 v.1](../design/ADR-039-standard-artifacts.md)
§2 and §4 and are not re-argued here. What follows is the reconciliation
with the codebase.

### §4.1 Why `Artifact` is an interface and the struct goes

The container collection needs a static type admitting exactly the artifact
kinds. `[]any` checks nothing; a slice per kind multiplies collections and
loses cross-kind declaration order; the closed interface matches the
standard's abstract supertype and lets `AddArtifacts` accept any admitted
kind. The bare struct it replaces was the abstract supertype reified as a
constructible value — nothing constructs one, and a value of it means
nothing.

### §4.2 Why `Group` keeps `NewGroup`'s shape, and what round-trip that buys

`NewGroup(categoryName, …)` builds the embedded `CategoryValue` with the
group's own id (`artifact.go:79`), so an imported group does not preserve
the document's categoryValue *id* — only its *value*. That is deliberate
and sufficient: ADR-024 §2.8's round-trip is **semantic**, not
byte-lossless, and the value is the group's meaning while the id is wiring
a future exporter regenerates (each group emitting its category value, or
deduplicating by value). Extending `NewGroup` to thread a foreign id
through to the embedded value would add API surface to preserve a datum
nothing consumes.

### §4.3 Why `AddArtifacts` is a method, not a construction option

Lanes attach at construction (`lanes.WithLaneSets`) because a lane set's
skeleton is complete before the process exists, its node references
resolving later through `Lane.Place`. An association is the opposite: it
*is* its resolved ends, and in the importer those ends are built nodes —
which exist only after `constructProcess` and `buildNodes` have run
(`importer.go:1614-1681`). A `WithArtifacts` option could never serve the
importer, and for a Go author `AddArtifacts` after construction is the same
one call an option would have been. One attachment surface beats two of
which one is decorative (`Process.Add` already establishes post-construction
mutation as the container's normal mode — `process.go:254`).

### §4.4 Why the category lookup is document-level, not per-assembly

`<category>` is a `RootElement`: it lives under `<bpmn:definitions>`,
outside any `<process>`, so its values must be visible to groups in every
process of a multi-process document. The parser already keeps exactly this
shape for `<itemDefinition>` and interfaces (the catalog); the category
lookup is the same pattern one size smaller — a map, not a model element,
because ADR-039 §2.3 carries the value inside the group and nothing else
ever reads a category.

## §5 API

| Symbol | Package | Change |
|---|---|---|
| `Artifact` | `artifacts` | **struct → interface** (breaking; nothing external imports the package) |
| `NewArtifact`, `MustArtifact` | `artifacts` | **removed** |
| `Association.Source/Target/Direction` (fields) | `artifacts` | **unexported** (breaking, same argument); accessors added |
| `NewAssociation`, `MustAssociation` | `artifacts` | new |
| `TextAnnotation`, `NewTextAnnotation`, `MustTextAnnotation`, `Text()`, `TextFormat()` | `artifacts` | new |
| `Group.artifact()` | `artifacts` | new (marker; shape unchanged) |
| `Process.AddArtifacts`, `Process.Artifacts` | `process` | new |
| `SubProcess.AddArtifacts`, `SubProcess.Artifacts` | `activities` | new |

Converter surface: no exported change. `refusePlainAssociations` is replaced
by `buildArtifacts`; the three skip rows become parsers/lookup; nothing else
moves.

## §6 Tests

| # | Test | Asserts | FR |
|---|---|---|---|
| T-1 | `TestNewAssociation` | valid ends accepted; nil source refused, nil target refused, each error self-identifying; empty direction defaults to `None`; a made-up direction refused; invalid base option propagated | FR-2 |
| T-2 | `TestNewTextAnnotation` | text and format carried; empty text accepted; empty format defaults to `text/plain`; invalid base option propagated | FR-3 |
| T-3 | `TestArtifactAccessors` | every carried field reads back; `Must*` twins panic on error, return on success | FR-2, FR-3 |
| T-4 | `TestArtifactInterface` | `*Association`, `*TextAnnotation`, `*Group` all satisfy `Artifact` (compile-time pins) | FR-1, FR-4 |
| T-5 | `TestProcessArtifacts` / `TestSubProcessArtifacts` | `AddArtifacts` accepts and accumulates in order; `Artifacts()` returns a copy, not an alias; a nil artifact refused with its index; a duplicate id refused | FR-5 |
| T-6 | `TestAnnotationAndAssociationImport` | the issue's failing case (§3.4) **imports**; the process carries both artifacts; ends resolve; direction carried | FR-7, FR-9 |
| T-7 | `TestArtifactsInsideSubProcess` | an annotation + group + association declared inside a `<subProcess>` land on the sub-process's collection, not the process's | FR-7, FR-9 |
| T-8 | `TestGroupImport` | a `<group>` with `categoryValueRef` builds with the resolved value; one without a ref builds with the placeholder; the category itself creates no artifact | FR-7, FR-8 |
| T-9 | `TestAssociationToSequenceFlow` | an association whose end is a `<sequenceFlow>` resolves | FR-9 |
| T-10 | `TestUnresolvableReferenceIsReported` | an association to an undeclared id, and a group with an undeclared `categoryValueRef`, import the file, build no artifact for the broken one, and `Dropped` names each artifact and its unresolved ref | FR-10 |
| T-11 | `TestCompensationAssociationNotDuplicated` | a compensation boundary's association produces the handler wiring and **no** artifact | FR-11 |
| T-12 | `TestMultiProcessCategoryResolution` | a category value referenced by groups in two `<process>`es resolves for both | FR-8 |
| T-13 | e2e `TestArtifactsDoNotAffectExecution` | the same process, bare and fully annotated (annotations, a group, associations, on process and sub-process), produces identical execution | NFR-1, FR-6 |
| T-14 | `TestArtifactsAreNotCloned` | the per-instance graph and the snapshot carry no artifact state; the definition still does | FR-6 |

Existing tests pinning today's behaviour are rewritten in the same milestone
that changes it: `TestPlainAssociationIsRefused`
(`association_test.go:137-166`), `TestAssociationIsNotAnAnnotation` and
`TestVisualArtifactsAreSkipped` (`dispositions_test.go:19-78`), and the
`"plain association"` row of `refusalwording_test.go:38-43`. The `artifacts`
package tests constructing the old struct literally are rewritten against
the new constructors (NFR-5).

T-13 is the load-bearing one, for the same reason as SRD-076 T-10:
model-only is a claim about behaviour, and only running the process both
ways asserts it.

## §7 Milestones

| M | Scope | Commit |
|---|---|---|
| M1 | `pkg/model/artifacts` reshape: interface, `NewAssociation`, `TextAnnotation`, `Group` marker, error class, package tests (T-1…T-4) | one |
| M2 | Container wiring: `AddArtifacts`/`Artifacts` on both containers; execution-invariance and non-cloning proofs (T-5, T-13, T-14) | one |
| M3 | Importer, carriers: `<textAnnotation>`/`<group>` parsers, the `<category>` lookup (T-8, T-12, the carrier half of T-7) | one |
| M4 | Importer, associations: `buildArtifacts` association half, dropped-report degradation, retired refusals and rewritten pins (T-6, T-9, T-10, T-11, rest of T-7) | one |

Doc sync (ADR-024 §2.9 rows, ADR-024 §2.16, `conformance.md`,
`conformance-status.md`, README sweep) follows as its own `docs:` commit at
the handover step, per the flow.

## §8 Cross-doc references

| Direction | Document | Why |
|---|---|---|
| up | [ADR-039 v.1](../design/ADR-039-standard-artifacts.md) | the decision this implements |
| up | [ADR-024](../design/ADR-024-process-interchange-converters.md) §2.16 | the register row this consumes and the lands-first rule this obeys |
| up | [ADR-024 v.5](../design/ADR-024-process-interchange-converters.md) §2.8, §2.9, §2.14 | the round-trip guarantee, the disposition rows this changes, the `Dropped` contract FR-10 reuses |
| up | [SAD-001 v.1.2](../design/SAD-001-vision-and-architecture.md) §14 | the model-only carrier tier statement |
| side | [SRD-076](SRD-076-lanes-model-only-carrier.md) | the carrier pattern |
| side | [SRD-089.E](SRD-089.E-bpmn-import-containers-and-lanes.md) | the compensation reading FR-11 preserves (a frozen one-shot; its §4.9 refusal is superseded by this landing, not retro-edited) |
| — | [conformance.md](../bpmn-spec/conformance.md) | the scope table whose visual-artifacts row re-classes with this landing |

No downward references.

## §9 Definition of Done

1. FR-1…FR-11 implemented and wired; NFR-1…NFR-5 held.
2. Every §6 test exists and passes, including T-13's execution-invariance
   proof; the retired refusal tests are rewritten, not deleted-and-forgotten.
3. `make ci` green (both halves), including `make gen_mock_files` if any
   mocked interface moved.
4. Diff-coverage at `COVER_MIN`; every touched function at 100%.
5. The doc-sync commit lands: ADR-024 §2.9 artifact rows updated, ADR-038
   §2.3 register row retired as landed, `conformance.md` visual-artifacts
   row re-classed (the three artifacts in scope, model-only;
   `Category`/`CategoryValue` consumed at load, not carried),
   `conformance-status.md` gains the second deliberate model-only entry,
   README swept.
6. [#323](https://github.com/dr-dobermann/gobpm/issues/323) closed by the
   landing PR; the [#335](https://github.com/dr-dobermann/gobpm/issues/335)
   checkbox ticked.

## §10 Implementation summary

### §10.1 Milestones as landed (branch `feat/artifact-carrier`)

| Commit | Milestone |
|---|---|
| `cea6efbc` | ADR-039 v.1 + SAD-001 v.1.2 (the decision this implements) |
| `0dff86dd` | this document |
| `4e009464` | **M1** — the artifacts package: interface, `NewAssociation`, `TextAnnotation`, markers |
| `230e1220` | **M2** — container wiring + the invariance and non-cloning proofs |
| `f770f9fd` | **M3** — importer carriers: annotation, group, category lookup |
| `6371c4bc` | **M4** — the association builder, the report degradation, the retired refusal |
| `ef2d31ee` | **M5** — the independent-review round (below) |

### §10.2 Where reality diverged from the draft

All five are test-naming, found by writing the tests rather than by review;
no behaviour differs from §2.

1. **T-3 dissolved into its neighbours.** A standalone
   `TestArtifactAccessors` would have re-read fields T-1/T-2 already assert;
   the accessor and `Must*` checks live in `TestNewAssociation` /
   `TestNewTextAnnotation` / `TestMustAssociation` / `TestMustTextAnnotation`.
2. **T-6 landed as `TestPlainAssociationImports`** — the rewrite of
   `TestPlainAssociationIsRefused` keeps the old test's fixture and its
   position in `association_test.go`, so the history of the refusal and its
   retirement reads in one place. `TestAnnotationImports` carries the
   annotation-only half.
3. **T-8 is `TestGroupImports`** (plural, matching `TestAnnotationImports`).
4. **T-9 folded into `TestAssociationEnds`** — one table over the whole
   resolution universe (flow, data object, group) beats three tests sharing
   a fixture.
5. **T-10 split by artifact kind** — `TestGroupUnresolvableRefIsReported`
   (M3, where groups are built) and `TestAssociationDanglingEndIsReported`
   (M4, covering both ends), because the two degradations land in different
   milestones.

Also worth recording: the `associationDirection` attribute turned out to be
**unread** by the pre-existing parser — the compensation reading never needed
it — so M4 is where the document's direction first reaches the model at all.

### §10.2a The independent-review round (M5)

Three doc-blind lenses (agy / gemini-3.1-pro-high) returned six notes; five
were agreed (one — the association-to-association gap — raised independently
by two lenses), two rejected with written reasons (the exported `Append`;
the invalid-direction refusal). The landed corrections:

- **A built association joins the artifact lookup**, so an association may
  end on an earlier one; a forward reference degrades to the report,
  document order being the resolution order.
- **Lanes and lane sets join the resolution universe** — ADR-039 §2.6
  degrades only what the model does *not* hold, and a lane is held.
- **Two SRD-089.E pins are superseded**, both resting on the mistaken
  premise that the model requires ids (it generates one when none is
  given): an id-less `<lane>`/`<laneSet>`/`<childLaneSet>` now imports
  under a generated id — the FR-4-style cardinality preservation, and the
  same convention as the artifacts — and declared lane/lane-set ids now
  join the document's one ledger, where before a lane could silently reuse
  a task's id. SRD-089.E stays frozen; the supersession is recorded here.
- **The direction constants renamed** to `DirectionNone`/`DirectionOne`/
  `DirectionBoth` (`artifacts.None` read as artifact cardinality at call
  sites); the enum is this branch's to reshape, since NFR-5 already
  reshaped the package's surface and nothing external imports it.
- `TestAssociationEnds` asserts sources as well as targets.

### §10.3 Verification

- `make ci` at `ef2d31ee` (all five milestones): **PASS, 14/14 steps in
  8m06s** (`.ci/last-run.json`), including the end-to-end example runs.
- Diff-coverage in that run: **98.6% of 296 changed coverable lines —
  PASS**; every touched function 100% except `errs.Invariant`
  continuation lines (`artifactHolderFor`, the same unreachable-guard
  residue `containerFor` ships with) and pre-existing error-wrap blocks
  the lane builder carries.
- FR-6's "never executed" is a property of the import graph: nothing under
  `internal/`, `pkg/thresher/` or `pkg/exec/` imports `pkg/model/artifacts`.
- The load-bearing proof is `TestArtifactsDoNotAffectExecution`: one process,
  bare and fully artifacted on both containers, completing identically.

## Open questions

None — the concept decisions live in ADR-039 v.1; the implementation
decisions a reviewer would ask about are recorded in §4.
