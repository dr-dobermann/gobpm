# FIX-035 — a swallowed observer panic and an unenforced attribute vocabulary

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Draft (2026-07-31, branch `fix/observer-panic-and-attr-vocabulary`, not yet implemented).
**Date:** 2026-07-31.
**Author:** Ruslan Gabitov.
**Branch:** `fix/observer-panic-and-attr-vocabulary` — names the two halves; the third (gate docs) rides along as documentation of the same class of defect.
**Upstream:** [ADR-013 v.2](../design/ADR-013-instance-observability.md) §5 (contain observer failures with *drop-with-warning* — the half this FIX completes), [ADR-022 v.1](../design/ADR-022-error-propagation-and-logging-policy.md) §2.4/§2.5/§2.6 (level discipline, the attribute vocabulary, silence-is-opt-out).

**Grounded in (internal artifacts):**
- `pkg/thresher/observer.go:103` — `deliver`, the containment point that drops the recovered value.
- `pkg/thresher/producer.go:120` — the second, previously unrecorded call site.
- `pkg/observability/fact.go:203-289` — the 47-constant `Attr*` vocabulary.
- [FIX-034](FIX-034-gate-blind-spots-and-doc-drift.md) §8.3 — where §1.1 was first recorded, with a premise this FIX disproves.

---

## 1 Symptoms

Three defects, one shape: **the project decided something and built nothing to
hold it.** That is the same shape FIX-034 addressed for the CI gate; these are
the observability-layer instances of it.

### 1.1 Symptom A: a panicking observer leaves no trace

A host observer whose `OnFact` panics is contained — correctly — but the
recovered value is discarded, so a broken observer is indistinguishable from a
working one. The engine runs on, the host's observation silently does nothing,
and nothing anywhere says so.

```go
// pkg/thresher/observer.go:103
func deliver(o Observer, f observability.Fact) {
	defer func() { _ = recover() }() //nolint:errcheck // deliberate containment

	o.OnFact(f)
}
```

This is **half** of what ADR-013 v.2 §5 prescribes:

> **Contain observer failures** (recover/timeout/**drop-with-warning**) so an
> observer can never stall or crash a track.

The recover landed. The drop landed. The warning never did.

It is also the exact class ADR-022 v.1 §2.6 names a defect:

> Accidental silence — a discarded error, a nil logger erasing the default, a
> missing log on a handling boundary — is treated as a defect, and a worse one
> than accidental noise: noise is annoying, silence is undiagnosable.

**Scope is wider than previously recorded.** `deliver` has **two** call sites,
not one: the instance-handle drain (`observer.go:57`) and the engine-scope drain
(`producer.go:120`, `producer.subscribe`). Both are blind. FIX-034 §8.3 and the
backlog entry it spawned described only the first.

### 1.2 Symptom B: the attribute vocabulary drifted from its own rule

ADR-022 v.1 §2.5 closes with a registration rule:

> **New entity keys join by a version bump** of this ADR, not ad hoc.

Nothing enforces it, and it has not held. Measured against
`pkg/observability/fact.go`:

| Direction | Count | Meaning |
|---|---|---|
| `Attr*` constants in code | 47 | the vocabulary as actually used |
| …absent from ADR-022 §2.5 | 28 | landed without the bump the rule requires |
| Keys canonized in §2.5 but **not** in the `Attr*` family | 2 | `event_definition_type` (12 uses), `event_processor_id` (8) — bare string literals |
| Literal occurrences of a key that **has** an `Attr*` constant | 222 across 27 keys | the constant exists and the call site ignores it |

Roughly half of the 28 are the *descriptive/count* kind §2.5 explicitly leaves
free-form (`ordinal`, `row_count`, `loop_counter`, `stop_reason`, `version`, …),
so they need no registration. The remainder are unambiguous **entity
references** that do: `child_instance_id`, `parent_instance_id`,
`call_activity_node_id`, `called_key`, `called_version`, `user_id`,
`from_user_id`, `to_user_id`, `data_store`, `data_path`, `scope_path`,
`decision_ref`, `escalation`.

So the vocabulary is unenforced in **both** directions — keys that never reached
the doc, and canonized keys that never reached the constants. Adding
`observer_type` for §1.1 would have been the 29th breach of an unheld rule
rather than an exception to a held one; that is why this FIX reconciles the
whole table instead of appending one row.

### 1.3 Symptom C: half the blocking gate is undocumented

`make ci-core` is the REQUIRED CI job. It runs **nine** steps:

```
ci-core: mock-check link-check tidy-check-core lint-core build-core \
         consumer-smoke test-core cover-check vuln-core
```

`CONTRIBUTING.md` §"Local CI parity" tells a contributor it lists "the same
checks GitHub Actions runs" and names **five** — omitting `mock-check`,
`link-check`, `consumer-smoke` and `cover-check`, the last of which is the
blocking diff-coverage gate. `CLAUDE.md`'s pipeline sentence names six,
omitting `mock-check`, `link-check` and `consumer-smoke`.

A contributor whose PR fails on a dead link or on diff-coverage meets a red
gate they were told nothing about.

---

## 2 Root cause analysis

### 2.1 `deliver` had no sink — and the recorded reason was wrong

The code comments its own omission:

```go
// … the recovered value is deliberately
// dropped because deliver has no sink to report it to. Surfacing it is
// worth doing — see FIX-034 §8.3.
```

`deliver` genuinely has no sink: it is a free function taking only the observer
and the Fact. But the **callers** do:

- `producer.subscribe` holds `p.log` (`producer.go:17`).
- `InstanceHandle.Observe` reaches `h.current().Logger()` — promoted through
  `Instance`'s embedded `renv.EngineRuntime`, which declares
  `Logger() observability.Logger`.

The backlog entry claimed the opposite — that `Logger()` "is not directly on
`Instance`". It is, by embedding, and `internal/instance/loop.go:221` already
calls `inst.Logger()`. **The blocker was recorded from inspection of the struct
rather than of the promoted method set**, and it deterred the fix for as long as
it stood. A one-line grep would have disproved it; that is the reusable lesson,
and it is why this FIX re-verified every claim it inherited.

The logger also cannot be nil, so no guard is needed: `WithLogger(nil)` is
rejected (`options.go:69`) and the default is `slog.Default()`
(`options.go:461`).

### 2.2 A registration rule with no machinery

§2.5's rule is prose in a document. Nothing in `make ci` reads it, so the only
thing standing between a new `Attr*` constant and the vocabulary is whether the
author remembers a sentence in an ADR they may never have opened. The block's
own comments name ten SRDs that added keys (SRD-050, 054, 059, 060, 063, 064,
068, 069, 073, 074), and 28 keys arrived unregistered. Prose rules decay at a
rate set by how often someone reads them.

The reverse direction failed for the same reason: `event_definition_type` and
`event_processor_id` are canonized in §2.5 yet written as bare literals in the
waiters, because nothing points a compiler at the difference.

### 2.3 Two hand-maintained lists of a nine-step target

`CLAUDE.md` and `CONTRIBUTING.md` each enumerate the gate in prose. `ci-core`'s
prerequisite list is the truth; both copies drift every time a step is added,
and each has drifted independently. FIX-034 added `link-check` and updated
neither — this FIX's own immediate ancestor demonstrating the failure mode.

### 2.4 Where the tests were

`pkg/thresher/observer_test.go:159` — `TestObserverPanicRecovered` — asserts
containment: the engine survives and a healthy peer still receives events. It
asserts nothing about *evidence*, because there was none to assert. The
canary was written to the behaviour, and the behaviour was half the
prescription.

Nothing at all tests the §2.5 vocabulary in either direction.

---

## 3 Solution

### 3.1 Alternatives considered

**A — report the panic as a `Fact` through the Reporter.**

| | |
|---|---|
| Pros | Uses the existing observation channel; a host already watching Facts would see it. |
| Cons | **Self-defeating and unbounded.** The Fact fans back out to every observer, including the one that just panicked, which panics again — reporting that panic emits another Fact, and so on. Containing the recursion needs a re-entrancy flag on a hot path. |
| Decision | ❌ rejected — the failure mode is worse than the defect. |

**B — log every panic at `Error`.**

| | |
|---|---|
| Pros | Simplest; loudest reading of ADR-022 §2.6. |
| Cons | Violates two rules at once. §2.4 defines `Error` as "an actionable failure handled here: **engine state was affected**" — contained, engine state is untouched. And its corollary: "a **hot path** (per-event, per-token, per-message) never logs above `Debug`" — observer delivery is per-event, so an unbounded per-panic record at any level above Debug is out of contract. A broken observer on a busy engine would drown every other record. |
| Decision | ❌ rejected. |

**C — count silently, summarize on `Cancel`.**

| | |
|---|---|
| Pros | Zero flood; the count stays available. |
| Cons | A long-running engine learns nothing until teardown — precisely the accidental silence §2.6 calls the worse defect. |
| Decision | ❌ rejected. |

**D — first panic loud and bounded, the rest counted.** ✅ **chosen**

The first panic per subscription logs at `Warn` with a stack trace; subsequent
ones log at `Debug`; every one increments a counter exposed as
`Subscription.Panicked()`.

- `Warn` is the level §2.4 defines for "degraded but continuing; someone should
  look eventually", which is exactly a contained observer failure — and it is
  the level ADR-013 §5 names ("drop-with-**warning**").
- Bounding the `Warn` to once per subscription is what keeps the hot-path
  corollary intact: the per-event record is `Debug`, as required.
- The counter is symmetric with the existing `Subscription.Dropped()`, so the
  two lossy paths — buffer overflow and observer panic — are queryable the same
  way, and the count stays authoritative regardless of log level.
- The stack is captured **only** for the record that carries it, so a flooding
  observer pays no `debug.Stack()` cost per Fact.

**For §1.2, the alternative was appending one row** (register `observer_type`,
leave the other 28). Rejected: it would ratify the drift while invoking the rule
that forbids it, and the next author would inherit a table that is authoritative
for exactly one key. The reconciliation is the point.

### 3.2 Changes by file

#### 3.2.1 `pkg/thresher/observer.go` — deliver reports; the drain decides

`deliver` returns what it recovered instead of discarding it, capturing a stack
only when asked:

```go
// before:
func deliver(o Observer, f observability.Fact) {
	defer func() { _ = recover() }() //nolint:errcheck // deliberate containment

	o.OnFact(f)
}

// after:
func deliver(
	o Observer, f observability.Fact, wantStack bool,
) (recovered any, stack []byte) {
	defer func() {
		if r := recover(); r != nil {
			recovered = r

			if wantStack {
				stack = debug.Stack()
			}
		}
	}()

	o.OnFact(f)

	return nil, nil
}
```

A `nil` return reliably means "no panic": since Go 1.21 `panic(nil)` is
recovered as a non-nil `*runtime.PanicNilError`, and every module pins
`toolchain go1.25.12`.

A new shared helper holds the policy so both drains behave identically:

```go
// deliverObserved calls o.OnFact under panic containment and records any panic
// per ADR-013 v.2 §5 (drop-with-warning): the first per subscription at Warn
// with a stack, later ones at Debug, all counted into panicked. The Warn is
// bounded to one per subscription so the per-event record stays Debug, honouring
// the ADR-022 v.1 §2.4 hot-path corollary.
func deliverObserved(
	log observability.Logger,
	o Observer,
	f observability.Fact,
	panicked *atomic.Uint64,
)
```

Both drain loops call it in place of `deliver`, and `Observe` captures the
logger once at registration.

#### 3.2.2 `pkg/thresher/producer.go` — the second call site

`producer.subscribe`'s drain switches to `deliverObserved`, passing `p.log` and
the subscription's new counter. No other change: the producer already holds
everything the policy needs.

#### 3.2.3 `pkg/thresher/observer.go` — `Subscription.Panicked()`

```go
// Panicked reports how many times this observer's OnFact panicked and was
// contained (ADR-013 v.2 §5). Best-effort, monotonic — the companion to
// Dropped(): a non-zero count means the host's observer is broken, not that
// the engine lost events.
func (s *Subscription) Panicked() uint64
```

`Subscription` gains a `panicked *atomic.Uint64` field beside `dropped`, set by
both constructors.

#### 3.2.4 `pkg/observability/fact.go` — `AttrObserverType`, and a comment realigned

Adds the constant the reconciled vocabulary registers:

```go
// AttrObserverType names the concrete Go type of a host observer whose OnFact
// panicked (FIX-035) — the only handle the engine has on it, since an observer
// is a host-supplied value the engine assigns no id.
AttrObserverType = "observer_type"
```

It also repairs a comment/code misalignment this FIX's grounding surfaced: the
call-activity comment at `fact.go:272-275` describes `called_key`,
`called_version` and `child_instance_id`, but the Ad-Hoc block (SRD-074) was
inserted between it and those constants, so it now reads as documentation for
`AttrCandidates`. The two blocks are re-joined with their constants.

#### 3.2.5 The literal sweep — 222 call sites reach the vocabulary through constants

Every hand-written key that has an `Attr*` constant is replaced by that
constant: `slog` argument pairs (`"error", err.Error()`), `errs.D` detail keys
(`errs.D("waiter_id", …)`), and `Details` map literals. The two canonized keys
that had no constant gain one — `AttrEventDefinitionType`,
`AttrEventProcessorID` — so all 27 affected keys resolve through the family.

**One class is deliberately untouched: struct tags.** `json:"version"` and
`json:"ordinal"` in `internal/instance/checkpoint/document.go` are the
checkpoint **wire format**, not log attributes; rewriting them would change
persisted documents. They collide with a vocabulary key by coincidence of
spelling, and the guard below excludes them structurally (`ast.Field.Tag` is
not an expression literal) rather than by name, so the exclusion cannot rot into
a stale allowlist.

#### 3.2.6 `docs/design/ADR-022-…-policy.md` → **v.2** (+ `.ru.md` twin)

§2.5's table is reconciled against all 48 keys (47 existing + `observer_type`),
split explicitly into **canonical entity keys** and **free-form descriptive
attributes**, so the boundary the rule depends on is legible rather than
inferred. `event_definition_type` and `event_processor_id` stay canonical and
gain constants (§3.2.4). The registration rule keeps its wording; what changes
is that the table it governs is now true.

Status flips to `Draft` on the bump per the versioning rule, and back to
`Accepted` at the PR handover once `/check-srd` passes. Its outgoing pins are
re-checked at the bump: **`SAD-001 v.1` → `v.1.1`** (stale), `ADR-002 v.2` and
`ADR-013 v.2` both current.

**Inbound pins stay at `v.1`.** Seven documents cite `ADR-022 v.1`. Five are
frozen one-shot SRD/FIX docs, never retro-edited. The two live ADRs (ADR-013,
ADR-034) cite it for §2.5's *existence* and for the discard rule, both unchanged
by v.2 — a superset table does not invalidate them, so they remain correctly
pinned to the version they were written against.

#### 3.2.7 `CLAUDE.md` — the pipeline sentence, and link-check described

The pipeline sentence is corrected to `ci-core`'s real nine steps, and
`link-check` gains a short paragraph beside the diff-coverage one: what it
checks, that it is offline and Go-only because the parity rule pins every CI
tool through `make tools`, and that external URLs and code spans are out of
scope by design.

#### 3.2.8 `CONTRIBUTING.md` — the four missing targets

The §"Local CI parity" list gains `mock-check`, `link-check`, `consumer-smoke`
and `cover-check`, each with the one line a contributor needs to know what a
failure means.

---

## 4 Verification

Current coverage: `pkg/thresher/observer_test.go` has containment tests
(`TestObserverPanicRecovered`) and drop-counter tests; nothing on panic
evidence, and nothing anywhere on the §2.5 vocabulary.

### 4.1 Regression tests

| Test | File | Setup | Assertion |
|---|---|---|---|
| `TestObserverPanicIsReported` | `observer_test.go` | handle observer panicking on every Fact; capturing `slog` handler | exactly one `Warn` record; carries `observer_type`, a stack, and the recovered value under `error` |
| `TestObserverPanicFloodIsBounded` | `observer_test.go` | same, ≥50 Facts delivered | still exactly one `Warn`; the rest `Debug`; `Panicked() >= 50` |
| `TestObserverPanickedCounter` | `observer_test.go` | panicking vs healthy observer | panicking `Panicked() > 0`; healthy stays `0` |
| `TestEngineObserverPanicIsReported` | `observe_engine_test.go` | panicking observer on `Thresher.Observe` | the engine-scope drain reports identically — the call site FIX-034 missed |
| `TestDeliverReturnsRecoveredValue` | `observer_internal_test.go` (new; follows the existing `producer_internal_test.go` convention) | `deliver` directly, panicking and non-panicking observers, `wantStack` both ways | returns the value; `nil` when no panic; stack present only when asked |
| `TestAttrConstantsAreRegistered` | `internal/lintcfg` | parse every `Attr*` constant; parse ADR-022 §2.5's tables | every constant appears in one of the two tables — the constants→doc drift cannot silently recur |
| `TestNoLiteralAttrKeys` | `internal/lintcfg` | AST-walk `pkg/` + `internal/`, collecting string literals equal to any `Attr*` value, skipping `fact.go` and every `ast.Field.Tag` | zero hits — a hand-written key that has a constant fails the gate; struct tags are structurally exempt, not allowlisted |

The last two tests are the machinery §2.2 says was missing, one per
direction: `TestAttrConstantsAreRegistered` closes constants→doc, and
`TestNoLiteralAttrKeys` closes call-sites→constants. Both live in
`internal/lintcfg` beside `TestNoMustCallsInLibrary`, the existing home for
repo-wide policy guards, so a reader finds every such rule in one package.

### 4.2 Gate

`make ci` green, with diff-coverage ≥ `COVER_MIN` on every touched file.

---

## 5 Prevention

- Both new exported symbols (`Subscription.Panicked`, `AttrObserverType`) carry
  doc comments citing the governing ADR section, so the *why* survives without
  this document.
- `deliverObserved`'s comment names the two rules its shape satisfies
  (ADR-013 §5's warning, ADR-022 §2.4's hot-path corollary), so a future
  simplification to "just log it" meets the reason it is not that.
- `TestAttrConstantsAreRegistered` converts §2.5's prose rule into a gate.
- `CLAUDE.md`/`CONTRIBUTING.md` now describe the gate they actually run; a
  future step addition has two named places to update, both listed in §6.1.

---

## 6 Regressions and side effects

### 6.1 What may rely on the old behaviour

- `grep -rn "deliver(" pkg/thresher/` — both call sites move to
  `deliverObserved`; `deliver`'s signature change is package-private.
- A host asserting on *silence* in logs while running a panicking observer would
  now see a `Warn`. No such test exists (`TestObserverPanicRecovered` asserts
  delivery, not quiet), and the new behaviour is the prescribed one.
- Adding a gate step later means updating `CLAUDE.md`'s pipeline sentence and
  `CONTRIBUTING.md`'s list.

### 6.2 Rollback

Single-commit revert per milestone; no data, schema or wire-format change.

---

## 7 Related

- [ADR-013 v.2](../design/ADR-013-instance-observability.md) §5 — the
  drop-with-warning prescription this completes; §2.7/§2.8 — the two Observe
  surfaces, both fixed.
- [ADR-022 v.1](../design/ADR-022-error-propagation-and-logging-policy.md)
  §2.4/§2.5/§2.6 — the levels, the vocabulary, silence-is-opt-out. Bumped to
  v.2 here (§3.2.5).
- [FIX-034](FIX-034-gate-blind-spots-and-doc-drift.md) — the same
  decided-rule-with-no-machinery shape, in the CI gate; its §8.3 recorded §1.1
  with the premise §2.1 disproves, and its `link-check` addition is §1.3's
  immediate cause.
- [FIX-022](FIX-022-error-logging-policy-remediation.md) — the sweep that landed
  ADR-022 §2.5's vocabulary "grounded against the code"; §1.2 measures how far
  it has drifted since.

---

## 8 Implementation summary

> Filled after landing.

### 8.1 Stages by commit

| Milestone | Commit | Scope | Tests |
|---|---|---|---|

### 8.2 Empirical findings — where reality diverged from the §3 draft

### 8.3 Backlog

---

## 9 Open questions

None.
