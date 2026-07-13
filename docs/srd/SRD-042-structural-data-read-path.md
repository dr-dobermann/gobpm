# SRD-042 — Structural data S1: the Record capability and the read path

| Field | Value |
|---|---|
| Status | Draft v.1 |
| Version | v.1 |
| Date | 2026-07-13 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-011 v.6](../design/ADR-011-process-data-flow.md) §2.9 — the S1 slice (§2.9.1 the value model, §2.9.2 read-path addressing, the §2.9.4 `Updater` deletion, §2.9.6 phasing). ADR-011 v.6 is Draft and flips to Accepted when this SRD lands (§2.9.6: "Accepted once S1 proves the model"). |
| Upstream | [ADR-010 v.2](../design/ADR-010-process-data-model.md) §2.7 (the `SOURCE/addr` provider seam — untouched, reconciled), [ADR-012 v.1](../design/ADR-012-execution-layering.md) (public contracts live in `pkg/*`) |

This SRD lands the first slice of ADR-011 v.6 structural data: the **`Record`**
capability beside the existing `Collection`, the dynamic **`values.Record`**,
the **path resolver** in the data-access seam (`order.items[0].price`, read
side), the **shape-by-traversal** helpers, the **name reservation** for the
structural characters, and the **deletion** of the dormant
`Updater`/`UpdateCallback` machinery. Writes (S2), commit-diff/DataChange (S3),
and native-struct adapters (S4) are explicitly out of scope.

---

## 1. Background & current state (verified against the code)

- **The `Value` family** (`pkg/model/data/value.go`): `Value` is 6 methods —
  `Get(ctx) any`, `Update(ctx, any) error`, `Lock/Unlock`, `Type() string`,
  `Clone() Value` (value.go:9-32). `Collection` (value.go:45-87) is the optional
  list capability — cursor ops plus `GetAt(ctx, index any) (any, error)`,
  `Add/Insert/Delete`. **`GetAt` returns `any`, not `Value`** — a walker must
  handle raw elements (§4.3). Kind-by-assertion is already house practice:
  `NewItemDefinition` detects collection-ness by `value.(Collection)`
  (item.go:85-89).
- **No record kind exists.** `pkg/model/data/values/` holds exactly
  `Variable[T]` (scalar; variable.go:14-19) and `Array[T]` (list;
  array.go:15-20).
- **The read seam**: `data.Source{ Find(ctx, name) (Data, error) }`
  (data.go:28-32). Four implementations: the runtime `execEnv.Find` →
  `frame.GetData` (internal/instance/execenv.go:57-59), the association
  transformation source (association.go:290-303), the correlation payload
  source (msgflow/correlation.go:33-43), and the output-mapping fault source
  (pkg/tasks/errormapper.go:139-155). `Frame.GetData`
  (internal/scope/frame.go:174-198) cuts on the **first `/`** → provider
  (verbatim, opaque — ADR-010 §2.7), else frame-first + container walk-up.
- **Conditions and expressions already flow through this seam.** A sequence
  flow's condition (`SequenceFlow.Condition()`, sequenceflow.go:309-310) is
  evaluated by the shared gateway evaluator
  `re.ExpressionEngine().Evaluate(ctx, cond, re)` (gateways/gateway.go:221-248)
  — **the Source is the runtime environment itself** (`execEnv` →
  `frame.GetData`). `goexpr.GExpFunc` receives the `data.Source` directly
  (goexpr.go:22). Output-mapping rules evaluate against the fault source
  (`outputmapping.go:28-58`). So a path walk placed at the seam reaches
  gateways, expressions, and mapping reads with **no per-consumer change**.
- **The dormant subscription machinery**: `Updater` (value.go:89-99),
  `UpdateCallback` (value.go:101-107), `evtUpdaters` + `Register/Unregister` +
  async fan-out in both value types (variable.go:17,25,80-142;
  array.go:17,29,394-457 with 5 `notify` call sites at
  array.go:85,224,248,311,341). **Zero non-test consumers, zero mocks** (repo
  grep empty; `.mockery.yaml` has no entry). Only consumer:
  `values/values_test.go` (three subtests/funcs, §3.6). `ChangeType` + its 3
  consts (value.go:109-119) live in their **own decl block** — the §2.9.4
  carve-out is surgically clean.
- **Name reservation precedent**: `CheckName` (name.go:12-26) reserves `/`;
  called by exactly 3 constructors — `NewParameter` (io_spec_obj.go:95),
  `NewProperty` (property.go:36), `NewDataObject` (data_object.go:52). A repo
  sweep of all name-taking constructor call sites found **zero names**
  containing `.`/`[`/`]` — no back-compat break. One test currently asserts
  the opposite and must flip: `name_test.go:18` blesses `order.items.price`
  as a valid name.

## 2. Requirements

### Functional

- **FR-1 — the `Record` capability.** `pkg/model/data` gains the public
  `Record` interface (§3.1): `Value` + ordered `Keys()`, `Field(ctx, name)`,
  `SetField(ctx, name, Value)`. Mirrors `Collection`'s optional-capability
  pattern; discovered by type assertion.
- **FR-2 — the dynamic record.** `pkg/model/data/values` gains **`Record`**
  (§3.2): insertion-ordered, mutex-guarded, deep-`Clone`, permissive
  (`SetField` accepts new fields — the dynamic tier of ADR-011 §2.9.5).
  Constructs through the existing `NewItemDefinition`/`NewItemAwareElement`
  unchanged.
- **FR-3 — the path resolver (read).** A structural path is
  `name( .field | [index] )*` (§3.3). `pkg/model/data` gains the tokenizer and
  the walker: split a path into head + steps; fold steps over capabilities —
  `.field` requires `Record`, `[i]` requires `Collection`; every violation is a
  classified `errs` error naming the failing step and the value's actual kind,
  never a panic (§4.3).
- **FR-4 — the seam integration.** `Frame.GetData` resolves structural paths:
  the **`/` provider cut stays first** (ADR-010 §2.7, opaque tail untouched);
  otherwise the head resolves exactly as today (frame-first, container
  walk-up) and the steps walk the found value. A plain name (no structural
  chars) takes today's code path unchanged (NFR-2). The **input-mapping**
  source adopts the same resolver: `Association.Find`
  (association.go:290-303) applies `SplitPath`/`WalkSteps` — the head is the
  declared source ID, the steps walk the found value — so a
  `DataAssociation` transformation reads structural paths (ADR-011 v.6
  §2.9.2: "every consumer … input mapping"). The **output-mapping** fault
  source does the same, so a rule reads into a worker body (`body` →
  structural steps). Gateway conditions and `goexpr` expressions need **no
  change** — they already resolve through the seam (§1). Proven by
  T-4/T-5/T-6/T-11. The correlation payload source
  (msgflow/correlation.go:33-43) is **not** a mapping read (exact-message
  key extraction) and stays untouched.
- **FR-5 — shape by traversal.** Free helpers in `pkg/model/data` (§3.4):
  `SchemaAt(ctx, v, path)` — the fields/kind at one level — and
  `Walk(ctx, v, visit)` — full descent. No stored schema artifact
  (ADR-011 §2.9.1).
- **FR-6 — name reservation.** `CheckName` additionally rejects `.`, `[`, `]`
  in data-element names (the structural characters; extends the SRD-010 `/`
  precedent in the same function). `name_test.go:18` flips to `wantErr: true`.
- **FR-7 — the `Updater` deletion.** `Updater`, `UpdateCallback`, both
  `evtUpdaters` fields, `Register`/`Unregister`, `notify` + both async senders
  and all 6 `notify` call sites are deleted (§3.6 inventory).
  **`ChangeType` + `ValueAdded/Updated/Deleted` stay** (retargeted as the S3
  commit-diff vocabulary; wire names already mirrored by the observability
  phases).
- **FR-8 — the worked example.** `examples/structural-data/`: a
  `values.Record` order variable; an exclusive gateway routes on
  `order.total > 100`; a service task reads `order.items[0].price` (§3.7).
  Runs under a timeout in the landing smoke; proves the ADR's S1 gate.

### Non-functional

- **NFR-1 — no new reflection.** S1 adds zero `reflect` usage (adapters are
  S4; the pre-existing type-name-string uses in `values` are out of scope);
  the walker is capability dispatch (type assertions + method calls).
- **NFR-2 — plain-name cost unchanged.** A name without `.`/`[` takes exactly
  today's lookup path; the structural split is gated on a cheap
  `strings.IndexAny` scan. No new allocation on the plain path.
- **NFR-3 — compatibility.** `SOURCE/addr` provider dispatch is byte-identical
  (the `/` cut runs first; provider tails like `BUSINESS/order.items[0].price`
  still ride verbatim). Existing `Find` consumers see identical results for
  plain names (T-9). An **under-specified** item (no declared structure —
  ADR-011 v.6 §2.9.1 / BPMN §10.4.1) stays a dynamic, opaque scalar exactly as
  today: it implements neither capability, so it is a path leaf.
- **NFR-4 — coverage.** Diff-coverage ≥95% (aim 100%) per `make ci`
  (Makefile:187 chain incl. `cover-check`); full `-race` suite.

## 3. Models (code-grounded)

### 3.1 The `Record` capability (`pkg/model/data/value.go`)

Mirrors `Collection`'s shape (embedded `Value`, ctx on data-touching methods,
classified errors — the `GetAt (any, error)` precedent):

```go
// Record is the optional structural capability of a Value (ADR-011 v.6
// §2.9.1): a string-keyed, heterogeneous, insertion-ordered set of fields. A
// Value implementing Record is navigable by `.field` path steps; kind is
// discovered by type assertion, as with Collection.
type Record interface {
	Value

	// Keys lists the field names in insertion order.
	Keys() []string

	// Field returns the named field's value. Unknown name → an
	// errs.ObjectNotFound-classified error.
	Field(ctx context.Context, name string) (Value, error)

	// SetField sets the named field. The implementation enforces its own
	// shape: a typed adapter (S4) rejects unknown names/type clashes; the
	// dynamic values.Record accepts new fields.
	SetField(ctx context.Context, name string, v Value) error
}
```

Field names obey `CheckName` (no `/`, `.`, `[`, `]`) — enforced by
`values.Record.SetField`, so an unaddressable field cannot be created.

### 3.2 The dynamic record (`pkg/model/data/values/record.go`)

```go
// Record is the dynamic, engine-assembled record value — the zero-setup tier
// of ADR-011 v.6 §2.9.5. Insertion-ordered, permissive on SetField.
type Record struct {
	fields map[string]data.Value
	order  []string
	lock   sync.Mutex
}

func NewRecord(fields ...RecordField) (*Record, error)  // RecordField{Name, V}
func MustRecord(fields ...RecordField) *Record           // errs.Panic on error
func F(name string, v data.Value) RecordField            // field literal helper

// interface assertions (house pattern, array.go:461-466):
var recInterfaceChecker *Record
var (
	_ data.Value  = recInterfaceChecker
	_ data.Record = recInterfaceChecker
)
```

- **Whole-structure access stays on the inherited `Value` contract — no extra
  method.** `Get(ctx) any` returns the **whole underlying structure** as a
  deep plain-Go snapshot: `map[string]any`, each field via its own
  `Value.Get` (a nested record yields a nested map, a list its elements) — a
  copy, safe to mutate. `Update(ctx, any)` is symmetric: it accepts a
  `map[string]any` and replaces matching fields. So every existing
  whole-value consumer (e.g. an association's `srcV.Get(ctx)` →
  `structure.Update(...)`, association.go:262-273) works on records
  unchanged. (An S4 adapter-backed record will return its **native Go
  object** from `Get` — the same contract, per tier; decided there.) A
  separate "whole structure" method is deliberately not added: it would
  return the same `any` under a second name — `Value` already owns
  whole-value access; `Record` adds only the structural verbs.
- `Clone()` deep-clones: a fresh `Record` with each field's `Value.Clone()`
  (the `Array.Clone` cursor-preserving precedent, array.go:99-107 —
  order preserved here).
- `Type()` returns `"record"`.
- **Forward-pointer (S4):** a `Record` constructed *from a native Go struct*
  (a live view via the type→adapter registry — wrap, not convert) is the S4
  tier (ADR-011 v.6 §2.9.5); its constructor rides the adapter machinery and
  is out of S1's reflection-free scope. In S1 a native struct participates
  whole-value only, exactly as today.

### 3.3 The path grammar and resolver (`pkg/model/data/path.go`)

```
path  := head step*
head  := name                     // resolved by the existing seam lookup
step  := '.' name                 // Record.Field
       | '[' uint ']'             // Collection.GetAt
name  := any run without '/', '.', '[', ']' (CheckName-legal)
```

```go
// Step is one navigation step of a structural path.
type Step struct {
	Field string // ".field" when Index < 0
	Index int    // "[i]" when >= 0
}

// SplitPath splits "order.items[0].price" into head "order" and its steps.
// A path with no structural characters returns (name, nil) — the plain case.
func SplitPath(path string) (head string, steps []Step, err error)

// WalkSteps folds steps over v: '.name' asserts Record, '[i]' asserts
// Collection. Errors are classified and name the failing step, the walked
// prefix, and the actual kind — never a panic.
func WalkSteps(ctx context.Context, v Value, steps []Step) (Value, error)

// NewPathData wraps a path-resolution leaf as a transient, read-only Data:
// Name() returns the full path, state is Ready. It deliberately BYPASSES
// CheckName — a path is a derived address, not a data-element name — which
// the CheckName-guarded constructors (FR-6) would rightly reject.
func NewPathData(path string, v Value) Data
```

**The raw-element rule** (Collection.GetAt returns `any`, §1): if an element
asserts to `data.Value`, the walk continues over its capabilities; a raw Go
value is terminal — it is wrapped as a read-only scalar when it is the **last**
step's result, and a further step into it is a classified error
(`NOT_NAVIGABLE`, naming the prefix). Nesting therefore requires elements that
are themselves `Value`s (e.g. `Array[data.Value]` holding `*values.Record`s) —
the shape `values.Record` produces naturally.

### 3.4 Shape by traversal (`pkg/model/data/shape.go`)

```go
// FieldInfo describes one field/element at a path level.
type FieldInfo struct {
	Name string // field name; "[]" for a collection's element slot
	Kind string // "scalar" | "list" | "record" — by capability assertion
	Type string // the leaf's Value.Type() for scalars
}

// SchemaAt lists the fields (records) or element shape (collections) at path
// ("" = the value itself). One level; recurse by appending steps.
func SchemaAt(ctx context.Context, v Value, path string) ([]FieldInfo, error)

// Walk visits every node depth-first with its full path.
func Walk(ctx context.Context, v Value, visit func(path string, fi FieldInfo)) error
```

### 3.5 The seam integration (`internal/scope/frame.go`)

`Frame.GetData` (frame.go:174-198) becomes, in order:

1. `strings.Cut(name, PathSeparator)` — the **provider cut, unchanged first**
   (ADR-010 §2.7; the tail stays opaque, so `BUSINESS/order.items[0].price`
   rides to its provider verbatim);
2. `data.SplitPath(name)` — no structural chars → **today's path, unchanged**
   (frame-first lookup, container walk-up);
3. otherwise: resolve the head exactly as today; a head that is **not Ready**
   (or holds a nil value) fails with a classified error — structural
   resolution never walks unusable data (the `Association.Find` non-Ready
   precedent, association.go:299-301) — then
   `data.WalkSteps(ctx, found.Value(), steps)`, wrapping the leaf via
   `data.NewPathData(path, leaf)` (§3.3): the leaf is Ready **by
   construction** (it was successfully read from a Ready head) — the same
   `Data` contract every `Find` consumer already expects.

The **input-mapping** source (`Association.Find`, association.go:290-303)
and the **output-mapping** fault source
(`pkg/tasks/errormapper.go:139-155`) apply `SplitPath`/`WalkSteps` the same
way — head resolved by their existing exact lookup, steps walked, leaf
wrapped — so a `DataAssociation` transformation and an output-mapping rule
both read into structured values (FR-4).

### 3.6 The deletion inventory (FR-7)

| Artifact | Location | Action |
|---|---|---|
| `Updater` interface | value.go:89-99 | delete |
| `UpdateCallback` | value.go:101-107 | delete (drops value.go's `time` import) |
| `ChangeType` + 3 consts | value.go:109-119 | **keep** (own decl block; S3 vocabulary) |
| `evtUpdaters` + init | variable.go:17,25 · array.go:17,29 | delete |
| `Register`/`Unregister` | variable.go:83-118 · array.go:394-429 | delete |
| `notify` + async senders | variable.go:120-142 · array.go:432-457 | delete |
| `notify` call sites | variable.go:62 · array.go:85,224,248,311,341 | delete calls |
| assertion lines | variable.go:146-150 · array.go:461-466 | drop the `data.Updater` line only |
| tests | values_test.go: `TestArray/update_check` (:222-266), `TestVariable/update check` (:325-363), `TestArrayDeleteLastNotifies` (:404-427) | delete the two subscription subtests; **rework** `TestArrayDeleteLastNotifies` to assert the FIX-014 1.3 invariant it guards (delete-last leaves a consistent, re-addable collection) without the callback channel |

### 3.7 The worked example (`examples/structural-data/`)

Entry split per the >80-line rule: `process.go` (model — an order `Property`
holding
`values.MustRecord(values.F("id", …), values.F("total", …), values.F("items", values.NewArray[data.Value](item0, item1)))`
where each item is itself a `MustRecord` (nesting requires `Value` elements,
§3.3), an exclusive gateway with `order.total > 100` on its priority flow, a
gofunc service task printing `order.items[0].price`), `main.go` (thresher
wiring + run + `WaitCompletion`). Expected output pins the full data path:

```
route: premium (order.total = 150 > 100)
first item price: 50
```

## 4. Analysis & decisions

- **§4.1 Why the walk lives in the seam, not the consumers.** Conditions,
  goexpr expressions, and mapping rules already receive a `data.Source` and
  call `Find` (§1) — one integration point (`frame.GetData` + the fault
  source) reaches all of them; per-consumer path handling would triplicate the
  grammar. Verified: the gateway evaluator's Source *is* the runtime env
  (gateway.go:221-248).
- **§4.2 Why `Field` returns `(Value, error)`, not `(Value, bool)`.**
  `Collection.GetAt` returns `(any, error)` with classified errors — the
  sibling capability follows the sibling's contract; a comma-ok shape would
  make the two capabilities disagree on error discipline.
- **§4.3 Fail-loud walking.** Every mis-step (field into a scalar, index into
  a record, unknown field, out-of-range, step into a raw element) is a
  classified `errs` error carrying the walked prefix and the actual kind —
  ADR-011 v.6 §2.2's "never silently produce nothing" posture; no nil-returns.
- **§4.4 Why reserve `.`/`[`/`]` now.** The resolver cannot distinguish
  `a.b` the *name* from `a.b` the *path*; the sweep found zero in-repo names
  using the characters (§1), so the reservation is free today and breaking
  later. Mirrors SRD-010's `/` reservation — same function, same error class.
- **§4.5 Why delete `Updater` in S1** (not S3, which replaces it). The value
  types are reshaped here (capability assertions, new sibling type); carrying
  dead fields/goroutines through the reshape is wasted motion, and the
  stale-interface rule says dead machinery goes on sight. `ChangeType` stays —
  deleting it would orphan the observability phase mirror and S3 re-adds it
  anyway.
- **§4.6 Rejected: a `Record` method on `Value` / a new mega-interface.**
  Capability-by-assertion is the established pattern (`Collection`,
  item.go:85-89); widening `Value` would break every implementor.
- **§4.7 Rejected: traversal on the interface / a withholdable `Traverser`;
  the helpers are deliberately general over `Value`.** `SchemaAt`/`Walk` stay
  free functions (the `fs.WalkDir`-over-minimal-`fs.FS` pattern): a method
  would force every tier to reimplement path walking (§4.1) and to
  re-dispatch the recursion per node, which the one free function already
  does across mixed implementations. Enumeration (`Keys`) stays mandatory on
  `Record` because the **engine itself** consumes it — the S3 commit-diff and
  the shape helpers; hiding is layered where it belongs: per-field via the S4
  adapter tags (`gobpm:"-"` — absent from `Keys` AND `Field`), per-observer
  via the ADR-013 v.2 visibility seam. A future non-enumerable need is an
  additive marker the helpers/diff would respect (opaque-leaf diffing) — not
  paid for speculatively. And the helpers take `Value`, not `Record`, because
  **shape is a total question**: a scalar is a valid *answer*
  (`{Kind: "scalar", Type: "int"}`), not a failure; discovery must not
  require knowing the kind before asking (asserting `Record` first is
  circular); the recursion's own intermediate nodes are typed `Value`
  (`Field` returns one, `GetAt` yields one); and the real failures —
  unknown field, out-of-range, a step deeper than the structure — are
  **data-dependent**, arising at any depth, so narrowing the top-level
  receiver's type prevents none of them. Only `WalkSteps` with steps against
  a leaf is partial, and there failing loudly is the §4.3 contract.

## 5. Public API surface

Additive: `data.Record`, `data.Step`, `data.SplitPath`, `data.WalkSteps`,
`data.NewPathData`, `data.FieldInfo`, `data.SchemaAt`, `data.Walk`,
`values.Record` + `values.NewRecord`/`MustRecord`/`RecordField`/`values.F`. **Removed (breaking, pre-1.0,
changelog at release):** `data.Updater`, `data.UpdateCallback`, and the
`Register`/`Unregister` methods on `values.Variable`/`values.Array` (zero
known consumers — §1). **Behavior change:** data-element names may no longer
contain `.`/`[`/`]` (`CheckName`); a structural path in `Find` now resolves
instead of failing the exact-name lookup. `ChangeType` is unchanged.

## 6. Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | `TestRecordValue` (values) | Keys insertion order; Field/SetField; unknown-field error class; deep Clone independence; Get/Update map round-trip; field-name CheckName enforcement |
| T-2 | `TestSplitPath` (table) | plain name → no steps; `.field`/`[i]` mixes; malformed (`a..b`, `a[`, `a[x]`, trailing `.`) → classified errors |
| T-3 | `TestWalkSteps` (table) | record/list/leaf navigation; field-into-scalar, index-into-record, unknown field, out-of-range, step-into-raw-element → classified errors naming the prefix |
| T-4 | `TestFrameStructuralRead` (scope) | `frame.GetData("order.items[0].price")` returns the leaf as Ready `Data`; plain names and `RUNTIME/<var>` byte-identical to today |
| T-5 | `TestGatewayRoutesOnStructuralPath` | an exclusive gateway condition `order.total > 100` routes correctly — the §2.9.2 acceptance criterion, no gateway-code change |
| T-6 | `TestGoexprReadsStructuralPath` | a `GExpFunc` calling `ds.Find(ctx, "order.items[0].price")` computes from the leaf |
| T-7 | `TestSchemaAtAndWalk` | one-level fields+kinds at "", at a nested path; full-descent visit set |
| T-8 | `TestCheckName` (flipped) | `order.items.price`, `a[0]` as **names** now rejected; plain and `SOURCE/`-free names still pass |
| T-9 | `TestFindPlainNameUnchanged` | regression: every pre-S1 `Find` behavior (plain, provider-qualified) unchanged; an under-specified (opaque-scalar) item reads whole exactly as today |
| T-10 | example smoke | `timeout 20 go run ./examples/structural-data/` exits 0 with the pinned output |
| T-11 | `TestAssociationTransformationStructuralRead` | a `DataAssociation` transformation's `GExpFunc` reads `<sourceID>.items[0].price` through `Association.Find` — the input-mapping half of FR-4 |

## 7. Milestones

| # | Scope |
|---|---|
| **M1** | FR-6 + FR-7: `CheckName` reservation (+ `name_test.go:18` flip, T-8) and the full `Updater` deletion (§3.6, incl. the test rework). Self-contained, shrinks the value types before the reshape. |
| **M2** | FR-1 + FR-2: the `Record` capability + `values.Record` (+ assertions). T-1. |
| **M3** | FR-3 + FR-5: `SplitPath`/`WalkSteps` + `NewPathData` + `SchemaAt`/`Walk`. T-2, T-3, T-7. |
| **M4** | FR-4: the seam integration — `frame.GetData`, `Association.Find` (input mapping), the fault source (output mapping); integration tests. T-4, T-5, T-6, T-9, T-11. |
| **M5** | FR-8: the worked example + smoke (T-10); §10 fill; `/check-srd`; ADR-011 v.6 → Accepted flip + SAD-001 catalog sync + linked-docs sweep; status flip. |

## 8. Cross-doc

| Ref | Version | Direction | Role |
|---|---|---|---|
| ADR-011 | v.6 (Draft → Accepted at this landing, per §2.9.6) | SRD → ADR (up) | implemented here (S1) |
| ADR-010 | v.2 | up | the provider seam the resolver composes with (§3.5) |
| ADR-012 | v.1 | up | public contracts in `pkg/*` |
| SRD-010 | (one-shot, by number) | sideways | the `/` name-reservation precedent FR-6 extends |

## 9. Definition of Done

- [ ] FR-1..FR-8 wired; every §6 test exists and is green.
- [ ] The `Updater` machinery gone (repo grep clean); `ChangeType` intact.
- [ ] Plain-name and provider `Find` behavior regression-proven (T-9).
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); full `-race`;
      example smoke (T-10) exits 0.
- [ ] ADR-011 v.6 flipped to Accepted (+ its RU twin updated); SAD-001 ADR
      catalog row updated; linked docs swept — in the landing change-set.
- [ ] §10 filled with milestone SHAs and deltas.

## 10. Implementation summary

> ⚠️ TODO: filled after landing.

## Open questions

None. The `Record` method set, the path grammar, the raw-element rule, the
deletion inventory, and the reservation scope are decided above; identifier
spellings and file layout inside the slices are implementation detail.
