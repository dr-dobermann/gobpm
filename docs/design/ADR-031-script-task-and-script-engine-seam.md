# ADR-031 — The Script Task and the pluggable Script Engine seam

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-25 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-027 v.1](ADR-027-business-rule-task-and-rule-engine-seam.md) §2.5 (the standing decision this ADR fulfils: *the Script Engine is a pluggable seam of the same interface-plus-default shape as the rule engine — script interpreters are swappable engine services, never baked in*; everything else was deferred to this conception), [ADR-002 v.2](ADR-002-extension-architecture.md) §4.1/§4.2 (the five-point extension pattern), [ADR-003 v.1](ADR-003-module-layout.md) §4.4 (interpreters live in adapter modules — the core stays stdlib+uuid), [ADR-029 v.1](ADR-029-decision-table-engine-adapter.md) (the adapter-tier engine precedent this mirrors), [SAD-001 v.1](SAD-001-vision-and-architecture.md) §11 |

The Script Task is the last conformance-scope task type with no execution.
This ADR decides how it executes: a **pluggable Script Engine seam** —
format-routed by the standard's own `scriptFormat` MIME hint — with a
**null default in core** (the engine stays stdlib-light; interpreters are
adapters) and **Lua as the batteries adapter** (`adapters/lua`,
pure-Go `gopher-lua`).

## 1. Context & problem

The model stub is bare — `ScriptTask{ScriptFormat, Script string; task}`
with no constructor and no execution. The standard's clause is minimal
(the extract's §ScriptTask semantics, sourced from BPMN 2.0 §13.3): upon
activation *the associated script
is invoked*; on the script's completion *the Script Task completes*. The
element carries exactly two own attributes: **`script`** (the source body)
and **`scriptFormat`** (a MIME-type hint) — the language is deliberately
unspecified; the extract's engine note: "the spec does not mandate a script
language."

Prior decisions frame the answer:

- **ADR-027 §2.5** already fixed the shape: the Script Engine is a
  pluggable seam, interpreters are swappable engine services.
- **The dependency-light core** (SAD-001; the README contract: stdlib +
  `google/uuid` only) forbids embedding any interpreter in `pkg/` — the
  ADR-029 adapter tier is where interpreters live, and `adapters/dtable`
  proved that shape end to end (module discovery, depguard direction,
  external seam proof).
- **The expression layer (#74) is a different seam**: `expression.Engine`
  evaluates *modeled* `FormalExpression`s (conditions, gateways); the
  Script Engine executes a task's *opaque source body*. They stay separate
  — a FEEL adapter and a Lua adapter answer different questions.

## 2. Decision

### 2.1 The seam — multiple engines, format-routed by the core

A new engine service, the **Script Engine**, in the ADR-002 shape — with
one deliberate extension over the rule-engine precedent: **more than one
script engine registers in a single gobpm engine**, and the core routes
between them by the standard's own `scriptFormat`.

- **Interface** — three members: the `##`-kind string (startup config and
  observability — the ScriptTask has no `implementation` attribute, so the
  kind is engine identity only); **`Formats()` — the enumerable list of
  MIME hints the engine interprets** (enumerable, not a predicate: only a
  listable claim lets the router detect conflicts and print the routing
  table); and **execute** (*run this script body against the read-only
  data surface, return the script's named outputs*). The read surface is
  the same `service.DataReader` walk-up every in-process functor receives.
- **The router is core machinery** (pure Go — the stdlib-light contract
  holds): registered engines fold into a **format → engine map** at
  construction. Two engines claiming the same format is a **loud
  construction-time error naming both kinds** — never silent shadowing.
  The startup config prints the routing table (format → engine kind), so
  which interpreter answers which `scriptFormat` is operator-visible.
- **Routing at the task**: the task's `scriptFormat` (normalized
  case-insensitively) selects the engine; a format no registered engine
  claims is a **classified error that lists the formats actually
  registered** — self-explaining, never a silent skip.
- **Wiring** — the five-point pattern with a registry twist: the config
  holds the engine set; **`WithScriptEngine(e)` is repeatable — each call
  registers another engine** (nil rejected; format conflicts rejected);
  the `EngineRuntime` accessor exposes the routing surface; the
  startup-config line prints the table.

### 2.2 The default — the empty registry, loud

The in-core default is the **empty registry** (shown as `##None` in the
startup config): no formats are claimed, and executing any Script Task
fails with a classified error telling the operator exactly what to do
(*register an engine — e.g. `adapters/lua` — via `WithScriptEngine`*).
ADR-027 §2.5 anticipated a default "if any"; here the honest answer is
none:

- an interpreter in core breaks the stdlib-light contract (§1);
- a Go-functor "script" default would duplicate `gooper`/`gorules` under a
  false name — a script engine that cannot run script *text* is a lie.

The empty registry keeps the never-nil accessor convention and turns a
model that needs scripts into a **loud, self-explaining failure** instead
of a nil-dereference, through the ordinary fault machinery — the zero
case of the general multi-engine mechanism, not a special object.

### 2.3 Execution semantics — invoke, complete, commit named outputs

Exactly the standard's clause on the existing task machinery:

- **On activation**: the task's `scriptFormat` routes to the registered
  engine (§2.1) and the script runs synchronously (the token waits, as for
  an in-process operation).
- **On return**: the script's result is a set of **named outputs** (name →
  value). Each output commits as its own Ready datum through the execution
  frame — the script-natural semantics (*a script sets variables*), and
  deliberately simpler than the Business Rule Task's DMN fold: scripts
  have no hit policies and no multi-row results, so per-name commit is the
  whole mapping. An empty result commits nothing. Reads happen only
  through the read surface; writes only through the returned outputs (the
  functional posture every in-process seam shares).
- **On failure**: a script error fails the task through the ordinary fault
  path — boundaries, loops, compensation apply unchanged. An engine may
  map a script-level convention onto typed business errors (an adapter
  concern, conceived per adapter); absent that, a script failure is a
  technical task failure.
- **Observability**: a new `Script` fact kind, mirroring the rule engine's
  (SRD-060 FR-6 precedent): an *Executed* fact (engine kind, format,
  output count/names — never payload values) and a *Failed* fact (the
  decision-level context before the error rides the fault machinery).

### 2.4 The batteries adapter — Lua (`adapters/lua`)

The first interpreter is **Lua via `gopher-lua`** — chosen over the
proposed alternative (Python) deliberately:

- **pure Go, no cgo** — embedding CPython requires cgo and a system
  Python, breaking static builds and cross-compilation (the deployment
  posture a Go library must not impose); `gopher-lua` is a mature, MIT,
  pure-Go VM;
- **Lua is the industry's embeddable script language** — small surface,
  designed for hosting, sandboxable;
- a **Python-dialect sibling** (`starlark-go`, pure Go, deterministic) is
  the honest path if Python syntax is wanted — a future `adapters/starlark`
  behind the same seam; real CPython is rejected outright.

Adapter conception (landed by its own SRD):

- **Formats**: the MIME hints `text/x-lua` and `application/x-lua` (plus
  the bare `lua` convenience), normalized case-insensitively. Kind:
  `##Lua`.
- **Data exposure — lazy and fail-loud**: the script sees a read-only
  `data` table whose lookups resolve through the `DataReader` on access
  (a metatable, not an eager snapshot). Reading an **absent datum is a
  script error** (the dtable §2.5 posture — a typo fails loud), with an
  explicit `has(name)` probe for optional data. Values map Go↔Lua
  (bool/number/string; structured values to tables) — an unmappable value
  is a loud error, never a silent nil.
- **Results**: the script's returned table (string keys → mappable
  values) becomes the named outputs; returning nothing commits nothing;
  a non-table return is an error (outputs are named, per §2.3).
- **Sandbox by default**: only the safe libraries load (base, table,
  string, math); `io`/`os` stay out unless the embedder opts in; execution
  honors the task context (cancellation/timeout through the VM's context
  hook). The sandbox posture is the default precisely because script
  bodies are the first artifact likely to arrive from outside the Go
  codebase.

### 2.5 Engine notes (deviations & choices)

| Choice | Standard position | Engine choice |
|---|---|---|
| Script language | not mandated; `scriptFormat` is a MIME hint | **multiple registered engines**, core-routed by an enumerable format→engine map (conflicts loud at construction); Lua as the batteries adapter; the default is the loud empty registry (§2.1/§2.2/§2.4) |
| Result mapping | silent (no result model on ScriptTask) | the script's named outputs commit per-name as Ready data — no fold, no result variable (§2.3) |
| Script data access | silent | read-only reader surface, lazy `data` table, absent datum fails loud with a `has()` probe (§2.4) — the fail-loud house rule over Lua's nil convention |
| Failure typing | task faults | technical failure by default; typed business-error conventions are per-adapter (§2.3) |
| Sandbox | — | safe-libraries-only by default; embedder opt-in for more (§2.4) |

## 3. Grounding

| Claim | Source |
|---|---|
| ScriptTask semantics: invoke on activation, complete on the script's completion | the vendored extract, `semantics/tasks.md` §ScriptTask (file source: BPMN 2.0 §13.3.3, p430) |
| `script` + `scriptFormat` (MIME) carry language and source; no mandated language; both 0..1 in the metamodel | the extract's ScriptTask engine note (`semantics/tasks.md:65`) + the element model (`elements/activities.md` §ScriptTask: `scriptFormat` attr 0..1, `script` child 0..1) |
| The Script Engine is pluggable, same shape as the rule engine | ADR-027 v.1 §2.5 (the standing decision) |
| Core stays stdlib+uuid; interpreters can't live there | SAD-001 v.1 (the library posture); ADR-003 v.1 §4.4 |
| The adapter-tier engine shape (module, seam proof, deploy split) | ADR-029 v.1 (landed: `adapters/dtable`) |
| The expression seam is separate (`FormalExpression` evaluation) | ADR-011 lineage; `pkg/model/expression` (landed) |
| The observability mirror (per-engine facts, masking rule) | SRD-060 v.1 FR-6 (`KindRules` precedent) |

## 4. Alternatives considered

- **Embed an interpreter in core** (Lua or otherwise). Rejected — breaks
  the stdlib-light contract; the adapter tier exists for exactly this.
- **Real Python (CPython via cgo).** Rejected outright — cgo + system
  Python destroys static builds, cross-compilation and the embedding
  story; the deployment cost lands on every consumer of the adapter.
  Starlark (`starlark-go`) is the recorded pure-Go path to Python syntax,
  as a future sibling adapter — noted, not built.
- **Reuse the expression engine for scripts.** Rejected — different
  contracts: expressions are modeled, typed, single-valued evaluations;
  scripts are opaque multi-statement bodies with named outputs. Conflating
  them couples #74's evolution to task execution (the reason ADR-029
  rejected `FormalExpression` conditions too).
- **A Go-functor default engine.** Rejected — duplicates `gooper` under a
  false name and cannot execute script text (§2.2); the null default is
  honest.
- **The BRT rows-and-fold result model for scripts.** Rejected — scripts
  have no hit policies; named outputs committed per-name are the natural
  and simpler mapping (§2.3).

## 5. Consequences

**Positive:** the last silent conformance task type gains execution; the
seam completes the ADR-027 §2.5 promise with the same proven shape;
interpreters stay out of the core; the Lua adapter gives embedders real
scripting with a sandbox-by-default posture; a second interpreter
(Starlark) is one adapter away.

**Negative / cost:** a third engine-service seam to keep stable; a new
adapter module with an external dependency (`gopher-lua`) to track; the
null default means zero-config models with Script Tasks fail at runtime
(loudly, by design) rather than at registration — registration-time
validation would need model-level knowledge of the wired engine, noted as
a possible future guard.

**Follow-ups this conception sets up:** `adapters/starlark` (the
Python-dialect sibling — registering beside Lua in the same engine, the
multi-engine mechanism's first natural exercise); a registration-time
"format supported" validation hook; per-adapter typed business-error
conventions.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 (Accepted) | 2026-07-25 | Ruslan Gabitov | Accepted with both landing SRDs: the multi-engine seam, registry routing, the ScriptTask semantics and the `##None` default landed first (the seam SRD); the Lua batteries adapter (`adapters/lua`) completed the conception (its SRD). Starlark remains the noted sibling. |
| v.1 | 2026-07-25 | Ruslan Gabitov | Draft conception, fulfilling ADR-027 §2.5. The Script Engine seam: format-routed (the standard's `scriptFormat` MIME hint validates against the engine's format predicate; unsupported = loud classified error), executing an opaque script body against the read surface and returning **named outputs committed per-name** (no fold — scripts set variables). **Multiple script engines register in one gobpm engine** (repeatable `WithScriptEngine`): the core folds their enumerable `Formats()` claims into a format→engine routing map — claim conflicts are loud construction-time errors, the startup config prints the routing table, and an unclaimed format errors listing what IS registered. The default is the loud empty registry, shown as `##None` (stdlib-light core; a Go-functor default would duplicate gooper dishonestly). **Lua (`gopher-lua`, pure Go) is the batteries adapter** — real CPython rejected outright (cgo), Starlark recorded as the future Python-dialect sibling. Adapter conception: lazy fail-loud `data` table with a `has()` probe, table-return results, sandbox-by-default (safe libs only, context-honoring). A `Script` observability kind mirrors the rule engine's facts. Implementation rides the accompanying SRDs (the seam+task, then the Lua adapter). |
