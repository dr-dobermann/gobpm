# SRD-047 — Structural data S5: the map kind

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-07-19 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-011 v.7](../design/ADR-011-process-data-flow.md) §2.9.7 (the map kind — homogeneous values under data keys) — the S5 slice (§2.9.6): the `Map` capability, the dynamic `values.Map[T]`, the `["key"]` path step on read, write, and vivify, the per-entry commit-diff walk, and the adapter tier's `map[string]V` lift. S5 is the slice ADR-011 v.7 was bumped for; this SRD lands it whole. |
| Upstream | [ADR-010 v.2](../design/ADR-010-process-data-model.md) §2.7 (the data plane the values live in), [ADR-012 v.1](../design/ADR-012-execution-layering.md) (public contracts in `pkg/*`) |
| Refines | SRD-042 (S1 — the read path whose grammar and walk gain the key step), SRD-043 (S2 — the write path whose vivify gains the map arm), SRD-044 (S3 — the commit-diff that gains `diffMaps`), SRD-045 (S4 — the adapter tier whose "map field — opaque leaf" fallback narrows) — by number, sideways. This SRD consumes the SRD-047 numbering reservation recorded in SRD-048's header note. |

S1–S4 made values navigable, writable, change-detected, and native-struct-backed
— for three kinds: `scalar｜list｜record`. S5 adds the fourth: a **map** —
homogeneous values under **data** keys (a record's keys are its schema; a map's
keys are data, ADR-011 v.7 §2.9.7). After this SRD a process holds a dictionary
it grows key-by-key (`rates["EUR"]`), conditions and mappings address map
entries through the one resolver, commit-diff reports per-entry changes with
the existing `ChangeType` vocabulary, and a native `map[string]V` struct field
is navigable over the live map instead of an opaque leaf.

## 1. Background

The engine's kind discovery is capability assertion (`kindOf`,
`pkg/model/data/shape.go:19-28`): `Record` → record, `Collection` → list,
anything else → scalar. Every structural seam probes the same two capabilities:

- **read walk** — `WalkSteps` (`pkg/model/data/path.go:234-277`): a field step
  asserts `Record`, an index step asserts `Collection`; raw collection elements
  are wrapped read-only by `scalarLeaf` (`path.go:296-324`);
- **grammar** — `Step{Field, Index}` (`path.go:21-24`), discriminated by
  `Field != ""` (`path.go:29`); a `[` is parsed **unconditionally as a numeric
  index** — a quoted key today fails as `bad index` (`path.go:88-90`), so the
  `["key"]` form is free grammar space;
- **write walk** — `SetPath` → `walkToParent` → `descendOrVivify` → `setLast`
  (`pkg/model/data/values/setpath.go:21-126`); `vivify` builds `MustRecord()`
  for a following field step, `NewArray[data.Value]()` for a following index
  step (`setpath.go:130-136`);
- **commit-diff** — `diffInto` probes Record-pair, then Collection-pair, then
  kind-change, then scalar (`pkg/model/data/diff.go:48-93`); `diffRecords`
  walks the union of keys (`diff.go:97-121`);
- **adapters** — `classifyType` resolves custom → passthrough → struct →
  pointer-to-struct → slice → **leaf** (`adapters/builder.go:91-113`); a Go map
  field matches none of the arms and falls to `kindLeaf` — pinned by the
  sub-test `"map field — opaque leaf, not navigable"`
  (`adapters/structrecord_test.go:68-75`) and documented as the deliberate
  §4.5 deferral in SRD-045.

Every one of those seams consumes the capability interfaces only, so the map
kind threads through as **one additive arm per seam** — no landed surface
changes shape (ADR-011 v.7 §2.9.6, S5).

## 2. Requirements

### 2.1 Functional

- **FR-1 — the `data.Map` capability** (`pkg/model/data/value.go`). A fourth
  optional structural capability beside `Record`/`Collection`:

  ```go
  // Map is the optional dictionary capability of a Value (ADR-011 v.7
  // §2.9.7): homogeneous values under data keys. Keys are arbitrary
  // NON-EMPTY strings; enumeration is always sorted (deterministic over
  // Go's randomized map iteration). A Value implements at most one
  // structural capability (§2.9.1).
  type Map interface {
      Value

      // Keys lists all entry keys in ascending (sorted) order.
      Keys() []string

      // Entry returns the value stored under key, or a classified
      // errs.ObjectNotFound error when the entry is absent. Like
      // Collection.GetAt, the result may be a raw Go value — path walks
      // wrap it read-only (scalarLeaf).
      Entry(ctx context.Context, key string) (any, error)

      // SetEntry upserts the entry under key — a map is permissive on
      // the key by definition (keys are data); the value side is
      // owner-enforced. An empty key is a classified error.
      SetEntry(ctx context.Context, key string, value any) error

      // DeleteEntry removes the entry under key, or returns a classified
      // errs.ObjectNotFound error when it is absent (fail-loud, like
      // Entry).
      DeleteEntry(ctx context.Context, key string) error
  }
  ```

  The value side follows the **Collection idiom** (raw `any` + wrap at the
  seams), not the Record idiom (`Value`-typed fields) — rationale in §4.1.

- **FR-2 — the dynamic `values.Map[T]`**
  (`pkg/model/data/values/map.go`, helpers `map_t.go`). Generic, mirroring
  `Array[T]` (`values/array.go`):
  - `NewMap[T any](entries map[string]T) (*Map[T], error)` — copies the input
    (nil → empty); an empty-string key in the input is a classified error.
    `MustMap[T]` panics on error (the `Must*` convention).
  - `Get` snapshots to a `map[string]T` copy; `Update` **replaces** the entry
    set with the given `map[string]T` (ADR-011 v.7 §2.9.7 — replace, not
    merge), rejecting any other payload shape and empty keys.
  - Capability methods per FR-1; `SetEntry` converts through the same
    `checkValue[T]` contract `Array[T].SetAt` uses (`array.go:267-282`), so a
    `data.Value` payload and a plain `T` behave identically across both
    homogeneous kinds.
  - `Type()`, `Clone()`, `Lock`/`Unlock` mirror `Array[T]`'s implementations;
    there is **no iteration cursor** (a Map is not a Collection — §4.9).
  - `map_t.go` carries the typed helpers mirroring `array_t.go`'s shape
    (`EntryT`, `SetEntryT`).

- **FR-3 — kind surfaces** (`pkg/model/data/shape.go`). `kindOf` gains
  `case Map: return "map"` (probe order documented: Record, Collection, Map —
  a value implements at most one, ADR-011 v.7 §2.9.1); `FieldInfo.Kind`
  vocabulary becomes `"scalar" | "list" | "record" | "map"`; `SchemaAt` gains
  a map arm answering one `["*"]` element slot (the map counterpart of the
  list's `"[]"` slot — same first-element mechanics, sorted-first entry,
  `"unknown"` when empty; §4.6); `Walk`/`walkChildren` descend map entries in
  sorted key order with `["key"]`-rendered child paths.

- **FR-4 — the path grammar** (`pkg/model/data/path.go`). `Step` gains a
  `Key string` field and stays **comparable** (`PathsOverlap` relies on
  element-wise `==`, `path.go:210-216`): a field step has `Field != ""`, a
  **key step** has `Field == "" && Key != ""`, an index step has both empty —
  unambiguous because map keys are non-empty (§4.2) and field names are
  CheckName-legal. `nextStep`'s `[` arm branches on a leading `"`: a
  double-quoted, backslash-escapable key (`\"` and `\\` only; any other
  escape, an unterminated quote, an empty key, or a missing `]` is a
  classified `InvalidParameter` error) — parsed character-wise, so `]` and
  `[` are legal **inside** a quoted key; a bare number stays the index step
  verbatim. A shared `keyLabel(key)` renderer produces the canonical
  escaped `["key"]` form for walked prefixes, diff paths, and error texts.

- **FR-5 — the read walk** (`pkg/model/data/path.go`). `WalkSteps` gains the
  key-step arm: assert `Map` (else `notNavigable(walked, "a map", …)`), call
  `Entry`, wrap a raw result via the existing `scalarLeaf`/`asValue`
  mechanics. `ResolvePath` and `resolveInto` need no change beyond the step
  vocabulary — so **conditions, expressions, mappings, and `Source.Find`
  read map paths with zero consumer change** (the S1 acceptance shape).

- **FR-6 — the write walk** (`pkg/model/data/values/setpath.go`).
  `descendOrVivify` and `setLast` gain the key-step arm: assert `Map` (else
  `notWritable(keyLabel(k), "a map", …)`), descend an existing entry
  (a raw non-Value entry is `notWritable`, mirroring the collection arm),
  vivify a missing one; `setLast` upserts via `SetEntry`. `vivify` gains the
  third arm: a following key step builds `NewMap[data.Value]()` — the exact
  map counterpart of `NewArray[data.Value]()` (`setpath.go:130-136`).

- **FR-7 — the commit-diff** (`pkg/model/data/diff.go`). `diffInto` gains the
  Map-pair probe (after Record-pair and Collection-pair) → `diffMaps`: the
  union of keys — new-side keys in sorted order (Added/Updated), then
  old-only keys in sorted order (Deleted) — each entry recursing through
  `diffInto` at `path + keyLabel(k)`; the kind-change guard (`diff.go:84-88`)
  extends to the map probes so `map↔record/list/scalar` is one `ValueUpdated`
  with no descent. `Scope.Commit`/`Frame.Commit`
  (`internal/scope/scope.go:254-266`, `frame.go:256-283`) and the
  `KindDataChange` fact emission (`internal/instance/datachange.go:12-35`)
  consume `[]Change` unchanged — **zero observability surface change**; map
  entry paths flow through `AttrDataPath` as strings.

- **FR-8 — the adapter lift** (`pkg/model/data/adapters/`). `classifyType`
  gains, between the slice arm and the leaf fallback: a `reflect.Map` whose
  key **kind** is `reflect.String` (named string types included) →
  `kindMap`; any other key type keeps falling to `kindLeaf` (ADR-011 v.7
  §2.8 — no key stringification). A new `mapValue` view (file
  `adapters/mapvalue.go`) implements `data.Map` over the **live** Go map —
  wrap, not convert: `Keys` sorted, `SetEntry` coerced through the existing
  `coerce` and written via `reflect.Value.SetMapIndex` (allocating a nil
  map on first write), `DeleteEntry` via `SetMapIndex` with the zero
  `Value`, locking and `Clone` (detached) per `sliceCollection`'s contract.
  `structRecord.Field` dispatches `kindMap` to the view. **Top-level wrap**:
  `Wrap(&m)` for `m map[string]V` returns the standalone `mapValue` (the
  "process genuinely holds a dictionary" driver), branching before the
  struct-only `buildAdapter` guard (`builder.go:18-23`). The SRD-045 pinned
  sub-test `"map field — opaque leaf, not navigable"`
  (`structrecord_test.go:68-75`) **flips** into the navigability test; the
  opaque-leaf behavior is re-pinned on a non-string-keyed fixture
  (`map[int]V`).

  **The write contract is entry-level (Go maps are not addressable).**
  Unlike a slice element (`s.slice.Index(i)` is addressable, so
  `sliceCollection` hands out live sub-views that write through), a map
  value read via `reflect.Value.MapIndex` is **never addressable** — there
  is no pointer into a Go map. So the adapter map's write path is
  **entry-level**: `SetEntry` / `DeleteEntry` replace or remove a whole
  value, live (the idiomatic Go copy-modify-restore). A **scalar / leaf**
  or **passthrough (`data.Value`)** element is served live — the scalar
  round-trips through `SetEntry`, the passthrough IS the stored mutable
  value. A **composite** element (a struct, `*struct`, slice, or nested
  map value) is served as a **read-navigable frozen snapshot** (`freeze`,
  `adapters/frozen.go`): its read methods work over a materialized copy,
  its mutators — `SetField` / `SetAt` / `Update` / … — return a classified
  error (`"a native map value isn't addressable; re-upsert the whole entry
  via SetEntry"`), so a deep `SetPath` into a composite map entry **fails
  loud** instead of silently writing to a detached copy. This is a recorded
  engine choice (see §4.8); a future slice may make `map[string]*struct`
  deep-write-through without changing this seam.

### 2.2 Non-functional

- **NFR-1 — determinism.** Every enumeration surface over map entries —
  `Keys()`, `SchemaAt`, `Walk`, `diffMaps` — is sorted-key ordered; two runs
  over equal graphs produce identical output (ADR-011 v.7 §2.9.7).
- **NFR-2 — no hot-path reflection.** The adapter's reflection stays where S4
  bounded it — type classification once, cached; `mapValue` per-access work is
  cached-kind dispatch plus the unavoidable `reflect` map access on the live
  value, exactly the `sliceCollection` precedent (SRD-045 §4).
- **NFR-3 — additive only.** No landed public surface changes shape or
  behavior; the only intentional behavior change is the FR-8 lift of
  string-keyed map fields (pinned today as opaque — the flip is the point of
  the slice). `Step` gains a field but stays comparable and
  backward-constructible (`Step{Field: "x"}` / `Step{Index: 0}` literals in
  existing code remain valid).
- **NFR-4 — the CI gate.** `make ci` green (tidy → lint → build → race tests →
  diff-coverage ≥ `COVER_MIN` (95%) → govulncheck); every touched function
  ≥80% covered, aiming 100%, measured per function (`go tool cover -func`).
- **NFR-5 — fail-loud keys.** The empty key is rejected everywhere it could
  enter (constructor input, `SetEntry`, `Update` payload, the path lexer);
  no seam silently normalizes or skips it.

## 3. Models

### 3.1 Package surface (new/changed declarations)

```go
// pkg/model/data/value.go
type Map interface { /* FR-1 */ }

// pkg/model/data/path.go
type Step struct {
    Field string // ".field"  — record step
    Index int    // "[0]"     — list step
    Key   string // `["key"]` — map step (non-empty ⇒ key step)
}

// pkg/model/data/values/map.go
type Map[T any] struct {
    entries map[string]T
    lock    sync.Mutex
}
func NewMap[T any](entries map[string]T) (*Map[T], error)
func MustMap[T any](entries map[string]T) *Map[T]

// pkg/model/data/adapters/mapvalue.go
type mapValue struct { /* live-map data.Map view, sliceCollection's sibling */ }
```

### 3.2 Worked example — the full data path

A process accumulates FX rates under run-time currency codes and a native
config struct carries a string-keyed map field:

```go
// dynamic tier — assemble, navigate, mutate
rates := values.MustMap[float64](map[string]float64{"EUR": 1.08})
rates.SetEntry(ctx, "GBP", 1.27)            // upsert: keys are data
keys := rates.Keys()                        // ["EUR" "GBP"] — sorted, always

// path read — one resolver for mappings, expressions, conditions
v, err := source.Find(ctx, `fx["EUR"]`)     // the ["key"] step (FR-4/FR-5)

// path write — vivify: "quotes" is missing; the following ["key"] step
// builds an empty dynamic map, then the entry is upserted (FR-6)
err = values.SetPath(ctx, root, `quotes["EUR"].bid`, bid)

// native tier — the live map behind the same capability (FR-8)
type Config struct {
    Limits map[string]int `gobpm:"limits"`
}
cfg := Config{Limits: map[string]int{"day": 100}}
w := adapters.MustWrap(&cfg)
lim, _ := w.(data.Record).Field(ctx, "limits") // a data.Map view
lim.(data.Map).SetEntry(ctx, "week", 500)      // writes the live cfg.Limits

// commit-diff — per-entry changes, existing vocabulary (FR-7)
// old: fx = {"EUR": 1.08, "GBP": 1.27}   new: fx = {"EUR": 1.09, "JPY": 161}
// DiffValues("fx", oldV, newV) →
//   {`fx["EUR"]`, Value_Updated}
//   {`fx["JPY"]`, Value_Added}
//   {`fx["GBP"]`, Value_Deleted}
// → one KindDataChange fact per path (internal/instance/datachange.go).
```

Bracket disambiguation: `items[0]` indexes a list; `fx["0"]` addresses the map
key `"0"`; `fx["a\"b"]` addresses the key `a"b` (escaped quote); `fx[""]` is a
classified parse error (empty key, NFR-5).

## 4. Analysis & decisions

### 4.1 The value side follows the Collection idiom, not the Record idiom

`Entry` returns `(any, error)` and `SetEntry` accepts `any` — the
`Collection.GetAt`/`SetAt` shape (`value.go:79,85`) — rather than `Record`'s
`Value`-typed `Field`/`SetField`. A map is a **homogeneous** kind like a list:
`values.Map[T]` holds raw `T` exactly as `Array[T]` holds `[]T`, and the seams
already own the raw-to-Value wrapping (`scalarLeaf`, `asValue`, `diffInto`'s
`any` walk). *Alternative — Value-typed entries (Record idiom):* forces
`Map[T]` to allocate a wrapper per read, breaks the `Array[T]` symmetry the
ADR pins (§2.9.7 "mirrors `Array[T]`"), and buys nothing — the walker wraps
anyway. *Alternative — both (generic `Entry` returning `T`):* not expressible
on a non-generic capability interface. Rejected.

### 4.2 Keys are non-empty; `Step` stays a comparable sentinel struct

`PathsOverlap` compares steps element-wise (`path.go:210-216` — "a Step is
comparable"), and the existing discrimination is sentinel-based
(`Field != ""`). Admitting the empty map key would force an explicit
discriminator field on `Step` (an empty `Key` could no longer mean "not a key
step"), breaking every `Step{...}` literal and the comparability idiom, to
support a key that is a near-certain authoring error. So: **map keys are
arbitrary non-empty strings**; `[""]` fails in the lexer, `SetEntry("")` and a
`""` key in a constructor/`Update` payload fail in the value (NFR-5). ADR-011
v.7 says "arbitrary strings"; excluding the empty string is a bounded,
fail-loud restriction this SRD decides (the ADR is silent on the empty key).
*Alternative — a `StepKind` enum on `Step`:* cleaner in the abstract, but a
breaking reshape of a landed S1 surface for zero user-visible gain. Rejected.

### 4.3 The quoted-key lexer is strict and character-wise

The current index arm scans to the first `]` (`path.go:83`); a quoted key may
legally contain `]`, so the key arm parses character-wise inside the quotes
with exactly two escapes: `\"` and `\\`. Any other backslash sequence is an
error — a lax lexer that passes unknown escapes through would make `\n` mean
two characters today and tempt a breaking reinterpretation later.
*Alternative — full Go/JSON escape set (`\n`, `\uXXXX`):* map keys come from
process data, not string literals; control characters in keys are served
verbatim by `Get`/`Keys` and need no escape to *exist* — only `"` and `\`
collide with the path syntax itself. The minimal set is complete; extending it
later is additive. *Alternative — single quotes:* one canonical form keeps
`keyLabel` rendering unambiguous (a path is also an identity — diff paths and
`PathsOverlap` compare parsed steps, but humans compare strings). Rejected.

### 4.4 `Keys()` sorts on every call

`values.Map[T]` stores a plain Go map and sorts in `Keys()` (and `diffMaps`
consumes `Keys()`), rather than maintaining a sorted index beside the map.
Enumeration is not the hot path (writes are); keeping one representation
avoids a second structure that can drift. *Alternative — a maintained sorted
slice (the `values.Record.order` pattern):* `Record`'s slice preserves
*insertion* order — meaningful schema order. A map has no meaningful insertion
order (ADR-011 v.7 §2.9.7), so the slice would exist only as a sort cache;
premature. Rejected (revisit only if profiling shows enumeration hot).

### 4.5 `Update` replaces, and accepts exactly `map[string]T`

Per ADR-011 v.7 §2.9.7: replace-on-`Update` (`Get`/`Update` round-trip; a
replace can delete). The payload contract is strict — `map[string]T` only,
through the same `checkValue` conversion `Array[T]`/`Variable[T]` use —
because a permissive `map[string]any` acceptance would re-introduce silent
per-entry coercion. *Alternative — merge-upsert:* rejected by the ADR (merge
can never delete; `Record`'s merge rationale — unknown keys are schema
violations — does not transfer).

### 4.6 `SchemaAt` answers one `["*"]` slot

The map arm mirrors the list arm (`shape.go:67-78`): one slot named `["*"]`,
kind from the sorted-first entry, `"unknown"` when empty. `["*"]` is a slot
label, not an addressable key — exactly as the list's `"[]"` is not an index.
*Alternative — enumerate the actual keys (the record arm):* a record's keys
are its schema, so enumerating them *is* answering the shape; a map's keys are
data — enumerating them would make `SchemaAt` answer *content*, and its output
size data-dependent. The homogeneous one-slot answer is the shape. Rejected.

### 4.7 `diffMaps` mirrors `diffRecords`, sorted

Union of keys; new-side keys sorted (Added/Updated), then old-only keys
sorted (Deleted) — `diffRecords`' structure (`diff.go:97-121`) with sorted
order replacing record insertion order (NFR-1). Entries recurse through
`diffInto`, so nested maps/records/lists diff to leaf granularity, and the
`oldV == nil / newV == nil` arms already yield Added/Deleted per entry.
*Alternative — one `ValueUpdated` for any map change:* discards exactly the
per-path precision the conditional-event substrate and `KindDataChange` facts
exist for (ADR-011 v.7 §2.9.4). Rejected.

### 4.8 Adapter classification: string-kind keys only, placed before the leaf

`classifyType` tests `t.Kind() == reflect.Map && t.Key().Kind() ==
reflect.String` after the slice arm — named string key types (`type Code
string`) qualify by kind, matching how the engine treats named types
everywhere else; every other key type falls through to `kindLeaf` exactly as
today (ADR-011 v.7 §2.8 — no stringification; `Register[T]` is the per-type
lift, `registry.go:22-42`). The `mapValue` view follows `sliceCollection`
(element kind classified once from `t.Elem()`, shared element-view dispatch,
`coerce` on writes); `DeleteEntry` uses `SetMapIndex(k, reflect.Value{})` —
the reflect deletion idiom. Top-level `Wrap(&m)` branches on the pointee kind
before `buildAdapter`'s struct guard, so a bare dictionary wraps without a
carrier struct. *Alternative — `Register`-only (no built-in lift):* leaves the
common `map[string]V` case as boilerplate and the S4 test's "recognized
future" unrealized — the slice's whole driver. Rejected.

**The write contract is entry-level, because Go maps are not addressable.**
`reflect.Value.MapIndex` returns a **non-addressable** value — there is no
pointer into a Go map, unlike a slice element (`Index(i)` is addressable,
which is exactly why `sliceCollection` hands out live, write-through
sub-views). So a deep write into a composite map value
(`SetPath(root, cfg.limits["k"].field, v)`) *cannot* mutate the live map — it
would write to a detached copy. Rather than lose that write silently (against
the §2.9.3 fail-loud posture), the adapter map's contract is: **whole-entry
writes** (`SetEntry` / `DeleteEntry`) are live for every element kind (the
idiomatic Go copy-modify-restore); a **scalar / leaf** or **passthrough**
entry is fully live (the scalar round-trips, the passthrough `data.Value` is
the stored mutable object); a **composite** entry (struct, `*struct`, slice,
nested map) is a **read-navigable frozen snapshot** (`freeze`,
`adapters/frozen.go`) whose mutators error loud. *Alternative — write-back
views (a composite view carrying a closure that re-`SetMapIndex`es the
modified copy):* propagates deep writes, but adds a mechanism neither
`structRecord` nor `sliceCollection` has, with partial-mutation and
concurrency edges; deferred as an additive follow-up (the seam does not
change). *Alternative — `map[string]*struct` deep-write-through (the
pointer's pointee IS addressable):* correct for that one shape, but a
"sometimes live, sometimes frozen" rule per element type is less predictable
than one uniform entry-level contract; also deferred. The uniform
frozen-composite rule is the fail-loud, predictable choice — a recorded
engine choice, like the bounded-reflection one (SAD-001 §6), noted at
landing (§8).

### 4.9 A `Map` is not a `Collection` — no cursor

`Collection` carries a stateful iteration cursor (`Rewind`/`GoTo`/`Next`/
`Index`). The map capability deliberately has none: sorted `Keys()` plus
`Entry` is complete, stateless enumeration; a cursor on a keyed container
would duplicate `GetKeys`+`GoTo` semantics that exist on `Collection` for
BPMN multi-instance mechanics maps do not serve. *Alternative — implement
both capabilities on `values.Map`:* violates the at-most-one rule (ADR-011
v.7 §2.9.1) and makes `kindOf` order-dependent. Rejected.

### 4.10 Existing consumers ride free — verified, not assumed

`ResolvePath`/`Source.Find` (conditions, expressions, mappings), scope
commit, and fact emission consume only steps, capabilities, and `[]Change` —
each gains map support through the seam arms with no consumer edits. §6's
T-6/T-7/T-9 exist to *demonstrate* that (scope-level diff, thresher-level
facts, condition routing on a map path), not to change those layers.

## 5. API / contract summary

| Surface | Change | Compatibility |
|---|---|---|
| `data.Map` (new) | fourth capability interface | additive |
| `data.Step` | gains `Key string` | additive; stays comparable; existing literals valid |
| `data.SplitPath`/`ParsePath` | accept `["key"]` steps | previously a parse error — strictly widens |
| `data.WalkSteps`, `SchemaAt`, `Walk`, `kindOf` | map arms | additive |
| `data.DiffValues` | `diffMaps` + kind-change probes | additive (map inputs were scalar-compared before) |
| `values.Map[T]`, `NewMap`, `MustMap`, `map_t.go` helpers | new | additive |
| `values.SetPath` | key-step descend/vivify/set | additive |
| `adapters` | `kindMap`, `mapValue`, `freeze` (frozen composite views), top-level map `Wrap` | behavior change **by design**: string-keyed map fields become navigable (was opaque leaf; SRD-045 §4.5's "recognized future" realized); write contract is entry-level (§4.8 — Go maps aren't addressable); non-string keys unchanged |
| observability | none | map paths are just paths |

## 6. Test scenarios

| # | Test (location) | Covers |
|---|---|---|
| T-1 | `TestMapValue` (`values/map_test.go`) | FR-2: ctor copy + nil + empty-key reject; `MustMap` panic; `Get` snapshot isolation; `Update` replace + wrong-shape/empty-key reject; sorted `Keys`; `Entry`/`SetEntry`/`DeleteEntry` incl. `ObjectNotFound` and `checkValue` conversion; `Clone` independence; `Type` |
| T-2 | `TestMapPathGrammar` (`data/path_test.go`) | FR-4: `["key"]` parse; `\"`/`\\` escapes; `]`/`[`/`.` inside quotes; errors — unclosed quote, unknown escape, empty key, missing `]`; `[0]` vs `["0"]`; `ParsePath` leading `["k"]`; `PathsOverlap` with key steps |
| T-3 | `TestMapWalkSteps` (`data/path_test.go`) | FR-5: read `m["k"]` and nested `m["k"].f[0]`; raw entry → `scalarLeaf`; `notNavigable` "a map" text; missing key → `ObjectNotFound` |
| T-4 | `TestMapSetPath` (`values/setpath_test.go`) | FR-6: upsert leaf; vivify `a.b["k"].c` chain (map created by the following key step); raw-entry `notWritable`; wrong-kind `notWritable` "a map" |
| T-5 | map cases in `TestDiffValues` (`data/diff_test.go`) | FR-7: per-key Added/Updated/Deleted; sorted, deterministic output order; nested map-in-record/record-in-map recursion; kind-change map↔record/list/scalar → single `ValueUpdated`; escaped keys in paths (added to the existing SRD-044 diff table, its idiom) |
| T-6 | scope commit with a map (`internal/scope/structural_test.go` or `diff_commit_test.go`) | FR-7: a map mutation through frame commit yields the per-entry change set |
| T-7 | map `DataChange` facts (`pkg/thresher/datachange_test.go`) | FR-7/§4.10: one `KindDataChange` fact per changed map path, correct phases |
| T-8 | `TestStructMapField` + registry/edge tests (`adapters/*_test.go`) | FR-8: string-keyed field navigable (read + `SetEntry`/`DeleteEntry` on the **live** map); named-string key type; `map[int]V` stays opaque leaf (re-pinned); a composite (struct-valued) map entry reads navigably but a deep write **errors loud** (frozen snapshot, §4.8); scalar/passthrough entries write live; top-level `Wrap(&m)`; `Clone`; the flipped SRD-045 sub-test |
| T-9 | condition on a map path (`pkg/thresher/structural_routing_test.go`) | §4.10: a sequence-flow/gateway condition reading `m["k"]` routes correctly |
| T-10 | examples smoke (step 13a) | the extended runnable examples exit 0 under timeout with expected output |

## 7. Milestones

Each milestone is one commit, `make ci` green:

- **M1 — the capability + the dynamic value + kind surfaces.** FR-1
  (`value.go`), FR-2 (`values/map.go`, `map_t.go`), FR-3 (`shape.go`); tests
  T-1 + shape assertions.
- **M2 — the read path.** FR-4 + FR-5 (`path.go`: `Step.Key`, quoted lexer,
  `keyLabel`, `WalkSteps` arm); tests T-2, T-3.
- **M3 — the write path.** FR-6 (`values/setpath.go`); test T-4.
- **M4 — commit-diff + runtime verification.** FR-7 (`diff.go`); tests T-5,
  T-6, T-7.
- **M5 — the adapter lift + end-to-end.** FR-8 (`adapters/`); tests T-8, T-9;
  extend the runnable examples (`examples/native-structs/` — a map field;
  `examples/data-change/` — a map path change) and smoke them (T-10).

## 8. Cross-doc

- Implements **ADR-011 v.7 §2.9.7** (the S5 slice, §2.9.6). The ADR was
  bumped v.6→v.7 in this same branch to un-defer the map kind; on landing,
  ADR-011 v.7 flips Draft→Accepted (its §2.9.6 "S5 rides the same pattern").
- Upstream: **ADR-010 v.2 §2.7** (structural steps stay within engine-managed
  values; the `SOURCE/` provider seam is untouched — `["key"]` is a
  structural character, not a provider address), **ADR-012 v.1** (all new
  public surface lives in `pkg/model/data`/`values`/`adapters`).
- Sideways: SRD-042/043/044/045 (each seam this SRD extends), SRD-048 (whose
  header note reserved this number — consumed, no edit: the note is a frozen
  historical statement).
- At landing (the sync-linked-docs step): SAD-001's ADR-011 catalog row
  (currently "Accepted v.6 … navigable `scalar｜list｜record`", the §6
  engine-choice table, and §14.2's extension list) is refreshed for the map
  kind as a recorded engine choice; README's data/feature blurbs and their
  translated twins gain the map kind.

## 9. Definition of Done

- [x] All FR-1…FR-8 implemented; every §6 test exists and passes (634 tests
      green across the 6 touched packages).
- [x] `make ci` green (tidy, lint, build, race tests, diff-coverage ≥95%,
      govulncheck); diff-coverage **98.9%** of 537 changed lines; every
      touched function ≥80% (most 100%) per `go tool cover -func`.
- [x] The SRD-045 opaque-leaf sub-test flipped (→ "string-keyed map field — a
      live navigable data.Map"); the opaque behavior re-pinned on a
      non-string-keyed `map[int]string`/`map[int]V` fixture.
- [x] A runnable example (`examples/maps/`) demonstrates both tiers and a map
      DataChange path; smoked exit 0 with expected output; its built binary
      (`mapexample`) is gitignored; the sibling structural examples still
      smoke exit 0.
- [x] Deterministic enumeration demonstrated (`TestMapValue` "Keys is sorted
      regardless of insertion order"; `TestMapShape` sorted `Walk`;
      `TestDiffValues` "sorted deterministic order").
- [x] ADR-011 v.7 flipped to Accepted; this SRD's §10 filled. SAD-001 +
      README sync is the sync-linked-docs commit that follows this landing.

## 10. Implementation summary

Landed on `feat/srd-047-map-data`, five milestone commits, each `make ci`
green. The kind threaded through every seam as one additive arm — no landed
S1–S4 surface changed shape.

| Milestone | Commit | Touched | Tests |
|---|---|---|---|
| Doc — ADR-011 v.7 bump | `97d2907` | `ADR-011-process-data-flow{,.ru}.md` | — |
| Doc — SRD-047 | `91db2af` | `SRD-047-structural-data-maps.md` | — |
| M1 — capability + `values.Map[T]` + kind surfaces | `166fc99` | `value.go`, `path.go` (`KeyLabel`), `shape.go`, `values/map.go`+`map_t.go` | `TestMapValue`, `TestMapShape` (T-1) |
| M2 — read path | `3145372` | `path.go` (`Step.Key`, `keyStep`, `WalkSteps` arm) | `TestMapPathGrammar`, `TestMapWalkSteps` (T-2/3) |
| M3 — write path | `ac74176` | `values/setpath.go` (key arm + `vivify`) | `TestMapSetPath` (T-4) |
| M4 — commit-diff | `5feb436` | `diff.go` (`diffMaps` + kind guard) | `TestDiffValues` map cases, `TestCommitReturnsMapDiff`, `TestMapDataChangeFactsEmitted` (T-5/6/7) |
| M5 — adapter lift + example | `6a64439` | `adapters/{builder,typeadapter,structrecord,wrap,mapvalue,frozen}.go`, `examples/maps/` | `TestStructMapField`, `TestMapValueWholeValue`, `TestFrozenCompositeEntries`, `TestMapCoverageEdges`, `TestMapCustomValue`, `TestWrapBareMap` (T-8) |
| Audit — T-9 close | *(this landing commit)* | `pkg/thresher/structural_routing_test.go` | `TestExclusiveRoutingOnMapPath` (T-9) |

**Verification (V-results).**
- **V-1 build/lint:** `make ci` green across all modules.
- **V-2 tests:** 634 pass across `pkg/model/data`, `…/values`, `…/adapters`,
  `internal/scope`, `pkg/thresher` (+`goexpr`).
- **V-3 diff-coverage:** 98.9% of 537 changed lines (min 95%) — PASS; per-file
  100% except `mapvalue.go` 98.7% (an `adapterFor`-error defensive arm) and
  `shape.go` 85.2% (the `Entry`-error arms mirroring the landed collection
  arms); `frozenCollection.Clear` is an executed empty-body no-op (a frozen
  `Clear` cannot signal via its errorless signature).
- **V-4 smoke:** `examples/maps/` runs exit 0, printing sorted dynamic-map
  keys, a `["EUR"]` read, a live native-map `SetEntry`, and per-entry
  DataChange facts (`rates["EUR"]` updated, `rates["JPY"]` added,
  `rates["GBP"]` deleted); `native-structs`, `data-change`, `structural-data`
  still exit 0.

**Deviations from the plan.**
- **Example (§7 M5).** The plan proposed extending `examples/native-structs/`
  and `examples/data-change/`; instead a **dedicated `examples/maps/`** was
  created (the "apply forward, don't rewrite existing examples" house rule),
  with `main.go`/`demo.go`/`process.go`/`observer.go` (entry split by concern
  per the ≤80-line rule). Its module is `mapexample` (not `maps` — that name
  collides with the stdlib `maps` package the code imports).
- **Write contract (§4.8), decided mid-M5.** Go map values are not addressable
  (`reflect.MapIndex` is never addressable), so the adapter map is
  **entry-level**: `SetEntry`/`DeleteEntry` are live; a composite entry is a
  read-navigable frozen snapshot whose deep write errors loud. Recorded as an
  engine choice in ADR-011 v.7 §2.9.7 and this doc §4.8 (both amended while
  Draft).
- **T-5 location.** Map diff cases were added to the existing `TestDiffValues`
  table (the SRD-044 idiom), not a standalone `TestDiffMaps` — §6 amended.

## Open questions

- None. The capability shape (§4.1), the non-empty-key rule (§4.2), the
  lexer's escape set (§4.3), sort-on-call enumeration (§4.4), the strict
  replace contract (§4.5), the `["*"]` slot (§4.6), diff ordering (§4.7), the
  adapter classification and top-level wrap (§4.8), and the no-cursor rule
  (§4.9) are decided above; ADR-level questions were closed by ADR-011 v.7.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-19 | Ruslan Gabitov | Initial draft — the S5 maps slice per ADR-011 v.7 §2.9.7: `data.Map` capability, `values.Map[T]`, the `["key"]` grammar, read/write/vivify walks, `diffMaps`, and the adapter `map[string]V` lift. |
