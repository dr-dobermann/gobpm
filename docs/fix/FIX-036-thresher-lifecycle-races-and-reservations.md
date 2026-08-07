# FIX-036 — the engine's lifecycle bookkeeping: unsynchronized state, reservations that are never released, and foreign code under a lock

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted.
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
  ever seen, against ADR-002 v.2 §4.2's bounded-defaults principle;
- once that instance is gone, a later message carrying the same business key is
  answered `"joined existing instance (key seen)"` and **silently dropped** —
  there is no instance to join. Order `ORD-42` handled today means order
  `ORD-42` can never start a process again.

`Forget` — documented as the path so "a long-running engine doesn't accumulate
finished instances" — does not release keys.

**And the mirror image, found while fixing the above** (not in the audit): the
map is in-memory only, so it does not survive the process. An engine that
recovers a live conversation from its checkpoint — a cold restart
(`recovery.go`) or a wake (`wake.go`) — brings the instance back **unreserved**,
and the next message carrying its key starts a *duplicate* conversation beside
it. The two halves are one defect seen from both ends: the reservation's
lifetime is not tied to the conversation's.

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

The arm has **two** unguarded sides, which is why simply swapping the order is
not the fix. Recording first makes the hold visible to a concurrent release,
but that release then calls `UnregisterEvent` on a holder the hub does not know
yet: the call fails `ObjectNotFound` (`eventhub.go:401-409`), the registration
that follows succeeds, and the subscription is again live with no record —
the same leak reached from the other side. The hold must therefore be recorded
**before** the arm so a release can see it, and **confirmed still recorded**
after the arm so a release that could not reach the hub is honoured.

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

`LogRedactor` — the producer's *other* host-supplied hook — has exactly the
same shape and the same gap:

```go
// producer.go:67-75
if p.redactor != nil {
    redacted, ok := p.redactor.RedactLog(ev)
```

It is the same interface family (`visibility.go:9`), asserted off the same
authorizer, called on the same reporting goroutine, with no recover either. It
is repaired with the filter rather than left for a second pass: they are one
defect reached through two entry points, and containing only the one the audit
happened to name would leave the identical crash reachable through the other.

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
| 2 | §1.2 the key reservation | (a) release in `Forget` only — still wrong for an engine that never calls it; (b) a watcher goroutine per reservation — a goroutine per conversation, the cost ADR-007 v.2.1 exists to avoid; (c) record `nsKey → instanceID` and re-take the reservation when the recorded instance is unknown or terminal — self-healing, O(1), no goroutine | **(c)** + release in `Forget` so the map also shrinks |
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

A rebuilt conversation re-takes its reservation: `recoverOne` and
`rebuildAndContinue` bind the keys the checkpoint carries (ADR-033 v.3 §2.1's
`ConvKeys`) to the instance they just tracked, so a conversation that crossed a
restart is reserved exactly as one that never stopped.

#### 3.2.3 `pkg/thresher/discovery.go` — `Forget` forgets everything

`Forget` deletes the instance's `settled` channel and any correlation
reservation it holds, alongside the registration.

#### 3.2.4 `pkg/thresher/subscription_holder.go` — record, arm, confirm

`HoldSubscription` writes `t.subs` first, registers on the hub second, and then
re-reads the record: if it no longer names this holder, a release took it while
the arm ran and could not withdraw it from the hub, so the hold withdraws
itself. A refused registration removes the entry — conditionally, so a rollback
never clobbers a record a later arm has put there. A hold withdrawn by a
concurrent release returns **no error**: the release is authoritative, and the
caller's wait is gone either way.

`ReleaseWaits`' per-holder hub call moves into a shared `withdraw` helper, since
the confirm path needs exactly the same "unregister, log a failure at Debug"
behaviour — a missing waiter is an idempotent condition, not an error.

#### 3.2.5 `pkg/thresher/producer.go` — the two hooks are host code

`FilterObservation` and `RedactLog` are both wrapped in the containment
`deliver` provides, through one shared `callHostHook` helper: the panic is
recovered, counted per hook, and logged once at Warn with a stack (Debug
thereafter, since ADR-022 v.2 §2.4 forbids a per-event record above it).

Both **fall closed**. A panicking filter denies its recipient; a panicking
redactor suppresses the log record. The alternative — treating a failed hook as
pass-through — would make a crash in the host's policy code the one condition
under which the engine emits what that policy exists to withhold, which is the
worst possible moment to fail open.

#### 3.2.6 `pkg/observability/visibility.go` — the hooks' obligations, stated

Both interfaces' doc comments gain the terms the implementation has always
relied on and never wrote down: each hook is called **on the reporter's
goroutine** — and the filter additionally **under the producer's dispatch
lock**, once per registered observer — so it must be cheap and must not block
or call back into the engine; and a panic is contained, falling closed, rather
than propagated. This is the same contract `Observer` carries, and stating it
is what turns §1.5's remaining serialization from an unexamined hazard into a
declared cost.

#### 3.2.7 `pkg/thresher/thresher.go` — `UpdateState` validates the transition

A transition table admits the operator transitions (`Started ↔ Paused`) and
refuses everything else with `errs.InvalidState`, naming both states. The
lifecycle transitions stay the CAS ladder's alone.

#### 3.2.8 `pkg/thresher/thresher.go` — `Shutdown` drains what it started

The snapshot moves BELOW the cancel and becomes a loop: `drainInstances`
re-reads the registry after each pass and awaits whatever is new, ending at the
fixed point where a pass adds nothing. Births stop once the cancel propagates,
so the loop terminates; `ctx` bounds it regardless. The timeout error names the
instances that did not settle, sorted, so it reads the same way twice.

On §1.7's second half — the deferred publish marking `Stopped` even on the
timeout path — the state **stays** `Stopped`. The engine's context is cancelled
and its hub is being torn down, so it must go on rejecting new work; leaving it
`Stopping` would make it neither usable nor shut down, and `Shutdown`'s own
idempotence rule (`case Stopping: return nil`) would then refuse the retry. What
was wrong is not the state but letting an abandoned shutdown read as a clean
one, so the timeout additionally logs a `Warn` naming the instances left
running. The returned error and the operator log now say the same thing, and
neither claims the stop was orderly.

#### 3.2.9 `pkg/thresher/thresher.go` — starter wiring is idempotent

`ProcessRegistration` gains a `wired` flag guarded by `t.m`; both
`RegisterProcess` and `registerAllStarters` check-and-set it, so whichever runs
first wires and the other is a no-op.

Only the double-wiring interleaving is reachable, and it is worth saying why the
opposite one is not: `Run` stores `Started` *before* it sweeps, so a
`RegisterProcess` whose state read returns not-started is ordered before that
store, hence before the sweep's registry read — its version is therefore always
visible to the sweep. The two paths can both wire; they cannot both skip.

**A claim must be released whenever it does not result in a live subscription.**
`registerStarters` is all-or-nothing (FIX-013 §1.3), so a failed call leaves
nothing on the hub, and `Run` rolls the whole start back on one — a claim kept
across that rollback would make the RETRY find every version already marked
wired and wire nothing, starting a clean engine with no auto-start at all. That
is a worse failure than the double-wiring the flag exists to prevent, and
`TestRunRollsBackWhenStarterRegistrationFails` catches it. The claim is
therefore handed back on every failure path, and the flag tracks the hub in both
directions: `RegisterProcess` releases the superseded version's claim as it tears
its starters down, and promote-on-removal takes the claim for the version it
promotes.

## 4 Verification

### 4.1 Regression tests

| # | Test | Asserts |
|---|---|---|
| T-1 | `TestEngineContextIsRaceFree` | concurrent `Run`/`Shutdown`/launch under `-race` — no race on the engine context; a `Shutdown` after a completed `Run` always cancels (§1.1) |
| T-2 | `TestCorrelationKeyReleasedAfterInstanceEnds` | a second message with the same key after the first instance terminated starts a NEW instance, not a silent join (§1.2) |
| T-2a | `TestRecoveredConversationKeepsItsKey` | engine A parks a conversation and is fenced out; engine B recovers it from the checkpoint and the reservation names the recovered instance — without the rebind the key comes back free and the next message duplicates the conversation (§1.2, restart half) |
| T-3 | `TestForgetReleasesKeyAndSettled` | after `Forget`, `seenKeys` and `settled` no longer hold the id (§1.2, §1.3) |
| T-4 | `TestReleaseWaitsDuringArmWithdrawsTheHold` | a `ReleaseWaits` driven into the MIDDLE of `HoldSubscription` (a hub wrapper fires it from inside `RegisterEvent`) leaves neither a record nor a hub subscription — and the withdrawal is counted at the hub, so removing either half of the fix fails it (§1.4) |
| T-4a | `TestHoldSubscriptionRollsBackOnArmFailure` | a refused arm leaves nothing recorded — the record exists only to make an ARMED hold releasable (§1.4) |
| T-5 | `TestPanickingObservationFilterContained` | a filter that panics denies its recipient, is counted, is logged once at Warn and at Debug thereafter, and never reaches `Report`'s caller (§1.5) |
| T-5a | `TestPanickingLogRedactorContained` | a redactor that panics suppresses the record rather than echoing it unredacted, and leaves the observer stream — which is not the redactor's to deny — untouched (§1.5) |
| T-6 | `TestUpdateStateRejectsLifecycleJumps` | `Started` on a never-run engine is refused; `Started ↔ Paused` is admitted (§1.6) |
| T-7 | `TestShutdownAwaitsInstanceBornDuringCancel` | an instance created in the snapshot→cancel window is awaited; the timeout error names the unsettled (§1.7) |
| T-8 | `TestStartersWiredExactlyOnce` | a registration racing `Run` yields exactly one hub registration per starter — counted at the hub, so a second arm fails it (§1.8) |
| T-8a | `TestFailedSweepReleasesItsClaims` | a sweep that wired nothing claims nothing, so `Run`'s rollback stays retryable (§1.8) |
| T-9 | `TestForgetCancelsInstanceContext` | `Forget` releases the instance's retained context-cancel, not only its registration — the reaping path stops leaking a `cancelCtx` per reaped instance (§8.2) |

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
  Three existing tests drove it in ways the rule now refuses, and each is
  re-pinned rather than relaxed:
  - `TestThresher_StateManagement/update state success` asserted that a
    never-run engine could be moved to `Started` by hand — it encoded §1.6's
    defect as the expected behaviour, so it now asserts the refusal. The
    legitimate pause/resume pair keeps its coverage in the same file's
    `run and pause workflow`, which runs the engine first.
  - `TestEngineStatePausedAndSupersede` reached `Paused` from `NotStarted`
    purely to emit the fact; it now runs the engine first, which is what
    pausing means.
  - The same test's `UpdateState(NotStarted)` line existed to exercise
    `reportEngineState`'s no-phase early return. That state is no longer
    reachable through the public API, so the early return is pinned directly by
    `TestUnmappedEngineStateReportsNothing` — moved to the reporter rather than
    dropped, since the Run rollback still reaches it internally.
- `Shutdown` may now wait marginally longer — it awaits instances it previously
  abandoned — bounded by the caller's context exactly as before.
- **`TestCorrelationDedup` is re-pinned to a live conversation.** It asserted
  ADR-016 v.1 §2.3's join rule using the start→end process, so its "existing
  instance" completed the moment it was created; §1.2's release then let the
  repeat key legitimately start a third instance whenever the message lost the
  race against that completion, and the test failed roughly one run in six.
  The join rule is unchanged and still pinned — the test now uses the parking
  process, which is what makes the existing instance *joinable* at all. The
  finished-conversation half is owned by its own test
  (`TestCorrelationKeyReleasedAfterInstanceEnds`, T-2), which waits for the
  completion instead of racing it.

  This is the only pre-existing test whose asserted CONTRACT this landing
  changes, and it is worth saying why it is a re-pin and not a weakening:
  joining a finished instance cannot deliver the message anywhere — no receiver
  is parked on it — so the old reservation did not preserve the join rule, it
  discarded the business message that the rule exists to route.
- **`TestRestartRecoveryBoundaryDeadline` is hardened, not re-pinned.** It
  asserts exactly what it did before — a recovered boundary fires at its
  RECORDED deadline rather than a re-evaluated one — but it failed once in a
  full parallel `go test ./...` sweep and never in isolation. Engine-1 is
  abandoned by lease rather than stopped, so it keeps running with its own timer
  armed: if the crash-and-recover sequence did not finish inside the 700 ms
  deadline, engine-1 fired the boundary and completed the instance, leaving
  engine-2 nothing in flight to recover. The deadline widens to 2.5 s, which
  costs nothing in assertion strength because an overdue restored deadline fires
  immediately (`TestRestartRecoveryOverdueTimer` pins that) — the test proves
  the same thing whether the deadline is still ahead or already behind at
  recovery time. Its crash gate also now waits for the checkpoint to carry the
  armed BOUNDARY rather than only `StatusActive`, the same race
  `waitParkedRecord` closes for the track's own wait.

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

### 8.1 Milestones

| # | Commit | Scope |
|---|---|---|
| — | `acd305b` | this document |
| M1 | `0fd8592` | §1.1 — the engine context becomes an `atomic.Pointer` to an immutable `{ctx, cancel}` pair (§3.2.1) |
| M2 | `455aedb` | §1.2, §1.3 — the reservation records its owner and self-heals; `recoverOne`/`rebuildAndContinue` rebind a recovered conversation's keys; `Forget` reaps `settled` and the reservations (§3.2.2, §3.2.3) |
| M3 | `aa8acc4` | §1.4 — `HoldSubscription` records, arms, then confirms (§3.2.4) |
| M4 | `0a768c0` | §1.5 — both host hooks contained and their obligations stated; `TestCorrelationDedup` re-pinned (§3.2.5, §3.2.6) |
| — | `87a4b10` | `gofmt` on three test files the lint scope never saw |
| M5 | `9f9298c` | §1.6, §1.7 — `UpdateState`'s transition rule; `Shutdown` drains what it started (§3.2.7, §3.2.8) |
| M6 | `8799afb` | §1.8 — starter wiring is idempotent per registration (§3.2.9) |
| M7 | `d247464` | the branches the fix added, covered; four duplicated guards folded into one; `TestRestartRecoveryBoundaryDeadline` hardened |
| M8 | `786176d` | the three findings of the independent pre-merge review (§8.2) |

### 8.2 Findings the implementation added to the design

Three things the doc did not foresee, each recorded where it belongs and
repeated here because they are the parts a later reader is most likely to
re-derive:

- **§1.4 has two sides, not one.** The prescription — "record, then arm" — closes
  the window the audit named and opens its mirror: a release landing between the
  record and the arm unregisters a holder the hub does not know yet, the arm
  then succeeds, and the subscription is live with no record. Recording first is
  necessary but not sufficient; the arm must also *confirm* it still owns the
  record. Writing T-4 is what surfaced this — the doc-as-written version fails it
  on "the holder must actually leave the hub, not just the map".
- **§1.5's defect has two entry points.** `LogRedactor` is the same interface
  family, asserted off the same authorizer, called on the same goroutine, with
  the same missing recover. It is repaired with the filter rather than left for a
  second pass.
- **§1.8's flag needs a release, not just a claim.** `registerStarters` is
  all-or-nothing and `Run` rolls the whole start back when it fails, so a claim
  kept across that rollback makes the retry wire *nothing* — an engine that
  starts cleanly with no auto-start at all, strictly worse than the double-wiring
  the flag exists to prevent. `TestRunRollsBackWhenStarterRegistrationFails`
  caught it.

**And three the self-review chain could not have found.** After `/check-srd`
passed at 0 🔴 / 0 🟡 and the gate was green, the branch diff was reviewed by an
**external agent** (Antigravity / `agy`, `gemini-3.1-pro-high`) across three
lenses, doc-blind. Every lens returned exactly one note; all three verified as
real, and all three are fixed in M8:

- **`instanceReg.stop` was never called — anywhere.** Every launch path derives
  the instance's context from the engine's with `context.WithCancel` and retains
  the cancel; three separate comments describe it as "retained for later
  teardown (engine stop / instance cleanup)", and no teardown ever used it. The
  child therefore stayed attached to the engine context's children for the
  engine's whole lifetime, and `Forget` — whose doc comment says it exists "so a
  long-running engine doesn't accumulate finished instances" — reaped the
  registration, the terminal signal and the reservation while leaving the
  context behind. `Shutdown` masked it by cancelling the parent, so the leak was
  bounded only by engine lifetime, which for a server is unbounded. `Forget` now
  collects the retained cancels under `t.m` and invokes them after the unlock
  (the `…Locked` convention), pinned by `TestForgetCancelsInstanceContext`.
- **`Fact` is shared, and §3.2.6 did not say so.** `Fact.Details` is a
  `map[string]string` — a reference. A redactor or filter that edits it in
  place, the allocation-free reflex, corrupts the event for the log echo and
  every later observer; for a *per-recipient* filter that is precisely
  backwards. M4 documented what those hooks owe the engine and missed this one;
  both doc comments now state that `ev` is read-only and that a modification
  must be returned as a copy. No defensive copy in the engine: that would be a
  per-event allocation on the hot path, which is the cost ADR-022 v.2 §2.4's
  hot-path corollary exists to avoid.
- **T-1 could pass for the shape it exists to catch.** `Run` is non-blocking, so
  without a start barrier the writer goroutine could finish before the runtime
  scheduled a single reader — and the race detector only reports accesses that
  actually overlap. Since the engine pair is now atomic the detector has nothing
  to say either way; the test's entire value is as a canary for someone
  reverting to plain fields, and that value needed the overlap it did not
  guarantee. All nine goroutines now block on one channel and fire together.

The point is not the three findings but their shape: none is a doc-vs-code
mismatch, so `/check-srd` was not built to see them, and none is a house-rule
violation, so `/check-style` was not either. Both are the author checking the
author. This is why the independent review became a step of its own
(`/pr-review`, `/sdd-fix` step 14a) rather than a one-off.

### 8.3 Gate

`make ci` green end to end at `d247464`, verified by its own completion markers
rather than an exit code:

- `diff-coverage: 100.0% of 298 changed coverable lines (min 95%) — PASS`
- `No vulnerabilities found.` on every module
- `consumer-smoke … ✓`, `linkcheck: every relative link resolves`, mock-check
  regenerated clean, `tidy` across all example modules, every example run
- no `*** [<target>] Error N` anywhere in the log

The lines that remain uncovered are the three `if err != nil` propagations at
`instanceContext`'s gated call sites (`InvokeProcess`, `launchInstanceFromEvent`,
recovery), which no caller can reach because all three refuse on a non-started
engine long before they get there. The guard itself is pinned by
`TestEngineContextRefusedBeforeRun`.

## 9 Open questions

None.
