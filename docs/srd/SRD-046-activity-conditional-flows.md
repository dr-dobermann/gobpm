# SRD-046 — Activity-outgoing conditional and default flows

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-15 |
| Owner | Ruslan Gabitov |
| Implements | BPMN 2.0 sequence-flow semantics at activity completion — the vendored extract `docs/bpmn-spec/semantics/token-flow.md` §"Multiple outgoing sequence flows on an activity" (GitHub issue #51) |
| Upstream | [ADR-001 v.6](../design/ADR-001-execution-model.md) (the track/fork execution model — the consumer of the selected flows), [ADR-005 v.4](../design/ADR-005-gateways-and-joins.md) (the gateway condition-evaluation idiom this mirrors), [ADR-010 v.2](../design/ADR-010-process-data-model.md) (the data source conditions evaluate over) |
| Refines | — |

Today `flow.Condition()` is honored **only by gateways** (`gateway.go:221-302`,
`exclusive.go:91`, `event_based.go:396`). Every activity's `Exec` returns
`Outgoing()` unfiltered and the track forks **all** of them — while the model
happily **accepts** what the engine ignores: `flow.WithCondition` attaches to
any `flow.Link(...)` call regardless of source type, and
`activity.SetDefaultFlow` exists (`activity.go:143`) but nothing reads it
(dead today, grounding-verified). A modeller's condition or default on
a task-outgoing flow is **silently dropped** — the silent-misbehavior class the
engine's fail-fast posture forbids.

The standard's rule (the vendored `token-flow.md`, quoted):

> When an activity transitions to **Completed**, all of its outgoing sequence
> flows receive a token. Behavior depends on the conditions:
> | All outgoing unconditional | Parallel split — all branches activated. |
> | All outgoing have `conditionExpression` | Inclusive split — each true condition produces a token. |
> | Mix of unconditional + conditional | Combination — unconditional always fire, conditional fire when their condition is true. |

## 1. Background

- **The engine gap.** Activity `Exec`s return raw `Outgoing()` at 8 sites
  across 5 files (`service_task.go:269,388,400,470`, `send_task.go:147`,
  `user_task.go:210`, `receive_task.go:200`, `manual_task.go:72`); the track's
  `checkFlows` forks exactly what `Exec` returned, no re-filtering
  (`track.go:985-1056`) — so **activity-side selection needs zero engine
  change**, the same locus gateways already use (nodes own their routing).
- **The model half-exists.** `activity.defaultFlow` + `SetDefaultFlow`
  (outgoing-membership validated, empty clears) are present but dead;
  `SetDefaultFlow` misses the no-condition rule `gateways.UpdateDefaultFlow`
  enforces (`gateway.go:182-186`).
- **A clone hazard (grounding find).** `activity.clone` shares `defaultFlow`
  **by reference** (`activity.go:120`) while instances clone their node graphs
  — a pointer-identity default check would silently stop matching on a cloned
  instance. Selection must compare **by flow ID**.
- **The evaluation idiom exists.** `gateways.checkCondition`
  (`gateway.go:221-248`): require `ResultType() == "bool"`, evaluate via
  `re.ExpressionEngine().Evaluate(ctx, cond, re)` (the `re` is the
  `data.Source`), classified errors carrying node/flow ids. Every activity
  `Exec` already holds the same `ctx, re` pair.

## 2. Requirements

### Functional

- **FR-1 — the selection rule at every activity completion.** A shared
  `activity.selectOutgoing(ctx, re) ([]*flow.SequenceFlow, error)` implements
  the standard's table:
  - an **unconditional** non-default flow → always selected;
  - a **conditional** flow → selected iff its condition evaluates `true`;
  - the **default** flow → selected iff **no conditional flow** was selected
    (with no conditional flows present, vacuously selected);
  - a **single outgoing flow** (or none) short-circuits unfiltered — the
    overwhelmingly common case pays nothing (the gateway pass-through
    precedent, `gateway.go:258-260`).
- **FR-2 — the evaluation idiom mirrors gateways.** Bool-typed conditions
  only; a non-bool `ResultType` or a failed evaluation is a classified error
  naming the activity and flow ids (the `checkCondition` shape).
- **FR-3 — every Exec return site selects.** All 8 unfiltered
  `return X.Outgoing(), nil` sites (§1) become
  `return X.selectOutgoing(ctx, re)` — including the ServiceTask worker-resume
  (`bindOutput`/`writeStatus`) and UserTask completion paths.
- **FR-4 — zero-selected is a classified error (engine choice).** All-flows
  conditional + all false + no default → a classified error (→ the track's
  fault path). The extract prescribes this **for gateways only** and is
  silent for activities — recorded as an engine choice: gateway parity
  (`gateway.go:293-299`) and Camunda 7 alignment (it throws), consistent with
  the no-hidden-dead-token posture. Unreachable when ≥1 unconditional flow
  exists.
- **FR-5 — default-flow hardening.** `SetDefaultFlow` additionally rejects a
  flow **carrying a condition** (the BPMN rule `UpdateDefaultFlow` already
  enforces for gateways); selection identifies the default **by ID** (the §1
  clone hazard). A `DefaultFlow()` getter is added for symmetry with gateways.
- **FR-6 — tests + changelog.** Unit tests on the selection rule (the
  inclusive-gateway test harness shape), an engine e2e (a task's conditional
  flows route a real instance), the changelog entry. No new example — the
  behavior composes into existing ones; `docs/guides` is data-scoped.

### Non-functional

- **NFR-1 — zero engine change.** No edits in `internal/` or to gateways;
  the track consumes the selected flows as today.
- **NFR-2 — no behavior change for the plain case.** One outgoing
  unconditional flow (virtually every existing model/test/example) takes the
  short-circuit — byte-identical behavior, no evaluation cost.
- **NFR-3 — events out of scope.** The extract's rule binds to *activity*
  completion; it is silent on event-outgoing conditions. Events keep today's
  behavior (conditions ignored); a registration-time rejection is a possible
  follow-up, not S46.
- **NFR-4 — `completionQuantity` out of scope.** The extract's "the number of
  tokens placed on each outgoing flow is `completionQuantity` (default 1)"
  rides the loop-characteristics/multi-instance model gobpm doesn't implement
  yet (epic #88); S46 places one token per selected flow — the default-1
  behavior, explicitly noted rather than silently narrowed.

## 3. Models

### 3.1 The selector (`pkg/model/activities/activity.go` + a new `flowselect.go`)

```go
// selectOutgoing applies the BPMN activity-completion rule to the activity's
// outgoing flows (token-flow.md §"Multiple outgoing sequence flows"):
// unconditional flows always fire; conditional flows fire when true; the
// default fires only when no conditional fired. One (or no) outgoing flow
// short-circuits unfiltered.
func (a *activity) selectOutgoing(
    ctx context.Context, re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
    out := a.Outgoing()
    if len(out) <= 1 {
        return out, nil
    }

    selected := []*flow.SequenceFlow{}
    conditionalFired := false

    for _, of := range out {
        if a.defaultFlow != nil && of.ID() == a.defaultFlow.ID() { // by ID — clone-robust
            continue // decided after the loop
        }

        cond := of.Condition()
        if cond == nil {
            selected = append(selected, of) // unconditional → always
            continue
        }

        ok, err := a.checkCondition(ctx, re, cond, of) // the gateway idiom
        if err != nil {
            return nil, err
        }

        if ok {
            selected = append(selected, of)
            conditionalFired = true
        }
    }

    if a.defaultFlow != nil && !conditionalFired {
        selected = append(selected, a.defaultFlow) // the activity's own object
    }

    if len(selected) == 0 {
        return nil, errs.New(
            errs.M("no outgoing flow selected: all conditions false and "+
                "no default flow"),
            errs.C(errorClass, errs.InvalidState),
            errs.D("activity_id", a.ID()),
            errs.D("activity_name", a.Name()))
    }

    return selected, nil
}
```

`checkCondition` mirrors `gateways.checkCondition` (bool `ResultType`,
`re.ExpressionEngine().Evaluate(ctx, cond, re)`, classified errors with
activity/flow ids) — a deliberate ~25-line cross-package duplication; hoisting
a shared evaluator would create a new common home for two callers (§4.3).

### 3.2 Default-flow hardening (`activity.go`)

`SetDefaultFlow(flowID)` gains (after the existing outgoing-membership check):

```go
if o.Condition() != nil {
    return errs.New(
        errs.M("default flow %q must not carry a condition", flowID),
        errs.C(errorClass, errs.InvalidParameter))
}
```

plus `func (a *activity) DefaultFlow() *flow.SequenceFlow` (the gateway-getter
symmetry). The pre-existing typo in the membership error (`"dosn't existed"`,
`activity.go:161`) is fixed in the same touch — FR-5 makes that message live
for the first time (the check-comment-vs-code rule).

### 3.3 Worked example

`quote` completes with four outgoing flows: `→ ship` (unconditional),
`→ audit [total > 1000]`, `→ discount [total > 100]`, `→ review` (default).

| `total` | Selected |
|---|---|
| 1500 | ship, audit, discount (conditionals true; default suppressed) |
| 150 | ship, discount |
| 50 | ship, **review** (no conditional fired → default) |

With `ship` absent and `total = 50` and **no** default → the FR-4 classified
error faults the track.

## 4. Analysis & decisions

### 4.1 Selection at the activity, not the track

Gateways already own their routing (their `Exec` returns the *chosen* flows);
activities join the same contract, and `checkFlows` stays untouched (NFR-1).
*Alternative:* evaluate in `track.checkFlows` — rejected: it would re-filter
gateway-returned flows (an exclusive gateway's chosen flow *carries* the very
condition that selected it — double evaluation, possibly against changed data)
and would put model semantics in the engine layer.

### 4.2 Default selected by ID, not pointer

`activity.clone` shares `defaultFlow` by reference while the instance's node
graph is cloned — pointer identity would silently unmatch on clones (the §1
hazard). Gateways carry the same latent pattern (`of == g.defaultFlow`,
`gateway.go:269`, on cloned instances per ADR-009) — a named FIX candidate,
out of S46's scope. ID comparison is clone-robust and costs one string
compare. The *appended* object
is the activity's own `defaultFlow` reference (the same object its `Outgoing()`
holds on that clone — verified by the e2e).

### 4.3 Duplicate the 25-line evaluator, don't hoist it

`checkCondition` exists in `gateways`; activities get their own copy.
*Alternative:* hoist into a shared package (`flow`? a new one) — rejected for
S46: `flow` cannot import `renv` (cycle), a new package for one 25-line
function serving two callers is speculative structure; noted as a by-need
refactor if a third caller appears.

### 4.4 Zero-selected errs — an engine choice, marked as such

The extract's all-false-no-default **exception** clause exists for gateways
only; for activities it is **silent** (verified). Choosing the error (over
silently ending the track) is gateway parity + Camunda 7 alignment + the
engine's no-hidden-outcomes posture: a token that silently vanishes on an
activity with outgoing flows is undiagnosable. Recorded here, not claimed as
a standard mandate.

### 4.5 Events keep today's behavior

The activity-completion rule binds to activities; the extract says nothing
about conditions on event-outgoing flows. Start events return all outgoing
(parallel), end events none — unchanged. Rejecting event-flow conditions at
registration is a separate, smaller follow-up if wanted.

## 5. API / contract

- **Changed (additive)**: `activities` — every task's multi-flow completion
  now honors conditions/default (previously silently ignored — a behavior
  *fix*, not a break: any model relying on the old behavior was relying on a
  standard violation); `SetDefaultFlow` rejects conditional flows (stricter —
  previously accepted-and-ignored); new `DefaultFlow()` getter.
- **Unchanged**: gateways, `internal/`, `flow.Link`/`WithCondition`, events.

## 6. Test scenarios

| # | Test | Covers |
|---|---|---|
| T-1 | `TestSelectOutgoing` (activities) | every §3.3 row: all-unconditional (parallel, incl. the ≤1 short-circuit), all-conditional (true subset), mix, default suppressed by a fired conditional, default fires on none, vacuous default, zero-selected → classified error, non-bool condition → error, failing evaluation → error |
| T-2 | `TestSetDefaultFlowRejectsConditional` (activities) | the FR-5 hardening + `DefaultFlow()` getter; empty-clear and unknown-flow behaviors retained |
| T-3 | `TestActivityConditionalFlowsE2E` (thresher) | a real instance: a task with conditional + default outgoing flows routes per §3.3 (incl. on a **cloned** instance — the by-ID robustness); the all-false-no-default case faults the instance |
| T-4 | every existing test/example | unchanged (NFR-2) — `make ci` green is the proof |

## 7. Milestones

| # | Scope |
|---|---|
| **M1** | FR-1/2/4/5: `selectOutgoing` + `checkCondition` + `SetDefaultFlow` hardening + `DefaultFlow()`. T-1, T-2. |
| **M2** | FR-3: the 8 Exec sites; the e2e (T-3). |
| **M3** | Changelog; §10; `/check-srd`; Accepted; sync; the PR closes #51. |

## 8. Cross-doc

| Ref | Version | Direction | Role |
|---|---|---|---|
| ADR-001 | v.6 | up | the track/fork model consuming the selected flows |
| ADR-005 | v.4 | up | the gateway condition idiom mirrored (gateways untouched) |
| ADR-010 | v.2 | up | the data plane conditions evaluate over |

## 9. Definition of Done

- [x] FR-1..FR-6 wired; every §6 test exists and is green.
- [x] Zero changes in `internal/` and `pkg/model/gateways` (NFR-1).
- [x] `make ci` green; diff-coverage ≥95% (aim 100%); full `-race`.
- [x] SRD-046 flipped to Accepted; changelog entry; issue #51 closed by the PR.
- [x] §10 filled with milestone SHAs and deltas.

## 10. Implementation summary

Landed on `feat/activity-conditional-flows` in three milestones.

### 10.1 Milestones

| # | Commit | Scope | Tests |
|---|---|---|---|
| doc | `2f77a48` | this SRD | — |
| M1 | `0065425` | `selectOutgoing` + `checkCondition` (`flowselect.go`), `SetDefaultFlow` hardening + typo fix + `DefaultFlow()` | `TestSelectOutgoing`, `TestSetDefaultFlowHardening` |
| M2 | `bdb5aa0` | the 8 Exec sites wired; `bindOutput` gains `ctx`; ManualTask/UserTask discarded params named | `TestActivityConditionalFlowsE2E` |
| M3 | (this) | changelog `[Unreleased]`; §10; Accepted flip | `make ci` (T-4) |

All touched functions at 100% coverage; `make ci` green at each milestone
(M2: diff-coverage 100% of 70 changed lines).

### 10.2 Deltas vs the §3 draft

- **`bindOutput` gained a `ctx` parameter.** The grounding said all 8 sites
  had `ctx` in scope; `bindOutput(re, res)` itself did not — its caller
  (`execWorkerOutcome`) did, so the context is threaded one level (an
  internal-method signature change).
- **Two discarded parameters became used.** `ManualTask.Exec(_, _)` and
  `UserTask.Exec(_, re)` now name `ctx`/`re` — the selection needs them.
- Everything else landed exactly per the §3 model (by-ID default, the
  short-circuit, the classified errors, the gateway-idiom duplication).

## Open questions

None. The selection locus (activity), the zero-selected engine choice, the
by-ID default matching, the evaluator duplication, and the events non-goal are
decided above. The gateway clone-pointer latency (§4.2) and event-flow
condition rejection are named follow-ups, not S46 questions.
