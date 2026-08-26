# ADR-039 — Standard artifacts: Association, Group and Text Annotation as model-only carriers

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-08-25 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-024 v.5](ADR-024-process-interchange-converters.md) §2.9 (the artifact rows of the import-disposition table), [SAD-001 v.1.2](SAD-001-vision-and-architecture.md) §14 (the conformance scope; its model-only carrier statement lands with this record) |

> **Scope.** BPMN's §8.4.1 artifacts — **Association, Group, Text
> Annotation** — become elements the model *carries*: held for BPMN loading
> and round-trip, never executed. This is the model capability
> [ADR-038 v.2](ADR-038-converter-coverage-boundaries.md) §2.3 registers for
> the plain `<association>` (#323), delivered per its §2.4 as the
> capability's own decision record; the accompanying SRD lands it. Nothing
> decided here runs.

## 1. Context

*"BPMN provides three standard Artifacts: Associations, Groups, and Text
Annotations"* (§8.4.1) — the modeler's way of *"showing additional
information about a Process that is not directly related to the Sequence
Flows or Message Flows"* (§8.4.1). They are representation, not semantics:

- *"An Association is used to associate information and Artifacts with Flow
  Objects"*, in particular *"to connect user-defined text (an Annotation)
  with a Flow Object"*; the same element *"is also used to show the Activity
  used for compensation"* — the one shape with execution meaning (§8.4.1).
- *"Text Annotations are a mechanism for a modeler to provide additional
  information for the reader of a BPMN Diagram"* (§8.4.1).
- *"The Group object is an Artifact that provides a visual mechanism to
  group elements of a diagram informally"*; its optional `categoryValueRef`
  *"specifies the CategoryValue that the Group represents"*, owned by a
  Definitions-level `Category` root (§8.4.1).

`Process` and `SubProcess` each carry `artifacts 0..*`
([elements/process.md](../bpmn-spec/elements/process.md),
[elements/activities.md](../bpmn-spec/elements/activities.md)).

The engine's posture is two-sided. §2.3.1 lets execution ignore
non-operational elements — artifacts have no token semantics. But §2.3.2
obliges support for **import of Process diagrams**, and
[ADR-024 v.5](ADR-024-process-interchange-converters.md) §2.8 guarantees a
**semantic round-trip**, which can only re-emit what the model holds. The
model holds none of the family, and the cost is recorded at the converter's
edge: [ADR-038 v.2](ADR-038-converter-coverage-boundaries.md) §1.3 — the
annotation is skipped, the line to it is refused, and an ordinary annotated
diagram fails to load.

## 2. Decision

### 2.1 The model carries the standard's three artifacts, for loading

`Association`, `TextAnnotation` and `Group` become model elements under the
**model-only contract**: carried by the definition of the container that
declares them (`Process` or `SubProcess`, ordered — artifacts have no name,
and declaration order is what a round-trip reproduces), authorable in Go,
preserved through interchange, and **never executed**. No engine decision
reads an artifact; no artifact state exists on an instance.

**This tier exists for BPMN loading, not for execution**, and
[SAD-001](SAD-001-vision-and-architecture.md) §14 states that split at the
architecture level: artifacts sit with the §2.3.2 loading obligation, on the
opposite side of the table from the §2.3.1 semantics the engine animates.

### 2.2 The carried set is closed at the standard's three

The standard's `Artifact` is an abstract supertype with no properties of its
own — the model represents it as the classification of the three kinds, not
as a constructible element. §8.4.1 lets *"a modeler or modeling tool"* add
new artifact types; **engine note:** gobpm declines that extension point. A
carried element earns its place by being re-emittable, and the engine cannot
re-emit a kind it does not know. Admitting a kind is an amendment to this
record, not a plugin surface.

### 2.3 A Group carries its category value; the category catalog is load input

A `Group` is carried **with the `CategoryValue` it represents embedded** —
the value is the group's meaning (§8.4.1), and the model keeps no
Definitions-level root store to point into. At load, the document's
`<category>` declarations are read as the **resolution input** that inflates
a group's `categoryValueRef` into its value; at export they are re-emitted
from the carried values. `Category` itself is not a separately carried model
element. **Engine note:** the category membership of individual flow
elements (`FlowElement.categoryValueRef`) stays outside the carried set —
execution ignores categories, and the group artifact is what the diagram
states.

### 2.4 The compensation association is not an artifact

The tag's second meaning — *"the Activity used for compensation"* (§8.4.1) —
is execution semantics and already has its model representation: the
compensation wiring of [ADR-026 v.1](ADR-026-compensation-events.md), the
boundary-attached handler and its resolved handler reference. **One document
fact gets one model representation.** A consumed compensation association is
not additionally materialized as an artifact — a second copy could disagree
with the first — and future export re-emits it from the wiring.

### 2.5 The standard's rules on ends and direction, and no invented ones

An association's two ends are references to model elements, both mandatory
(`sourceRef`/`targetRef` are schema-`required`, §8.4.1); its direction is
`None | One | Both` with the standard's default `None` (§8.4.1). Beyond
that the standard is **silent** — no containment rule, no end-type rule (an
end is any `BaseElement`) — and the engine invents none: a modeler may
legally associate a process-level annotation with a node inside a
sub-process, and a stricter rule would refuse conformant files.

### 2.6 The converter follows the model

The import dispositions consume the capability (refining
[ADR-024 v.5](ADR-024-process-interchange-converters.md) §2.9's artifact
rows): `<textAnnotation>`, `<group>` and the plain `<association>` are
**mapped** — parsed and preserved — in every context a container reads
children, process-level and nested alike; `<category>` is consumed per §2.3.
A reference the model cannot resolve — an association end or a
`categoryValueRef` naming nothing the model holds — **degrades that one
artifact to the ADR-024 §2.14 report**: the file loads, the artifact is
dropped, the host is told which reference failed. Never a refusal — refusing
a file for a comment's line is the failure this capability ends — and never
silence, because an artifact states something and ADR-024 §2.9 reserves
silent skipping for content whose absence changes nothing. The compensation
shape keeps its existing reading (§2.4).

### 2.7 Export owes re-emission, on the export schedule

Round-trip is the reason to carry (§1), so export owes re-emission of
everything carried — text, format, direction, ends, category values. That
debt is scheduled with the export slice of the interchange work, beside the
same debt lanes already carry; this record fixes the shape those exports
will read and does not advance the schedule.

## 3. Consequences

- **An annotated diagram loads.** The ADR-038 §2.3 register row for #323 is
  consumed, and the register's "file refused for a comment" cost paragraph
  retires with it.
- **The model authors what it loads** — annotations, groups and their links
  can be built in Go and survive to interchange.
- **No execution surface appears.** Like lanes: no runtime type reads an
  artifact, so "never executed" stays checkable by the absence of any path.
- **Export debt is explicit** (§2.7): carried artifacts, like carried lanes,
  are held but not yet re-emitted until the export slice lands.

## 4. Alternatives considered

**Report-and-drop instead of carrying** (no model change; every plain
association becomes a §2.14 report entry). Rejected: it fails both loading
obligations — stripping every comment is not §2.3.2's "support of import",
and ADR-024 §2.8 cannot re-emit what was never stored — and it leaves the
model unable to author an annotated definition.

**Carry Association and TextAnnotation only** (the first draft of this
record). Rejected: it splits one standard family across two dispositions,
leaves a group-ended association permanently degraded, and re-opens the same
decision the first time a grouped diagram matters. The category machinery a
Group needs is bounded by §2.3's embed-the-value rule.

**A converter-local artifact type.** Rejected without new argument:
[ADR-038 v.2](ADR-038-converter-coverage-boundaries.md) §2.1 forbids the
converter compensating for a missing model capability.

**An open, host-extensible artifact set** (§8.4.1's "MAY extend"). Rejected:
the engine cannot re-emit a kind it does not know; the standard offers that
point to modeling tools, and this engine is not one (§2.2).

## 5. Open questions

None. The one deliberate deferral — export re-emission — is scheduled in
§2.7, not open.

## 6. References

- [ADR-024 v.5](ADR-024-process-interchange-converters.md) — the disposition
  table §2.6 refines; the §2.14 report contract it reuses.
- [ADR-038 v.2](ADR-038-converter-coverage-boundaries.md) — the capability
  register this answers and the §2.4 ordering it obeys.
- [ADR-026 v.1](ADR-026-compensation-events.md) — the compensation wiring
  that owns the tag's executable meaning (§2.4).
- [SAD-001 v.1.2](SAD-001-vision-and-architecture.md) §14 — the conformance
  posture; its loading-not-execution statement lands with this record (§2.1).
- [conformance.md](../bpmn-spec/conformance.md) — the vendored scope extract
  whose visual-artifacts row this record re-classes.
- BPMN 2.0.2 §8.4.1 *Artifacts* — all standard-claims; quotes verified
  against the OMG specification text (pp. 64–71).

## Document History

| Version | Date | Author | Changes |
|---|---|---|---|
| v.1 | 2026-08-25 | Ruslan Gabitov | Initial decision. The model carries **the standard's three artifacts** — Association, TextAnnotation, Group — under the lanes-style model-only contract: held on `Process`/`SubProcess` for **BPMN loading and round-trip** (§2.3.2, ADR-024 §2.8), never executed (§2.3.1), never on an instance; SAD-001 §14 states the loading-not-execution split. The set is **closed** (§8.4.1's tool-extension point declined — an engine cannot re-emit a kind it does not know). A **Group embeds the CategoryValue it represents**; the Definitions-level `<category>` catalog is load-time resolution input, re-emitted from carried values, not a carried element. The **compensation association is not an artifact** (one fact, one representation — ADR-026's wiring owns it). Ends and direction follow §8.4.1 (both ends required, default direction `None`); **no containment rule is invented** where the standard is silent. Converter dispositions follow: map all three in every container context; an unresolvable reference degrades that one artifact to the ADR-024 §2.14 report — never a refusal, never silence. Export re-emission is an explicit scheduled debt (§2.7). |
