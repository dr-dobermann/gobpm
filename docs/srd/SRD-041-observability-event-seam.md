# SRD-041 — The observable-event seam: taxonomy wiring, engine scope, visibility

| Field | Value |
|---|---|
| Status | Draft v.1 |
| Version | v.1 |
| Date | 2026-07-11 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-013 v.2](../design/ADR-013-instance-observability.md) §2.6–§2.11 (the observable-event taxonomy, the single producer, the engine observer scope, the details payload, the v.1 corrections, the visibility-policy seam). ADR-013 v.2 is Draft and flips to Accepted when this SRD lands. |
| Upstream | [ADR-022 v.1](../design/ADR-022-error-propagation-and-logging-policy.md) (log-echo levels §2.4, the attribute vocabulary §2.5, separate-channels §2.7), [ADR-002 v.2](../design/ADR-002-extension-architecture.md) (the extension accessor pattern the sink follows), [ADR-012 v.1](../design/ADR-012-execution-layering.md) (public contracts live in `pkg/*`) |

This SRD wires ADR-013 v.2: every catalog transition emits **one** event through
**one** producer that writes the operator-log echo and feeds the observer
stream; the engine gains an **engine-scope observer registry** that sees
everything; events carry a **details map** keyed by the ADR-022 §2.5
vocabulary; the three v.1 defects (collapsed node progress, invisible faults,
dormant data callback) are corrected; and the **visibility capabilities**
(pass-through by default) gate both channels.

Evidence base: the two-channel completeness gap matrix (2026-07-11 audit,
persisted in the session scratchpad) — the current stream has exactly **two**
kinds emitted from **two** `notify()` sites (`internal/instance/lifecycle.go:53`,
`internal/instance/track.go:236`).

---

## 1. Background & current state (verified against the code)

- **Internal event side** (`internal/instance/observer.go`): `ObsKind` is a
  2-value `uint8` enum (`InstanceState`, `NodeProgress`); `Fact` is
  `{At, NodeID, NodeName, State string, Kind}` — no details map, no instance
  id (added later by the public wrapper). `notify(kind, nodeID, nodeName,
  state)` fans out under `obsMu` read-lock; it builds the event only when
  observers exist (hot-path guard to preserve).
- **Public observer side** (`pkg/thresher/observer.go`): `Event`
  `{At, Kind EventKind, InstanceID, NodeID, NodeName, State}` with **string**
  `EventKind` (already the ADR-013 §2.4 open vocabulary — extension is
  additive consts); `Observer{OnEvent(Event)}`; `InstanceHandle.Observe`
  wraps `Instance.AddObserver` with the per-observer buffered channel + drain
  goroutine + drop counter (`observerBuffer = 64`); `eventKind()` maps
  internal→public and **already falls back to `k.String()`** for unknown
  kinds — new internal kinds auto-map.
- **Emission gaps**: per the gap matrix — instance states emit Facts but
  no log (`setState`, `lifecycle.go:51-53`); `Created` bypasses `notify`
  (`instance.go:163` raw `state.Store`); node progress collapses `trackState`
  to a 3-value token projection (`token.go:95-117` via `track.record`,
  `track.go:236`); everything engine-side (thresher states, hub, process
  registration, the whole `localdispatcher` job lifecycle), correlation,
  user tasks, boundaries, and faults emit **no** Fact; gateway decisions,
  task-taken, boundary arm/disarm, and boundary-caught faults
  (`track.go:648-658` `discardOrFail` + `boundary_watch.go:177-208`
  `matchErrorBoundary`) are silent on **both** channels.
- **The engine runtime seam** (`pkg/renv/engineruntime.go`): `EngineRuntime`
  exposes the extension accessors (`Logger()`, `Clock()`, …); implemented by
  `thresherConfig` (`pkg/thresher/options.go`), `internal/enginert.Runtime`,
  and the generated `mockrenv`. `RuntimeEnvironment` embeds it — nodes
  already reach `Logger()` through it, so a sink accessor lands on the same
  path.
- **The dormant data callback** (`pkg/model/data/value.go:92-118`): the
  separate optional `data.Updater` interface (beside `Value`, which does NOT
  carry it) — `Register(regName string, updFunc UpdateCallback) error`
  (errors on a duplicate name) with `ChangeType`
  (`Value_Updated/Added/Deleted`) and an async per-value fan-out
  (`values/variable.go:130`, `values/array.go:432`; the concrete
  `values.Variable`/`values.Array` implement `Updater`) — zero non-test
  consumers.
- **The auth extension** (`pkg/auth`): `AuthorizationProvider{Authorize(ctx,
  Request) error}` with the `allowall` default — the anchor for the §2.11
  visibility capabilities (optional interfaces, type-asserted).

## 2. Requirements

### Functional

- **FR-1 — the canonical vocabulary, public, one event type.** The 13 ADR-013
  §2.6 kinds and their phase sets exist as public string constants
  (`thresher.EventKind` extends; kind/phase strings defined once beside the
  canonical event). The internal `instance.Fact`/`ObsKind` pair is
  **replaced** by the canonical `obs.Fact` (§3.2) — one type from emitter
  to delivery; `Instance.AddObserver`'s callback signature changes accordingly
  (internal, package-private consumers only — verified: the sole external
  consumer is the handle wrapper).
- **FR-2 — the details map.** Both the internal `Fact` and the public
  `Event` gain `Details map[string]string`; keys come from the ADR-022 §2.5
  canonical vocabulary (`instance_id`, `node_id`, `job_id`, `task_id`,
  `correlation_key`/`correlation_value`, `error`, …). Masking holds: ids,
  names, states, codes — never payload values.
- **FR-3 — the Reporter seam.** A minimal public sink contract (an
  `Report(Fact)`-style single-method interface) joins the engine extension
  set: `EngineRuntime` gains an accessor for it (the `Logger()` pattern), so
  the hub, the dispatcher, and — via `RuntimeEnvironment` — every node reach
  the same producer the instance uses.
- **FR-4 — one producer, two channels.** The sink's `Report` is THE producer:
  it (a) writes the operator-log echo at the kind/phase-mapped ADR-022 level
  (with the event's details as the log attributes), and (b) fans out to the
  engine-scope observers — after the §2.11 visibility checks. Instance-scope
  events additionally fan out to the instance's local observers (the v.1
  path). One call site per catalog transition; `Logger()` stays for
  non-catalog diagnostics.
- **FR-5 — engine-scope registry.** `Thresher.Observe(o Observer)
  *Subscription` — same `Observer`/`Event`/`Subscription` contract as the
  instance handle (buffered, lossy, drop-counted, panic-contained); receives
  every engine-kind event AND every instance's events (each carrying
  `instance_id`).
- **FR-6 — the emissions.** Every §2.6 catalog transition emits: engine
  states (incl. the previously log-only `Starting/Started/Stopping/Stopped`),
  hub start/stop, process registered/unregistered/superseded, instance
  `Created` (today bypassing notify) through terminal states, un-collapsed
  node phases, gateway decisions, event flow
  (registered/fired/delivered/dropped/unregistered), correlation
  (associated/matched/mismatched), the job lifecycle
  (enqueued…retries-exhausted/lock-reclaimed), user-task
  announced/taken/completed/withdrawn, boundary armed/fired/disarmed, and
  faults Thrown/Caught/Uncaught (closing the silent boundary-caught path).
- **FR-7 — data changes.** The dormant `data.UpdateCallback` becomes the
  `DataChange` source: instance-committed values get a callback that emits
  (element name/id + change type, never the value) to the observer stream
  only — **no log echo** (the flood guard).
- **FR-8 — visibility capabilities.** Two optional interfaces (working names
  `LogRedactor`, `ObservationFilter`) are defined publicly and asserted
  against the configured `AuthorizationProvider` **once at wiring**;
  unimplemented ⇒ pass-through. `LogRedactor` may transform/suppress the log
  echo; `ObservationFilter` may allow/redact/deny per registered observer; a
  policy-denied event does not increment the drop counter.
- **FR-9 — compatibility.** The public v.1 surface keeps working unchanged:
  `InstanceHandle.Observe` and existing `Event` consumers compile and behave
  as before (new fields/kinds are additive; unknown kinds were already
  documented as to-be-tolerated).

### Non-functional

- **NFR-1 — hot-path cost.** With no observers and no policy, an emission
  costs at most the existing log call plus one registry-empty check; the
  event struct/details map is built only when someone listens or the echo
  needs details (preserve the `notify` build-only-when-listening guard).
- **NFR-2 — no new races.** Emission points ride their existing execution
  contexts (the instance loop, the thresher's locked sections, the
  waiter/dispatcher goroutines that already log there) — except
  `DataChange`, whose values fan-out runs on its own goroutine
  (`sendVariableUpdates`/`sendArrayUpdates`), so the sink's `Report` MUST be
  safe to call from arbitrary goroutines (it is: the echo is a logger call;
  the fan-out is the existing lock-guarded non-blocking send). `-race` clean.
- **NFR-3 — ADR-022 conformance.** Echo levels follow §2.4 (hot-path kinds at
  `Debug`; `DataChange` echoes nothing); attribute keys follow §2.5; no
  log-and-return regressions.
- **NFR-4 — coverage.** Diff-coverage ≥95% (aim 100%) per `make ci`; every
  new emission path exercised.

## 3. Models

### 3.1 The public vocabulary and event (`pkg/thresher/observer.go`)

`EventKind` gains the eleven new kinds (string values = the internal
`ObsKind.String()` names):

```go
EventEngineState     EventKind = "EngineState"     // Starting, Started, Paused, Stopping, Stopped (⏳ Resumed)
EventHubState        EventKind = "HubState"        // Started, Stopped (⏳ Paused/Resumed)
EventProcessLifecycle EventKind = "ProcessLifecycle" // Registered, Unregistered, VersionSuperseded
EventGatewayDecision EventKind = "GatewayDecision" // BranchesChosen
EventFlow            EventKind = "EventFlow"       // Registered, Fired, Delivered, Dropped, Unregistered
EventCorrelation     EventKind = "Correlation"     // KeyAssociated, Matched, Mismatched
EventJobState        EventKind = "JobState"        // Enqueued…RetriesExhausted, LockReclaimed (⏳ Incident)
EventTaskState       EventKind = "TaskState"       // Announced, Taken, Completed, Withdrawn
EventBoundary        EventKind = "Boundary"        // Armed, Fired, Disarmed
EventFault           EventKind = "Fault"           // Thrown, Caught, Uncaught
EventDataChange      EventKind = "DataChange"      // Value_Added/Updated/Deleted (data.ChangeType)
```

`Event` gains `Details map[string]string` (nil-able; ADR-022 §2.5 keys). The
existing `EventInstanceState`/`EventNodeProgress` stay; `EventTokenMoved`
stays reserved.

### 3.2 the Reporter seam (`pkg/observability` + `pkg/renv`)

```go
// pkg/observability — beside Logger/Tracer/MetricsRecorder.
// Reporter receives every observable engine event (ADR-013 v.2 §2.7): the
// single producer behind it writes the log echo and fans out to observers.
// Report must be non-blocking for the caller.
type Reporter interface {
	Report(ev Fact)
}
```

The canonical event struct moves to `pkg/observability`. Its enumerated
fields are **named string types**, not bare `string` (compile-checked, the
vocabulary is discoverable): `type Kind string` (the 13 §2.6 kinds as typed
consts) and `type Phase string` (the per-kind phases as typed consts).

```go
type Fact struct {
	At       time.Time
	Kind     Kind
	Phase    Phase
	NodeID   string            // real id — bare string
	NodeName string            // real name — bare string
	Details  map[string]string // §2.5-keyed; values are variadic
}
```

The **details keys** are named single-source constants (`AttrInstanceID =
"instance_id"`, … — the ADR-022 §2.5 vocabulary, kept untyped string so one
set of constants serves both the details map AND slog `...any` echo calls —
the one-vocabulary-two-channels point of §2.9). `thresher.EventKind` becomes
a **type alias** of `observability.Kind` (`type EventKind = observability.Kind`)
so the public kind constants are the same values with no conversion, and the
existing `EventInstanceState`/`EventNodeProgress` consts keep working. So
`internal/instance`, the hub, the dispatcher, and
`pkg/model` nodes all emit one type (the internal `Fact`/`ObsKind` pair is
replaced — FR-1); `thresher.Event` remains the delivered public shape
(constructed from it, `InstanceID` promoted from details for compatibility).
`EngineRuntime` gains `Reporter() observability.Reporter`; the internal
emitters (the instance loop, the hub, the dispatcher) reach the sink through it.
The one node-level emission — a gateway's branch decision — is emitted by the
track at its node-execution site (§3.4), not from inside `Exec`, so a gateway's
`Exec` stays a pure decision function and `RuntimeEnvironment` needs no new
observation method. Regeneration covers the generated `MockRuntimeEnvironment`
(the only renv mock; it satisfies `EngineRuntime` by expansion).

**The default sink is never silent.** `enginert.Runtime` and `thresherConfig`
default to an **echo-only producer bound to the configured logger** — no
observers, no policy, but every emission still writes its §2.6-level log echo.
This preserves the visible-by-default posture (ADR-022 §2.6) and keeps the
relocated fault echo (§3.4) intact for every enginert-based runtime and test;
a true no-op sink exists only as an explicit opt-out.

**The dispatcher binder.** `localdispatcher.Dispatcher` holds no runtime — it
receives capabilities via the optional binder pattern (`tasks.LoggerBinder`
et al.). A new optional `tasks.ReporterBinder{BindReporter(
observability.Reporter)}` joins that set; the Thresher binds it at startup
exactly like the logger. A third-party dispatcher without it simply does not
emit (the optional-capability pattern).

### 3.3 The producer (`pkg/thresher`, engine-owned)

One implementation behind the sink: per event it (1) applies `LogRedactor`
if present → writes the echo via the configured `Logger()` at the
kind/phase-mapped level (a small `levelFor(kind, phase)` table implementing
the §2.6 column: `Info` lifecycle, `Debug` flow, `Warn`/`Error` per-phase
overrides, none for `DataChange`); (2) for each engine-scope subscription,
applies `ObservationFilter` if present → non-blocking send (the existing
buffered/drop contract). The instance's local fan-out keeps living in
`internal/instance` (the v.1 path), now fed by the same emission call —
`Instance.observe(ev)` replaces raw `notify(...)`, forwards to the engine
sink, and keeps the local observer list semantics intact. **The
`ObservationFilter` applies to instance-scope observers too** (ADR-013 §2.11
is per-recipient with no scope carve-out): the handle's `Observe` wrapper —
where the per-observer buffered channel already lives — runs the filter
before the send, so a policy-denied event never reaches a handle observer
either, and denied ≠ dropped in both scopes.

**A worked dual-channel row** (the shape T-3 pins). A job exhausting its
retries emits once:

```go
sink.Report(obs.Fact{
    Kind: "JobState", Phase: "RetriesExhausted",
    Details: map[string]string{
        "instance_id": iid, "job_id": jid, "topic": tp,
        "attempts": "3", "error": lastErr.Error(),
    }})
```

producing (a) the echo `Warn "job retries exhausted" job_id=… topic=…
attempts=3 error=…` — the existing FIX-022 record, now written by the
producer — and (b) the delivered `thresher.Event{Kind: EventJobState,
State: "RetriesExhausted", InstanceID: iid, Details: …}` on the engine-scope
stream. One call, two channels, one vocabulary.

### 3.4 Emission points (per gap-matrix row; details carry §2.5 keys)

| Where | Emits |
|---|---|
| `Thresher.Run/Shutdown` state walk (+ the live `Paused` state — reachable via `UpdateState`, so it emits too per the completeness rule) | `EngineState` per transition (echo Info — the missing lifecycle logs) |
| hub `Start/Shutdown` | `HubState` |
| `RegisterProcess`/`UnregisterVersion`/`UnregisterProcess`/supersede | `ProcessLifecycle` |
| `Instance.New` (+`setState`) | `InstanceState` incl. `Created`; `Failed` phase echoes Error (replaces the direct `fail()` log — the producer becomes its echo). `Failed` is **phase-only in this landing**: the `State` enum is untouched; `State()`/`WaitCompletion` still report `Terminated` |
| `track.record` (un-collapsed) | `NodeProgress` with the real phase (Entered/Executing/Completed/Failed/Canceled/Merged/Parked mapped from `trackState`, not the token projection) |
| the track's node-execution site (`track.executeNodeCore`), when the executed node is a diverging gateway (`>1` outgoing) | `GatewayDecision` with chosen flow ids. Emitted at the one node-execution site — gated by a `DefaultFlow()` marker interface, not `NodeType()` (which panics on a bare `BaseNode`) — rather than inside each gateway's `Exec`, so `Exec` stays a pure decision function (no observation side-effect, no per-gateway test churn). A converging merge/join (single outgoing) is a pass-through, not a decision, and is skipped |
| hub register/unregister/propagate/broadcast/drop | `EventFlow` |
| `correlator.validateAndAssociate`/`associate` | `Correlation` |
| `localdispatcher` enqueue/lock/report/retry/exhaust/reclaim | `JobState` (echo levels = the FIX-022 levels) |
| `loopState.addTask`/`handleTaskRequest`/`Instance.withdrawTask` | `TaskState` |
| `loopState.armBoundaries`/`fireBoundary`/`disarmBoundaries` | `Boundary` |
| `track.discardOrFail` → Thrown; `matchErrorBoundary` hit → Caught; `Instance.fail` → Uncaught | `Fault` |
| `instanceScope` commit → `d.Value().(data.Updater)` assertion → `Register("obs:"+instanceID, cb)` | `DataChange` (stream-only). `Register` lives on the separate optional `data.Updater` interface (not `Value`); non-`Updater` values simply don't emit. Idempotency: one canonical registration name per instance — a duplicate-`Register` error is the expected already-registered no-op (checked, skipped with a comment) |

### 3.5 Visibility capabilities (`pkg/observability`)

```go
// LogRedactor — optional capability on the AuthorizationProvider: transform
// or suppress the log echo of an observable event (ADR-013 v.2 §2.11).
type LogRedactor interface {
	RedactLog(ev Fact) (Fact, bool) // ok=false suppresses the record
}

// ObservationFilter — optional capability: per-recipient visibility of an
// observable event. ok=false denies delivery (not a counted drop).
type ObservationFilter interface {
	FilterObservation(observer any, ev Fact) (Fact, bool)
}
```

Asserted once against `cfg.authz` at engine start / observer registration;
absent ⇒ nil fast-path (no per-event assertion). `allowall` implements
neither — pass-through by default, per the ADR.

## 4. Analysis

- **Why the sink is an EngineRuntime extension** (vs a Thresher-only
  registry): the hub already holds the runtime, nodes reach it for free
  through `RuntimeEnvironment`, and the accessor pattern (`Logger()`) is the
  established seam. The dispatcher is the one component holding *no* runtime
  — it gets the sink via the optional `ReporterBinder` (§3.2),
  mirroring its existing `LoggerBinder`. `enginert.Default()` stays usable in
  tests because its default sink is the **echo-only producer** (§3.2), never
  a silent no-op — the relocated fault echo and every Info lifecycle record
  survive on all enginert-based runtimes. Rejected: threading a bus parameter
  through every constructor (churn), or hub/dispatcher events emitted by the
  Thresher on their behalf (loses in-context details).
- **Why one canonical event type in `pkg/observability`**: three shapes
  (internal Fact / public Event / a new engine event) would need N×N
  mapping; ADR-012 puts shared public contracts in `pkg/*`. The thin
  `thresher.Event` wrapper survives for API compatibility only.
- **Why `Instance.fail`'s Error log moves into the producer**: FR-4's one
  producer would otherwise double-report the fault (the `Uncaught` echo + the
  existing direct log) — the echo *is* the record; content (level, keys,
  error message) is preserved verbatim.
- **Risk — public interface extension.** Adding `Reporter()` to
  `EngineRuntime` breaks third-party *implementers* of the interface (none
  known; embedders consume it). Flagged in §5; mockrenv regenerates.
- **Risk — emission volume in tests.** Existing log-capture tests assert
  specific records; echoes add records but don't remove any except the
  relocated `fail()` log (its test asserts message+level, which the echo
  preserves).

## 5. Public API surface

Additive: `thresher.Event.Details`, eleven `EventKind` consts,
`Thresher.Observe`, `observability.Fact`/`Reporter`/`LogRedactor`/
`ObservationFilter`, `tasks.ReporterBinder`,
`EngineRuntime.Reporter()` (**the one breaking change for external
`EngineRuntime` implementers** — none known; documented in the changelog at
release). No removals; `InstanceHandle.Observe` keeps its signature and
delivery contract. **One behavior change on an existing kind**: the
`NodeProgress` event's `State` strings change from the 3-value token
projection (`Alive/WaitForEvent/Consumed`) to the real phases (FR-6, T-5) —
permitted by the documented open-vocabulary contract (consumers tolerate
unknown values; no existing test asserts the old strings on stream events),
and the token projection remains available on the handle's token view.

## 6. Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | taxonomy round-trip | every internal kind maps to its public string; unknown-kind fallback intact |
| T-2 | engine-scope registry | `Thresher.Observe` receives engine kinds AND a launched instance's events with `instance_id`; drop counter + cancel semantics match the handle's |
| T-3 | producer echo | a captured logger shows one record per emitted event at the §2.6 level with §2.5 keys; `DataChange` echoes nothing |
| T-4 | emission completeness | a worked **two-process scenario** (correlation needs a message partner: a catcher process + a thrower/message source) exercising a service task w/ worker + retry, a user task, a gateway decision, a boundary-caught error, and message correlation drives the engine-scope observer; the seen (kind, phase) set covers every implemented catalog row — the FR-6 canary. Every ingredient has a landed example to crib from (service-task-worker, usertask, gateway-routing, boundary-events, inter-instance-correlation) |
| T-5 | un-collapsed node phases | the stream distinguishes Entered/Executing/Completed/Failed (was 3-value) |
| T-6 | fault triple | Thrown+Caught on a boundary-caught error (instance completes, no Uncaught); Thrown+Uncaught on an uncaught one |
| T-7 | data change | a variable write emits `DataChange` with the element name and change type, no value, no log record |
| T-8 | visibility | a `LogRedactor` suppresses/redacts echoes; an `ObservationFilter` denies one observer while another still receives — verified on **both scopes** (an engine-scope subscription AND an instance-handle observer); denied ≠ dropped counter; no capability ⇒ pass-through |
| T-9 | hot path | with zero observers and no policy, emission does not build events (the existing guard, extended) |
| T-10 | compatibility | existing handle-observer tests green unchanged |

## 7. Milestones

| # | Scope |
|---|---|
| **M1** | The contracts: `obs.Fact`/`Reporter` + vocabulary/levels table, `EngineRuntime.Reporter()` (+enginert/thresherConfig **echo-only default sink**, mocks regen), `tasks.ReporterBinder`, visibility capability interfaces (asserted, pass-through), `thresher.Event.Details` + kinds. T-1, T-8-partial. |
| **M2** | The producer + engine-scope registry: `Thresher.Observe`, echo writing, filters wired, instance-event relay; `Instance.observe()` replaces `notify` internals (existing kinds only). T-2, T-3, T-9, T-10. |
| **M3** | Instance-layer emissions: `Created`+`Failed`, un-collapsed `NodeProgress`, gateway decisions, correlation, boundary, task states, the fault triple (incl. `fail()` echo relocation). T-5, T-6. |
| **M4** | Engine-layer emissions: engine/hub states, process lifecycle, the job lifecycle in `localdispatcher`. |
| **M5** | `DataChange` via `UpdateCallback` wiring at scope commit. T-7. |
| **M6** | The completeness canary (T-4), examples touch-up if warranted, ADR-013 v.2 flip to Accepted + RU twin catch-up, SAD row update, docs sync. |

## 8. Cross-doc

| Ref | Version | Direction | Role |
|---|---|---|---|
| ADR-013 | v.2 (Draft → Accepted at landing) | SRD → ADR (up) | implemented here |
| ADR-022 | v.1 | up | echo levels, attribute vocabulary, separate-channels |
| ADR-002 | v.2 | up | the extension-accessor pattern |
| ADR-012 | v.1 | up | public contracts in `pkg/*` |
| SRD-018, SRD-040 | (one-shot, by number) | sideways | the v.1 observer landing; the loopState/correlator homes the emissions land in |

## 9. Definition of Done

- [ ] Every §3.4 emission point wired through the single producer; the T-4
      completeness canary covers every implemented catalog row.
- [ ] Engine-scope `Observe` + instance relay live; handle behavior unchanged
      (T-10 green).
- [ ] Echo levels/keys conform to ADR-022 (§2.4/§2.5); `DataChange` has no
      log echo; no log-and-return regressions.
- [ ] Visibility capabilities asserted at wiring; pass-through default
      verified; denied ≠ dropped.
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); full `-race` suite;
      examples smoke.
- [ ] ADR-013 v.2 flipped to Accepted; its RU twin updated (the
      twins-follow-implementation rule); sibling pins refreshed v.1→v.2;
      SAD-001 row updated — all in the landing change-set.
- [ ] §10 filled with milestone SHAs and deltas.

## 10. Implementation summary

> ⚠️ TODO: filled after landing.

## Open questions

None.
