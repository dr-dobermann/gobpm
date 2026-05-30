# BPMN 2.0 KB

Reference extracted from OMG BPMN 2.0 spec, scoped to **Process Execution Conformance** (the subclass `gobpm` targets).

**Sources:**
- Spec PDF: `../../docs/BPMN formal-13-12-09.pdf` (OMG formal/2013-12-09, v2.0.2 with errata) — semantics, normative claims
- bpmn-moddle: `github.com/bpmn-io/bpmn-moddle` `resources/bpmn/json/bpmn.json` — structural metamodel
- OMG XSD: tiebreaker for compliance edge cases

**Layout:**

| Path | Content |
|---|---|
| [conformance.md](conformance.md) | In/out element list per §2.1.2 |
| [elements/](elements/) | Structural metamodel — attributes, associations, type hierarchy. 11 files by category |
| [state-machines/](state-machines/) | Activity + Process lifecycles from §13 |
| [semantics/](semantics/) | Token-flow, per-task behavior, sub-process variants, MI/Loop, compensation, events, end-events, data, correlation, event-handling |
| [scripts/](scripts/) | `gen.py` regenerates `elements/*.md` from cached `bpmn-moddle.json` |

Snapshot, not continuously updated. Regenerate `elements/` via `python3 scripts/gen.py` after updating `scripts/bpmn-moddle.json`. State-machines and semantics are authored from the spec PDF — manual update only.

## Gaps

_None currently. The §10.5–§13 execution-semantics core is covered. Further detail would come from §10.3.4 Human Interactions (UserTask form mapping), §10.4.3 XPath bindings (only relevant if engine targets XPath as `expressionLanguage`), and §10.6 Gateways (notation-level; structural and execution coverage already done)._
