---
title: Dehydratable waits
description: How a wait node opts into releasing the instance, and who holds it while it sleeps.
---

# Dehydratable waits

Most seams in this section are things you *implement*. This one is mostly
something to **understand** — it explains why an instance sometimes vanishes
from memory while it waits, which element decides that, and what stands in for
the instance while it is gone. You only implement anything here if you write a
custom wait node.

The behavior itself is described from the operator's side in
[Persistence & recovery](../operating/persistence.md#dehydration-a-long-wait-costs-no-goroutines);
this page is the seam view.

## Two halves

Releasing an instance's goroutines needs two independent things to be true, and
they are deliberately separate:

| | Question | Who answers |
|---|---|---|
| **Eligibility** | *May* this wait release the instance? | the **element**, via `renv.Dehydratable` |
| **Holdability** | *Can* anything wake it afterwards? | the **engine**, via `exec.WaitHolders` |

An instance releases only when **every** live track is parked on a wait that is
both. A wait that declares itself dehydratable but has no holder stays resident
and logs — never a lost trigger.

## `renv.Dehydratable` — the element's opt-in

```go
type Dehydratable interface {
    // Dehydratable reports whether this wait node releases the instance's
    // goroutines when parked here.
    Dehydratable(ctx context.Context, re RuntimeEnvironment) bool
}
```

An optional capability, in the same family as `DeadlineHinter` and the other
wait-node capabilities: a node that does not implement it is simply not
dehydratable. That default — **stay resident** — is what makes the whole feature
safe to roll out element by element.

It is a method rather than a flag because the answer can depend on the element's
own evaluated state. The engine's own implementations:

| Element | Answers | Why |
|---|---|---|
| Intermediate catch (timer) | `true` | the engine holds the absolute deadline |
| Intermediate catch (message / signal) | `true` | a receive is a pure wait — arbitrarily long, externally driven |
| Intermediate catch (**conditional**) | `false` | its trigger is the instance's *own* data commits; releasing the instance would destroy the thing that fires it |
| User Task | `true` | the task lives in the distributor's inbox, independent of residency |
| Event-Based Gateway | `true` | the wait node is the *gateway*, not its arms — but see below |
| Service Task (`WithWorker`) | `false` | a job in flight is active work, not a passive wait |

Two subtleties worth knowing if you write your own:

- **The Event-Based Gateway answers for the whole race.** It reports `true`
  unconditionally and ignores its arms' own policies, because eligibility is a
  property of the *wait node* and the gate is the wait node. Whether it actually
  releases is then decided by holdability — every arm must be holdable, so one
  conditional arm keeps the gate resident.
- **Saying `true` is not a promise it will release.** A timer catch reports
  `true`, but a *short* one still stays resident: the deadline threshold is
  applied at arm time, where the value is known. Eligibility is permission, not
  instruction.

## `exec.WaitHolders` — the engine's side

```go
type WaitHolders interface {
    HoldTimer(instanceID, trackID string, eDef flow.EventDefinition,
        deadline time.Time, cycles int) error
    HoldSubscription(instanceID, trackID string, eDef flow.EventDefinition,
        convKeys []string) error
    HoldTask(instanceID, trackID, taskID string) error
    ReleaseWaits(instanceID, trackID string)
}
```

**Implemented by the engine, consumed by the instance loop** — you do not
implement this. It is documented because it explains the guarantee: at arm time
a dehydratable wait hands its trigger source to the engine, which becomes the
permanent subscriber. That is why a trigger never arrives at an instance that
has gone: the *engine* was subscribed all along, and it rebuilds the instance
from its checkpoint before delivering.

A held wait is a released instance's only way back, so the engine surrenders a
hold only once a wake has actually succeeded. A wake that fails keeps its hold
and retries after `WithWakeRetryBackoff`, so the instance recovers by itself
once the cause clears.

`ReleaseWaits` withdraws *every* hold a track owns at once — which is also how an
Event-Based Gateway's losing arms are released when one wins.

## Writing a dehydratable wait node

If you add a custom wait node, implement `Dehydratable` only when a trigger
source **outside the instance** can wake it, and only for a kind the engine can
hold. In practice that means a timer, a message/signal subscription, or a human
task; anything driven by the instance's own state (a conditional) or by work in
flight (a worker job) must stay resident. Returning `true` without a holder does
not lose triggers — the engine's holdability check keeps the instance resident —
but it does mean the instance never releases, so prefer to be explicit.

## See also

- Operator view: [Persistence & recovery](../operating/persistence.md)
- [Custom repository](repository.md) — the checkpoint port dehydration wakes from
- [Custom clock](clock.md) — the engine timer service reads deadlines through it
- Design: [ADR-007 — In-memory long waits](../../design/ADR-007-in-memory-long-waits.md)
