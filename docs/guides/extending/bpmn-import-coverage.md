# BPMN import coverage — what is refused, and what to do instead

The importer maps the executable element set — the "In scope" list of
[conformance.md](../../bpmn-spec/conformance.md), enumerated by family in
[Interchange converters](converters.md#what-the-importer-maps). What that
guide's [refusal outcome](converters.md#what-is-skipped-and-what-fails)
does not list is the constructs behind it, and the two questions a refused
file raises: is the refusal waiting on something, and what do I do today.

Every refused construct is one of two kinds ([ADR-024 §2.16](../../design/ADR-024-process-interchange-converters.md)):

- **Capability-blocked** — legal BPMN the engine could execute, refused
  because `pkg/model` lacks a capability. The refusal names the capability
  and the issue tracking it; the same file imports unchanged once that
  lands. Until then the alternative is to build that piece in Go.
- **Standing** — the engine will not accept it, and not for want of work:
  the constructor takes a Go value no document can carry, or the project
  has decided against the mechanism. The refusal says what to do instead
  and never says "yet".

The unit is the construct, not the tag: a `<transaction>` imports whole,
and what its `method` names is judged at registration, not at import — see
the note under the standing table.

## Capability-blocked

Tracked one issue per row under the import epic
[#335](https://github.com/dr-dobermann/gobpm/issues/335). A row leaves this
table when its capability lands.

| Construct | Why it is refused | Do this instead today | Tracking |
|---|---|---|---|
| `<callActivity calledElement="…">` naming anything but a literal process key; the `globalTask` family | resolving reuse-by-reference needs a registry of callable definitions ([ADR-024 §2.13](../../design/ADR-024-process-interchange-converters.md)) | call a process by its key; inline a global task's body as a task | [#325](https://github.com/dr-dobermann/gobpm/issues/325) |
| `<assignment>`, `<transformation>`, or a data association with several sources | the scope-routed copy path evaluates no expression (SRD-063 §10.3's follow-up) | align the two ends' `itemDefinition`s and copy plainly, or map through a script task before or after the activity | [#328](https://github.com/dr-dobermann/gobpm/issues/328) |
| a `<property>` as a data-association end | the model's `Property` has no `Associate*` | use a `<dataObject>` for data that flows between nodes | [#331](https://github.com/dr-dobermann/gobpm/issues/331) |

## Standing

Not defects; do not file them.

| Construct | Why the engine will not take it | Do this instead |
|---|---|---|
| `<complexGateway>` | BPMN activates it by an `activationCondition` expression; the engine by per-incoming-flow token counts. Recovering counts from an arbitrary Boolean is not mechanical, and guessing changes *when* the gateway fires | build it programmatically with `WithActivationThreshold` or the activation triples |
| `<adHocSubProcess>` | entered by a host-supplied `adhoc.Router` — a Go value deciding which activities run and in what order (ADR-035 §2.1); a file carries only a completion condition | build it programmatically with `activities.WithAdHoc` |
| a second `<inputSet>` / `<outputSet>` per direction | the engine models one set per direction | one set; several flavours of I/O are several tasks |
| a collection `<itemDefinition>` whose `structureRef` does not resolve | the element type comes from an external XSD/WSDL the converter neither has nor fetches; a collection of an unknown type is a shape, not a value — the item imports as a single empty record and the loss is reported | build the collection's item in Go if it matters |
| the Choreography and Conversation families | separate conformance classes: a Choreography is not a Process, and dropping one silently would import a different diagram than the one drawn | model the process each participant runs |

**Not a refusal at all: a transaction's `method` and `protocol`.** Both
import verbatim onto the model (`Transaction().Method()` / `.Protocol()`);
`compensate`, `##Compensate` and the absent attribute all mean the built-in
coordinator, and any other value — `store`, `image`, a URI — is carried and
then refused by `RegisterProcess` as *no transaction coordinator is
registered for this method*, the same moment a script format nothing claims
is (ADR-028 §2.7). Model the undo as compensation handlers.

## Reading a refusal

A refused element yields a `*convert.UnsupportedElementError` naming the
tag, its id and the spec section, with the note above as its `Planned`
text; a refused attribute or association yields an error whose message
carries the same content. What is consumed rather than refused — a
`<messageFlow>`, a dialect attribute, a dropped `protocol` — lands in
`ImportDocument`'s `Dropped` report instead, and the file survives. The
wording is deliberate and tested (`pkg/convert/bpmn/refusalwording_test.go`):
a capability-blocked refusal names its capability, a standing one names the
programmatic route, and neither reads like the other.

## See also

- [Interchange converters](converters.md) — the seam, the mapped families, the skipped and carried elements
- Design: [ADR-024 — Process interchange: pluggable import/export converters](../../design/ADR-024-process-interchange-converters.md) §2.16 · [ADR-039 — Standard artifacts](../../design/ADR-039-standard-artifacts.md)
- The import epic: [#335](https://github.com/dr-dobermann/gobpm/issues/335)
