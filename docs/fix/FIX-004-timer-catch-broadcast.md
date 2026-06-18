# FIX-004 «Concurrent timer-catch instances share one waiter — a timer fires them all»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted v.1 (2026-06-18, branch `fix/timer-catch-broadcast`, implemented).
**Date:** 2026-06-17.
**Author:** Ruslan Gabitov.
**Branch:** `fix/timer-catch-broadcast` (one focused defect — the timer event definition gains per-instance identity, exactly as the message definition did in SRD-017).
**Paired doc:** none (a point fix; the per-instance-eDef-identity mechanism is the SRD-017 precedent — see §7).
**Upstream:** [SRD-017 v.1](../srd/SRD-017-conversation-token-threading.md) (the message-scoped precedent — `CloneForInstance`, §4.3); [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) (the EventHub waiter model); [ADR-009 v.1](../design/ADR-009-per-instance-node-graph.md) (each instance owns a private node-graph clone).

**Grounded in (internal artifacts):**
- SRD-017 fixed this **for message event definitions only** (`MessageEventDefinition.CloneForInstance`, M3a) and explicitly deferred the non-message types to "its own FIX" (SRD-017 §4.3 final note). This is that FIX.
- Every claim below re-verified against `master` at `221ea3f` (post-SRD-017 merge) — line numbers are current.

## §1 Symptoms

### §1.1 Symptom: a single timer occurrence resumes every concurrent instance waiting on the same timer catch

Two (or more) running instances of the same process, each parked at the same
intermediate **timer** catch event, share **one** EventHub waiter. When the
timer fires, the waiter resumes **all** of them at once — instead of each
instance's own timer resuming only that instance. This is wrong per BPMN
(§10.5: a catch event resumes the token in *its* process instance) and it is
non-deterministic from the operator's view: N instances complete on one timer.

It is the exact analogue of the message broadcast bug SRD-017 fixed, but for
timers — and it is currently **masked** because no test runs two concurrent
instances parked on the same timer catch.

```
instance A ─┐
            ├─ both parked at intermediate timer catch "wait-15m" (same eDef id)
instance B ─┘
   timer fires once  ─▶  ONE shared waiter ─▶ fireDefinition → resumes A AND B
```

In code: a timer intermediate catch is registered through
`internal/instance/track.go:310` (`t.instance.RegisterEvent(t, d)`); the
EventHub keys waiters by `eDef.ID()` and **merges** a second registration of
the same id onto the existing waiter via `AddEventProcessor`
(`internal/eventproc/eventhub/eventhub.go:173`). Because both instances' timer
eDefs carry the **same** id (see §2), they land on one waiter; when it fires it
loops every registered processor — both tracks.

## §2 Root Cause Analysis

### §2.1 `Event.clone()` shares non-message event definitions by reference

Each instance owns a private clone of the node graph (ADR-009). Node cloning
runs through `Event.clone()` (`pkg/model/events/event.go:161`), whose
`cloneDefsForInstance` (`event.go:175`) gives a **fresh per-instance id only to
definitions implementing the optional `CloneForInstance` interface**:

```go
func cloneDefsForInstance(defs []flow.EventDefinition) []flow.EventDefinition {
	out := make([]flow.EventDefinition, len(defs))
	for i, d := range defs {
		if c, ok := d.(interface {
			CloneForInstance() flow.EventDefinition
		}); ok {
			out[i] = c.CloneForInstance()
			continue
		}
		out[i] = d            // <-- shared by reference: same id across instances
	}
	return out
}
```

Only `MessageEventDefinition` implements `CloneForInstance`
(`pkg/model/events/message.go:155`, added by SRD-017 M3a). `TimerEventDefinition`
(`pkg/model/events/timer.go:11`) does **not** — so every instance's clone keeps
the **template** timer eDef (the `out[i] = d` branch), i.e. the same
`eDef.ID()`. The EventHub then merges them onto one waiter (§1.1).

### §2.2 Timer is the only non-message type that can actually collide today

An IntermediateCatchEvent permits conditional / signal / timer triggers
(`pkg/model/events/intermediate_catch.go:18` `intermediateCatchTriggers`), but
the waiter factory builds a waiter for **only** timer and message:

```go
// internal/eventproc/eventhub/waiters/waiters.go (CreateWaiter)
case flow.TriggerTimer:   w, err = NewTimeWaiter(eh, ep, eDef, "", rt)
case flow.TriggerMessage: w, err = NewMessageWaiter(eh, ep, eDef, "", rt, true)
default:                  err = ... "couldn't find builder for ... %s"
```

So a **signal** or **conditional** catch can be *modelled* but **fails to
register a waiter** (the `default` error) — it cannot wait in-instance, so it
cannot hit this bug. Message is already fixed (SRD-017). **Timer is therefore
the only catchable, waiter-backed, non-message event definition that exhibits
the broadcast** — making this FIX timer-scoped.

### §2.3 Why no test caught it

`grep` of the timer tests (`internal/eventproc/eventhub/eventhub_timer_test.go`,
`pkg/model/events/*timer*_test.go`) shows none registers **two** processors on
one timer eDef and asserts only one fires; the per-instance-identity canary
(`pkg/model/events/clone_for_instance_test.go`) covers **message** only
(`TestMessageReceiverPerInstanceClone`). The gap is the absence of a
timer-distinct-id / no-broadcast assertion.

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. Add `CloneForInstance` to **`TimerEventDefinition`** (structural, mirroring `message.go:155`) | One method; no `Event.clone` change (its optional-interface check already applies it); no `flow.EventDefinition` interface change; no mock regen; matches the SRD-017 pattern exactly | Per-type (must be repeated when another catchable type gains a waiter) | ✅ chosen |
| B. Add `CloneForInstance` to the **`flow.EventDefinition` interface** (force every impl) | Compile-time guarantee every eDef is per-instance | Touches ~10 eDef impls + the interface + `mockflow` regen, for types that **cannot** wait today (no waiter builder) — churn with no current payoff (YAGNI) | ❌ rejected (revisit if/when many types become catchable) |
| C. Key EventHub waiters by `(eDefID, processorID)` instead of `eDefID` | Fixes all event types at once, no model change | Ripples the FIX-003-hardened removal core (`WaiterFired`/`RemoveWaiter`/`UnregisterEvent` all key by `eDefID`); larger blast radius for a one-type bug | ❌ rejected |

Option A is the minimal, precedent-consistent fix for the one type that is
actually affected.

### §3.2 Changes by file

#### §3.2.1 `pkg/model/events/timer.go` — `CloneForInstance` with a fresh id

```go
// CloneForInstance returns a per-instance copy of the TimerEventDefinition with
// a FRESH id, sharing the (immutable) timer expressions by reference. Node
// cloning (Event.clone) uses it so each process instance's timer catch registers
// a DISTINCT EventHub waiter: without it concurrent instances waiting on the same
// timer would share one waiter and a single timer occurrence would resume them
// all (FIX-004; the timer analogue of MessageEventDefinition.CloneForInstance,
// SRD-017 §4.3). A timer carries no payload, so there is no fire-path CloneEvent
// to keep id-stable — only the registration identity must be per-instance.
func (ted *TimerEventDefinition) CloneForInstance() flow.EventDefinition {
	return &TimerEventDefinition{
		definition:   definition{BaseElement: *foundation.MustBaseElement()},
		timeDate:     ted.timeDate,
		timeCycle:    ted.timeCycle,
		timeDuration: ted.timeDuration,
	}
}
```

Adds the `foundation` import to `timer.go`. No other production change:
`Event.clone`'s `cloneDefsForInstance` already routes any `CloneForInstance`
implementer through the fresh-id path (§2.1), so the timer clone becomes
per-instance automatically.

## §4 Verification

Current coverage in the test dir:
- unit: timer waiter delivery / lifecycle exist, but **no** per-instance-identity
  or two-instance-no-broadcast assertion for timers (§2.3).
- the message canary (`TestMessageReceiverPerInstanceClone`) is the shape to mirror.

### §4.1 Regression tests (mandatory)

#### §4.1.1 `TestTimerReceiverPerInstanceClone`

**New:** `pkg/model/events/clone_for_instance_test.go` (alongside the message one).

| Test | Setup | Assertion |
|---|---|---|
| `TestTimerReceiverPerInstanceClone` | an `IntermediateCatchEvent` with a `TimerEventDefinition`; `Clone()` it twice | the two clones' timer-eDef ids **differ** from each other and from the template (mirrors the message canary) |
| `TestTimerEventDefinitionCloneForInstance` | `CloneForInstance()` on a timer eDef twice | each yields a fresh id; the timer expressions are shared by reference |

#### §4.1.2 Two-instance no-broadcast (engine-level, if tractable)

A `pkg/thresher` or `internal/instance` test: two instances of a process with an
intermediate timer catch, a short timer; assert each instance resumes
**independently** (the timer firing for one does not complete the other).
(Evaluate during implementation; the §4.1.1 distinct-id canary is the primary
proof — distinct ids ⇒ distinct waivers ⇒ no shared fire, by the EventHub keying.)

## §5 Prevention

- **Doc comment** on `TimerEventDefinition.CloneForInstance` names the invariant
  (per-instance registration identity) and the canary test.
- **Forward note (the real prevention):** the per-instance-identity requirement
  applies to **every catchable event definition that gains a waiter builder**.
  Today only timer + message have builders (`CreateWaiter`, §2.2). When a future
  change adds a `CreateWaiter` case for signal / conditional / etc., it MUST also
  add `CloneForInstance` to that eDef type — else it reintroduces this broadcast
  bug. Record this next to the `CreateWaiter` switch as a comment so the
  requirement is visible at the point of change.

## §6 Regressions / side-effects

### §6.1 Timer fire-path
A timer carries no payload and has no `CloneEvent` (it is not a
`flow.EventDefCloner`), so it fires the **registered** (now per-instance) eDef
as-is — which still matches its own waiter. Existing timer-waiter delivery tests
(`eventhub_timer_test.go`) guard that a timer still fires its instance.

### §6.2 What may rely on the old (shared-id) behaviour
`grep` for any code that looks up a timer by the *template* eDef id across
instances → none expected (the EventHub map is per-registration; `WaiterFired`/
`UnregisterEvent` use the live eDef id, which is now per-instance, exactly as for
the message fix). Re-run the audit grep pre-landing.

### §6.3 Rollback path
Single-commit revert (the method + its test); no migration, no data.

## §7 Related

- [SRD-017 v.1](../srd/SRD-017-conversation-token-threading.md) — the message
  precedent (`CloneForInstance`, M3a, §4.3) and the explicit deferral this FIX
  closes. Sideways (FIX → SRD).
- [ADR-006 v.1](../design/ADR-006-events-and-subscriptions.md) — the EventHub
  waiter model (keying by eDef id; the hub owns removal).
- [ADR-009 v.1](../design/ADR-009-per-instance-node-graph.md) — each instance
  owns a private node-graph clone; per-instance eDef identity is the natural
  completion of that principle for catchable definitions.
- **Promote-to-ADR candidate:** if/when several event types become catchable,
  the "every waiter-backed catch definition is per-instance" rule should be
  stated once (an ADR or SAD note) rather than per-type.

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

### §8.1 Stages by commit (branch `fix/timer-catch-broadcast`)

| Stage | Commit | Scope | Tests |
|---|---|---|---|
| doc | `fb683ac` | FIX-004 document | — |
| M1 | `b98c069` | `TimerEventDefinition.CloneForInstance` (timer.go) + `foundation` import; forward-note at the `CreateWaiter` switch (waiters.go); two canary tests (clone_for_instance_test.go) | `TestTimerEventDefinitionCloneForInstance`, `TestTimerReceiverPerInstanceClone` |

Landed exactly as the §3.2 draft: one method, no `Event.clone` / interface /
mock change. `cloneDefsForInstance`'s existing optional-interface check applied
the new method automatically — the receiver canary proves it through the real
clone path.

### §8.2 Verification results

- `make ci` (CI-parity gate): **PASS** — tidy / golangci-lint (incl.
  fieldalignment, misspell) / build / `-race` tests / diff-coverage / govulncheck
  all green. `TimerEventDefinition.CloneForInstance` measured **100%** covered.
- All 9 runnable examples exit 0, including the timer examples `simple-timer`
  and `timer-event` (timers still fire correctly — no fire-path regression,
  confirming §6.1).
- The existing `TestNonMessageDefSharedOnClone` (signal stays shared) still
  passes — only the timer type flipped to per-instance, as intended.

### §8.3 Empirical findings — where reality diverged from the §3 draft

None. The fix landed as designed; the only authoring adjustments were
US-spelling (`analogue`→`analog`) and comment line-wrapping for the project's
80-column / misspell lints — no behavioural delta.

### §8.4 Backlog (out of FIX-004 scope)

- Signal / conditional intermediate catches: when they gain a `CreateWaiter`
  builder, each MUST also implement `CloneForInstance` (see the §5 forward-note),
  or this broadcast class reappears for them.
- The two-instance engine-level no-broadcast assertion (§4.1.2) was covered by
  the distinct-id canary rather than a dedicated thresher test — a future
  integration test could exercise it end-to-end if a broader event-broadcast
  suite is built.

## §9 Open questions

None. Scope is **timer-only** (the sole non-message catchable, waiter-backed
event definition — §2.2): add `TimerEventDefinition.CloneForInstance` (fresh
per-instance id, expressions shared), mirroring the message fix, with a
distinct-id canary test and a forward-note tying future `CreateWaiter` cases to
the per-instance-identity requirement. Signal/conditional and the broad
interface-method approach are explicitly out of scope until those types gain
waiter builders (§3.1 B).
