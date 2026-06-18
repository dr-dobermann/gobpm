# SRD-018 — Instance observation: a public handle + lifecycle event stream

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-18 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-013 v.1 Instance Observability & Control](../design/ADR-013-instance-observability.md) |

This SRD lands the **observe** slice of [ADR-013 v.1](../design/ADR-013-instance-observability.md)
(§2.1 observation surface, §2.2 the one lifecycle/token/node channel + async lossy delivery,
§2.4 the standard-named open state vocabulary). It gives a host a public
**`InstanceHandle`** returned from `StartProcess` — `State()`, a token-position
snapshot, read-only data, and `WaitCompletion(ctx)` — plus a single **event
stream** observers subscribe to, into which the instance loop and its tracks
publish lifecycle / token-movement / node-progress events. **Control** (`Cancel`,
`Suspend`/`Resume`) and **engine lifecycle** (`Shutdown`, `UnregisterProcess`)
are ADR-013's *other* slice and are **out of scope** here (a sibling SRD).

## 1. Background & motivation

### 1.1 Current state (verified against the code)

- **The public API is write-only (audit 2.2).** `Thresher.StartProcess(processID string) error`
  (`pkg/thresher/thresher.go:622`) returns only an `error` — no handle, no state, no
  tokens, no completion signal. A host that starts a process can learn nothing more.
- **Running instances are tracked but unreachable.** The Thresher holds
  `instances map[string]instanceReg` (`thresher.go:109`) where
  `instanceReg{stop context.CancelFunc; inst *instance.Instance}` (`thresher.go:98-101`),
  guarded by `sync.Mutex m` (`thresher.go:113`); `launchInstance` stores
  `t.instances[inst.ID()]` but hands nothing back to the caller.
- **The instance already exposes observation internally — lock-free.**
  `Instance.State() State` (`internal/instance/instance.go:501`) reads an
  `atomic.Uint32` (`instance.go:100`); the state enum is
  `Created → Active → Completed` (+ `Terminating → Terminated`)
  (`instance.go:48-67`). `GetTokens() []Token` (`instance.go:720`) and
  `TokenHistory() []TokenPath` (`instance.go:740`) read a copy-on-write
  `tracksSnap atomic.Pointer[[]*track]` (`instance.go:91`) lock-free.
  `RuntimeVar`/`RuntimeVarNames` (`instance.go:800,848`) serve `StartedAt /
  CurrState / TracksCount`. **But `internal/instance` is import-closed to external
  hosts** (Go `internal/` rule + ADR-012), so none of this is reachable from a
  consumer of `pkg/thresher`.
- **The token is a projection, not a stored entity.** `track.Token() Token`
  (`internal/instance/track.go:191`) where `Token{Node flow.Node; State TokenState}`
  (`token.go`); `TokenState` ∈ `Alive / WaitForEvent / Consumed / Withdrawn`. Track
  state (`trackState`, 10 values incl. `TrackWaitForEvent`, `internal/instance/track.go:54-86`)
  projects to it.
- **There is no completion signal and no host-facing observer.** The instance has an
  internal `events chan trackEvent` (`instance.go:87`, track→loop, kinds
  `evFork/evEnded/evAwaiting/evMerged` in `event.go:23-37`) and a `loopDone chan
  struct{}` (`instance.go:96`) the loop closes, plus `lastErr atomic.Pointer[error]`
  (`instance.go:92`) — all unexported. Examples learn a process finished only by
  threading a manual `done := make(chan struct{})` through a service functor (the
  `basic-process` example) or by `time.Sleep` (`internal/instance/instance_test.go`
  `TestMonitoring`).
- **A read-only data reader already exists, public.** `service.DataReader`
  (`pkg/model/service/datareader.go:10-24`): `GetData(name)`, `GetDataByID(id)`,
  `GetSources()`, `List(path)`; the instance's `dataPlane *scope.Scope`
  (`instance.go:88`, `internal/scope/scope.go`) backs it, serialized by the
  data-plane mutex (safe for concurrent external reads).

### 1.2 Problem

A host embedding gobpm is blind after `StartProcess`: it cannot read state, see
where tokens are, read process data, block until the instance finishes, or
follow progress live — it can only read logs. ADR-013 decided the fix; this SRD
implements the **observe** half of it without leaking the `internal/instance`
god-object.

## 2. Decision

Return a public **`thresher.InstanceHandle`** from `StartProcess` (and offer a
by-id lookup). The handle is a **narrow window** over the internal instance — it
holds the `*instance.Instance` unexported and exposes only read-only observation,
never the god-object's mutating surface (ADR-013 §4). It provides:

- **pull** observation — `State()`, `Tokens()` (live positions), `History()` (every
  track's path incl. merged/consumed, with lineage + step timings), `Data()` — over
  the instance's existing lock-free accessors; and `WaitCompletion(ctx)` backed by
  the instance's `loopDone` close + `lastErr`.
- **push** observation — `Observe(...)` registering an observer on **one** event
  stream the instance loop and its tracks publish lifecycle / token-movement /
  node-progress events into, with **async best-effort lossy** delivery (per-observer
  buffered channel + drain goroutine + non-blocking send + drop counter), so a slow
  observer never blocks a track and terminal completion is never dropped.

The public state/token vocabulary is **standard-named and open** (ADR-013 §2.4):
host code must tolerate unknown values (forward-compatible), so the deferred
states (`Failing`/`Paused`/`Compensating`) join additively later.

## 3. Functional requirements

**Observe-by-pull (the handle):**

- **FR-1 — `StartProcess` returns a handle.** `StartProcess(processID string)
  (*InstanceHandle, error)` (was `error` only, `thresher.go:622`). On success the
  handle wraps the launched instance.
- **FR-2 — by-id lookup.** `Thresher.Instance(instanceID string) (*InstanceHandle,
  bool)` returns the handle of a tracked running instance, or `false`.
- **FR-3 — `State()`.** `InstanceHandle.State() InstanceState` maps the internal
  `instance.State` (`instance.go:501`) to the public, standard-named open
  `InstanceState` vocabulary. Lock-free.
- **FR-4 — live token snapshot.** `InstanceHandle.Tokens() []TokenView` projects the
  instance's `GetTokens()` (`instance.go:720`, active tracks only — `Alive` /
  `WaitForEvent`) into a public `TokenView{NodeID, NodeName string; State TokenState}`
  — where execution currently is. Lock-free (copy-on-write snapshot).
- **FR-4a — full token history.** `InstanceHandle.History() []TokenPath` projects the
  instance's `TokenHistory()` (`instance.go:740`) into a public `TokenPath{TrackID,
  ParentID string; Steps []StepVisit; Terminal TokenState}` — **every** track, active
  and finished, each with its lineage (`ParentID` = fork parent) and per-step visit
  timings. This is the "include merged tokens" view; `Tokens()` (FR-4) stays the
  live-active snapshot. **Vocabulary note (grounded):** the token projection collapses
  ended / **merged** / canceled / failed tracks to **`Consumed`** (`token.go`
  `tokenStateFor`) — there is no distinct `Merged` token value; a merged track appears
  as a `Consumed` `TokenPath` whose `Steps` end at the join node, and its `ParentID`
  reveals the fork lineage. Public `TokenState` is therefore the projected set
  `Alive / WaitForEvent / Consumed / Withdrawn`. Lock-free (copy-on-write). No
  single-track variant — the full list is filterable by `TrackID`.
- **FR-5 — read-only data.** `InstanceHandle.Data() service.DataReader` exposes the
  instance's data plane (`instance.go:88`) read-only: process properties + RUNTIME
  vars (`StartedAt/CurrState/TracksCount`). No mutation surface.
- **FR-6 — `WaitCompletion`.** `InstanceHandle.WaitCompletion(ctx context.Context)
  (InstanceState, error)` blocks until the instance reaches a terminal state
  (`Completed`/`Terminated`) or `ctx` is done; returns the terminal state + any
  `lastErr` (`instance.go:92`). Replaces the examples' manual `done` channel.

**Observe-by-stream (the one channel):**

- **FR-7 — observer registration.** `InstanceHandle.Observe(o Observer)
  *Subscription` registers an observer on the instance's single event stream;
  the returned `Subscription` deregisters + drains via `Cancel()` and reports the
  drop count via `Dropped()`. `Observer` is a one-method interface
  (`OnEvent(Event)`).
- **FR-8 — events the stream carries.** `Event{Kind EventKind; InstanceID,
  NodeID, NodeName string; State string; At time.Time}` for: instance lifecycle
  (created/active/completed/terminated), token movement (a token entering/leaving a
  node), and node-execution progress (entered/executing/left) — sourced at the
  instance loop's state transitions and the tracks' `record()` points
  (`track.go:171`). **No payloads** — ids/names/state/timestamps only (masking,
  ADR-010/011).
- **FR-9 — async best-effort lossy delivery.** Per observer: a **buffered channel**
  (size N) drained by **one dedicated goroutine** calling `OnEvent`; the emitter
  sends **non-blocking** (`select { case ch <- ev: default: dropped++ }`). The
  track/loop never blocks on an observer; a slow observer **drops** events and
  exposes a `Dropped() uint64` count; a panicking observer is recovered, not
  propagated. **Terminal completion is never dropped** — it is the `WaitCompletion`
  `loopDone` close (FR-6), independent of the lossy stream.

## 4. Non-functional requirements

- **NFR-1 — no god-object leak.** The handle exposes only read-only observation;
  it never returns `*instance.Instance` nor any mutating method (ADR-013 §4,
  ADR-012 public boundary). `internal/instance` stays internal.
- **NFR-2 — observation off the hot path.** Pull reads use the existing lock-free
  accessors (atomics / copy-on-write snapshot); push emission is a non-blocking
  send — no new mutex on the track execution path (NFR matches the house
  loop-owner + atomics style, `instance.go:91-100`, `track.go:160-164`).
- **NFR-3 — forward-compatible vocabulary.** `InstanceState` / `TokenState` /
  `EventKind` are open sets with a `String()`; consumers must tolerate unknown
  values. Adding a state later is non-breaking (ADR-013 §2.4).
- **NFR-4 — coverage.** Every file created/updated finishes ≥80% (target 100%)
  diff-coverage; `make ci` green (lint incl. fieldalignment, `-race`, govulncheck).

## 5. Path analysis (alternatives)

- **Return the handle from `StartProcess` vs keep `error`-only + a separate
  `Instance(id)` lookup.** Chosen: **both** — return on start (the common path) and
  offer by-id lookup (FR-2) for later retrieval. Rejected error-only: forces every
  host through a second call for the thing it just created.
- **Expose `*instance.Instance` / its exported `State()`,`GetTokens()` directly.**
  Rejected: `internal/instance` is import-closed to hosts and the type is the
  god-object with mutating methods (ADR-013 §4). The narrow public handle is the
  boundary.
- **Reuse `instance.State` as the public type.** Impossible — it lives under
  `internal/`. A public `thresher.InstanceState` (mapped 1:1 today) is required, and
  is what makes the open-vocabulary forward-compat possible.
- **Observer delivery: buffered-channel drop-newest + counter (chosen) vs a
  drop-oldest ring vs synchronous callback vs unbounded queue.** ADR-013 §2.2
  decided drop-newest + counter as the robust default (drop-oldest a later
  refinement behind the same contract); synchronous callback would let a slow
  observer block a track (forbidden); unbounded queue risks memory blowup.
- **Channel-returning `Subscribe() <-chan Event` vs `Observe(Observer)` callback.**
  Chosen `Observe(Observer)` + internal drain goroutine: the engine owns the
  buffer/drop/recover policy (FR-9) rather than exposing a raw channel a host could
  mishandle; a channel adapter can wrap it if wanted.

## 6. API (public surface, `pkg/thresher`)

```go
// InstanceHandle is a read-only window onto one running process instance.
// It never exposes the engine's internal instance object or any mutating method.
type InstanceHandle struct{ /* unexported: inst *instance.Instance */ }

func (h *InstanceHandle) ID() string
func (h *InstanceHandle) State() InstanceState
func (h *InstanceHandle) Tokens() []TokenView   // live active positions (Alive/WaitForEvent)
func (h *InstanceHandle) History() []TokenPath  // every track incl. merged/consumed, lineage + timings
func (h *InstanceHandle) Data() service.DataReader
func (h *InstanceHandle) WaitCompletion(ctx context.Context) (InstanceState, error)
func (h *InstanceHandle) Observe(o Observer) *Subscription

// Subscription is a live observer registration.
type Subscription struct{ /* unexported */ }
func (s *Subscription) Cancel()         // deregister + drain + stop
func (s *Subscription) Dropped() uint64 // events dropped when the observer fell behind

// InstanceState is the standard-named, OPEN lifecycle vocabulary (treat unknown
// values gracefully — the set grows additively, ADR-013 §2.4).
type InstanceState string
const (
	StateCreated     InstanceState = "Created"
	StateActive      InstanceState = "Active"
	StateCompleted   InstanceState = "Completed"
	StateTerminating InstanceState = "Terminating"
	StateTerminated  InstanceState = "Terminated"
)

type TokenView struct {
	NodeID   string
	NodeName string
	State    TokenState // "Alive" | "WaitForEvent" | "Consumed" | "Withdrawn" (open)
}
type TokenState string

// TokenPath is one track's full path — including merged/consumed/canceled tracks.
type TokenPath struct {
	TrackID  string
	ParentID string      // immediate fork parent ("" if root)
	Steps    []StepVisit
	Terminal TokenState  // projected: Consumed for ended/merged/canceled, else the live state
}
type StepVisit struct {
	NodeID   string
	NodeName string
	State    TokenState
	At       time.Time
}

type Observer interface{ OnEvent(Event) }

type Event struct {
	Kind       EventKind
	InstanceID string
	NodeID     string
	NodeName   string
	State      string
	At         time.Time
}
type EventKind string // "InstanceState" | "TokenMoved" | "NodeProgress" (open)

// Engine entry point (changed return):
func (t *Thresher) StartProcess(processID string) (*InstanceHandle, error)
func (t *Thresher) Instance(instanceID string) (*InstanceHandle, bool)
```

Internal support added to `internal/instance` (consumed only by `pkg/thresher`):
`Instance.Done() <-chan struct{}` (returns `loopDone`) and `Instance.Err() error`
(reads `lastErr`) for FR-6; an observer-registration + non-blocking fan-out hook on
the loop/track emit path for FR-7–9 (the loop already centralizes state transitions
at `instance.go:569-654`; tracks already centralize step transitions at
`track.go:171` `record`).

## 7. Test plan

- **`TestStartProcessReturnsHandle`** — `StartProcess` yields a non-nil handle whose
  `ID()` matches the tracked instance; `Instance(id)` finds it (FR-1, FR-2).
- **`TestHandleStateTransitions`** — handle `State()` reports `Active` then
  `Completed` across a run (FR-3).
- **`TestWaitCompletion`** — `WaitCompletion` returns `Completed` for a finishing
  process and respects a cancelled `ctx` (returns ctx err); replaces the `time.Sleep`
  in `TestMonitoring` (FR-6).
- **`TestTokensSnapshot`** — a process parked at a wait shows a `TokenView` with
  `State == WaitForEvent` at the expected node (FR-4).
- **`TestHistoryIncludesMerged`** — a process with a parallel fork+join yields
  `History()` entries for the merged tracks (terminal `Consumed`, `Steps` ending at the
  join, `ParentID` lineage set) plus the survivor — proving the history view includes
  non-active tokens that `Tokens()` omits (FR-4a).
- **`TestHandleDataRead`** — `Data().GetData` reads a process property and a RUNTIME
  var read-only (FR-5).
- **`TestObserverReceivesLifecycleEvents`** — an observer sees `InstanceState`
  active→completed and ≥1 `NodeProgress`/`TokenMoved` event (FR-7, FR-8).
- **`TestSlowObserverDropsNeverBlocks`** — a deliberately blocked observer drops
  events (`Dropped() > 0`) while the process still completes; the track never blocks
  (FR-9).
- **`TestObserverPanicRecovered`** — a panicking `OnEvent` doesn't crash the engine
  or other observers (FR-9).
- **Example update** — `examples/basic-process` uses `WaitCompletion` instead of the
  manual `done` channel (proves the ergonomic win); smoke-run exits 0.

## 8. Cross-document consistency

- **Implements** [ADR-013 v.1](../design/ADR-013-instance-observability.md) §2.1
  (observation surface), §2.2 (one channel + async lossy delivery), §2.4 (open
  vocabulary). §2.3/§2.5 (control + engine lifecycle) are the sibling SRD.
- [ADR-011 v.5 §2.6](../design/ADR-011-process-data-flow.md) — the public
  `service.DataReader` the handle's `Data()` returns.
- [ADR-012 v.1](../design/ADR-012-execution-layering.md) — the public-`pkg`/internal
  boundary the handle respects (no `internal/instance` leak).
- [ADR-001 v.5](../design/ADR-001-execution-model.md) — the instance lifecycle states
  the public vocabulary names, and the single-owner loop the emission rides.
- [ADR-002 v.2](../design/ADR-002-extension-architecture.md) — the §4.7 public-API
  surface this handle joins; the Logger/Metrics extensions observers complement.
- References up/sideways, version-pinned; no downward refs.

## 9. Definition of Done

- FR-1…FR-9 (incl. FR-4a `History()`) wired and exercised by the §7 tests.
- `StartProcess` returns `*InstanceHandle`; all call sites (examples, tests) updated.
- The handle exposes no mutating surface and does not return `*instance.Instance`
  (NFR-1 — grep the public API).
- `make ci` green (tidy, lint incl. fieldalignment, build, `-race`, diff-coverage
  ≥95% on changed lines, govulncheck); touched files ≥80% (target 100%).
- `examples/basic-process` smoke-runs (exit 0) using `WaitCompletion`.
- §10 filled (commits, key files, V-results); status flipped to Accepted; RU twin
  added; linked docs synced.

## 10. Implementation summary

### 10.1 Commits (branch `feat/instance-observability`)

| Stage | Commit | Scope |
|---|---|---|
| doc | `79fdfee` | SRD-018 |
| M1 | `c7c5fe7` | pull handle — `InstanceHandle` (`State`/`Tokens`/`History`/`Data`/`WaitCompletion`), `StartProcess`→handle, `Thresher.Instance` lookup, instance `Done()`/`DataReader()` + a lifetime root reader, call-site + example updates |
| M2 | `85357eb` | event stream — `Observe`/`*Subscription`, async lossy delivery (buffered drain + drop counter + panic-recover), `ObsEvent` fan-out from `setState`+`record`, `TokenState`/`ObsKind` `String()` |

### 10.2 Key files

- `pkg/thresher/handle.go` — `InstanceHandle` + public vocab types.
- `pkg/thresher/observer.go` — `Observer`/`Event`/`EventKind`/`Subscription` + `Observe`.
- `internal/instance/observer.go` — `ObsEvent`/`ObsKind` + `AddObserver`/`removeObserver`/`notify`.
- `internal/instance/instance.go` — `Done()`/`DataReader()`, lifetime reader (in `loadProperties`), `setState` emission; `pkg/thresher/thresher.go` `StartProcess`/`launchInstance`/`Instance`.
- `internal/instance/track.go` — `record()` emission.

### 10.3 V-results

- `make ci` green: lint (incl. fieldalignment, misspell), build, `-race`, **diff-coverage 98.2%** of 222 changed lines (≥95), govulncheck clean.
- All §7 tests green; all 9 examples smoke-run exit 0 (`examples/basic-process` migrated to `WaitCompletion`); README quick-start updated to the handle + a finish-listening/observer example.

### 10.4 Deltas vs the draft

- **Token vocabulary** — the projection collapses ended/merged/canceled to `Consumed` (no distinct `Merged`); `History()` surfaces merged tracks as `Consumed` with their join-node path + lineage (§4.1a wording corrected pre-implementation).
- **`Observe` signature** — returns `*Subscription` (`Cancel()`/`Dropped()`) rather than a bare `cancel func()`, since FR-9 surfaces the drop count (§6 reconciled).
- **Data reader** — became an instance **lifetime** resource built once in `loadProperties` (so `DataReader()` and the handle carry no error path), cleaner than a per-handle build.
- **Carried unchanged**: ~4 unreachable defensive error guards in `launchInstance`/`loadProperties` (a pre-validated snapshot + fresh instance + open scope can't fail them) account for the <100% diff-coverage; accepted as the deterministic 98.2%.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-18 | Ruslan Gabitov | Accepted (landed via M1 `c7c5fe7` + M2 `85357eb`, `make ci` green, diff-coverage 98.2%). Lands the observe slice of ADR-013 v.1 (§2.1/§2.2/§2.4): a public `thresher.InstanceHandle` returned from `StartProcess` (pull: `State`/`Tokens` live + `History` incl. merged/`Data`/`WaitCompletion`) + one async best-effort lossy event stream (`Observe`); standard-named open vocabulary. Control + engine lifecycle (§2.3/§2.5) deferred to a sibling SRD. Code-grounded against `pkg/thresher`, `internal/instance`, `pkg/model/service`. Implements ADR-013 v.1; refs ADR-001 v.5, ADR-002 v.2, ADR-011 v.5, ADR-012 v.1. |
