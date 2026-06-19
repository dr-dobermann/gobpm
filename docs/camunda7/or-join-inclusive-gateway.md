# Camunda 7 — Inclusive Gateway join (OR-join) implementation

Reference analysis of how Camunda 7 implements the converging Inclusive Gateway, compared against the normative BPMN 2.0.2 activation rule. Useful as a worked reference for implementing the synchronizing merge in `gobpm`.

**Sources:**
- Spec: OMG BPMN 2.0.2 (formal/2013-12-09), §13.4.3, Table 13.3 «Inclusive Gateway Execution Semantics», pp. 435–436.
- Engine: `org.camunda.bpm.engine.impl.bpmn.behavior.InclusiveGatewayActivityBehavior` and `GatewayActivityBehavior` from `camunda-engine-7.22.0-sources.jar` (line numbers below refer to these files).
- Spec-side KB entry: [`../bpmn-spec/semantics/gateways.md`](../bpmn-spec/semantics/gateways.md) — Inclusive Gateway section.

---

## 1. The normative rule (BPMN 2.0.2 §13.4.3, Table 13.3)

> The Inclusive Gateway is activated if
> - At least one incoming Sequence Flow has at least one token **and**
> - For every directed path formed by sequence flow that
>   - starts with a Sequence Flow *f* of the diagram that has a token,
>   - ends with an incoming Sequence Flow of the inclusive gateway that has **no** token, and
>   - does not visit the Inclusive Gateway.
>
>   There is **also** a directed path formed by Sequence Flow that
>   - starts with *f*,
>   - ends with an incoming Sequence Flow of the inclusive gateway that **has** a token, and
>   - does not visit the Inclusive Gateway.

Key reading of the second condition (the *refinement clause*): an upstream token blocks the join **only if** it can still reach an *empty* incoming flow **and cannot** alternatively reach an already-*marked* incoming flow (both without passing through the gateway). A token that could end up either way is assumed to potentially take the route to the marked flow and does **not** block. This is the "wait for everything that can still possibly arrive, and only for that" rule — the *synchronizing merge* (WCP-7) of the workflow-patterns literature.

The rule is **non-local**: it is the only gateway whose firing decision requires inspecting global token distribution across the whole process instance, not just the gateway's own incoming flows.

On firing, per the same table: a token is consumed **from each incoming flow that has one**; all outgoing conditions are evaluated (no ordering guarantee); a token is produced on every flow whose condition is true; the default flow is taken iff all conditions are false; if all are false and there is no default — the gateway throws an exception.

Note: the spec contains **no normative text about cyclic graphs / "vicious circles"** at the OR-join. That problem (the firing decision becoming self-referential in cyclic models) is discussed only in academic literature; the spec leaves such structures undefined.

## 2. Camunda 7 implementation

Class: `InclusiveGatewayActivityBehavior` (≈200 lines). A "token" in the PVM is an `ExecutionEntity`; the join state is persisted as *inactive concurrent executions* parked at the gateway activity.

### 2.1 Token arrival

`execute(ActivityExecution)` — entry point, runs each time a token arrives (lines 46–91):

```java
execution.inactivate();          // park this token at the gateway
lockConcurrentRoot(execution);   // forceUpdate() on the concurrent-root execution

if (activatesGateway(execution, activity)) {
    // ... fire: evaluate outgoing conditions, consume joined executions
} else {
    // ... wait: token stays parked, nothing else happens
}
```

`lockConcurrentRoot` (in `GatewayActivityBehavior`, lines 30–38) calls `forceUpdate()` on the parent of the concurrent branches. This bumps the optimistic-lock revision of that row even though nothing changed, so two transactions joining at the same gateway concurrently are forced to collide: one commits, the other rolls back with `OptimisticLockingException`. There is no real lock — concurrency safety is entirely serialization through the version column.

### 2.2 Activation check

`activatesGateway()` (lines 107–130) — Camunda's translation of the spec rule:

1. **Fast path** (line 113): count inactive executions parked at the gateway via `findInactiveConcurrentExecutions(gatewayActivity)`. If `count >= |incoming sequence flows|` — fire. All inputs covered, no graph analysis.
2. **Slow path** (lines 117–127): collect all *leaf* executions in the gateway's scope (`getLeafExecutions`, recursing through `getNonEventScopeExecutions()` — executions belonging to event scopes, e.g. parked boundary-event subscriptions, are excluded), subtract those already parked at the gateway, and for each remaining execution ask `canReachActivity()`. **If any one can still reach the gateway — wait.** If none can ("no more token may arrive") — fire.

### 2.3 Reachability

`canReachActivity()` / `isReachable()` (lines 132–200) — plain DFS over the **static** process graph:

- Start position: the execution's current activity, or — if the execution is mid-transition — the transition's *destination* (line 135).
- A `visitedActivities` set guards against infinite traversal (lines 146–152), so **cycles in the model don't hang the engine**; the vicious-circle problem is sidestepped pragmatically, not solved semantically.
- Two special cases when an activity has no outgoing sequence flows:
  - an **event-based gateway** is traversed into its event activities (`getEventActivities()`, lines 158–169);
  - an activity at the **end of an embedded subprocess** climbs up via `getFlowScope()` and continues from the subprocess's outgoing flows (lines 174–177) — a token deep inside an upstream subprocess correctly blocks the join.

### 2.4 Firing

Lines 56–87, all in one transaction with the join decision:

1. `joinedExecutions = execution.findInactiveConcurrentExecutions(activity)` — every token parked at the gateway.
2. Evaluate conditions on all outgoing flows except the default; collect every flow whose condition is absent or true.
3. If none matched: take the default flow if declared, else throw the "stuck execution" engine exception.
4. `execution.leaveActivityViaTransitions(transitionsToTake, joinedExecutions)` — consume all joined executions (prune the execution tree) and produce new concurrent executions per activated outgoing flow. Join and split are atomic.

## 3. Deviations from the spec

| # | Spec | Camunda 7 | Consequence |
|---|------|-----------|-------------|
| 1 | Refinement clause: a token that can also reach a *marked* incoming flow does not block | **Not implemented** — any execution that `canReachActivity()` at all blocks the join | Strictly *more conservative* than spec: in topologies where a token has paths to both a marked and an unmarked incoming flow, spec says fire, Camunda waits (and fires later, when the token actually arrives) |
| 2 | Tokens counted per incoming flow (a flow "has at least one token") | Fast path compares *number of parked executions* vs *number of incoming flows*, regardless of which flow each arrived by | Two tokens arriving via the same incoming flow count as 2; in looping models the join can fire *earlier* than a strict spec reading |
| 3 | Path conditions are over the token's possible futures | Reachability DFS is purely structural — sequence-flow conditions are ignored | A token whose only route to the gateway runs through a condition that would evaluate to false still blocks the join (future condition outcomes are unknowable at decision time) |
| 4 | Activation is a property of the marking (implicitly continuous) | `activatesGateway` is evaluated **only on token arrival** at the gateway (sole call site: `execute()`) | If the last potentially-blocking token is *cancelled* elsewhere (e.g. an interrupting boundary event kills the awaited branch), nothing re-evaluates the join — parked tokens stay stuck until another token arrives or manual intervention. The classic OR-join production pitfall in Camunda 7 |
| 5 | — | Decision is snapshot-based per transaction; `forceUpdate` on the concurrent root serializes concurrent joiners via optimistic locking | A contended OR-join without `asyncBefore` surfaces `OptimisticLockingException` to the API caller; with `asyncBefore` collisions become automatic job-executor retries — the recommended pattern for high contention |

## 4. Takeaways for a `gobpm` OR-join

- The two-tier check (cheap counter first, graph reachability only when the counter falls short) is a sound performance structure worth replicating.
- Decide explicitly whether to implement the refinement clause (spec-exact, more complex: per-token *pair* of reachability questions) or Camunda's conservative approximation (single reachability question per token). The conservative variant is simpler and only ever errs toward waiting longer — but combined with deviation #4 it converts "waits longer" into "waits forever" when the awaited branch dies.
- Deviation #4 is the real production trap: re-evaluating the join on **token death** (branch cancellation, interruption), not only on token arrival, removes the worst failure mode of the Camunda design.
- Count markings per incoming flow rather than per gateway if loop-containing models should follow the spec strictly (deviation #2).
