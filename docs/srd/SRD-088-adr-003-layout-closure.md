# SRD-088 — Closing the ADR-003 layout, and the adapter lifecycle seam

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-09 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-003 v.1](../design/ADR-003-module-layout.md) §4.2, §4.4, §4.6, §5 · [ADR-002 v.2](../design/ADR-002-extension-architecture.md) §8.3 |
| Milestone | E0 — conception & layout stabilization |
| Issue | [#269](https://github.com/dr-dobermann/gobpm/issues/269) |

---

## 1 Context

E0 is the last milestone before the 1.0 API freeze, and ADR-003 is its only
structural item: the engine/server division is enforced by import direction
(SAD-001 §9.1), and ADR-003 §4.4 is where that direction is written down.

**Issue #269's premise is stale.** It states that ADR-003's "§4.6 migration has
never started" and that "nothing in CI enforces import direction today". Both
are false at `10467bb`:

| §4.6 step | #269 claims | Verified at `10467bb` |
|---|---|---|
| 1 — scaffold `runtime/` | not started | `runtime/go.mod`, `runtime/doc.go`, `runtime/cmd/` exist |
| 2 — scaffold `adapters/` | not started | four modules: `sqlite`, `dtable`, `lua`, `postgres` |
| 4 — `EngineRuntime` → `pkg/renv/` | not started | `pkg/renv/engineruntime.go:26` |
| 6 — the `pkg/` seam packages | not started | `auth`, `clock`, `messaging`, `repository`, `tasks`, `observability`, `rules`, `script`, `datastore` all present |
| 7–8 — options + `New` | not started | 24 options in `pkg/thresher/options.go` |
| 10 — depguard | "None in CI" | five rule groups, `.golangci.yml:41-79`, each citing ADR-003 §4.4 |
| 11 — conformance helpers | not started | `pkg/clock/clocktest/`, `pkg/repository/repositorytest/` |
| 12 — delete empty `internal/` dirs | pending | `find internal -type d -empty` → none |

The migration landed incrementally rather than as the named project. What
remains is **making the catalogue true** and closing the one seam it names that
the code genuinely lacks: the adapter lifecycle hooks.

### 1.1 The extension model this document assumes

gobpm is a **library**, and its extension surface is a **closed list of ports**.
A user substitutes an implementation for a named port — Repository, IAM,
telemetry — and that implementation is the adapter. There is no mechanism for
adding new extension points, and none is wanted.

That closure is enforced **mechanically, not by convention**: everything under
`internal/` is unreachable from outside the module by Go's own import rule, so
`pkg/*` *is* the list of what may be substituted. `EventHub` is the worked
example — ADR-003 §4.6 step 3 keeps it in `internal/eventproc/` precisely
because it is "execution plumbing, not an extension point", though it is
interface-based throughout. Node execution is the same: interfaces there are
decomposition, not contract.

Two consequences shape §3:

- **No registry, no generic plugin list.** The engine knows its seams by name.
  Where a lifecycle hook matters, it is asserted at that seam — the pattern
  already in `thresher.go:723`, where `Migrate` is asserted on the Repository
  and nowhere else.
- **Targeted call sites are a correctness requirement, not economy.** Shutdown
  has an order: the MessageBroker must stop accepting before the Repository
  closes, or in-flight state is lost, and telemetry must flush after everything
  it observes. A loop over an abstract plugin list cannot express that; named
  call sites can.

### 1.2 Corrections the audit produced

- **ADR-023 is already `Accepted` v.3**, so half of
  [#272](https://github.com/dr-dobermann/gobpm/issues/272) is closed; only
  ADR-004 (`Draft` v.1) needs its re-acceptance run.
- **#269 says §4.6 has 11 steps and the roadmap's "12" is wrong.** §4.6 lists
  **12** numbered items; two (3 and 5) are explicit *no-move* decisions, which
  is likely where the count diverged. The roadmap is right.

## 2 Requirements

### Functional

- **FR-1 — the lifecycle and observation hooks exist**, in
  `pkg/renv/capabilities.go` beside `Migrator` and `ClusterAware`, exactly as
  ADR-002 §8.3 shapes them: `Starter`, `Stopper`, `HealthChecker`,
  `RuntimeAware`. All four are **optional**: an adapter that does not implement
  one is not asked, and that is not an error.
- **FR-2 — the engine invokes them at named per-seam call sites**, in the order
  §3.2 fixes. `Stop` is called on the seams that hold resources, in reverse
  dependency order; `Start` in forward order; `UseRuntime` during `New`.
- **FR-3 — `Stop` is idempotent.** A second call is a no-op returning nil. This
  is a contract requirement, not an implementation detail: the server may stop
  an adapter it started before the engine existed (ADR-004 §4.3's failure
  path), and the engine stops what it holds. Idempotency is what makes that
  seam safe.
- **FR-4 — ADR-003's catalogue is reconciled with the code** (three stale
  entries, §4.1) **and gains the battery-vs-adapter criterion** (§4.6), carried
  by an ADR-003 v.1 → v.2 amendment.
- **FR-5 — the four conformance helpers named by §4.2 exist**: `messagingtest`,
  `expressiontest`, `taskstest`, `authtest`.
- **FR-6 — depguard denies `examples/* → runtime/*`.** §4.4 also forbids
  `examples/* → adapters/*`, but two examples demonstrate adapters today
  (`decision-table` → `dtable`, `script-task` → `lua`) and an adapter nobody
  demonstrates is an adapter nobody can learn to wire. ADR-003 v.2 amends §4.4
  to permit it; only the server boundary stays closed.
- **FR-7 — ADR-003 §5's departure table is closed row by row**, and the document
  moves `Draft` → `Accepted`.
- **FR-8 — the `script` port ships a battery**: `pkg/script/gofunc`, a registry
  of named Go functions, wired by default in `internal/enginert` alongside the
  expression batteries and suppressible by `WithoutDefaultScriptEngines()`. A
  model containing a Script Task must execute on a stock engine.

- **FR-9 — everything gobpm publishes is provably implementable from outside.**
  One depguard rule: `pkg/**` denies `internal/`, excepting `pkg/thresher/**`.
  Measured at `10467bb`, `pkg/thresher` is the **only** non-test package under
  `pkg/` that imports `internal/` (49 imports; every other package, 0) — it is
  the engine facade, so it wires internals rather than implementing a port. The
  property holds today by coincidence; the rule makes it a guarantee, which is
  what lets a battery serve as the reference implementation an outside author
  copies. It subsumes the existing `model-no-internal` rule.
- **FR-10 — the examples are actually linted.** `exclusions.paths` carries
  `examples`, so every issue in every example module is discarded: `ci-examples`
  runs the linter over 49 modules and reports "0 issues" unconditionally
  (§4.7). The entry is removed and the 198 issues it hides are fixed.
### Non-functional

- **NFR-1 — no public API breaks, and no seam loses structural satisfaction.**
  Every hook is optional and separate: `observability.Logger` must still be
  satisfied by `*slog.Logger` directly, which a mandatory method would end.
- **NFR-2 — the gate stays green**, including `workflow-check`, `link-check`
  and diff-coverage ≥95% on changed lines.
- **NFR-3 — the conformance helpers are usable by an out-of-repo adapter
  author**: exported, documented, depending only on the public surface.

## 3 Shapes

### 3.1 The hooks

```go
// pkg/renv/capabilities.go — beside Migrator and ClusterAware.

// Starter is the optional capability an adapter implements to learn that the
// engine is starting, before it accepts work. An error aborts the start.
type Starter interface{ Start(ctx context.Context) error }

// Stopper is the optional capability an adapter implements to release what it
// holds. Called once the work that depends on it has drained. It MUST be
// idempotent: a second call is a no-op returning nil.
type Stopper interface{ Stop(ctx context.Context) error }

// HealthChecker is the optional capability an adapter implements to answer,
// on demand, whether it is presently usable. Pull, not push: the engine's
// observation stream reports what HAS happened, and cannot answer "is this
// healthy right now" for a host's readiness probe.
type HealthChecker interface{ HealthCheck(ctx context.Context) error }

// RuntimeAware is the optional capability an adapter implements to receive the
// engine's resolved services (ADR-002 §8.3 Pattern C). It is how an adapter
// emits its operational statistics and inner-state changes: through
// rt.MetricsRecorder(), rt.Tracer() and rt.Logger(), the same path every engine
// component uses. Called during New, after the engine resolves its config.
type RuntimeAware interface{ UseRuntime(rt EngineRuntime) }
```

`RuntimeAware` needs no new observation API: `EngineRuntime`
(`pkg/renv/engineruntime.go:26-35`) already exposes `Logger()`, `Tracer()` and
`MetricsRecorder()`. An adapter that wants to publish pool exhaustion or a
retry count emits a metric, exactly as ADR-002 §8.2 asks of a production
Repository.

### 3.2 Which seams are asked, and in what order

The principle is **input → work → storage → telemetry** on the way down, and
its reverse on the way up. Telemetry stops last because it observes everything
above it; storage stops after the work that checkpoints into it.

| Order | Seam | Why it holds something |
|---|---|---|
| 1 | `MessageBroker` | subscriptions and connections; stopping first closes the inbound door |
| 2 | `WorkerDispatcher` | worker connections and in-flight dispatch |
| 3 | `TaskDistributor` | human-task queues |
| 4 | `DataStore` (each registered) | per-store connections |
| 5 | `AuthorizationProvider` | directory connections, decision caches |
| 6 | `Repository` | the durable store — after everything that checkpoints |
| 7 | `Tracer`, `MetricsRecorder` | exporter flush; last, so it captures the six above |

`Logger`, `Clock`, `ExpressionEngine`, `RuleEngine` and `ScriptEngine` are not
asked: they hold nothing whose release is the engine's business. An
implementation of one that *does* is free to implement `Stopper` — it simply
will not be called by the engine, and its owner stops it. This is the closed
list working as intended.

Start order is the reverse (telemetry first, so a later failure is observable),
which is also ADR-004 §4.3's phase order for the server — the two layers agree
rather than each inventing a sequence.

### 3.3 Placement in the existing paths

`Shutdown` today is: claim the state transition → cancel the engine context →
`drainInstances` → `eventHub.Shutdown` (`thresher.go:779-841`). The adapter
`Stop` calls go **after** `eventHub.Shutdown`: waiters hold broker
subscriptions, and draining instances still checkpoints through the Repository.

A `Stop` failure does not abort the sequence — the remaining adapters are still
stopped and the failures are joined, for the reason FIX-038 §1.5 landed on:
abandoning the rest leaves live resources with no second chance.

### 3.4 The `script` battery

The core takes no interpreter dependency (SAD-001 G2), so the battery executes
Go the host compiled in. The BPMN `script` text is therefore the **name of a
registered function**, not source — the same move `gooper` makes for Service
Tasks, where the "operation" is a Go functor rather than a remote call.

```go
// pkg/script/gofunc

// GoFuncType is the engine kind (the "##"-hint convention, as adapters/lua's
// "##Lua"). Its claimed scriptFormat hints are "application/x-gobpm-gofunc"
// and "gofunc".
const GoFuncType = "##GoFunc"

// ScriptFunc is an in-process Go script body. It receives the per-execution
// read-only data reader and returns the named outputs the Script Task commits
// — the same shape script.Engine.Execute returns for every other engine.
type ScriptFunc func(ctx context.Context, r service.DataReader) (script.Outputs, error)

// New builds the registry. WithScript registers one named body; a duplicate
// name and an empty name both fail loud at construction, not at execution.
func New(opts ...Option) (*Engine, error)
func WithScript(name string, f ScriptFunc) Option
```

`Execute(ctx, format, name, r)` resolves `name` in the registry and calls it. An
unregistered name fails loud **listing the registered names** — the failure a
modeller actually hits, so it must say what is available rather than only what
is missing.

Wiring mirrors the expression batteries exactly (`enginert.go:76-79`,
`WithoutDefaultExpressionEngines`): `internal/enginert` builds a default script
registry containing the `gofunc` engine, and `WithoutDefaultScriptEngines()`
starts it empty for the "remove it from the runtime if unused" posture.

## 4 Analysis

### 4.1 Three stale catalogue entries

| ADR-003 says | Reality | Resolution |
|---|---|---|
| `pkg/observability/slog/` is the `Logger` default | the battery already exists and is `slog.Default()`, wired by the engine: `Logger` is defined as "the leveled subset of `*slog.Logger`, so the standard library's `*slog.Logger` satisfies it directly", asserted at `pkg/observability/logger.go:28` | drop the entry — a gobpm package would wrap a type that already satisfies the interface, and the port is not battery-less |
| `pkg/extension/` holds the lifecycle traits | `pkg/renv/capabilities.go` holds `Migrator` and `ClusterAware`, both citing ADR-002 §8.3, both structurally satisfied | drop the entry; the traits join their siblings in `pkg/renv` (FR-1) |
| §4.6 step 5 — human interaction "stays internal" | `pkg/interactor/` exists, with `console` as its battery | record the promotion in v.2 (decided at the doc gate) |

All three are the document lagging the code, but the third differently: the
first two describe packages that should not exist, while `pkg/interactor/`
describes a package that does. Under a closed seam list a package in `pkg/` *is*
a public extension point, so the position is already taken — v.2 records
`TaskDistributor` as a promoted seam with `console` as its battery, and drops
step 5's "human interaction is deferred" clause, which the code has outrun.

What v.2 does **not** do is settle the human-interaction design that ADR-001
v.4 §9 reserves. Recording that the seam is public is a statement about layout,
which is ADR-003's subject; the `Registrator → TaskDistributor` naming and the
interaction model remain that ADR's to decide, and it may still reshape the
interface. Nothing here pre-empts it.

### 4.2 Why the hooks are optional rather than mandatory

| Option | Verdict |
|---|---|
| Every adapter must implement `Stopper` | **Rejected.** It ends structural satisfaction: `*slog.Logger` cannot grow a `Stop` method, so `observability.Logger` would no longer be satisfiable by the standard library — a property that package documents deliberately. It also forces `func (x) Stop(context.Context) error { return nil }` on every trivial adapter. |
| Optional, asserted per seam — **chosen** | ADR-002 §8.3's stated model: "adapters that don't implement them just work… small adapters stay simple; large adapters get lifecycle hooks when they need them." |

### 4.3 Why not a registry

Considered and rejected: the adapter set is closed and compiled in, so a
name → constructor table buys indirection and costs the ability to see at
compile time what a build can be configured with. `sql.Register` exists for
third-party drivers loaded by blank import; gobpm has no such case, and none is
planned. The server selects among known constructors from its config
(ADR-004 §3.5) — a `switch`, not a registry.

### 4.4 Batteries included — one port ships without one

ADR-002's bundled-default principle says every advertised port arrives with a
working implementation, so a stock engine runs with no wiring. The audit at
`10467bb` finds that true for every seam but one:

| Port | Bundled default |
|---|---|
| `auth` | `allowall` |
| `clock` | `syscl` |
| `messaging` | `membroker` |
| `repository` | `memrepo` |
| `tasks` | `localdispatcher` |
| `observability` | `noop`, `memmetrics`, `memtrace`, and `slog.Default()` for `Logger` |
| `rules` | `gorules` |
| `datastore` | `memstore` |
| `interactor` | `console` |
| `model/expression` | `goexpr`, `lite` |
| **`script`** | **none** — `pkg/script/script.go:9`: "the in-core default is the empty Registry — `##None` — whose execution fails loud" |

One advertised **adapter** is likewise empty: `adapters/sqlite` is a scaffold —
one `doc.go`, no implementation — though ADR-002 §4.2 and ADR-003 §4.2 both
name it as a SQLite-backed Repository. It stays as ADR-003 §4.6 step 2's
placeholder, and its implementation is filed as
[#316](https://github.com/dr-dobermann/gobpm/issues/316); this document only
requires its `doc.go` to point there rather than read as though an
implementation is imminent (FR-4).

A model containing a **Script Task** — a standard BPMN element the conformance
ledger counts as covered — therefore cannot execute on a stock engine; the user
must wire `adapters/lua` or another engine first. Every other standard element
runs out of the box.

The likely reason is that a script engine implies an interpreter, and the core
holds to stdlib + `uuid` (SAD-001 G2). But the same constraint applies to
Service Tasks, and those ship `pkg/model/service/gooper` — a Go-function
implementation needing no dependency. The analogous battery for Script Tasks is
a Go-function script registry, and it is the shape this gap wants.

**Decided at the doc gate: the port gains a `gofunc` battery, in this
document** (FR-8, §3.4, §4.5).

### 4.5 Why the script text is a name, not source

| Option | Verdict |
|---|---|
| The `script` text is Go **source**, compiled at runtime | **Impossible** — Go has no runtime compiler in the standard library, and shipping one is not a core concern. |
| Embed a Go interpreter (`yaegi`) as the core battery | **Rejected** — a substantial third-party dependency in a core that holds to stdlib + `uuid` (SAD-001 G2). It is a legitimate `adapters/` module, exactly like `adapters/lua`, and this document does not preclude one. |
| The `script` text names a **registered Go function** — chosen | The only shape a dependency-free core can offer, and it has precedent: `gooper` makes the same move for Service Tasks. The host writes Go, registers it under a name, and the model references that name. |

The honest limitation, stated so nobody mistakes the battery for more than it
is: this does not make a `.bpmn` file authored elsewhere run unchanged, because
its inline script body is source, not a name. It makes **gobpm-authored models
with Script Tasks** run with no wiring, and gives a Camunda-migration path a
named seam to bind to. An imported model with inline source still needs
`adapters/lua` or its own adapter — which §4.4's gap analysis already implied
and which this battery does not close.

### 4.6 Battery or adapter — the criterion, and why location does not move

The tree holds two kinds of implementation and nothing states which is which,
so `adapters/dtable` (a dependency-free `rules.Engine`) looks arbitrarily
placed beside `pkg/rules/gorules` (also dependency-free). The criterion that
actually fits, to be recorded in ADR-003 v.2:

| | Battery | Adapter |
|---|---|---|
| **Test** | every user wants it compiled in | you opt into it |
| **Lives in** | `pkg/<port>/<name>/` — a subpackage of its port | `adapters/<name>/` — its own Go module |
| **Dependencies** | stdlib + `uuid` only (SAD-001 G2) | may take third-party ones |
| **Wiring** | wired by `internal/enginert` with no user action | wired explicitly by the user |
| **Examples** | `memrepo`, `allowall`, `syscl`, `membroker`, `gorules` | `postgres` (pgx), `lua` (gopher-lua), `dtable` (optional DMN capability) |

It is *not* "needs a dependency → its own module": `dtable` needs none and is
still an adapter, because a DMN decision-table engine is not something every
build should carry.

**Moving the batteries into `adapters/` was considered and rejected.** They are
separate Go modules, so a battery there could not be wired by core without core
taking a module dependency — which ends batteries-included at the module
boundary. ADR-003 §3.3 already decided sibling-subpackage locality for the
pay-for-what-you-use reason, and the subpackage split already delivers it: a
user wiring `postgres` compiles no `memrepo`. The stdlib arranges itself the
same way (`database/sql` + `database/sql/driver`, `slog.NewTextHandler`).

**What the move was reaching for is real, and is about privilege rather than
location.** A battery is only a valid reference implementation if it uses
nothing an outside author cannot use. Measured at `10467bb`:

| | imports from `internal/` |
|---|---|
| 12 port packages | 0 |
| 13 batteries | 0 |
| `pkg/thresher` (the facade) | 49 |

The partition already exists — ports and batteries are outsider-writable, the
facade is the engine — but nothing enforces it, so a battery could import
`internal/` tomorrow and silently stop being copyable. FR-9 is that rule.

### 4.7 The examples are not linted, and have not been

`.golangci.yml`'s `exclusions.paths` lists `examples`, which discards every
issue found in an example module. `make ci-examples` runs
`lint-all-modules MODULES="$(EXAMPLE_MODULES)"` and CI has a job for it, so the
gate *intends* to lint them; it reports "0 issues" for all 49 regardless. This
is the FIX-034 / FIX-039 class — a step that checks nothing while looking
green.

Two consequences, both measured by removing the entry at `10467bb`:

- **No depguard rule has ever fired on an example.** The existing
  `examples-no-internal` rule is dead, which is why `script-task` imports
  `adapters/lua` unchallenged. A probe rule denying `adapters` reported 0
  issues with the entry present and 1 with it removed.
- **198 issues are hidden across all 49 modules**, while the core module passes
  the same configuration with 0 — so example quality has drifted silently:

| Linter | Count |
|---|---|
| `govet: shadow` | 108 |
| `misspell` | 23 |
| `forcetypeassert` | 22 |
| `revive` | 17 |
| `gocyclo` | 10 |
| `gocritic` | 8 |
| `fieldalignment`, `unused`, `prealloc`, `gosec` | 10 |

**Decision: examples are held to the library standard** — the entry is removed
and all 198 are fixed (FR-10). The alternative considered was an
examples-specific subset excluding `shadow` and `forcetypeassert` (130 of the
198, both arguably fighting teaching clarity); it was rejected because a second
standard is a second thing to keep true, and an example is the first code a
user copies.

## 5 Tests

| # | Test | Asserts |
|---|---|---|
| T-1 | `TestStarterIsCalledBeforeWork` | an adapter implementing `Starter` has `Start` called once, before the engine accepts work |
| T-2 | `TestStarterFailureAbortsRun` | a failing `Start` fails `Run`, names the seam, and leaves the engine `NotStarted` |
| T-3 | `TestStopOrderIsBrokerBeforeRepository` | the recorded call order matches §3.2 — the ordering claim pinned, since it is the correctness argument for targeted sites |
| T-4 | `TestStopFailuresAreJoinedAndDoNotAbort` | two failing `Stop`s both surface and the seams after them are still stopped |
| T-5 | `TestStopIsIdempotent` | a second `Stop` on the engine's path is a no-op returning nil (FR-3) |
| T-6 | `TestSeamWithoutHooksIsUntouched` | an adapter implementing none of the four is neither started nor stopped, and does not fail the run |
| T-7 | `TestUseRuntimeReceivesTheResolvedRuntime` | a `RuntimeAware` adapter receives an `EngineRuntime` whose `MetricsRecorder()` is the configured one |
| T-8 | `TestHealthCheckerIsReachable` | a host can reach `HealthCheck` on a configured adapter |
| T-9 | conformance-helper self-tests (×4) | each new `<pkg>test` helper passes against its in-repo default and **fails** against a deliberately broken fake — a helper that cannot fail proves nothing |
| T-11 | `TestScriptTaskRunsOnAStockEngine` | a process with a Script Task naming a registered function executes end to end on `thresher.New` with no engine wiring — the battery's whole point (FR-8) |
| T-12 | `TestUnregisteredScriptNamesTheRegistered` | an unknown name fails loud and the error lists the registered names |
| T-13 | `TestWithoutDefaultScriptEnginesIsEmpty` | the suppression option leaves the registry `##None`, preserving today's fail-loud posture for hosts that want it |
| T-14 | `make depguard-check` | a throwaway file in a battery package importing `internal/` is denied (FR-9); `pkg/thresher` importing `internal/` still passes, since the facade legitimately does |
| T-15 | `make lint-all-modules MODULES="$(EXAMPLE_MODULES)"` | reports real issues rather than an unconditional 0 — verified by the probe that returns 1 issue with the exclusion removed and 0 with it present (FR-10) |
| T-10 | `make depguard-check` | a throwaway file under `examples/` importing `runtime/` is denied (FR-6). Not a committed fixture: once FR-10 lands the examples are genuinely linted, so a permanent violating file would keep the gate red — the check mirrors `consumer-smoke`, which builds a throwaway module for the same reason |

## 6 Follow-ups (filed, not deferred)

- **The `pkg/* → internal/*` exposure caveat has no mechanical check.** §4.4
  permits the import but forbids *exposing* internal types in public
  signatures — an API-surface property depguard cannot see. It needs a
  `go/types` walk over exported signatures, which is a tool, not a rule. Filed
  with that trigger so §5's gate is not closed on a claim nothing verifies.

## 7 Cross-document references

| Doc | Version | Used for |
|---|---|---|
| [ADR-003](../design/ADR-003-module-layout.md) | v.1 → v.2 | §4.2 catalogue, §4.4 import rules, §4.6 steps, §5 gate |
| [ADR-002](../design/ADR-002-extension-architecture.md) | v.2 | §8.3 side-capability interfaces and their call points; §8.2 adapter operational expectations |
| [ADR-004](../design/ADR-004-runtime-environment-contract.md) | v.1 | §3.5 config model, §4.3 phase order, §4.4 graceful shutdown |
| [SAD-001](../design/SAD-001-vision-and-architecture.md) | v.1.1 | §9.1 — the division is enforced by import direction |

## 8 Definition of Done

- FR-1…FR-7 landed and wired; T-1…T-10 green.
- `make ci` PASS end to end (verdict in `.ci/last-run.json`, per FIX-039).
- Diff-coverage ≥95% on changed lines; every touched function ≥80%, aim 100%.
- ADR-003 §5's departure table closed row by row; ADR-003 `Draft` → `Accepted`,
  v.1 → v.2, with a `Document History` row.
- #269's stale claims corrected in the issue by comment, its delivered
  checkboxes ticked.

## 9 Open questions

None. Both items raised during review are decided: the `script` battery rides
this document as FR-8, and `pkg/interactor/`'s promotion is recorded in
ADR-003 v.2 (§4.1).
