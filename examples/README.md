# GoBPM examples

Runnable programs demonstrating the engine. Each example is its own Go module
(its own `go.mod` with a `replace` onto the repo root), so run it from its own
directory:

```bash
cd <example> && go run .
```

Every example runs end-to-end in CI and asserts its own outcome; the list below is grouped by concern.

## Basics

| Example | Demonstrates |
|---|---|
| [`basic-process/`](basic-process/) | The fundamentals: engine → process (start → service task → end) → register → run. |
| [`process-data/`](process-data/) | Process data through a task — a property and an engine runtime variable read via the `DataReader`, plus a per-branch **`DataObject`** (a scope-resident named container, read back by name). |
| [`data-store/`](data-store/) | The engine-global **Data Store** (`DataStoreReference` + `thresher.WithDataStore`) — a value one instance writes outlives it and is read by a *separate* instance through the shared store. |

## Gateways

| Example | Demonstrates |
|---|---|
| [`parallel-gateway/`](parallel-gateway/) | Parallel (AND) split into concurrent branches + a synchronizing join. |
| [`gateway-routing/`](gateway-routing/) | Exclusive (XOR) data-based routing — first-true condition, else the default flow. |
| [`inclusive-join/`](inclusive-join/) | Inclusive (OR) split — every true branch forks — and the OR-join. |
| [`complex-gateway/`](complex-gateway/) | Complex gateway: an activation-threshold join (fire once N of M arrive). |
| [`event-based-gateway/`](event-based-gateway/) | Mid-flow deferred choice — the first of several events to fire wins; the rest are dropped. |
| [`event-based-parallel-start/`](event-based-parallel-start/) | A process **started** by an event gateway — two correlated messages, one instance. |

## Structural data

| Example | Demonstrates |
|---|---|
| [`structural-data/`](structural-data/) | Reaching **into** a record value by path — a service task reads `order.items[0].price`, a gateway routes on `order.total` (ADR-011 v.6 §2.9). |
| [`structural-output-mapping/`](structural-output-mapping/) | Assembling a nested value **out of** a flat worker body — output-mapping rules sharing the head `order` build one record with an auto-vivified `items` list, read back by path (ADR-011 v.6 §2.9.5 / SRD-043). |
| [`data-change/`](data-change/) | Observing **which data changed** — commit-diff at the activity boundary surfaces one `DataChange` fact per changed path: a first commit is one `Value_Added` at the root, a nested re-commit one `Value_Updated` at the leaf (ADR-011 v.6 §2.9.4 / SRD-044). |
| [`embedded-subprocess/`](embedded-subprocess/) | **Composition** — a fulfillment fragment as an embedded Sub-Process: a nested scope in the same instance, walk-up reads of the parent's data, scoped locals disposed at close, drain-resume, and the Scope lifecycle facts (ADR-023 / SRD-049). |
| [`call-activity/`](call-activity/) | **Composition — the reuse boundary** — a `checkout` process invokes a separately registered `tax-calc` as a **child instance**: declared I/O cloned across the boundary (isolated data plane), latest-at-launch or pinned versioning, output committed back, and the Call lifecycle facts (ADR-023 / SRD-050). |
| [`process-io/`](process-io/) | **Process I/O contract** — a process DECLARES its inputs and outputs (`data.WithInputs`/`WithOutputs`, or `<ioSpecification>` under `<process>`): a host binds them with `thresher.WithStartInput`, a Call Activity through its parameters; a required input left unbound or an undeclared datum refuses the launch before the instance exists, and the declared outputs are collected at completion — `Outputs()` on the handle, or committed back to the caller — including an engine `RUNTIME/…` variable a task publishes under a declared name (ADR-040 / SRD-093). |
| [`adhoc-subprocess/`](adhoc-subprocess/) | **Ad-Hoc Sub-Process — order decided at runtime** — an incident-triage container whose four activities carry **no sequence flows**: a Router answers what runs next, reads the case's `severity` to fork `notify-customer` **and** `escalate`, ends only the asking track while a sibling still runs (a join without a join gateway), and closes the incident before answering empty (ADR-035 / SRD-074). |
| [`event-subprocess/`](event-subprocess/) | **Composition — an interrupting Event Sub-Process** — a payment wait guarded by a `triggeredByEvent` Timer handler: armed while the scope is open, on fire it cancels the blocked wait, runs in the parent's data context, and absorbs the event so the parent resumes on its normal flow (ADR-023 v.2 / SRD-052). |
| [`conditional-events/`](conditional-events/) | **Data-driven waiting without polling** — an intermediate conditional catch parks a branch until a sibling task's committed change flips its condition false→true; `goexpr.WithDependencies` narrows re-evaluation to overlapping commits (ADR-006 v.3 §2.7 / SRD-048). |
| [`native-structs/`](native-structs/) | The host's **own Go struct as process data** — `adapters.Wrap` returns a live view (wrap, not convert): `gobpm:"..."` tags, a host-side `SetPath` writing into the live struct, a gateway routing on `order.total`, and DataChange facts over wrapped commits (ADR-011 v.6 §2.9.5 / SRD-045). |
| [`standard-loop/`](standard-loop/) | **Iteration — a Standard Loop** (§13.3.6) — a Service Task marked `WithLoop` re-runs while its condition holds, reading the engine-published 0-based `loopCounter` each pass; the same marker loops a Sub-Process by re-opening its child scope per iteration, and `WithTestBefore()` / `WithLoopMaximum(n)` select a pre-tested loop and cap the count (ADR-025 / SRD-054). |
| [`multi-instance-sequential/`](multi-instance-sequential/) | **Iteration — a sequential Multi-Instance** (§13.3.7) — a Sub-Process runs once per element of an input collection, sequentially; each instance sees its element by name, and the per-instance outputs are assembled — in order — into an output collection published once at completion. `WithCardinality` / `WithInputCollection` set the count and `WithCompletionCondition` stops early (ADR-025 / SRD-055). |
| [`multi-instance-parallel/`](multi-instance-parallel/) | **Iteration — a parallel Multi-Instance** (§13.3.7) — a review panel where reviewers score a proposal **concurrently**, each in a distinct scope; the scores assemble positionally (in reviewer order) into an output collection despite non-deterministic completion. `numberOfActiveInstances` > 1, and `WithCompletionCondition` cancels the still-running instances (ADR-025 / SRD-056.A). |
| [`multi-instance-behavior/`](multi-instance-behavior/) | **Iteration — Multi-Instance `behavior`** (§13.3.7) — a review board throws a **boundary-catchable** event as instances complete: a **Complex** behavior throws a *quorum-reached* Signal once `numberOfCompletedInstances ≥ 2`, caught by a **non-interrupting** boundary that posts a notification while the board keeps running. `WithBehavior` (All / None / One / Complex) + `WithComplexBehavior` (ADR-025 / SRD-056.B). |

## Service workers

| Example | Demonstrates |
|---|---|
| [`service-task-worker/`](service-task-worker/) | External worker (fetch-and-lock) with in-process retry, trust modes, a Business Status / Business Error verdict, and **structural output mapping** (nested fields extracted from a structured worker body). |
| [`usertask/`](usertask/) | User task — a human-completed wait node gated by Camunda-style assignee / candidate authorization, then **claimed** for exclusive hold: completion is strict, so only the actual owner may finish it (ADR-020 v.2). |
| [`usertask-sla/`](usertask-sla/) | **SLA warnings on a human task** — three *bounded, non-interrupting* timer boundaries at 50% / 90% / 100% of a User Task's budget. Each carries a `timeDuration` **alone** (a relative deadline measured from the moment the boundary arms); the operator deliberately overruns, so every warning fires and the approval still completes — which is what non-interrupting means. |
| [`incident-retry/`](incident-retry/) | **A failure the process survives** — an unhandled technical failure opens a durable *incident* (cause chain, attempt history, a failure-time data snapshot) instead of killing the instance. The retry policy re-enters the node once on its own; when it exhausts, the operator inspects the incident and `RetryIncident`s it to completion. |
| [`business-rule-task/`](business-rule-task/) | **Business Rule Task on the pluggable rule-engine seam** — the task evaluates a named decision on the configured engine (here the batteries-included `gorules` Go registry, `##GoRules`), the 1×1 result fold commits the outcome as a scalar, and the task's conditional flows route on it; any DMN/rules service swaps in via `WithRuleEngine` without touching the model (ADR-027 / SRD-060). |
| [`script-task/`](script-task/) | **Script Task on the multi-engine Script Engine seam** — an embedded `order.lua` runs sandboxed on `adapters/lua` (`##Lua`), routed by the task's `scriptFormat`; lazy fail-loud `data` reads with a `has()` probe, outputs returned as a table and committed per-name; several interpreters can register side by side via repeatable `WithScriptEngine` (ADR-031 / SRD-064 / SRD-065). |
| [`expression-routing/`](expression-routing/) | **Language-routed expression engines** — one process mixes `gobpm:lite` text conditions (record paths, a map probe, a `time()` comparison) with `goexpr` Go functors at three sites: task flows, an XOR gateway and a UserTask whose assignee is computed by a lite string expression; zero extra registration on the batteries registry (ADR-032 / SRD-066 / SRD-067). |
| [`restart-recovery/`](restart-recovery/) | **Instance checkpoints & restart recovery** — one shared repository, two engines: park on a timer, crash by abandonment, claim the expired ownership lease, restore at the RECORDED deadline and complete; CAS-fenced zombie saves show exactly-once state over at-least-once effects (ADR-033 / SRD-070). |
| [`dehydration/`](dehydration/) | **Instance dehydration & wake-on-trigger** — six long waits, one per holder kind (timer, message, signal, user task, event-based gateway): each releases every goroutine the instance owns, including its loop, and comes back on its trigger with the checkpoint as the only wake source; the near-deadline timer deliberately stays resident, showing the threshold from both sides (ADR-007 / SRD-071). |
| [`decision-table/`](decision-table/) | **A deployed JSON decision table on the `adapters/dtable` engine** — the first out-of-core Business Rule Engine: an embedded artifact (structure only — grid, FIRST hit policy, names) deploys through the pluggable Decoder seam over a `Vocabulary` of named Go functors; the BRT evaluates it per order (vip+big 25% / big 15% / fallthrough 5%). Behavior stays compiled Go; redeploying the artifact re-wires the rules (ADR-029 / SRD-062). |

## Messages & correlation

| Example | Demonstrates |
|---|---|
| [`message-send-receive/`](message-send-receive/) | A SendTask publishes to the broker; a ReceiveTask waits and binds the payload. |
| [`message-intermediate-events/`](message-intermediate-events/) | Throw / catch intermediate message events. |
| [`inter-instance-correlation/`](inter-instance-correlation/) | A message **instantiates** a handler process and **correlates** by a key derived from the payload (one instance per distinct order). |
| [`conversation-routing/`](conversation-routing/) | A follow-up message **routes back** to the specific handler instance whose conversation it belongs to; two conversations stay isolated. |
| [`event-data/`](event-data/) | **Event data attachment** — a message Start Event's output association fills a **declared process input** from the payload (and a data object), a message End Event's input association sources a **declared process output** — the standard's Start/End special case (§10.4.2), so the message route reaches the same contract a Call Activity binds (ADR-040 v.2 §2.7 / SRD-094). |

## Signals

| Example | Demonstrates |
|---|---|
| [`signal-broadcast/`](signal-broadcast/) | One throw reaches **every** waiting catcher in reach (broadcast, no correlation). |
| [`signal-start/`](signal-start/) | A broadcast signal **instantiates** processes whose start trigger is a signal. |

## Timers, boundaries & termination

| Example | Demonstrates |
|---|---|
| [`simple-timer/`](simple-timer/) | A minimal timer start event. |
| [`timer-event/`](timer-event/) | Timer event definitions with a `timeDate` expression. |
| [`boundary-events/`](boundary-events/) | An interrupting timer boundary as a timeout — it fires before a long task finishes, cancels it, and routes onto the exception flow. |
| [`terminate-end-event/`](terminate-end-event/) | A Terminate End Event ends the whole instance mid-flight — it settles `Terminated`, not `Completed`. |

## Links, escalation & compensation

| Example | Demonstrates |
|---|---|
| [`link-events/`](link-events/) | **Link events — an intra-process GOTO** — a source Intermediate Throw hands the token to the same-name target Intermediate Catch within one Process level (static name-pairing, no wait, no broadcast); shown as an on-page loop with a back-edge (ADR-006 v.4 §2.8 / SRD-057). |
| [`escalation-events/`](escalation-events/) | **Escalation — a non-critical signal up the scope chain** — a sub-process raises `OVER_BUDGET`, an interrupting Escalation boundary catches it by code and routes to a manager; unlike an Error it never faults, and an unresolved escalation is logged, not dropped (ADR-006 v.4 §2.2/§2.6 / SRD-058). |
| [`compensation-events/`](compensation-events/) | **Compensation — undoing completed work (the saga pattern)** — a trip-booking saga: guarded bookings enter the completion ledger with data snapshots, and a Compensation End Event undoes them in **reverse completion order**, waiting for the handlers (ADR-026 / SRD-059). |
| [`transaction-sub-process/`](transaction-sub-process/) | **Transaction Sub-Process — atomic abort on Cancel** — a booking Transaction (`WithTransaction`) that reserves and charges (both compensable), then a **Cancel End Event** aborts it: compensate in reverse order (refund before release), terminate the residuals, and leave through the interrupting **Cancel boundary** to notify the customer (ADR-028 / SRD-061). |

## Lifecycle

| Example | Demonstrates |
|---|---|
| [`versioning/`](versioning/) | Camunda-style definition versioning — one key, many versions; start by latest, by number, or by handle; promote-on-removal. |

## Interchange

| Example | Demonstrates |
|---|---|
| [`bpmn-convert/`](bpmn-convert/) | **BPMN 2.0 XML import & export** — a bundled `.bpmn` parsed through the `pkg/convert` seam into a live `*process.Process`, registered (the BPMN `id` becomes the ADR-019 version key), run to completion, then serialized back out. The converter itself lives in `pkg/convert/bpmn` and is switched on by a blank import (ADR-024 / SRD-051). |

---

Prerequisites: a recent Go toolchain (see the repo `go.mod`). Some examples
(worker, structural-data) also print the engine's observability facts as they
run. New to the engine? Start with [`basic-process/`](basic-process/), then the
[project README](../README.md).
