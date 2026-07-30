# BPMN Conformance Status — implementation tracker

| Field | Value |
|---|---|
| Type | **Continuously-current tracker** (not an SRD/ADR — updated as elements land, in the landing PR) |
| Scope authority | [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) — Common Executable Subclass + the ComplexGateway extension |
| Last verified | 2026-07-30, **human-task ownership landed** (ADR-020 v.2 / SRD-073 — `actualOwner` §10.3.4.1 Table 10.14, the first *instance* attribute the engine implements; §1 Human interaction + §3 leftovers); Ad-Hoc Sub-Process landed (ADR-035 / SRD-074 — #92 row 13 ✅, the last executable element in scope); prior full re-sweep post-SRD-071/072 (Persistence & State slices — #84 row 12 ✅; #79 row 10 closed 2026-07-22; §1 caught up with the row-1…11 landings; §5 order updated) |
| Owner | Ruslan Gabitov |

Status vocabulary: ✅ **executed** (model type + engine semantics + tests) ·
🟡 **model-only** (a type exists, no execution wiring) · ❌ **absent** ·
📐 **registered deviation** (a deliberate engine choice in SAD-001 §14).

## 1. Conformant today

| Family | Elements | Landed via |
|---|---|---|
| Process container | `Process` (executable, versioned registration) | core; ADR-019 |
| Activities | `ServiceTask` (in-process + external workers), `UserTask`, `ManualTask`, `SendTask`, `ReceiveTask`, `ScriptTask` (multi-engine seam + the Lua battery), `BusinessRuleTask` (rule-engine seam + `gorules` + the Decision Table adapter), the abstract `Task` base, **`SubProcess` (embedded)** — nested scope in the instance: §13.3.4 shapes + drain, §10.5.7 data visibility, scoped Terminate (§13.5.6), boundary-on-composite, the Error scope-chain — **`CallActivity`**, **Event Sub-Process** (interrupting + non-interrupting), **`Transaction`** (Cancel-driven atomic abort), **Ad-Hoc** (§13.3.5 — Router-driven succession over a flow-less inner set, manual selection, `completionCondition`) | ADR-021/SRD-035…039; ADR-020/SRD-034; SRD-013/014; ADR-031/SRD-064/065; ADR-027/SRD-060, ADR-029/SRD-062; ADR-023/SRD-049/050/052/053; ADR-028/SRD-061; ADR-035/SRD-074 |
| Gateways — **all five** | `Exclusive`, `Parallel`, `Inclusive` (incl. the OR-join), `EventBased` (incl. Exclusive/Parallel instantiating starts), `Complex` (the declared extension) | SRD-005, SRD-021/022, SRD-023, SRD-024/025 |
| Events (positions) | `StartEvent`, `EndEvent`, `IntermediateCatchEvent`, `IntermediateThrowEvent`, `BoundaryEvent` (interrupting + non-interrupting) | ADR-006; ADR-018/SRD-029 |
| Event definitions | `Message` (incl. instantiation + correlation), `Timer` (durable — checkpointed deadlines + dehydration/wake), `Signal` (throw/catch/broadcast/start), `Error` (end + boundary), `Terminate`, `Conditional` (catch + boundary + EBG arms + event-sub-process start; top-level start = registered fail-fast rejection 📐), `Escalation` (throw + scope-chain catch), `Compensate` (ledger + throw-driven undo + boundary handler link), `Link` (static GOTO pairing), `Cancel` (Transaction-only end + boundary) | ADR-014/015/016, SRD-013…017, SRD-026, SRD-029, SRD-030, ADR-006 v.3/SRD-048, ADR-023 v.2/SRD-052; SRD-058/059/057/061; ADR-033/SRD-070, ADR-007 v.2.1/SRD-071 |
| Flows | `SequenceFlow` — conditional + default honored at **gateways and activities** | ADR-005; SRD-046 (#51) |
| Data | `ItemDefinition`, `Property`, `InputOutputSpecification` (single-set 📐), `DataInput/Output` + associations + `Assignment` shapes, structural values (record/list/**map**, path addressing, commit-diff, native structs), **`DataObject`** (per-instance scope-resident), **`DataStore`/`DataStoreReference`** (engine-global port) | ADR-010/011, SRD-007…011, SRD-042…045, SRD-047; ADR-030/SRD-063/068 |
| Correlation | `CorrelationKey`/`Property`/`RetrievalExpression`/`Binding`/`Subscription` — key-based, multi-key conversation threading | ADR-016, SRD-015/017 |
| Operations | `Interface`, `Operation` (polymorphic: external message kind + in-process Go kind 📐-adjacent, SAD-001 §14.2) | ADR-011 v.5, SRD-011 |
| Human interaction | The Camunda triad (`assignee`/`candidateUsers`/`candidateGroups`), `Rendering`, `Resource`(+`Parameter`) — **plus the ownership half**: `UserTask.actualOwner` (§10.3.4.1, **Table 10.14** — the first *instance* attribute the engine implements, a layer the generated `elements/` pages cannot show), `Claim`/`Unclaim`/`Reassign` as the operations the standard leaves to the engine, strict owner-only completion, and a performer record later nodes route on | ADR-020 v.1/SRD-034; ADR-020 v.2/SRD-073 |
| Foundation | `BaseElement`, `Documentation`, `Import`, `FormalExpression` | core |

## 2. Gaps — mapped to issues

Ordered by the recommended implementation sequence (rationale in §4).

| # | Gap | Status | Issue | Notes |
|---|---|---|---|---|
| 1 | `CallActivity` + `CallableElement` I/O + `InputOutputBinding` + `GlobalTask` variants | ✅ | [#85](https://github.com/dr-dobermann/gobpm/issues/85) | **Landed SRD-050** (child instance via the ADR-019 registry; latest-at-launch + `WithCalledVersion`; by-name I/O cloned across the boundary; child-error catch at the node; cancel cascade; `Call` facts). Boundary events on CallActivity landed here too (§10.5.4 — a CallActivity is an activity, the boundary machinery consumes it). `GlobalTask` variants remain out of scope. **Closes #85.** |
| 2 | Event Sub-Process (`SubProcess` with `triggeredByEvent=true`) | ✅ | [#91](https://github.com/dr-dobermann/gobpm/issues/91) | **Interrupting (SRD-052)** — scope-armed handler (Message/Timer/Signal/Conditional-start + Error on the scope chain), cancel-and-run, absorb, the shared interrupting budget, handler facts — **plus non-interrupting (SRD-053)** — fork-don't-cancel, unlimited concurrent instances in unique child scopes, per-instance payload, multi-shot (Error stays interrupting-only). The Event Sub-Process type is complete. Transaction (row 3) is separate (#91) |
| 3 | `Transaction` + `CancelEventDefinition` execution | ✅ | [#91](https://github.com/dr-dobermann/gobpm/issues/91) | **Landed SRD-061 (ADR-028)** — a `WithTransaction()` Sub-Process variant aborting atomically on a **Cancel End Event**: compensate the completed activities (the row 8 ledger + scope-wide sweep as an ACID-like barrier) → terminate the residuals → leave via the interrupting **Cancel boundary** (un-defers ADR-018 §2.7). Cancel is direct-resolution (loop-local, never the hub); Cancel End / Cancel boundary are Transaction-only and nested Transactions rejected (validated at registration); `CancelEventDefinition` execution ✅. Deep (recursive) scope compensation, `store`/`image`, Ad-Hoc stay out of scope per ADR-028. **Closes the Transaction slice of #91** |
| 4 | `StandardLoopCharacteristics` execution ✅ + `MultiInstanceLoopCharacteristics` (sequential ✅ + parallel ✅ + behavior ✅) + `completionQuantity` (non-goal) | ✅ | [#88](https://github.com/dr-dobermann/gobpm/issues/88) | Standard Loop landed (ADR-025 / SRD-054 — leaf in-place + composite scope re-entry, `loopCounter`, testBefore/loopMaximum); **sequential Multi-Instance landed** (ADR-025 / SRD-055 — cardinality XOR collection, per-instance input/output mediator, `numberOf*` attributes, `completionCondition`, visibility barrier); **parallel Multi-Instance landed** (ADR-025 / SRD-056.A — N concurrent distinct scopes, per-instance-scope data, positional assembly, `completionCondition` cancellation + `numberOfTerminatedInstances`, MI-aware interrupting-boundary teardown); **`behavior` landed** (ADR-025 / SRD-056.B — All/None/One/Complex event throwing as instances complete, `ImplicitThrowEvent` + `ComplexBehaviorDefinition`, boundary-catchable, thrown off-loop by the iteration decorator). All landed on the **off-loop iteration decorator** (ADR-025 v.2 §2.12). `startQuantity`/`completionQuantity` ≠ 1 are a **deliberate non-goal** (SAD-001 §4 N8, ADR-023 v.3 §2.3 — no diagram notation → opaque; Camunda-aligned; use an explicit Parallel Gateway), so the row is complete |
| 5 | `ScriptTask` execution | ✅ | [#87](https://github.com/dr-dobermann/gobpm/issues/87) | **Landed ADR-031 / SRD-064 / SRD-065** — the multi-engine Script Engine seam (enumerable format claims, the core routing registry, repeatable `WithScriptEngine`, loud claim conflicts and unclaimed-format errors, the `##None` default), the rebuilt `ScriptTask` (route by `scriptFormat` → execute → per-name sorted output commits; `Script` facts) and the batteries **Lua interpreter** (`adapters/lua` over pure-Go gopher-lua: fresh context-bound sandboxed LState per execution, the lazy fail-loud `data` table with `has()`, table-return outputs). **Closes #87 with the BRT rows** (6). Starlark noted as the sibling adapter |
| 6 | `BusinessRuleTask` (DMN) execution | ✅ | [#87](https://github.com/dr-dobermann/gobpm/issues/87) | **Landed SRD-060 (ADR-027 v.1)** — the pluggable Business Rule Engine seam in the ADR-002 shape: one-method `rules.Engine` (evaluate a decision reference against the data reader → result **rows**, the DMN-universal list-of-records), the batteries-included `gorules` Go decision registry (`##GoRules`, bounded, fail-loud on unknown refs), the five-point thresher/renv wiring (`WithRuleEngine`), and the task rebuilt to the house pattern (call → 1×1-scalar/row-list **fold** commit through the frame → conditional flows route on it; failures ride the ordinary fault machinery; `KindRules` decision facts). The `Deployer` capability is declared (deploy + evaluate — the minimal DMN component API); the Decision Table model (Rule interface, hit policies) and any DMN adapter ride a follow-up SRD. DMN itself stays external per SAD-001 N2. **Script Task (row 5) remains under #87.** |
| 7 | `EscalationEventDefinition` execution | ✅ | [#90](https://github.com/dr-dobermann/gobpm/issues/90) | **Landed SRD-058 (ADR-006 v.4 §2.2/§2.6 · ADR-018 · ADR-023 v.2 §2.6)** — Error's non-critical twin: a throw (Intermediate Throw / End Event) climbs the scope chain to the innermost catcher (Escalation boundary — interrupting **or** non-interrupting — or an event-sub-process Escalation start, inline handler winning), matched by code (empty = catch-all). The throw continues its token and never faults; an **unresolved** escalation is logged (`KindEscalation`/`Unresolved` at Warn), not silently dropped. Reuses `matchErrorScopeChain` with three deltas (non-critical throw, logged-not-faulted miss, non-interrupting catch); adds `Escalate(code)` to `renv.RuntimeEnvironment` and `KindEscalation`. **Compensation (row 8) remains under #90.** |
| 8 | `CompensateEventDefinition` execution + compensation `Association` semantics | ✅ | [#90](https://github.com/dr-dobermann/gobpm/issues/90) | **Landed SRD-059 (ADR-026 v.1)** — the completion ledger (per-scope, completion-ordered compensable entries with data snapshots captured at `Completed`; child→parent folding; discard with the enclosing scope), throw-driven resolution (targeted / scope-wide **reverse completion order**, sequential handlers, `waitForCompletion` parks the thrower), the Compensation boundary with a **typed handler link** realizing the Association's semantics in the programmatic model (full `Association` container wiring rides the ADR-024 interchange workstream), the compensation Event Sub-Process, presumed abort, unresolved-logs-not-faults, and `KindCompensation` ledger observability. Recursive default compensation / error-driven auto-sweep / `compensate-on-terminate` are ADR-026 designed-for; Transaction-cancel rides #91. **Closes #90** — all four epic events landed |
| 9 | `LinkEventDefinition` execution | ✅ | [#90](https://github.com/dr-dobermann/gobpm/issues/90) | **Landed SRD-057 (ADR-006 v.4 §2.8)** — intra-process GOTO by static name-pairing: throw source → same-name catch target within one Process level, resolved at `WireClonedGraph` (nested Sub-Process links free) and validated fail-fast at registration; the throw redirects (no hub, no waiter), the catch is a bypassed flow label. **Retired** the `SubscriptionKey()` generalization premise (Link is a static redirect, not a name-matched subscription — Signal stays the only one). Compensate (row 8) remains under #90 (Escalation, row 7, landed SRD-058) |
| 10 | Boundary-on-SubProcess/CallActivity + Error **scope-chain propagation** | ✅ | [#79](https://github.com/dr-dobermann/gobpm/issues/79) | **Complete — #79 closed 2026-07-22.** Error scope-chain landed (SRD-049); boundary-on-composite landed with the Sub-Process (SRD-049) and boundary-on-CallActivity with SRD-050 (the base activity's boundary machinery consumes both, Error catch verified e2e); Escalation reuses the same scope-chain matcher (SRD-058) |
| 11 | `DataObject` execution semantics ✅ + `DataObjectReference` (deferred) + `DataStore`/`DataStoreReference` ✅ | ✅ | [#82](https://github.com/dr-dobermann/gobpm/issues/82) | **Data elements landed (ADR-030 v.1).** `DataObject` scope integration (**SRD-063**): a **per-instance scope-resident named container** — registered on a `Process`/`SubProcess`, seeded into the matching scope (root / child-on-open, disposed at close), resolved by name via the walk-up, with bidirectional DataAssociation flow through per-instance scope. `DataStore`/`DataStoreReference` (**SRD-068**): the **engine-global** store as an infrastructure port (ADR-030 §2.5/§2.6) — a `datastore.Registry` of named stores (`thresher.WithDataStore`, fail-loud on unknown ref, each store its own capacity/backing), and a flow-scope `DataStoreReference` whose DataAssociation I/O routes to the shared store (`capacity` advisory in-memory; durability a swappable adapter = future Persistence & State). `DataObjectReference` is a **deliberate non-implementation** (SAD-001 §14.1 + BPMN-translation rules). **Closes #82** (bar the deferred `DataObjectReference`) |
| 12 | Timer durability + hydration | ✅ | [#84](https://github.com/dr-dobermann/gobpm/issues/84) | **Landed with the Persistence & State slices.** Consistent-cut checkpoints + restart recovery re-arm a restored timer at its RECORDED absolute deadline, an overdue timer fires once (ADR-033 / SRD-070); goroutine-releasing **dehydration + wake-on-trigger** makes the engine-held deadline the instance's liveness, with a failed wake retried on backoff, never stranding the instance (ADR-007 v.2.1 / SRD-071). **Closes #84** |
| 13 | `AdHocSubProcess` | ✅ | [#92](https://github.com/dr-dobermann/gobpm/issues/92) | **Landed SRD-074 (ADR-035)** — a `WithAdHoc(router)` Sub-Process variant on the landed nested scope, whose inner activities carry **no sequence flows**: a host-supplied **Router** replaces sequence-flow succession, consulted at scope open (the standard's *initially enabled* set) and after each settle (§13.3.5's "the enabled set is updated"), reading the scope's data through a transient frame. An empty answer ends the asking track and the container completes on the existing **scope drain** — no new completion path. `parallel` default + `sequential`; manual selection parks the answer as the enabled set with a per-container handle (`Enabled`/`Running`/`Activate`, unoffered activations rejected); `completionCondition` is a Router decorator and the sole trigger `cancelRemainingInstances` hangs off; batteries `Standard`/`Expression`/`Sequence` ship but **no Router is implied**, never by declaration order. Inner containment validated to leaf Tasks + plain embedded Sub-Processes; `KindAdHoc` facts make a case's routing reconstructible. **Closes #92** |

## 3. Small items — decide, don't (necessarily) build

| Item | Status | Disposition |
|---|---|---|
| `Performer`/`HumanPerformer`/`PotentialOwner`, `ResourceParameterBinding`, `ResourceAssignmentExpression` | ❌ | gobpm deliberately chose the Camunda triad (ADR-020). **Candidate for SAD-001 §14.1 registration** as an engine choice — currently an unregistered deviation |
| `DataState` (the BPMN label element) | ❌ | gobpm's closed three-state model (ADR-010 §2.1) covers the semantics. **Candidate for §14.1 registration** |
| `ImplicitThrowEvent` | ✅ | **Landed** with Multi-Instance `behavior` (SRD-056.B, row 4) — the activity-thrown, never-token-reached event carrying the behavior's EventDefinition; boundary-catchable |
| `UserTask.taskPriority` (§10.3.4.1, Table 10.14) | ❌ | The other instance attribute beside `actualOwner`, which landed with ADR-020 v.2/SRD-073. A distribution and ranking concern with no bearing on ownership or execution semantics — deferred, not overlooked (ADR-020 §7). Implement when a distributor needs to sort an inbox by it |
| Reassignment to a **group-only** nominee | ❌ | `Reassign` checks the nominee against the frozen triad, but group membership is authenticated for a *present* actor and cannot be asserted for an absent one — so a task eligible only via `candidateGroups` can be claimed by any member and reassigned to none (ADR-020 §2.5.2). Closing it needs the directory/resource-query subsystem ADR-020 §7 defers |
| `InputSet`/`OutputSet` multiplicity | 📐 | Already registered (SAD-001 §14.1 — single set, per-parameter flags) |
| Data-availability wait | 📐 | Already registered (§14.1 — error, never wait) |
| Value-less item-aware elements | 📐 | Already registered (§14.1 — rejected at registration) |

## 4. XML-interchange tier — out of the execution-conformance path

Per BPMN §2.1.2, **Process Execution Conformance does not require XML
import/interchange**; gobpm's model is deliberately programmatic (the
README's "XML parsing is intentionally decoupled"). These elements matter
only if/when an XML layer arrives, and are **not** conformance gaps today:

`Definitions` (the registration layer plays the container role), `Lane`/
`LaneSet` (parse-and-preserve only — no token semantics by spec), `EndPoint`
(rides the ADR-004 remote transport), `Auditing`/`Monitoring` placeholders,
the vendor `Extension*` model types.

## 5. Recommended order (rationale)

1. ~~#89 Conditional events~~ — **landed** (SRD-048, ADR-006 v.3 §2.7).
2. ~~#85 Sub-Process + Call Activity~~ — the keystone, **CLOSED**: the
   embedded Sub-Process (the token-scope container) **landed** (SRD-049)
   and the Call Activity (the reuse boundary) **landed** (SRD-050).
3. ~~#88 loops/multi-instance~~ — **landed** (ADR-025; SRD-054/055/056.A/
   056.B — see row 4).
4. ~~#87 Script/DMN tasks~~ — **landed** (rows 5–6: ADR-027/ADR-031 with
   their SRDs); the expression layer (~~#74~~) **landed** too — ADR-032
   language-routed engines, the `gobpm:lite` text battery beside
   `goexpr` (SRD-066/067).
5. ~~#90 Escalation/Compensate/Link~~ — **landed** (SRD-058/059/057;
   + Cancel with #91, SRD-061). The event catalog is complete.
6. ~~#82 data objects/stores~~, ~~#84 timer durability~~ — **landed**
   (rows 11–12: ADR-030 data elements; the ADR-033/ADR-007 v.2.1
   Persistence & State slices, SRD-070/071).
7. ~~#92 Ad-Hoc Sub-Process~~ — **landed** (row 13: ADR-035 / SRD-074).
   It was the last executable element in scope, so the element set for
   Process Execution Conformance is now complete.
8. Remaining, doc-only: the §3 SAD-001 §14.1 registrations (triad,
   DataState).

## Maintenance

Update this tracker **in the same PR** that lands or registers an element
(the front-door-sync discipline): flip the row's status, move it to §1, keep
the issue link until the epic closes. The "Last verified" date bumps on every
full re-sweep.
