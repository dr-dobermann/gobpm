# SRD-077 — ISO 8601 timer authoring, and the duration-only gap

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-03 |
| Owner | Ruslan Gabitov |
| Implements | [SAD-001](../design/SAD-001-vision-and-architecture.md) §14 (the library's §2.3.1 half) and [conformance.md](../bpmn-spec/conformance.md) (Timer in scope) |
| Upstream | [ADR-006 v.4](../design/ADR-006-events-and-subscriptions.md) §2.3 (wait nodes & subscription lifecycle); [ADR-007 v.2.1](../design/ADR-007-in-memory-long-waits.md) §2.4 (which waits release, and who holds them); [ADR-033 v.2](../design/ADR-033-persistence-and-state.md) §2.1 (the checkpoint's serializable wait descriptors — a timer's absolute deadline) |
| Closes | [#275](https://github.com/dr-dobermann/gobpm/issues/275) |

## §1 Background

BPMN gives `TimerEventDefinition` three mutually exclusive attributes
(**§10.5.5, Table 10.101**, p. 273), each carrying an ISO 8601 value:

> Timer attributes are **mutually exclusive** and if any of the other Timer
> attributes is set, `timeDate` **MUST NOT** be set (if the `isExecutable`
> attribute of the Process is set to true).

| Attribute | Return type MUST conform to |
|---|---|
| `timeDate` | ISO 8601 date and time |
| `timeCycle` | ISO 8601 **recurring** time interval |
| `timeDuration` | ISO 8601 time interval |

**gobpm decomposes `timeCycle`.** Instead of one `R3/PT10H` string it carries
the repetition count in `timeCycle` (an `int`) and the interval in
`timeDuration`. The two fields are jointly the recurrence — semantically
equivalent to the standard's single string, and type-safe rather than parsed.
`internal/eventproc/eventhub/waiters/timer.go` implements exactly that: `Cycle`
fills `cyclesLeft`, `Duration` fills `duration`, and `processTimerEvent`
decrements-then-tests so a cycle of N delivers exactly N events (FIX-012).

Two consequences follow, one of them a defect.

**The defect.** Because `timeDuration`'s meaning is taken by the recurrence, the
constructor guard requires it to appear *with* a cycle
(`pkg/model/events/timer.go:38-39`), so a standalone `timeDuration` — BPMN's
one-shot relative timer, *"wait five minutes, then fire"* — cannot be
constructed at all. Measured against the constructor before this SRD:

| Call | BPMN Table 10.101 | gobpm |
|---|---|---|
| `timeDate` only | legal | accepted |
| **`timeDuration` only** | **legal** | **rejected** |
| `timeCycle` only | legal | rejected |
| `timeCycle` + `timeDuration` | illegal | accepted |
| `timeDate` + `timeDuration` | illegal | rejected |
| all three / none | illegal | rejected |

Rows 3 and 4 are the decomposition and are internally coherent — a repetition
count with no interval has nothing to schedule, and the pair *is* the
recurrence. **Row 2 is the defect**, and it is purely a model-layer one: the
runtime already handles a duration-only timer end to end. Verified against
`waiters.TimerPlan` with a duration-only definition constructed past the guard:

```
deadline = now + 30m0s   cyclesLeft = 0     (one-shot, checkpoint-ready)
```

`Service` skips its `!tw.next.IsZero()` branch, arms on `duration`, and
`processTimerEvent` decrements `0 → -1` and terminates after one delivery.

**The second consequence** is that a modeller cannot write the standard's own
notation. Every real BPMN source — XML, a modeller, Camunda documentation —
expresses timers as ISO 8601 strings, and gobpm accepts only evaluated Go
values. The converter ([#284](https://github.com/dr-dobermann/gobpm/issues/284))
will need that translation regardless; doing it now costs one parser and makes
that work pure plumbing.

Camunda 7, the project's alignment target, states the same rule the standard
does — *"A timer definition must have **exactly one** of the following
elements"* — and documents `timeDuration` as *"how long the timer should run
before it is fired"* and `timeCycle` as `R3/PT10H` or a cron expression
(`docs/docs.camunda.org/manual/7.22/reference/bpmn20/events/timer-events`).

## §2 Requirements

### Functional

| # | Requirement |
|---|---|
| **FR-1** | A `TimerEventDefinition` with **only** `timeDuration` is constructible, and fires **once** after that interval. |
| **FR-2** | Every other attribute combination keeps its current accept/reject verdict. The recurrence stays `timeCycle` + `timeDuration`. |
| **FR-3** | The rejection error names which rule was broken — `timeDate` combined with another attribute, or a cycle without its interval — instead of the current single message. |
| **FR-4** | A new internal package parses ISO 8601 **date-time** (`2011-03-11T12:13:14Z`), **duration** (`P10D`, `PT10H`, `P2W`, `P1DT2H3M4S`), and **repeating interval** (`R3/PT10H`, `R/PT10H`). |
| **FR-5** | Calendar-relative designators `Y` and `M` in a duration are **rejected** with an error naming them and stating why (see §4.2). |
| **FR-6** | `NewISO8601Timer(expr, opts...)` / `MustISO8601Timer(expr, opts...)` build a `TimerEventDefinition` by **disassembling** one ISO 8601 string into the existing `timeDate` / `timeCycle` / `timeDuration` triple. |
| **FR-7** | An **unbounded** recurrence (`R/PT10H`) is representable and fires indefinitely until the waiter is stopped. |
| **FR-8** | The disassembly is recorded as an engine choice in SAD-001 §14.2 — `timeCycle` carried as (count, interval) rather than one string. |

### Non-functional

| # | Requirement |
|---|---|
| **NFR-1** | **No new dependency.** The parser is hand-written over stdlib; core stays on stdlib + `uuid` (SAD-001 G2). |
| **NFR-2** | **No runtime change beyond FR-7.** The waiter, `TimerPlan`, the checkpoint and the restore hint are untouched. |
| **NFR-3** | Every touched function finishes at ≥95% line coverage (the `COVER_MIN` gate). |
| **NFR-4** | `make ci` green, including `-race`. |

## §3 Models

### §3.1 `internal/iso8601` (new)

```go
// Duration is a parsed ISO 8601 duration. Weeks, days and time components
// only — Y and M are rejected at parse (§4.2).
func ParseDuration(s string) (time.Duration, error)

// ParseDateTime accepts the RFC 3339 profile of ISO 8601.
func ParseDateTime(s string) (time.Time, error)

// Repeat is a parsed ISO 8601 repeating interval: R[n]/<duration>.
// Count is Unbounded when the designator carries no number.
type Repeat struct {
    Count    int           // repetitions, or Unbounded
    Interval time.Duration // the recurrence interval
}

// Unbounded marks a recurrence with no repetition limit (`R/PT10H`).
const Unbounded = -1

func ParseRepeat(s string) (Repeat, error)
```

`time.ParseDuration` is **not** usable — it parses `10h`, not `P10D`
(verified: every ISO form returns a parse error). `time.RFC3339` **is**
usable for the date-time form.

### §3.2 `pkg/model/events` — the guard

```go
if (tDate != nil && (tCycle != nil || tDuration != nil)) ||
	(tDate == nil && tDuration == nil) {
```

One clause changes: `(tCycle == nil || tDuration == nil)` → `tDuration == nil`.
A cycle still requires its interval; a duration no longer requires a cycle.

### §3.3 `pkg/model/events` — the ISO 8601 constructor

```go
func NewISO8601Timer(expr string, baseOpts ...options.Option) (*TimerEventDefinition, error)
func MustISO8601Timer(expr string, baseOpts ...options.Option) *TimerEventDefinition
```

Disassembly, by what the string parses as:

| Input | Fields set |
|---|---|
| `2011-03-11T12:13:14Z` | `timeDate` |
| `P10D` | `timeDuration` |
| `R3/PT10H` | `timeCycle` = 3, `timeDuration` = 10h |
| `R/PT10H` | `timeCycle` = `Unbounded`, `timeDuration` = 10h |

Each field is a constant `goexpr` returning the parsed Go value, so the result
is an ordinary `TimerEventDefinition` indistinguishable from a hand-built one.

### §3.4 `internal/eventproc/eventhub/waiters` — unbounded

`processTimerEvent` currently decrements unconditionally and terminates at
`<= 0`, so no value means "forever". Guard the decrement:

```go
if tw.cyclesLeft != iso8601.Unbounded {
    tw.cyclesLeft--
}
```

This is the **only** runtime change in this SRD.

## §4 Analysis

### §4.1 Why the model is not changed to a single `timeCycle` string

The alternative was to make `timeCycle` carry `R3/PT10H` itself and drop the
decomposition, giving literal attribute parity with Table 10.101. Rejected:
it changes the public constructor, the waiter's parse, `TimerPlan` and the
checkpoint's recorded plan — for no behavioural gain, since (count, interval)
and `R3/PT10H` denote the same schedule. Disassembling at **authoring** time
keeps one internal model with two authoring paths, and leaves the runtime,
its race-tested lifecycle and its restore semantics untouched (NFR-2).

### §4.2 Why `Y` and `M` are rejected

They are not fixed-length, so they cannot become a `time.Duration`. Go's
calendar arithmetic also normalizes overflow rather than clamping —
`2026-01-31` plus one month via `AddDate` yields **2026-03-03**, not
February 28 — so even a calendar-aware representation must decide a
truncation policy the standard does not specify. `PnW`, `PnD` and the time
components cover the timers BPMN processes actually use. Rejecting loudly is
honest; approximating `Y = 365d` is not. A calendar-aware duration is a
follow-up if a real model needs one.

### §4.3 Why cron is out of scope

Cron does not disassemble into (date | count + interval). `0 0/5 * * * ?`
does — interval 5m, unbounded — but `0 0 9 * * MON` needs an anchor *and* an
interval *and* unboundedness (three fields the guard forbids), and
`0 0 9 1 * ?` has no fixed interval at all, since month lengths vary. A cron
schedule is "recompute the next fire time", whereas the waiter re-arms at a
fixed `tw.duration`. Supporting it properly is a scheduler change, not a
parsing one, and belongs with the converter work
([#284](https://github.com/dr-dobermann/gobpm/issues/284)) or its own doc.

### §4.4 Why a sibling constructor rather than an option

`NewTimerEventDefinition` is positional, so an option form reads
`NewTimerEventDefinition(nil, nil, nil, WithISO8601("P10D"))` — three nils to
say "none of these", and a guard that must run after option application. A
sibling constructor states its input in its name, keeps the `Must*` twin
convention, and leaves the existing constructor untouched.

### §4.5 Why `timeCycle` alone stays rejected

Under the decomposition a repetition count carries no interval, so there is
nothing to schedule. `NewISO8601Timer("R3/PT10H")` sets **both** fields, so the
standard's notation is expressible; only the hand-built half-recurrence is
refused, and FR-3 makes the error say so.

## §5 API

Added: `events.NewISO8601Timer`, `events.MustISO8601Timer`,
`internal/iso8601.{ParseDuration,ParseDateTime,ParseRepeat,Repeat,Unbounded}`.

Changed: none — `NewTimerEventDefinition` keeps its signature and accepts a
strictly larger set of inputs. No caller breaks.

## §6 Tests

| # | Test | Asserts |
|---|---|---|
| T-1 | `TestTimerAttributeCombinations` | all seven combinations, verdict per §1's table (FR-1, FR-2) |
| T-2 | `TestTimerGuardErrorNamesTheRule` | the two rejection classes carry distinct messages (FR-3) |
| T-3 | `TestParseDuration` | `P10D`, `PT10H`, `P2W`, `P1DT2H3M4S`; malformed input; `P1Y`/`P1M` rejected by name (FR-4, FR-5) |
| T-4 | `TestParseDateTime` | RFC 3339 accepted, garbage rejected (FR-4) |
| T-5 | `TestParseRepeat` | `R3/PT10H` → (3, 10h); `R/PT10H` → (Unbounded, 10h); malformed rejected (FR-4, FR-7) |
| T-6 | `TestISO8601TimerDisassembly` | each §3.3 row sets exactly the expected fields (FR-6) |
| T-7 | `TestDurationOnlyFires` | a duration-only waiter fires **once**, on the injected clock (FR-1) |
| T-8 | `TestDurationOnlyTimerPlan` | `TimerPlan` returns `now+d`, `cyclesLeft == 0` (FR-1) |
| T-9 | `TestUnboundedCycleKeepsFiring` | an unbounded waiter delivers > N times and stops only on `Stop()` (FR-7) |

## §7 Milestones

| # | Milestone | Contents |
|---|---|---|
| **M1** | The duration-only gap | §3.2 guard, FR-3 error, doc comment; T-1, T-2, T-7, T-8 |
| **M2** | The ISO 8601 parser | `internal/iso8601`; T-3, T-4, T-5 |
| **M3** | The authoring path | §3.3 constructors, §3.4 unbounded, SAD-001 §14.2 entry; T-6, T-9 |

## §8 Cross-doc references

| Doc | Version | Why |
|---|---|---|
| [SAD-001](../design/SAD-001-vision-and-architecture.md) | Draft v.1.1 | §14 registered deviations — FR-8 adds the disassembly entry; G2 the dependency rule (NFR-1) |
| [ADR-006](../design/ADR-006-events-and-subscriptions.md) | v.4 | §2.3 wait nodes & subscription lifecycle — the lifecycle this rides unchanged |
| [ADR-007](../design/ADR-007-in-memory-long-waits.md) | v.2.1 | §2.4 which waits release — a duration timer is one of them |
| [ADR-033](../design/ADR-033-persistence-and-state.md) | v.2 | §2.1 the checkpoint's wait descriptors carry a timer's absolute deadline — `TimerPlan` already derives one for duration-only |

## §9 Definition of Done

- [ ] FR-1…FR-8 implemented and demonstrated by §6
- [ ] NFR-1: `go.mod` unchanged
- [ ] NFR-2: the only runtime diff is §3.4
- [ ] NFR-3: touched functions ≥95%, measured on the diff not the aggregate
- [ ] NFR-4: `make ci` green
- [ ] SAD-001 §14.2 carries the disassembly entry
- [ ] An example demonstrating a duration-only timer, asserting its own outcome
- [ ] `/check-srd` PASS

## §10 Implementation summary

_To be filled after the milestones land._

## Open questions

_None._
