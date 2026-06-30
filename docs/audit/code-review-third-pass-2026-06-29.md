# GoBPM Code Review — Third Pass (Novel Findings)

**Date**: 2026-06-29
**Author**: Claude (Opus 4.8), multi-agent review
**Scope**: Whole tree. This pass deliberately excludes everything already in
`docs/code-review-2025.md` and `docs/code-review-codex-second-pass-2026-06-29.md`
and reports only defects those two passes did **not** find.

## Method

Eight finders swept distinct subsystems (data/expr/scope, gateways/flow,
instance/track/snapshot, events/waiters, thresher/registry, activities/service,
support packages, build/CI). Each was handed the ~50 already-documented issues
as an exclusion list. Every candidate was then re-checked by an independent
adversarial verifier that read the actual source, reproduced the defect where
possible, corrected line numbers, and re-rated severity. 36 candidates → **31
confirmed novel findings** (5 refuted or duplicate).

A recurring theme distinguishes this pass from the first two: **method/identifier
mismatches that compile but silently no-op** (`GetItemList` vs `GetItemsList`,
`CloneEvent` vs `CloneEventDefinition`) and **doc/comment claims that contradict
the code** (Clone "immutable" properties, idempotent `RegisterProcess`,
forward-only clock). Several findings are *latent* — real defects on code paths
not yet wired into production (persistence, observability, the runtime server).
Each finding carries a **Status** line saying whether it bites today.

## Priority Matrix

| # | Finding | Severity | Status |
|---|---------|----------|--------|
| 1 | `Snapshot.Clone` shares mutable process Properties across all instances | 🔴 P1 | Active (data race) |
| 2 | `GExpression.Evaluate` nil-derefs a `(nil, nil)` user result | 🟠 P2 | Active |
| 3 | `SignalEventDefinition.GetItemList` misnamed — never overrides interface | 🟠 P2 | Active |
| 4 | `EventDefCloner` never satisfied (`CloneEvent` ≠ `CloneEventDefinition`) | 🟠 P2 | Active |
| 5 | `RegisterProcess` godoc claims idempotent dedup; code mints a new version | 🟠 P2 | Active (doc) |
| 6 | Parallel-start event gateway without a correlation key double-instantiates | 🟠 P2 | Active (misconfig) |
| 7 | Register/Unregister TOCTOU can orphan a live starter | 🟠 P2 | Active (race) |
| 8 | `Run` stays `Started` but returns error on starter-registration failure | 🟠 P2 | Active |
| 9 | `WithRenderer` rejects a second renderer of the same implementation type | 🟠 P2 | Active |
| 10 | `UserTask.Exec` ignores context — goroutine leak on instance teardown | 🟠 P2 | Active |
| 11 | `memrepo` can evict an Active instance after a terminal→Active re-save | 🟠 P2 | Latent (persistence unwired) |
| 12 | `bpmncommon.Error.Structure()` nil-derefs; `NewError` accepts nil structure | 🟠 P2 | Active |
| 13 | `govulncheck` scans only the root module, not runtime/adapters/examples | 🟠 P2 | Active (CI gap) |
| 14 | `scope.namesFrom` omits a `/`-keyed root scope from enumeration | 🟡 P3 | Latent |
| 15 | `Array.Insert` off-by-one: cannot insert at `index == len` | 🟡 P3 | Active |
| 16 | `Array.Clone` resets the iteration cursor to 0 | 🟡 P3 | Latent (no cursor consumers) |
| 17 | `Array.Delete`/`DeleteT` skip the notification when emptying the array | 🟡 P3 | Latent (no callback consumers) |
| 18 | Unspecified-direction gateway validation doesn't enforce merge-or-split | 🟡 P3 | Active (validation gap) |
| 19 | Default-flow routing relies on pointer identity `UpdateDefaultFlow` doesn't guarantee | 🟡 P3 | Latent (fragility) |
| 20 | `errs.M` format verbs with no args in `track.go` unregister path | 🟡 P3 | Active (diagnostic) |
| 21 | Cyclic timer fires N+1 times for a cycle count of N (off-by-one) | 🟡 P3 | Active (caller compensates) |
| 22 | Starter reconcile aborts mid-loop, leaving the hub partially wired | 🟡 P3 | Active (rare) |
| 23 | `DeriveKey` accepts a present-but-nil value as a valid partial correlation key | 🟡 P3 | Active (narrow) |
| 24 | `clocktest.Advance` allows moving the clock backwards; `Set` forbids it | 🟡 P3 | Test-only |
| 25 | `Message.Clone` drops `BaseElement` documentation | 🟡 P3 | Active (fidelity) |
| 26 | `memmetrics.seriesKey` uses `%v`, so distinct attribute sets collide | 🟡 P3 | Latent (no emit sites) |
| 27 | `memtrace.liveSpan` mutates span state without synchronization | 🟡 P3 | Latent (tracer unwired) |
| 28 | `test`/`test-all`/`test_race` lack `-count=1`; cached `-race` masks flakes | 🟡 P3 | Active (CI) |
| 29 | `.golangci.yml` `tests: false` disables all govet analyzers on `_test.go` | 🟡 P3 | Active (lint gap) |
| 30 | `depguard` `core-no-runtime-no-adapters` will block the runtime server binary | 🟡 P3 | Latent (server stub) |
| 31 | `make clear` (`rm ./bin/*`) errors on a clean checkout | 🟡 P3 | Active (DX) |

---

## 1. Critical (P1)

### 1.1 `Snapshot.Clone` shares mutable process Properties across all instances

**Location**: `internal/instance/snapshot/snapshot.go:171` (Clone) and
`internal/instance/instance.go:543-548` (loadProperties)

`Snapshot.Clone` copies the Properties slice **by reference**:

```go
clone := Snapshot{
    ...
    Properties: s.Properties,   // shared, not cloned
}
```

The Clone doc-comment even asserts this is safe — *"The immutable header —
process id/name and properties — is shared"* — but properties are **not
immutable**. `loadProperties` commits those same `*data.Property` pointers into
each instance's own data plane without copying:

```go
for _, p := range inst.s.Properties { dd = append(dd, p) }
return inst.dataPlane.Commit(inst.rootScope, dd...)
```

`scope.Commit` stores the pointer as-is (`vv[names[i]] = d`, scope.go:243). A
`*data.Property` embeds a mutable `ItemAwareElement` that execution updates **in
place** (e.g. task output write-back `task.go:295-300`, data association
`association.go:176`). So **N concurrent instances of the same registered
process write to the same property objects**.

**Impact**: Breaks the per-instance isolation invariant that the snapshot/Clone
mechanism exists to guarantee (ADR-009). A node in instance A that writes a
process property mutates the value/state seen by instance B running the same
process — a genuine data race (no synchronization across the instance goroutines)
plus silent cross-instance corruption, also reproducible across *sequential*
runs since properties are never reset. `clone_race_test` only exercises node-graph
isolation, never property-data isolation, so this is uncovered.

**Status**: **Active.** This is the headline finding of the pass.

**Fix**: Deep-clone each property per instance. `ItemAwareElement.Clone` already
exists — add a `Property.Clone` (or clone in `loadProperties` before `Commit`)
so each instance owns private property objects, exactly as nodes are cloned.
Then fix the Clone doc-comment, which is actively misleading.

---

## 2. High (P2)

### 2.1 `GExpression.Evaluate` nil-derefs a `(nil, nil)` user result

**Location**: `pkg/model/data/goexpr/goexpr.go:126-141`

```go
res, err := ge.gexFunc(ctx, ge.src)
if err != nil { ... return }
... ge.result.Structure().Update(ctx, res.Get(ctx))   // res may be nil
```

`GExpFunc` is `func(ctx, ds) (data.Value, error)`. A user evaluation function
that legitimately returns `(nil, nil)` makes `res` a nil interface and
`res.Get(ctx)` panics. There is no `if res == nil` guard.

**Impact**: goexpr is the documented reference `FormalExpression` and the default
expression engine delegates to it; the path is reachable from gateway condition
eval, data-association transformation, correlation, and timer expressions. A
caller mistake crashes the evaluating goroutine instead of producing a classified
error. **Status**: Active (requires a user-supplied function returning nil/nil).

**Fix**: After the error check, `if res == nil { return errs.New(errs.M("evaluation
returned a nil value"), errs.C(errorClass, errs.OperationFailed)) }` before `Get`.

### 2.2 `SignalEventDefinition.GetItemList` is misnamed — it never overrides the interface

**Location**: `pkg/model/events/signal.go:128`

```go
func (sed *SignalEventDefinition) GetItemList() []*data.ItemDefinition { ... }
//                                ^^^^^^^^^^^ singular
```

`flow.EventDefinition` (flow/events.go:63) requires the **plural**
`GetItemsList()`. The embedded `definition` provides a `GetItemsList()` that
returns an **empty** list. Because the override is misspelled, interface dispatch
resolves to the embedded empty-returning method, and the singular method is dead
code (zero callers). All siblings — `MessageEventDefinition`,
`ErrorEventDefinition`, `EscalationEventDefinition` — spell it correctly.

**Impact**: A `SignalEventDefinition` always reports zero data items, so signal
payloads are excluded from the readiness check siblings perform, and from the
item-collection in the throw path (`event.go:605`). An intended override silently
does not override — and nothing catches it because the misnamed method just
compiles as an extra unused method. **Status**: Active.

**Fix**: Rename to `GetItemsList`. Add a test asserting it returns the signal's
structure.

### 2.3 `EventDefCloner` is never satisfied — thrown error/escalation payload is discarded

**Location**: `pkg/model/events/event.go:624`

`flow.EventDefCloner` (flow/events.go:68) requires
`CloneEventDefinition(data []data.Data) (EventDefinition, error)`. But the three
implementers name the method `CloneEvent` — `message.go:104`, `error.go:78`,
`escalation.go:157`. There is **no** `CloneEventDefinition` anywhere and no
`var _ flow.EventDefCloner = ...` compile-time assertion. So at:

```go
if c, ok := ed.(flow.EventDefCloner); ok { ced, err = c.CloneEventDefinition(idd) }
```

the assertion is **always false**, `ced` stays `ed`, and the data items `idd`
gathered from scope are never woven into the propagated definition.

**Impact**: `emitEvent` gathers a throw's data from the running scope and is
supposed to clone-with-data before `PropagateEvent`. Because the interface is
never satisfied, that step is dead — error and escalation throws propagate
**without their payload** (only `*MessageEventDefinition` is diverted to msgflow
earlier and uses its `Message()` directly). BPMN §10.4.2 data propagation is
broken. The type assertion is legal Go regardless of whether any type satisfies
it, so the compiler never flags the mismatch. **Status**: Active.

**Fix**: Rename the three `CloneEvent` methods to `CloneEventDefinition` and add
`var _ flow.EventDefCloner = (*MessageEventDefinition)(nil)` (etc.) so the gap is
caught at build time.

### 2.4 `RegisterProcess` godoc claims idempotent dedup; the code mints a new version every call

**Location**: `pkg/thresher/thresher.go:496-497`

Header godoc: *"Re-registering an already-registered process is idempotent (the
first registration wins)."* The body has **no dedup**: it unconditionally calls
`snapshot.New(p)` then `appendVersionLocked`, which does `t.nextVersion[...]++`
and appends. The inline comment 30 lines down says the opposite (*"Re-registering
the same key mints a NEW version rather than a silent no-op"*), and
`TestLatestSupersedesAutoStart` asserts `reg2.Version() == 2`.

**Impact**: A caller trusting the godoc (e.g. idempotent boot-time wiring that
registers on every start) instead grows the registry unboundedly, each call
superseding the prior latest's auto-start starters. **Status**: Active
(documentation defect — the self-contradiction is the bug).

**Fix**: Correct the godoc to describe latest-supersedes semantics, or add real
dedup before `appendVersionLocked` if idempotency is actually intended.

### 2.5 Parallel-start event gateway without a correlation key double-instantiates and never completes

**Location**: `pkg/thresher/instance_starter.go:152-158`,
`pkg/thresher/thresher.go:705-711`, `pkg/model/gateways/event_based.go:493-514`

A `ParallelEvents` instantiating gate keeps `startNode = gate` and
`corrKey = correlationKeyOf(gate)`. If the gate declares no `CorrelationKey`,
`correlationKeyOf` returns nil, `deriveKey` yields `""`, and `resolveAndLaunch`
takes the no-dedup branch that **always** launches a new instance. Starters are
persistent subscriptions, so the gate fires once per arm message — each spawns a
fresh instance that pre-fires its own arm and waits forever for the others, whose
messages already went to sibling instances. `validateStartGate` does **not**
require a correlation key for `ParallelEvents`, so this gate passes
`Process.Validate`.

**Impact**: A parallel-start gateway must produce **one** instance completing when
**all** arms fire (SRD-025 §4.3); without a correlation key it produces one stuck
instance per arm. Every test supplies `WithCorrelationKey`, so the uncorrelated
case is untested. **Status**: Active for this (arguably misuse) configuration.

**Fix**: Reject a `ParallelEvents` instantiating gate with no `CorrelationKey` at
validation time, or key the dedup on the gate id when no correlation key is present.

### 2.6 Register/Unregister TOCTOU can orphan a live starter

**Location**: `pkg/thresher/thresher.go:543-562, 600-610`; helpers `locked.go:23-93`

`RegisterProcess` appends the new version under `t.m` then **releases the lock
before** the hub work `registerStarters(starters)`. A concurrent
`UnregisterVersion` on that same new registration can run in the window:
`removeVersionLocked` drops it from the registry (it's already observable via
`Registrations()`), but `unregisterStarters` removes nothing because the starters
aren't on the hub yet. `RegisterProcess` then subscribes starters for a
registration no longer in the registry.

**Impact**: The orphaned persistent starter fires forever on matching messages,
instantiating an unregistered, unreachable version — a leak of live behavior with
no handle to stop it short of `Shutdown`. **Status**: Active but narrow (requires
concurrent register + unregister-of-the-same-registration).

**Fix**: Make the registry mutation and the hub subscription atomic w.r.t. removal
(mark the registration hub-pending under the lock), or reconcile the hub from the
authoritative registry state inside one per-key critical section.

### 2.7 `Run` stays `Started` but returns an error on starter-registration failure

**Location**: `pkg/thresher/thresher.go:328-333`

After `t.state.Store(Started)`, `Run` calls `registerAllStarters()` and on failure
returns an error **without** rolling back state or tearing down the now-live hub
goroutine. The hub-Start failure path (lines 308-316) deliberately rolls back to
`NotStarted` to stay re-runnable (`TestRunRollsBackOnHubStartFailure`); the
starter path has no equivalent.

**Impact**: The caller gets an error implying startup failed, but the engine is
`Started` with the hub live and some-but-not-all starters registered. A retry
`Run()` is rejected (CAS requires `NotStarted`), and `Shutdown` tears down a
half-wired engine. **Status**: Active (uncommon — a starter registration must fail
at startup).

**Fix**: On failure, roll back (Shutdown the hub, store `NotStarted`) to match the
re-runnable contract, or treat partial registration as non-fatal and log.

### 2.8 `WithRenderer` rejects a second renderer of the same implementation type

**Location**: `pkg/model/activities/user_task_options.go:66-73`

```go
slices.ContainsFunc(cfg.renderers, func(r2c hi.Renderer) bool {
    return r2c.ID() == r.ID() || r2c.Implementation() == r.Implementation()
})
```

`Implementation()` is a fixed **type marker** (e.g. every console renderer returns
`"##consInputRender"`), so two distinct console renderers with different IDs and
prompts collide on the second clause and the second is rejected — with a
misleading `"duplicate renderer: #<id>"` message even though the IDs differ.

**Impact**: BPMN permits a User Task to carry multiple renderings of any kind (the
engine's own `rendering.go` doc-comment repeats this). The `Implementation()`
clause silently forbids the common case. **Status**: Active.

**Fix**: Dedup by ID only — drop the `|| r2c.Implementation() == r.Implementation()`
term.

### 2.9 `UserTask.Exec` ignores context — goroutine leak on instance teardown

**Location**: `pkg/model/activities/user_task.go:170-209`

```go
func (ut *UserTask) Exec(_ context.Context, re renv.RuntimeEnvironment) (...) {
    rCh, err := rr.Register(ut)
    for d := range rCh { dd = append(dd, d) }   // blocks until rCh closes
}
```

The context is discarded and the loop blocks until the external registrator closes
`rCh`. There is no `select { ... case <-ctx.Done(): }`. The sibling `ServiceTask`
and `ReceiveTask` both propagate `ctx`.

**Impact**: If the instance is cancelled/times out, the UserTask can't abort — the
track goroutine stays parked on `rCh`, leaking the goroutine and pinning its
frame/scope. The track's own `discardOrFail` cancellation path
(`track.go:568-579`) is unreachable because execution never returns to the
`ctx.Done()` check. A never-completed human task can never be reclaimed.
**Status**: Active (any pending UserTask at cancellation time).

**Fix**: Take the context and `select` on `ctx.Done()` alongside `rCh`; have the
registrator unregister/close on cancellation.

### 2.10 `memrepo` can evict an Active instance after a terminal→Active re-save

**Location**: `pkg/repository/memrepo/memrepo.go:59-74`

`Save` only tracks IDs on the terminal branch and **never untracks**. If an ID is
first saved terminal (added to `termSet`/`termOrder`) then re-saved `Active`, the
record becomes Active but stays in the terminal eviction set;
`evictTerminalLocked` then `delete(r.records, oldest)` can evict the now-Active
record. Reproduced with a probe test (cap=1): `Save(x,Completed)`,
`Save(x,Active)`, `Save(y,Completed)` evicts the live `x`.

**Impact**: Violates the package contract (Active instances retained
unconditionally; only terminal records capped). A terminal→Active transition
(status correction, ID reuse on replay) silently makes a live instance
evictable, losing it from `Load`/`ListInFlight`. **Status**: **Latent** — `Save`
has no production callers yet (persistence is future work, ADR-009), and the
normal lifecycle is monotonic Active→terminal.

**Fix**: On the non-terminal path, untrack the ID (mirror `Delete`):
`else if _, ok := r.termSet[rec.ID]; ok { delete(r.termSet, rec.ID);
r.termOrder = removeFirst(r.termOrder, rec.ID) }`.

### 2.11 `bpmncommon.Error.Structure()` nil-derefs a nil structure that `NewError` accepts

**Location**: `pkg/model/bpmncommon/error.go:58-62`

```go
func (e *Error) Structure() *data.ItemDefinition { str := *e.structure; return &str }
```

`NewError` stores `structure: str` with no nil check, so a nil `ItemDefinition`
produces a valid Error, but `Structure()` then dereferences nil and panics
(reproduced empirically). Worse, the would-be guards in `events/error.go:57,69`
are written `if eed.err.Structure() == nil` — the guard call itself panics.
`boundary_test.go:46` even constructs `NewError(..., nil)`, proving nil is a used
input.

**Impact**: A BPMN error event legitimately may carry no `ItemDefinition`. Routing
such an error through `GetItemsList`/`CheckItemDefinition`/`CloneEvent` (reachable
from `instance.go:302,427`, `instance_starter.go:80`, `waiters/message.go:355`)
crashes the engine at runtime. **Status**: Active (error events without an item
definition).

**Fix**: `if e.structure == nil { return nil }` in `Structure()`, and have callers
handle nil — or have `NewError` reject nil if a structure is required.

### 2.12 `govulncheck` scans only the root module

**Location**: `Makefile:156-159`; `.github/workflows/check.yml:70-71`; `CLAUDE.md:67`

`vuln` runs `govulncheck ./...` once from the repo root. Standard Go prunes nested
modules from `./...`, so the separate modules `runtime`, `adapters/sqlite`, and
every `examples/*` (each its own `go.mod`) are never scanned — yet `CLAUDE.md`
documents `make ci` as running "govulncheck, across all modules" and every other
monorepo target loops over `$(MODULES)`.

**Impact**: A vulnerable dependency in `runtime/` or `adapters/sqlite/` (the
modules slated to gain real third-party deps — SQLite driver, HTTP/gRPC — per
ADR-004) passes CI undetected. The "all modules / no drift" guarantee is violated
for the one check most dependent on per-module dependency graphs. **Status**:
Active (low blast radius today; material as those modules gain deps).

**Fix**: Loop `govulncheck` over `$(MODULES)` like the other multi-module targets,
or scope it explicitly and correct the `CLAUDE.md` wording.

---

## 3. Medium / Low (P3)

### 3.1 `scope.namesFrom` omits a `/`-keyed root scope (`scope.go:159-181`)
The ancestor scan matches `strings.HasPrefix(prefix, path.String()+PathSeparator)`.
For the root scope, `path.String()+PathSeparator` is `"//"`, which is never a
prefix of a non-root descendant, so a scope keyed exactly `"/"` is dropped from
enumeration. **Status: Latent** — production roots every plane at `"/ProcessName"`
(never bare `"/"`), and all `NewFrame` sites attach at the plane root, so no live
`List` caller hits multi-level ancestry; name/ID resolution (`getData`'s
`DropTail` walk) handles root correctly. Real but unreachable today. **Fix**:
collect ancestors via the same `DropTail` walk `getData` uses, or special-case root.

### 3.2 `Array.Insert` off-by-one — cannot insert at `index == len` (`values/array.go:286`)
`Insert` reuses `checkIndex`, whose bound is `index > len-1`, so inserting at the
end errors (`Insert(9, 3)` on a 3-element array → OUT_OF_RANGE, reproduced). The
insertion bound is `[0, len]`. **Status: Active** (mitigated: `Add` appends to the
end, so end-append is reachable another way). **Fix**: dedicated
`idx < 0 || idx > len` bound for `Insert` (and bypass `checkForEmpty` to allow
insert-at-0 into an empty array).

### 3.3 `Array.Clone` resets the iteration cursor to 0 (`values/array.go:97-103`)
`Clone` routes through `NewArray(a.elements...)`, which forces `index = 0`,
discarding the source cursor (`GoTo(2)` then `Clone()` → clone `Index()==0`,
reproduced). On the snapshot→instance Clone path. **Status: Latent** — no
production code consumes the array cursor (`Index`/`GoTo`/`Next`); multi-instance
is a stored boolean, not cursor iteration. **Fix**: carry `a.index` into the clone
under the source lock instead of going through `NewArray`.

### 3.4 `Array.Delete`/`DeleteT` skip the notification when emptying the array (`values/array.go:312-324`)
The `len == 0` branch returns before `a.notify(data.ValueDeleted, ...)`, so
deleting the last element fires no `UpdateCallback` even though the collection
state changed (index → -1). Same skip in `array_t.go:162-165`. **Status: Latent**
— no production code registers array update callbacks. (The finder's secondary
`any`-vs-`int` index-type claim was **refuted**: the value is already dynamically
`int`.) **Fix**: emit the notification before the empty-collection early return.

### 3.5 Unspecified-direction gateway validation doesn't enforce merge-or-split (`gateways/gateway.go:295`)
The `Unspecified` branch only requires `inCount >= 1 && outCount >= 1`, so a
1-in/1-out gateway passes — yet the BPMN rule quoted in the same file's header
(lines 35-46) says a gateway MUST merge or split (multiple in **or** multiple
out). `Unspecified` is the default direction, so this is the common path.
**Status: Active** (validation gap; harmless pass-through at runtime). **Fix**:
require `inCount >= 2 || outCount >= 2`, or document an explicit engine relaxation.

### 3.6 Default-flow routing relies on a pointer identity `UpdateDefaultFlow` doesn't guarantee (`gateways/gateway.go:178`)
Routing excludes the default flow by `of == g.defaultFlow` (exclusive.go:87,
gateway.go:255), but `UpdateDefaultFlow` stores the **caller's** pointer `f`, not
the matching member object `sf` it located by ID. Both current callers happen to
pass the exact member object, so it works today. **Status: Latent fragility** — a
future caller passing an ID-matching-but-different pointer silently misroutes with
no validation error. **Fix**: store `g.defaultFlow = sf`, or compare by ID in
routing.

### 3.7 `errs.M` format verbs with no args in the track unregister path (`internal/instance/track.go:896`)
`errs.New(errs.M("node %q[%s] doesn't implement flow.EventNode interface"))` — two
verbs, zero args → renders `node %!q(MISSING)[%!s(MISSING)] ...`. `go vet` doesn't
catch it (`errs.M` isn't a recognized Printf wrapper). Distinct site from the
`event.go` `UploadData` case already documented; also drops the `errs.C` class
every neighbor carries. **Status: Active** (defensive branch, malformed
diagnostic). **Fix**: pass `n.Name(), n.ID()` and add `errs.C(errorClass,
errs.TypeCastingError)`.

### 3.8 Cyclic timer fires N+1 times for a cycle count of N (`waiters/timer.go:354`)
`processTimerEvent` checks `if tw.cyclesLeft == 0 { ...end... }` **before**
`tw.cyclesLeft--`, so the terminal check spends one extra cycle: a `Cycle` of N
delivers N+1 times. The regression test only passes because it feeds `cycles - 1`
to compensate. **Status: Active** (the sole in-repo caller compensates, so no
production path is wrong yet). **Fix**: decrement first, then test `<= 0`, and drop
the test's `-1` compensation.

### 3.9 Starter reconcile aborts mid-loop, leaving the hub partially wired (`thresher.go:651-682`)
`registerStarters`/`unregisterStarters` `return` on the first failing starter. In
the supersede path, if `unregisterStarters(prevLatest.starters)` fails on the 2nd
of 3, the 1st is already torn down, the 3rd is still live, and none of the new
version's starters get registered — registry and hub disagree, no repair.
**Status: Active but rare** (only when a hub op itself errors). **Fix**: make the
reconcile all-or-nothing (accumulate errors and/or roll back applied mutations).

### 3.10 `DeriveKey` accepts a present-but-nil value as a valid partial correlation key (`msgflow/correlation.go:88-101`)
The only emptiness guard is `val == nil`; a non-nil `data.Value` whose
`Get(ctx)` is nil is formatted as `"<nil>"` and joined into the composite key,
contradicting the doc-comment ("ok must be false when a property yields no value")
and BPMN §8.4.2 (all partial keys must be populated). **Status: Active but narrow**
(retrieval expression resolving to a present-but-nil field). **Fix**: reject
`raw := val.Get(ctx); raw == nil` before formatting.

### 3.11 `clocktest.Advance` allows moving the clock backwards (`clocktest/clocktest.go:56-62`)
`Advance` has no sign guard (`Advance(-time.Hour)` rewinds `now`), while `Set` is
documented forward-only and guards `if t.After(c.now)`. Contradicts the
monotonic-clock contract the timer waiters assume. **Status: Test-only** helper,
positive-duration callers only. **Fix**: `if d <= 0 { return }` at the top of
`Advance`.

### 3.12 `Message.Clone` drops `BaseElement` documentation (`bpmncommon/message.go:82-102`)
`Clone` rebuilds `BaseElement` from `WithID(m.ID())` only, discarding `docs`. The
flow-node clone path (`flow/element.go cloneIdentity`) deliberately preserves
docs, so this is inconsistent with the codebase's own Clone-fidelity convention.
**Status: Active** (docs are annotation metadata — no execution impact). **Fix**:
re-apply `WithDoc` for each `m.Docs()`, or use a BaseElement clone helper.

### 3.13 `memmetrics.seriesKey` uses `%v` over `any`, so distinct attribute sets collide (`memmetrics/memmetrics.go:209-224`)
`fmt.Sprintf("%s=%v", a.Key, a.Value)` renders `int(1)`, `int64(1)` and `"1"`
identically, and a comma/equals inside a string value forges another set's key
(both collisions reproduced). Counts/gauges/histograms for distinct sets merge,
and the series cap undercounts. **Status: Latent** — `Attr` is constructed only in
tests today, but it's a public API mirroring OTel `attribute.KeyValue`, so
embedders will pass arbitrary typed values. **Fix**: include the type
(`%s=%T:%v`) or a length-prefixed/quoted encoding.

### 3.14 `memtrace.liveSpan` mutates span state without synchronization (`memtrace/memtrace.go:72-96`)
`liveSpan.data`/`ended` are read/written by `End`/`SetAttributes`/`RecordError`/
`SetStatus` with no lock, while only `Recorder.add` is mutex-guarded. The `Span`
interface is "modeled on OpenTelemetry", whose contract is concurrency-safe, and
no doc confines a span to one goroutine. **Status: Latent** — `Tracer.Start` is
never called in production (noop is the default tracer), so no live path shares a
span. **Fix**: guard the per-span fields, or document single-goroutine confinement.

### 3.15 Test targets lack `-count=1`; cached `-race` results mask flakes (`Makefile:124-134, 80-82, 84-86`)
`go test -race ...` results are cached (verified: a rerun prints `(cached)`), so a
flaky goroutine/lock race — the dominant bug class in this engine — that passed
once is served green on rerun. (Finder's "only root escapes caching" claim was
**corrected**: `-coverprofile` is *also* cacheable, so the root caches too; only
the root module has tests, so impact is local re-runs of `make ci`/`make test-all`.)
**Status: Active** (CI cold-cache unaffected). **Fix**: add `-count=1`.

### 3.16 `.golangci.yml` `tests: false` disables all govet analyzers on `_test.go` (`.golangci.yml:5`)
With `run.tests: false`, golangci-lint excludes `_test.go` entirely, so
`testinggoroutine`, `copylocks`, `loopclosure`, `tests`, `sigchanyzer` never see
test code (reproduced: a `t.Fatal`-in-goroutine bug is reported only with
`tests: true`). This is a concurrency-heavy suite — 17 test files spawn goroutines,
11 use `WaitGroup`/`Mutex` — left unlinted, plus a redundant `_test\.go$`
exclusion. Distinct from the documented "depguard/errorlint not enabled" item.
**Status: Active** (preventive — `tests: true` currently yields 0 issues). **Fix**:
`tests: true` with targeted per-linter exclusions for the noisy linters on tests.

### 3.17 `depguard` rule will block the runtime server binary (`.golangci.yml:40-50`)
`core-no-runtime-no-adapters` uses `files: **/cmd/**/*.go` and denies the
`runtime`/`adapters` packages. That glob also matches `runtime/cmd/gobpm-server/
main.go`, whose entire purpose (ADR-004) is to import `.../runtime`. depguard *is*
enabled and CI lints the runtime module with this config (`make
lint-all-modules`), so the first real commit wiring the server will fail lint
(glob match against the absolute path confirmed). Today the server is a stub
importing nothing, so it's inert. **Status: Latent.** **Fix**: exclude
`**/runtime/cmd/**`, or scope the rule to the root-module layout.

### 3.18 `make clear` (`rm ./bin/*`) errors on a clean checkout (`Makefile:100`)
No `-f`, no `-` prefix: on a fresh clone (no `bin/`) or right after a prior
`clear`, the unmatched glob is passed literally to `rm`, which exits 1 and aborts
the target (reproduced under `/bin/sh`). Non-idempotent cleanup target. **Status:
Active** (DX papercut). **Fix**: `rm -rf ./bin/`.

---

## Notes on rigor

- **Refuted/dropped** during verification (not in this report): a
  `ParallelGateway.Arrive` "missing duplicate-flow guard" claim, and four other
  candidates that were either misreads or duplicates of the existing two passes.
- **Severity re-rated** by the verifier vs the finder's first guess in several
  cases (e.g. #1 P2→P1 as a genuine race; #14/#16/#17 P1/P2→P3 once blast radius
  was traced). The matrix reflects the verified severity.
- **Latent ≠ ignorable**: #11, #14, #26, #27, #30 are real defects on code paths
  (persistence, observability, the runtime server) that are stubbed today. They
  are cheapest to fix now, before those paths carry load.
