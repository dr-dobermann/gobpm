# FIX-019 «`errs` error details use `any`, forcing boxing + reflection on error paths»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Draft v.1 (2026-07-04, branch `test/harden-core-coverage`, not yet implemented).
**Date:** 2026-07-04.
**Author:** dr-dobermann.
**Branch:** `test/harden-core-coverage` (folded into the in-flight core-hardening work per the user's request — the `errs` redesign was surfaced while hardening `pkg/errs` coverage).
**Paired doc:** none (local to `pkg/errs`).
**Upstream:** self-contained per hierarchy-rule (no ADR/SAD governs the `errs` utility).

**Grounded in (internal artifacts):**
- `pkg/errs/{errors.go,error_options.go}` (current implementation).
- Call-site survey: 206 `errs.D(...)` sites across `pkg/` + `internal/` (non-test).

---

## §1 Symptoms

This is a **design/performance defect**, not a runtime crash: `errs` is invoked
on every error-construction path but its detail-value machinery is built on
`map[string]any`, which violates the package's intended contract — *fast,
minimal-overhead, no runtime reflection, no long conversion*. Three concrete
consequences, each with a code witness:

### §1.1 `any` boxing on every `D()` with a non-pointer value

```go
// pkg/errs/error_options.go:15
details map[string]any
// pkg/errs/error_options.go:96
func D(k string, v any) errOption {
```

Every `errs.D("count", n)` with a scalar (`int`, `Duration`, a named enum)
**heap-boxes** the value into `any` to store it in the map. On error paths that
build several details this is repeated allocation for values that are almost
always strings already.

### §1.2 Reflective `%v` formatting when rendering the error

```go
// pkg/errs/errors.go:129-131
str += "Details:\n"
for k, v := range ae.Details {
    str += fmt.Sprintf(" %s: %v\n", k, v)
}
```

`%v` over an `any` dispatches through `reflect` for every non-string detail each
time `Error()` is called (i.e. whenever the error is logged/rendered). String
concatenation in a loop also re-allocates.

### §1.3 Reflective, runtime-**fallible** JSON serialization

```go
// pkg/errs/errors.go:107-112
func (ae *ApplicationError) JSON() []byte {
    js, err := json.Marshal(ae)
    if err != nil {
        Panic("couldn't convert application error to json: " + err.Error())
        return nil
    }
    ...
// pkg/errs/errors.go:157-168  (MarshalJSON marshals Details map[string]any directly)
```

`json.Marshal` reflects over the value, and because `Details` is `map[string]any`
it **can genuinely fail** — a detail holding an unmarshalable value (a `chan`,
`func`, …) makes `MarshalJSON` error, firing the `Panic` branch. So the `any`
buys a latent panic-on-diagnostic on top of the reflection cost.

**User-visible impact:** avoidable allocation + reflection on the error path
(rendering is common under logging); a diagnostic serializer that can panic on
otherwise-valid `ApplicationError`s.

---

## §2 Root Cause Analysis

### §2.1 `map[string]any` is over-general for the actual detail values

Survey of all 206 non-test `errs.D(...)` call sites: **~95% already pass a
`string`** — `X.ID()`, `X.Name()`, `reflect.TypeOf(x).String()`, `st.String()`,
`slot.String()`, string literals. The non-string minority is small and trivial
to convert:

- **`int`** — `pkg/model/gateways/complex.go:51,59,441-442`, `.../gateway.go:335-336`
  (`count`, `len(...)`, `incoming_count`, …).
- **named string types** — `errs.D("direction", d)` (`activity_options.go:183`,
  `Direction`), `errs.D("event_definition_type", eDef.Type())`
  (`thresher.go:523`, `track.go:944`, `flow.EventTrigger`), state values
  (`signal.go:177`).
- **object / func (a diagnostic smell)** — `goexpr.go:58-60` passes a
  `data.Source`, an `*ItemDefinition`, and a **func**; two sites elsewhere pass a
  whole `EventDefinition` where its `.ID()` was intended.

`any` is therefore not earning its generality — the true shape is
`map[string]string`. Error *context* being open-ended is a real argument for
`any` in the abstract (cf. `slog.Any`), but the empirical call-set here does not
exercise it.

### §2.2 No tests exercise the reflective/fallible branches

`grep` of `pkg/errs/*_test.go` for `JSON`/`Details`: the `JSON()` marshal-failure
branch (§1.3) and the `Error()` detail loop (§1.2) have **no** direct coverage
(the failure branch is why `pkg/errs` sat at 80.2% before this hardening pass).
The absence let the `any`-driven cost and the latent panic persist unnoticed.

### §2.3 Contract mismatch

The package's implicit contract — a cheap, reflection-free error builder for hot
paths — is contradicted by its own storage choice. This is a design defect, not
a localized logic bug: the fix is to make the type match the contract.

---

## §3 Solution

Narrow error-detail values to `string`: `Details map[string]any` →
`map[string]string`, `D(k string, v any)` → `D(k, v string)`. The hot
construction/rendering paths become allocation-lean and reflection-free; the
`JSON()` failure branch becomes provably dead and is deleted.

### §3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. Generic `D[T any](k string, v T)` | type-inferred call sites | `T` erases to `any` at the `map[string]any` boundary — zero benefit; still boxes, still reflects | ❌ rejected |
| B. Constrain `v` to `fmt.Stringer` | forces a string form | **rejects the common `string`/`int` values** (neither implements `Stringer`); still lossy; still doesn't guarantee marshalability | ❌ rejected |
| C. Constrain `v` to a "JSON-compatible" type | compile-time safety | **inexpressible in Go** — constraints are allow-lists/method-sets; marshalability is a recursive structural runtime property (stdlib `json.Marshal` itself takes `any` + returns `error` for exactly this reason) | ❌ impossible |
| D. Keep `any`, stringify inside `MarshalJSON` (`%v`) | removes the JSON failure branch | retains `any` boxing (§1.1) and reflective `Error()` (§1.2) — fixes only the rarest path | ❌ rejected: doesn't address the hot path |
| E. **`map[string]string` + `D(k, v string)`** | no boxing; `Error()` uses `%s` (no reflection); `JSON()` can't fail → delete the branch; matches ~95% of call sites unchanged | ~20 non-string call sites need an explicit `strconv`/`string(...)`/`.ID()`; JSON details lose native-type fidelity (become strings) | ✅ chosen |

**JSON depth (decided — pragmatic):** keep `json.Marshal(map[string]string)` on
the opt-in `JSON()` path. It still reflects internally, but `JSON()` is a rare
diagnostic serializer, not a hot path; hand-rolling JSON escaping trades a real
correctness risk for a micro-optimization on a cold path. The hot
`New`/`D`/`Error` paths become reflection-free either way.

**JSON error handling (amended during implementation):** the original draft
deleted `JSON()`'s marshal-failure branch as dead code. On review, silently
swallowing a marshal error in a library is as objectionable as the old panic —
so `JSON()` **propagates** it instead: the signature changes from `() []byte` to
`() ([]byte, error)` and returns `json.Marshal(ae)` directly (no panic, no
swallow, no fallback). With `map[string]string` the error is unreachable in
practice, but a library surfaces it rather than hiding it. `JSON()` had **zero
production callers** (only one test), so the signature change is free.

### §3.2 Changes by file

#### §3.2.1 `pkg/errs/error_options.go` — string-typed details

```go
// before:
details map[string]any
func D(k string, v any) errOption { ... cfg.details[k] = v ... }
// after:
details map[string]string
func D(k, v string) errOption { ... cfg.details[k] = v ... }
```

Doc-comment on `D` states the string-only contract and *why* (§5). `newError`
copies `map[string]string`.

#### §3.2.2 `pkg/errs/errors.go` — field type, reflection-free render, dead-branch delete

- `ApplicationError.Details map[string]any` → `map[string]string`; `New`'s
  `details: map[string]any{}` init → `map[string]string{}`.
- `Error()` detail loop uses `%s` (no reflection) and a `strings.Builder`.
- `JSON()` — the old `if err != nil { Panic(...) }` branch is replaced by
  **propagating** the error: signature `() []byte` → `() ([]byte, error)`,
  body `return json.Marshal(ae)`. No panic, no swallow. Its one caller (a test)
  is updated. (See the "JSON error handling" note in §3.1.)
- `MarshalJSON()` anonymous struct field `Details map[string]any` →
  `map[string]string`.

#### §3.2.3 Call-site migration (mechanical, by conversion kind)

The ~13 `reflect.TypeOf(x).String()` sites are **already strings — unchanged**.
The non-string sites get an explicit, non-reflective conversion:

| Kind | Conversion | Sites (file:line) |
|---|---|---|
| `int` | `strconv.Itoa(n)` | `gateways/complex.go:51,59,60,441,442,453`, `gateways/gateway.go:335,336` |
| named string type | `string(v)` | `activities/activity_options.go:183` (`direction`), `thresher/thresher.go:523`, `internal/instance/track.go:944`, `events/timer.go:59-61`, `eventhub/waiters/signal.go:177` (as applicable) |
| object / func (smell) | pass `.ID()` / a meaningful string, or drop the detail | `data/goexpr/goexpr.go:58-60` (`ds`/`res`/`gfunc`), the 2 `EventDefinition()` sites |

Each converted site is verified to compile; the full set is the `go build ./...`
gate (a missed site is a compile error, not a silent slip).

---

## §4 Verification

Current `pkg/errs` coverage after slice-1 hardening: 96.7% (the residual is the
`JSON()` failure branch this FIX deletes, plus one white-box guard).

### §4.1 Regression tests (mandatory)

#### §4.1.1 `Error()` renders string details without reflection

**Update:** `pkg/errs/errors_test.go` (existing `TestErrors`/`TestJson`).

| Test | Setup | Assertion |
|---|---|---|
| detail rendering | `New(M("m"), D("id","x"), D("count","3"))` | `Error()` contains `id: x` and `count: 3`; `JSON()` round-trips `{"details":{"id":"x","count":"3"}}` |

#### §4.1.2 `errFunc.apply` nil-config guard (white-box)

**New:** `pkg/errs/apply_internal_test.go` (`package errs`).

| Test | Setup | Assertion |
|---|---|---|
| `TestErrFuncApplyNil` | `errFunc(func(*errConfig) error { return nil }).apply(nil)` | returns the "empty error configuration" error (the guard `New` never reaches) |

#### §4.1.3 JSON error handling

The `JSON()` panic branch (§1.3) is replaced by **propagating** the marshal
error (signature `() ([]byte, error)`). With `map[string]string` the error is
unreachable — a `map[string]string`/scalar struct cannot produce an
`UnsupportedTypeError` — so the error path isn't unit-testable, but surfacing
it (rather than panicking or swallowing) is the honest library contract. The
success path is covered by `TestJson`'s round-trip; §6.1 records the signature
change's blast radius (one test caller).

### §4.5 Observability

No new logs; the win is fewer allocations + no `reflect` frames on the
error-render path (visible in a CPU/alloc profile of an error-heavy run, not in
functional output).

---

## §5 Prevention

- **Doc comment on `D`** stating the contract: *values are pre-stringified by the
  caller; `errs` stores and renders them without reflection or allocation. This
  keeps error construction cheap on hot paths — do not reintroduce `any`.*
- **Doc comment on `ApplicationError.Details`** noting the `map[string]string`
  choice and the deleted failure mode.
- **Canary:** `TestErrFuncApplyNil` + the `Error()`/`JSON()` round-trip in
  §4.1.1 — if `Details` regains `any`, the round-trip's native-vs-string shape
  changes and the test flags it.

---

## §6 Regressions / side-effects

### §6.1 What may rely on the old behaviour

- **Callers reading `ApplicationError.Details` as `map[string]any`** — grep:
  `grep -rn "\.Details\[" --include=*.go pkg internal | grep -v _test` and
  `grep -rn "range .*\.Details" ...`. Any consumer type-asserting a detail value
  (`d.(int)`) breaks and must read the string. (Expected: none outside `errs`
  itself; confirm pre-landing.)
- **JSON detail fidelity** — machine consumers parsing `errs` JSON `details` as
  native types (`{"count":3}`) now see strings (`{"count":"3"}`). Diagnostic
  output only; confirm no downstream parser asserts native types.
- **`JSON()` signature change** — `() []byte` → `() ([]byte, error)`. Grep
  `grep -rn "\.JSON()" ...` confirmed **one** caller (a test), now updated to
  handle the returned error. No caller depends on the old panic-on-bad-detail
  behaviour (it was a latent failure, never a feature).

### §6.2 Rollback path

Single-commit revert of the `errs` core change + the call-site migration commit.
No data/migration involved.

### §6.3 Cross-team backlog

None — `pkg/errs` is internal to gobpm; all call sites are in-repo.

---

## §7 Related

- Self-contained per the hierarchy-rule; no ADR/SAD governs `errs`.
- **Promote-to-ADR candidate:** the invariant *"`errs` is a hot-path utility —
  reflection-free, allocation-lean, no `any` in its stored/rendered surface"* is
  a reusable design principle. If a second low-overhead-utility decision appears
  (e.g. a logging or metrics helper), promote this principle to a small ADR.
- Sits within the broader core-hardening pass on branch
  `test/harden-core-coverage` (coverage of `pkg/errs`, `pkg/model/data`, …).

---

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

> ⚠️ TODO: fill AFTER landing; records the implementation history and empirical
> findings vs the §3 draft.

### §8.1 Stages by commit (branch `test/harden-core-coverage`)
| Stage | Commit | Scope | Tests |
|---|---|---|---|
| 1 | `<sha>` | `errs` core: `map[string]string`, `D(k,v string)`, reflection-free `Error()`, delete `JSON()` failure branch | `<count + names>` |
| 2 | `<sha>` | call-site migration (int/named/object conversions) | build gate |

### §8.2 Empirical findings — where reality diverged from the §3 draft
> ⚠️ TODO: e.g. any call site whose value could not be cheaply stringified;
> any consumer of `.Details` found that needed a fix (would expand §3.2.3).

### §8.3 Backlog (out of FIX-019 scope)
- The `goexpr.go` / `EventDefinition()` detail sites that pass whole objects are
  a diagnostic smell; this FIX converts them to `.ID()`/drop, but a broader
  "detail hygiene" sweep (consistent id-only diagnostics) is future work.
