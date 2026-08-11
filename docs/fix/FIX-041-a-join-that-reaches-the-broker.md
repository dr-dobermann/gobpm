# FIX-041 — a join that reaches the broker: a foreign call under the hub lock, and the windows around it

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Draft.
**Date:** 2026-08-11.
**Author:** Руслан Габитов.
**Branch:** `fix/lost-iteration-correlation-key` (the same branch as the #320
fix these defects were introduced by — they are that fix's own error paths, not
a separate landing).
**Tracking:** #320.
**Upstream:** [ADR-006 v.5](../design/ADR-006-events-and-subscriptions.md) §2.5
(*"Waiter lifecycle: the EventHub is the sole owner"*) and §2.4 (*"Delivery
contract: in-memory, subscribe-before-publish, non-durable"*);
[ADR-017 v.1](../design/ADR-017-channel-based-event-processing.md) §2 Rule 1
(inbound events are channel-parked and loop-dispatched — the receiving loop
runs the correlation gate, so the waiter is a pure forwarder).

ADR-006 is at v.6, which is `Draft` until its changes land; §2.4 and §2.5 are
v.5 text and v.5 is the contract in force, so that is what this fix is pinned
against.

**Grounded in (verified at `fb6179a2`):**

- `internal/eventproc/eventhub/eventhub.go:292-307` — `joinExistingWaiter`
  takes `eh.m` (`:292`, `defer` unlock `:293`) and calls
  `w.AddEventProcessor(ep)` at `:307`.
- `internal/eventproc/eventhub/eventhub.go:328-352` — `publishWaiter` takes
  `eh.m` at `:328`, calls `winner.AddEventProcessor(ep)` at `:340`, releases at
  `:352`.
- `internal/eventproc/eventhub/eventhub.go:231-237` — the doc comment
  justifying both: *"The build func and AddEventProcessor never re-enter eh.m
  … so holding eh.m across them is safe."*
- `internal/eventproc/eventhub/eventhub.go:243-244` — *"That is registry work
  and stays under the lock."*
- `git show origin/master:internal/eventproc/eventhub/waiters/message.go` —
  on master `AddEventProcessor` is `slices.Index` + `append` under `mw.m` and
  nothing else. It makes no foreign call.
- `internal/eventproc/eventhub/waiters/message.go:191-204` — the current
  version calls `sub.AddKey(k)` per key, then appends `ep` at `:206-212`.
- `internal/eventproc/eventhub/waiters/message.go:320-332` — `Service`'s
  late-key loop returns on failure without `sub.Unsubscribe()`.
- `internal/eventproc/eventhub/waiters/message.go:180-186` — a processor
  joining before `Service` is appended to `mw.processors` **and** its keys to
  `mw.pendingKeys`; `:289-298` — `Service` then reads both.
- `pkg/messaging/messagebroker.go:23-37` — the `Subscription` port: `C`, `AddKey`,
  `Unsubscribe`. **There is no `RemoveKey`.**
- `pkg/messaging/membroker/membroker.go:48` — `keys map[string]struct{}`;
  `:291` — `keySet` builds it. Duplicate keys collapse in this implementation.
- `internal/eventproc/eventhub/eventhub.go:861-876` — `AddEventKey` is the
  established shape for reaching an optional waiter capability: `RLock` →
  lookup → `RUnlock` → type-assert → call **outside** the lock.
- `internal/instance/correlation.go:219` and `pkg/thresher/subscription_holder.go:40`
  — the only two implementors of `CorrelationKeys() []string`.
- [FIX-038](FIX-038-locks-across-host-calls-and-lost-registrations.md) §1.1 —
  the same lock, the same broker, fixed there by moving `Service` out of the
  critical section; §5 — *"A mechanical sweep for the shape is worth more than
  reading the sites an audit names."*
- `ls scripts/` → `check-tool-pins.sh`, `ci-run.sh`, `mkdocs_hooks.py`. The
  FIX-038 sweep was never committed.

---

## 1 Symptoms

None of these produced a field report. They were found by the pre-merge gates
on the branch that introduced them: nine of the ten notes below came from the
doc-blind external review (three lenses, `agy`/gemini-3.1-pro-high), and §1.1 —
the most serious — came from verifying one of those notes against the caller,
which is not in the branch diff.

That provenance is the point of §5. The branch had passed `make ci` green,
100% diff-coverage, `golangci-lint` clean and a previous round of the same
external review before any of this surfaced.

### 1.1 The hub's global lock is held across a call into the host's broker

`AddEventProcessor` now applies correlation keys to the live broker
subscription:

```go
// waiters/message.go:191-192
for _, k := range keys {
    if err := sub.AddKey(k); err != nil {
```

`sub` is a `messaging.Subscription` — an interface the **host** implements, over
any queue, possibly remote, possibly slow. Both callers hold `eh.m` across it:

```go
// eventhub.go:292-307 — the FAST path, taken by every join
eh.m.Lock()
defer eh.m.Unlock()
…
if err := w.AddEventProcessor(ep); err != nil {
```

```go
// eventhub.go:328-340 — the lost-race path
eh.m.Lock()
…
addErr = winner.AddEventProcessor(ep)
```

While a broker is slow, no waiter can be registered, unregistered or looked up
anywhere in the engine. **This is FIX-038 §1.1 verbatim** — the same lock, the
same broker — reintroduced **three days** after it was fixed (FIX-038 is dated
2026-08-08), by a change that never touched `eventhub.go`.

It is the common path, not an edge: a Multi-Instance iteration parking on a
message catch its sibling already parked on takes it every time.

### 1.2 The window between applying a key and registering its processor eats a message

The order is: apply every key, then append `ep`.

```go
// waiters/message.go:191-212
for _, k := range keys {
    if err := sub.AddKey(k); err != nil { … }
}
mw.m.Lock()
if idx := slices.Index(mw.processors, ep); idx == -1 {
    mw.processors = append(mw.processors, ep)
}
```

The moment `AddKey` returns, the broker routes envelopes for that key to this
waiter. If one arrives before the append, `deliver` snapshots a processor list
that does not contain `ep` (`:460-462`), forwards the event to the processors
that *are* listed, and each drops it on its own correlation gate (ADR-017 v.1
§2). The envelope is **consumed from the broker and lost** — the iteration it
was addressed to stays parked forever.

That is #320's symptom, restored by the code written to prevent it. The
previous review round moved the append after the keys to avoid a half-joined
processor; the trade was a lost message for a half-join, and a lost message is
the worse of the two.

### 1.3 A partial key failure leaves keys on the subscription with no processor behind them

If a processor brings two keys and the second is refused, the first is already
applied and cannot be taken back: **the port has no `RemoveKey`**
(`messaging.go:23-37`). `AddEventProcessor` returns an error and does not
register `ep`, so the subscription now routes a key no processor answers to —
and every envelope for it is consumed and dropped exactly as in §1.2, silently,
for as long as the waiter lives.

Unreachable through `membroker`, whose `AddKey` never fails. Reachable through
any host adapter, which is what the port exists for.

### 1.4 `Service` leaks the subscription when a buffered key is refused

```go
// waiters/message.go:320-332
for _, k := range late {
    if aerr := sub.AddKey(k); aerr != nil {
        mw.m.Lock()
        mw.state = eventproc.WSFailed
        mw.m.Unlock()

        return errs.New(…)
    }
}
```

`Subscribe` has already succeeded, so `sub` is live and registered at the
broker. The serving goroutine is started **after** this loop (`:341`), so
nothing will ever read `sub.C()`, and nothing calls `Unsubscribe`. The
subscription stays at the broker claiming matching messages into a channel with
no reader — the swallowing failure the port's own doc warns about
(`messaging.go:31-35`).

Two of the three review lenses reported this independently. The test written
for this exact path one commit earlier
(`TestKeyRefusedDuringSubscribeFailsTheWaiter`) asserts `WSFailed` and never
asks what happened to the subscription.

### 1.5 A processor that joins before `Service` has its keys counted twice

```go
// waiters/message.go:180-186 — sub == nil
mw.processors = append(mw.processors, ep)
mw.pendingKeys = append(mw.pendingKeys, keys...)
```

```go
// waiters/message.go:289-298 — Service
keys := append([]string(nil), mw.pendingKeys...)
…
for _, p := range procs {
    keys = append(keys, processorKeys(p)...)
}
```

`ep` is in `procs` and its keys are in `pendingKeys`, so `Subscribe` receives
each of them twice. Harmless in `membroker` — `keys` is a `map[string]struct{}`
(`membroker.go:48`) and duplicates collapse — but the port is host-implementable
and an adapter that turns each key into a queue-level registration will make two.

No test covers `AddEventProcessor` before `Service`, which is why it was not
noticed.

### 1.6 `Service` reads and writes the waiter state outside the lock it added

`Service` checks `mw.state` at `:280`, sets `WSFailed` at `:302`, sets
`WSRunned` at `:334` and assigns `mw.stopCh`/`mw.done` at `:335-336` — all
unlocked — while `State()` (`:538-543`), `setState` (`:546-550`) and `Stop()`
(`:505-535`) guard the same fields with `mw.m`. The branch itself added a
*locked* write at `:322-324`, three lines from an unlocked one.

**This is not a demonstrated race.** Every unlocked write completes before the
waiter is shared: `registerWaiter` calls `Service` (`eventhub.go:276`) before
`publishWaiter` inserts it into the map (`:343`), and the serving goroutine
starts after the last of them. It is an inconsistency that makes the next
reader's reasoning wrong, and it is four lines to remove.

### 1.7 Three tests start a waiter and never stop it

`TestJoiningProcessorBringsItsKey`, `TestKeyAddedBeforeSubscribeSurvives` and
`TestLazyKeyReachesALiveSubscription` call `w.Service(ctx)` — which spawns a
goroutine and registers a broker subscription — and never call `Stop`. Only
`TestJoiningProcessorWithARefusedKeyDoesNotHalfJoin` has
`t.Cleanup(func() { _ = w.Stop() })`.

### 1.8 The correlation-key capability has no named contract

`processorKeys` detects it with an anonymous interface:

```go
// waiters/message.go:220-224
kp, ok := ep.(interface {
    CorrelationKeys() []string
})
```

Two types implement it (`internal/instance/correlation.go:219`,
`pkg/thresher/subscription_holder.go:40`) and nothing declares it, so neither
implementor can be checked against it and a third would be written by imitation.
`eventhub.go:869-871` does the same for `AddKey`.

*(The external note framed this as a problem for host implementers.
`internal/eventproc` is an internal package — no host can implement
`EventProcessor` at all. The in-repo discoverability cost is real; the stated
premise is not.)*

### 1.9 `FailingBroker`'s configuration contract is unstated

`SubscribeErr`, `AddKeyErr`, `AddKeyAfter` and `OnSubscribe` are exported,
mutable, and read in `Subscribe` without `b.mu`, while the type carries a mutex
for its subscription list. A caller that reconfigures the double after handing
it to the engine races, and the race detector will name `failing.go`.

### 1.10 Not a defect: the doc comments

One note asked for `FailingBroker`'s documentation to be rewritten
caller-first, on the grounds that it reads as an internal record. It already
opens with *"FailingBroker is a `messaging.MessageBroker` whose operations fail
on demand"*, and narrative *why*-comments are this project's documented house
style. Recorded here because a rejected note whose reason is written down does
not come back next release.

---

## 2 Root cause

**§1.1 has a specific cause, and it is a stale justification, not an oversight.**
`registerWaiter`'s doc comment says holding `eh.m` across `AddEventProcessor` is
safe because it *"never re-enter[s] eh.m"* (`:235`), and the fast path repeats
it: *"That is registry work and stays under the lock"* (`:243-244`). Both were
true when written, and the re-entrancy claim is **still** true. What changed is
a different property — the method became foreign — and no comment mentions
foreignness because, at the time, `AddEventProcessor` had none to mention.

So the author of the #320 fix read a comment asserting the call was safe under
the lock, and the comment was not wrong about the thing it asserted. FIX-038
established the foreignness rule and applied it to `Service`; nothing propagated
it to the sibling call in the same function.

**§1.2, §1.3 and §1.4 share a cause:** the #320 fix moved key application into
paths that previously had none, and each new error branch was reasoned about
locally — *don't leave a half-joined processor*, *report the failure* — without
asking what the broker is left holding when the branch is taken. FIX-038 §5
recorded this exact lesson (*"The remedy's own error branches need the same
coverage as the defect's"*) after its own gate run failed at 91.7%; here the
gate passed at 100% because the branches are covered — the tests assert the
return value and the state, and nothing asserts what the broker was left with.

**§1.5** is a double-source bug: the same key legitimately has two homes
(the processor that owns it, and the buffer that survives the subscribe window),
and `Service` reads both without asking whether they overlap.

**§1.6, §1.7, §1.9** are hygiene the branch did not introduce but did touch.
They are fixed here because the person holding the context is the one reading it
now.

---

## 3 Solution

### 3.1 Decision

Four decisions, in dependency order. D1 subsumes §1.2 for free: once the
registry step and the foreign step are separated, the safe ordering is the only
one available.

#### D1 — split the join: registry work under the hub lock, key application outside it

`AddEventProcessor` becomes registry-only again, exactly as on master. Applying
the joining processor's keys moves to a separate, explicitly optional step the
hub invokes **after** releasing `eh.m`, modelled on `AddEventKey`
(`eventhub.go:861-876`): lookup and register under the lock, capability-assert,
call outside.

The ordering this produces — *register `ep`, then apply its keys* — is the
inverse of today's and is the correct one. Its window (processor listed, key not
yet subscribed) drops nothing: the broker routes no envelope for a key it has
not been given, and an unmatched envelope waits in the broker's inbox — which is
not merely `membroker`'s behaviour but the stated contract, ADR-006 v.5 §2.4:
*"An external message arriving before its `ReceiveTask` / catch subscribes is
held in the `MessageBroker`'s inbox and delivered on subscribe."* Today's window
(key subscribed, processor not listed) consumes and discards. On failure the hub removes the processor it
registered, so no half-join survives either.

Crucially, the register-then-apply order also closes the race that makes the
naive fix wrong: because `ep` is already in the waiter's list when the lock is
released, a concurrent `UnregisterEvent` sees it and cannot strand it against a
waiter it is tearing down — FIX-038 §1.3's defect, which a "just move the call
after the unlock" fix would reopen.

#### D2 — a key that cannot be applied fails the waiter, and the waiter unsubscribes

The port has no `RemoveKey`, so a partially-applied key-set cannot be repaired
in place (§1.3). It can be discarded: `Unsubscribe` drops the whole
subscription, orphan keys and all, and the messages those keys would have eaten
go back to waiting in the broker's inbox for a subscription that wants them
(ADR-006 v.5 §2.4, quoted above — this is why discarding is safe rather than
lossy).

So every key-application failure — the join path (§1.3) and `Service`'s late-key
path (§1.4) — ends the same way: `Unsubscribe`, `WSFailed`, error returned.

**This is not a waiter removing itself.** ADR-006 v.5 §2.5 is explicit — *"A
waiter **never removes itself**; on trigger/completion it signals the hub … and
the hub does the removal"* — and it stays true here: the waiter drops its own
broker subscription (which it already does in `Stop` and in the serving
goroutine's `defer`) and marks itself failed. Removal from the registry remains
the hub's, on the failed registration it is already returning.

#### D3 — de-duplicate the key-set before subscribing

`Service` de-duplicates `keys` before `Subscribe` (§1.5). Removing the buffering
instead would be wrong: a processor joining between `Service`'s snapshot
(`:289-293`) and the publication of `mw.sub` (`:314-318`) genuinely needs the
buffer, and that window is the one #320 was lost in.

#### D4 — name the two optional waiter/processor capabilities

`CorrelationKeys() []string` (§1.8) and the waiter-side key seam get declared
interfaces in `eventproc`, and every ad-hoc assertion — `processorKeys`,
`AddEventKey`, and D1's new call site — asserts against the named type.
`EventWaiter` itself is **not** widened: signal and timer waiters have no keyed
subscription, and a mandatory method they must stub is worse than an optional
one they simply do not implement.

### 3.2 Alternatives considered

| # | Alternative | Pros | Cons | Decision |
|---|---|---|---|---|
| A1 | Move `AddEventProcessor` out of the lock unchanged | one-line diff | reopens FIX-038 §1.3: between unlock and join, `UnregisterEvent` can stop and unmap the waiter, and the processor attaches to a corpse — events never arrive, no error anywhere | ❌ |
| A2 | Keep the call under the lock; document it as accepted | no code change | it is the engine-wide lock and a remote broker; FIX-038 already rejected this reasoning for the same lock | ❌ |
| A3 | Apply keys asynchronously after the join returns | lock held briefly, no ordering problem | a refused key becomes a background failure with no caller to report to — #320 was *precisely* a key that failed silently | ❌ |
| A4 | **Split registry work from key application (D1)** | restores the `:235` invariant; safe ordering falls out; matches `AddEventKey`'s established shape | the hub must undo its registration when application fails | ✅ |
| B1 | Add `RemoveKey` to the `Subscription` port | a partial failure could be repaired exactly | a port change breaking every host adapter, to serve an error path; that is an ADR-level contract decision, not a fix | ❌ (§7) |
| B2 | Leave orphan keys, log a warning | smallest change | the orphan key silently eats every message addressed to it — the #320 failure mode, made permanent | ❌ |
| B3 | **Discard the subscription (D2)** | uses only what the port has; unmatched messages return to the inbox and survive | one refused key costs the waiter its subscription, including for processors already parked on it | ✅ |
| C1 | Stop buffering a joining processor's keys | removes the duplication at the source | loses the key of a processor that joins during the subscribe window — the #320 bug | ❌ |
| C2 | **De-duplicate before `Subscribe` (D3)** | correct regardless of how a key reached the set | one `slices.Compact`-shaped pass | ✅ |

B3's cost deserves its own sentence, because it is the one place this fix
chooses the harsher behaviour: a single refused key tears down a subscription
other processors were using. It is chosen because the alternative is not
"they keep working" — it is "they keep working while an orphan key eats
messages addressed to somebody else". A loud, recoverable failure beats a quiet,
permanent one.

### 3.3 `internal/eventproc/eventproc.go` — name the optional capabilities

Declare the two capabilities `EventWaiter`/`EventProcessor` do **not** mandate.
Doc comments state the optionality and who satisfies it: a plain track has no
correlation keys, and a timer waiter has no keyed subscription.

```go
// KeyedProcessor is an EventProcessor that answers to correlation keys.
// OPTIONAL: a plain track implements it not at all; an instance carrying
// conversation or iteration keys does. Satisfied by *instance.Instance and
// thresher's subscription holder.
type KeyedProcessor interface {
    EventProcessor

    CorrelationKeys() []string
}

// KeyedWaiter is an EventWaiter whose delivery is filtered by correlation key,
// and whose key-set therefore grows after the fact. OPTIONAL: only the message
// waiter has a keyed broker subscription; signal and timer waiters have none,
// which is why this is not part of EventWaiter.
//
// ApplyProcessorKeys is FOREIGN — it calls the host's broker — and MUST be
// invoked with no engine lock held (FIX-038 §1.1, FIX-041 §1.1).
type KeyedWaiter interface {
    EventWaiter

    AddKey(key string) error
    ApplyProcessorKeys(ep EventProcessor) error
}
```

`EventWaiter` itself gains nothing, so `signalWaiter`
(`waiters/signal.go:269`) and `timeWaiter` (`waiters/timer.go:491`) are
untouched and their mocks keep their current shape.

### 3.4 `internal/eventproc/eventhub/waiters/message.go` — the waiter side

- `AddEventProcessor` returns to registry-only: duplicate check, append, no
  foreign call, no key handling. (§1.1, §1.2)

  ```go
  // before — foreign call inside, ep appended only after it succeeds:
  keys := processorKeys(ep)
  mw.m.Lock()
  … duplicate check …
  if mw.sub == nil { mw.processors = append(…); mw.pendingKeys = append(…); return nil }
  sub := mw.sub
  mw.m.Unlock()
  for _, k := range keys { if err := sub.AddKey(k); err != nil { return … } }
  mw.m.Lock(); mw.processors = append(mw.processors, ep); mw.m.Unlock()

  // after — registry only, the shape master had:
  mw.m.Lock()
  defer mw.m.Unlock()
  if idx := slices.Index(mw.processors, ep); idx == -1 {
      mw.processors = append(mw.processors, ep)
  }
  return nil
  ```

- `ApplyProcessorKeys(ep)` is the new foreign step: it reads `ep`'s keys,
  applies them to the live subscription, or buffers them when there is no
  subscription yet; on any failure it unsubscribes, sets `WSFailed` and
  returns the error. (§1.3)

  ```go
  func (mw *messageWaiter) ApplyProcessorKeys(ep eventproc.EventProcessor) error {
      keys := processorKeys(ep)          // outside the lock: a call into ep
      if len(keys) == 0 { return nil }

      mw.m.Lock()
      if mw.sub == nil {                 // no subscription yet — BUFFER
          mw.pendingKeys = append(mw.pendingKeys, keys...)
          mw.m.Unlock()
          return nil
      }
      sub := mw.sub
      mw.m.Unlock()

      for _, k := range keys {
          if err := sub.AddKey(k); err != nil {
              return mw.discardSubscription(k, err)   // D2
          }
      }
      return nil
  }
  ```

  The `mw.sub == nil` branch **must keep buffering**, and this is the one place
  the design is easy to get wrong: it is tempting to drop it, reasoning that a
  registered processor is in `Service`'s `procs` snapshot and will have its keys
  read there. That is false for precisely the window #320 was lost in — a
  processor joining *after* `Service` snapshots `procs` (`:289-293`) but *before*
  it publishes `mw.sub` (`:314-318`) is in neither the snapshot nor the
  subscription, and its key would vanish exactly as before. This is alternative
  C1, rejected in §3.2; the resulting double-count is D3's job, not this
  branch's.
- `Service`'s late-key failure path calls `sub.Unsubscribe()` before returning.
  (§1.4)
- `Service` de-duplicates `keys` before `Subscribe`. (§1.5)
- `Service` takes `mw.m` for every `state`/`stopCh`/`done` access. (§1.6)
- `processorKeys` asserts the named `KeyedProcessor`. (§1.8)

### 3.5 `internal/eventproc/eventhub/eventhub.go` — the hub side

- `joinExistingWaiter` keeps its critical section for lookup and registration,
  and returns the waiter it joined; the caller applies keys after `eh.m` is
  released, and unregisters the processor if that fails. (§1.1)

  ```go
  // registerWaiter, fast path:
  joined, w, err := eh.joinExistingWaiter(ep, eDef)   // eh.m held, registry only
  if err != nil { return err }
  if joined {
      return eh.applyKeys(w, ep)                      // eh.m released — FOREIGN
  }
  ```

  This keeps ADR-006 v.5 §2.5's *"Register / unregister / propagate are atomic
  with respect to the registry"* intact: what moves out of the critical section
  is the broker call, not the registry mutation. And because `ep` is registered
  before the lock is released, a concurrent `UnregisterEvent` sees it — the
  reason A1 (move the whole call out) is unsafe and this is not.
- `publishWaiter`'s lost-race branch does the same. (§1.1)
- `AddEventKey`'s anonymous assertion uses the named interface. (§1.8)
- The stale justification at `:231-237` and `:243-244` is rewritten to say what
  is now true, and *why* the property that matters is foreignness.

### 3.6 `pkg/messaging/messagingtest/failing.go` — state the double's contract

A doc sentence: configuration fields are read when the engine calls the broker
and are not guarded; set them before handing the broker over. No setters — no
caller needs concurrent reconfiguration, and the API weight would serve none.
(§1.9)

### 3.7 Tests

`t.Cleanup(func() { _ = w.Stop() })` in the three tests that lack it. (§1.7)

---

## 4 Verification

### 4.1 Regression tests

Each fails on the pre-fix code; that is verified per test, not assumed.

| # | Test | New/extended | Setup | Assertion |
|---|---|---|---|---|
| T-1 | `TestJoinDoesNotHoldTheHubLock` | new, `eventhub` | a broker whose `AddKey` blocks on a channel; one goroutine joins a live waiter, another calls a hub lookup | the lookup completes while `AddKey` is still blocked — on the pre-fix code it blocks until released (§1.1) |
| T-2 | `TestJoinHalvesAreSeparable` (`waiters`) + `TestJoinRegistersBeforeItReachesTheBroker` (`eventhub`) | new | a broker recording, and probing from inside, its `AddKey` | the registry half reaches no broker; the hub lists the processor before its key reaches one (§1.2 — see §8.2.1) |
| T-3 | `TestPartialKeyFailureDiscardsTheSubscription` | new, `waiters` | `FailingBroker{AddKeyAfter: 1}`, a processor with two keys | the join fails, the processor is not listed, `Unsubscribe` was called, waiter `WSFailed` (§1.3) |
| T-4 | `TestKeyRefusedDuringSubscribeFailsTheWaiter` | **extended** | as today | additionally: the subscription was unsubscribed (§1.4) |
| T-5 | `TestJoinBeforeServiceSubscribesEachKeyOnce` | new, `waiters` | join a keyed processor before `Service` | `Subscriptions()[0].Keys` contains each key exactly once (§1.5) |
| T-6 | existing suite under `-race` | — | — | `internal/eventproc/...` clean (§1.6) |

T-1 is the one that matters most and the one nothing in the suite resembles
today: it asserts a *timing* property, so it needs a broker that blocks on
command. `FailingBroker` gains that (a channel the test closes), which is the
same reason it exists — the real broker cannot be made to hold still.

### 4.2 Gate

`make ci` green on the final commit, judged by `.ci/last-run.json`, with
diff-coverage ≥ `COVER_MIN` on every touched file. `make gen_mock_files` after
the `eventproc` interface additions.

### 4.3 What the tests must assert about the broker, not just the return value

§1.3 and §1.4 both passed a green gate because the tests asserted the error and
the state and stopped there. Every test above asserts what the **broker** was
left holding — keys applied, subscriptions closed. That is the observable the
bug lives in.

---

## 5 Prevention

- **A rule written next to one call does not reach its sibling.** FIX-038 moved
  `Service` out of `eh.m` and left `AddEventProcessor` inside it, correctly, on
  the strength of a re-entrancy argument. When this branch changed what
  `AddEventProcessor` does, the comment at `:235` still read as true. The
  comments rewritten in §3.5 name **foreignness** as the property, so the next
  person to add a call inside that lock is asked the question that matters.

- **The FIX-038 sweep was never committed, and the shape came back.** Its §5
  says the third scanner "is the only one worth running" — `ls scripts/` shows
  it was not kept. A sweep that exists only as a description in a document is
  re-derived, badly, or not at all. This fix commits it as a script with a
  `make` target so it can be re-run, and re-runs it on the pre-fix tree to
  confirm it **fails** there. A scanner that reports clean is the dangerous
  failure mode.

  It is deliberately not wired into the REQUIRED gate in this landing: a new
  blocking check needs its false-positive rate measured against the whole tree
  first, and that measurement is part of this work, not a promise about it.
  Landed as `scripts/lock-sweep.py` + `make lock-sweep`; it found two live
  instances of the shape on its first run, in files this branch never touched
  (§8.2.4), and its limits are stated in §8.2.5.

- **Error branches get asserted on their effects, not their return values.**
  §4.3. Three of the ten findings are error paths that returned the right error
  while leaving the broker wrong.

- **Doc comments** on every changed exported symbol explain why, and name the
  §4.1 test that guards them — if it falls, the fix regressed.

---

## 6 Regressions and side effects

### 6.1 What may rely on the old behaviour

- `grep -rn 'AddEventProcessor' --include='*.go'` → the interface declaration,
  the two hub call sites, three waiter implementations, generated mocks and the
  tests. Any caller relying on `AddEventProcessor` applying keys must move to
  the new step; the compiler finds them all, since the behaviour leaves the
  method entirely.
- Mocks regenerate (`make gen_mock_files`); `mock-check` fails the gate if they
  are not committed.
- Signal and timer waiters are untouched: they implement neither optional
  interface, and D4 keeps `EventWaiter` unwidened precisely so they stay that
  way.

### 6.2 Behaviour change visible to a host

A refused correlation key now tears the subscription down (D2) where it
previously left it partially keyed. A host broker that refuses keys will see
`Unsubscribe` followed by a fresh `Subscribe` on the next registration, instead
of a subscription silently accumulating keys nobody answers to. `membroker`
never refuses, so no in-repo behaviour changes.

### 6.3 Rollback

Single-branch revert. The fix touches no persisted state, no wire format and no
public package outside `messagingtest`'s doc comment.

---

## 7 Related

- [FIX-038](FIX-038-locks-across-host-calls-and-lost-registrations.md) §1.1 —
  the same lock across the same broker. §1.1 here is its recurrence; §5 is why.
- [FIX-036](FIX-036-thresher-lifecycle-races-and-reservations.md) §1.5 — the
  first appearance of the shape (host code inside the producer). FIX-038
  observed it "occurred twice more"; this is the fourth site, and the first
  where the engine *introduced* it rather than inherited it.
- **Promote-to-ADR candidate:** `Subscription.RemoveKey` (alternative B1). A
  port that can only grow a key-set forces D2's discard-and-rebuild. One source
  is not enough to change a host-facing contract; a second — an adapter that
  needs to shed keys for its own reasons — would make it an ADR.

---

## 8 Implementation summary

### 8.1 Stage commits

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| Doc | `29435458` | this document | — |
| Double | `514db68e` | `messagingtest`: `OnAddKey`, `Unsubscribed`, the configuration contract (§3.6) | `TestFailingSubscriptionRunsTheAddKeyHook`, `TestFailingSubscriptionReportsUnsubscribed` |
| Fix | `5de0cf54` | D1–D4: `eventproc` capabilities, the waiter split, the hub side (§3.3–§3.5, §3.7) | T-1…T-6, below |
| Changelog | `b139ba7b` | `[Unreleased]` entry for #320 | — |
| Sweep | `be402c39` | `scripts/lock-sweep.py` + `make lock-sweep`, and the two findings it produced | §8.2.4 |
| Guard | `96909a70` | `ApplyProcessorKeys` refuses a failed waiter; the last uncovered branches | `TestApplyProcessorKeysGuardsItsInput`, `TestApplyProcessorKeysRejectsAFailedWaiter` (§8.2.7) |

Gate: `make ci` PASS at `b139ba7b`, `be402c39` and `96909a70` — the last at
**100.0% diff-coverage of 259 changed lines** across all four touched files —
judged by `.ci/last-run.json` rather than by the exit code of whatever wrapped
the run (FIX-039).

### 8.2 Empirical findings

**8.2.1 — T-2 as drafted asserted nothing.** §4.1's T-2 was to observe, at the
waiter, that a joining processor is listed before its key reaches the
subscription. But the waiter does not decide that order — the **hub** does, and
a waiter-level test performs both halves itself, so it would have asserted the
order of its own test helper. It was replaced by two tests that each pin a real
property: `TestJoinHalvesAreSeparable` (waiter) — `AddEventProcessor` reaches no
broker and `ApplyProcessorKeys` is the only half that does — and
`TestJoinRegistersBeforeItReachesTheBroker` (hub), which probes from inside the
broker call, the only place the order is visible.

**8.2.2 — discarding the subscription kills the waiter, and §3 did not say who
buries it.** D2 has the waiter unsubscribe and fail. §3.1 said registry removal
"remains the hub's, on the failed registration it is already returning" — but
the hub was only going to unregister the *processor*. That leaves a waiter in
the registry with no subscription and a exiting goroutine: every later
registration for the same definition joins it and receives nothing, forever,
with no error anywhere. That is FIX-038 §1.3's stranding reached from the other
direction, and it would have been *introduced* by the fix. `dropFailedWaiter`
unmaps it, so the next registration builds a fresh one
(`TestRefusedJoinKeyLeavesNoWaiterBehind`).

**8.2.3 — a failed waiter was relabelled by the goroutine its own failure
woke.** Unsubscribing closes the channel `runMessageService` is selecting on, so
it wakes at once and set `WSStopped` over the `WSFailed` just written — an
orderly-shutdown label on a break. `setStateUnlessFailed` refuses the downgrade,
and `discardSubscription` writes the state **before** unsubscribing so the
refusal is deterministic rather than a coin toss. Both halves were needed: with
only the guard, the anti-fix sweep below still passed, because the two writes
raced. T-3 now waits on `Done()` before reading the state, which also pins that
the goroutine exits at all.

**8.2.4 — the sweep found two live instances of the very shape, in code this
branch had not touched.** §5's second bullet proposed committing the FIX-038
sweep. Committed as `scripts/lock-sweep.py` + `make lock-sweep`, its first run
reported:

- `waiters/message.go:635` — `Stop` held `mw.m` across `sub.Unsubscribe()`, a
  host call, blocking `State`, `EventProcessors` and every delivery snapshot
  behind it. Fixed: the state is set and `stopCh` closed under the lock, the
  unsubscribe happens after it is released — still synchronous, so SRD-031.A
  FR-7 (a replacement waiter must not race a live subscription) still holds.
- `pkg/rules/gorules/gorules.go:80` — `Register` held `reg.mu` across
  `reg.sink.Report()`, and the sink is whatever the host passed to
  `BindReporter`. Fixed the same way. `Evaluate`, forty lines below, already
  read under the lock and called outside it, which is what makes `Register` an
  outlier rather than the local convention.

Neither is in this branch's diff, so no review lens and no `/check-style` pass
could have reached them. That is the argument for the sweep existing, and it
paid for itself on its first run.

**8.2.5 — what the sweep would NOT have caught is #320 itself.** It is
syntactic: it sees a host call written inside a critical section and misses one
reached through a helper — which is exactly the shape of §1.1, where
`AddEventProcessor` was two frames from the lock. The script's docstring says so
first, because a scanner that reports clean is the dangerous failure mode
(FIX-038 §5). It is verified to FAIL on the pre-fix copies of both files above
and pass on the fixed tree.

**8.2.6 — `Done()` was reading `mw.done` unlocked.** §3.4 listed only `Service`
for §1.6. `Done()` reads the same field with no lock while every other path
guards it; now it takes `mw.m`.

**8.2.7 — D2 opened a way to restore #320's silence, and §3 did not see it.**
Once a failed waiter has given its subscription back, `mw.sub` is nil again — so
`ApplyProcessorKeys`'s buffering branch would take a joining processor's keys
into `pendingKeys` and **return nil**, for a waiter the hub has unmapped and
nothing will ever service. A registration that read the waiter out of the
registry just before another one failed it lands exactly there. That is #320's
failure mode — a key accepted, never routed, no error — reachable only through
the fix for #320. `ApplyProcessorKeys` now refuses a `WSFailed` waiter
(`TestApplyProcessorKeysRejectsAFailedWaiter`).

The same pass closed the branches the diff-coverage gate left at 98.0%:
`ApplyProcessorKeys` with a nil and with a keyless processor
(`TestApplyProcessorKeysGuardsItsInput` — a keyless processor must not touch the
subscription at all, since a keyless subscription is a wildcard a spurious
`AddKey` would silently narrow), and `dropFailedWaiter`'s already-unmapped
branch, which was removed rather than tested: the Error log belongs on both
paths, so only the unmapping is conditional now.

### 8.3 What this fix deliberately leaves alone

- **`make lock-sweep` is not in the required gate.** §5 said this would be so
  and why: a blocking check earns its place after its false-positive rate is
  measured across the tree over more than one landing. This run produced two
  findings and zero false positives, which is one data point, not a rate.
- **`Subscription.RemoveKey`** (alternative B1) stays a promote-to-ADR
  candidate, unchanged from §7 — one source is not enough to change a
  host-facing port contract.

---

## 9 Open questions

None.
