# SRD-021 — Exclusive & Inclusive gateway split (data-based routing)

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-19 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-005 v.2 Gateways & Joins](../design/ADR-005-gateways-and-joins.md) §2.8, §2.9 |

This SRD lands the **data-based routing splits** decided in [ADR-005 v.2](../design/ADR-005-gateways-and-joins.md):
the **Exclusive (XOR) split** (§2.8 — first-true / default / exception) and the
**Inclusive (OR) split** (§2.9 — fork the true subset / default / exception),
evaluating the conditional + default sequence flows **at gateways**. The
**Inclusive OR-join** (§2.10) is the sibling SRD-022 (its own branch); the
Exclusive merge is already the non-synchronizing pass-through (ADR-005 §2.3/§2.7).
**Task-level** conditional flows (issue #51 — a task's own outgoing routed by
condition) stay out of scope: ADR-005 §2.7 still forks a task's outgoing
unconditionally; #51 is separate work that will reuse this SRD's condition helper.

## 1. Background & motivation

### 1.1 Current state (verified against the code)

- **The Exclusive split exists but deviates from §2.8 and isn't exercised.**
  `ExclusiveGateway.Exec` (`pkg/model/gateways/exclusive.go:69-117`) evaluates each
  outgoing flow's condition via `checkCondition`
  (`exclusive.go:123-150`, through `re.ExpressionEngine().Evaluate`), falls back to
  `defaultFlow` when none match (`:94-104`), and errors when there is no default
  (`:96-101`). Two deviations from §2.8 / §13.4.2:
  - It **collects every true condition and errors if more than one is true**
    (`:106-114`) — but the standard is **first-true, short-circuit** ("the first
    condition that evaluates to true … no more conditions are evaluated", §13.4.2);
    overlap is not an error.
  - It is **not direction-aware**: a *converging* Exclusive merge (a single
    conditionless outgoing) loops that outgoing, skips it (nil condition →
    `continue`, `:78-80`), finds no flows, and **fails with "no available outgoing
    flows"** (`:94-101`). A pure XOR merge is broken today.
- **It is wired but untested end-to-end.** A diverging gateway's `Exec` is called
  by `track.executeNode` → `checkFlows` (`internal/instance/track.go:507-547`,
  `:610-666`), which continues on one returned flow and forks the rest (`evFork`).
  `ExclusiveGateway` is used **only** in `internal/instance/snapshot/snapshot_test.go`
  — there is **no engine-level / example exercise** of XOR; `exclusive_test.go`
  unit-tests `Exec` for the no-outgoing / with-default / normal cases only (no
  overlap, no merge).
- **There is no Inclusive gateway.** `pkg/model/gateways/` has `gateway.go`
  (base: `direction`, `defaultFlow`, `DefaultFlowHolder`), `exclusive.go`,
  `parallel.go` — **no `inclusive.go`**. The OR split has no model and no runtime.
- **Conditions + default flows are modelled, not yet exercised by routing.**
  Sequence flows carry `conditionExpression data.FormalExpression`
  (`pkg/model/flow/sequenceflow.go:50`, `Condition()` `:295-297`,
  `flow.WithCondition`); gateways hold a default via `DefaultFlowHolder`
  (`gateway.go:151-193`). The gateways evaluating these conditions is what this
  SRD delivers; task-level conditional routing (#51) is separate (tasks still fork
  all outgoing — every task `Exec` returns `Outgoing()` unconditionally, e.g.
  `service_task.go:177`, per ADR-005 §2.7).

### 1.2 Problem

A process cannot branch on data: XOR's split errors on a merge and on condition
overlap, is never run through the engine, and OR has no gateway at all. This SRD
reconciles the Exclusive split to §2.8, adds the Inclusive split (§2.9), and
proves both end-to-end.

## 2. Decision

- **Exclusive split → §2.8.** Make `ExclusiveGateway.Exec` **first-true,
  short-circuit** (take the first outgoing whose condition is true and stop), use
  the **default** when none match, and **fail the instance** only when none match
  *and* there is no default — replacing the collect-all / error-on-overlap logic.
- **Direction-aware `Exec`.** A diverging gateway (more than one outgoing) selects
  by condition; a **pass-through** gateway (a converging merge / a single outgoing)
  returns its outgoing flow(s) unconditionally — no condition evaluation, no
  "no available outgoing flows" error. This fixes the XOR merge.
- **Inclusive split (new `InclusiveGateway`).** A new gateway type mirroring
  `ExclusiveGateway` whose diverging `Exec` returns the **subset of outgoing flows
  whose condition is true** (≥1), falling back to the default, failing when none +
  no default (§2.9). `checkFlows` forks that subset exactly as a Parallel split.
- **Shared condition evaluation.** The condition-evaluation helper (`checkCondition`)
  moves to the base `Gateway` so Exclusive and Inclusive share one
  ExpressionEngine-backed, bool-typed evaluation (no duplication).
- **OR-join is out of scope (SRD-022).** This SRD builds the Inclusive gateway's
  **split** only. A *converging* Inclusive gateway (the OR-join, ADR-005 §2.10)
  needs the reachability + re-evaluation machinery of SRD-022; until it lands a
  converging Inclusive gateway is unsupported (documented, not silently mis-merged).

## 3. Functional requirements

- **FR-1 — Exclusive first-true split.** `ExclusiveGateway.Exec` returns the
  **first** outgoing flow whose condition evaluates `true` and evaluates no
  further conditions (§2.8 / §13.4.2). Overlapping true conditions are **not** an
  error (the prior `>1` error path is removed).
- **FR-2 — direction-aware pass-through.** When the gateway is not diverging
  (a converging merge or a single outgoing flow), `Exec` returns its outgoing
  flow(s) unconditionally — the non-synchronizing pass-through (§2.3) — instead of
  failing on a conditionless outgoing.
- **FR-3 — default + exception (Exclusive).** When no condition is true, the
  **default** flow is taken; when no condition is true and there is no default, the
  gateway fails the instance with a classified error (§2.8 / §13.4.2).
- **FR-4 — Inclusive split.** A new `InclusiveGateway` (`pkg/model/gateways/inclusive.go`)
  implements `exec.NodeExecutor`; its diverging `Exec` returns **every** outgoing
  flow whose condition is `true` (the true subset, ≥1), with the same default /
  exception fallback as FR-3 (§2.9). `checkFlows` forks the subset.
- **FR-5 — shared condition evaluation.** Condition evaluation (bool-typed, via
  `re.ExpressionEngine().Evaluate`) lives once on the base `Gateway` and is used by
  both gateways; a non-bool condition result is a classified error.
- **FR-6 — gateway conditional + default flows end-to-end.** A process with
  condition-bearing outgoing flows and a default flow on a gateway routes correctly
  through the engine (not just in a model unit test). (Task-level conditional flows
  — #51 — are out of scope; see §1.1.)
- **FR-7 — Inclusive OR-join out of scope.** `InclusiveGateway` does **not**
  implement `exec.SynchronizingJoin` in this SRD; a converging Inclusive gateway is
  unsupported until SRD-022. Documented on the type.

## 4. Non-functional requirements

- **NFR-1 — standard-grounded routing.** Exclusive = exactly one out (first-true /
  default), Inclusive = the true subset (≥1) — per ADR-005 §2.8/§2.9, §13.4.2/§13.4.3.
- **NFR-2 — no new fork machinery.** Both splits feed the existing
  `Exec → checkFlows → evFork` path (ADR-005 §2.7) unchanged; the only change is
  *which* flows `Exec` returns.
- **NFR-3 — coverage.** Touched files finish ≥80% (target 100%) diff-coverage;
  `make ci` green.

## 5. Path analysis (alternatives)

- **Reconcile `ExclusiveGateway.Exec` (chosen) vs a new exclusive node.** Chosen:
  the existing `Exec` already evaluates conditions + default; reconciling it to
  first-true + direction-aware is smaller and keeps one Exclusive type. Rejected a
  rewrite.
- **First-true short-circuit (chosen) vs error-on-overlap (current) vs take-all.**
  Chosen: §13.4.2 is explicit — first true wins, no more evaluated; overlap is a
  modelling choice the spec resolves by order, not an engine error. Rejected the
  current error (non-conformant) and take-all (that is Inclusive, not Exclusive).
- **Direction-aware Exec discriminating on outgoing count (chosen) vs the gateway
  `direction` field vs a separate merge path.** Chosen: `>1 outgoing` → conditional
  select; `≤1 outgoing` → pass-through — robust without depending on the
  `direction` field being set, and a merge (1 outgoing) passes through cleanly. The
  exact discriminator (count vs `direction`) is pinned at implementation.
- **New `InclusiveGateway` mirroring `ExclusiveGateway` (chosen) vs a shared
  parametric gateway.** Chosen: a distinct type per ADR-005 §2.1 (per-type
  behaviour, no central switch); the shared part (condition eval) is factored to
  the base. Rejected a parametric "routing gateway" with a mode flag (a hidden
  switch).
- **OR-join in this SRD (rejected).** Its reachability + re-evaluation machinery
  (ADR-005 §2.10) is substantial and deserves a focused SRD-022; the split is
  independently useful and testable.

## 6. API & key shapes

```go
// pkg/model/gateways/inclusive.go (new) — mirrors ExclusiveGateway:
type InclusiveGateway struct{ Gateway }
func NewInclusiveGateway(opts ...options.Option) (*InclusiveGateway, error)
func (ig *InclusiveGateway) Exec(ctx, re) ([]*flow.SequenceFlow, error) // true subset / default / exception
func (ig *InclusiveGateway) Clone() flow.Node
func (ig *InclusiveGateway) Node() flow.Node
var _ exec.NodeExecutor = (*InclusiveGateway)(nil)   // NOT SynchronizingJoin (SRD-022)

// pkg/model/gateways/gateway.go — condition eval moves to the base:
func (g *Gateway) checkCondition(ctx, re, cond data.FormalExpression, of *flow.SequenceFlow) (bool, error)

// pkg/model/gateways/exclusive.go — Exec reconciled to §2.8 (first-true,
// short-circuit, direction-aware pass-through; default; exception).
```

No new public engine surface; processes author gateways with the existing
`NewExclusiveGateway` / new `NewInclusiveGateway` + `flow.WithCondition` +
`UpdateDefaultFlow`.

## 7. Test plan

- **`TestExclusiveSplitFirstTrue`** (model unit) — overlapping true conditions →
  the **first** flow is returned, not an error (FR-1).
- **`TestExclusivePassThrough`** (model unit) — a single conditionless outgoing
  (a merge) → returned unconditionally, no error (FR-2).
- **`TestExclusiveDefaultAndException`** (model unit) — none true → default; none
  true + no default → classified error (FR-3).
- **`TestInclusiveSplitSubset`** (model unit) — multiple true → **all** true flows
  returned; none true → default; none + no default → error (FR-4).
- **`TestInclusiveConvergingUnsupported`** — a converging Inclusive gateway is
  rejected/flagged (FR-7).
- **Engine-level (`pkg/thresher` or `internal/instance`)** —
  `TestExclusiveRoutingEndToEnd` (a process: start → XOR → {A cond true | B
  default} → end; the true branch runs, the other doesn't) and
  `TestInclusiveSplitEndToEnd` (start → OR-split → forks the true subset, branches
  run concurrently and complete) — proving gateway conditional routing end-to-end.
- A runnable `examples/exclusive-routing` (or extend an example) smoke-runs exit 0.

## 8. Cross-document consistency

- **Implements** [ADR-005 v.2](../design/ADR-005-gateways-and-joins.md) §2.8
  (Exclusive split), §2.9 (Inclusive split); the Exclusive merge is §2.3/§2.7
  (non-synchronizing pass-through), the Inclusive OR-join is §2.10 (SRD-022).
- [ADR-001 v.5](../design/ADR-001-execution-model.md) — the fork mechanic
  (`Exec → checkFlows → spawn`) the splits feed.
- [ADR-010 v.1](../design/ADR-010-process-data-model.md) — the per-execution data
  the conditions read (`re` data source).
- References up/sideways, version-pinned; no downward refs (ADR-005 does not cite
  SRD-021).

## 9. Definition of Done

- FR-1…FR-7 wired and exercised by the §7 tests, including the engine-level
  gateway routing tests.
- `ExclusiveGateway.Exec` is first-true + direction-aware; `InclusiveGateway`
  exists with a subset-forking `Exec` and no `SynchronizingJoin`; `checkCondition`
  shared on the base.
- `make ci` green (tidy, lint, build, `-race`, diff-coverage ≥95, govulncheck);
  touched files ≥80% (target 100%).
- A runnable routing example smoke-runs exit 0; the existing examples still exit 0.
- §10 filled; status → Accepted; RU twin added; ADR-005 flipped to Accepted v.2
  (its conception is now implemented, OR-join excepted — pending SRD-022); linked
  docs synced.

## 10. Implementation summary

> ⚠️ TODO: fill AFTER landing — commits, key files, V-results, deltas vs this draft.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-19 | Ruslan Gabitov | Draft. Lands the data-based routing **splits** of ADR-005 v.2: reconciles `ExclusiveGateway.Exec` to §2.8 (**first-true short-circuit** replacing collect-all/error-on-overlap; **direction-aware pass-through** fixing the broken converging merge; default/exception kept) and adds a new `InclusiveGateway` whose diverging `Exec` forks the **true subset** (§2.9), with `checkCondition` factored to the base `Gateway` for both. Gateway conditional + default sequence flows exercised end-to-end; task-level conditional flows (#51) stay out of scope (tasks still fork all outgoing, ADR-005 §2.7) — separate work reusing this SRD's condition helper. The Inclusive **OR-join** (§2.10) is excluded — sibling SRD-022 — so `InclusiveGateway` implements `NodeExecutor` but not `SynchronizingJoin`; a converging Inclusive gateway is unsupported until then. Code-grounded against `pkg/model/gateways` (gateway.go/exclusive.go/parallel.go), `pkg/model/flow` (sequenceflow.go), `internal/instance` (track.go executeNode/checkFlows). Implements ADR-005 v.2 §2.8/§2.9; refs ADR-001 v.5, ADR-010 v.1. |
