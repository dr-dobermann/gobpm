# Changelog

All notable changes to the GoBPM project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
  ([docs/guides/data.md](docs/guides/data.md)).

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
