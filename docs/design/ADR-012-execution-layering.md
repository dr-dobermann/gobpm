# ADR-012 — Execution layering (the model implements public contracts, not internal types)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-14 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-002 v.1 Extension Architecture](ADR-002-extension-architecture.md) |

> **Draft — not yet implemented.** Fixes the audit's finding 2.1: `pkg/model`
> (the public modeling API) imports `internal/*` and exposes internal,
> user-unconstructable types in exported method signatures (`Exec(ctx,
> renv.RuntimeEnvironment)`). The decision keeps the existing execution shape —
> model element types still carry their `Exec` / data-binding methods, the node
> set stays closed (no user-defined nodes) — but moves the **contracts** those
> methods implement and consume into **public** packages, so the model depends
> only on public types. `internal/*` keeps the implementations. A CI depguard
> rule makes the boundary permanent. This is a hygiene/encapsulation fix, not an
> extensibility feature. Scope is conception; the implementing SRD does the
> file-level move and stages it green.

## 1. Context

### 1.1 What a clean public API requires

`pkg/model` is gobpm's **public, stable modeling surface** — the types a user
builds a process from. A public package should:

- **not import the engine's `internal/*`** (Go's own `internal` rule exists for
  exactly this; a public package leaning on internals couples the stable surface
  to volatile machinery), and
- **not expose internal types in its exported signatures** — a method a caller
  cannot name or satisfy is not really part of the API; it is internal
  plumbing wearing a public coat.

The runtime depends on the model (it executes it); the model should depend only
on **published contracts**, never on internal implementations.

### 1.2 What the engine has today

`pkg/model` element types (tasks, events, gateways) implement the runtime's
execution interfaces **directly against internal types**:

- the node-executor interface and the synchronizing-join marker (`internal/exec`);
- the per-execution environment passed to `Exec` (`internal/renv`);
- the data-binding consumer/producer interfaces and the execution **frame** they
  receive (`internal/scope` — `LoadData`/`UploadData` take a `*scope.Frame` and
  call its `InstantiateInputs`/`InstantiateOutputs`/`LoadProperties`/`GetDataByID`);
- the interaction registrator (`internal/interactor`, the user task) and the
  event producer (`internal/eventproc`, the events).

So `pkg/model` imports five `internal/*` packages, and exported methods like
`Exec(ctx, renv.RuntimeEnvironment)` carry an internal, user-unconstructable
parameter (audit 2.1, graded CRITICAL there).

This is encapsulation debt, not a bug: it compiles, there is no import cycle,
and only the engine ever calls these methods. ADR-002 anticipated a
public/internal interface split (its §3.3) and defined the per-execution
`RuntimeEnvironment` (its §4.3); this ADR completes that split for the contracts
the model touches.

### 1.3 Why now

It is the last open CRITICAL from the 2026-06-11 audit, and it is foundational
for the queued work: the parked SendTask/ReceiveTask executors (ADR-014) would
otherwise add **new** model→`internal` edges, and the observability ADR (ADR-013)
wants a clean public surface. Doing the contract split first means new executors
implement public contracts from the start.

### 1.4 What this is *not*

gobpm provides a **closed, standard-defined set of BPMN node types**
(SAD-001 §14, Process Execution Conformance). There is **no goal** to let users
add their own node kinds. So this ADR deliberately does **not** introduce an
executor registry, a visitor, or any extensibility machinery — the dispatch
stays exactly as it is (the track runs a node through the executor interface it
implements). The only change is that the interface, and everything it hands the
node, are **public**.

## 2. Decision

### 2.1 The model implements public contracts and imports no `internal/*`

`pkg/model` element types keep their execution methods (`Exec`, the
data-binding `LoadData`/`UploadData`) — the execution shape and the closed node
set are unchanged — but every interface they **implement** and every type they
**consume** is **public**. `pkg/model` imports zero `internal/*` packages.

### 2.2 The execution contracts move to public packages

The contracts the model touches are relocated to public package(s); `internal/*`
keeps only the **implementations** that satisfy them:

- **The node-executor interface** (and the synchronizing-join marker) — public;
  the model implements it, the track consumes it (by the same type assertion it
  does today — no registry).
- **The per-execution environment** the executor receives — a public interface
  (the runtime-facing peer of SRD-011's read-only `service.DataReader`). This is
  the "placement of the public service reader and node-executor contracts"
  ADR-011 v.5 left to the layering ADR. `internal` keeps the implementation
  (the instance's per-execution environment); what is public is the interface,
  free of internal types.
- **The data-binding surface** — the consumer/producer interfaces and the
  execution-frame operations the model calls (`InstantiateInputs`/`…Outputs`,
  `LoadProperties`, `GetDataByID`, commit) — public, with `internal/scope`
  keeping the concrete frame as the implementation.
- **The interaction registrator** (user task) and the **event producer**
  (events) — public interfaces, internal implementations.

The semantics are unchanged: this relocates the contracts and re-points the
model at them; it does not redesign data flow (ADR-010/011), eventing, or the
track lifecycle.

### 2.3 No registry, no user-defined nodes — dispatch is unchanged

Because the node set is closed (§1.4), the runtime keeps dispatching a node by
asserting it to the (now public) node-executor interface and calling it — there
is no kind→executor registry and no public registration entry point. This keeps
the change minimal: it is about *where the contracts live*, not about *how
nodes are dispatched* or *who may add a node*.

### 2.4 The boundary is enforced in CI by depguard

A depguard rule forbids `pkg/model/**` from importing `internal/**` — the rule
ADR-003 §4.4 (import-direction rules) called for and that is missing. Once the
contracts are public and the model is clean, the rule makes the regression that
accreted silently impossible to repeat: a model file reaching for an internal
type fails `make ci`.

### 2.5 Non-goals and scope (phased; each deferral named)

- **An executor registry / visitor / user-defined node kinds** — explicitly out
  of scope (§1.4); the node set is closed.
- **Redesigning data-flow, eventing, or lifecycle semantics** — this ADR
  relocates contracts only; ADR-010/011 semantics are untouched.
- **Splitting the `Instance` god-object** (audit 2.3) — a sibling refactor that
  shares the "separate the roles" spirit but is its own change.
- **The exact public package(s), whether the per-execution environment and the
  data-binding frame unify into one contract or stay two, and the per-element
  ordering of the move** — implementation decisions for the SRD(s), which stage
  it (contracts public → model re-pointed element-by-element → depguard rule on)
  keeping `make ci` green per step.

## 3. Consequences

- **`pkg/model` becomes a clean public API.** No `internal/*` imports, no
  internal types in exported signatures — the stable surface stops leaking
  machinery, and it compiles without the runtime loaded.
- **Internal machinery is free to change again.** With the model behind public
  contracts, refactoring the instance / data plane / event hub no longer risks
  the public model's compilation or signatures (ADR-002 §4.7 stability).
- **The boundary is permanent.** depguard fails any future model→internal edge.
- **ADR-014 and ADR-013 land cleanly.** New executors implement public contracts
  from the start; the observability ADR has a clean surface to extend.
- **Cost: a broad but mechanical relocation.** Five internal contracts go public
  and ~six element types + the task base re-point at them; no behaviour changes,
  no registry to design. The SRD stages it green.
- **A new public surface to keep stable.** The executor / environment /
  data-binding contracts join the public API under ADR-002 §4.7 versioning — a
  deliberate, documented commitment. Kept narrow because the node set is closed
  (no third-party implementors to support, only gobpm's own).

## 4. Alternatives considered

- **Full inversion with an executor registry (model = pure data, executors
  registered by kind).** The heavier "visitor/registry" the audit floated. Its
  payoff is **user-defined node kinds** — which SAD-001 §14 rules out (closed,
  standard-defined set). Rejected as over-engineering: it would move all
  behaviour out of the model and build a registration mechanism for an
  extensibility that is not a goal. The cheaper contract-relocation removes the
  internal coupling without it.
- **Accept the coupling and document it (won't-fix).** gobpm owns both layers,
  the node set is closed, only the engine calls `Exec` — so the coupling is
  "merely" a public package importing internal and exposing an internal type.
  Rejected: `pkg/model` is a *published* API; an exported method taking an
  unconstructable internal type is a real (if modest) API defect, and the
  depguard rule + clean import graph are cheap insurance against worse coupling
  later. The fix is bounded; living with the smell on the public surface is the
  worse trade.
- **Move only the executor interface public, keep `Exec` taking the internal
  environment.** Fixes one import line, not the problem: the signature would
  still carry an internal type. Rejected — the environment (and the frame, the
  registrator, the producer) must go public too, or the leak remains.
- **Make the per-execution environment public but keep the data-binding frame
  internal.** Half a fix: `LoadData`/`UploadData` would still take a
  `*scope.Frame`. Rejected — all five contracts must be public for the model to
  be internal-free; a partial move leaves the depguard rule failing.

## 5. Enterprise-readiness recommendations

Advisory, not gating — for the implementing SRD(s):

- **Land the depguard rule in the same change that completes the move**, so the
  boundary is enforced the moment it's correct and every staged step is checked.
- **Keep the public contracts minimal.** Since there are no third-party
  implementors (closed node set), expose exactly what gobpm's own executors
  need — a narrow surface is easier to keep stable (ADR-002 §4.7).
- **Add a model-only build/test** that imports `pkg/model` without the runtime,
  as living proof the inversion holds beyond the depguard rule.
- **Prefer one per-execution contract over many** if the environment and the
  data-binding frame can be unified without widening the surface — fewer public
  contracts to version.

## 6. Open questions

- None. Keeping the execution shape and the closed node set (no registry, §2.3),
  relocating the five execution contracts to public packages (§2.2), and
  enforcing `pkg/model ↛ internal` by depguard (§2.4) are decided above. The
  exact public package layout, whether the environment and frame contracts
  unify, and the staging order are implementation concerns for the landing
  SRD(s).

## 7. References

- [SAD-001 v.1 Vision & Architecture](SAD-001-vision-and-architecture.md) — §14
  Conformance & Compliance Scope (the closed, standard-defined node set this ADR
  assumes); the library-not-framework, model-as-public-surface goals it serves.
- [ADR-001 v.5 Execution Model](ADR-001-execution-model.md) — the track /
  node-execution lifecycle whose dispatch is left unchanged (§2.3).
- [ADR-002 v.1 Extension Architecture](ADR-002-extension-architecture.md) — the
  public/internal split (§3.3), the per-execution `RuntimeEnvironment` (§4.3),
  and the §4.7 versioning discipline this ADR refines and completes for the
  execution contracts.
- [ADR-003 v.1 Module Layout](ADR-003-module-layout.md) — §4.4 (import-direction
  rules) prescribed the depguard checks; this ADR adds the missing
  `pkg/model ↛ internal` rule.
- [ADR-011 v.5 Process Data Flow](ADR-011-process-data-flow.md) — §2.6 deferred
  "placement of the public service reader and node-executor contracts" to the
  layering ADR — placed here (§2.2).
- [ADR-014 v.1 Message Handling](ADR-014-message-handling.md) — its SendTask/
  ReceiveTask executors implement the public contracts this ADR defines.
- Architecture audit 2026-06-11 (`docs/audit/architecture-audit-2026-06-11.md`)
  — finding 2.1 (layering) this ADR remediates; re-graded from CRITICAL to an
  encapsulation fix once user-extensible nodes are excluded as a non-goal.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-14 | Ruslan Gabitov | Draft. Fixes audit 2.1 as an **encapsulation** fix (not extensibility): `pkg/model` keeps its execution methods and the closed node set, but the contracts it implements/consumes — the node-executor interface (+ synchronizing-join marker), the per-execution environment, the data-binding consumer/producer + frame operations, the interaction registrator, the event producer — move to **public** packages; `internal/*` keeps the implementations; `pkg/model` imports zero `internal/*`. A CI depguard `pkg/model ↛ internal` rule (ADR-003 §4.4) makes it permanent. **No registry / visitor / user-defined nodes** (SAD-001 §14 closed node set) — dispatch is unchanged (§2.3). Placement of the public reader/node-executor contracts that ADR-011 v.5 §2.6 deferred here is decided (§2.2). Out of scope: data-flow/eventing/lifecycle redesign, the Instance god-object split (2.3), exact package layout. Refines ADR-002 v.1; siblings ADR-001 v.5, ADR-003 v.1, ADR-011 v.5, ADR-014 v.1. |
