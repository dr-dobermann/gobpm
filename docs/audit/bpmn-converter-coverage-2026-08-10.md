# BPMN converter — element and Camunda-extension coverage audit — 2026-08-10

**Question asked:** can every BPMN element be loaded and unloaded, and which
Camunda extensions are supported?

**Subject:** `pkg/convert` (≈340 LOC) + `pkg/convert/bpmn` (≈1 660 LOC non-test) —
the `Importer`/`Exporter` seam of
[ADR-024 v.3](../design/ADR-024-process-interchange-converters.md) and its
BPMN 2.0 XML implementation ([SRD-051](../srd/SRD-051-bpmn-converter.md)).

**Baseline:** `10467bb5` (master).

**Method:** the source was read against the ADR-024 §2.6 fence first, then the
importer and exporter were **driven** by a throwaway probe — an element sweep at
all three nesting levels (`<definitions>` children, `<process>` children, flow-node
children), a stock Camunda 7 modeler file, a repeated-export determinism run, and
an export sweep over model types outside the subset. Every outcome recorded below
is a run, not a reading. The probe was deleted after the run; it asserted nothing,
it only logged what the converter answered.

---

## 1 Verdict

**No.** The converter carries **9 flow elements**, and the fence is deliberate
(ADR-024 §2.6 — "the MVP element subset"). What the sweep adds to that known
number is that the *behaviour inside the fence* has eight defects, six of which
are not "a later slice" but wrong answers on files the fence claims to accept.

**Camunda extensions: none, in either direction.** Not one attribute, not one
extension element, and — the part that matters — the drop is **silent**. A
Camunda file whose flow graph is inside the subset imports "successfully" and
arrives with its assignee, its candidate groups, its external-task topic and its
I/O mapping gone, with no error, no warning and no log line.

Five of those dropped Camunda values have a **home in the gobpm model already**
(§5) — the mapping is missing, not the target.

---

## 2 What loads — import sweep

Each element was fed to `Import` on its own, inside an otherwise valid process.
Three outcomes exist by design: **mapped**, **skipped silently**, **hard error**
(`*convert.UnsupportedElementError`).

### 2.1 Children of `<bpmn:process>`

| Element | Outcome |
|---|---|
| `startEvent` (none) · `endEvent` (none) | ✅ mapped |
| `task` · `manualTask` | ✅ mapped — **but only when it carries a `name`** (F2) |
| `userTask` | ✅ mapped — **only when named** (F2) |
| `serviceTask` (+ `operationRef`) | ✅ mapped |
| `exclusiveGateway` (+ `default`) · `parallelGateway` | ✅ mapped |
| `sequenceFlow` (+ `conditionExpression`) | ✅ mapped |
| `documentation` · `extensionElements` | ⚪ skipped silently — content discarded (F7, F9) |
| `sendTask` · `receiveTask` · `scriptTask` · `businessRuleTask` · `callActivity` | ❌ error, §13.3.3 |
| `subProcess` · `adHocSubProcess` · `transaction` | ❌ error, §13.3.4 |
| `inclusiveGateway` | ❌ error, §13.4.3 |
| `eventBasedGateway` · `complexGateway` | ❌ error, §13.4 |
| `intermediateCatchEvent` · `intermediateThrowEvent` | ❌ error, §13.5 |
| `boundaryEvent` | ❌ error, §13.5.5 |
| `laneSet` | ❌ error, §10.5 (F8) |
| `dataObject` · `dataObjectReference` · `dataStoreReference` · `property` · `ioSpecification` | ❌ error, §10.3 |
| `textAnnotation` · `association` · `group` | ❌ error, **no § pinned** (F6) |

### 2.2 Children of `<bpmn:definitions>`

| Element | Outcome |
|---|---|
| `process` (the first one) · `interface`/`operation` | ✅ mapped |
| `documentation` · `extensionElements` | ⚪ skipped silently |
| any **foreign namespace** (`bpmndi:` · `dc:` · `di:` · `camunda:` · `zeebe:`) | ⚪ skipped silently |
| `message` · `signal` · `error` · `escalation` · `itemDefinition` · `dataStore` · `category` · `resource` · `import` · `relationship` | ❌ error |
| `collaboration` | ❌ error, §10.1 |
| a **second** `process` | ❌ error |

The definitions-level catalog rows are the practically important ones: a modeler
file that declares a `<bpmn:message>` or a `<bpmn:error>` fails **before** the
flow graph is looked at, even if the graph itself is entirely inside the subset.

### 2.3 Children of a flow node

| Element | Outcome |
|---|---|
| `incoming` · `outgoing` | ⚪ skipped (redundant with `sequenceFlow` wiring) |
| `documentation` · `extensionElements` | ⚪ skipped silently |
| every event definition (`message` · `timer` · `error` · …) | ❌ error, §13.5 |
| `multiInstanceLoopCharacteristics` · `standardLoopCharacteristics` | ❌ error, §13.3.5 |
| `ioSpecification` · `property` · `dataInputAssociation` | ❌ error, §10.3 |
| `potentialOwner` · `humanPerformer` · `resourceRole` · `script` | ❌ error, no § pinned |

### 2.4 Attributes silently ignored inside the fence

Import reads only unprefixed attributes and only the ones it maps. These were
accepted and discarded without a word: `isExecutable`, `isClosed`, `processType`,
`isInterrupting`, `parallelMultiple`, `isForCompensation`, `startQuantity`,
`completionQuantity`, `isImmediate`, and `implementation` on `serviceTask`
(F3, F4).

---

## 3 What unloads — export sweep

Export dispatches on the Go type of each node. The mapped set is **7 model
types**:

`events.StartEvent` · `events.EndEvent` · `activities.ManualTask` ·
`activities.UserTask` · `activities.ServiceTask` ·
`gateways.ExclusiveGateway` · `gateways.ParallelGateway`, plus
`flow.SequenceFlow` and a synthetic `<bpmn:interface>` catalog.

Everything else — `SubProcess`, `CallActivity`, `Transaction`, `ScriptTask`,
`BusinessRuleTask`, `SendTask`, `ReceiveTask`, all three remaining gateways,
every intermediate and boundary event — aborts the export with
`UnsupportedElementError` carrying the **Go type name** (`"*activities.SubProcess"`),
not a BPMN tag and no spec §.

Set against [conformance-status.md](../design/conformance-status.md), which
records the executable element set as complete, the export side covers
**7 of the ~20** node types the engine can actually run. A process built with the
library's own documented features is, in the general case, not exportable.

Export also never emits: `<bpmn:documentation>`, `<bpmn:incoming>`/`<outgoing>`,
any Diagram Interchange, lanes, data elements, or `isExecutable="false"`.

> **Consequence worth stating plainly.** No DI is emitted, and bpmn-js — which
> Camunda Modeler and bpmn.io are built on — renders from DI. An exported file
> therefore opens without a diagram; it is an interchange artifact for engines,
> not a file to hand back to a modeler. ADR-024 §2.8 accepts this for MVP; it is
> restated here because "export" reads like "round-trip to the tool it came from".

---

## 4 Round-trip asymmetries

| Item | Import | Export | Net effect |
|---|---|---|---|
| `<bpmn:task>` vs `<manualTask>` | both → `ManualTask` | always `<task>` | spelling normalised (documented, ADR-024 §2.8) |
| `serviceTask@implementation` | **ignored** | written | export-only attribute — survives no round-trip (**F4**) |
| `parallelGateway@default` | ignored | **written** | export emits an attribute BPMN does not define (**F5**) |
| `process@isExecutable` | ignored | hard-coded `true` | `false` silently becomes `true` (**F3**) |
| `<bpmn:documentation>` | skipped | never written | documentation lost both ways (**F9**) |
| `operation` `inMessageRef`/`outMessageRef` | parsed into `opSpec` | never written | parsed and discarded (known, ADR-024 §2.6 v.2 note) |
| `<bpmn:interface>` identity | id + name collected | one synthetic `<processID>-services` | original interface id/name lost |
| `definitions@id`, `@targetNamespace` | ignored | synthesised | document envelope not preserved |
| element order | document order recorded | **map order** | output varies run to run (**F1**) |

---

## 5 Camunda extensions

Nothing named `camunda:` is read or written. `extensionElements` is skipped
wholesale (`bpmn.go:48` `isSkippableAnnotation`) and `attrValue` matches only
unprefixed attributes (`importer.go:1004`), so prefixed attributes never reach a
mapping. The probe on a stock Camunda 7 file confirms it: import succeeded,
re-export contained the string `camunda` **zero** times.

The third column is what makes this actionable — several of these have a target
in the model *today*:

| Camunda 7 extension | Supported | Model home that already exists |
|---|---|---|
| `camunda:assignee` | ❌ silent drop | `activities.WithAssignee` / `WithAssigneeExpr` |
| `camunda:candidateUsers` | ❌ silent drop | `activities.WithCandidateUsers(Expr)` |
| `camunda:candidateGroups` | ❌ silent drop | `activities.WithCandidateGroups(Expr)` |
| `camunda:type="external"` + `camunda:topic` | ❌ silent drop | `activities.WithWorker(topic)` |
| `camunda:failedJobRetryTimeCycle` | ❌ silent drop | `activities.WithRetryPolicy` (shape differs) |
| `camunda:priority` (user task) | ❌ silent drop | `taskPriority` — setter is a registered 📐 extension (ADR-020 v.3 §2.11) |
| `camunda:inputOutput` (`inputParameter`/`outputParameter`) | ❌ silent drop | data associations exist; the converter maps no data at all |
| `camunda:formKey` · `camunda:formData`/`formField` | ❌ silent drop | partial — `hinteraction.Renderer` is Go code, not a form URL |
| `camunda:resultVariable` | ❌ silent drop | loosely `WithOutputMapping` / `WithStatus` |
| `camunda:class` · `delegateExpression` · `expression` · `camunda:script` | ❌ silent drop | none — JVM-specific, correctly out of scope for a Go engine |
| `camunda:executionListener` · `camunda:taskListener` | ❌ silent drop | none |
| `camunda:asyncBefore` · `asyncAfter` · `exclusive` · `jobPriority` | ❌ silent drop | none — gobpm has no job-executor concept |
| `camunda:properties`/`property` | ❌ silent drop | none |
| `camunda:versionTag` · `camunda:historyTimeToLive` | ❌ silent drop | none (ADR-019 keys versions on the process id) |
| `camunda:in`/`out` (call activity), `camunda:errorEventDefinition` | ❌ silent drop | element itself outside the subset anyway |

**Zeebe / Camunda 8** (`zeebe:taskDefinition`, `zeebe:ioMapping`, …): identical —
foreign namespace, skipped silently, nothing mapped.

ADR-024 §6 already names *"extension-element passthrough"* and *"dialect
targeting on export (Camunda/Zeebe namespaces)"* as future work, and §7 puts
passthrough in slice 2+. So the **absence** is planned. The **silence** is not
(F7).

---

## 6 Defects

Ordered by how much damage they do to a user who stays inside the declared fence.

**F1 — export element order is non-deterministic.** `Process.Nodes()` and
`Flows()` range over `map[string]…` (`process.go:38-39,199,240`), and the
exporter appends in that order. Two `Export` calls on the *same* process produce
different documents; the shipped `examples/bpmn-convert` prints a different
element order across runs (observed 2 orderings in 5 runs). Exported `.bpmn`
files cannot be diffed or version-controlled, and any golden-file test on them
is flaky. The exporter already sorts operation ids "for deterministic export"
(`exporter.go:245-250`), so determinism is the intent — nodes and flows were
missed. **Route: FIX-track**, small (sort by id, or record document order).

**F2 — a task with no `name` cannot be imported.** BPMN `name` is optional
(0..1), and modelers emit unlabelled boxes routinely. `parseNode` passes the raw
name to `NewManualTask`/`NewUserTask`, which reject an empty one — so
`<bpmn:task id="x"/>` fails with *"Activity name couldn't be empty"*. The same
importer already falls back to the id for `<process>` (`importer.go:227-230`) and
for `serviceTask` (`importer.go:522-524`); the three task kinds were missed.
**Route: FIX-track**, small (same id fallback).

**F3 — `isExecutable` is ignored on import and hard-coded `true` on export.**
`isExecutable="false"` marks a non-executable (descriptive) process; importing it
yields an executable definition, and exporting writes `true` back
(`exporter.go:190`). A descriptive pool becomes a runnable process silently. The
right answer is a decision — reject, or carry the flag — not a default.
**Route: design-backlog** (small, but it is a semantic choice, and ADR-024 §3
notes the attribute exists).

**F4 — `serviceTask@implementation` is export-only.** The exporter writes it
(`exporter.go:323-325`), the importer never reads it (probe: XML said
`##WebService`, model reported `##unspecified`). ADR-024 §3 already records
`implementation` as a spec string hint, so the parse side is simply missing.
**Route: FIX-track**.

**F5 — export can emit a schema-invalid `parallelGateway`.**
`UpdateDefaultFlow` lives on the base `Gateway` (`gateway.go:174`), so a parallel
gateway can carry a default flow, and `setGatewayAttrs` writes `default` for
every gateway kind. Probe output: `<bpmn:parallelGateway id="g" default="f2">`.
BPMN §13.4.1 gives the parallel gateway no `default` attribute — ADR-024 §3 says
so in as many words. **Route: FIX-track** (guard on the exclusive branch; the
model-side question of why a parallel gateway accepts a default at all is a
separate, larger one).

**F6 — pure-notation artifacts are hard errors.** `textAnnotation`,
`association` and `group` carry zero execution semantics — they are the BPMN
equivalent of a comment — and they appear in most modeler output. They fail the
import with no spec § pinned (`sectionFor` has no entry). This is the same
argument that earned `documentation` and `extensionElements` their silent-skip
carve-out (ADR-024 §2.6). **Route: design-backlog** — it is a one-line change to
`isSkippableAnnotation`, but it moves the fence, so it belongs in the ADR.

**F7 — extension content is dropped with no signal whatsoever.** SAD-001 §5
asks for *"clear feedback on unsupported elements"*, and the converter delivers
it for unmapped flow elements only. Everything inside `extensionElements`, and
every prefixed attribute, vanishes without an error, a warning, a log line or a
count. A user migrating a Camunda 7 process gets a process that looks imported
and is missing its assignees and its I/O mapping. Passthrough needs a model
carrier (ADR-024 §6) and is genuinely slice-2 work — but **reporting** what was
dropped does not, and is the part that closes the §5 gap.
**Route: design-backlog** (the diagnostics channel is a contract decision:
error? a returned report? a logger on the seam?).

**F8 — `laneSet` is a hard import error although the model now carries lanes.**
SRD-076 landed `Lane`/`LaneSet` explicitly so a lane-bearing diagram could
survive import→export ([conformance-status.md](../design/conformance-status.md)
row 14 argues exactly that), and ADR-024 v.3's history row carries the same
consequence. The model half landed; the converter half did not, so the
round-trip that justified the work still does not exist. Planned as slice-2
(ADR-024 §7 "lanes (parse-and-preserve)"), but the tracker's wording reads as if
the gap were closed. **Route: already-planned slice + a doc-accuracy correction.**

**F9 — `<bpmn:documentation>` is lost in both directions** although
`foundation.Documentation` exists and every element carries `Docs()`. Skipped on
import, never written on export. **Route: FIX-track** (small) or fold into the
F7 slice.

---

## 7 What this means for a real file

A stock Camunda 7 / bpmn.io export imports **only if** all of the following hold:
a single `<process>`; no `<collaboration>`, no pools; no definitions-level
`message`/`signal`/`error`/`itemDefinition` catalog; no lanes; no text
annotations; only the nine subset elements; and every task carries a `name`.
Diagram interchange and `camunda:` content do not block the import — they are
discarded.

When it does import, it arrives stripped of all vendor configuration, and what
comes back out is a differently-ordered, diagram-less document.

---

## 8 Routing

| # | Finding | Route |
|---|---|---|
| F1 | Non-deterministic export order | FIX-track |
| F2 | Unnamed task rejected | FIX-track |
| F4 | `implementation` export-only | FIX-track |
| F5 | `default` on `parallelGateway` | FIX-track |
| F9 | `documentation` dropped both ways | FIX-track |
| F3 | `isExecutable` ignored / forced | design-backlog (semantic choice) |
| F6 | Notation artifacts are errors | design-backlog (moves the ADR-024 §2.6 fence) |
| F7 | Extension content dropped in silence | design-backlog (needs a diagnostics contract; passthrough needs a carrier) |
| F8 | Lanes do not round-trip | already-planned slice 2 + correct the tracker's row 14 wording |

F1, F2, F4, F5 and F9 are one FIX-sized change-set: they are all local to
`pkg/convert/bpmn`, none of them moves the fence, and each is a wrong answer
inside the subset the converter already claims.

## 9 Doc-accuracy notes

- [`docs/guides/extending/converters.md`](../guides/extending/converters.md)
  shows a sample export with elements in document order. That is one of several
  orderings the code can produce (F1); the sample should either follow the fix or
  say the order is unspecified.
- [`conformance-status.md`](../design/conformance-status.md) row 14 reads as
  though landing `Lane`/`LaneSet` closed the import→export argument it makes.
  The model carries lanes; the converter still rejects `<laneSet>` (F8).

## See also

- [ADR-024 — Process interchange converters](../design/ADR-024-process-interchange-converters.md) — the fence (§2.6), the semantic-round-trip choice (§2.8), the deferred passthrough (§6, §7)
- [SRD-051 — BPMN 2.0 converter](../srd/SRD-051-bpmn-converter.md) — the landing record for the MVP subset
- [BPMN conformance status](../design/conformance-status.md) — §4, the XML-interchange tier
- [Interchange converters guide](../guides/extending/converters.md)
