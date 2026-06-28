# SRD-029 — Boundary Events & Activity Interruption

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-27 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-018 v.1 Boundary Events & Activity Interruption](../design/ADR-018-boundary-events-and-activity-interruption.md) |

This SRD lands **ADR-018** on the [ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md)
single-writer event-processing core: a concrete `BoundaryEvent`, a **per-track cancellable context**
(the interruption signal that is the one piece the codebase lacks), a **loop-owned watcher
subscription** over a guarded activity's execution window, and the **Error catch** path wired onto the
Error event model owned by [ADR-006 v.2 §2.6](../design/ADR-006-events-and-subscriptions.md). It is
the highest-frequency 0.1.0 gap ([SAD-001 v.1 §15.3](../design/SAD-001-vision-and-architecture.md)):
the Timer boundary (timeouts/SLA) and the Error boundary (business-error paths).

## 1. Background & current state (verified against the code)

ADR-018 decided the *mechanism*; this section records what already exists and what is missing, so the
requirements below are deltas, not green-field invention.

### 1.1 The model placeholders are inert

- `pkg/model/flow/events.go:73` — the `BoundaryEvent` interface exists (`EventNode` +
  `BoundTo(ActivityNode) error`) but **no type implements it** (zero implementations, zero callers).
- `pkg/model/activities/activity.go:28` — the `boundaryEvents []flow.EventNode` field exists; its
  getter `BoundaryEvents()` (`activity.go:138`) returns a copy; **the field is never populated**
  (copied as-is in `clone()` at `activity.go:94`, so `nil` until something sets it — and nothing
  does: no setter, no attach option).
- Concrete tasks (`ServiceTask`/`UserTask`/`SendTask`/`ReceiveTask`) embed `*task` → `activity`, so
  all inherit the inert field unchanged.

### 1.2 The event-definition machinery is complete and reusable

- `pkg/model/events/error.go:12` — `ErrorEventDefinition` (carries a `*bpmncommon.Error`) +
  `NewErrorEventDefinition` (validates non-nil error); `Type()` → `flow.TriggerError`. The `Error`
  object carries `errorCode` (ADR-006 v.2 §2.6).
- `TimerEventDefinition` / `MessageEventDefinition` / `SignalEventDefinition` (`events/{timer,message,signal}.go`)
  are complete; each `Type()` returns its trigger constant.
- `pkg/model/events/intermediate_catch.go:103` — `IntermediateCatchEvent.Exec` is the catch-node
  shape we mirror (it returns its outgoing flows; the wait/park is the engine's, not the node's).

### 1.3 The execution core has every part except the interruption signal

- **Catch park** — `internal/instance/track.go` run loop parks a waiting track in a `select` over
  `<-ctx.Done()` and `<-t.evtCh` (the ADR-017 per-track buffered channel; the loop is the sole
  sender — SRD-027). The `stopIt` atomic is polled **only at the run-loop top**, never during a
  node's `Exec`.
- **Step lifecycle** — `track.go` `prepareNodeExecution` → `executeNodeCore` → `finalizeNodeExecution`;
  `executeNodeCore` calls `ne.Exec(ctx, …)` synchronously (`track.go:~750`). There is **no `select`
  around `Exec`** — a context-ignoring operation cannot be force-stopped (Go has no goroutine kill).
- **Single-writer loop** — `internal/instance/instance.go` `loop()` owns `position`/`parked`
  (SRD-028) and dispatches `trackEvent`s (`event.go` kinds: `evFork`/`evDeliver`/`evWaiting`/
  `evMoved`/`evFailed`/…). A track **emits `evMoved` on every move** (SRD-028), so the loop already
  observes when a track arrives on / leaves a node.
- **Fork** — `instance.go` `spawnForks` builds and runs a fresh track per extra outgoing flow
  (reusable for a non-interrupting boundary token).
- **Failure → instance fault** — `track.go` sets `TrackFailed` + `t.lastErr` when `executeNode`
  returns an error; the loop surfaces it as an instance fault (`Instance.fail` cancels `inst.cancel`).
  This is the path an Error boundary must **intercept** before it faults the instance.

### 1.4 The one true gap — there is no *per-track* cancellation

`internal/instance/instance.go:612` derives a **single** `inst.ctx, inst.cancel = context.WithCancel(ctx)`
and every track is run with that same context (`t.run(inst.ctx)`). Cancelling it cancels **all**
tracks. ADR-018 §2.2 requires cancelling **only the guarded track**; the codebase has no per-track
cancel handle. **This SRD introduces it** — it is the linchpin of interrupting boundaries.

## 2. Requirements

### Functional

| ID | Requirement |
|---|---|
| **FR-1** | A concrete **`BoundaryEvent`** type (package `events`) implements `flow.BoundaryEvent`, carries `attachedToRef` (the guarded `flow.ActivityNode`), a `cancelActivity bool` (default `true`), and exactly one trigger `EventDefinition` (Timer / Message / Signal / Error). One parameterized type — **not** four trigger-specific types (§4.1). |
| **FR-2** | `NewBoundaryEvent(attachedTo, def, opts…)` validates **every** public parameter: non-nil `attachedTo`, non-nil `def`, `def.Type() ∈ {Timer,Message,Signal,Error}`, and **rejects `cancelActivity=false` when `def.Type()==TriggerError`** (Error is always interrupting — BPMN §10.5.6). Each rejection is a self-identifying `errs` error. |
| **FR-3** | Attaching a `BoundaryEvent` populates the guarded activity's `boundaryEvents` (via `BoundTo` → the activity's exported `AddBoundaryEvent`), so `activity.BoundaryEvents()` returns it. Attachment enforces **multiplicity** (in `BoundTo`): at most one *interrupting* handler per `(activity, EventDefinition)` declaration; non-interrupting are unbounded (ADR-018 §2.5). |
| **FR-4** | Each track runs under its **own** cancellable context `t.ctx, t.cancel = context.WithCancel(inst.ctx)` (replacing the shared `inst.ctx` hand-off). The loop can cancel **one** track via `t.cancel` without touching siblings; `inst.cancel` still cancels all (instance terminate is unchanged). |
| **FR-5** | When a track **moves onto** an activity that has boundary events (observed via the existing `evMoved`), the loop **registers the activity's boundary watchers** (a catch subscription per trigger, reusing the Timer/Message/Signal waiter + the loop delivery path). When the track **moves off** the activity or the track ends (`evMoved` to the next node / `evEnded` / `evFailed`), the loop **tears the watchers down**. Teardown is loop-owned — no send-on-closed, no leak (inherits ADR-017 discipline). |
| **FR-6** | An **interrupting** boundary fire (loop-applied): the loop (a) `t.cancel()`s the guarded track, (b) `spawnForks`-style spawns a fresh track on the boundary's **outgoing (exception) flow**, (c) tears the activity's watchers down. A **waiting** activity wakes on `<-t.ctx.Done()` and ends `TrackCanceled`; a **running** ctx-honouring `Exec` observes `ctx.Done()` and is interrupted *in its execution phase*; a ctx-ignoring `Exec` runs on but its result is discarded (§3.7). |
| **FR-7** | A **non-interrupting** boundary fire: the loop spawns a parallel continuation track on the boundary's outgoing flow (`spawnForks`), leaves the guarded track running, and **re-arms** the watcher (it may fire again). Permitted for Timer/Message/Signal only; Error never reaches this path (FR-2). |
| **FR-8** | The **completion-vs-fire race** is arbitrated by the single-writer loop with no extra lock: whichever of {host activity completes (its `evMoved`/`evEnded`), boundary fires} the loop applies first wins; the loser is dropped (host completion tears down a still-pending watcher; an applied fire cancels the host). This is the ADR-017 deferred-choice atomicity (SRD-027), reused. |
| **FR-9** | **Error catch.** An activity that raises a typed **`BpmnError`** (carrying an `errorCode`) fails its track; before the loop faults the instance, it matches the failing activity's **Error boundary** declarations by `errorRef`/`errorCode`. **Match** → route as an interrupting fire (FR-6): spawn the exception-flow track, **do not** fault the instance. **No match** → the existing instance-fault path (§1.3) is unchanged. |
| **FR-10** | An **Error End Event** (an `EndEvent` carrying an `ErrorEventDefinition`) resolves, in 0.1.0's single scope, to an **instance fault** carrying the error's `errorCode` (end-in-error) — no boundary guards the process (ADR-018 §2.4, ADR-006 v.2 §2.6 engine note). |

### Non-functional

| ID | Requirement |
|---|---|
| **NFR-1** | **No new race surface, no new lock.** Boundary registration, fire, and host completion are all loop-applied (single writer). Verified by `-race` tests (FR-8). |
| **NFR-2** | **Cooperative-cancellation limitation is documented & contract-tested.** A `ServiceTask` operation must honour `ctx.Done()` to be promptly interruptible; a non-cooperative op degrades to "result abandoned" (control-flow effect still guaranteed). |
| **NFR-3** | **Subscription pressure is bounded** to the host activity's execution window (not the whole instance); teardown is loop-owned. |
| **NFR-4** | **Backward compatibility.** The per-track context (FR-4) changes the context handed to every `Exec`; existing nodes already receive a context and must observe no behavioural change when no boundary is attached. Instance terminate (`inst.cancel`) semantics are preserved. |
| **NFR-5** | **Coverage.** Every file created/updated by this work finishes at ≥95 % diff-coverage (aim 100 %), measured per the project gate (`make ci` / `cover-check`). |

## 3. Models

### 3.1 `events.BoundaryEvent` — the concrete type (`pkg/model/events/boundary.go`, new)

A single type, parameterized by its trigger definition (mirrors `IntermediateCatchEvent`, which is one
type over many definitions):

```go
// BoundaryEvent is a catch event attached to an activity; it fires while the
// activity executes and either interrupts it (cancelActivity) or runs in parallel.
type BoundaryEvent struct {
	attachedTo flow.ActivityNode
	catchEvent          // the shared catch base (id, name, single definition, payload outputs)
	cancelActivity bool
}

// NewBoundaryEvent builds a boundary event and attaches it to host.
// It validates every parameter (ADR-018 §2.5; CLAUDE.md public-API rule).
func NewBoundaryEvent(
	name string,
	host flow.ActivityNode,
	def flow.EventDefinition,
	cancelActivity bool,
	baseOpts ...options.Option,
) (*BoundaryEvent, error)
```

The trigger definition is held by the embedded `catchEvent` (one type over many triggers, like
`IntermediateCatchEvent` — §4.1), so `Definitions()` returns `[]flow.EventDefinition{def}` and, for a
message trigger, the payload output is registered (reusing `catchEvent.addMessagePayloadOutput`).

Validation (FR-2), each a self-identifying `errs` error:

- `host == nil` → `"NewBoundaryEvent: a nil host activity isn't allowed"`.
- `def == nil` → `"NewBoundaryEvent: a nil event definition isn't allowed"`.
- `def.Type()` ∉ {Timer,Message,Signal,Error} → names the rejected trigger.
- `def.Type()==flow.TriggerError && !cancelActivity` → `"NewBoundaryEvent: an Error boundary is always interrupting; cancelActivity=false isn't allowed"`.

`EventClass()` returns the new `flow.BoundaryEventClass` (a boundary is a distinct event position in
BPMN §10.5.6, so it gets its own class value rather than reusing the intermediate class). The interface
`flow.BoundaryEvent` gains `CancelActivity() bool` (needed by the multiplicity check). `BoundTo(host)`
is the interface method; the constructor calls it.

### 3.2 `flow.BoundaryEvent.BoundTo` + activity attachment (`pkg/model/activities/activity.go`)

`BoundTo` records the attachment, enforces multiplicity, and registers the boundary on the host.
Multiplicity lives **here** (in the `events` package) rather than on the activity, because the check
must read the existing boundaries' trigger definitions — which the `events` package owns. `BoundTo`
type-asserts the host to a narrow `boundaryHost` interface (so `flow.ActivityNode` need not widen):

```go
// boundaryHost is the activity-side capability BoundTo needs.
type boundaryHost interface {
	flow.ActivityNode
	BoundaryEvents() []flow.EventNode
	AddBoundaryEvent(flow.BoundaryEvent) error
}

func (b *BoundaryEvent) BoundTo(host flow.ActivityNode) error {
	// nil-host + not-boundary-capable guards (errs) …
	h := host.(boundaryHost)
	if b.cancelActivity {
		// reject a 2nd interrupting handler for the same Event Declaration —
		// keyed by declarationKey(b) = trigger + EventDefinition identity.
	}
	b.attachedTo = host
	return h.AddBoundaryEvent(b)
}
```

On the activity side, add the **exported** setter the field has lacked (exported because `BoundTo`
lives in another package); it simply stores, since multiplicity is already enforced by `BoundTo`:

```go
// AddBoundaryEvent stores the attachment (nil-checked); multiplicity is
// enforced by BoundaryEvent.BoundTo before this is called.
func (a *activity) AddBoundaryEvent(be flow.BoundaryEvent) error
```

(The getter `BoundaryEvents()` is unchanged.) The declaration key is the trigger plus the
EventDefinition identity (`declarationKey`), so two boundaries on **distinct** declarations — e.g.
different `errorRef`, modeled as distinct definitions — are both allowed while a re-attachment of the
**same** declaration as a second interrupting handler is rejected.

### 3.3 Per-track cancellable context (`internal/instance/track.go`, `instance.go`)

The track gains its own cancel handle; the loop owns it (single writer):

```go
type track struct {
	// … existing …
	ctx    context.Context
	cancel context.CancelFunc // NEW — loop cancels THIS track for an interrupting boundary
}
```

Where the loop spawns a track (today `t.run(inst.ctx)`), derive per-track:

```go
t.ctx, t.cancel = context.WithCancel(inst.ctx) // was: shared inst.ctx
go func(t *track) { t.run(t.ctx); inst.emit(trackEvent{kind: trackEndKind(t), track: t}) }(t)
```

The waiting `select` is unchanged in shape — its `case <-ctx.Done()` now fires on a **per-track**
cancel, which is exactly the clean interruption of a waiting activity (FR-6). `inst.cancel` still
cancels the parent of every `t.ctx`, so instance terminate cascades unchanged (NFR-4).

### 3.4 Boundary watcher — a loop-owned subscription keyed to the activity window (`internal/instance/`)

The loop holds an active-watchers map keyed by track (or activity-execution), built from
`activity.BoundaryEvents()` when an `evMoved` lands the track on a guarded activity, and dropped on the
next `evMoved`/`evEnded`/`evFailed` for that track:

```go
// in loop() state (single-writer owned, like position/parked in SRD-028)
watchers map[string][]*boundaryWatch // trackID -> live boundary subscriptions
```

A `boundaryWatch` reuses the existing Timer/Message/Signal waiter registration and the loop's
`evDeliver` path; a fired watcher is applied by the loop as an interrupting (FR-6) or non-interrupting
(FR-7) action against the guarded track. No new delivery mechanism (ADR-018 §2.1).

### 3.5 `BpmnError` — the typed error an activity raises (`pkg/model/events/error.go` or `pkg/errs`)

To let an operation signal a *modeled* business error the loop can match by code (FR-9):

```go
// BpmnError is the error an activity raises to signal a modeled BPMN Error.
// The loop matches its Code against the activity's Error boundary errorRef.
type BpmnError struct {
	Code string // the Error.errorCode
	Err  error  // optional underlying cause
}
func (e *BpmnError) Error() string { … }
func (e *BpmnError) Unwrap() error { return e.Err }
```

The loop extracts it from a failed track via `errors.As(t.lastErr, &be)`; `be.Code` is matched against
each Error `BoundaryEvent`'s `def.(*ErrorEventDefinition).Error().ErrorCode()`. A plain (untyped)
error keeps today's behaviour: instance fault.

### 3.6 Error End Event resolution (`pkg/model/events/end.go`)

An `EndEvent` whose definition is an `ErrorEventDefinition`, on execution, faults the instance with the
error's `errorCode` (FR-10) — there is no process-level catcher in 0.1.0. (Exact integration site
confirmed against `end.go` at the M4 milestone; the requirement is the end-in-error fault.)

### 3.7 The interruption checkpoint — discard vs. fail (`internal/instance/track.go`)

Because the **same** per-track `t.ctx` (§3.3) is handed to `ne.Exec`, a context-honouring operation
returns the moment the loop cancels the track — so a cooperative `ServiceTask` is interrupted
**in its execution phase**, not only at a lifecycle boundary. The one design care is
**disambiguating a cancelled op from a genuinely-failed one**, since both surface as a non-nil error
from `Exec`. The rule: **the context is the authority, and cancellation is checked first**, between the
`Exec` call and the output-commit (`finalizeNodeExecution`), so a discarded result is never committed:

```go
nexts, err := ne.Exec(ctx, env)            // ctx == t.ctx (per-track)

// (1) Cancellation wins over any returned error AND over success.
//     A boundary fire (or instance terminate) cancels t.ctx; a ctx-honouring op
//     returns early, a ctx-ignoring op returns late — either way the result is
//     abandoned BEFORE finalize: no output is committed, no flow is followed.
if ctx.Err() != nil {
    t.updateState(TrackCanceled)
    return                                  // discard — NOT a failure
}
// (2) A genuine execution error -> TrackFailed -> the loop's Error-boundary
//     match / instance-fault path (FR-9).
if err != nil {
    t.lastErr = err
    t.updateState(TrackFailed)
    return
}
// (3) success -> finalizeNodeExecution commits outputs and returns the flows.
```

Two consequences:

- **The check is on `ctx.Err()`, not on the value of `err`.** A cancelled op may return
  `context.Canceled`, a wrapped error, or even `nil` — irrelevant; if the context is done the result
  is abandoned. This is what makes terminating a cooperative op *in the execution phase* safe.
- **The checkpoint is uniform for a boundary cancel and an instance terminate** — both discard and end
  `TrackCanceled`. The *spawn* of the exception flow is the loop's separate action (only when the loop
  applied a boundary fire — FR-6), so the track-side discard never needs to know *why* it was
  cancelled. (The exact return plumbing — a sentinel vs. a state probe in the run loop — is an M2/M3
  detail; the invariant is: cancellation checked first, discard before finalize.)

## 4. Analysis

### 4.1 One `BoundaryEvent` type, not four (decided)

The survey suggested `BoundaryTimerEvent`/`…Message`/`…Signal`/`…Error`. **Rejected** per the project
rule *no speculative universality / prefer composition*: the four would differ only by the
`EventDefinition` they hold — exactly how `IntermediateCatchEvent` already handles many triggers with
one type. One `BoundaryEvent` carrying any allowed `EventDefinition` is the concrete minimum; trigger
behaviour lives in the definition, not in a type hierarchy. (If a trigger later needs genuinely
divergent structure, split *then*.)

### 4.2 Per-track context vs. a per-activity context (decided: per-track)

ADR-018 §2.2 says "cancel only the track executing the guarded activity". Two realizations:

- **Per-activity derived context** wrapping each `Exec` call, cancelled on a boundary fire, leaving
  the track alive to continue. Rejected: the track would then have to *redirect* mid-run onto the
  exception flow, duplicating the spawn logic and adding a mid-`Exec` redirect path the run loop
  doesn't have.
- **Per-track context** (chosen): a boundary fire cancels the *track* and the loop **spawns a fresh
  track** on the exception flow (reusing `spawnForks`). This matches ADR-006 §2.2 ("cancel the track,
  spawn a continuation") and ADR-018 §2.2/§2.3 verbatim, reuses the existing fork + the existing
  `<-ctx.Done()` wait arm, and adds the *single* missing primitive (a per-track cancel) rather than a
  new redirect path. Cost: a guarded activity that is mid-`Exec` and ignores its context wastes that
  goroutine until it returns — the documented cooperative limitation (NFR-2), unavoidable in Go.

### 4.3 Watcher lifecycle on `evMoved` (decided)

SRD-028 already makes the loop observe every track move. Registering watchers on move-onto and
dropping them on move-off reuses that signal exactly — no new "activity started/ended" event is
needed, and the loop (single writer) owns the watcher map alongside `position`/`parked`, so the
race with a fire is arbitrated for free (FR-8).

### 4.4 Error catch intercepts the fault path (decided)

The Error boundary is not a *waiting* catch (an error is not a published trigger that arrives on a
channel); it is a **match against the failing activity** at the moment the track fails. Hooking the
match into the loop's `evFailed` handling — before `Instance.fail` — reuses the existing failure
detection (§1.3) and keeps Error always-interrupting by construction (a matched error spawns the
exception flow; the already-failed track is the "cancelled guarded activity").

### 4.5 Failure-vs-interruption ordering (decided)

A cooperative op returns an error when cancelled, so the post-`Exec` checkpoint (§3.7) must test
cancellation **before** the error — otherwise an interrupted activity is mis-routed as a failure
(`TrackFailed` → Error-boundary match / fault) instead of a clean interruption. Checking `ctx.Err()`
first, treated as authoritative over the returned `err`, is the rule that keeps a boundary-cancelled
activity, an instance-terminated activity, and a genuinely-failed activity on three correct paths. The
upside the shared context buys: a well-behaved operation is stopped *during* execution, not only after
it returns — at no extra cost beyond honouring `ctx` (NFR-2).

## 5. Public API surface

New / changed exported surface:

- `events.BoundaryEvent` (type) + `events.NewBoundaryEvent(host, def, cancelActivity, opts…) (*BoundaryEvent, error)`.
- `events.BpmnError` (type) — the error an operation returns to raise a modeled BPMN Error.
- `flow.BoundaryEvent.BoundTo` gains its first implementation; `activity.addBoundaryEvent` is
  package-internal (not exported).
- No change to the `thresher`/engine public surface; a boundary is attached at model-build time and
  executes transparently.

Worked example (the verification example, §6): a `ServiceTask` with a **Timer boundary** (interrupting)
that fires before a slow operation completes, routing to a "timed-out" path; and a second task that
raises a `BpmnError` caught by an **Error boundary** routing to a "compensate" path.

## 6. Test scenarios

| ID | Scenario | Asserts |
|---|---|---|
| **T-1** | `NewBoundaryEvent` parameter validation | nil host / nil def / bad trigger / Error+`cancelActivity=false` each return the self-identifying `errs` error (FR-2). |
| **T-2** | Attachment + multiplicity | `BoundTo` populates `BoundaryEvents()`; a 2nd interrupting handler for the same declaration is rejected; non-interrupting accepted (FR-3). |
| **T-3** | Per-track cancel isolation (`-race`) | cancelling one track's `t.cancel` ends only that track; siblings run on; `inst.cancel` still ends all (FR-4, NFR-4). |
| **T-4** | Interrupting Timer boundary on a **waiting** activity | a `ReceiveTask` parked on its message is interrupted by a timer; track ends `TrackCanceled`; a fresh track runs the exception flow; instance completes via that flow (FR-5, FR-6). |
| **T-5** | Interrupting boundary on a **ctx-honouring running** `ServiceTask` | the op observes `ctx.Done()` and stops; exception flow taken (FR-6, NFR-2). |
| **T-6** | Interrupting boundary on a **ctx-ignoring running** op | the op runs to completion but its result is abandoned; control already on the exception flow (FR-6, NFR-2). |
| **T-7** | Non-interrupting boundary + re-arm (`-race`) | a non-interrupting timer fires twice while the host runs; two parallel tokens spawned; host completes normally (FR-7). |
| **T-8** | Completion-vs-fire race (`-race`, stress) | host completes and boundary fires near-simultaneously; exactly one outcome applied; no double-spawn, no leak (FR-8, NFR-1). |
| **T-9** | Error boundary catch by `errorCode` | a `ServiceTask` raises `BpmnError{Code:"E1"}`; an Error boundary with matching `errorRef` routes to the exception flow; instance does **not** fault (FR-9). |
| **T-10** | Error boundary no-match → fault | `BpmnError{Code:"E2"}` with no matching boundary → instance faults (existing path, FR-9). |
| **T-11** | Error End Event → instance fault | a process ending at an Error End Event faults the instance carrying the `errorCode` (FR-10). |
| **T-12** | Watcher teardown / no leak (`-race`) | normal completion tears down a pending boundary watcher; no goroutine/subscription leak (FR-5, NFR-3). |
| **T-13** | Runnable example smoke | `examples/boundary-events/` builds **and runs** to exit 0, printing the timed-out + compensate paths. |
| **T-14** | Disambiguation: interrupted cooperative op returning an error | an op that returns `context.Canceled` (or a wrapped error) on cancel ends the track `TrackCanceled`, **not** `TrackFailed`; no instance fault; exception flow taken (§3.7, §4.5). |

## 7. Milestones

| # | Milestone | FRs | Notes |
|---|---|---|---|
| **M1** | Model — `BoundaryEvent` type + `NewBoundaryEvent` + `BoundTo`/`addBoundaryEvent` + multiplicity | FR-1,2,3 | Pure model package; no runtime. Tests T-1,T-2. |
| **M2** | Per-track cancellable context | FR-4 | The linchpin primitive; isolated, race-tested. T-3. |
| **M3** | Loop-owned watcher subscription + interrupting/non-interrupting firing + re-arm | FR-5,6,7,8 | Reuses `evMoved`, waiters, `spawnForks`. T-4..T-8, T-12. |
| **M4** | Error path — `BpmnError`, boundary match in `evFailed`, Error End Event fault | FR-9,10 | Wires onto ADR-006 v.2 §2.6. T-9,T-10,T-11. |
| **M5** | Verification — runnable example + `-race` sweep | all | `examples/boundary-events/`. T-13. |

Each milestone is one commit, tests included; coverage gate per milestone (NFR-5).

## 8. Cross-doc

| Ref | Pin | Direction |
|---|---|---|
| ADR-018 Boundary Events & Activity Interruption | v.1 | SRD → ADR (Implements) ✓ |
| ADR-006 Events & Subscriptions §2.6 (Error event model), §2.2 | v.2 | SRD → ADR ✓ |
| ADR-017 Channel-based event processing | v.1 | SRD → ADR (delivery core) ✓ |
| ADR-001 Execution Model §4.6 (cancellation) | v.6 | SRD → ADR ✓ |
| ADR-005 Gateways & Joins (fork) | v.4 | SRD → ADR ✓ |
| SAD-001 §15.3 (0.1.0 scope) | v.1 | SRD → SAD ✓ |
| SRD-027 (inbound delivery), SRD-028 (loop-owned positions) | — | SRD → SRD (number only) ✓ |

Direction is up/sideways only. The instance-fault path this SRD intercepts is grounded in code
(`track.go` `TrackFailed`, `instance.go` `Instance.fail`), not by a downward FIX reference.

## 9. Definition of Done

- [x] FR-1..FR-10 implemented and wired (model + runtime + error path).
- [x] NFR-1 (`-race` clean), NFR-2 (limitation documented + contract test), NFR-3 (no leak),
      NFR-4 (no behaviour change when no boundary attached), NFR-5 (diff-coverage ≥95 %).
- [x] T-1..T-14 green; `examples/boundary-events/` builds **and runs** (exit 0); its binary gitignored.
- [x] `make ci` green (tidy, lint, build, `-race`, cover-check, govulncheck).
- [x] §8 cross-doc pins verified directional + present.
- [x] §10 filled (files/lines, V-results, milestone SHAs); status flipped Draft → Accepted at landing.

## 10. Implementation summary

Landed on `feat/adr-018-boundary-events` in five milestones (each one commit, tests included).

### Files

| Area | File | What |
|---|---|---|
| Model | `pkg/model/events/boundary.go` | `BoundaryEvent` type (one class, not four), `NewBoundaryEvent` (validates every public param), `BoundTo`, `declarationKey` |
| Model | `pkg/model/events/bpmn_error.go` | `BpmnError{Code, Err}` typed error: `NewBpmnError` rejects empty code, `Error()` self-identifies, `Unwrap()` |
| Model | `pkg/model/events/end.go` | Error End Event — `Exec` emits non-error defs, then faults with the `BpmnError` carrying the Error's code |
| Model | `pkg/model/activities/activity.go` | `AddBoundaryEvent` + `BoundaryEvents()` host attachment/multiplicity |
| Runtime | `internal/instance/boundary_watch.go` | `boundaryWatch` (loop-owned EventProcessor), `armBoundaries`/`disarmBoundaries`, `fireBoundary` (interrupting cancels the track), `matchErrorBoundary` (fault-path catch) |
| Runtime | `internal/instance/instance.go` | loop-owned `watchers` map; `evBoundary` apply; arm-on-`evMoved`/disarm-on-`evEnded`/`evFailed`; `applyFailed` Error-boundary interception |
| Runtime | `internal/instance/track.go` | per-track cancellable `ctx`/`cancel`; §3.7 interruption checkpoint (`ctx.Err()` before finalize) → `discardOrFail` (TrackCanceled, not TrackFailed) |
| Runtime | `internal/instance/event.go` | `evBoundary` trackEventKind |
| Example | `examples/boundary-events/` | interrupting timer boundary as a timeout on a ~4s payment task |

### Verification (V-results)

- `make ci` green: golangci-lint 0 findings, `-race` tests pass, govulncheck clean.
- Diff-coverage **98.5 %** on touched files (gate ≥95 %), measured cumulatively across M1–M5.
- T-1..T-14 green (T-4 e2e exercises a signal boundary; the timer-boundary path is covered end-to-end
  by `examples/boundary-events/`. T-8 is a deterministic race-guard test, not a stress loop).
- `examples/boundary-events/` runs to completion (exit 0); its binary is gitignored.

### Milestone commits

| # | SHA | Milestone |
|---|---|---|
| M1 | `36d9331` | Model — `BoundaryEvent` + `BoundTo` + multiplicity |
| M2 | `07de552` | Per-track cancellable context |
| M3a | `4e2171c` | Watcher scaffold + `evBoundary` |
| — | `4329bb3` | Coverage backfill (model error paths) |
| M3b | `52b6204` | Arm/disarm + interrupting/non-interrupting firing |
| M3c | `4093d18`, `56d69ac` | Interruption of a running activity + §3.7 checkpoint |
| M4 | `842f176` | Error path — `BpmnError`, `evFailed` catch, Error End Event |
| M5 | `174a782` | Runnable example + `-race` sweep |

## Open questions

None.
