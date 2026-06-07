# gobpm Development Roadmap

| Property | Value |
| :---- | :---- |
| **Author** | dr-dobermann |
| **Status** | Living |
| **Version** | 3.0 |
| **Date** | 2026-06-06 |
| **Subordinate to** | [SAD-001 v.1 Vision & Architecture](../design/SAD-001-vision-and-architecture.md) |
| **Conformance scope** | [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) |

This roadmap sequences the work that delivers the architecture described in [SAD-001 v.1](../design/SAD-001-vision-and-architecture.md) and its subordinate ADRs ([ADR-001 v.3](../design/ADR-001-execution-model.md), [ADR-002 v.1](../design/ADR-002-extension-architecture.md), [ADR-003 v.1](../design/ADR-003-module-layout.md), [ADR-004 v.1](../design/ADR-004-runtime-environment-contract.md), [ADR-005](../design/ADR-005-gateways-and-joins.md)/[006](../design/ADR-006-events-and-subscriptions.md)/[007](../design/ADR-007-in-memory-long-waits.md)). It is **subordinate** to those documents: where they establish *what* and *why*, this roadmap orders the *when*. It does not introduce architecture — anything that looks like a new decision belongs in an ADR, not here.

It replaces the v2.0 roadmap, which was organised purely as BPMN-element phases and predated the SAD/ADR conception. The element ordering from v2.0 survives (it is sound), but it is now framed inside the dependency chain the SAD/ADRs imply: conception → structural foundation → element completion → runtime overlay.

## 1. How this roadmap works

gobpm is built **specification-first**. Every non-trivial landing follows the project's SDD flow:

1. A spec exists first — an **SRD** (one landing's requirements) or **FIX** (a bug landing), referencing the governing **ADR** up the hierarchy.
2. Spec is agreed before implementation.
3. Implementation lands with tests; verification is demonstrable (`make ci` green, acceptance gate met).
4. Status flips and the change merges via PR.

Supporting discipline already in force:

- **CI parity** — `make ci` mirrors the GitHub `check` workflow exactly (tidy → lint → build → race tests → govulncheck). Green locally ⇒ green on CI. Tooling is pinned (`make tools`, Go toolchain pinned in `go.mod`).
- **Branch protection** — `master` takes changes only through a PR with a green `check`; no direct or force pushes, no admin bypass.
- **Document hierarchy** — references go up or sideways only, version-pinned. SAD ← ADR ← SRD/FIX. This roadmap (a planning artifact subordinate to SAD-001) references up into SAD/ADR/conformance.
- **Bilingual twins** — SAD/ADR/SRD/FIX get a Russian `.ru.md` twin once they reach Accepted (EN canonical). This roadmap is a Living analytics artifact, not in that set — it stays EN.

This is a **Living** document: workstreams below are updated as they advance, unlike one-shot SRDs.

## 2. Current state (baseline as of 2026-06-06)

Grounded in the code, not aspiration.

### 2.1 Implemented (real logic + tests)

- **Execution core (ADR-001 v.3 two-layer model).** `Instance` + `track` implemented (SRD-001, Accepted): one event-loop goroutine per instance is the sole state mutator; one goroutine per track; the **token is a projection** of a track's step (no stored type, no `split()`); lineage on `track.prev`. Instance lifecycle `Created → Active → Completed` (+ `Terminating → Terminated`). Token-state projection `Alive / WaitForEvent / Consumed` (`Withdrawn` reserved → ADR-005). Joins/events/long-waits are out of this core (ADR-005/006/007).
- **Event processing.** `EventHub` with the synchronous `Start` / blocking `Run` split (FIX-001, Accepted); event registration / propagation / waiter management. **Timer** waiter implemented. Race-clean under `-race` stress.
- **Scope.** Hierarchical scope tree with path-based lookup and shadowing (`internal/scope`).
- **Model elements.** Start/End events; **Exclusive** gateway (conditions, default flow); **Service** and **User** tasks; sequence flow (conditions, default); data objects, item definitions, properties, I/O specification, data associations, `FormalExpression` + Go-native evaluator; service/operation; correlation *structures*; message/resource.
- **Module skeleton.** Multi-module monorepo: core (root), `runtime/` (stub binary), `adapters/sqlite/` (doc-only scaffold), `examples/*` (working).

### 2.2 Stubbed or missing

- **Extension architecture (ADR-002):** none of the 11 interfaces are promoted to `pkg/` yet; `thresher.New` has no functional-options assembly; `RuntimeEnvironment` lives in `internal/renv`. Repository, Logger, Tracer, MetricsRecorder, Clock, MessageBroker, AuthorizationProvider, WorkerDispatcher, ExpressionEngine (as an interface) do not exist as Go types yet.
- **Module layout (ADR-003):** the `pkg/` subpackage catalogue and the 12 migration steps are not started; `runtime/` and `adapters/sqlite/` are scaffolds with no real code.
- **Persistence & rehydration (P0 per SAD §10/§13):** no `Repository`, no checkpointing, no long-wait token release, no restart recovery. Execution is in-memory and ephemeral.
- **BPMN elements:** Parallel / Inclusive / Complex / Event-Based gateways; Manual / Script / Send / Receive / Business-Rule tasks; Call Activity, (Embedded/Transaction/Event/Ad-hoc) Sub-Process; Message/Signal/Error/Escalation/Conditional/Compensation/Link/Terminate event behavior; multi-instance & loop execution — all absent or skeleton.
- **Messaging runtime:** correlation *structures* exist; no correlation engine, no Message Start/Catch/Throw routing.
- **Fault tolerance:** no Incident / Retry / DLQ.
- **Runtime overlay (ADR-004):** no server, API, tenancy, AuthN/Z wiring, diagnostics, health checks.

### 2.3 Document status & integrity

- **Statuses:** SAD-001 v.1 Draft; **ADR-001 v.3 Accepted**; ADR-002 / ADR-003 / ADR-004 v.1 Draft; **ADR-005 / ADR-006 / ADR-007 v.1 Draft** (gateways/events/long-waits, relocated from ADR-001); **SRD-001 v.1 Accepted** (instance/track/token refactor); FIX-001 v.1 **Accepted**. (ADR-008 Distribution & Scale — planned, the home for SAD §13.)
- **Document integrity:** FIX-001's earlier dead `SRD-001` reference (a *never-written* doc at the time) was repointed to the real sources (the `chore/ci-audit` `-race` gate + SAD-001 §9 / ADR-003 for the multi-module scaffold); ADR-004's legacy IAM-ADR reference is folded into the AuthN/Z model (§4.7 + `AuthorizationProvider`). A real **SRD-001** was later authored for the two-layer runtime refactor and is Accepted with its implementation (per the rule that SRD/FIX land in the same change-set as their code).

## 3. Sequencing principles

1. **Conception before the features it governs.** An ADR should be Accepted (its acceptance gate closed) before the bulk of the work it specifies lands — per the SDD discipline. Stabilising ADR-002→003→004 unblocks everything structural.
2. **Foundation before features.** Extension architecture (ADR-002) and module layout (ADR-003) are enablers the element work and the runtime both stand on. They come first.
3. **Persistence/rehydration is P0.** SAD §10/§13 make save/restore the foundation for long-waits, restart recovery, and all distribution. It is sequenced early, not deferred to "day-2".
4. **Embedded-library journey reaches MVP before the runtime overlay.** The runtime (ADR-004) is an additive overlay on a working library; the library must be usable first (SAD's two journeys).
5. **Each element lands against the spec.** Every BPMN element is implemented + tested and cross-checked against the `docs/bpmn-spec/` KB and `conformance.md`'s in-scope list.

## 4. Workstreams

Workstreams are dependency-ordered tracks. They overlap in calendar time but have the ordering constraints noted. The chain **WS-A → WS-B → (WS-C, WS-D)** is firm; WS-E and WS-F attach where noted.

### WS-A — Conception stabilization

Close each ADR's test-based acceptance gate (§7 in each) and flip Draft → Accepted, then pin outgoing references and add the Russian twin.

- Accept **ADR-001** (execution model) — **done (v.3 Accepted)**: scoped to the runtime core; §7 gate exercised and green (race-freedom, leak-free, fork, projection, completion, termination cascade); the gate's former rows for joins/withdrawn/long-wait/boundary/restart were **relocated** to ADR-005/006/007 + the Persistence ADR. Landed with SRD-001 (Accepted). Race-freedom noted as exercised in the engine (no downward FIX reference).
- Accept **ADR-002 → ADR-003 → ADR-004** in that order (linear dependency: interfaces defined → placed → wired).
- Author & accept **ADR-005 (Gateways & Joins) → ADR-006 (Events) → ADR-007 (Long Waits)** alongside their first implementations (currently Draft seeds relocated from ADR-001).
- Accept **SAD-001**: requires §13 Distribution & Scale to be refined or relocated to a dedicated **ADR-008** first (it is explicitly flagged preliminary).
- **Doc-integrity gaps cleared** (done): FIX-001's dead `SRD-001` reference (never-written at the time) repointed to the real sources; ADR-004's dead IAM-ADR reference folded into the AuthN/Z model. A real SRD-001 was later authored for the two-layer refactor and is Accepted with its code (per the rule that SRD/FIX docs land in the same change-set as their implementation).

*Output:* a stable, Accepted conception layer with version-pinned cross-references and twins.

### WS-B — Core structural foundation

The enablers everything downstream needs. Governed by ADR-002 and ADR-003; each step is its own SRD.

- **B1 Extension architecture (ADR-002).** Functional-options assembly on `thresher.New` (zero-option `New` produces a working engine); promote and extend `RuntimeEnvironment`; define the 11-interface catalogue with in-core default implementations (slog logger, no-op tracer/metrics, in-memory repository/message-broker/event-hub, wall-clock, allow-all authz, local task distributor/dispatcher, Go-native expression engine); startup configuration logging.
- **B2 Module layout migration (ADR-003, 12 steps).** Scaffold `runtime/` and `adapters/` (partly done); promote `EventHub` → `pkg/messaging/`, `RuntimeEnvironment` → `pkg/renv/`, `Registrator` → `pkg/tasks/TaskDistributor`; create the seven net-new `pkg/` subpackages with their default-impl siblings; add depguard import-direction enforcement to CI; add conformance test-helper packages; clean up emptied `internal/` dirs. Each migration step lands independently with CI green.
- **B3 Persistence & rehydration (P0).** `Repository` interface + in-memory default (`pkg/repository/memrepo/`); checkpoint at every observable BPMN lifecycle transition (ADR-001 policy); long-wait token release + rehydration on trigger; restart recovery (query in-flight instances, re-spawn). Likely needs its own SRD set and possibly an ADR refinement of the checkpoint format.

*Constraint:* B1 precedes B2 (interfaces must exist before they're placed); B3 builds on B1's `Repository` interface.

### WS-C — BPMN element completion

Fill the Common Executable Subclass + ComplexGateway per `conformance.md`, in dependency order. Each element: implement + tests + cross-check against `docs/bpmn-spec/`. Builds on WS-B (assembly, scope, and — for durable elements — persistence).

- **C1 Core flow.** None Start/End, Terminate End; **Parallel** gateway (AND, token synchronisation); Manual Task. (Exclusive gateway, Service/User tasks, sequence-flow conditions already done.)
- **C2 Errors & fault tolerance.** `BpmnError` contract; Boundary Error events with hierarchical resolution; Incident / Retry / DLQ (depends on WS-B3 persistence).
- **C3 Messaging & timers.** Message correlation engine (structures exist); Message Start/Catch/Throw; Signal events; timer **persistence + hydration** (waiter exists; hydration depends on WS-B3); **Event-Based** gateway.
- **C4 Structure & reuse.** Embedded Sub-Process (new scope level); Call Activity (variable mapping); Receive / Send tasks.
- **C5 Business logic & iteration.** Script Task (internal engine); Business Rule Task (DMN via external engine, per non-goal N2); Standard Loop and Multi-Instance (sequential/parallel) with per-branch scope isolation; Conditional events (reactive on scope-data change).
- **C6 Full conformance.** **Inclusive** (OR) and **Complex** gateway (extension; two-phase activation/reset, deadlock detection); Compensation, Escalation, Link events; Transaction and Event Sub-Process; Ad-Hoc Sub-Process.

*Output:* full BPMN 2.0 Process Execution Conformance (Common Executable + ComplexGateway), validated by the conformance suite.

### WS-D — Runtime overlay (ADR-004)

The standalone `gobpm-server`, built additively on WS-B interfaces and WS-C engine features. Each API service group is its own SRD.

- 7-phase startup + reverse-order graceful shutdown with drain.
- API service groups: process registry; instance lifecycle; user task; diagnostics (state/token-positions/history/manual intervention); event streaming; worker dispatch; health & ops.
- Tenancy via `context.Context` (Repository enforces per-tenant filtering); AuthN provider chain (OIDC/JWT/mTLS, per service group); observability wiring (OTel); health checks (liveness/readiness); hierarchical YAML config.

### WS-E — Adapters

Production implementations of the extension interfaces, each in its own `adapters/*` module, scheduled when its first consumer materialises. Each: implement the public interface, pass the conformance helper suite, declare cluster compatibility (`ClusterAware`).

- `adapters/postgres/` (Repository), `adapters/otel/` (Tracer/MetricsRecorder), `adapters/oidc|jwt|mtls/` (AuthN), `adapters/casbin/` (AuthorizationProvider), `adapters/feel/` (ExpressionEngine), `adapters/redis|nats-broker/` (MessageBroker). The existing `adapters/sqlite/` scaffold becomes a Repository adapter.

### WS-F — Distribution & scale (future)

Deferred until multi-node demand materialises; gated on WS-B3 persistence. Specified in a future **ADR-008** (the home for SAD §13's preliminary content):

- Task-level remote execution via `WorkerDispatcher` (direct dispatch, not a queue).
- Instance-level distribution: sticky routing per instance ID + failover via persistence rehydration.
- Cluster-wide shared state: signal broadcast backplane, cross-node message correlation, cluster-config validation.

## 5. Milestones

Milestones are demonstrable capability checkpoints cutting across the workstreams.

| # | Milestone | Contains | Demonstrates |
|---|---|---|---|
| **M0** | Conception accepted | WS-A | SAD-001 + ADR-001..007 Accepted; conception stable, refs pinned, twins in place. (ADR-001 + SRD-001 already Accepted with twins.) |
| **M1** | Embedded-library MVP | WS-B1, WS-B2, WS-C1 | `gobpm.New(opts...)` clean assembly; Parallel+Exclusive gateways; Service/User/Manual tasks; None/Terminate events; working example under 20 lines (SAD G3) |
| **M2** | Durable execution | WS-B3, WS-C2 | Checkpoint + restart recovery; long-wait token release/rehydration; Incidents/Retry/DLQ |
| **M3** | Messaging, time & reuse | WS-C3, WS-C4 | Message correlation; Message/Signal/Timer events; Event-Based gateway; Sub-Process & Call Activity |
| **M4** | Full conformance | WS-C5, WS-C6 | Script/Business-Rule tasks; loops & multi-instance; Inclusive/Complex gateways; Compensation/Escalation/Link; Transaction/Event/Ad-Hoc sub-processes; conformance suite green |
| **M5** | Standalone runtime | WS-D, WS-E (core adapters) | `gobpm-server` over HTTP/gRPC with postgres + otel + an AuthN provider |
| **M6** | Distribution | WS-F | Multi-node operation — when demand materialises |

## 6. References

- [SAD-001 v.1 Vision & Architecture](../design/SAD-001-vision-and-architecture.md) — the architecture this roadmap delivers.
- [ADR-001 v.3 Execution Model](../design/ADR-001-execution-model.md) — two-layer Instance + track; token as projection; ctx cancellation cascade (joins/events/long-waits/persistence relocated).
- [ADR-002 v.1 Extension Architecture](../design/ADR-002-extension-architecture.md) — 11-interface catalogue; functional-options assembly; defaults.
- [ADR-003 v.1 Module Layout](../design/ADR-003-module-layout.md) — `pkg/` subpackage catalogue; 12 migration steps; import-direction rules.
- [ADR-004 v.1 Runtime Environment Contract](../design/ADR-004-runtime-environment-contract.md) — runtime overlay; startup/shutdown; API service groups; tenancy; AuthN/Z.
- [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) — authoritative in/out-of-scope element list.
- [docs/bpmn-spec/](../bpmn-spec/) — BPMN 2.0 normative reference KB.

## Changes

### 2026-06-06

- **v3.0 — full rework.** Re-framed from BPMN-element phases to dependency-ordered workstreams (A conception → B structural foundation → C element completion → D runtime overlay → E adapters → F distribution) crossed with capability milestones (M0–M6). Added a grounded "current state" baseline (§2) and explicit sequencing principles (§3). Aligned to SAD-001 v.1 and ADR-001..004 v.1, and to the project's SDD / CI-parity / branch-protection / doc-hierarchy method. The v2.0 element ordering is preserved inside WS-C.

### 2026-05-29

- Roadmap refreshed (v2.0): aligned with SAD-001 Vision & Architecture; §1.1 expanded with Security/Observability extension categories; Phase 0 reframed (IAM/multitenancy as runtime concern); ComplexGateway noted as in-scope extension; References added.

### 2026-03-29

- v1.05: translated to English; stages synchronised with architectural GAP analysis. Added Script Task, Event Sub-Process, Complex Gateway; refined Timer Events with Non-interrupting support.
