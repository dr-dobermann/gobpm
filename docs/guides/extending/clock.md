---
title: Custom clock
description: Control time (for tests or virtual time).
---

# Custom clock

Every timer in a process — a timer start event, a timer boundary event, an
intermediate timer catch — asks the engine "what time is it?" and "wake me after
this duration". gobpm routes both questions through a single `Clock` interface
instead of calling `time.Now`/`time.After` directly. Swap the clock when you
need to control time: fire a two-hour timer in a millisecond of test wall-clock,
run a deterministic virtual clock, or slave the engine to a simulation's time.

This page shows the seam interface, how to install your own clock, the built-in
controllable clock to reach for first, and how the engine uses it.

## The seam interface

A clock answers two questions — the current time, and a channel that fires after
a duration:

```go
type Clock interface {
    // Now returns the current time.
    Now() time.Time
    // After returns a channel that delivers one value after d elapses.
    After(d time.Duration) <-chan time.Time
}
```

That's the whole contract. `Now` is the engine's source of wall-clock time;
`After(d)` is how a timer waiter schedules a wake-up. Under the production clock,
`Now` is `time.Now()` and `After(d)` is `time.After(d)`, so timer behavior is
ordinary wall-clock behavior. Replace the clock and every timer in every instance
reads time from your implementation instead.

> The `Clock` is engine-wide, set once at construction. It governs BPMN timer
> semantics — see [Timer events](../events/timer.md). It does **not** re-time
> your own `Operation` code (that still sees real `time.Now`); it drives the
> engine's timer scheduling.

## Installing your clock

Pass it to the engine with the `thresher.WithClock` option:

```go
func WithClock(ck clock.Clock) Option
```

| Aspect | Behavior |
|---|---|
| Scope | engine-wide — every instance and every timer this engine runs reads this clock. |
| Nil guard | a `nil` clock is rejected: `WithClock: a nil Clock isn't allowed` (the default stays in place). |
| Default | the system wall clock (`syscl`) when you don't pass `WithClock`. |
| Timing | set at engine construction; there is no per-instance override. |

## The built-in controllable clock

Before writing your own, reach for `clocktest` — the shipped manually controlled
`Clock`, built exactly for time-dependent tests. Its `Now` is settable and its
`After` channels fire when you advance the clock past their deadline:

```go
func New(now time.Time) *Clock       // a clock fixed at now
func (c *Clock) Now() time.Time      // the current (manually set) time
func (c *Clock) After(d time.Duration) <-chan time.Time
func (c *Clock) Advance(d time.Duration)  // move forward by d, fire due timers
func (c *Clock) Set(t time.Time)          // jump to t (forward only), fire due timers
```

Drive an engine's timers by hand — a timer scheduled for two hours out fires the
moment you `Advance` past it, with no real waiting:

```go
ck := clocktest.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

engine, _ := thresher.New("engine", thresher.WithClock(ck))
// … register a process with a 2h timer, start an instance …

ck.Advance(2 * time.Hour) // the timer's After channel fires now
```

`Advance` and `Set` are **forward-only** — a non-positive `Advance` or an earlier
`Set` is ignored, keeping the fake clock monotonic the way the timer waiters
assume. A non-positive `After(d)` fires immediately.

## A minimal implementation

If `clocktest` doesn't fit — say you want time slaved to a simulation counter —
the whole surface is two methods. A virtual clock that only ever moves when you
tell it to, firing pending timers on each tick:

```go
type simClock struct {
    mu     sync.Mutex
    now    time.Time
    timers []struct {
        at time.Time
        ch chan time.Time
    }
}

func (s *simClock) Now() time.Time {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.now
}

func (s *simClock) After(d time.Duration) <-chan time.Time {
    s.mu.Lock()
    defer s.mu.Unlock()
    ch := make(chan time.Time, 1)
    s.timers = append(s.timers, struct {
        at time.Time
        ch chan time.Time
    }{s.now.Add(d), ch})
    return ch
}

// Tick advances the clock and fires any timer now due.
func (s *simClock) Tick(d time.Duration) { /* move now, send on due channels */ }
```

Two rules your implementation must honor, both visible in `clocktest`:

- **`After` must not block.** Buffer the channel (capacity 1) so firing a timer
  never stalls; the timer waiter drains it when it wakes.
- **Stay monotonic.** The timer waiters assume time never runs backward — never
  move `Now` earlier, or a scheduled `After` may never fire.

The engine calls `Now`/`After` from concurrent timer goroutines, so guard shared
state (the `sync.Mutex` above) yourself — the interface makes no locking
promises.

## Reference implementations

Two ship in-tree, both good models:

| Package | Role |
|---|---|
| `pkg/clock/syscl` | the production default — `Now`→`time.Now()`, `After`→`time.After(d)`, stateless. `syscl.New()` returns it. |
| `pkg/clock/clocktest` | the manually controlled test fake — settable `Now`, `Advance`/`Set` to fire timers. Use it in tests before rolling your own. |

`syscl` shows the trivial production shape (no state, no locking — it delegates to
the stdlib); `clocktest` shows the controllable shape (buffered channels, a mutex,
forward-only monotonic time).

## How the engine uses it

The clock is wired once and read everywhere a timer runs:

1. You pass `thresher.WithClock(yourClock)` at engine construction (or take the
   `syscl` default).
2. When an instance reaches a timer element, its timer waiter reads the deadline
   via `Clock().Now()` and schedules the wake-up via `Clock().After(d)`.
3. When that channel delivers, the waiter fires the timer — moving the token past
   a timer start/boundary/intermediate event.

So a single injected clock deterministically controls every timer the engine
schedules — which is what makes timer-driven processes testable without real
waiting, and drivable from an external time source.

## See also

- Timer events: [Timer events](../events/timer.md) — the BPMN elements this clock drives.
- The engine: [The engine (Thresher)](../concepts/engine.md) — where `WithClock` is set.
- Design: [ADR-002 — Extension architecture](../../design/ADR-002-extension-architecture.md) (§4.2, the Clock extension).
- Full API: `go doc github.com/dr-dobermann/gobpm/pkg/clock`
