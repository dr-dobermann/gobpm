# dehydration

Demonstrates **instance dehydration and wake-on-trigger** (ADR-007 /
SRD-071): an instance whose every live track sits on a long wait releases
**all** of its goroutines — the loop included — and leaves. Its checkpoint is
what wakes it.

```
start → park on a long wait → checkpoint → release every goroutine
                                              ⋮  (nothing is running)
        trigger arrives → rebuild from the checkpoint → continue the flow
```

Six cases, one per holder kind, plus both sides of the timer threshold:

| # | Wait | Releases? | Woken by |
|---|---|---|---|
| 1 | Timer, 48h out | **yes** | its deadline |
| 2 | Timer, 5m out | **no** — stays resident | its deadline, from memory |
| 3 | Message catch | **yes** | a correlated message |
| 4 | Signal catch | **yes** | a broadcast |
| 5 | User task | **yes** | `Complete` on the task |
| 6 | Event-based gateway (timer + message) | **yes** | the winning arm |

What the trace shows:

- **A release needs two independent yeses.** The element must be
  *dehydratable* (a long wait — parking is the point) **and** the engine must
  hold a way to wake it (a timer deadline, a broker subscription, a task in
  the distributor's inbox). Either missing and the instance stays resident;
  case 2 is the deliberate no.
- **The timer threshold.** A one-shot timer further out than an hour is worth
  a checkpoint-and-rebuild round trip; a nearer one is not, so it simply waits
  in memory. Both timers fire correctly — the difference is only in what it
  cost to wait.
- **A gateway holds a SET.** The event-based gateway releases only because
  **both** arms are holdable. When the message arm wins, the whole set is
  withdrawn together — the example proves the losing timer arm is gone by
  pushing the clock far past its deadline and observing nothing.
- **A held task keeps its identity.** The user task is announced, the instance
  releases, and completing the task by the id the caller already held brings
  the instance back. Nothing about the caller's side changes.
- **Two facts bracket every cycle.** `Dehydrated` reports what the instance is
  waiting on and that it now holds zero goroutines; `Hydrated` reports what
  woke it and whether the flow continued. They are the operator's whole view
  of the feature — this example prints them as they arrive.

## Running it

```bash
cd examples/dehydration
go run .
```

The example drives a **controlled clock** (`pkg/clock/clocktest`) so a 48-hour
wait demonstrates in milliseconds — advancing time is what fires the timers.
In production you leave the default wall clock in place and change nothing
else about the model or the engine wiring.

Dehydration rides on the same switch as the rest of the persistence slice: an
engine with an explicitly configured repository (`thresher.WithRepository`)
checkpoints, recovers on restart, **and** dehydrates. Without one, nothing
releases. See [`../restart-recovery/`](../restart-recovery/) for the
checkpoint and recovery half.
