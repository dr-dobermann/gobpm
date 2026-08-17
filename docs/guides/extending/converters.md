---
title: Interchange converters
description: Import and export process definitions — BPMN 2.0 XML, or your own format.
---

# Interchange converters

gobpm builds a process definition programmatically — `process.New`, `Add`,
`flow.Link`. A **converter** lets a definition cross the boundary between that
in-memory model and an external interchange format, in both directions: read a
`.bpmn` file into a runnable `*process.Process`, or write a model back out.

The seam is format-agnostic. gobpm ships one batteries-included
implementation — BPMN 2.0 XML — and the same two interfaces let you register
XPDL, a JSON DSL, or a vendor dialect without touching core.

## The seam

| | |
|---|---|
| Package | `github.com/dr-dobermann/gobpm/pkg/convert` |
| Interfaces | `convert.Importer`, `convert.Exporter` |
| Registration | `convert.RegisterImporter` / `RegisterExporter` (or the `…AtInit` twins, from `init()`) |
| Consumed by | your host code — **not** the engine |
| Built-in | [`pkg/convert/bpmn`](../../../pkg/convert/bpmn) — BPMN 2.0 XML, both directions |

The registry is keyed by `convert.Format` and follows the `image.RegisterFormat`
idiom: a format package self-registers from `init()`, so a blank import turns it
on. The seam itself carries no serialization — it may not even import
`encoding/xml` (a depguard rule enforces this), which is what keeps it honest
as a *seam* rather than a BPMN façade.

The engine is not involved. `Import` hands you a `*process.Process`; you
register it with the thresher yourself. Nothing in `pkg/thresher` imports
`pkg/convert`.

## Using the BPMN converter

Blank-import the format, then call the façade:

```go
import (
	"github.com/dr-dobermann/gobpm/pkg/convert"
	_ "github.com/dr-dobermann/gobpm/pkg/convert/bpmn"
)

p, err := convert.Import(ctx, convert.BPMN, file)   // *process.Process
...
err = convert.Export(ctx, convert.BPMN, os.Stdout, p)
```

Without the blank import, `convert.Import` returns a self-identifying *unknown
format* error listing whatever **is** registered — never a silent failure.
`convert.Formats()` reports the same list.

### Identity is preserved, and it is the version key

Every BPMN `id` becomes the element's `foundation` identity; nothing is
auto-generated. That matters beyond tidiness: gobpm keys a definition's version
lineage on the **process id**, so re-importing an edited file and registering it
produces v2 of the same definition rather than a second singleton. A flow
element with a missing or blank `id` is a hard import error.

### What the current slice maps

| BPMN element | Model target |
|---|---|
| `<bpmn:process>` | `process.New(name, WithID(id))` |
| `<bpmn:startEvent>` / `<bpmn:endEvent>` (none) | `events.NewStartEvent` / `NewEndEvent` |
| `<bpmn:task>` / `<bpmn:manualTask>` | `activities.NewManualTask` |
| `<bpmn:userTask>` | `activities.NewUserTask` |
| `<bpmn:serviceTask>` (`operationRef`, `implementation`) | `activities.NewServiceTask` (+ `WithImplementation`) |
| `<bpmn:interface>` / `<bpmn:operation>` | `service.NewOperation` (catalog stub) |
| `<bpmn:sequenceFlow>` (+ `conditionExpression`) | `flow.Link` (+ `flow.WithCondition`) |
| `<bpmn:exclusiveGateway>` (+ `default`) | `gateways.NewExclusiveGateway` |
| `<bpmn:parallelGateway>` | `gateways.NewParallelGateway` |
| `<bpmn:documentation>` (on any of the above) | `foundation.WithDoc` → `Docs()` |

An imported `serviceTask` is bound to an operation **without an implementor** —
a catalog stub carrying the id and name from the definitions-level
`<interface>`/`<operation>`. The host supplies the real implementor after
import, before running.

**`name` is optional in BPMN, and the model does not always agree.** A
`<task>`, `<manualTask>` or `<userTask>` with no `name` imports with its **id**
as the name, because those constructors require one — the same fallback
`<process>` and `<serviceTask>` already used. It shows on the way out: such an
element re-exports carrying `name="<its id>"`. A start event, end event or
gateway accepts an empty name and keeps one, so nothing acquires a name it
never had.

### What is skipped, and what fails

Three outcomes:

- **Skipped silently** — diagram interchange (`bpmndi:*`, `dc:*`, `di:*`) and
  any other foreign-namespace subtree. Layout is not execution.
- **Skipped silently, despite being BPMN** — `<bpmn:extensionElements>`, the
  purely visual artifacts (`<textAnnotation>`, `<group>`, `<category>`), and
  `<relationship>`/`<import>`. All are common in modeler output and carry no
  execution semantics, so dropping them leaves the imported definition meaning
  the same thing — erroring on them would reject a runnable file for carrying a
  comment. `<association>` is **not** in this group: it carries compensation
  semantics, so it is reported until the converter maps it.
- **Reported** — a *flow element* in the BPMN namespace that this slice does
  not map yet (inclusive gateway, boundary event, sub-process, …) yields a
  `*convert.UnsupportedElementError` naming the tag, its id, and the spec
  section, so a modeler learns exactly what the engine will not run:

```go
var uee *convert.UnsupportedElementError
if errors.As(err, &uee) {
	log.Printf("%s (%s) is not supported yet — BPMN %s", uee.Tag, uee.ID, uee.Section)
}
```

### Conditions, and the language they are written in

An imported condition is **runnable**, not a carrier: it becomes the model's
text-expression kind, which the routed engine evaluates. Which language it is
written in is resolved from the expression's own `language`, else the
document's `expressionLanguage`, else the expression's own shape.

| The file says | The importer does |
|---|---|
| `language="gobpm:lite"` | passes it through |
| nothing, and the body is `${…}` | rewrites the JUEL into `gobpm:lite` |
| `expressionLanguage="…XPath"` but the body is `${…}` | rewrites it — the delimiters win, because that document is a Camunda file whose schema default nobody edited |
| FEEL, XPath, or anything else | **refuses the import**, naming the language |

Refusing may look harsh next to carrying the text along, but the alternative is
worse than it sounds: a condition the engine cannot evaluate imports cleanly,
registers cleanly, and faults at the first gateway that needs it — with nothing
pointing back at the file. The refusal happens where the file, the flow and the
language can all still be named.

The same rule governs a JUEL construct with no counterpart — a bean method, a
ternary, `empty`. See [Coming from JUEL](../data/expressions.md#coming-from-juel-a-camunda-diagram)
for the full list and what to write instead.

### Round-trip is semantic, not byte-for-byte

Import→export reproduces ids, node kinds, flows, conditions, gateway direction
and defaults, documentation (text and `textFormat`), and the `serviceTask`
`implementation` hint — not the original bytes. Diagram interchange is not
re-emitted (it was never parsed), attribute order and whitespace are the
encoder's, and a condition is written back from its recorded source text plus
its `language` URI. A condition with **no** source text — a compiled expression
from some future engine — is a classified export error rather than a silent
drop.

**Export is deterministic.** The model holds nodes and flows in maps, so the
exporter imposes an order rather than inheriting Go's randomized map iteration:
nodes are walked from the start events following outgoing flows in flow-id
order — the way a token travels the process — with anything unreachable
appended by id, and flows follow by id. Two exports of an unchanged process are
byte-identical, so an exported file can be diffed and version-controlled.

## Registering your own format

Implement either interface — or both; they register independently, so a
read-only or write-only format is fine:

```go
package xpdl

import (
	"context"
	"io"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

const XPDL convert.Format = "xpdl"

type importer struct{}

func (importer) Import(ctx context.Context, r io.Reader) (*process.Process, error) {
	// parse r, build the model with process.New / Add / flow.Link,
	// preserve source ids via foundation.WithID, and return the process.
}

func init() { //nolint:gochecknoinits // register-by-format-key, image.RegisterFormat idiom
	convert.RegisterImporterAtInit(XPDL, importer{})
}
```

Your package can live in your own module — the seam is public API. Register
both directions under the same `Format` and callers get symmetry for free;
register one and the other direction reports *unknown format*.

Rules the registry enforces on your behalf: a blank `Format` is rejected, a nil
implementation is rejected, and registering the same `(format, direction)`
twice is rejected. `RegisterImporter` returns those if you register at runtime.
`RegisterImporterAtInit` has no caller to return to, so it **records** the
failure against the format and `Import`/`Export` return it on first use — a
deliberate choice over panicking at load time: an embedder that never touches
your format is not killed by a converter it does not use, and one that does
gets the cause through the ordinary error path rather than a stack trace.

## Running it end to end

`examples/bpmn-convert/` bundles a `.bpmn`, imports it, registers it, runs it to
completion on a thresher, and exports it back:

```
imported process id="linear" name="Linear" nodes=3 flows=2
registered key="linear" version=1 (ADR-019 version key = BPMN process id)
instance completed: Completed
--- exported BPMN ---
<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="linear-definitions" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="linear" name="Linear" isExecutable="true">
    <bpmn:startEvent id="s1" name="start"></bpmn:startEvent>
    <bpmn:task id="t1" name="work"></bpmn:task>
    <bpmn:endEvent id="e1" name="done"></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"></bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"></bpmn:sequenceFlow>
  </bpmn:process>
</bpmn:definitions>
```

## See also

- Examples: `examples/bpmn-convert/`
- Related guides: [Service Task](../tasks/service-task.md) · [Custom Operation](operation.md) · [Custom expression engine](expression-engine.md)
- Design: [ADR-024 — Process interchange: pluggable import/export converters](../../design/ADR-024-process-interchange-converters.md) · [SRD-051 — BPMN 2.0 converter](../../srd/SRD-051-bpmn-converter.md) · [ADR-019 — Definition versioning](../../design/ADR-019-definition-versioning.md)
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/convert`
