# Audit backlog — findings that need design work, not a FIX

Findings surfaced by the code-review audits whose remediation exceeds a one-shot
FIX — they change *semantics* or a *contract*, so they need an ADR update and/or
a dedicated SRD rather than a defect patch. Parked here for future research and
development; each entry names the governing docs to amend. A FIX-track item, by
contrast, lands as `docs/fix/FIX-NNN`.

> **Graduated:** **AB-001** (keyless `ParallelEvents` start gate
> double-instantiates) landed as **SRD-033** (2026-07-02) — implementing the
> already-decided **ADR-005 v.4 §2.12.5(7)** validation rule, so it needed a
> landing SRD, not the ADR decision this backlog assumed.

---

## AB-002 — UserTask interactor lifecycle & rendering multiplicity

- **Source**: `docs/audit/code-review-third-pass-2026-06-29.md` §2.8, §2.9 (both
  🟠 P2, Active).
- **Code**: `pkg/model/activities/user_task.go` `Exec` (`:170-209`),
  `pkg/model/activities/user_task_options.go` `WithRenderer` (`:60-81`);
  registrator contract in `pkg/model/hinteraction` / `renv.RuntimeEnvironment`.

Two defects in the User Task ↔ interactor boundary. Grouped as one design track
because both turn on how the human-interaction subsystem is *supposed* to behave
(rendering multiplicity; task lifecycle on instance teardown), not on a local
patch — they belong with a future Service/User-task interactor design pass
(cf. [[project_manualtask_decision]], the bundled Service/User-task refactor).

**§2.9 — `UserTask.Exec` ignores context (goroutine leak).** `Exec` takes
`_ context.Context` (`:171`) and blocks on `for d := range rCh` (`:194`) until
the registrator closes the channel. On instance cancel/timeout the track
goroutine never observes `ctx.Done()`, so a never-completed human task pins the
goroutine + its scope frame forever. Siblings (`ServiceTask`, `ReceiveTask`)
already select on `ctx.Done()`. The fix is more than a `select`: the
`RenderRegistrator` contract has to define what cancellation *means* for a
half-entered human task — does the registrator get an `Unregister`/cancel call,
is partial input discarded, who closes `rCh`? That contract decision is the
design work.

**§2.8 — `WithRenderer` rejects a second renderer of the same implementation
type.** The dedup predicate (`user_task_options.go:70`) rejects when
`r2c.Implementation() == r.Implementation()`, but `Implementation()` is a fixed
**type marker** (e.g. `"##consInputRender"`), so two distinct console renderers
with different IDs collide and the second is refused. BPMN permits one-or-more
renderings of any kind (`hinteraction/rendering.go`). The narrow fix is "dedup by
ID only", but the right scope is the rendering-multiplicity contract (may a task
carry N renderers of the same impl? how are they composed/ordered?) — a
hinteraction-design question.

**Governing docs to amend.** A hinteraction/interactor ADR (none exists yet —
the subsystem is `pkg/model/hinteraction` + `internal/interactor`) plus the
landing SRD; fold into the planned Service/User-task interactor refactor.

**Status**: **Resolved** by [ADR-020](../design/ADR-020-human-interaction-execution-model.md)
+ SRD-034 (landed on `feat/human-interaction-model`, 2026-07-03) — the subsystem
got its hinteraction/interactor ADR (the human-interaction execution model).
§2.9 is dissolved by construction: UserTask is now a wait node parking cooperatively
on the loop-owned `evtCh` (cancellable via `ctx`/teardown), so there is no
uncancellable blocking loop; §2.8 is fixed by dedup-by-`ID()`-only. The whole
blocking interactor contract (`Registrator`/`RenderRegistrator`) was removed.
Originally parked 2026-06-30 (pulled from the FIX-014 P2 cluster: the other two —
§2.1 `GExpression.Evaluate`, §2.11 `bpmncommon.Error.Structure` — were already
resolved by FIX-010, so the cluster yielded no FIX).

---

## AB-003 — Unspecified-direction gateway: enforce merge-or-split?

- **Source**: `docs/audit/code-review-third-pass-2026-06-29.md` §3.5 (🟡 P3,
  Active validation gap).
- **Code**: `pkg/model/gateways/gateway.go` `testDirectionFlows`, the
  `Unspecified` case (`:295-298`) accepts `inCount >= 1 && outCount >= 1` — so a
  1-in/1-out gateway passes `TestFlows`.

**Problem.** The audit asks to tighten the `Unspecified` rule to **merge-or-split**
(`inCount >= 2 || outCount >= 2`), citing a BPMN mandate that "a Gateway MUST
merge or split". **That normative rule is NOT in the vendored spec extract**:
`docs/bpmn-spec/elements/gateways.md` lists `gatewayDirection` ∈ {`Unspecified`
(default), `Converging`, `Diverging`, `Mixed`} and `docs/bpmn-spec/semantics/
gateways.md` gives per-type token rules, but neither forbids a 1-in/1-out
gateway. So the fix rests on an **unverified standard-claim**.

**Why it's not a FIX.** Two coupled decisions, neither mechanical:
1. **Verify the mandate** against the full BPMN 2.0 spec (and the BPMN NotebookLM)
   with an actual `§`-pin — per the project's spec-grounding rule, an
   asserted-from-memory standard-claim can't drive a validation change.
2. **Policy**: tightening rejects processes that validate **today** (1-in/1-out
   `Unspecified` gateways). The project rule is *standard-default + opt-in
   relaxation* ([[feedback_parametrize_relaxations_default_standard]]) — so if the
   mandate holds, the change likely needs a relaxation parameter, not a hard
   reject. That is ADR-005 (gateways) work.

**Governing docs to amend.** **ADR-005** (gateways-and-joins) — the
direction-conformance decision + any relaxation knob; `docs/bpmn-spec/`
elements/semantics gateways as the authority to pin.

**Status**: Parked 2026-06-30 (pulled from the FIX-014 P3 sweep; the other 11
sweep findings are mechanical and proceed as FIX-014).

---

## AB-004 — depguard core-import rule blocks the runtime server under per-module lint

- **Source**: `docs/audit/code-review-third-pass-2026-06-29.md` §3.17 (🟡 P3,
  Latent — server stub).
- **Code**: `.golangci.yml:39-50` (the `core-no-runtime-no-adapters` depguard
  rule, `files` globs include `**/cmd/**/*.go`); `runtime/cmd/gobpm-server/
  main.go` (a stub today, no `runtime` import yet); `Makefile:132-139`
  `lint-all-modules` runs `cd $$dir && golangci-lint run --config=$(CURDIR)/
  .golangci.yml` per module.

**Problem.** The rule denies *core* importing `…/runtime` / `…/adapters`. When
the ADR-004 server is wired, `runtime/cmd/gobpm-server` must import `…/runtime`
— and `make ci` (via `lint-all-modules`) will deny it, failing lint.

**Why it's not a quick fix.** `lint-all-modules` `cd`s into each module and runs
golangci with module-relative paths *and the forced root config*. Inside
`runtime/`, the server's path is `cmd/gobpm-server/main.go` — **identical** to the
core module's `cmd/…`. So a path-glob carve-out (`!**/runtime/cmd/**`) can't
distinguish the two, and a `runtime/.golangci.yml` is overridden by the forced
`--config`. The real remedy is a small **multi-module lint-config strategy**
(give `runtime` its own config without the core-import deny, and stop forcing the
root config — or a runtime-scoped rule), a decision best made and **validated
against a real runtime import**, not blind against a stub.

**Governing docs to amend.** **ADR-003** (layering — the core-import rule §4.4)
and **ADR-004** (runtime server). Do it when the server actually wires `runtime`.

**Status**: Parked 2026-06-30 (pulled from the FIX-015 CI/build-hardening
cluster; the other four — §2.12, §3.15, §3.16, §3.18 — are mechanical and
proceed as FIX-015).

---

## AB-005 — Structured ItemDefinition composition / decomposition (multi-variable Data{Input,Output}Associations)

- **Source**: surfaced during the **SRD-036** Service Task external-worker
  design (input/output marshalling) — "how does a worker get more than one
  process variable?".
- **Code**: `pkg/model/service/operation.go` `BindInput` resolves a **single**
  scope variable by the message item's ID
  (`r.GetDataByID(msg.Item().ID())` → `msg.Item().Structure().Update(...)`);
  `pkg/model/bpmncommon/message.go` `Message` carries a single
  `item *data.ItemDefinition`; `pkg/model/data/item.go` `ItemDefinition` already
  supports a composite `Structure()` value and `isCollection`.

**Problem.** An Operation's input/output is a single Message → a single
`ItemDefinition`, and gobpm binds it from **one** scope variable by ID. It does
**not** (a) assemble several distinct process variables into one structured
input item (a **`DataInputAssociation`** with multiple sources), nor (b) spread a
structured output item's fields back out into several process variables (a
**`DataOutputAssociation`** with multiple targets). So a ServiceTask/worker that
needs several process variables must have them **pre-combined** into one
structured variable, and cannot scatter a structured result across variables.

**What's needed (bidirectional).**
1. **Compose** — put multiple `data.Data` values (variables and other data) as
   items **into** a structured `ItemDefinition` (gather → structure), to build a
   worker's single input message from many process variables.
2. **Spread** — destructure a structured `ItemDefinition`'s fields **out** into
   separate process variables (structure → scatter), to land a worker's single
   output message into many process variables.

**Why it's not a quick fix.** This changes the **data-flow semantics** (how
`ItemDefinition`s compose from / decompose to process variables) — a model /
contract change touching the data layer, the association model, and operation
binding — not a defect patch.

**Governing docs to amend.** **ADR-011** (Process Data Flow — the data-binding
model), plus a dedicated landing SRD.

**Status**: Parked 2026-07-06. `Job.Input` stays a single `*data.ItemDefinition`
(the operation contract is one message); this composition/spread is a data-layer
concern **orthogonal** to the SRD-036 job-queue rework.
