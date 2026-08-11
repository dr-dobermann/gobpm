# FIX-040 — formatting a live engine object reflects across it, and the race detector blames the engine

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted.
**Date:** 2026-08-10.
**Author:** Руслан Габитов.
**Branch:** `feat/loop-mi-decorator`.
**Tracking:** #314.
**Upstream:** [ADR-006 v.5](../design/ADR-006-events-and-subscriptions.md) §2.9
(the in-instance delivery contract — the registration call whose argument gets
formatted). No ADR governs how an engine object renders under `%v`; §3.2
alternative 2 records the one contract question this fix deliberately leaves
to #313.

**Grounded in (verified at `10467bb`):**
- `internal/instance/track.go:658-660` — the processor handed to
  `RegisterEvent` is `eventproc.EventProcessor(t)`, except for a message
  trigger where it is `proc = t.instance`: the whole live `*Instance`.
- `internal/instance/eventproducer.go:57-58` — `Instance.RegisterEvent`
  forwards that processor unchanged to `inst.parentEventProducer`.
- `internal/instance/composite_restore_test.go:45-57` — `laxEP` builds a
  `MockEventProducer` whose expectations are `mock.Anything`.
- `testify@v1.11.1/mock/mock.go:975` — inside `Arguments.Diff`:
  `actualFmt = fmt.Sprintf("(%[1]T=%[1]v)", actual)`, run for each actual
  argument; `mock.go:393` — `findExpectedCall` calls `Diff` on every
  invocation, matching or not.
- Neither `*Instance` nor `*track` implemented `fmt.Stringer` at that commit
  (`grep -rn "func (inst \*Instance) String" internal/instance/` → empty).
- `internal/instance/instance.go:53` — `corr correlator`, and
  `correlation.go:29,34,36` — its `iterKeys` / `iterKeyNames` maps and the
  `m sync.Mutex` guarding them, all reachable by reflection from the struct
  root.

---

## 1 Symptoms

### 1.1 A data race is reported with innocent engine code on one side

Under `-race`, a test that restores a message-routing instance reported up to
14 data races per run. Every stack named engine internals:

```
Write at 0x00c0002fe410 by goroutine 15:
  sync.(*Mutex).Lock()
  …instance.(*correlator).markIterationKeyName()   correlation.go:62
  …instance.(*track).checkNodeType()               track.go:544
  …instance.(*track).run()                         track.go:1212
```

The engine's side is correct: `markIterationKeyName` takes `c.m` before
touching the map, exactly as its three siblings do. Nothing in the engine
reads that memory without the lock.

### 1.2 The other side is a test double formatting its arguments

The reported *other* side is not engine code at all:

```
Previous read at 0x00c0002fe410 by goroutine 16:
  reflect.typedmemmove() / reflect.packEface() / reflect.Value.Interface()
  fmt.(*pp).printValue()                           print.go:769
  fmt.Sprintf()                                    print.go:239
  testify/mock.Arguments.Diff()                    mock.go:975
  testify/mock.(*Mock).findExpectedCall()          mock.go:393
```

`RegisterEvent(proc, eDef)` hands the mock a `proc` that, for a message
trigger, IS the live `*Instance` (`track.go:659`). testify formats every
argument with `%v` to build its diff string — on the calling goroutine,
through reflection, field by field, including the correlator's maps and the
mutex that guards them. `fmt` cannot take that lock: it reaches the fields
behind the type's back.

So the read is unsynchronized and the detector is right to report it. What is
wrong is *who* it appears to accuse.

### 1.3 The cost: a real coverage gap was closed by deleting the test

SRD-085 landed iteration-correlated message routing. Its restore coverage is
T-7, `TestIterationRoutingKillAndResume` (`pkg/thresher`), which publishes
exactly ONE envelope after the restore
(`delivery_payload_test.go`, the single `broker.Publish` following the
recovery poll) and asserts that it reaches the matching iteration. Routing
each of TWO deliveries to its own restored subscription — the case where a
rebuilt index can be wrong rather than merely present — had no test at any
level.

The test written to close it tripped §1.1 and was **withdrawn rather than
committed green-without-race** (`feat/composite-followups`, commit `27e111b`:
"TestIterationRoutingSurvivesRestore is deliberately absent"). The gap
therefore outlived the work that created it, and the issue filed for the race
(#314) also carried the gap as a blocked deliverable.

### 1.4 Two wrong diagnoses were published before this one

Recorded because they are the reason this doc insists on measurement:

- **"`captureAt` cancels without waiting, so a teardown races a restore."**
  False: `captureAt` (`composite_restore_test.go:168-169`) registers its
  `cancel` as `t.Cleanup` and never calls it during the test. Nothing was
  tearing down.
- **"It is narrow — the ad-hoc restore family does the same thing and is
  clean over 15 `-race` runs."** True as a measurement, worthless as an
  inference: those fixtures never reach `RegisterEvent` with a mock while
  another goroutine writes, so their cleanliness says nothing about the
  trigger.

Both survived because the race report was read by grepping for engine frames,
which is precisely the filter that hides a formatter on the other side.

## 2 Root cause

**An engine object with no `String()` method is a reflection target.** `%v`
over a struct pointer walks every field transitively. `*Instance` reaches the
correlator (maps + mutex), the track table, the incident map and the atomics;
`*track` reaches its instance. Any collaborator that formats one of them —
a test double's matcher, a `%v` in a log line, an error built from an
interface value — becomes an unsynchronized reader of live engine state.

The engine cannot defend against this with locks, because the reader never
calls a method. The only defence is to stop the walk at the type boundary.

Three properties make the event path the place it surfaces:

1. `RegisterEvent` is on the hot path of every catching node, so it runs often.
2. Its processor argument is, for message triggers, the entire `*Instance`
   rather than a small track (`track.go:658-660`).
3. Registration happens on **track** goroutines while the loop and other
   tracks mutate the same instance — so a concurrent writer is always present.

## 3 Solution

### 3.1 Decision

Implement `fmt.Stringer` on `*Instance` and `*track`, rendering each as its
immutable element id. `fmt` then calls `String()` and never reflects, so no
formatter anywhere — present or future, in tests or in production logging —
can read engine internals without synchronization.

The id comes from the embedded `foundation.BaseElement` and is fixed at
construction, so `String()` needs no lock and cannot itself race.

### 3.2 Alternatives considered

| # | Option | Verdict |
|---|---|---|
| 1 | Replace `laxEP`'s testify mock with a hand-written stub | Rejected as the primary fix. It repairs `laxEP`'s 18 call sites and leaves the other four `EXPECT().RegisterEvent(mock.Anything, …)` sites plus every future one exposed. It also treats the symptom's venue (one helper) rather than its cause (the type is reflectable). Measured: with a stub, the same scenario is clean — which is what proves the diagnosis, not what fixes it. |
| 2 | Pass a narrow processor handle instead of the whole `*Instance` | Rejected here, though it is the better long-term shape. Changing what `track.go:659` registers is an ADR-006 v.5 §2.9 contract change — who the message processor IS decides who receives the delivery — and #313's decorator work is about to redefine exactly that. Doing it now would be a second, conflicting answer to a question that work must settle. |
| 3 | Mark the fields with `//nolint` / suppress the detector | Rejected. The report is accurate; suppressing it discards a real signal, and the next genuine race in the same struct would be indistinguishable from the noise. |
| 4 | Keep the test out of the suite and document the limitation | Rejected — this is what `feat/composite-followups` did, and it left a real coverage gap open for the sake of a defect that turned out not to be in the engine at all. |
| 5 | `String()` on `*Instance` and `*track` | **Chosen.** Nine lines, fixes every formatter at once, and yields better diagnostics: `instance <id>` rather than a page of internals. |

### 3.3 `internal/instance/stringer.go` (new)

```go
// String renders the instance as its id — immutable, so it is safe to
// call from any goroutine.
func (inst *Instance) String() string {
	return fmt.Sprintf("instance %s", inst.ID())
}

// String renders the track as its id, on the same terms as Instance's.
func (t *track) String() string {
	return fmt.Sprintf("track %s", t.ID())
}
```

The file's header comment states the hazard in full, so the next reader of a
race report naming `correlator` does not repeat §1.4.

### 3.4 `internal/instance/msg_routing_test.go`

`TestIterationRoutingSurvivesRestore` — the test §1.3 says was withdrawn —
lands, using the standard `laxEP` deliberately: the fix must hold for the
fixture that exposed the problem, not only for a stub written to avoid it.

## 4 Verification

### 4.1 Tests

| # | Test | Asserts |
|---|---|---|
| T-1 | `TestIterationRoutingSurvivesRestore` (`internal/instance`) | two iterations parked at capture both re-arm after restore, and **both** deliveries — out of order, `b` then `a` — each reach their own iteration; the gap §1.3 names |
| T-2 | T-1 run with `stringer.go` removed | **5 data races**; with it restored, 0. The pin fails on the pre-fix code |
| T-3 | `go test -race ./internal/instance/... ./pkg/thresher/` ×2 | no race, no failure anywhere in the suites the change touches |

### 4.2 Diagnosis experiments (recorded, not committed)

The scratch tests that produced the diagnosis were deleted; their results are
the evidence for §2 and are reproducible from this table.

| Scenario | testify mock | plain stub |
|---|---|---|
| Two live instances from ONE snapshot — no capture, no restore | **6 races** | 0 (×3 runs) |
| #314's own scenario (capture → restore) | **5 races** | 0 (×5 runs) |

The first row is the important one: it removes restore from the picture
entirely and still reproduces, which is what disproves §1.4's first diagnosis
and shows the trigger is "a mock formats a live instance while tracks run",
not anything about checkpointing.

## 5 Prevention

- The hazard is now impossible to hit through `%v` on these two types, which
  is how every reported instance of it arrived.
- `stringer.go`'s header comment names the failure mode, so a future reader of
  a race report that accuses `correlator` has the explanation in the package
  rather than in a closed issue.
- The general rule this instance teaches — **a type that is handed out as an
  interface value and holds mutable state should implement `Stringer`** —
  applies to any future engine object placed on a collaborator boundary.

## 6 Regressions considered

- **Log output changes** where a `*Instance` or `*track` was formatted with
  `%v`. That is the intended improvement; no test asserted on the reflected
  form (verified: no `&{` match against an instance in the suite).
- **`String()` on a partially-built instance.** It reads only the embedded
  `BaseElement`, which `newInstanceIdentity` sets before any goroutine starts,
  so there is no window where it returns garbage or panics.
- **Recursion.** Neither `String()` formats the receiver, so `fmt` cannot
  re-enter.
- **`golangci-lint` incl. tests**: 0 issues.

## 7 Related

- **#314** — this closes the **race half** and its blocked deliverable (the
  T-1 test), not the issue. While this branch was in flight, measurements on
  `feat/adr-003-layout-close` attached a second symptom to the same issue:
  `TestIterationCorrelatedRouting` and `TestIterationRoutingKillAndResume`
  **hang** in a full-package run — no `-race` report, the delivery simply
  never arrives — with the first failing at any deadline, which is the
  signature of a lost delivery rather than a slow one. Nothing in this fix
  addresses that: a formatter reading engine state without a lock and a
  message that never reaches its waiter are different failures. The issue
  stays open on that half, and `docs/backlog.md` carries both halves with
  their measurements. What this doc does supersede is the issue's original
  *mechanism* for the race reports (§1.4).
- **#313** — the loop/MI decorator. Alternative 2 (a narrow processor handle)
  belongs to that work; this fix deliberately does not pre-empt it.
- **SRD-085** — the routing this test finally covers across a restore.
- **FIX-038** — the previous branch's independent review, which is where the
  withdrawn test and the wrong diagnosis were recorded.

## 8 Implementation summary

### 8.1 Stage commits

| Stage | Commit | Scope |
|---|---|---|
| Doc | `4f5ae05` | this document |
| M1 | `adb817c6` | `internal/instance/stringer.go` (new, 2 functions) + `TestIterationRoutingSurvivesRestore` |

### 8.2 Verification (measured)

- **T-2, the honesty check.** With `stringer.go` moved aside, T-1 reports
  **5 data races** and fails; restored, **0** and passes. The pin fails on the
  pre-fix code.
- **T-3.** `go test -race ./internal/instance/... ./pkg/thresher/` — clean
  across two full sweeps.
- **Gate.** `make ci` at `adb817c6`: `verdict: PASS`, 14/14 steps, 125s;
  **diff-coverage 100.0% of 4 changed coverable lines** (min 95); examples
  run end-to-end (step 14/14 `run-examples ok`); govulncheck clean;
  `golangci-lint` incl. tests 0 issues.
- **Touched functions.** Both `String()` implementations at **100.0%**
  (`go tool cover -func`).

The gate was run twice on purpose. The first run, before the commit, reported
`100.0% of 0 changed coverable lines` — covercheck is HEAD-based, so on a
dirty tree it measures nothing and that number means "nothing was measured",
not "everything is covered". The figure above is from the run against the
committed tree.

### 8.3 What this fix deliberately leaves alone

`laxEP` remains a testify mock (§3.2 alternative 1: redundant once nothing
reflects), and `RegisterEvent` still receives the whole `*Instance` for a
message trigger (§3.2 alternative 2: an ADR-006 v.5 §2.9 contract question
belonging to #313). Neither is a deferral of this defect — both are recorded
positions with reasons, and the defect itself is closed.

## 9 Open questions

*None — §3.2 records the resolved design points (why not the helper, why not
the narrow handle now, why not a suppression).*
