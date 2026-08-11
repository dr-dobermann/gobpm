# Changelog

All notable changes to the GoBPM project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **A parallel Multi-Instance leaf activity's instances are no longer
  tracks, and no longer get a scope apiece** (ADR-025 v.3 §2.13,
  SRD-090.A M2b, part of #313). Iterating a leaf three times used to
  fork three tracks into three per-instance scopes, coordinated by a
  loop-owned barrier; the activity's decorator now holds one executor
  per instance, isolates each in its own execution frame, and runs the
  N-of-N barrier as ordinary control flow. A track means a token
  walking a path again.

  **One behaviour changes, deliberately.** Each instance used to run in
  a scope of its own, so anything it wrote beyond its declared
  `outputDataItem` died when that scope closed. With no per-instance
  scope, such a write reaches the **enclosing** scope, and for a
  parallel activity the last writer wins — which is order-dependent.
  The declared output collection is unaffected: it is still assembled
  positionally by ordinal and published once, so a model that reads its
  `loopDataOutputRef` sees exactly what it saw before. A model that
  relied on undeclared writes vanishing now sees the last one.

  The checkpoint schema moves 5 → 6 with it: an iterated leaf persists
  as an executor set keyed by ordinal, replacing the per-instance track
  records and the group record it used to be scattered across.
  Documents written by the previous release still restore.

### Fixed

- **A `%v` over an engine object no longer reflects across it**
  (FIX-040, closes #314). `Instance` and its tracks now implement
  `fmt.Stringer`, rendering as their element id. Without it, anything
  formatting one with `%v` — a log line, an error, a test double's
  argument matcher — walked the struct through reflection, reading the
  correlator's maps and the mutexes guarding them from whatever
  goroutine happened to be formatting. That read takes no lock and
  cannot: `fmt` reaches the fields behind the type's back. The visible
  symptom was a `-race` report whose every frame was engine code
  holding its locks correctly. Diagnostics improve as a side effect —
  an instance prints as its identity rather than a page of internals.

### Changed

- **BPMN converter: the import parser is rebuilt, and six of its own
  defects are fixed** (ADR-024 v.4, SRD-089.A). Element dispatch is now
  a table keyed by parse context and local name rather than six
  disagreeing `switch` statements, and forward references (a gateway's
  `default` today; `attachedToRef`, `calledElement` and link pairing
  next) resolve through one mechanism that names the referring element,
  the attribute and the missing id — and distinguishes a target of the
  **wrong kind** from one that does not exist, since a `default` naming
  a start event is a modeling mistake, not a typo.

  Six behaviours change. **Export is deterministic**: it walked Go's
  randomized map iteration, so two exports of one process differed and
  an exported file could not be diffed; nodes are now emitted from the
  start events along outgoing flows in flow-id order, unreachable ones
  by id. A **`<task>`/`<manualTask>`/`<userTask>` with no `name`**
  imports instead of being refused (BPMN makes `name` optional; the id
  is the fallback, as `<process>` and `<serviceTask>` already did).
  **`<bpmn:documentation>`** is imported onto `Docs()` and written back
  with `textFormat`, instead of being dropped in both directions.
  **`serviceTask@implementation`** round-trips. A **`parallelGateway`**
  is no longer exported with a `default` attribute BPMN §13.4.1 does
  not define. And the **purely visual artifacts** (`<textAnnotation>`,
  `<group>`, `<category>`, plus `<relationship>`/`<import>`) are
  skipped rather than refused — a runnable file was being rejected for
  carrying a comment. `<association>` is deliberately still refused:
  it carries compensation semantics.

### Added

- **`activities.WithImplementation`** (SRD-089.A). BPMN carries
  `implementation` on the `ServiceTask` itself, while gobpm derived it
  from the Operation's `Implementor` — which an imported operation
  deliberately lacks, so a document's own hint had nowhere to live. The
  option gives it one; unset, the derived value stands, so no existing
  caller changes.

### Added

- **Adapter lifecycle and observation hooks** (SRD-090, closes #269).
  `renv.Starter`, `renv.Stopper`, `renv.HealthChecker` and
  `renv.RuntimeAware` join `Migrator` and `ClusterAware` in
  `pkg/renv/capabilities.go`. All are optional and satisfied
  structurally — an adapter implements one by having the method. The
  engine calls them at named per-seam sites in a fixed order, because
  shutdown has one: the message broker stops accepting before the
  repository closes, and telemetry flushes after everything it
  observes. `Stop` is idempotent by contract, so an adapter the host
  started before the engine existed can be stopped by either.
  `Thresher.HealthCheck` asks every seam and joins what they report.
- **Four conformance helpers for adapter authors** (SRD-090):
  `messagingtest`, `expressiontest`, `taskstest` and `authtest`, the
  names ADR-003 §4.2 had listed but nothing provided. Each publishes
  its port's contract as `Conformance(t, factory)` — one line in an
  adapter's test — and each carries a negative control proving the
  suite can fail. `messagingtest` and `taskstest` also publish
  `Waits()`/`SetWaits()`, so an adapter over a remote backend can widen
  bounds tuned for an in-process one.
- **A dependency-free Script Task engine** (SRD-090):
  `pkg/script/gofunc` runs a Go function the host registered under a
  name, the same move `gooper` makes for Service Tasks. It is opt-in —
  an auto-wired empty registry would be `##None` with a longer name —
  and `adapters/lua` remains the choice for interpreted source.

### Changed

- **A process whose Script Task no configured engine can run is refused
  at registration** (SRD-090). `RegisterProcess` walks the model, nested
  Sub-Processes included, and rejects any Script Task whose
  `scriptFormat` no wired engine claims — naming the task, the format,
  the formats that ARE registered, and the option to wire one. This
  moves an existing failure earlier: it previously surfaced
  asynchronously, inside an already-running instance, as an incident.
  **Migration:** a model with a Script Task now needs its engine wired
  before `RegisterProcess`, not before the token arrives.
- **`pkg/**` may not import `internal/`** (SRD-090), enforced by
  depguard, excepting the `pkg/thresher` facade. This is what makes a
  bundled battery a reference implementation an outside author can
  copy.
- **The examples are linted** (SRD-090). An `exclusions.paths` entry had
  been suppressing every finding across all 49 example modules, so the
  `examples-no-internal` rule had never fired. 198 real issues surfaced
  and are fixed.

### Fixed

- **`goexpr.Engine.Evaluate` panicked on a nil expression** (SRD-090)
  where `lite` returns a named error — a public extension point turning
  a caller's bug into a library crash. A nil *source* is still passed
  through, deliberately: a `GExpression` may carry one bound at
  construction.
- **A failed start left already-started adapters running** (SRD-090).
  `startSeams` now unwinds the started prefix in reverse before
  returning, joining any rollback failure onto the cause rather than
  replacing it.
- **A replaced Data Store stayed in the lifecycle** (SRD-090).
  `WithDataStore` documented replace-by-ref and the registry honoured
  it, but the lifecycle list appended — so a superseded store was still
  started, health-checked and stopped while serving no reference.
- **`gofunc` could register an unreachable script** (SRD-090): the name
  was stored untrimmed and looked up trimmed, so a padded registration
  failed as "no script registered" on a name plainly in the registry.

### Changed

- **BREAKING: instance discovery queries compose** (SRD-084, closes
  #306). `Thresher.Instances` now takes an `InstanceQuery` — four
  ANDed axes (`Kind`, `Stage`, `ProcessID`, `ParentID`; the zero value
  lists everything) — and returns `([]string, error)`; the single-axis
  `InstanceFilter` enum and its five constants are removed. Migration:
  `InstancesAll` → `InstanceQuery{}`, `InstancesRunning/Completed` →
  `InstanceQuery{Stage: StageRunning/StageSettled}`,
  `InstancesRoots/Children` → `InstanceQuery{Kind: KindRoots/
  KindChildren}`. An out-of-range axis value now refuses instead of
  silently listing everything, and handles gain `ProcessID()`.

### Added

- **Leaf-activity Multi-Instance execution** (SRD-086). A sequential
  leaf runs its passes in place (fresh frame each, split item +
  loopCounter bound); a parallel leaf fans out per-instance scopes
  each running one track at the task; both restore at their position
  over the existing checkpoint schema. A leaf that **waits** stays
  unsupported and is now **refused at build time** rather than run
  wrongly: an activity that both iterates and parks on execution (a
  ReceiveTask or other catching event node, a user task, an external
  worker) fails `snapshot.New` with a message naming it and pointing at
  the workaround — model the wait inside an iterated Sub-Process. The
  decorator that would make it work natively is tracked in #313.


- **In-instance event delivery: per-delivery payload binding and
  iteration-correlated routing** (ADR-006 v.5 §2.9, SRD-085, closes
  #305). A fired definition's payload now travels with the DELIVERY —
  captured by the receiving execution and bound from its own frame —
  and `events.WithIterationCorrelation(keyName, expr)` routes a
  message to exactly the matching parallel-MI iteration; a second
  keyless concurrent waiter on one definition refuses loudly.


- **Ad-Hoc Sub-Process checkpoint fidelity** (SRD-083, closes #307).
  The checkpoint document (schema 5) records each open container's
  routing state — completed counts, a manual container's pending
  offer, a fired completion condition — and the routed tracks'
  activity assignments; restore rebuilds the container at that
  position, so the next Router decision sees the true cross-crash
  progress and completed activities never re-run.

- **Checkpoint fidelity for composite constructs** (SRD-082, closes
  #277). The checkpoint document (schema 4) records every composite
  construct's position, and restore rebuilds it there: composite
  scopes resume their host exactly once (previously the body
  double-executed — or the document refused to restore at all, since
  inner nodes were unresolvable); sequential MI and Standard Loop
  resume at the recorded pass with their collected outputs; parallel
  MI re-opens exactly its still-open ordinals; a resolving
  compensation sweep continues in order with its RUNNING handler
  re-run (at-least-once over the immutable snapshot) and the
  wait-throw resuming only after the drain; and a Call Activity child
  is now a durable instance of its own, symmetrically linked — kill
  an engine mid-call and the next one re-links the SAME child, never
  a duplicate, with loud refusals when either record is missing. The
  capture-deferral guards are fully retired: `CheckpointDeferred` now
  means a real failure only. Discovery separates the registry:
  `Instances(InstancesRoots/InstancesChildren)` and
  `InstanceHandle.ParentID()/CallNodeID()`. Design: ADR-033 v.4
  §2.10, ADR-023's restart contract.

- **Incidents: a technical failure becomes durable, operable state**
  (ADR-036, SRD-079). An unhandled failure — an in-process task error, a
  worker whose job retries exhausted, an uncaught BPMN error — no longer
  terminates the instance: the failing attempt's track ends and a durable
  **incident** opens, carrying the node, the cause chain and class, the
  attempt history and a **failure-time data snapshot** (the variables visible
  from the failing node's scope, exactly as the attempt saw them). Sibling
  branches keep running; the token stays visible at the node
  (`TokenIncident`); the incident rides the checkpoint (schema 3) under a new
  `repository.StatusActiveIncidents`, so "what needs an operator" is a store
  query. An **incident retry policy**
  (`activities.WithIncidentRetryPolicy` / `thresher.WithIncidentRetryPolicy`)
  re-enters the node by respawn — lineage carried, armed boundary timers
  transferred, never re-armed, so an SLA clock is never reset by failing —
  and with no policy (the default) the incident waits for the operator:
  `InstanceHandle.Incidents()`, `RetryIncident`, `ResolveIncident` (continue
  past the node without re-executing it), `DropIncident` (a durable dead
  letter the process never silently completes past). Ops on a parked instance
  rebuild it from its checkpoint transparently. Three failures deliberately
  keep the fatal path: invariant violations, an Error End Event's own
  uncaught throw, and any failure in a called process — that one propagates
  across the call boundary, and the incident arises at the top-level
  caller's Call Activity, whose retry re-runs the whole child.
  Runnable: `examples/incident-retry/`; guide:
  `docs/guides/operating/incidents.md`.

- **The documentation site gets a structured sidebar, a Russian design-docs
  group, and rendered Mermaid diagrams** (SRD-081): sections read Home →
  Developer Manual → Design documents → BPMN 2.0 extract → Landing records →
  Project (curated via pinned `mkdocs-awesome-nav` + `.nav.yml` files; page
  listings stay derived), the Manual's parts follow the index's reading
  order, and the 84 pages with ` ```mermaid ` fences now render diagrams
  instead of code blocks. The Russian-twin convention is enforced in the
  tree: twins are a SAD/ADR privilege living under `docs/design/ru/`; the
  38 stale SRD/FIX twins are removed (git history keeps them). The READMEs'
  status line drops its hardcoded version in favor of the tag badge.
- **The documentation publishes as a searchable site** (SRD-080):
  <https://dr-dobermann.github.io/gobpm/> — the developer manual, the design
  docs, and the BPMN 2.0 extract, built with MkDocs Material from the `docs/`
  tree (`camunda7/` stays internal). A new `docs` workflow validates every
  docs-touching PR with `mkdocs build --strict` and redeploys the site on
  every docs-touching push to `master`; links that leave `docs/` are
  rewritten to GitHub URLs at build time, so the Markdown sources stay
  relative and in-repo reading is unchanged. Locally: `make docs-build` /
  `make docs-serve` (pinned `mkdocs-material`, guarded like the Go tools).
  The Monday pin sweep now also covers the new PyPI pin and the
  previously-unswept `linkcheck`, and bumps only the Makefile pin line
  itself instead of every occurrence of the version string.
- **A durable PostgreSQL repository** (`adapters/postgres`, SRD-078,
  closes #276). `postgres.New(db)` over a user-owned `*sql.DB` stores
  instance checkpoints in a namespaced schema the adapter migrates
  itself at `Run` (embedded versioned SQL under an advisory lock; a
  failure aborts the start loud). CAS saves and ownership leases make
  the fencing a database guarantee — proven by a real zombie-engine
  test and a kill-and-resume e2e over postgres. Recovery is now scoped
  to **engine groups**: records carry their creator's group, an
  ungrouped engine is a solo group under its own id, and
  `thresher.WithEngineGroup` / `WithExistingEngineGroup` (join-only,
  the typo-guard) form explicit recovery clusters over one store. The
  storage is tenant-ready (ADR-033 v.3): each record carries its
  tenant, `""` resolves to the group's flag-designated default row,
  and a partial unique index enforces one default per group. New
  `pkg/renv` capabilities `Migrator` and `ClusterAware`; a published
  conformance suite (`pkg/repository/repositorytest`) every adapter
  runs — memrepo and postgres both pass it. `make pg-up`/`pg-down`
  provide the disposable test database; CI runs the postgres paths on
  every PR.

- **A timer can now wait for a plain duration.** `timeDuration` alone —
  BPMN's one-shot relative timer, *"wait five minutes, then fire"* (§10.5.5,
  Table 10.101) — was unconstructible: the guard required a `timeCycle` beside
  it, so the most common timer there is had no expression. It works now, and
  nothing below the constructor changed to make it work: the waiter already
  armed on a duration, already terminated after one delivery, and `TimerPlan`
  already derived `now + d` for the checkpoint. Only the model layer refused.
  Relative timers previously had to be faked with a `timeDate` expression
  computing `time.Now().Add(d)` at evaluation time — a workaround that bypasses
  the engine's injected `Clock`, so a substituted clock could not govern it.

- **Timers can be written in ISO 8601, the notation the standard uses.**
  `events.NewISO8601Timer("PT5M")` / `("R3/PT10H")` / `("2011-03-11T12:13:14Z")`
  takes one string and disassembles it into the attributes the engine stores —
  a recurrence fills both `timeCycle` and `timeDuration`, so the pair never has
  to be written by hand. `NewISO8601TimerExpr` makes the timing **dynamic**:
  the expression yields the ISO string when the timer arms, so a deadline can
  come from the instance's own data. The form is named by the caller there
  (`events.Time` / `Duration` / `Cycle`), because the value does not exist
  until arming while the attribute is fixed at build time — which is how BPMN
  itself resolves it. Neither path changes the runtime: a dynamic timer
  installs an adapter expression that parses at evaluation, so the waiter
  still reads a typed value exactly as before.

  The parser (`pkg/iso8601`) refuses rather than approximates: `P1Y`/`P1M`
  (not fixed-length), `P1W2D` (ISO makes the week form exclusive), fractional
  components, lowercase designators, zero and negative values, and unbounded
  recurrence `R/PT10H` — which nothing in the engine can consume safely. Each
  refusal names its reason. No new dependency; it is hand-written over stdlib,
  since `time.ParseDuration` reads Go's `10h` syntax and rejects every ISO form.

- **`examples/usertask-sla`** — SLA warnings on a human task. Three *bounded,
  non-interrupting* timer boundaries mark 50% / 90% / 100% of a User Task's
  budget; the operator deliberately overruns, so every warning fires and the
  approval **still completes**. That last part is the point: a non-interrupting
  boundary must not cancel the work it warns about, and the run asserts it
  rather than printing it. They are three separate bounded timers, not one
  recurrence — 50/90/100 is not a uniform interval, so no cycle expresses it.

### Changed

- **`Thresher.UpdateState` now validates the TRANSITION, not just the value**
  (FIX-036). It accepted any legal `State` member and stored it, so a host could
  put a never-run engine into `Started` — after which `RegisterEvent`'s
  `State() != Started` guard admitted registrations to a hub that was never
  started. It now admits only the operator transitions, `Started ↔ Paused`, and
  refuses anything else with a classified `InvalidState` error naming both
  states; starting and stopping remain the compare-and-swap ladder's alone, in
  `Run` and `Shutdown`. A lost race re-reads and re-judges rather than failing,
  so a concurrent pause/resume is not spuriously rejected while a concurrent
  `Shutdown` is reported with the state it actually left behind. **Callers that
  used `UpdateState` to start an engine must call `Run`**; the pause/resume use
  is unaffected.

- **Timer construction errors now name the rule they broke.** The three
  rejections — attributes that are mutually exclusive, a recurrence missing its
  interval, and nothing set at all — previously shared one message
  (*"doesn't allow to define Timer Data or Cycle and Duration simultaneously"*),
  which described the accepted shape without explaining it and made a missing
  case read as an inverted boolean. Each now identifies itself, names the
  offending attributes, and cites Table 10.101 where the standard is the reason.

### Fixed

- **A recovering engine could half-recover a call tree** (SRD-087). In
  a multi-engine group, recovery claimed instance by instance, so a
  caller and the child it awaits could land on different engines: each
  recovered correctly and then the caller's re-attach — engine-local —
  refused, leaving the child running and the caller never resuming. A
  call tree is now the unit of a claim: a child is never revived on
  its own, and a caller's claim walks its recorded call links
  transitively before restoring.
- **A child whose caller had already finished was revived** —
  recovery checked that the caller's record existed, never that it was
  still in flight, so the child came back and ran into a caller that
  completed long ago. Such a child is the unfinished half of a cancel
  cascade: recovery now writes its terminal record and reports it.


- **A leaf task decorated with Multi-Instance silently ran ONCE** —
  executeStep had no leaf-MI branch, so a 3-item collection completed
  with a single execution and no complaint.


- **A shared catch node's payload slot crossed iterations.** All
  parallel-MI iterations bound from ONE mutable cell on the shared
  node (mutex-guarded but single), so an iteration could bind its
  sibling's message; the slot (and the ReceiveTask's twin of it) is
  designed out — the frame carries each delivery's payload.
- **A second same-definition message waiter was silently
  overwritten** — the routing index mapped a definition to one track,
  making the earlier iteration undeliverable.
- **The engine never forwarded the hub's `AddEventKey`**, so no
  instance under the engine could extend a live broker subscription
  (the conversation flow's lazy secondary-key association silently
  no-opped); and the subscription-extension walk visited only
  top-level nodes, missing every message catch inside a composite
  body.


- **An in-flight Ad-Hoc container restored silently corrupt.** The
  routing state was never captured (and, unlike the other composites,
  no guard deferred the capture): after a restore the Router was
  never consulted again, the progress counts were false, a manual
  container's pending offer hung forever, and a stopped container
  re-routed work past its fired completion condition. The state now
  rides the checkpoint; a pre-fidelity document (schema ≤ 4) with an
  in-flight container refuses to restore loudly instead.

- **The checkpoint codec refused nil** — but a parallel MI's staging
  is pre-sized with nil holes, and an early-stopped group *publishes*
  an output containing them: one nil silently poisoned every later
  checkpoint of the instance. The codec now carries an explicit nil
  kind.
- **Shared-node payload race**: N parallel MI bodies share their
  catch node, and concurrent fires wrote the captured payload to it
  unsynchronized. The access is now guarded; the payload-routing
  semantics of node sharing are a filed follow-up.
- **Iteration-counter race**: the loop decorators wrote the pass
  ordinal bare while the checkpoint capture read it under the track
  mutex.
- **The wake latch discarded what it refused, and two retained handles were
  never released** (FIX-037 — the first findings of the `/audit-package` sweep).
  - **A message arriving during a timer wake was permanently lost.** The
    single-flight latch told a second trigger that another wake was already in
    flight, and that trigger returned `nil` — which the event hub and the timer
    service both read as *delivered*. The in-flight wake carries its own
    trigger and cannot deliver another's. An Event-Based Gateway arming a timer
    and a message on one dehydrated instance is the ordinary shape that reached
    it. A refused caller now waits for the in-flight wake and retries its own
    delivery, and a trigger that cannot be delivered is reported rather than
    dropped.
  - **An operator's incident retry could start a second execution loop.**
    `RetryIncident` on a parked instance rebuilt it without taking the latch —
    the only rebuild path that did not. The durable claim does not compensate:
    it retries a lost compare-and-swap rather than failing, so two concurrent
    rebuilds both succeed and two goroutine loops run over one instance's state.
  - **A task action racing a wake unbalanced the residency counter** and could
    act on a still-dehydrated instance, surfacing a spurious failure.
  - **Every dehydrate/rehydrate cycle leaked a context.** A rebuild replaced the
    instance's registration and dropped the previous cancel without calling it,
    so each cycle left a child attached to the engine context for the engine's
    lifetime.
  - **A cancelled timer wait could re-arm itself.** `HoldTimer` registered its
    deadline blind, so a release landing mid-arm withdrew nothing and was
    overtaken by the arm — leaving a deadline that later woke the instance for a
    track that no longer existed.

- **Engine locks held across host calls, and requests that were silently lost**
  (FIX-038 — the second `/audit-package` sweep, over `internal/eventproc`,
  `internal/scope`, `pkg/thresher` and `internal/instance`). Sixteen defects
  sharing two shapes: an engine-wide lock held while foreign code runs under it,
  and an operation that reports success while nothing happened.
  - **One slow host call stalled the whole engine.** The event hub built and
    *started* a waiter while holding its single registry lock — and a message
    waiter's start subscribes to the host's broker, which may be remote and may
    block. Every registration, unregistration and lookup in the engine queued
    behind one host call. The same shape held the engine's registry lock across
    an embedder's `Actor.Groups()` during task authorization, and the scope
    plane's lock across the host's runtime-variable supplier. All three now run
    the foreign call unlocked.
  - **A failed process registration bricked its key.** Registering a new version
    withdraws the previous version's starters first; when the new arm then
    failed, the call returned with *neither* on the hub and the failed version
    still recorded as latest — so every later registration for that key tried to
    withdraw starters that were never armed, failed too, and the key was dead
    for the engine's lifetime. A failed registration now restores the previous
    version exactly, and reports it if that restore also fails.
  - **A transient store error abandoned an instance at startup.** Recovery read
    *every* failure to claim a record as "another engine got there first" and
    returned success, so a connection reset silently left an in-flight instance
    unrecovered and unlogged. Only a compare-and-set conflict means that now.
  - **Cancelling a dehydrated instance did nothing.** `Cancel` cancelled the
    instance's context, but a parked instance has no loop reading it: the
    request vanished, the next wake resumed the instance as if it had never been
    made, and the caller blocked until its deadline. A cancel now rides the
    rebuild, as an incident operation does, and the fresh loop tears the
    instance down before deciding whether to park again.
  - **A host's observer went quiet after the first rebuild.** `Observe`
    registered on the instance *object*, which every rebuild replaces, while its
    `Subscription` still reported itself live. Observers now belong to the
    handle, whose identity outlives the object — and an operator's incident
    retry re-attaches them too.
  - **An operator request after shutdown reported success.** The engine pointer
    is never cleared, so the "not running" guard could not fire: a cancel or an
    incident operation rebuilt the instance, watched the fresh loop tear back
    down on the dead engine context, and returned `nil`. Both entry points now
    refuse.
  - **A concurrent register could orphan itself against an unregister**, and a
    scope snapshot taken during a commit could tear. Both are now taken under a
    single acquisition.
  - **A cancel could still be lost, and an observer could be told twice.** The
    routing that fixes the parked-instance cancel read the state and then
    cancelled, so an instance parking between those two steps lost the request
    exactly as before; the state is now re-read. And a subscription taken while
    the engine was rebuilding an instance was registered twice on the new
    object, so the host received every fact twice from a subscription it could
    only cancel once. Both were found by an independent pre-merge review, on
    code whose gate was already green.
  - **`GetDataByID` became deterministic, not stricter.** An ItemDefinition is a
    *type*, so two variables of one type share its id and Go's map iteration
    returned a different one per run. Resolution is now nearest scope, then
    lowest name: a model that worked by luck now works reliably, and one that
    was picking the wrong variable does so consistently — which is what makes it
    findable.

- **The engine's lifecycle bookkeeping: a data race, reservations with no
  owner, and host code with no containment** (FIX-036 — the remediation of an
  external audit of `pkg/thresher`; eight defects, one shape: bookkeeping
  maintained by convention rather than by machinery).
  - **A repeat business key could never start a process again.** An
    event-started instance reserved its correlation key and nothing released it
    when the instance finished, so a later message carrying that key was
    answered "joined existing instance" and **silently dropped** — there was no
    instance to join. Order `ORD-42` handled today meant `ORD-42` could never
    start a process again, and the map grew for the engine's lifetime. A
    reservation now records *whose* it is and is taken over once that
    conversation is gone. Its mirror image is fixed with it: a conversation
    recovered from a checkpoint — a cold restart or a wake — used to come back
    **unreserved**, so the next message carrying its key started a duplicate
    beside it.
  - **The engine context was written without synchronization.** `Run` assigned
    the context and its cancel with no lock while `Shutdown` read them under
    one, which is a data race, not synchronization — a `Shutdown` could cancel
    nothing and still report `Stopped`. Both are now published as one immutable
    pair through an `atomic.Pointer`, and a launch on an engine that never ran
    is a classified error rather than a nil-context panic.
  - **A held subscription could outlive its own release.** The hub registration
    and the release-path record were written in two unguarded steps, so a
    `ReleaseWaits` racing the arm — an interrupting boundary, an Event-Based
    gateway's losing arm — withdrew nothing and left a subscription registered
    forever, able to wake an instance for a wait nobody was waiting on.
  - **A panic in the host's observability policy killed a business process.**
    `ObservationFilter.FilterObservation` and `LogRedactor.RedactLog` are host
    code called on the reporting goroutine — an instance's execution loop — with
    no recover around either. Both are now contained and **fall closed** (the
    recipient is denied, the record suppressed), counted, and logged once at
    `Warn`. Both interfaces now also state the obligations they always relied
    on: cheap, non-blocking, no call back into the engine.
  - **`Shutdown` left instances running.** It snapshotted the instance registry
    *before* cancelling the engine context and awaited only that snapshot, so an
    instance born in the window — an event-triggered start, a Call Activity
    child — was abandoned. It now re-reads the registry until a pass adds
    nothing, and a timeout names the instances that did not settle instead of
    reporting only that something did not finish.
  - **`Forget` leaked a context per instance it reaped.** Every launch path
    derives the instance's context from the engine's and retains the cancel, but
    nothing ever called it — so the child stayed attached to the engine context
    for the engine's whole lifetime, and the one method whose stated purpose is
    "so a long-running engine doesn't accumulate finished instances" released
    everything about an instance except its context.
  - **A process registered while the engine was starting could be wired twice.**
    `RegisterProcess` and `Run`'s startup sweep both wire the latest version's
    instance-starters and are not mutually exclusive, so one message could spawn
    two instances. Wiring is now claimed per registration.
- **Scope snapshots rejected Properties and DataObjects.** `SnapshotAt`'s
  value-copy (the compensation ledger's and now the incident snapshot's
  machinery) asserted one `Clone` shape, but `Property` and `DataObject`
  declare their own concrete `Clone` methods that shadow it — any walk-up
  surface reaching a process property failed "isn't clonable".
- **A parked child instance read as a completed call.** The Call Activity
  watcher waited on the child's loop-exit signal, which also closes on a
  dehydration park — the caller then resumed with a phantom success. It now
  waits on the engine's cross-rebuild settled signal.

- **memrepo could evict a live instance.** A terminal record re-saved
  back to a non-terminal status stayed in the terminal-eviction
  ledger, so cap pressure could evict an Active instance (audit
  remediation row 11). It now leaves the ledger on the transition —
  an Active record is never evicted (SRD-078 FR-9).

- **`examples/usertask` was compiled but never run.** It had no `go.mod`, so it
  fell into the root module instead of being one of the example modules — and
  the run sweep iterates modules, so 46 of 47 examples were executed and the
  skipped one was the User Task example. It is a module now, and a new
  `examples-module-check` gate fails when any `examples/*` directory lacks one,
  so the sweep cannot silently shrink again. The guard runs in the **required**
  core job, not the non-blocking examples job: a hole in that job cannot be
  guarded from inside it.

## [v0.11.0] - 2026-08-02

**The library's element set is complete, and its conformance claim is finally
accurate.** v0.10.0 announced the element set as done; this release finishes the
job on both sides of that sentence — the last elements landed, and the statement
the project makes about the standard was corrected at its root.

`goBpm` targets **BPMN 2.0.2 §2.3 Process Execution Conformance**, whose two
requirements now have owners: **§2.3.1 execution semantics is the library's**
and is what this release completes; **§2.3.2 import of Process diagrams is the
`gobpm-server` product's**, through the converter. The honest present-tense
claim, in the standard's own terms (§2.1), is that the library **implements §13
execution semantics for the element set in `conformance.md`, with the deviations
registered in SAD-001 §14** — conformance itself is a claim for the server to
make once §2.3.2 is met.

Highlights, for deciding whether to upgrade:

- **BPMN's own resource vocabulary executes.** Declare a `PotentialOwner` or
  `HumanPerformer` and it decides who may act on a User Task, resolved at
  distribution alongside the Camunda triad. Previously such a role was carried,
  surfaced to your distributor, and never consulted.
- **Both UserTask instance attributes are implemented** — `actualOwner` landed in
  v0.10.0, `taskPriority` here.
- **`Lane` / `LaneSet`**, the engine's only model-only elements: carried, nested,
  validated, and provably never executed. They exist so a diagram survives
  import → export, which a model without lanes cannot do.
- **Two role declarations now fail** that previously registered silently — a
  directory-mode role (needs an organizational directory the engine does not
  own) and one naming nobody. Both only ever authorized no one.
- **A `UserTask` accepts `WithRoles`.** It silently rejected the option family,
  which made the role feature unreachable from the one task type it governs.
- **No library code calls a panicking `Must*` constructor** — the ban is now
  absolute, with the previously documented carve-outs removed rather than
  re-justified.

**Conformance pin.** This release is checked against the vendored BPMN extract at
tree `9902730`. `make tag` now records that hash in the tag object, so the pin is
automatic rather than a remembered step (SAD-001 §14).

**Known scope boundaries**, all deliberate and registered in SAD-001 §14:
directory-based resource assignment and group-only reassignment (both awaiting an
identity subsystem), the closed `DataState` model, single `InputSet`/`OutputSet`,
`DataObjectReference`, data-availability wait, and `GlobalTask` — whose authoring
need the library covers with Go constructors (see the tasks guide, "Reusing
tasks") and whose by-reference form belongs to the server's registry.


### Added

- **`Lane` / `LaneSet`** — the engine's only *model-only* elements. A `Process`
  or `SubProcess` can now carry lane sets (`lanes.WithLaneSets`), lanes nest,
  and a lane's membership is declared from the lane (`Lane.Place`) — nothing is
  added to `flow.Node`, so no element reports which lane it is on and no
  execution path can consult one. Lanes are **never executed**: a laned process
  runs identically to the same process without lanes, which is asserted rather
  than assumed. They exist because "non-operational" governs execution, not
  representation — BPMN obliges a tool to support import of Process diagrams,
  and the converter's semantic round-trip cannot re-export a structure the model
  never stored. Registration rejects a lane placing a node its container does
  not hold, and a lane set whose lanes partition by mixed types.

- **BPMN's own resource roles now decide who may act on a User Task.**
  `PotentialOwner` and `HumanPerformer` are no longer carried and ignored: a
  declared human role resolves its assignment expression when the task is
  announced, and its identifiers join the eligible set beside the Camunda
  triad. The standard gives a `ResourceRole` two mutually exclusive ways to
  name people (Table 10.5), and gobpm now implements one of them completely —
  so expression-based resource assignment is conformant and *executed*, not
  merely modelled. Declare a role with `activities.WithRoles(...)`, which a
  `UserTask` finally accepts. A role identifier matches the actor's user id
  **or** one of its groups, because the standard returns "Users or Groups" and
  marks neither; a declared `assignee` still excludes everything else, and a
  task is open to anyone only when neither a triad member nor a human role is
  declared.
- **`UserTask.taskPriority`** (Table 10.14), the second of BPMN's two UserTask
  instance attributes — the first, `actualOwner`, landed in v0.10.0. The
  standard defines a *reader* and nothing else: no scale, no direction, no
  default, and no behaviour that consumes it. gobpm reports it to your
  distributor on `TaskInfo` and acts on it nowhere. `WithTaskPriority` is a
  documented engine extension, since no BPMN XML can set an instance attribute.
- `foundation.EmptyBaseElement`, `values.EmptyRecord` and `values.EmptyMap` —
  total constructors for the no-argument cases that previously needed a
  panicking `Must*` twin.
- `convert.RegisterImporterAtInit` / `RegisterExporterAtInit`, replacing the
  `MustRegister*` pair: a converter package's `init()` has no caller to return
  an error to, so a failed self-registration is recorded against the format and
  returned by `Import`/`Export` at first use instead of panicking at load time.

### Changed

- **The conformance target is corrected, and it changes what the project
  claims.** Earlier releases cited "§2.1.2" for Process Execution Conformance and
  derived the element set from the Common Executable Subclass. Neither clause
  exists as cited — Process Execution Conformance is **§2.3** — and Common
  Executable is a sub-class of Process *Modeling* Conformance, for tools that
  emit executable models, mandating XML Schema, WSDL and XPath. The basis is now
  §13's operational semantics, split by tier: §2.3.1 is the library's, §2.3.2
  (import) the server's. Two consequences reach the API surface indirectly:
  `ComplexGateway` was never an "extension" (§13.4.5 always required it), and
  `Lane`/`LaneSet` became a real element rather than a scope question.
- **Two role declarations that used to register are now refused**, both
  scoped to the authorizing kinds (`HumanPerformer`, `PotentialOwner`) — a bare
  `ResourceRole` or a `Performer` is unaffected, since it grants nothing either
  way. A role naming its people through `resourceRef` (a query into an
  organizational directory gobpm does not have) fails at **registration**; a
  role with neither a `resourceRef` nor an assignment expression fails at
  **construction**. Neither ever authorized anyone; they were accepted and
  silently ignored, which is the defect this release removes. Both are recorded
  in `SAD-001` §14.1, with the identity subsystem named as what would close the
  first.
- **Declaring a human role closes a previously open task.** A `UserTask` with a
  role and no triad member moves from "any actor authorized" to "role members
  only" — the declaration doing what it says.
- `ResourceAssignmentExpression.Expression` is now a `data.FormalExpression`
  rather than a `data.Expression`. The latter is the natural-language variant
  the standard defines as "not executable", so the field could never reach an
  evaluator. The field had no reader anywhere, so no working code depended on
  it.
- **No library code calls a panicking `Must*` constructor.** The repository's
  guard already banned it but carried documented carve-outs for "provably
  infallible" cases; the carve-outs are gone rather than re-justified, and the
  ban is now absolute. `Must*` twins remain — they exist to simplify tests and
  examples.

### Fixed

- **A `UserTask` rejected `activities.WithRoles`.** Its constructor dispatched
  on four option families and the role family was in none of them, so a
  declared `HumanPerformer` or `PotentialOwner` could not reach the one task
  type whose eligibility it decides.
- **The vendored BPMN extract repeated a specification erratum.** §10.3.4.1
  cites Table 8.49 for the Activity instance attributes a UserTask inherits,
  but 8.49 is *"Resource attributes and model associations"*. The correct table
  is 10.4, it is now extracted, and it has exactly one row — `state`.

- **Every example now asserts the outcome it demonstrates.** All 46 example
  modules previously failed only on a returned error, so one that completed
  down the wrong branch, computed a wrong value or skipped an activity still
  exited 0 and passed the CI run-step. Each now compares what it claims to
  show — the branch taken, the ordering, the value read back, the version
  resolved — and fails when it differs, turning the existing exit-0 gate into
  an outcome gate with no new CI machinery.
- **`timer-event` faulted on every run.** Its ServiceTask was built on an
  operation with no implementation, so the instance failed and terminated; the
  example blocked on the context deadline and printed "Process completed"
  regardless. It now has a real operation and requires a completed instance.
- **`simple-timer` demonstrated behaviour the engine does not have.** It
  claimed a timer Start Event instantiates a process on schedule; no instance
  was ever created, because a timer start is deliberately not an instantiating
  trigger and scheduled instantiation is unimplemented. It now starts the
  instance explicitly and asserts the timer held the token for its full delay.

## [v0.10.0] - 2026-07-30

**The BPMN Common Executable Subclass is complete.** Ad-Hoc Sub-Process was the last
executable element in scope, so every element gobpm set out to support now executes —
see [`conformance-status.md`](docs/design/conformance-status.md), the per-element
tracker. This release also closes the durability gap that made long-running processes
theoretical, and adds the human-task half that made them usable.

Highlights, for deciding whether to upgrade:

- **Full element conformance.** Ad-Hoc Sub-Process (Router-driven succession over a
  flow-less inner set), Transaction + Cancel, Event Sub-Processes (interrupting and
  non-interrupting), Compensation, Escalation, Link, Multi-Instance (sequential,
  parallel, `behavior`), Script and Business Rule tasks, Data Objects and Data Stores,
  Inclusive and Complex gateways.
- **Persistence & state.** Checkpoints and restart recovery, instance dehydration with
  wake-on-trigger, durable timers, ownership leases for cluster-safe fencing. Ten
  thousand orders waiting three days now cost ten thousand rows, not ten thousand
  running processes.
- **Human-task ownership.** `Claim` / `Unclaim` / `Reassign` over BPMN's `actualOwner`
  (§10.3.4.1, Table 10.14), strict owner-only completion, and a performer record later
  tasks can route on. A task offered to twenty candidates can no longer be worked by all
  twenty.
- **Process interchange.** Import and export BPMN 2.0 XML, so a `.bpmn` authored in a
  modeller finally has a way in.
- **Expressions and scripting.** A language-routed expression layer hosting several
  engines side by side, plus an embedded Lua script engine.

**Breaking change.** Completing a User Task now requires claiming it first — an
unclaimed task is completable by nobody. Processes that assign a task to exactly one
person are unaffected: such a task is born owned. See the Added section below.

**Not in this release**, and the reason this is 0.10 rather than 1.0: the standalone
`gobpm-server` runtime (HTTP/gRPC, postgres, otel, an AuthN provider) is milestone M5 and
has not started. gobpm remains an embedded library.

### Added

- **Human-task ownership — claim, unclaim, reassign (ADR-020 v.2, SRD-073).**
  A UserTask could be offered to twenty candidates and worked by all twenty:
  whoever submitted first won and the other nineteen discarded their effort,
  with nothing to signal "I am doing this". BPMN already names the missing
  piece — §10.3.4.1 Table 10.14 defines `actualOwner`, the user who
  "picked/claimed" the task — so this is conformance work rather than
  invention, and it is the first *instance* attribute the engine implements.
  `Thresher` grows `Claim` / `Unclaim` / `Reassign`; completion becomes
  **strict**, so only the holder may complete and an unclaimed task is
  completable by nobody. `Claim` is checked (it refuses a task another actor
  holds, and is an idempotent no-op for the holder, so claim-before-complete is
  retry-safe); `Reassign` is deliberately **unguarded at the task level** —
  its callers are managers and administrators, never participants — so the
  embedder authorizes it and should log who invoked it, while the nominee is
  still checked against the process's own triad. A task assigned to exactly one
  person is born owned, needing no ceremonial self-claim. Ownership operations
  are registry mutations: they never advance a token, never resist
  cancellation, and never wake a dehydrated instance, so a claim during a
  three-day wait costs nothing.

  Completion records **who actually performed the work** in the read-only
  `RUNTIME/COMPLETED_BY` map (node → user), carried across a hydrate on the
  instance checkpoint, so a later task can route on it: "send it to the
  approver's manager" becomes a process decision instead of glue code. It is
  engine-written and cannot be forged or overwritten by the process.

  The eligibility triad is now resolved **once, when the task is announced**,
  and frozen for the task's life — an owner's right to finish work they hold
  must not be revocable by an unrelated data change. Breaking change: an
  embedder that completed without claiming must now claim first.

- **Process interchange — import and export BPMN 2.0 XML (ADR-024, SRD-051).**
  gobpm could only ever be handed a definition built in Go, which shut out the
  modeler persona entirely: a `.bpmn` authored in bpmn.io or Camunda had no way
  in. Two new packages close that. `pkg/convert` is a format-agnostic seam —
  the `Importer`/`Exporter` interfaces over `io.Reader`/`io.Writer` plus a
  register-by-format-key registry in the `image.RegisterFormat` idiom — and
  `pkg/convert/bpmn` is the batteries-included BPMN 2.0 implementation, turned
  on by a blank import. `convert.Import` returns a `*process.Process` you
  register yourself; the engine never imports a converter, so a host that wants
  no XML gets none. Imported BPMN `id`s become the model's identity rather than
  being regenerated, which is what makes a re-imported file land as the next
  *version* of the same definition instead of a fresh singleton (ADR-019). The
  supported set is the executable core — start/end events, task, manualTask,
  userTask, serviceTask with `operationRef`, sequence flows with conditions,
  exclusive and parallel gateways. Diagram interchange is skipped, as are
  `documentation` and `extensionElements`; an unmapped *flow* element raises a
  `*convert.UnsupportedElementError` naming the tag, id and spec section, so a
  modeler is told what will not run rather than losing it silently. Round-trip
  is semantic, not byte-identical. See
  [the guide](docs/guides/extending/converters.md) and
  `examples/bpmn-convert/`.
- **`activities.ServiceTask.Operation()`** — a read-only accessor for the
  operation a Service Task was built with. Needed by BPMN export to write
  `operationRef` back out; additive, and never nil since `NewServiceTask`
  rejects a nil operation.

- **Ad-Hoc Sub-Process — execution order decided at runtime (ADR-035,
  SRD-074; closes #92).** An embedded Sub-Process marked
  `WithAdHoc(router)` holds activities with **no sequence flows** between
  them: what runs next is answered by a host-supplied **Router**, which
  replaces sequence-flow succession inside the container. It is consulted
  when the scope opens — its first answer is the standard's *initially
  enabled* set — and again after each inner activity settles, seeing what
  has completed, what is running, and the case's own data through a
  transient read frame. An empty answer ends the asking track; the
  container completes when its scope **drains**, so completion is
  inherited from the existing scope machinery rather than built anew, and
  a container joins a fork without a join gateway by answering empty while
  a sibling still runs.

  `parallel` ordering is the default (the metamodel declares none and
  Camunda 7 does not implement the element) with `AdHocSequential`
  available; `WithAdHocManualSelection()` **offers** the Router's answer
  instead of running it, so a host picks through
  `InstanceHandle.AdHoc(nodeID)` — `Enabled` / `Running` / `Activate`,
  where activating an unoffered activity is a classified error rather
  than a silent no-op. `WithAdHocCompletion(expr)` keeps the standard's
  `completionCondition` as a decorator over the Router, and is the one
  trigger `cancelRemainingInstances` hangs off, per §13.3.5.

  Batteries ship in `pkg/adhoc/routers` — `Standard()` (each activity
  once, the conformance shape), `Expression(expr)` (successors named by a
  BPMN expression, routed through the language-routed engine) and
  `Sequence(ids…)` — but **no Router is applied by default**, and never by
  declaration order: a container missing its Router is rejected at
  registration instead of running in a silently arbitrary order. Inner
  containment is validated to leaf Tasks and plain embedded
  Sub-Processes. Routing decisions ride a new `KindAdHoc` fact kind
  (`Offered` / `Activated` / terminal, carrying `candidates`,
  `selected_by` and `stop_reason`), so a case's routing is
  reconstructible from the stream alone.

- **Typed value extraction — `data.As[T]` (ADR-034 Data-Layer Generics
  Policy, SRD-072).** The canonical typed idiom for reading a payload out
  of a bare `data.Value`: `data.As[int](ctx, v)` replaces the
  discard-prone hand assertion `v.Get(ctx).(int)`. A nil `Value` and a
  type mismatch return classified, self-identifying errors naming both
  the held and the requested type (interface types included), instead of
  a silent zero value. ADR-034 also records why the `Value` interface
  family stays dynamic and confines generics to the edges (generic
  constructors, `T`-suffix accessors, registration-time adapters).

- **Instance dehydration & wake-on-trigger — a long timer wait now costs
  ZERO goroutines (SRD-071, finalizing ADR-007; closes the timer-durability
  gap).** When every live track of a checkpointed instance is parked on a
  wait that is both *dehydratable* and *held* by an engine-level holder,
  the instance **dehydrates**: it takes a final consistent-cut checkpoint,
  releases **all** its goroutines — the loop and every parked track — and
  leaves, its checkpoint becoming the wake source. A new engine-level
  **timer service** holds the absolute deadline on the released instance's
  behalf (one goroutine for the whole engine, replacing the per-waiter
  goroutine for released timers) and, at the deadline, rebuilds the
  instance and **continues** the woken wait: a continuation fork re-enters
  the wait node with the trigger already in hand and fires *through* it to
  the outgoing flow — never re-arming. The sharpest invariant:
  **trigger-present continues, trigger-absent re-arms**, so wake-on-trigger
  and cold restart recovery share one `Restore` path and differ only by
  whether a trigger accompanies the hydration. An instance oscillates
  freely (park → release → wake → continue → release) with the recorded
  track lineage bounded across cycles, and concurrent triggers hydrate it
  exactly once. Two `KindInstanceState` facts make it observable at Info:
  **`Dehydrated`** (the wait kinds it parked on, how many, and
  `goroutines=0`) and **`Hydrated`** (the waking trigger, the woken wait,
  and whether the wake continued the flow or completed the instance).
  Residency is visible through the public API too: `StateDehydrated` names
  the non-terminal state, `WaitCompletion` keeps blocking across
  dehydration cycles rather than mistaking a release for completion, and a
  handle taken before a release follows the instance through its rebuild. Eligibility is a capability the element
  declares (`Dehydratable`), so it stays data-driven and rolls out
  element-by-element: today a **one-shot timer more than an hour out**
  releases (a shorter or repeating one keeps its in-memory waiter), while
  a **message or signal catch** releases unconditionally (a receive is a
  pure wait — the engine takes over its subscription, **keyed to the
  instance's conversation**, so a foreign conversation is filtered exactly
  as it is for a resident instance and never wakes it, while a correlated
  one binds its payload and records the keys it derives), and an
  **Event-Based Gateway** releases when EVERY arm it races is holdable —
  it is one wait node owning a SET of holds, and the winning arm's trigger
  withdraws the losers exactly as a resident gateway does, and a **human
  task** releases too — the task keeps living in the distributor's inbox,
  so a `Take`/`Complete` hydrates the instance and proceeds normally,
  under **the task id the human is holding** (a rehydrated task is no
  longer re-issued under a fresh id — this also makes a task reference
  survive a restart) and with the instance pinned resident for the
  duration of the action, so a caller never observes dehydration. An
  external-worker task never releases (a job in flight is active work) and
  a conditional catch never does either (its trigger is the instance's own
  data — nothing external could wake it). No wait is ever released without
  something that can wake it, and a trigger racing the release is retried
  against the checkpoint rather than dropped — a trigger is never lost. The
  zero-config engine is untouched: without `WithRepository` nothing
  dehydrates. See `examples/dehydration/` — six long waits, one per holder
  kind, each releasing every goroutine the instance owns and coming back on
  its trigger; the near-deadline timer stays resident on purpose, showing
  the threshold from both sides.

  **An armed boundary event is durable state too.** "Approve within 24
  hours or escalate" is the canonical long wait, and a boundary is not a
  track — so it was invisible to the release decision and absent from the
  checkpoint: a released instance lost the escalation outright, and a
  recovered one silently restarted its clock by re-evaluating the
  definition. Now an armed boundary takes a holder of its own (the
  instance releases only when every boundary guarding it is held, the same
  per-arm rule an Event-Based Gateway applies to its arms), its resolved
  deadline rides the checkpoint (Schema 2 — additive, and an older
  document still reads), and a restore re-arms it at the RECORDED
  deadline. A deadline already passed does not wait again: the token forks
  at the boundary with the guarded track as its parent, interrupting
  cancelling it, exactly as a resident boundary fires. One caveat worth
  knowing: **pin the boundary's own id** (`foundation.WithID`) — unlike a
  missing node id it does not refuse loudly, it just loses the recorded
  deadline across engines.

  Two smaller durability defects went with it: a wait's engine-level holds
  now end with the wait on **every** exit path (a track cancelled by an
  interrupting boundary kept its deadline or subscription, which could
  wake a later cycle for a wait that no longer existed), and a track can
  hold **more than one deadline** — an Event-Based Gateway racing two
  timers previously kept only the second, so if the lost one was the
  earlier deadline the gateway fired late.

  Two durability gaps closed along the way: an **event-born instance**
  (one started by a message or signal) was never checkpointed at all —
  every launch path now shares one set of engine options — and a wake that
  raced the instance's own final checkpoint could lose its trigger.

- **Instance checkpoints, save/restore and restart recovery (SRD-070 —
  the first ADR-033 Persistence & State slice).** With an explicitly
  configured repository (`thresher.WithRepository`), every instance
  writes a **consistent-cut checkpoint** at its observable lifecycle
  transitions (activation, node completion, wait parks, scope opens,
  the terminal — the loop is the single writer, so every cut is
  consistent by construction). The Schema-1 document pins the
  **registered process version**, records the scope tree's data
  (canonical tagged-JSON codec over the value model; uncodable
  payloads fail loud), conversation keys, the compensation ledger and
  the live tracks with their **timer deadline descriptors**. The
  Repository contract grows CAS record versions and **per-instance
  ownership leases** (`WithLeaseTTL`) — the ADR-033 §2.8 cluster-safe
  fencing: a zombie engine's saves are rejected, never overwriting the
  new owner. **Restart recovery** in `Run` claims expired-lease
  records and restores with **re-enter-the-node semantics**
  (subscriptions re-register, tasks re-announce, jobs re-enqueue —
  at-least-once effects); a restored timer re-arms at the RECORDED
  absolute deadline (a Duration never restarts) and an **overdue
  timer fires once, immediately**. In-flight Call/MI/compensation
  constructs defer the checkpoint loudly (`CheckpointDeferred` at
  Warn) instead of writing torn documents — full fidelity rides the
  next slices. The zero-config engine stays exactly as before:
  volatile, zero overhead. See `examples/restart-recovery`.

- **Developer manual (`docs/guides/`) — a comprehensive, `go doc`-grounded
  reference for embedding gobpm.** The counterpart to `docs/design/` (which
  records how/why the engine was built), organized in seven parts:
  getting-started, architecture & runtime (the `BaseElement → Thresher` entity
  stack, process execution, event processing, scope), the value & data model,
  the element reference (taxonomy + a page per element — constructor, option
  families, the interfaces you implement, methods & behavior), controlling
  processes & instances, **extending gobpm** (a page per seam: custom ID
  generator, Value type, Operation, expression/rule/script engine, data store,
  repository, message broker, clock, observability, worker dispatcher, task
  distributor, authorization), and reference. Deep-but-readable, pure
  generator-agnostic Markdown; every page grounded in the public API and a
  runnable example. `docs/guides/CONTRIBUTING.md` records the authoring standard.

- **Rules/Script invocation facts and the registrar audit (SRD-069).**
  The decision/script observability stream becomes a closed ledger:
  the new **`Invoked`** phase opens every Business Rule / Script Task
  engine call (engine-call latency derivable, a hung engine
  attributable), and every opening now **closes** — commit-stage
  failures, previously silent on the stream, emit `Failed` with the
  new `stage` attr (`engine` | `commit`). The **registrar surfaces**
  gain their audit through the optional `rules.ReporterBinder`
  capability (bound automatically by `thresher.New`): `gorules` and
  `adapters/dtable` registration emit `Registered` (names and counts
  only), and a runtime `dtable` **`Deploy` emits `Deployed` at Info**
  — the decision-governance milestone, flagged `replaced` when it
  overwrote a live table. Unbound engines stay silent; engine
  internals deliberately stay off the stream (Tracer territory).

- **The `gobpm:lite` text-expression evaluator (SRD-067; completes
  ADR-032 and closes #74 together with the routing half).** The
  stdlib-only battery behind the expression seam: `##Lite` claims
  `gobpm:lite` in the **zero-config registry beside `goexpr`** — out of
  the box a model mixes functor and text expressions freely. The
  language: float64-unified numbers (an int datum compares with `100`
  naturally), strings, booleans, `nil`, and **times** with a `time()`
  RFC3339 builtin; **structural paths** (`order.customer.tier`,
  `items[0]`, `rates["EUR"]`) ride the engine's own resolver; cross-kind
  comparisons and missing data **fail loud** (`has()` is the explicit
  existence probe; `len()` counts elements, keys or runes);
  `and`/`or` short-circuit. The engine **enforces a declared result
  type** on the produced value, and `lite.Cond(body)` mints a condition
  with the `bool` declaration the condition paths require
  (`lite.Expr(body)` for plain text expressions). See
  `examples/expression-routing/` — one process mixing lite and goexpr
  across task flows, an XOR gateway and a UserTask whose **assignee is
  computed by a lite string expression**.

- **Language-routed expression engines and the text expression kind
  (SRD-066, ADR-032 v.1 — the routing half; part of #74).** The expression
  seam becomes **multi-engine**: `expression.Engine` widens with the
  `##`-kind and **enumerable `Languages()` claims**, and the new
  `expression.Registry` folds registered engines into a language→engine
  routing map at construction — the Registry is itself an `Engine`, so
  every runtime consumer (conditions, timers, multi-instance, correlation,
  the worker dispatcher's expression binder) is untouched however many
  engines are wired. `WithExpressionEngine` is **repeatable** (several
  evaluators coexist, routed by each expression's language URI); a
  duplicate language claim **fails `thresher.New` loud naming both
  kinds**; an unclaimed language errors listing the registered claims;
  `WithoutDefaultExpressionEngines()` opts out of the batteries for a
  fully explicit runtime (`##None`). The startup config prints the
  expression routing table. New `data.NewTextExpression(language, body)`
  carries **source-text expressions** (the standard's textual
  FormalExpression) for routed engines to interpret via the
  `data.BodyHolder` capability — with `WithResultType` declaring the
  result type (conditions require `"bool"`); the functor kind
  (`gobpm:goexpr`) keeps working unchanged as the routed default.

- **The Lua Script Engine — `adapters/lua` (SRD-065; completes ADR-031 and
  closes #87 together with the seam and the BRT landings).** The batteries
  interpreter behind the Script Engine seam, over pure-Go `gopher-lua`
  (no cgo — static builds intact): `##Lua` claiming `text/x-lua` /
  `application/x-lua` / `lua`. Every execution runs on a **fresh,
  context-bound, sandboxed `LState`** (base/table/string/math only, the
  load family removed, `io`/`os` never loaded; cancellation/deadline
  aborts a hung script). Scripts read process data **lazily** through the
  read-only `data` table — an absent datum **raises naming it** (the
  fail-loud house rule over Lua's nil idiom; `has(name)` probes optional
  data) — and produce outputs by **returning a table** of named values
  (numbers land as `float64`, Lua's single number type). Proven e2e with
  a real Lua script routed **beside a second engine** in one gobpm engine;
  see `examples/script-task/` — an embedded, recompile-free `order.lua`
  classifying three order profiles.

- **The Script Engine seam (multi-engine) and the Script Task (SRD-064,
  ADR-031 v.1 — the seam half; #87).** The last silent conformance task
  type gains execution on a **pluggable, multi-engine Script Engine seam**:
  `pkg/script` defines the engine contract (`##`-kind, **enumerable
  `Formats()` claims**, `Execute(format, script, DataReader)` → named
  outputs) and the core **Registry router** — registered engines fold into
  a format→engine map at construction, a duplicate claim **fails
  `thresher.New` loud naming both kinds**, and the startup config prints
  the routing table. `WithScriptEngine` is **repeatable** — several
  interpreters coexist in one gobpm engine, routed by the standard's own
  `scriptFormat` MIME hint; an unclaimed format errors listing the
  registered claims, and the zero-config `##None` default tells the
  operator exactly what to wire. The rebuilt `ScriptTask`
  (`NewScriptTask(name, format, script)`) runs its body on the routed
  engine and commits the script's **named outputs per-name** (sorted,
  deterministic) — a script sets variables, no result fold. New `Script`
  observability facts (`Executed`/`Failed`) carry the format, the routed
  engine's kind and the output count. The batteries **Lua interpreter
  (`adapters/lua`), the example and the conformance row-5 flip ride
  SRD-065**.
- **The engine-global Data Store + Data Store Reference (SRD-068, ADR-030
  v.1 — the `DataStore` half of #82).** BPMN §10.4.1 data that **outlives a
  Process instance** and is **shared across instances**: modeled as an
  **engine-level infrastructure port** (not a per-instance element), like
  `Repository`/`MessageBroker` (SAD-001 G4). A new `pkg/datastore` defines the
  `DataStore` interface (`Get`/`Put` by name + `Capacity`/`IsUnlimited`) and a
  **`Registry`** resolving a store by its `dataStoreRef` — each store with its
  own capacity/backing, so a Process may reference many (registration is
  **fail-loud**: an unknown ref is a configuration error, not a silent
  auto-provision). The default in-memory adapters live in
  `pkg/datastore/memstore`. Register a store with
  `thresher.WithDataStore("orders", memstore.New())`; the runtime reaches it via
  `renv.EngineRuntime.DataStores()` (never nil). A **`DataStoreReference`**
  (`pkg/model/data_stores`) is the flow-scope handle: it participates in
  `DataAssociation`s exactly like a `DataObject` (add it to a `Process`/
  `SubProcess`, `AssociateSource`/`AssociateTarget`), but its I/O routes to the
  engine-global store — the association carries the `dataStoreRef`, on which the
  task reroute branches (store vs per-instance scope). `capacity` is advisory in
  the in-memory adapter; durability is a swappable adapter (the future
  Persistence & State workstream). Every store access emits a
  **`KindDataStore`** observability fact (`PhaseRead`/`PhaseWritten`, carrying
  the store ref and key), echoed to the operator log at Debug — a shared-store
  access is operationally significant. See `examples/data-store/` — a writer
  instance stores a value a *separate* reader instance reads back.

- **Data Objects as scope-resident named containers (SRD-063, ADR-030
  v.1 — the `DataObject` half of #82).** A BPMN `DataObject` (§10.4.1)
  becomes a **per-instance scope variable** instead of an
  association-wired object. It can be registered on a **`Process`** or an
  embedded **`SubProcess`** (`proc.Add(do)` / `sub.Add(do)`, name-keyed,
  duplicate-name guarded), is **seeded into the matching scope** at start
  (a Process-level object into the root scope, a SubProcess-level object
  into the child scope when it opens, disposed when it closes), and is
  **resolved by name** through the walk-up — the same substrate a
  `Property` already uses. Its **DataAssociations flow through scope in
  both directions**: a task's DataOutputAssociation writes the produced
  value into the per-instance DataObject (Node → DataObject), a
  DataInputAssociation fills a task input from it (DataObject → Node); the
  association is a shared declaration and the value lives per-instance, so
  concurrent instances stay isolated with no association retargeting.
  Read a DataObject from outside the engine by name via the instance
  handle's data reader. `examples/process-data/` now registers its result
  Data Objects and reads them back by name. `DataObjectReference` stays a
  deliberate non-implementation (SAD-001 §14.1, with the BPMN-translation
  rules recorded there); the `DataStore` port rides a follow-up SRD.

- **The Decision Table rule engine — `adapters/dtable` (SRD-062, ADR-029
  v.1; the first shipped adapter module).** A pluggable out-of-core
  `rules.Engine` evaluating DMN-shaped decision tables with **Go functors
  as the rule expressions**: the declarative condition vocabulary
  (`Eq/NE/GT/GE/LT/LE/Between/In/Any/Pred` — type mismatches fail loud,
  never a silent false), the `Rule` behavior contract (match + yield) under
  a data-declared `Table`, and **all five DMN hit policies** (Unique
  contradiction and Any disagreement are classified errors; First
  short-circuits; no match = an empty result the task commits nothing on).
  **Missing input fails loud by default** — a deliberate deviation from
  DMN's null-tolerant fall-through — with per-condition `IfPresent` opting
  into the DMN no-match semantics. The engine also implements
  **`rules.Deployer`** through a pluggable **Decoder seam**
  (`WithDecoder`): the batteries `JSONDecoder` deploys **structure-only
  artifacts** (grid, policy, names) over a `Vocabulary` of named Go
  functors — behavior stays compiled Go, unresolved names fail deploy, a
  redeploy **replaces** the decision while programmatic `Register` keeps
  rejecting duplicates. Proven e2e through the Business Rule Task
  (`##DTable` in the `Rules` facts); see `examples/decision-table/` — a
  deployed FIRST-policy discount grid classifying three order profiles.

- **Business Rule Task on the pluggable rule-engine seam (SRD-060, ADR-027
  v.1 — the BRT half of #87).** The last model-only conformance task type
  gains execution. A new engine service — the **Business Rule Engine** —
  in the ADR-002 shape: the one-method `rules.Engine` seam (evaluate a
  **decision reference** against the read-only data surface → the decision
  **result rows**, the DMN-universal list-of-records shape) with the
  **batteries-included `gorules` registry** (`##GoRules`): named Go decision
  functions, bounded by construction, unknown references fail loud. Wired
  through the five-point pattern (`thresher.WithRuleEngine`,
  `renv.EngineRuntime.RuleEngine()`, the startup-config line); any DMN or
  external rules service swaps in wholesale without touching the model. The
  task itself (`activities.NewBusinessRuleTask(name, decisionRef)`) realizes
  the standard's §13.3.3 clause — call on activation, complete on return —
  committing the result with the **fold**: a 1-row/1-output result becomes a
  scalar named by its output (decision-driven gateway conditions with zero
  ceremony); anything else an array of row-maps under the decision
  reference. Failures ride the ordinary fault machinery (Error boundaries
  catch decision `BpmnError`s). New **`Rules` observability facts**
  (`Evaluated`/`Failed`) carry the decision reference, the engine kind, and
  the result shape — and node-emitted facts now reach the instance's handle
  observers (the `execEnv` Reporter override). The `rules.Deployer`
  capability (the deploy half of the minimal DMN component API) is declared;
  the Decision Table model rides a follow-up SRD.
  See `examples/business-rule-task/`.

- **Transaction Sub-Process (SRD-061, ADR-028 — #91).** A Sub-Process variant
  (`WithTransaction()`) that aborts atomically on a **Cancel End Event** — the
  ACID-style all-or-nothing unit. Reaching a Cancel End inside a Transaction
  aborts it in a fixed, load-bearing order (ADR-028 §2.3): **compensate** the
  completed activities (the ADR-026 scope-wide sweep, reverse completion order,
  as an ACID-like barrier), **terminate** the ones still running, then **leave**
  through the Transaction's interrupting **Cancel boundary** — a Transaction
  with no Cancel boundary ends there (Camunda-aligned). The order is enforced by
  the ledger-survival rule: the teardown discards the very completion ledger the
  sweep consumes, so the sweep runs first. Cancel is a **direct-resolution
  event**: the loop resolves it loop-locally, never through the EventHub
  (mirroring the scoped Terminate). A Cancel End Event and a Cancel boundary are
  legal **only** on a Transaction (validated at registration); a nested
  Transaction is rejected. New `Cancel()` on `renv.RuntimeEnvironment`,
  `WithTransaction()` / `IsTransaction()`, the Cancel boundary (always
  interrupting — un-defers ADR-018 §2.7), and `examples/transaction-sub-process/`
  (a booking saga that cancels). Observability reuses `KindScope`/`Canceled` +
  the `KindCompensation` sweep facts (no new phase). Deep (recursive) scope
  compensation, `store`/`image`, and Ad-Hoc stay out of scope per ADR-028.

- **Compensation events (SRD-059, ADR-026 v.1 — #90, closing the epic).**
  Undoing work that already **completed successfully** — the saga pattern in
  BPMN form. Each open scope keeps a **completion ledger**: compensable
  completions (activities guarded by a **Compensation boundary** linked to an
  `isForCompensation` handler, or a Sub-Process with a compensation **Event
  Sub-Process**) enter it in completion order with a **data snapshot** captured
  at that instant; child ledgers fold into the parent at scope completion and
  discard when the enclosing scope finishes (never a live subscription —
  ADR-006 §2.3's eligibility window made structural). A throw Compensation
  Event (Intermediate Throw / End) resolves **directly** against the ledger:
  `activityRef`-targeted or scope-wide in **reverse completion order**,
  handlers run sequentially; `waitForCompletion` (default) parks the throwing
  token until the sweep drains. A handler reads the **snapshot** its activity
  completed with (writes go live); a handler failure faults through the Error
  chain (`Compensating → Failed`); an unresolved throw is **logged**, never
  silent, never a fault. New `KindCompensation` observability (Thrown/Eligible/
  Folded/Compensating/Compensated/Discarded/Unresolved — the ledger's audit
  log), `Compensate(ref, wait)` on `renv.RuntimeEnvironment`, the
  `NewCompensationBoundaryEvent` constructor with its typed handler link, and
  `examples/compensation-events/` (a trip-booking saga). Recursive default
  compensation, the error-driven auto-sweep, `compensate-on-terminate`,
  Transaction/Cancel and Call-Activity compensation stay designed-for/out of
  scope per ADR-026. **Closes the #90 events epic** — Signal, Link, Escalation
  and Compensation all landed.

- **Escalation events (SRD-058, ADR-006 v.4 §2.2/§2.6 · ADR-018 · ADR-023 v.2 §2.6 — #90).**
  Error's **non-critical** twin: a throw (Escalation **Intermediate Throw** or
  **End Event**) raises a non-fault escalation that climbs the throwing
  execution's **scope chain** to the innermost matching catcher — an **Escalation
  boundary** (interrupting *or* non-interrupting) or an **event-sub-process
  Escalation start** (the inline handler wins over the boundary, §10.5.6) —
  matched by escalation **code** (empty code = catch-all). Unlike an Error, the
  throw **continues** its token (or ends normally) and never faults; an
  **unresolved** escalation (no reachable catcher) is **logged** (a
  `KindEscalation`/`Unresolved` fact at Warn) and execution continues — never
  silently dropped. Reuses the landed Error propagation machinery
  (`matchErrorScopeChain`) with three deltas: non-critical throw,
  logged-not-faulted miss, non-interrupting-capable catch. Adds `Escalate(code)`
  to `renv.RuntimeEnvironment` (the peer of `Terminate`), a new `KindEscalation`
  observability kind (Thrown/Caught/Unresolved), and `examples/escalation-events/`.
  Fixes the `NewEscalationEventDefintion` typo → `NewEscalationEventDefinition`
  (**breaking**, on a pre-1.0 constructor) and adds a `MustEscalationEventDefinition`
  twin. Only **Compensation** now remains of epic #90's four events.

- **Link events (SRD-057, ADR-006 v.4 §2.8 — #90).** An intra-process GOTO: a
  source Intermediate **Throw** hands the token to the same-name target
  Intermediate **Catch** within one Process level. It is **not** a wait node —
  the throw **redirects** (no broadcast, no correlation, no subscription). Pairing
  is by name, resolved once at graph-wiring time (`WireClonedGraph`, so nested
  Sub-Process links resolve for free, per instance) and validated **fail-fast at
  registration** (exactly one target and ≥1 source per name; boundary/start/end
  reject Link). New `examples/link-events/` (an on-page loop). `LinkEventDefinition`
  reshaped from an unwired stub to the event-definition skeleton. Retires the
  deferred `SubscriptionKey()` generalization (Link is a static redirect, not a
  name-matched subscription).

- **Standard Loop (SRD-054, ADR-025 — #88).** An activity marked `WithLoop`
  with `StandardLoopCharacteristics` (`loopCondition`, `testBefore`,
  `loopMaximum`) re-runs while its condition holds — a leaf Task **in place**, a
  composite (Sub-Process / Call Activity) by **re-opening its child scope** per
  iteration. A 0-based `loopCounter` is published each pass to the condition and
  the activity, and iteration scope facts carry it. An Event Sub-Process rejects
  any loop/multi-instance marker (it is instantiated by its trigger, not
  iterated). New `examples/standard-loop/` and `docs/guides/iteration.md`.
  `LoopCharacteristics` changed from a struct to a sealed interface (breaking, on
  a previously unused stub).
- **Multi-Instance — sequential (SRD-055, ADR-025 — #88).** An activity marked
  `WithLoop` with `MultiInstanceLoopCharacteristics` runs a fixed number of
  times, decided once at activation from an integer `loopCardinality` **or** the
  size of the input collection (`loopDataInputRef`). Each instance sees its
  collection element bound by name (`inputDataItem`) and publishes the
  `loopCounter` / `numberOfInstances` / `numberOfActiveInstances` /
  `numberOfCompletedInstances` runtime attributes; a `completionCondition` stops
  launching the remaining instances early, and per-instance outputs
  (`outputDataItem`) are assembled — in order — into an output collection
  (`loopDataOutputRef`) published once at completion (the visibility barrier).
  New `examples/multi-instance-sequential/`.
- **Multi-Instance — parallel (SRD-056.A, ADR-025 — #88).** A Multi-Instance
  *without* `WithSequential` runs all N instances **concurrently, each in a
  distinct scope**, completing when the last drains. Each instance binds its
  input item in its own scope and assembles its output positionally (slot =
  ordinal), so the output collection is deterministic despite nondeterministic
  completion order. The `completionCondition` now performs a real **cancellation**
  of the still-running instances (counted `numberOfTerminatedInstances`);
  `numberOfActiveInstances` reflects the running count. An interrupting boundary
  (or scoped Terminate) on a parallel Multi-Instance tears down all N instances.
  Behavior events (`ComplexBehaviorDefinition`) follow (SRD-056.B). New
  `examples/multi-instance-parallel/`.
- **Multi-Instance — `behavior` (SRD-056.B, ADR-025 — #88, completing the MI
  feature).** A Multi-Instance can throw a **boundary-catchable** event as its
  instances complete (`WithBehavior`): `All` (default, no throw), `None` (every
  completion), `One` (the first), or `Complex` (`WithComplexBehavior` — each
  `NewComplexBehaviorDefinition(condition, ImplicitThrowEvent)` throws when its
  boolean condition holds; one completion may throw several). The events carry the
  §2.9 runtime attributes and are caught by a boundary on the Multi-Instance
  activity — interrupting or **non-interrupting** (a progress notification, e.g.
  *quorum-reached*). New `events.ImplicitThrowEvent` and
  `activities.MultiInstanceBehavior` / `ComplexBehaviorDefinition`; new
  `examples/multi-instance-behavior/`. Only `completionQuantity` remains deferred
  of the Multi-Instance surface.

### Changed

- **Composite loop execution → off-loop iteration decorator (ADR-025 v.2 §2.12,
  SRD-054 / SRD-055 / SRD-056.A).** Internal rework, behavior-preserving: a looped
  **composite** activity (Sub-Process / Call Activity) now drives its own
  iteration from the host's runner goroutine — requesting scope open/close from
  the single-writer loop via a request/response protocol — instead of
  loop-goroutine-driven scope re-entry. Semantics are unchanged; the rework makes
  the Multi-Instance behavior throw (§2.8) implementable with a deterministic
  boundary catch (the loop-goroutine model self-deadlocked on it). **Standard
  Loop**, **sequential Multi-Instance**, and **parallel Multi-Instance** are all
  re-landed on the decorator: sequential/parallel keep only their output capture
  loop-side (before each child scope closes), and parallel drives its N-of-N
  barrier through a per-drain re-arm handshake (the loop delivers the concurrent
  drains one at a time onto the runner's cap-1 park). The old
  `compositeIterator` / loop-side `parallelInstanceDrained` seams are removed.

- **No `Must*` constructors in library runtime paths (FIX-026).** Every
  fallible `Must*` call site in `pkg/`/`internal/` non-test code now uses the
  error-returning `New*` constructors with classified wraps — a bad runtime
  input (an operation result, a broker payload, a mapped output name, a
  caller option) fails the operation through the ordinary fault machinery
  instead of panicking the engine. Signature changes for embedders:
  `bpmncommon.Message.Clone()` → `(*Message, error)`,
  `service.Operation.Clone()` → `(Operation, error)`, `data.NewPathData` →
  `(Data, error)`, `artifacts.NewCategory`/`NewCategoryValue` and
  `bpmncommon.NewCallableElement` → `(X, error)` (caller options are now
  validated); `MustUpdateDefaultFlow` left the `flow.DefaultFlowHolder`
  interface (the concrete `Gateway` method remains);
  `artifacts.MustCategory`/`MustCategoryValue` added as fixture twins.
  Tests and examples keep `Must*` by design; a repo-local guard test
  (`internal/lintcfg`) now fails the build on any new `Must*` call site in
  library code (the two provably-infallible argless literals
  `MustBaseElement()`/`MustRecord()` stay sanctioned).

### Fixed

- **An instance's start time survives dehydration.** `inst.startTime` was set
  when an instance began and never restored, and the checkpoint carried no
  timestamp at all — so every rebuild silently restamped it to "now" and
  `RUNTIME/STARTED_AT` reported the age of the latest rebuild rather than of
  the process. The interval it lost was exactly the one that mattered: a long
  wait causes the dehydration, and the rebuild then erased the evidence that
  the wait happened. Checkpoints written before the field existed keep the
  previous behaviour rather than zeroing the clock.

- **Local `make ci` now has honest macOS/tool-version preflights (FIX-030).**
  The example runner detects Homebrew's `gtimeout` when GNU `timeout` is not
  available and otherwise fails immediately with the exact coreutils install
  hint. Every pinned Go dev tool is now checked by its embedded module version,
  so an old `mockery` or `covercheck` cannot pass a presence-only guard and
  fail later with misleading config or flag errors. The local golangci-lint
  installer is fetched from its pinned tag instead of the moving `master`
  branch.
- **CI now runs every example end-to-end, not just builds it (FIX-029).**
  The examples job (and the local `make ci`) gained a `run-examples` step:
  each of the 46 example modules executes under a timeout with stdin
  closed, asserting exit 0 — closing the FIX-002 §5 follow-up (a
  runtime-broken example used to ship green; it happened twice). The
  measured full sweep costs 33 seconds on the warm build cache.
- **Invariant-only errors are no longer silently discarded (FIX-028).**
  The commit-diff collection walk ignored `GetAt` errors (a corrupted
  `Collection` would silently diff a slot as nil and misfire conditional
  events), and a task's parameter access ignored the `Parameters` error
  (surfacing as a parameterless task copying nothing). Both branches are
  impossible while contracts hold and now fail fast with a classified
  panic naming the violated invariant. The FIX also closes the repo-wide
  discard sweep: every remaining bare-discard site is either the
  documented console carve-out or a comma-ok assertion, recorded in the
  doc's inventory.
- **A failed wake no longer strands a dehydrated instance (FIX-027).** A
  dehydrated instance has no goroutines — the engine-held wait (a timer
  deadline, a subscription, a task hold) *is* its liveness. The engine
  released that hold **before** attempting the wake, and a wake can fail
  (an unregistered pinned process version, a checkpoint that will not
  decode, a rebuild that errors). The instance was then left in the store
  as in-flight with nothing that would ever wake it, recoverable only by
  restarting the engine. The hold is now surrendered **only once the wake
  commits**; a failed wake keeps it and retries after a backoff
  (`WithWakeRetryBackoff`, defaulting to half the lease window), so the
  instance recovers by itself as soon as the cause clears — no store scan,
  no restart. A fired one-shot timer still fires exactly once.

- **`examples/message-send-receive`** — the example bound a task output into a
  `received-order` DataObject but never registered it, so after DataObjects
  became scope-resident (SRD-063) the instance failed at the output association
  (`OBJECT_NOT_FOUND`). It now registers the object and reads it from
  per-instance scope by name (via the trailing task's `DataReader`); runs green
  end-to-end.

## [v0.9.0] - 2026-07-18

### Added

- **Event Sub-Process — non-interrupting (SRD-053, ADR-023 v.2 §2.10 — #91,
  completing the type).** A non-interrupting triggered start
  (`events.WithNonInterrupting()`, any trigger except Error — Error is
  interrupting-only, rejected at validation) **forks** instead of cancelling:
  each fire spawns a handler instance in its **own** fresh child scope, binds
  that fire's payload there, and leaves the watch armed — so it fires **again**
  on the next trigger, unlimited concurrent instances, side-by-side (unique
  scope paths, not serialized). The scope's sibling work is **not** cancelled;
  the enclosing sub-process completes only once its own work and every live
  handler instance drain. The shared interrupting budget is untouched (a
  non-interrupting fire never spends it), and the interrupting path (SRD-052) is
  unchanged. See [`docs/guides/subprocesses/index.md`](docs/guides/subprocesses/index.md).

- **Event Sub-Process — interrupting (SRD-052, ADR-023 v.2 §2.10 — #91).** A
  `SubProcess` marked `triggeredByEvent` (`activities.WithTriggeredByEvent()`)
  is a **scope-armed handler**, not a token target: it is armed while its
  enclosing scope is open and fires when its single triggered start catches an
  event — the boundary-event pattern lifted from an activity's window to a
  scope's window. Triggers: **Message / Timer / Signal / Conditional** (armed
  as the scope's subscription; the Conditional start is ADR-006 v.3's deferred
  piece, now landed) and **Error** (caught on the §2.6 scope chain at the
  throw site, innermost catcher first). The interrupting variant (the default,
  BPMN §13.5.4; `events.WithNonInterrupting()` flips a start) fires a
  **cancel-and-run**: it cancels the enclosing scope's sibling tracks (the
  data plane stays open, so the handler runs in the parent's data context),
  runs its own flow in a fresh child scope seeded from the triggered start,
  and — reaching its End without re-throwing — **absorbs** the event so the
  parent resumes on its normal flow. A scope allows **one** interrupting fire:
  an event sub-process and a boundary event on the composite share the budget,
  so they cooperate rather than double-fire. Handler `Armed` / `Fired` /
  `Disarmed` facts (Boundary-kind, carrying a scope path) sit next to the
  scope cancel/complete facts. Non-interrupting handlers and Transaction
  boundaries remain deferred (#90). See
  [`examples/event-subprocess/`](examples/event-subprocess/) and
  [`docs/guides/subprocesses/index.md`](docs/guides/subprocesses/index.md).

- **Call Activity (SRD-050, ADR-023 v.1 — the second slice of the
  composition keystone #85, which it closes).** A Call Activity invokes a
  **separately registered process as its own child instance** — the reuse
  boundary, in contrast to the embedded Sub-Process's nested scope
  (`activities.NewCallActivity(name, calledKey, …)`;
  `activities.WithCalledVersion` pins a version). The caller's token parks
  while the loop launches the callee through the engine's registry —
  **latest-at-launch** by default (ADR-019) or the pinned version; the
  declared **Input** parameters are resolved at the caller's scope and
  **cloned across the boundary** (an isolated child data plane, no
  walk-up); on completion the declared **Output** parameters are read by
  name and **committed back** into the caller's scope. A child `BpmnError`
  faults the caller **at the Call Activity node**, catchable by an Error
  boundary (an untyped termination faults the instance); the child
  **terminates with the caller** (the cancel cascade). The launch seam is
  a new `exec.ProcessInvoker` capability (implemented by the thresher, kept
  off the node-execution surface); the caller instance receives it via
  `instance.WithInvoker`. New `Call` observability kind
  (Started/Completed/Failed/Terminated + called key, resolved version,
  child instance id); every child fact carries `parent_instance_id` +
  `call_activity_node_id`. New example `examples/call-activity/`, the
  Call Activity section of `docs/guides/subprocesses/index.md`. Closes epic #85.

- **Embedded Sub-Process (SRD-049, ADR-023 v.1 — the first slice of the
  composition keystone #85).** A Sub-Process is an activity in its
  parent's graph AND a container of its own inner graph
  (`activities.NewSubProcess` + `Add`/`flow.Link` inside; the exported
  `flow.ElementsContainer` core and the shared graph-clone wiring back
  it). It executes as a **nested scope inside the same instance**: the
  host token parks, the inner flow seeds per the validated BPMN §13.3.4
  shapes (exactly one None Start Event, XOR flow-less-node seeding —
  everything else rejected at registration), and the composite completes
  when the scope **drains** (no tokens remain). Data follows §10.5.7:
  inner reads walk up to the parent, inner locals die with the scope.
  Interruption is scope-wide: a boundary event on the composite cancels
  the whole scope onto its exception flow; a **Terminate End Event
  inside** terminates only its enclosing scope (§13.5.6 — the parent
  continues); an **Error walks the scope chain** to the innermost
  enclosing catcher (an Error End Event inside becomes catchable by
  enclosing composites). Nesting is unbounded; conditional events inside
  a sub-process evaluate at their own scope. New `Scope` observability
  kind (Opened/Completed/Terminated/Canceled + the scope path). New
  example `examples/embedded-subprocess/`, guide
  `docs/guides/subprocesses/index.md`. The Call Activity is the next slice.

### Fixed

- **Conditional catch lost its wake-up after a same-track move.** A track
  walking onto a conditional catch as its continuation node armed the
  subscription (evWaiting) and then its evMoved-driven boundary disarm
  tore the fresh watch down (the unconditional clearConds in
  disarmBoundaries) — the later data commit swept an empty registry and
  the instance hung (the TestConditionalEventsE2E flake, ~1 in 4-8 under
  race; fork-born catches were unaffected, making the per-instance
  clone's flow-map order the selector). The disarm is now
  boundary-flavor-scoped; the subscription's fact attribution also names
  the wait node instead of the stale previous position.

- **Conditional events (SRD-048, ADR-006 v.3 §2.7 — closes #89).**
  Data-driven waiting without polling: a conditional event's boolean
  condition over process data is evaluated at arm and re-evaluated on every
  **committed** data change (the SRD-044 commit-diff is the trigger signal),
  firing on the normative **false→true edge** (BPMN Table 10.84). Supported
  positions: **intermediate catch** (including **event-based-gateway arms**
  — the arms deferral of ADR-005 v.4 §2.12 closes), and **boundary events**
  — interrupting (cancels the guarded activity onto its exception flow) and
  non-interrupting (fires in parallel; re-fires only on a fresh edge). An
  expression may declare its read paths (`goexpr.WithDependencies`,
  `data.DependencyLister`): declared subscriptions re-evaluate only on
  overlapping commits (`data.PathsOverlap` — segment-prefix); undeclared
  ones re-evaluate on every non-empty commit (safe, just unfiltered). A
  **top-level conditional Start Event is rejected** at `Process.Validate` —
  Table 10.84 forbids its condition to reference process data; the
  conditional start arrives with event Sub-Processes.
  `NewConditionalEventDefinition` now requires a boolean-typed condition.
  Subscriptions are instance-loop-owned (no hub waiter); a conditional-free
  process pays nothing. New example `examples/conditional-events/`, guide
  `docs/guides/conditional-events.md`.

### Fixed

- **Fork-into-catch deadlock.** Building a fork-born track directly on a
  catch/wait node emitted `evWaiting` from the instance-loop goroutine
  (spawnForks → newTrack → checkNodeType), deadlocking the loop on its own
  channel. Construction-time classification no longer emits — the loop's
  spawn path records born-parked tracks — so a sequence flow may now fork
  straight into an event node.

- **Activity-outgoing conditional and default flows (SRD-046, closes #51).**
  A task's completion now honors the BPMN sequence-flow rule: an
  unconditional outgoing flow always fires, a **conditional** flow fires only
  when its condition is true, and the activity's **default** flow fires only
  when no conditional fired. Previously conditions and defaults on
  task-outgoing flows were model-accepted but **silently ignored** (all flows
  forked unconditionally); `SetDefaultFlow` now also rejects a flow carrying
  a condition (the gateway rule), and a `DefaultFlow()` getter is added.
  Selecting nothing (all conditions false, no default) faults the instance
  with a classified error — an explicit engine choice mirroring the gateway
  exception (Camunda-7-aligned). Single-outgoing-flow activities are
  untouched (a short-circuit keeps the common case cost-free).

## [v0.8.1-rc.1] - 2026-07-15

The **substrate** release: with the Core Task Types epic complete
(v0.8.0-rc.1), this cycle paid down the platform underneath it — the Instance
internals refactor, the error-propagation & logging policy, engine-wide
observability (all 13 catalog kinds emit), and the complete **structural
process data** conception (S1–S4): navigable, writable, change-detected
values, up to the host's own Go structs participating live.

### Added

- **Structural process data — navigable values end to end (ADR-011 v.6,
  SRD-042…045).** Process data is no longer opaque: a value can be a **record**
  (`data.Record` — ordered named fields, beside the existing `Collection`
  list capability), nested to any depth, and **addressed by path** —
  `order.items[0].price` — through the one data-access seam serving gateway
  conditions, expressions, mappings, and in-process service code (the
  `SOURCE/addr` provider split still runs first). Landed in four slices:
  - **Read** (SRD-042): the `Record` capability + the dynamic `values.Record`;
    shape-by-traversal helpers (`SchemaAt`/`Walk`); path resolution wired into
    the resolver, associations, and the fault source.
  - **Write** (SRD-043): `values.SetPath` sets a value at a path (dynamic
    targets auto-vivify missing intermediates; typed targets reject);
    `Collection.SetAt` — the atomic, cursor-free indexed write; output-mapping
    rules sharing a `Var` head **assemble one nested value** instead of flat
    variables.
  - **Change detection** (SRD-044): at each activity-boundary commit the scope
    **diffs** the committed value graph into `(path, ChangeType)` entries, and
    the track emits one `DataChange` observability fact per changed path
    (observer-only, never echoed) — the 13th catalog kind now emits; all 13
    are asserted by the completeness canary.
  - **Native structs** (SRD-045): `adapters.Wrap(&order)` makes the host's
    **own Go struct** a live navigable value — wrap, not convert — with
    `gobpm:"..."` tags (rename, `-` exclusion) and `adapters.Register[T]`
    (a custom adapter factory for types you can't modify; the codegen
    generator's future seam). Reflection is **bounded**: once per type, at
    the first `Wrap`, cached — never on the execution path (registered as an
    engine choice in SAD-001 §6).

  Four runnable examples (`structural-data`, `structural-output-mapping`,
  `data-change`, `native-structs`) and the process-data guide
  ([docs/guides/data/index.md](docs/guides/data/index.md)).

- **Engine-wide observability — the observable-event seam (ADR-013 v.2 / SRD-041).**
  Every failure and major-object lifecycle transition now emits one
  `observability.Fact` through a single `Reporter` that both echoes to the
  operator log (levels per ADR-022) and fans out to observers. 12 of the 13
  catalog kinds emit: engine and hub state, process registration, instance
  lifecycle, un-collapsed node progress, gateway decisions, event flow,
  correlation, the worker-job lifecycle, user tasks, boundaries, and the fault
  triple (Thrown/Caught/Uncaught — the previously silent boundary-caught path is
  now visible). A new **engine-scope** registry, `Thresher.Observe(o)`, watches
  every instance plus engine-level facts through one stream (the instance-scoped
  `InstanceHandle.Observe` remains). An optional visibility seam
  (`LogRedactor` / `ObservationFilter` on the authorization extension) can redact
  or filter per recipient; unimplemented ⇒ pass-through. `DataChange` (the 13th
  kind) was deferred here and landed with the structural-data work above
  (SRD-044) — all 13 kinds emit in this release.

### Changed

- **Error propagation & logging policy (ADR-022 / FIX-022).** Every error is
  handled **exactly once** — logged XOR returned, never both; fail-fast vs
  best-effort is decided by the called function's actual failure surface; log
  attributes use one canonical snake_case vocabulary; silence is opt-out. A
  repo-wide sweep removed every silent `_ =` error discard from production
  code (the one documented console carve-out remains).

- **Instance internals refactored (SRD-040) — behavior-preserving.** The
  1661-line `instance.go` split one-concern-per-file; the event loop's state
  moved into a loop-constructed `loopState` (structural confinement — never
  an `Instance` field); correlation keys extracted into a `correlator`. The
  public surface is byte-identical; zero `pkg/thresher` edits.

- **BREAKING (pre-1.0): the `data.Collection` interface gains `SetAt`.**
  `SetAt(ctx, index, value) — the atomic, cursor-free indexed write ([0, len)
  replaces, == len appends, past-len errors)` — external `Collection`
  implementers must add it (none known besides the in-repo `values.Array` and
  the new adapter views). `Scope.Commit`/`Frame.Commit` (internal) now return
  the committed changed-path set alongside the error.

- **BREAKING (pre-1.0): the dormant in-value subscription machinery is
  removed.** `data.Updater`/`UpdateCallback` (zero consumers, incompatible
  with the engine's clone/commit execution model) are deleted per ADR-011
  v.6 §2.9.4; change detection is the scope's commit-diff. `data.ChangeType`
  (`Value_Added/Updated/Deleted`) is retained, retargeted as the diff
  vocabulary.

- **BREAKING (pre-1.0): the observation surface is one canonical type.**
  `thresher.Event`, `thresher.EventKind`, and `Observer.OnEvent` are removed;
  `thresher.Observer` is now a type alias of `observability.Observer`, so an
  observer implements `OnFact(observability.Fact)` and `InstanceHandle.Observe` /
  `Thresher.Observe` deliver an `observability.Fact` directly (identity + `Kind` +
  `Phase` + a masked `Details` map; `instance_id` moved into `Details`). Delivery
  semantics are unchanged (buffered, lossy, drop-counted, panic-recovered).
  `EngineRuntime` gains a `Reporter()` accessor (external implementers must add
  it; none known).

### Fixed

- **The PR-CI event-gate readiness race (FIX-021).** A test could observe a
  token parked before the instance's event waiters were registered and fire
  an event into the void. Fixed at both test levels: a registration-counter
  harness in the instance tests, and a `SignalCatchers` probe on the hub
  (counting catchers, not waiters — a same-id catch joins the existing
  waiter). Also pins the CI-parity `TEST_CPUS=4` budget in `test-all`.

## [v0.8.0-rc.1] - 2026-07-10

Completes the **Core Task Types** epic (#78): Service Task, User Task, and
Manual Task now all execute on the park/resume core.

### Added

- **Service Task execution model — in-process & external workers (ADR-021).**
  A `ServiceTask` now executes on two cleanly-separated loci. **In-process**
  (default): the synchronous operation on the track goroutine, optionally
  time-bounded and cancellable via `activities.WithTimeout(d)`. **External
  workers**: the ServiceTask becomes a wait node that enqueues a job onto an
  engine-owned asynchronous **fetch-and-lock job queue** (`activities.WithWorker(topic)`);
  workers pull by topic, execute, and report, and the report resumes the parked
  track — so a worker-waiting instance holds no live call (dehydration-ready).
  The batteries-included in-memory `localdispatcher` + local worker pool need
  zero extra infrastructure.

  Worker outcomes are classified by `{code, body}` into four kinds via a
  pluggable, declarative `ErrorMapper`: **Complete** (with `WithOutputMapping`
  shaping the raw body into output variables), **Business Error** (interrupting —
  a BPMN error caught by an Error boundary), **Business Status** (non-interrupting
  — a domain state written to a `WithStatus` variable and routed by a gateway),
  and **Technical fault** (retried). An extendable `RetryPolicy` (`NoRetry` /
  `FixedDelay` / `ExponentialBackoff`; default 3× jittered backoff) governs
  technical-fault retries. `WithWorkerTrust(mode)` selects where the whole policy
  bundle (output mapping + classification + retry) runs: **`WorkerTrusted`**
  (default) — the worker runs it in-process (maps, self-classifies via a
  `WorkerError`, retries holding its lock) and reports a verdict;
  **`EngineAuthoritative`** — the worker returns raw `{code, body}` and the engine
  owns the policy (re-enqueue retry). Worked example:
  `examples/service-task-worker/`.

- **User Task & Manual Task execution (ADR-020).** `activities.NewUserTask` is a
  wait node parked for a human to complete, gated by Camunda-style triad
  authorization (assignee / candidate users / candidate groups over an
  `Actor{UserID, Groups}`); a `TaskDistributor` boundary announces and retracts
  parked tasks (with a bundled console driver) and a `TaskView` exposes them.
  `ManualTask` is a pass-through no-op (a human-performed step with no engine
  automation).

- **Parallel-start event-gateway correlation validation (SRD-033).** Enforces the
  ADR-005 rule that a parallel-start event-based gateway's arms must carry
  correlation — rejected at registration, with a runtime guard for a conformant
  model that meets a non-conformant (underivable-key) message. Closes the AB-001
  defect where a keyless instantiating gateway spawned N stuck instances (one per
  arm message) instead of one.

- **Definition-versioning example (`examples/versioning/`).** A runnable demo of
  the versioning surface: registering a key twice yields v1/v2; `StartLatest` /
  `StartVersion` / `StartProcess` each resolve the expected version;
  `Registrations(key)` enumerates live versions; unregistering the latest
  promotes the previous one back.

### Changed

- **BREAKING — process registration and start API (ADR-019, SRD-031.A).**
  `Thresher.RegisterProcess` now returns a `(*ProcessRegistration, error)`
  registration handle instead of a bare `error`, and re-registering the same
  process id mints a new integer version instead of silently no-op'ing.
  `StartProcess` now takes that handle (was: the process id). Two new methods
  address by process id: `StartLatest(key)` (newest version) and
  `StartVersion(key, n)` (a specific version). The latest registered version
  owns auto-start — registering a newer version supersedes the previous one's
  starters, and unregistering the latest promotes the now-newest remaining
  version. Removal is split by scope: `UnregisterProcess(reg)` is renamed
  `UnregisterVersion(reg)` (removes one version), and `UnregisterProcess(key)`
  now removes the whole process (every version of the key). Version numbers are
  monotonic per key and never reused, so removing a non-latest version leaves a
  gap; `Registrations(key)` enumerates a key's versions.

  Migration: `engine.RegisterProcess(p)` → `reg, _ := engine.RegisterProcess(p)`;
  `engine.StartProcess(p.ID())` → `engine.StartProcess(reg)` or
  `engine.StartLatest(p.ID())`; `engine.UnregisterProcess(reg)` →
  `engine.UnregisterVersion(reg)` (or `engine.UnregisterProcess(p.ID())` to drop
  all versions).

- **Thresher lifecycle: atomic state with transitional `Starting`/`Stopping`
  (ADR-019 §2.7, SRD-031.B).** The engine `State` enum gains two transitional
  values — `Starting` (between `NotStarted` and `Started`) and `Stopping`
  (between `Started` and `Stopped`). `Run` and `Shutdown` now drive the lifecycle
  with compare-and-swap, so concurrent double-`Run` / double-`Shutdown` are
  deterministic (one wins; the rest reject or no-op), and `Started` / `Stopped`
  carry stronger meanings (hub accepting / teardown complete). Successful
  single-caller behavior is unchanged. Internally `state` is now lock-free
  (atomic) and every registry critical section is confined to a lock-held helper,
  retiring the fragile-mutex audit finding (§2.6).

- **BREAKING — `errs` error details are string-typed and reflection-free
  (FIX-019).** In `pkg/errs`, `Details` changes from `map[string]any` to
  `map[string]string`; `D(k string, v any)` becomes `D(k, v string)`; `Error()`
  is rebuilt with a `strings.Builder` (no reflective `%v`); and `JSON()` returns
  `([]byte, error)` instead of panicking. This removes `any`-boxing and reflective
  formatting from the error path; call sites migrated to pre-stringified values
  (`strconv.Itoa`, `.ID()`, etc.).

- **Event-trigger validity is enforced at compile time.** Each Start/End event
  configuration now add-or-rejects every trigger kind, so invalid combinations —
  a Cancel trigger on a Start event, a Conditional/Timer trigger on an End event —
  are rejected with a clear error instead of surfacing a leaky runtime
  `INVALID_TYPECASTING`. No behavior change for valid usage.

### Fixed

- **Snapshot property isolation (FIX-016).** A P1 data race: `Snapshot` shared
  mutable process `Property` objects by reference, so concurrent instances of the
  same process (and successive runs) corrupted each other's property state.
  `Snapshot.Clone` now clones properties per instance and `Snapshot.New` freezes a
  per-template copy, restoring the frozen-version guarantee (ADR-019).

- **Node-property clone isolation + value-less rejection (FIX-017).** Activity
  property maps and event property slices were copied by reference across the
  process → snapshot → instance boundary, leaking mutable `*Property` objects
  between instances; the clone is now a deep copy (a single `data.CloneProperties`
  helper). Value-less properties are rejected uniformly at node level.

- **Consistent element properties across all property-owning node types
  (FIX-018).** `data.WithProperties` was accepted by only 4 of the 9 BPMN
  property-owning node types (rejected by `NewUserTask` and the
  intermediate/boundary events), and catch events never loaded their declared
  properties at runtime. All property-owning activities and events now uniformly
  declare and load properties.

- **Correctness sweep — eleven localized defects (FIX-014).** Among them:
  `Array.Insert` could not append at `index == len`; `Array.Clone` reset the
  iteration cursor; a `/`-keyed root scope was omitted from name resolution;
  default-flow routing stored the caller's pointer instead of the member;
  `DeriveKey` accepted a present-but-nil value as a key part; `clocktest.Advance`
  could move the clock backwards; `memmetrics.seriesKey` collided distinct
  attribute sets; `memtrace.liveSpan` mutated span state without synchronization.
  No public-contract changes.

- **Doc-comment drift corrected.** Stale `WithId` references fixed to `WithID`;
  optioned-constructor doc comments realigned to the code.

- **membroker: message subscriptions are torn down on waiter stop.** A stopped
  message waiter previously left its subscription registered, so a later publish
  on the same message name could be swallowed into the dead (buffered) channel
  before a live waiter received it. `messaging.Subscription` gains
  `Unsubscribe()`; the message waiter now unsubscribes on every exit path.

## [v0.7.0] - 2026-06-28

**Version-line correction — no functional change from v0.1.1.**

The module's tag history carries an abandoned pre-2023 codebase (the
`v0.2.0-prerelease` … `v0.6.x` line, last published `v0.6.3` in 2022). Because
the module proxy serves the **highest** semver tag as "latest", that old code —
not the current ground-up rewrite — was what `pkg.go.dev` displayed, even after
`v0.1.1`. This release renumbers the current code **above** that line so the
proxy and `pkg.go.dev` reflect the actual module.

### Changed
- Version bumped `v0.1.1` → `v0.7.0` to supersede the abandoned `v0.6.x` line on
  the module proxy. The code is identical to `v0.1.1` (the complete 0.1.0 MVP
  element set — see below).

### Removed
- `retract` directive added for `[v0.2.0-prerelease, v0.6.4-prerelease]` — the
  pre-2023 codebase no longer reflects this module's API and should not be
  selected by `go get` or shown as current.

## [v0.1.1] - 2026-06-28

The 0.1.0 MVP element set is complete: the engine executes the high-frequency
BPMN core chosen by real-world usage frequency (SAD-001 §15.3).

### Added
- **Gateways**: Exclusive, Parallel, Inclusive (split + synchronizing OR-join),
  Complex (activation-threshold join), and Event-Based (mid-flow deferred choice
  and event-based instance start).
- **Events**: None start/end; Timer / Message / Signal intermediate catch and
  throw; signal-start instantiation; **Error End Event**; **Terminate End Event**
  (abnormal whole-instance termination).
- **Boundary events**: interrupting and non-interrupting, on Timer / Message /
  Signal / Error triggers, with per-track cancellation of the guarded activity.
- **Error handling**: `BpmnError` propagation and the Error Boundary Event.
- **Tasks**: Service, User, Send, Receive.
- **Messaging**: cross-instance message correlation by conversation keys, and
  event-triggered process instantiation.
- **Process data**: a container-scoped data plane with per-execution frames and
  addressable data sources (the `RUNTIME` provider, path-qualified reads).
- **Observability**: structured `slog` logging, visible by default with an
  explicit opt-out; a startup banner reporting the engine version and build
  revision.

### Changed
- Execution core reworked to a single-writer, per-instance event loop: one
  goroutine owns instance state and tracks communicate through it via events
  (ADR-001 / ADR-017).

### Fixed
- OR-join all-branches-arrive synchronization hang.
- Complex-join recheck race causing spurious gateway abort/hang.
- Non-message broadcast double-fire across concurrent instances sharing a catch.
- Runtime deadlocks in the bundled examples.

### Testing
- Diff-coverage CI gate (`covercheck`); every package now at or above 80%
  statement coverage.

## [v0.1.0] - Initial Development Phase

### Features Implemented
- BPMN v2 compatible BPM engine core
- Event-driven process execution
- Process instance management
- Timer event support
- Comprehensive BPMN model implementation
- Data handling and expression evaluation
- Error handling system
- Monitoring and observability

### Architecture
- Modular package structure
- Clean interfaces and abstractions
- Thread-safe concurrent processing
- Context-based cancellation support
- Extensible event system

### Components
- **Thresher**: Main BPM engine for process orchestration
- **EventHub**: Central event distribution system
- **Model Layer**: Complete BPMN element implementations
- **Instance Management**: Process execution and state management
- **Data Model**: Variable and expression handling
- **Error System**: Structured error handling

---

## Development Guidelines

### Versioning Strategy
- **Major** (X.0.0): Breaking API changes
- **Minor** (0.X.0): New features, backward compatible
- **Patch** (0.0.X): Bug fixes, backward compatible

### Changelog Categories
- **Added**: New features and capabilities
- **Changed**: Changes in existing functionality
- **Deprecated**: Soon-to-be removed features
- **Removed**: Features removed in this version
- **Fixed**: Bug fixes and error corrections
- **Security**: Security vulnerability fixes
- **Performance**: Performance improvements
- **Testing**: Test coverage and quality improvements
- **Documentation**: Documentation updates and additions

### Commit Message Format
Following [Conventional Commits](https://www.conventionalcommits.org/):
- `feat:` - New features
- `fix:` - Bug fixes
- `docs:` - Documentation changes
- `test:` - Test improvements
- `refactor:` - Code refactoring
- `perf:` - Performance improvements
- `chore:` - Maintenance tasks

### Breaking Changes
All breaking changes will be clearly documented with:
- **BREAKING CHANGE**: Clear indication in commit message
- Migration guide for updating existing code
- Deprecation warnings in prior minor version when possible
- Detailed explanation of the change and rationale

### Release Process
1. Update CHANGELOG.md with all changes
2. Update version numbers in relevant files
3. Create release tag following semver
4. Generate release notes from changelog
5. Update documentation if needed

### Contributing to Changelog
When contributing:
1. Add your changes to the "Unreleased" section
2. Use appropriate category (Added, Changed, Fixed, etc.)
3. Include issue/PR references where applicable
4. Describe user-facing impact, not internal details
5. Keep entries concise but informative

### Example Entry Format
```markdown
### Added
- Event-driven process execution with Timer support (#123)
- Comprehensive test suite achieving 75%+ coverage (#124)

### Fixed
- **CRITICAL**: Nil pointer dereference in EventHub registration (#125)
- Memory leak in process instance cleanup (#126)

### Changed
- **BREAKING**: EventProcessor interface now requires context parameter (#127)
- Improved error messages for better debugging experience (#128)
```

---

*This changelog is maintained manually alongside development. For detailed commit history, see the Git log.*
