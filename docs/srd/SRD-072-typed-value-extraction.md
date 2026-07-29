# SRD-072 — Typed value extraction (`data.As`)

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-07-29 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-034 v.1](../design/ADR-034-data-layer-generics-policy.md) §2.3 (the typed extraction edge — `data.As[T]`, nil-guarded, self-identifying) |
| Upstream | [ADR-011 v.7](../design/ADR-011-process-data-flow.md) (the `Value` family and its dynamic concretes); [ADR-010 v.2](../design/ADR-010-process-data-model.md) (the name-addressed data plane the bare values come from); [ADR-022 v.1](../design/ADR-022-error-propagation-and-logging-policy.md) (silent discards are forbidden — the rule this edge enforces at the API shape) |

## §1 Background

ADR-034 fixes the data layer's generics policy: the `Value` interfaces stay
dynamic, generics live at the edges (construction, T-suffix twins, adapters) —
and names the one **missing** edge: a consumer holding a *bare* `data.Value`
has no typed extraction. Bare values are handed out by every read seam — an
item-aware element's `Value()`, a `Record.Field` read (`pkg/model/data/record.go`
— `Field` returns `(Value, error)`), an external job's `Structure()`, a path
walk — and today the payload comes out via a hand assertion:

```go
amount, _ := d.Value().Get(ctx).(int)   // examples/multi-instance-sequential/tasks.go:26
```

The discarded comma-ok recurs across the shipped examples
(`multi-instance-behavior/tasks.go:26`, `service-task-worker/worker.go:68`,
`conditional-events/process.go:64`, `inclusive-join/process.go:158`, …). A
mismatch silently yields the zero value and the process continues with wrong
data — the silent-discard class ADR-022 forbids. This SRD lands the helper
that closes the edge: `data.As[T]`.

## §2 Requirements

### Functional

- **FR-1** — `pkg/model/data` exports `As[T any](ctx context.Context, v Value) (T, error)`
  returning `v`'s payload (`v.Get(ctx)`) as `T`.
- **FR-2** — a nil `v` is rejected with an `errs.ApplicationError` classed
  `errs.InvalidParameter`, message self-identifying the function and parameter
  (`"As: a nil Value isn't allowed"`). Never a zero-value return.
- **FR-3** — a payload that is not a `T` is rejected with an error classed
  `errs.TypeCastingError` whose message names **both** the held dynamic type
  and the requested type, with structured details (`held`, `requested`) for
  observability drilldown.
- **FR-4** — `T` may be any type, including an **interface** type: `As` must
  report the requested *interface* name correctly on mismatch (naive
  `fmt.Sprintf("%T", zero)` prints `<nil>` for a zero interface — see §4.1).
- **FR-5** — the developer manual presents `As` as the canonical extraction
  idiom (`docs/guides/data/value-model.md`), and `CHANGELOG.md` `[Unreleased]`
  records the addition. Example migration stays opportunistic per ADR-034 §3 —
  no sweep in this SRD.

### Non-functional

- **NFR-1** — no reflection on the success path; reflection is permitted only
  in composing the *mismatch error message* (cold error path — consistent
  with the layer's bounded-reflection stance, ADR-011 v.7 §2.9.5).
- **NFR-2** — the new file lands at 100% test coverage (house floor 95%).
- **NFR-3** — godoc on the exported function; `make ci` green across modules.

## §3 Models

One new file, `pkg/model/data/as.go` (one entity per file):

```go
// As returns v's payload as T. It rejects a nil Value and, when the
// payload is not a T, reports a self-identifying error naming both the
// held and the requested type — instead of the silent zero value a
// discarded type assertion produces.
func As[T any](ctx context.Context, v Value) (T, error) {
	var zero T

	if v == nil {
		return zero, errs.New(
			errs.M("As: a nil Value isn't allowed"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	payload := v.Get(ctx)

	t, ok := payload.(T)
	if !ok {
		requested := reflect.TypeFor[T]().String()

		return zero, errs.New(
			errs.M("As: value holds %T, not %s", payload, requested),
			errs.C(errorClass, errs.TypeCastingError),
			errs.D("held", fmt.Sprintf("%T", payload)),
			errs.D("requested", requested))
	}

	return t, nil
}
```

Notes:

- `errorClass` is the `data` package's existing error-class variable (as used
  by `association.go`).
- `errs.TypeCastingError` (`INVALID_TYPECASTING`) is the established class for
  exactly this failure; `errs.InvalidParameter` for the nil guard.
- No new types, no interface changes — the function is purely additive.

## §4 Analysis

### §4.1 Requested-type naming: `reflect.TypeFor[T]`, not `%T` of the zero (FR-4)

Two candidates for naming the requested type in the mismatch message:

1. `fmt.Sprintf("%T", zero)` — allocation-free, no reflection — but for an
   interface `T` the zero value is a nil interface and `%T` prints `<nil>`:
   `As[data.Collection]` would report `"value holds string, not <nil>"`,
   destroying the error's self-identification exactly where it's hardest to
   debug. Rejected.
2. `reflect.TypeFor[T]().String()` — names any `T` exactly, including
   interfaces (`data.Collection`) and named types. It is reflection, but only
   on the **cold mismatch path** (NFR-1); the success path performs a plain
   type assertion. Chosen.

### §4.2 `(T, error)`, not `(T, bool)` (FR-3)

A boolean is the shape that produced the defect class — the examples discard
it today. An `error` return travels the ordinary handling path, joins `%w`
chains, and carries the `errs` classes/details observability expects
(ADR-022 v.1). A comma-ok variant is deliberately not offered.

### §4.3 Package placement: `data`, not `values`

`As` operates on the `data.Value` interface and needs nothing from the
concretes; `values` imports `data`, so the reverse placement would also force
every consumer of a bare `Value` to import a concretes package for an
interface-level operation. It lands beside the interface it serves, sharing
the package's `errorClass`.

## §5 API

```go
func As[T any](ctx context.Context, v Value) (T, error)
```

Additive; no existing signature changes. The T-suffix twins on the generic
concretes remain the preferred access when the concrete type is in hand
(ADR-034 v.1 §2.2/§2.3).

## §6 Tests

`pkg/model/data/as_test.go`, table-driven where shapes allow:

| Test | Asserts |
|---|---|
| `TestAsScalar` | `As[int]` over a `values.Variable[int]` returns the payload; no error. |
| `TestAsStruct` | `As[order]` over a `values.Variable[order]` (struct payload) round-trips. |
| `TestAsCollectionElement` | `As[int]` over a `values.Array[int]` yields the element at the cursor (the `Collection.Get` contract). |
| `TestAsMap` | `As[map[string]int]` over a `values.Map[int]` returns the entry set (`Map.Get` returns the cloned `map[string]T`). |
| `TestAsRecord` | `As[map[string]any]` over a `values.Record` returns the field-name → payload map (the `Record.Get` contract). |
| `TestAsInterface` | `As[fmt.Stringer]` over a payload whose dynamic type implements it returns the payload as the interface; no error. |
| `TestAsNilValue` | nil `Value` → error classed `INVALID_PARAMETER`; message contains `"As: a nil Value isn't allowed"`. |
| `TestAsMismatch` | `As[int]` over a `string` payload → error classed `INVALID_TYPECASTING`; message names `string` and `int`; details `held`/`requested` populated. |
| `TestAsMismatchInterfaceRequested` | `As[data.Collection]` over an `int` payload → mismatch message names `data.Collection` (not `<nil>`) — the §4.1 regression pin. |

`As` asserts the payload `Get` returns — a `Variable[T].Get` yields `T`, an
`Array[T].Get` the element at the cursor, a `Map[T].Get` the cloned
`map[string]T` entry set, a `Record.Get` the `map[string]any` of field
payloads — never the `Value` container itself; the tests pin each concrete
family's payload shape.

## §7 Milestones

- **M1** — `as.go` + `as_test.go`, 100% file coverage, `make ci` green.
- **M2** — docs: `docs/guides/data/value-model.md` gains the canonical-idiom
  section; `CHANGELOG.md` `[Unreleased]` entry.
- **M3** — landing gate: `/check-srd` PASS; ADR-034 flips Draft → Accepted
  (+ RU twin per the bilingual policy); this SRD flips Draft → Accepted with
  §10 filled.

## §9 Definition of Done

- `make ci` green (all modules); diff-coverage ≥95% on touched lines, `as.go`
  at 100%.
- All §6 tests present and green under `-race`.
- FR-1…FR-5, NFR-1…NFR-3 verifiably satisfied.
- `/check-srd` PASS; ADR-034 Accepted with RU twin; no conformance-tracker
  change (this is an engine API addition, not a BPMN element).

## §10 Implementation summary

### §10.1 Stages by commit

| Stage | Commit | Scope |
|---|---|---|
| ADR-034 v.1 draft | `c670cfc` | `docs/design/ADR-034-data-layer-generics-policy.md` |
| SRD-072 draft | `cb8c206` | this document |
| M1 + M2 — helper, tests, docs | `7fdceff` | `pkg/model/data/as.go`, `as_test.go`, `docs/guides/data/value-model.md`, `CHANGELOG.md` |
| M3 — landing gate + flips + ADR RU twin | *the acceptance commit* | ADR-034 → Accepted (+ `.ru.md` twin), this §10, both status flips |

### §10.2 Deltas vs the draft

None — the landed code matches §3 verbatim; the §6 table was extended with
the `Map`/`Record`/cursor-element payload rows during pre-approval review
(draft evolution, not an implementation delta).

### §10.3 Backlog (out of scope)

- Typed positional variants (`AsAt[T]` and kin) and any `MustAs` — each
  waits for a concrete driver (ADR-034 v.1 §2.3 non-goals).
- Opportunistic migration of the examples' hand assertions to `As` — per
  ADR-034 v.1 §3, no sweep; touched examples adopt it as they are edited.

Verification: `make ci` green across modules (lint 0 issues, race tests ok,
consumer-smoke ✓, govulncheck clean); diff-coverage 100.0% of 21 changed
coverable lines (min 95%); `as.go` at 100% file coverage; `/check-srd` PASS
(14 🟢 / 0 🟡 / 0 🔴).

## Open questions

None.
