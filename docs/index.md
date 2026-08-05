---
title: gobpm documentation
description: Documentation for gobpm — an embeddable BPMN 2.0 process-execution engine for Go.
---

# gobpm documentation

**gobpm** is an embeddable BPMN 2.0 process-execution engine for Go — a
library, not a framework: you model processes in code, hand them to the
engine, and it runs them inside your application. The source, issues, and
releases live at [github.com/dr-dobermann/gobpm](https://github.com/dr-dobermann/gobpm).

Three doors, depending on what you came for:

- **[Developer Manual](guides/index.md)** — build, run, control, and extend
  the engine: getting started, the entity stack, every BPMN element gobpm
  supports, the data plane, and the extension seams. Every page is grounded
  in the public API and a runnable example. Start here.
- **[Design documents](design/SAD-001-vision-and-architecture.md)** — the
  *why* behind the engine: the architecture vision (SAD) and the decision
  records (ADRs) that fix the execution model, the event architecture, the
  data plane, and every other contract.
- **[BPMN 2.0 extract](bpmn-spec/index.md)** — the vendored extract of
  the OMG BPMN 2.0 specification the design documents cite by section:
  elements, execution semantics, state machines, and gobpm's conformance
  scope.

The one-shot landing records — the requirements documents under `srd/` and
the defect remediations under `fix/` — are published as the historical
record of how each slice of the engine landed; reach them through the
design documents that cite them, or via search.
