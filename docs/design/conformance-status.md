# BPMN Conformance Status — implementation tracker

| Field | Value |
|---|---|
| Type | **Continuously-current tracker** (not an SRD/ADR — updated as elements land, in the landing PR) |
| Scope authority | [docs/bpmn-spec/conformance.md](../bpmn-spec/conformance.md) — Common Executable Subclass + the ComplexGateway extension |
| Last verified | 2026-07-20, post-SRD-057 (Link events landed — #90 row 9 ✅; sequential Multi-Instance SRD-055 landed via master — #88 row 4) |
| Owner | Ruslan Gabitov |

Status vocabulary: ✅ **executed** (model type + engine semantics + tests) ·
🟡 **model-only** (a type exists, no execution wiring) · ❌ **absent** ·
📐 **registered deviation** (a deliberate engine choice in SAD-001 §14).

## 1. Conformant today

| Family | Elements | Landed via |
|---|---|---|
| Process container | `Process` (executable, versioned registration) | core; ADR-019 |
| Activities | `ServiceTask` (in-process + external workers), `UserTask`, `ManualTask`, `SendTask`, `ReceiveTask`, the abstract `Task` base, **`SubProcess` (embedded)** — nested scope in the instance: §13.3.4 shapes + drain, §10.5.7 data visibility, scoped Terminate (§13.5.6), boundary-on-composite, the Error scope-chain | ADR-021/SRD-035…039; ADR-020/SRD-034; SRD-013/014; ADR-023/SRD-049 |
| Gateways — **all five** | `Exclusive`, `Parallel`, `Inclusive` (incl. the OR-join), `EventBased` (incl. Exclusive/Parallel instantiating starts), `Complex` (the declared extension) | SRD-005, SRD-021/022, SRD-023, SRD-024/025 |
| Events (positions) | `StartEvent`, `EndEvent`, `IntermediateCatchEvent`, `IntermediateThrowEvent`, `BoundaryEvent` (interrupting + non-interrupting) | ADR-006; ADR-018/SRD-029 |
| Event definitions | `Message` (incl. instantiation + correlation), `Timer` (in-memory), `Signal` (throw/catch/broadcast/start), `Error` (end + boundary), `Terminate`, `Conditional` (catch + boundary + EBG arms + **event-sub-process start — landed SRD-052**; top-level start = registered fail-fast rejection 📐) | ADR-014/015/016, SRD-013…017, SRD-026, SRD-029, SRD-030, ADR-006 v.3/SRD-048, ADR-023 v.2/SRD-052 |
| Flows | `SequenceFlow` — conditional + default honored at **gateways and activities** | ADR-005; SRD-046 (#51) |
| Data | `ItemDefinition`, `Property`, `InputOutputSpecification` (single-set 📐), `DataInput/Output` + associations + `Assignment` shapes, structural values (record/list, path addressing, commit-diff, native structs) | ADR-010/011, SRD-007…011, SRD-042…045 |
| Correlation | `CorrelationKey`/`Property`/`RetrievalExpression`/`Binding`/`Subscription` — key-based, multi-key conversation threading | ADR-016, SRD-015/017 |
| Operations | `Interface`, `Operation` (polymorphic: external message kind + in-process Go kind 📐-adjacent, SAD-001 §14.2) | ADR-011 v.5, SRD-011 |
| Human interaction | The Camunda triad (`assignee`/`candidateUsers`/`candidateGroups`), `Rendering`, `Resource`(+`Parameter`) | ADR-020/SRD-034 |
| Foundation | `BaseElement`, `Documentation`, `Import`, `FormalExpression` | core |

## 2. Gaps — mapped to issues

Ordered by the recommended implementation sequence (rationale in §4).

| # | Gap | Status | Issue | Notes |
|---|---|---|---|---|
| 1 | `CallActivity` + `CallableElement` I/O + `InputOutputBinding` + `GlobalTask` variants | ✅ | [#85](https://github.com/dr-dobermann/gobpm/issues/85) | **Landed SRD-050** (child instance via the ADR-019 registry; latest-at-launch + `WithCalledVersion`; by-name I/O cloned across the boundary; child-error catch at the node; cancel cascade; `Call` facts). Boundary events on CallActivity landed here too (§10.5.4 — a CallActivity is an activity, the boundary machinery consumes it). `GlobalTask` variants remain out of scope. **Closes #85.** |
| 2 | Event Sub-Process (`SubProcess` with `triggeredByEvent=true`) | ✅ | [#91](https://github.com/dr-dobermann/gobpm/issues/91) | **Interrupting (SRD-052)** — scope-armed handler (Message/Timer/Signal/Conditional-start + Error on the scope chain), cancel-and-run, absorb, the shared interrupting budget, handler facts — **plus non-interrupting (SRD-053)** — fork-don't-cancel, unlimited concurrent instances in unique child scopes, per-instance payload, multi-shot (Error stays interrupting-only). The Event Sub-Process type is complete. Transaction (row 3) is separate (#91) |
| 3 | `Transaction` + `CancelEventDefinition` execution | ❌ / 🟡 | [#91](https://github.com/dr-dobermann/gobpm/issues/91) | Needs the landed scope model + row 8 (compensation) |
| 4 | `StandardLoopCharacteristics` execution ✅ + `MultiInstanceLoopCharacteristics` (sequential ✅ + parallel ✅) + `ComplexBehaviorDefinition` + `completionQuantity` | 🟡 | [#88](https://github.com/dr-dobermann/gobpm/issues/88) | Standard Loop landed (ADR-025 / SRD-054 — leaf in-place + composite scope re-entry, `loopCounter`, testBefore/loopMaximum); **sequential Multi-Instance landed** (ADR-025 / SRD-055 — cardinality XOR collection, per-instance input/output mediator, `numberOf*` attributes, `completionCondition`, visibility barrier); **parallel Multi-Instance landed** (ADR-025 / SRD-056.A — N concurrent distinct scopes, per-instance-scope data, positional assembly, `completionCondition` cancellation + `numberOfTerminatedInstances`, MI-aware interrupting-boundary teardown). `behavior`/`ComplexBehaviorDefinition` (→ SRD-056.B) and `completionQuantity` remain (#88 open). `completionQuantity` deferral noted in SRD-046 NFR-4 |
| 5 | `ScriptTask` execution | 🟡 model-only | [#87](https://github.com/dr-dobermann/gobpm/issues/87) | Can ride the Expression Layer epic ([#74](https://github.com/dr-dobermann/gobpm/issues/74)) |
| 6 | `BusinessRuleTask` (DMN) execution | 🟡 model-only | [#87](https://github.com/dr-dobermann/gobpm/issues/87) | Needs a rule-engine extension seam (the ADR-002 pattern) |
| 7 | `EscalationEventDefinition` execution | ✅ | [#90](https://github.com/dr-dobermann/gobpm/issues/90) | **Landed SRD-058 (ADR-006 v.4 §2.2/§2.6 · ADR-018 · ADR-023 v.2 §2.6)** — Error's non-critical twin: a throw (Intermediate Throw / End Event) climbs the scope chain to the innermost catcher (Escalation boundary — interrupting **or** non-interrupting — or an event-sub-process Escalation start, inline handler winning), matched by code (empty = catch-all). The throw continues its token and never faults; an **unresolved** escalation is logged (`KindEscalation`/`Unresolved` at Warn), not silently dropped. Reuses `matchErrorScopeChain` with three deltas (non-critical throw, logged-not-faulted miss, non-interrupting catch); adds `Escalate(code)` to `renv.RuntimeEnvironment` and `KindEscalation`. **Compensation (row 8) remains under #90.** |
| 8 | `CompensateEventDefinition` execution + compensation `Association` semantics | 🟡 type exists | [#90](https://github.com/dr-dobermann/gobpm/issues/90) | The `Association` type exists (`pkg/model/artifacts`); semantics deferred |
| 9 | `LinkEventDefinition` execution | ✅ | [#90](https://github.com/dr-dobermann/gobpm/issues/90) | **Landed SRD-057 (ADR-006 v.4 §2.8)** — intra-process GOTO by static name-pairing: throw source → same-name catch target within one Process level, resolved at `WireClonedGraph` (nested Sub-Process links free) and validated fail-fast at registration; the throw redirects (no hub, no waiter), the catch is a bypassed flow label. **Retired** the `SubscriptionKey()` generalization premise (Link is a static redirect, not a name-matched subscription — Signal stays the only one). Compensate (row 8) remains under #90 (Escalation, row 7, landed SRD-058) |
| 10 | Boundary-on-SubProcess/CallActivity + Error **scope-chain propagation** | 🟡 | [#79](https://github.com/dr-dobermann/gobpm/issues/79) | Error scope-chain LANDED (SRD-049); **boundary-on-CallActivity LANDED (SRD-050** — the base activity's boundary machinery consumes it, Error boundary catch verified e2e); boundary-on-SubProcess + the broader propagation matrix remain for #79 |
| 11 | `DataObject` execution semantics + `DataObjectReference` + `DataStore`/`DataStoreReference` | 🟡 / ❌ | [#82](https://github.com/dr-dobermann/gobpm/issues/82) | The `DataObject` model exists (`pkg/model/data_objects`) |
| 12 | Timer durability + hydration (in-memory works today) | 🟡 | [#84](https://github.com/dr-dobermann/gobpm/issues/84) | Rides the persistence work (ADR-007/009) |
| 13 | `AdHocSubProcess` | ❌ | [#92](https://github.com/dr-dobermann/gobpm/issues/92) | Rides the landed scope model |

## 3. Small items — decide, don't (necessarily) build

| Item | Status | Disposition |
|---|---|---|
| `Performer`/`HumanPerformer`/`PotentialOwner`, `ResourceParameterBinding`, `ResourceAssignmentExpression` | ❌ | gobpm deliberately chose the Camunda triad (ADR-020). **Candidate for SAD-001 §14.1 registration** as an engine choice — currently an unregistered deviation |
| `DataState` (the BPMN label element) | ❌ | gobpm's closed three-state model (ADR-010 §2.1) covers the semantics. **Candidate for §14.1 registration** |
| `ImplicitThrowEvent` | ❌ | Spec-rare; implement trivially when a need appears, or register out |
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
3. **#88 loops/multi-instance** — the second structural pillar, now next.
4. **#87 Script/DMN tasks** — Script with the expression layer (#74), DMN
   behind an extension seam.
5. **#90 Escalation/Compensate/Link** — completes the event catalog
   (+ Cancel with #91).
6. **#82 data objects/stores**, **#84 timer durability** — with the
   persistence workstream.
7. In parallel, doc-only: the §3 SAD-001 §14.1 registrations (triad,
   DataState).

## Maintenance

Update this tracker **in the same PR** that lands or registers an element
(the front-door-sync discipline): flip the row's status, move it to §1, keep
the issue link until the epic closes. The "Last verified" date bumps on every
full re-sweep.
