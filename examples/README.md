# GoBPM examples

Runnable programs demonstrating the engine. Each example is its own Go module
(its own `go.mod` with a `replace` onto the repo root), so run it from its own
directory:

```bash
cd <example> && go run .
```

Every example builds in CI; the list below is grouped by concern.

## Basics

| Example | Demonstrates |
|---|---|
| [`basic-process/`](basic-process/) | The fundamentals: engine → process (start → service task → end) → register → run. |
| [`process-data/`](process-data/) | Process data through a task — a property and an engine runtime variable read via the `DataReader`. |

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
| [`usertask/`](usertask/) | User task — a human-completed wait node gated by Camunda-style assignee / candidate authorization. |

## Messages & correlation

| Example | Demonstrates |
|---|---|
| [`message-send-receive/`](message-send-receive/) | A SendTask publishes to the broker; a ReceiveTask waits and binds the payload. |
| [`message-intermediate-events/`](message-intermediate-events/) | Throw / catch intermediate message events. |
| [`inter-instance-correlation/`](inter-instance-correlation/) | A message **instantiates** a handler process and **correlates** by a key derived from the payload (one instance per distinct order). |
| [`conversation-routing/`](conversation-routing/) | A follow-up message **routes back** to the specific handler instance whose conversation it belongs to; two conversations stay isolated. |

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

---

Prerequisites: a recent Go toolchain (see the repo `go.mod`). Some examples
(worker, structural-data) also print the engine's observability facts as they
run. New to the engine? Start with [`basic-process/`](basic-process/), then the
[project README](../README.md).
