# Camunda 7 — input/output variable mapping, and the standard's data associations

Reference for how the mainstream BPMN 2.0 engine moves data across an activity,
compared against what the standard prescribes. Useful when deciding how far the
standard's `DataAssociation` expression shapes should reach in `gobpm`.

**Sources:**
- Engine docs: [Camunda 7.20 user guide — process engine / variables](https://docs.camunda.org/manual/7.20/user-guide/process-engine/variables/)
  (input/output mapping) and the [BPMN 2.0 implementation reference](https://docs.camunda.org/manual/7.20/reference/bpmn20/).
- Spec-side KB entry: [`../bpmn-spec/semantics/data.md`](../bpmn-spec/semantics/data.md)
  — "Execution semantics of a single DataAssociation (§10.4.2, p225)".

---

## 1. What Camunda 7 actually implements

Camunda 7 moves data with a **vendor extension**, not with the standard's
association expressions: `camunda:inputOutput` on a task, event or sub-process,
holding `camunda:inputParameter` and `camunda:outputParameter` children.

- **Source** — "can be a simple constant string or an expression"; a script is
  also accepted, and "lists and maps can also be nested".
- **Target** — the parameter's `name` attribute: an input creates "a local
  variable to be created" in the activity's scope, an output maps the result
  back into the parent scope under that name. The target is a **plain variable
  name**, not an element expression.
- **Timing** — input mappings run at activity initialization, output mappings
  after the activity completes. "If an Activity is canceled (e.g. due to
  throwing a BPMN error), IO mapping is still executed."

## 2. What it does not implement

The BPMN 2.0 implementation reference lists Data Object and Data Store under
"Data" as symbols only, and says nothing about `dataInputAssociation`,
`dataOutputAssociation`, `transformation` or `assignment`. The standard's
association expression shapes (`data.md` §10.4.2 rules 1 and 2) have no
documented implementation in Camunda 7; the I/O mapping extension is what
modellers use instead.

## 3. What gobpm takes from this

- The standard's three shapes stay the model's contract — the engine reads
  documents written by any conformant tool, not only by one vendor.
- Where the standard is open and an implementation must choose, **practice is
  evidence of what modellers actually need**. An assignment's `to` is defined by
  §10.4.2 as an Expression yielding "any element in context or sub-element of
  it"; the mainstream engine's equivalent target is a plain name. gobpm narrows
  `to` to a **data path** (ADR-011 §2.4/§2.9.2) — strictly more than a bare name,
  strictly less than an arbitrary lvalue expression, and enough for every target
  a modeller addresses in practice.
- The vendor construct itself is a converter-dialect question (ADR-024 §2.14),
  not a model one: `camunda:inputOutput` is reported today, and mapping it onto
  the standard's shapes becomes *possible* only once those shapes evaluate.
