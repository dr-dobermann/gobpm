# SRD-045 — Structural data S4: native-struct adapters

| Field | Value |
|---|---|
| Status | Draft v.1 |
| Version | v.1 |
| Date | 2026-07-14 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-011 v.6](../design/ADR-011-process-data-flow.md) §2.9.5 (Go interop: native objects behind a per-type adapter, resolved once) — the S4 slice (§2.9.6): the structural-adapter contract, the type→adapter registry, the registration-time reflection builder, and the `gobpm:"..."` tag vocabulary. The codegen generator is an **additive follow-up** on the same seam (explicitly out of this SRD). ADR-011 v.6 is already Accepted; S4 is a slice and does **not** bump the ADR. |
| Upstream | [ADR-010 v.2](../design/ADR-010-process-data-model.md) §2.7 (the data plane the wrapped values live in), [ADR-012 v.1](../design/ADR-012-execution-layering.md) (public contracts in `pkg/*`) |
| Refines | SRD-042 (S1 — the `Record` capability + the §3.2 forward-pointer this closes), SRD-043 (S2 write path), SRD-044 (S3 commit-diff) — by number, sideways |

S1–S3 made values navigable, writable, and change-detected — for the engine's
**dynamic** tier (`values.Record` / `values.Array`). S4 lets a host's **own Go
struct participate directly**: `adapters.Wrap(&order)` returns a `data.Value`
satisfying the `Record` capability over the **live** value — wrap, not convert
— so paths, conditions, mappings, `SetPath`, and `DiffValues` navigate it with
**zero engine change** (every seam consumes values via `Record`/`Collection`
type-assertions; grounding: `path.go:197,212`, `setpath.go:69,85`,
`diff.go:64,73`).

How a type answers the capabilities is a per-type **structural adapter**,
resolved **once** through a type→adapter registry (the `encoding/json`
type-cache pattern — lazy build on the first `Wrap` of a type, cached in a
`sync.Map`) — a deliberate, *bounded* relaxation of the anti-reflection stance:
reflection walks a type **once, off the execution path**; field access
thereafter is a cached-index accessor. The engine choice is registered in
SAD-001's engine-choices scope at landing (§8).

## 1. Background

- **The tier is designed but absent.** ADR-011 v.6 §2.9.5 prescribes the
  three-tier interop (dynamic / reflection adapter / codegen adapter); today a
  native struct is boxed **opaque** by `values.NewVariable(anyStruct)` — no
  navigation into it. No adapter/registry/wrap machinery exists anywhere
  (grounding grep: zero hits).
- **The forward-pointer is explicit.** SRD-042 §3.2: "a `Record` constructed
  *from a native Go struct* (a live view via the type→adapter registry — wrap,
  not convert) is the S4 tier"; and §3.2 already decided the whole-value
  contract: "An S4 adapter-backed record will return its **native Go object**
  from `Get` — the same contract, per tier."
- **Hiding is layered.** SRD-042 §4.7: per-field hiding is the S4 tag
  (`gobpm:"-"` — absent from `Keys` AND `Field`); per-observer visibility is
  the separate ADR-013 v.2 seam, not S4's concern.
- **Reflection today is bounded to diagnostics**: type-name strings in
  errors/`Type()` and one `reflect.DeepEqual` per commit-diff leaf. S4 adds the
  one blessed registration-time walk, isolated in its own package.

## 2. Requirements

### Functional

- **FR-1 — the entry point.** A new package `pkg/model/data/adapters` with
  `Wrap(ptr any) (data.Value, error)` (+ `MustWrap`): wraps a live **pointer to
  struct** as a navigable value satisfying `data.Value` + `data.Record`. A nil,
  non-pointer, pointer-to-non-struct, or nil-pointer argument is a classified,
  self-identifying error (public-API validation).
- **FR-2 — the type→adapter registry.** Per Go type, the adapter (the ordered
  field table with cached indices, names, kinds) is built **once** on the first
  `Wrap` of that type and cached in a `sync.Map` — concurrent-safe, lazy, the
  `encoding/json` pattern. No per-access type walking; recursion-safe for
  self-referential types (field views resolve their own type's adapter at
  access, through the same cache).
- **FR-2a — the custom-adapter hook (the Marshaler-analogue, §4.10).**
  `Register[T](build func(v *T) data.Value)` installs a user-supplied adapter
  factory for `T`, **pre-empting the reflection builder** — at top-level
  `Wrap(*T)` and wherever a field of type `T` is classified. This is the seam
  ADR-011 v.6 §2.9.5 prescribes for the codegen generator ("its generated
  registration pre-empts the reflection builder") and the hand-written escape
  hatch — it lifts otherwise-opaque types (a third-party struct, `time.Time`,
  a map type) into navigability without touching them. A nil `build` is a
  classified error; registration is init-time by convention — a later
  `Register` replaces the cache entry for *future* wraps (documented).
- **FR-3 — the reflection builder + tag vocabulary.** The builder walks the
  struct type once: exported fields become record fields; `gobpm:"name"`
  renames (reconciling Go naming with process naming, `ID` → `id`);
  `gobpm:"-"` **excludes** the field entirely (absent from `Keys`, `Field`,
  `SetField`); unexported fields are always excluded. Field kinds: a nested
  struct (or pointer-to-struct) → a sub-record view; a slice → a collection
  view; a field whose type **implements `data.Value`** → **passthrough** — the
  held value participates *as itself*, navigable per its own capabilities (a
  `values.Record` field inside a wrapped struct stays fully navigable — the
  ADR's "freely mixed and nested" tiers, §4.7); anything else (scalars, maps,
  non-Value interfaces, …) → an **opaque scalar leaf** — it participates
  whole-value and navigation *into* it errs naturally (the §10.4.1 opacity
  posture; the map kind is the recognized §2.8 future). Tag vocabulary
  minimality: §4.8.
- **FR-4 — the wrapped record.** The struct view implements the full contract:
  - `Keys()` — declaration order, tag-renamed, exclusions absent;
  - `Field(name)` — a **live view** of the field (sub-record / collection /
    writable leaf); unknown names → classified error;
  - `SetField(name, v)` — type-checked write-through to the live struct: `v`'s
    value must be assignable to the field's Go type; unknown names and type
    clashes are classified errors (the typed-target rejection SRD-042 §3.3
    anticipated);
  - `Get(ctx)` — the **native Go object** (the dereferenced struct value — a
    copy), per the SRD-042 §3.2 decision;
  - `Update(ctx, v)` — accepts the **same struct type** (whole replace through
    the pointer) **or** a `map[string]any` (per-field merge via the setters,
    mirroring `values.Record.Update`) — so cross-tier whole-value assignment
    works in both directions; anything else → classified error;
  - `Clone()` — **value-copy + fresh slices**: a new struct instance copied by
    value; slice fields get a fresh backing array (elements value-copied);
    nested structs copy by value; pointer/map/opaque internals copy **shallow**
    (documented). Independent enough for frame-clone-then-replace;
  - `Lock`/`Unlock` — a real mutex owned by the **root** wrapper (every view
    into the same wrapped struct shares it); `Type()` — the cached Go type
    name (no per-access reflection).
- **FR-5 — the wrapped collection.** A slice field surfaces as a full
  `data.Collection` view over the **live** slice: cursor machinery
  (`Rewind`/`GoTo`/`Next`/`Index`) owned by the view; reads/writes
  (`Count`/`GetAll`/`GetKeys`/`GetAt`/`SetAt` — the S2 bounds: `[0,len)`
  replaces, `==len` appends, past-len errors —
  `Add`/`Insert`/`Delete`/`Clear`) go through the pointer with assignability
  checks; struct elements come back as live sub-record views, scalar elements
  raw (the `Array[T]`/`scalarLeaf` precedent).
- **FR-6 — seam proof, zero engine change.** `data.WalkSteps`,
  `values.SetPath`, and `data.DiffValues` navigate wrapped values **unmodified**
  — proven by tests that route a gateway condition on a wrapped struct's path,
  set into it, and diff two commits of it (DataChange facts fire), plus a
  thresher-level e2e. No file outside `pkg/model/data/adapters` (and
  docs/example) changes.
- **FR-7 — worked example + docs.** A runnable example wraps a host order
  struct, the process routes on `order.total` and writes into it; README/index
  wiring (the front-door sync rule).

### Non-functional

- **NFR-1 — bounded reflection, isolated.** The type walk runs once per type,
  at first `Wrap`, off the execution path; per-access work is a cached-index
  `reflect.Value.Field` accessor call. All of it lives in
  `pkg/model/data/adapters` — auditing the engine choice is "reflection lives
  only here". The `data` core and `values` stay as they are.
- **NFR-2 — concurrency invariant, documented.** After `Wrap`, access goes
  through the adapter (guarded by the root mutex). The host mutating the
  struct **directly, concurrently with process evaluation** is the host's
  synchronization responsibility — the same live-value posture `GetP()`
  already takes in `values`. The doc comments state it.
- **NFR-3 — codegen is a follow-up, not a dependency.** The generated adapter
  (compile-checked, reflection-free) pre-empts the reflection builder for its
  type **on the same registry seam**; nothing in S4 blocks or requires it.

## 3. Models

### 3.1 The package surface (`pkg/model/data/adapters`)

```go
// Wrap wraps a live pointer-to-struct as a navigable data.Value satisfying
// data.Record (ADR-011 v.6 §2.9.5 — wrap, not convert). The type's adapter is
// built on first use and cached.
func Wrap(ptr any) (data.Value, error)

// MustWrap is the panic-on-error twin (the values.MustRecord idiom).
func MustWrap(ptr any) data.Value

// Register installs a custom adapter factory for T, pre-empting the
// reflection builder at Wrap and at field classification (the
// Marshaler-analogue extension seam, §4.10; the codegen generator's
// registration rides it too).
func Register[T any](build func(v *T) data.Value) error
```

Internal shapes (one entity per file):

```go
// typeAdapter is the cached per-type build product: the ordered field table.
type typeAdapter struct {
    fields []fieldInfo          // declaration order, exclusions absent
    byName map[string]int       // process name → fields index
    goType reflect.Type
    name   string               // cached Type() string
}

// fieldInfo is one navigable field: its process name (tag-renamed), the cached
// struct index, and its resolved kind (record | collection | scalar leaf).
type fieldInfo struct {
    name  string
    index int
    kind  fieldKind
}

// structRecord is the live Record view: the root's pointer + adapter + the
// shared root mutex.
type structRecord struct { ... }

// sliceCollection is the live Collection view over a slice field (owns its
// iteration cursor; shares the root mutex).
type sliceCollection struct { ... }

// fieldLeaf is the writable scalar-leaf view of a non-navigable field.
type fieldLeaf struct { ... }
```

The registry: `var adapterCache sync.Map // reflect.Type → *typeAdapter`,
`adapterFor(t reflect.Type) (*typeAdapter, error)`.

Errors: the package defines its own class constant (the `values` idiom,
`values.go:4`) — `const errorClass = "ADAPTERS_ERROR"` — and every public-API
rejection self-identifies function + parameter.

### 3.2 The tag vocabulary

| Tag | Effect |
|---|---|
| *(none)* | exported field participates under its Go name |
| `gobpm:"id"` | participates under the process name `id` |
| `gobpm:"-"` | excluded — absent from `Keys`, `Field`, `SetField` |
| *(unexported)* | always excluded |

### 3.3 Worked example

```go
type Order struct {
    ID    string `gobpm:"id"`
    Total int    `gobpm:"total"`
    Items []Item `gobpm:"items"`
    audit string // unexported — never visible
}
type Item struct {
    SKU   string `gobpm:"sku"`
    Price int    `gobpm:"price"`
}

order := &Order{ID: "A-1", Total: 150,
    Items: []Item{{SKU: "widget", Price: 50}}}

v := adapters.MustWrap(order)                  // live Record view
// reads — the S1 seam, unchanged:
//   data.WalkSteps(ctx, v, steps("items[0].price")) → 50
// writes — the S2 seam, unchanged:
//   values.SetPath(ctx, v, "total", values.NewVariable(175))
//   → order.Total == 175 (the LIVE struct changed)
// diff — the S3 seam, unchanged:
//   data.DiffValues("order", v.Clone(), v) → [{order.total Value_Updated}]
// whole-value:
//   v.Get(ctx).(Order).Total == 175           // the native Go object
```

## 4. Analysis & decisions

### 4.1 A new package, not `values`

The adapter tier gets `pkg/model/data/adapters` (owner-confirmed): it isolates
the one place bounded reflection is permitted — the audit of the engine choice
reduces to "reflection lives only in `adapters`" — and mirrors the ADR's tier
split (`values` = dynamic, no adapter needed). Import direction `adapters →
data` only; no cycle, `values` untouched. *Alternative:* inside `values`
(fewer packages) — rejected: the relaxation would bleed into the dynamic
tier's package and `values` would carry two concerns.

### 4.2 Lazy registration — the cache IS the registry

"Registration time" is realized as the `encoding/json` pattern the ADR names:
the first `Wrap` of a type builds and caches its adapter; every later `Wrap`
and every field access hits the cache. Model-building happens before execution,
so the walk is off the execution path by construction. There is no *pre-warm*
API (running the reflection builder early would be a second name for the same
effect); the public `Register[T]` (FR-2a) is a different thing — it
**replaces** the builder for a type with a user-supplied adapter (§4.10).

### 4.3 Update accepts the struct OR a map (owner-confirmed)

`Get` returns the native object (decided in SRD-042 §3.2), so the symmetric
`Update` accepts the same struct type (whole replace). But cross-tier
assignment must work both ways — a dynamic `values.Record` source yields
`map[string]any` from `Get`; the adapter's `Update` therefore also merges a
map via its typed setters (mirroring `values.Record.Update`, unknown names and
type clashes rejected). *Alternatives:* struct-only (breaks dynamic→adapter
assignment), map-only (asymmetric with the decided `Get`) — both rejected.

Bidirectionally:

| Source → target | Path | Works |
|---|---|---|
| adapter → adapter | `Get()` struct → `Update(struct)` replace | ✓ |
| dynamic → adapter | `Get()` map → `Update(map)` merge | ✓ |
| adapter → dynamic | `Get()` struct → `values.Record.Update` rejects a struct (its map-only contract, unchanged in S4) | ✗ by design — convert per-field or via a map |

S4 changes nothing in `values`; the adapter side accepts both shapes so the
common assignments land.

### 4.4 Clone is value-copy + fresh slices

A general recursive deep copy is a known Go tar-pit (pointer cycles,
unexported fields, channels); a shallow share breaks frame-clone isolation.
The middle is deterministic and sufficient for the data plane: copy the struct
by value, re-allocate slice backing arrays (elements value-copied), copy
nested structs by value; pointers/maps/opaque internals stay shared and the
doc comment says so. A type needing stronger isolation can hand-write or (the
follow-up) generate its adapter.

### 4.5 Unsupported field kinds stay opaque, not errors

A field the builder can't navigate (map, interface, func, chan) participates
**whole-value** as a scalar leaf — the §10.4.1 opacity posture S1 set for
under-specified items — instead of failing the whole type's build. Navigation
*into* such a field errs naturally at the seam ("not a record"). This keeps
adoption frictionless (one exotic field doesn't lock a struct out) and keeps
the map kind cleanly deferred (§2.8). Opacity is the **default**, not a dead
end: the owner of such a type lifts it into navigability by implementing the
capabilities on it (§4.7 passthrough) or by registering a custom adapter for
it (`Register[T]`, §4.10) — the field-kind resolution order (§4.10) consults
both before falling back to the leaf.

### 4.6 One root mutex

Every view into one wrapped struct (the record, its sub-records, collections,
leaves) shares the root wrapper's mutex — coherent `Lock`/`Unlock` semantics
across the value graph, no per-view lock ordering. The live-host-mutation
caveat is NFR-2's documented invariant.

### 4.7 Tier mixing — the passthrough field kind

ADR-011 v.6 §2.9.5 prescribes tiers "freely **mixed and nested**". Both
directions hold: a wrapped struct is a `data.Value`, so it nests inside a
`values.Record` field trivially (dynamic-containing-wrapped); and a struct
field whose type implements `data.Value` is classified **passthrough** at
build — `Field` returns the held value as itself, navigable per its own
capabilities (wrapped-containing-dynamic). A nil passthrough field errs
naturally on navigation. Without the passthrough kind, a `values.Record` field
would degrade to an opaque interface leaf — a silent gap against the ADR's
mixing prescription (caught by /review-srd).

### 4.8 Tag vocabulary — minimal, grows by need

The ADR §2.9.5 says tags "reconcile Go naming with process naming, exclude
fields, and carry per-field options" — a capability list, not a mandated
option set. S4 ships the needed minimum: rename + `gobpm:"-"`. Further
per-field directives (copy strategy, visibility overrides, …) are recognized
futures on the same parse seam, added when a need names them — the
no-speculative-surface rule.

### 4.9 Top-level slices are not wrappable — deferred

`Wrap` accepts pointer-to-struct only. A top-level slice could satisfy
`Collection` directly (the ADR's capability phrasing permits it), but process
data is addressed by a **named head** (§2.9.2) and enters the plane as named
properties/parameters — a nameless root collection has no addressing story
today, and the dynamic `values.NewArray` covers standalone lists. Slices
participate as struct fields (FR-5); a direct slice wrap is a by-need
follow-up on the same registry seam.

### 4.10 Extensibility — the Marshaler/Unmarshaler pattern (owner-requested)

New-type processing plugs in the way `encoding/json` extends — two opt-in
routes, consulted in the **field-kind resolution order**:

1. a **registered custom adapter** for the exact type (`Register[T]`, FR-2a) —
   the route for types the user *cannot* modify (third-party structs,
   `time.Time`, map types): the factory receives the live `*T` and returns any
   `data.Value` (typically a hand-built `Record`/`Collection` view);
2. the type **implements `data.Value` itself** → passthrough (§4.7) — the
   `json.Marshaler` analogue for types the user owns;
3. a struct → the reflection-built sub-record; a slice → the collection view;
4. anything else → the opaque scalar leaf (§4.5).

The same order answers top-level `Wrap`. This is one seam, three suppliers —
reflection (standard), hand-written (escape hatch), codegen (follow-up) —
exactly the ADR's pre-emption model; the engine and the seams never know which
supplier answered. Registration is init-time by convention (the codegen
generator emits an `init()` that calls `Register`); replacing a cached entry
affects future wraps only — existing wrapped values keep their views.

## 5. API / contract

- **New public**: `pkg/model/data/adapters` — `Wrap`, `MustWrap`,
  `Register[T]` (+ the `gobpm:"..."` tag contract). Everything else
  unexported.
- **Changed**: nothing. `pkg/model/data`, `values`, the seams, the engine —
  untouched (FR-6/NFR-1).

## 6. Test scenarios

| # | Test | Covers |
|---|---|---|
| T-1 | `TestWrapValidation` | nil / non-pointer / pointer-to-non-struct / nil pointer → classified self-identifying errors; `MustWrap` panics on the same |
| T-2 | `TestAdapterRegistry` | the same type wraps twice → one build (cached adapter identity); concurrent `Wrap` of one type is race-free; self-referential type terminates |
| T-2a | `TestRegisterCustomAdapter` | `Register[T]` pre-empts the reflection builder at `Wrap` AND at field classification (an otherwise-opaque type — e.g. a map-holding third-party type — becomes navigable through the registered view); nil build → classified error; re-`Register` affects future wraps only |
| T-3 | `TestStructRecordRead` | `Keys` order + tag renames + `gobpm:"-"`/unexported absent; `Field` live views per kind (sub-record, collection, **passthrough** — a `values.Record` field navigable as itself, §4.7 — and leaf); unknown name errs; `Get` returns the native object; `Type` cached |
| T-4 | `TestStructRecordWrite` | `SetField` write-through (the live struct changes); unknown name + type clash rejected; `Update` with the same struct type (replace) and with `map[string]any` (merge — incl. a map that came from a `values.Record.Get`, the §4.3 cross-tier row); wrong shape errs |
| T-5 | `TestSliceCollectionView` | full Collection over a live slice: cursor ops, `GetAt`/`SetAt` (S2 bounds), `Add`/`Insert`/`Delete`/`Clear` mutate the live slice; struct elements return live sub-records |
| T-6 | `TestClone` | value-copy independence: mutating the clone's fields (incl. slice elements) leaves the original intact; **pointer fields remain shared** (mutating the pointee is visible on both — the documented shallow rule) |
| T-7 | `TestSeamsOverWrappedValues` | `WalkSteps` reads `items[0].price`; `SetPath` writes `total`; `DiffValues` over two states yields the leaf changes — all with zero seam edits |
| T-8 | `TestWrappedStructE2E` (thresher) | a wrapped struct as process data: a gateway condition routes on `order.total`, a task's commit of it emits DataChange facts |
| T-9 | example smoke | the worked example exits 0 with the pinned read-back |

## 7. Milestones

| # | Scope |
|---|---|
| **M1** | FR-1..FR-3 (incl. FR-2a `Register[T]`) + the record read path: `Wrap`/`MustWrap`, the registry + custom-adapter hook, the builder + tags, `structRecord` reads (`Keys`/`Field`/`Get`/`Type`). T-1, T-2, T-2a, T-3. |
| **M2** | FR-4/FR-5 writes + views: `SetField`, `Update`, `Clone`, `sliceCollection`, `fieldLeaf`. T-4, T-5, T-6. |
| **M3** | FR-6 seam proof: T-7 + T-8 (e2e). |
| **M4** | FR-7: the worked example + README/index wiring (T-9); §10; `/check-srd`; SRD-045 → Accepted; sync linked docs (SAD-001 §14 engine-choice row — the bounded-reflection relaxation — + the ADR-011 registry row S4 landed + roadmap). ADR-011 v.6 stays Accepted. |

## 8. Cross-doc

| Ref | Version | Direction | Role |
|---|---|---|---|
| ADR-011 | v.6 (Accepted) | SRD → ADR (up) | implements §2.9.5 (S4); **no bump** |
| ADR-010 | v.2 | up | the data plane the wrapped values live in |
| ADR-012 | v.1 | up | public contracts in `pkg/*` |
| SRD-042 / SRD-043 / SRD-044 | (one-shot, by number) | sideways | the S1/S2/S3 slices this completes |

**At landing (M4 sync):** SAD-001 (Draft) gains the engine-choice registration
ADR-011 v.6 §2.9.5 mandates — "reflection may run once per type, at
registration, off the execution path, and nowhere else"; the codegen generator
is recorded as the additive follow-up.

## 9. Definition of Done

- [ ] FR-1..FR-7 wired; every §6 test exists and is green.
- [ ] Zero changes outside `pkg/model/data/adapters` + example/docs (FR-6).
- [ ] Reflection isolated to the new package; per-access = cached-index
      accessor only (NFR-1).
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); full `-race`; example
      smoke exits 0.
- [ ] SRD-045 flipped to Accepted; SAD-001 §14 engine-choice row + registry
      row + roadmap synced. ADR-011 v.6 unchanged.
- [ ] §10 filled with milestone SHAs and deltas.

## 10. Implementation summary

> ⚠️ TODO: filled after landing.

## Open questions

None. The package placement (`adapters`), the `Update` contract (struct OR
map), the `Clone` semantics (value-copy + fresh slices), and the
`Register[T]` extension seam (§4.10, owner-requested) are decided above with
the owner; `Get`-returns-native-object and `gobpm:"-"`-is-exclusion were
decided in SRD-042 (§3.2, §4.7). The codegen generator (it rides `Register`),
richer tag options, and the map value kind are named follow-ups, not open
questions for S4.
