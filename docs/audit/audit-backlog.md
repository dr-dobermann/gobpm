# Audit backlog — findings that need design work, not a FIX

Findings surfaced by the code-review audits whose remediation exceeds a one-shot
FIX — they change *semantics* or a *contract*, so they need an ADR update and/or
a dedicated SRD rather than a defect patch. Parked here for future research and
development; each entry names the governing docs to amend. A FIX-track item, by
contrast, lands as `docs/fix/FIX-NNN`.

---

## AB-001 — Keyless `ParallelEvents` start gate double-instantiates

- **Source**: `docs/audit/code-review-third-pass-2026-06-29.md` §2.5 (🟠 P2,
  Active for the keyless configuration).
- **Code**: `pkg/thresher/instance_starter.go:152-158`,
  `pkg/thresher/thresher.go` `resolveAndLaunch`,
  `pkg/model/gateways/event_based.go:493-514`, `validateStartGate`.

**Problem.** An event-based gateway used as a process start in `ParallelEvents`
mode must produce **one** instance that completes when **all** arms' messages
have arrived (SRD-025 §4.3). Each arm is a persistent subscription, and the
create-or-route decision is keyed on a **correlation key** (ADR-016): the first
message mints an instance, later same-key messages route into it. If the gate
declares **no** `CorrelationKey`, `deriveKey` yields `""` and `resolveAndLaunch`
takes the no-dedup branch that **always** creates a new instance — so every arm
message spawns its own instance, each firing only its one arm and waiting forever
for the others (whose messages went to sibling instances). N arms → N stuck
instances, none completing. `Process.Validate` currently lets the keyless gate
through, and every test supplies a key, so the broken case is untested.

**Why it's not a FIX.** The remediation is a *semantics decision*, not a defect
patch: what should a keyless parallel-start gate **mean**? Two directions, each
with contract consequences:

1. **Reject at validation** — a `ParallelEvents` instantiating gate with no
   `CorrelationKey` fails `Process.Validate`. Standard-conformant, fail-fast;
   makes the keyless config illegal. Changes the validation contract.
2. **Key the dedup on the gate id** when no correlation key is present — all
   arms of one gate route into a single instance by construction. Changes the
   instantiation/correlation model (a gate-scoped implicit key).

Choosing between them — and pinning the BPMN-conformance argument — is ADR
work, and landing it (validation rule or implicit-key derivation + tests for the
keyless path) is a dedicated SRD.

**Governing docs to amend.**
- **ADR-015** (event-triggered instantiation) — the instantiation decision for a
  keyless parallel-start gate.
- **ADR-016** (message correlation) — if direction 2 is taken (implicit gate-id
  key when no `CorrelationKey`).
- **SRD-025** (event-based-gateway instantiation) — update §4.3 for the chosen
  keyless semantics; the landing SRD references it.

**Status**: Parked (was tentatively reserved as "FIX-014"; reclassified as
design work 2026-06-30).

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

**Status**: Parked 2026-06-30 (pulled from the FIX-014 P2 cluster: the other two
of that cluster — §2.1 `GExpression.Evaluate`, §2.11 `bpmncommon.Error.Structure`
— were already resolved by FIX-010, so the cluster yields no FIX).

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
