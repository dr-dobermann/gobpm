# FIX-036 — the engine's lifecycle bookkeeping: unsynchronized state, reservations that are never released, and foreign code under a lock

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Draft.
**Date:** 2026-08-05.
**Author:** Ruslan Gabitov.
**Branch:** `fix/thresher-audit` — the remediation of an external audit of `pkg/thresher` (2026-08-05).
**Upstream:** [ADR-002 v.2](../design/ADR-002-extension-architecture.md) §4.2 (bounded in-memory defaults — the principle the two unbounded maps break), [ADR-013 v.2](../design/ADR-013-instance-observability.md) §5 (contain observer failures — the containment the filter path skips), [ADR-015 v.1](../design/ADR-015-event-triggered-instantiation.md) (event-triggered instantiation and its correlation-key reservation), [ADR-019 v.1](../design/ADR-019-definition-versioning.md) §2.5 (latest-supersedes — the starter wiring both paths perform), [ADR-033 v.3](../design/ADR-033-persistence-and-state.md) §2.4/§2.5 (the wait-holder model whose subscription half publishes out of order).

**Grounded in (internal artifacts, verified at `36d9751`):**
- `pkg/thresher/thresher.go:535` — `Run` assigns `t.ctx`/`t.engineCancel` with no lock; `:707` — `Shutdown` reads `t.engineCancel` under `t.m`.
- `pkg/thresher/thresher.go:1118,1128` — the correlation-key reservation and its **only** release.
- `pkg/thresher/locked.go:241` — `settled` written; `pkg/thresher/discovery.go:82-84` — `Forget` deletes from `instances` alone.
- `pkg/thresher/subscription_holder.go:95,100` — the hub registration precedes the `t.subs` record; `:114-126` — `ReleaseWaits` scans `t.subs`.
- `pkg/thresher/producer.go:84-105` — `p.mu` spans `FilterObservation`; `pkg/thresher/observer.go:163` — the containment the sibling delivery path has.
- `pkg/thresher/thresher.go:456` — `UpdateState` stores any valid enum value; `:520,680` — the CAS ladder it bypasses.
- `pkg/thresher/thresher.go:1088` — `registerAllStarters` wires without the per-key lock `:862` takes.

---

## 1 Symptoms

An audit of `pkg/thresher` reported seven flags and five edge cases. Each was
re-checked against the code at `36d9751`; **eight are real**, three are
documented design (§7). They are not one bug, but they are one *shape*: the
engine's own bookkeeping — its context, its reservations, its state — is
maintained by convention, and every place the convention is not machinery is a
place it has already slipped.

### 1.1 The engine context is written without synchronization

`Run` derives and stores the engine context:

```go
// thresher.go:535
t.ctx, t.engineCancel = context.WithCancel(ctx)
```

No lock. `Shutdown` reads the same field **under `t.m`**:

```go
// thresher.go:701-708
t.m.Lock()
…
cancel := t.engineCancel
t.m.Unlock()
```

A mutex on one side of a shared write is not synchronization — under the Go
memory model this is a data race, and the reader may observe a nil or stale
`engineCancel`, i.e. a `Shutdown` that cancels nothing while reporting
`Stopped`. Five further readers take no lock at all: `launchInstance`
(`:1171`), `launchInstanceFromEvent` (`:1346`), `invoker.go:76`,
`recovery.go:103`, `wake.go:114,146`. `Run`'s own comment concedes the field is
reassigned on a retry — it solves that for the hub goroutine (by capturing
`runCtx`) and for nobody else.

### 1.2 A finished conversation's correlation key is reserved forever

An event-started process reserves its namespaced correlation key so two
concurrent messages cannot both instantiate:

```go
// thresher.go:1118-1131
if !t.reserveKeyLocked(nsKey) {
    return nil // an instance already exists for this key: join, no duplicate
}
if err := t.launchInstanceFromEvent(…); err != nil {
    t.releaseKeyLocked(nsKey)   // the ONLY release in the package
    return err
}
```

The reservation is released **only when the launch fails**. Nothing releases it
when the instance finishes. Two consequences, the second worse than the first:

- `seenKeys` grows for the engine's lifetime — one entry per correlation value
  ever seen, against ADR-002 §4.2's bounded-defaults principle;
- once that instance is gone, a later message carrying the same business key is
  answered `"joined existing instance (key seen)"` and **silently dropped** —
  there is no instance to join. Order `ORD-42` handled today means order
  `ORD-42` can never start a process again.

`Forget` — documented as the path so "a long-running engine doesn't accumulate
finished instances" — does not release keys.

### 1.3 `settled` is never deleted

`settledFor` mints one terminal-signal channel per instance id and stores it
(`locked.go:241`); the map is read on rebuild so a waiter survives dehydration
(`:254`). There is no `delete(t.settled, …)` anywhere in the package. `Forget`
removes the instance registration (`discovery.go:83`) and leaves the channel.

### 1.4 A held subscription reaches the hub before it reaches the release path

```go
// subscription_holder.go:95-101
if err := t.RegisterEvent(h, eDef); err != nil { return err }

t.subMu.Lock()
t.subs[subKey{instanceID, trackID, eDef.ID()}] = h
t.subMu.Unlock()
```

`ReleaseWaits` withdraws holds by scanning `t.subs` (`:114-126`). Between the
two statements the holder is live on the hub and invisible to the release: a
concurrent `ReleaseWaits` for that track (an interrupting boundary, an
Event-Based gateway's losing arm) returns having withdrawn nothing, the
subscription stays registered, and the map entry materialises afterwards —
a leaked holder that can still wake an instance for a wait nobody is waiting on.

### 1.5 The host's observation filter runs under the producer lock, uncontained

```go
// producer.go:84-97
p.mu.Lock()
defer p.mu.Unlock()

for _, s := range p.subs {
    if p.filter != nil {
        filtered, ok := p.filter.FilterObservation(s.obs, ev)
```

`ObservationFilter` is host-supplied (`pkg/observability/visibility.go:19`).
The sibling delivery path contains a panicking host observer
(`observer.go:163` → `deliver`'s recover); this path does not. A panicking
filter therefore propagates out of `Report` into **its caller** — which is an
instance's loop goroutine — and a merely slow filter serializes every `Report`
in the engine behind one host call.

### 1.6 `UpdateState` bypasses the lifecycle ladder it exists beside

`Run` claims its transition with a CAS (`:520`), `Shutdown` likewise (`:680`).
`UpdateState` (`:456`) validates that the value is a legal enum member and
stores it. A host may set `Started` on an engine that never ran — after which
`RegisterEvent`'s `State() != Started` guard (`:745`) admits registrations to a
hub that was never started. The method is not dead API: `Paused` is a real
state (`Shutdown` accepts `Started, Paused` at `:679`), so the defect is the
absence of a transition rule, not the method's existence.

### 1.7 `Shutdown` waits only for the instances that existed before the cancel

The snapshot is taken under `t.m` (`:701-705`), *then* the engine context is
cancelled (`:712`), *then* the snapshot is awaited (`:717`). An instance born in
that window — an event-triggered start, a Call Activity child — is never
awaited. Independently: the deferred publish (`:696`) stores `Stopped` on
**every** exit path, including the timeout return at `:721`, so the engine
reports itself stopped while instances are still running.

### 1.8 Starter wiring races between `RegisterProcess` and `Run`

`RegisterProcess` holds the per-key lock (`:862`) across the registry mutation
(`:871`) and the hub wiring, which it performs only `if t.State() == Started`
(`:888`). `Run`'s `registerAllStarters` (`:1088`) reads
`t.latestStartersLocked()` and wires everything it finds — **without** the
per-key lock. The two paths are therefore not mutually exclusive: a version
appended while `Run` is between its read and its wiring can be registered by
both (duplicate starters on one event definition) or, with the opposite
interleaving, by neither.

## 2 Root cause analysis

**One rule, four unenforced halves.** The package's central discipline —
recorded at `thresher.go:135` and in every `…Locked` helper — is *never hold
`t.m` across a subsystem call*. It is enforced by comments. §1.1, §1.4, §1.5
and §1.8 are each the same failure: a place where correctness depends on an
ordering or a scope that nothing checks. The state field escaped `t.m`
deliberately (it is atomic, and that is why `State()` is callable under the
lock); the *context* escaped by omission and kept the same "not guarded by
`t.m`" shape without the atomic that makes it safe.

**Reservations without a lifecycle.** §1.2 and §1.3 share a cause: both maps
are written on the way in and have no owner on the way out. `Forget` is the
package's only reaping path and it reaps one of the three maps. The
correlation reservation additionally conflates two questions — *is a start in
flight for this key?* and *does an instance for this key exist?* — and answers
the second with the first's data, which is why an expired reservation reads as
a live conversation.

**Containment applied once.** §1.5 is FIX-035's fix, applied to one of the two
call sites. FIX-035 §3.2.2 records the producer path as the "second call site"
for the *panic sink*; the filter call beside it was never brought under the
same rule.

## 3 Solution

### 3.1 Alternatives considered

| # | Decision | Alternatives | Chosen |
|---|---|---|---|
| 1 | §1.1 the engine context | (a) take `t.m` for every read and the write — puts the engine lock on the hot launch path and invites the RC2 lock-ordering hazard the package forbids; (b) an `atomic.Pointer` to an immutable `{ctx, cancel}` pair — lock-free reads, single writer, and the same shape `state` already uses | **(b)** |
| 2 | §1.2 the key reservation | (a) release in `Forget` only — still wrong for an engine that never calls it; (b) a watcher goroutine per reservation — a goroutine per conversation, the cost ADR-007 exists to avoid; (c) record `nsKey → instanceID` and re-take the reservation when the recorded instance is unknown or terminal — self-healing, O(1), no goroutine | **(c)** + release in `Forget` so the map also shrinks |
| 3 | §1.4 the hold ordering | (a) hold `subMu` across `RegisterEvent` — an engine lock across a subsystem call, the forbidden shape; (b) record in `t.subs` first and roll the entry back if registration fails — the release path can then always see it, and the hub's `UnregisterEvent` is already idempotent for an unknown definition (`eventhub.go:401-409`) | **(b)** |
| 4 | §1.5 the filter | (a) snapshot subscribers, unlock, then filter and send — breaks the documented fence that lets `unsubscribe` close a channel with no in-flight send, and buys nothing the contract cannot state; (b) contain the filter call the way `deliver` contains an observer, and make the cheapness the lock demands an explicit term of the `ObservationFilter` contract | **(b)** — the panic is the defect and is fixed; the serialization is a **property of a lock-fenced fan-out, decided here as a contract obligation on the host**, exactly as the observer stream already requires a fast, non-blocking observer. Lifting the fence (epoch counter or send-side refcount) would change the observation contract itself — an ADR question, and one nothing has asked: no measurement shows a filter cost worth redesigning for |
| 5 | §1.6 `UpdateState` | (a) remove the method — breaks a public API and a real feature (`Paused`); (b) validate the *transition*, not just the value | **(b)** |
| 6 | §1.7 shutdown | (a) refuse new instances once `Stopping` — a larger contract change (what does a start return then?); (b) re-snapshot after the cancel until the set stops growing, and name the unsettled instances on timeout | **(b)** |
| 7 | §1.8 starter wiring | (a) take the per-key lock in `registerAllStarters` — closes this window but leaves the invariant order-dependent; (b) make wiring idempotent per registration with a flag both paths check under `t.m` | **(b)** |

### 3.2 Changes by file

#### 3.2.1 `pkg/thresher/thresher.go` — the engine context becomes atomic

`t.ctx`/`t.engineCancel` are replaced by one `atomic.Pointer[engineCtx]` over
an immutable pair. `Run` stores it before the hub start; every reader loads it
(`launchInstance`, `launchInstanceFromEvent`, `invoker`, `recovery`, `wake`),
and a nil load — an engine that never ran — is a classified error rather than
a nil-context panic.

#### 3.2.2 `pkg/thresher/locked.go`, `thresher.go` — the reservation knows whose it is

`seenKeys map[string]struct{}` becomes `map[string]string` (nsKey →
instanceID). `reserveKeyLocked` returns true when the key is free **or** when
the instance holding it is no longer live (absent from `instances`, or present
and terminal), taking the reservation over in that case. The join path keeps
its existing semantics for a live instance.

#### 3.2.3 `pkg/thresher/discovery.go` — `Forget` forgets everything

`Forget` deletes the instance's `settled` channel and any correlation
reservation it holds, alongside the registration.

#### 3.2.4 `pkg/thresher/subscription_holder.go` — record, then arm

`HoldSubscription` writes `t.subs` first and registers on the hub second,
removing the entry if registration fails.

#### 3.2.5 `pkg/thresher/producer.go` — the filter is host code

The `FilterObservation` call is wrapped in the same containment `deliver`
provides: a panic is recovered, counted and logged once, and the event is
treated as denied for that recipient rather than propagating into the engine.

#### 3.2.6 `pkg/observability/visibility.go` — the filter's obligations, stated

`ObservationFilter`'s doc comment gains the two terms the implementation has
always relied on and never wrote down: the filter is called **on the
reporter's goroutine, under the producer's dispatch lock**, so it must be
cheap and must not block; and a panic is contained (the event is treated as
denied for that recipient) rather than propagated. This is the same contract
`Observer` carries, and stating it is what turns §1.5's remaining
serialization from an unexamined hazard into a declared cost.

#### 3.2.7 `pkg/thresher/thresher.go` — `UpdateState` validates the transition

A transition table admits the operator transitions (`Started ↔ Paused`) and
refuses everything else with `errs.InvalidState`, naming both states. The
lifecycle transitions stay the CAS ladder's alone.

#### 3.2.8 `pkg/thresher/thresher.go` — `Shutdown` drains what it started

After the cancel, the instance set is re-snapshotted and awaited until no new
ids appear; the timeout error names the instances that did not settle.

#### 3.2.9 `pkg/thresher/thresher.go` — starter wiring is idempotent

`instanceReg`/the version record gains a `startersWired` flag set under `t.m`;
both `RegisterProcess` and `registerAllStarters` check-and-set it, so whichever
runs first wires and the other is a no-op.

## 4 Verification

### 4.1 Regression tests

| # | Test | Asserts |
|---|---|---|
| T-1 | `TestEngineContextIsRaceFree` | concurrent `Run`/`Shutdown`/launch under `-race` — no race on the engine context; a `Shutdown` after a completed `Run` always cancels (§1.1) |
| T-2 | `TestCorrelationKeyReleasedAfterInstanceEnds` | a second message with the same key after the first instance terminated starts a NEW instance, not a silent join (§1.2) |
| T-3 | `TestForgetReleasesKeyAndSettled` | after `Forget`, `seenKeys` and `settled` no longer hold the id (§1.2, §1.3) |
| T-4 | `TestReleaseWaitsSeesHoldImmediately` | a `ReleaseWaits` racing `HoldSubscription` withdraws the hold — the hub carries no leftover subscription (§1.4) |
| T-5 | `TestPanickingObservationFilterContained` | a filter that panics denies its recipient, is counted and logged once, and never reaches `Report`'s caller (§1.5) |
| T-6 | `TestUpdateStateRejectsLifecycleJumps` | `Started` on a never-run engine is refused; `Started ↔ Paused` is admitted (§1.6) |
| T-7 | `TestShutdownAwaitsInstanceBornDuringCancel` | an instance created in the snapshot→cancel window is awaited; the timeout error names the unsettled (§1.7) |
| T-8 | `TestStartersWiredExactlyOnce` | a registration racing `Run` yields exactly one hub registration per starter (§1.8) |

### 4.2 Gate

`make ci` green end to end, `-race` included; diff-coverage ≥95% on the touched
lines (`COVER_MIN`).

## 5 Prevention

- The `…Locked` convention gains the one piece of machinery it can have: every
  field that is deliberately *not* guarded by `t.m` is now an atomic (state,
  engine context), so "unguarded" and "unsafe" stop being the same shape.
- The two maps with a lifecycle are reaped by the one path that already exists
  for reaping, `Forget`, and the correlation reservation additionally
  self-heals — a leak now requires both mechanisms to fail.
- Host code called from inside the engine is contained at both call sites, and
  the `ObservationFilter` contract states the constraint the lock imposes.

## 6 Regressions and side effects

- `seenKeys`'s value type changes; it is package-private, so no API moves.
- `UpdateState` becomes stricter: a host that today sets an arbitrary state
  gets a classified error. This is the point of the fix, but it is a
  behavioural change to a public method and is called out in the CHANGELOG.
- `Shutdown` may now wait marginally longer — it awaits instances it previously
  abandoned — bounded by the caller's context exactly as before.

## 7 Related

Three audit findings are **documented design, not defects**, and are recorded
here so a later reader does not re-open them:

- **Recovery does not retry a missing pinned version** (`recovery.go:84`) —
  deployment parity is the operator's contract (ADR-033 v.3 §2.8); the engine
  reports and stops rather than guessing.
- **`fireDue` scans all holds per tick** (`timer_service.go`) — the O(1)
  goroutine cost was bought with an O(n) scan, deliberately.
- **`Forget` is all-or-nothing** — stated in its own doc comment
  (`discovery.go:57-61`).

Prior art: [FIX-035](FIX-035-observer-silence-and-attribute-vocabulary.md)
(the containment rule §1.5 completes), [FIX-013](FIX-013-thresher-registry-starter-lifecycle.md)
(the per-key lock §1.8 extends), [FIX-002](FIX-002-event-start-registration-lifecycle.md)
(the RC2 rule every alternative in §3.1 is measured against).

## 8 Implementation summary

_To be filled after the milestones land._

## 9 Open questions

None.
