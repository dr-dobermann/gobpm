# SRD-065 — The Lua Script Engine adapter (`adapters/lua`)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-25 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-031 v.1](../design/ADR-031-script-task-and-script-engine-seam.md) §2.4 (the batteries adapter: `##Lua` over pure-Go `gopher-lua`, the lazy fail-loud `data` table with a `has()` probe, table-return results, sandbox by default) — completing the ADR's conception after SRD-064 v.1 landed §2.1–§2.3/§2.5 |
| Upstream | [ADR-029 v.1](../design/ADR-029-decision-table-engine-adapter.md) (the adapter-module engine precedent), [ADR-003 v.1](../design/ADR-003-module-layout.md) §4.4 |
| Refines | SRD-064 v.1 (the seam this plugs into), SRD-062 v.1 (the adapter-module landing pattern: module wiring, e2e-from-the-adapter, example with two replaces) — sideways |

Lands the first real interpreter behind the Script Engine seam: Lua via
`github.com/yuin/gopher-lua` (pure Go, MIT — no cgo, static builds
intact). With it: the e2e proof (a real Lua script beside a second engine,
routed live), `examples/script-task`, the conformance row-5 flip, and the
ADR-031 + #87 closure.

## §1 Background

- The seam is landed and proven (SRD-064): `script.Engine` (`Type` /
  `Formats` / `Execute → Outputs`), the routing Registry, the repeatable
  `WithScriptEngine`, the ScriptTask with sorted per-name commits, the
  `Script` facts. The e2e already routes two engines — with stubs; this
  SRD replaces one stub with a real interpreter.
- The adapter-module pattern is landed (SRD-062, `adapters/dtable`):
  own `go.mod` (`require gobpm v0.9.0` + `replace ../..`), auto-discovery
  by the Makefile module loops, root-config lint, adapter-side e2e
  importing `pkg/thresher` (the allowed direction), an example module
  with two replaces.
- `gopher-lua`'s embedding surface (verified against the library, pinned
  at its current release in `go.mod`): per-execution `lua.NewState`
  isolation; `SkipOpenLibs` + selective `OpenBase/OpenTable/OpenString/
  OpenMath` for sandboxing; `L.SetContext(ctx)` for cancellation and
  deadlines; metatables (`__index`) for the lazy `data` table;
  `L.DoString` returning the chunk's values on the stack; `LBool/LNumber
  (float64)/LString/LTable` as the value bridge.

## §2 Requirements

### §2.1 Functional

- **FR-1 — the module.** New `adapters/lua`
  (`module github.com/dr-dobermann/gobpm/adapters/lua`; deps: the core +
  `github.com/yuin/gopher-lua` + testify (tests)).
- **FR-2 — the engine.** `lua.New() *Engine` implementing
  `script.Engine`: `Type() = "##Lua"`; `Formats() = {"text/x-lua",
  "application/x-lua", "lua"}`; `Execute` runs the body on a **fresh,
  per-execution `LState`** (no cross-task state; concurrent tracks safe
  by construction), context-bound (`SetContext` — a canceled/expired task
  context aborts the script with an error).
- **FR-3 — the sandbox (ADR-031 §2.4).** `SkipOpenLibs` + base, table,
  string, math only; the base library's escape hatches (`dofile`,
  `loadfile`, `load`, `loadstring`) are **removed** after opening; `io`/
  `os` never load. `print` stays (stdout only — noted as the one
  deliberate side-channel, useful for examples/debugging). No opt-in
  surface for more libraries in this landing (a future option, noted).
- **FR-4 — the data bridge, lazy and fail-loud.**
  - The script sees a read-only global **`data`** table: `__index`
    resolves through the task's `DataReader` **on access**; a missing
    datum **raises a Lua error naming it** (fail-loud; the ADR's posture
    over Lua's nil convention); `__newindex` raises (read-only —
    writes go through returned outputs only).
  - A global **`has(name)`** returns a boolean probe for optional data.
  - **Go→Lua**: bool, int/int64/float64 (→ `LNumber`), string; maps
    (string keys) and slices recurse into tables; any other value is a
    loud error naming the datum and type.
  - **Lua→Go** (outputs): `LBool`, `LNumber` (as float64 — Lua's number
    type; documented like the dtable JSON-literal note), `LString`;
    tables convert to `map[string]any` (string-keyed) or `[]any`
    (sequence) recursively; functions/userdata/thread are loud errors.
- **FR-5 — results.** The chunk's **returned table** (string keys →
  bridgeable values) becomes the `script.Outputs`
  (`values.NewVariable` per entry); returning nothing/nil commits
  nothing; a non-table single return or non-string keys are classified
  errors (outputs are named — ADR-031 §2.3).
- **FR-6 — errors.** Compile and runtime Lua errors return as classified
  errors carrying the Lua message (line info included by the VM); no
  typed business-error convention in this landing (per ADR-031 §2.3 —
  noted for a follow-up).

### §2.2 Non-functional

- **NFR-1 — validate-all-params**; no `Must*` calls in non-test adapter
  code (the FIX-026 posture, as in `adapters/dtable`).
- **NFR-2 — isolation**: no state shared between executions; the module
  imports the core + `gopher-lua` only.
- **NFR-3 — coverage**: module suite `-race`, target ≥95% (aim higher);
  root `make ci` green with the module auto-discovered.

## §3 Models (shapes)

```go
// adapters/lua (package lua)

const LuaType = "##Lua"

func New() *Engine

func (e *Engine) Type() string
func (e *Engine) Formats() []string
func (e *Engine) Execute(ctx context.Context, format, body string,
	r service.DataReader) (script.Outputs, error)
```

(The engine is stateless — `New()` takes no options and cannot fail; all
per-execution state lives in the `LState` built inside `Execute`.)

Worked example (the e2e's script):

```lua
-- reads process data lazily; a typo'd name fails loud
local total = data.total
local tier  = has("tier") and data.tier or "retail"

return {
  discount_pct = (tier == "vip" and total > 100) and 25
                 or (total > 100 and 15 or 5),
  audited      = true,
}
```

## §4 Analysis & decisions

- **§4.1 A fresh `LState` per execution.** An `LState` is not
  goroutine-safe and carries globals; pooling would leak script state
  across tasks and instances. Per-execution construction is the isolation
  guarantee — the cost (state setup per script run) is the price of
  correctness, acceptable for the batteries tier (a pooling option is a
  measurable future optimization, noted).
- **§4.2 Numbers land as float64.** Lua has one number type; an int datum
  read by a script and returned comes back float64. Documented on the
  engine (the dtable JSON-literal precedent) — downstream conditions
  compare float64 or the script formats explicitly.
- **§4.3 `print` stays in the sandbox.** Pure stdout, no filesystem or
  process reach; the alternative (removing it) buys little safety and
  costs the primary debugging tool. Recorded as the sandbox's one
  deliberate side-channel.
- **§4.4 The `data` metatable raises on absence.** Lua idiom would return
  nil; the house rule wins (a typo'd datum name fails the task on first
  run, catchable by an Error boundary), with `has()` as the explicit
  opt-out — exactly the dtable `IfPresent` split, in Lua clothes.

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | engine unit: identity + formats | FR-2: `##Lua`, the three format claims; a fresh state per Execute (a global set in run 1 is invisible in run 2) |
| T-2 | the data bridge | FR-4: lazy reads (a datum read only when accessed — probe via a counting reader); absent datum raises naming it; `has()` true/false; `data.x = 1` raises (read-only); Go→Lua matrix incl. nested map/slice; unmappable Go value errors |
| T-3 | results | FR-5: the worked script returns the table (float64 numbers asserted); nil return = no outputs; non-table return errors; non-string keys error; Lua→Go matrix incl. nested tables; function-valued output errors |
| T-4 | sandbox + context | FR-3/FR-2: `io`/`os` are nil; `dofile/loadfile/load/loadstring` are nil; an infinite loop aborts on context cancel/deadline; compile and runtime errors surface with Lua's message (FR-6) |
| T-5 | e2e through the ScriptTask (adapter test importing `pkg/thresher`) | a real Lua script beside a stub second engine — live routing by format; the Lua outputs read downstream and driving a conditional flow; `Script/Executed` carrying `##Lua` |
| T-6 | example (`examples/script-task/`) | smoked exit 0: the script body embedded from a `.lua` file, outputs visibly driving the flow |

## §7 Milestones

- **M1 — the adapter.** FR-1…FR-6; T-1…T-4.
  `feat(adapters): lua — the Lua Script Engine (SRD-065 M1)`.
- **M2 — e2e + example + full doc sync.** T-5, T-6;
  `examples/script-task/` (embedded `.lua`, two replaces, mermaid README,
  gitignored binary); CHANGELOG; **conformance row 5 → ✅**;
  README + README.ru tour (the scripting paragraph); roadmap WS-E
  (`adapters/lua` — the second landed adapter); examples index;
  **ADR-031 → Accepted** and this SRD's §10/flip at the landing tail;
  the PR carries **Closes #87** (both task halves delivered).
  `feat(adapters): lua — e2e, example, doc sync (SRD-065 M2)`.

## §8 Cross-doc

- Implements **ADR-031 v.1** §2.4 (completing the conception).
- Upstream: **ADR-029 v.1**, **ADR-003 v.1** §4.4.
- Sideways: **SRD-064 v.1** (the seam), **SRD-062 v.1** (the adapter
  pattern).
- Tracking: **Closes #87** with this landing (BRT landed SRD-060/062;
  Script Task landed SRD-064/065).

## §9 Definition of Done

- [ ] FR-1…FR-6 implemented; every §6 test exists and passes.
- [ ] Module suite `-race` green (≥95%); root `make ci` exit 0 with the
      module in the loops; no `Must*` calls in non-test adapter code.
- [ ] The sandbox holds (T-4) and cancellation aborts scripts (T-4).
- [ ] `examples/script-task/` runs exit 0; binary gitignored; index row.
- [ ] §10 filled; row 5 flipped; README(+ru)/roadmap/CHANGELOG synced;
      ADR-031 + this SRD flipped; the PR closes #87.

## §10 Implementation summary

*Filled at landing.*

## Open questions

*None — §4 resolves the design points inline.*

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-25 | Ruslan Gabitov | Initial draft — lands ADR-031 §2.4: `adapters/lua` over pure-Go `gopher-lua` (`##Lua`; `text/x-lua`, `application/x-lua`, `lua`). A fresh context-bound `LState` per execution; the sandbox (base/table/string/math with the load-family removed, `io`/`os` never loaded, `print` the one recorded side-channel); the lazy fail-loud `data` table (`__index` through the reader, absence raises, `has()` probes, `__newindex` rejected); the two-way value bridge with loud unmappable errors and float64 numbers documented; table-return → `script.Outputs`. e2e beside a stub engine (live multi-engine routing with a real interpreter), `examples/script-task` with an embedded `.lua`, the conformance row-5 flip, ADR-031 acceptance and the #87 closure. Two milestones. |
