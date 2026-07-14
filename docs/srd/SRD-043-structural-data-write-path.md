# SRD-043 — Structural data S2: the write path

| Field | Value |
|---|---|
| Status | Draft v.1 |
| Version | v.1 |
| Date | 2026-07-14 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-011 v.6](../design/ADR-011-process-data-flow.md) §2.9.3 (read and write both flow through the path) — the S2 slice (§2.9.6). ADR-011 v.6 is already Accepted; S2 is a slice of it and does **not** bump the ADR. |
| Upstream | [ADR-010 v.2](../design/ADR-010-process-data-model.md) §2.7 (the data plane / commit), [ADR-012 v.1](../design/ADR-012-execution-layering.md) (public contracts in `pkg/*`) |
| Refines | SRD-042 (S1 read path — by number, sideways) |

S1 (SRD-042) landed the read path: `data.Record`, the resolver, addressing into
values through the data-access seam. S2 lands the **write** counterpart: setting
a value at a structural path — `order.items[0].price = X` — and **assembling** a
nested output across mapping rules, closing the "mapping and associations
replace **whole** values only" gap. Owner-enforced shape: a dynamic
`values.Record` is permissive (creates missing intermediates); typed-target
rejection is **S4**. Commit-diff / DataChange is **S3**; nested-target
`DataOutputAssociation` writes are a documented follow-up (§8).

---

## 1. Background & current state (verified against the landed S1)

- **The read walk stops at the leaf and is read-only** (`pkg/model/data/path.go`):
  `WalkSteps` (:151) folds *all* steps to the leaf; a raw collection element is
  wrapped as `scalarLeaf` whose `Update` **always errors** (:219) — its comment
  (:212) reserves writes for "the owning Record/Collection (SRD-042 S2)". So a
  write needs a **distinct walk-to-parent** returning the mutable owner.
- **`Record.SetField` is the field-write primitive** (`values/record.go:182`):
  permissive (adds an unknown field, appends to order), `CheckName`s the name,
  rejects nil. A field value is itself a `data.Value`, so a field can hold a
  record or list — **nested vivify works by construction**.
- **`Collection` has no set-at-index** (`value.go:44-86`): `Update` writes the
  **current cursor** only (`array.go:64`); `GoTo`+`Update` mutates the shared
  cursor as a write side-effect and is two locked ops; `Insert` **shifts**, it
  does not replace (`array.go:264`). No list index auto-grows — `GetAt`/`GoTo`
  reject `i≥len` (`checkIndex`, :336).
- **Outputs are produced and committed WHOLE.** `ApplyOutputMapping` builds a
  **fresh** whole datum per rule (`outputmapping.go:52` `append(out,
  MustParameter(r.Var, …))`); the track `re.Put`s whole Data (`service_task.go`
  bindOutput); commit is whole-replace (`scope.go:249` `vv[name]=d`; `Put`
  last-write-wins, `frame.go:164`). Two rules on the same `Var` **clobber** — so
  assembling a record across rules needs one shared output value.
- **No production caller writes into a value by path** (grep of `.SetField(` /
  `SetPath` / `WriteSteps` is empty) — S2 is the first structural writer.

## 2. Requirements

### Functional

- **FR-1 — the write-walk primitive.** `pkg/model/data` gains
  `SetPath(ctx, root Value, path string, v Value) error` (§3.1): parse the
  root-relative path, walk to the **parent** of the last step (creating missing
  intermediates, FR-4), then set the last step — `Record.SetField` for a
  `.field`, the collection index-set (FR-2) for a `[i]`. Every mis-step is a
  classified `errs` error naming the walked prefix and actual kind; a whole-value
  write (empty path) is rejected (use `Value.Update`).
- **FR-2 — `Collection.SetAt`.** `data.Collection` gains
  `SetAt(ctx, index any, value any) error` (§3.2) — an **atomic, cursor-free**
  indexed set, mirroring `GetAt`. `values.Array` implements it (+ a typed
  `SetAtT(index int, value T)`); the mocks regenerate. This realizes the ADR's
  "the collection's index write" (§2.9.3) — the missing sibling of `GetAt`.
- **FR-3 — index-write bounds.** `SetAt` sets an existing element for
  `index ∈ [0, len)`; **appends** when `index == len` (so rules assemble a list
  sequentially — matching `Array.Insert`'s existing `[0, len]` append tolerance,
  `array.go:281`); an `index > len` (a hole) is an `OutOfRangeError`. Grow by
  append, never by hole (§2.9.3 "created when the target permits").
- **FR-4 — auto-vivify (dynamic targets only).** Walking to the parent, a
  **missing** intermediate is created per the **next** step's kind: a following
  `.field` → `values.NewRecord`, a following `[i]` → `values.NewArray[data.Value]`.
  Vivify runs only when the parent is a **permissive dynamic** value (a
  `*values.Record` / dynamic `Array`); a typed/adapter parent that rejects an
  unknown field surfaces its own error (the S4 path). The leaf value set is `v`.
- **FR-5 — output-mapping assembly.** `OutputRule.Var` MAY be a **structural
  path** (§3.3). `ApplyOutputMapping` groups rules by the **head** of
  `SplitPath(Var)`; per head it assembles **one** output value — a plain `Var`
  (no steps) stays the whole-value datum (today's behavior, NFR-2), while
  nested-path rules build one `values.Record` and `SetPath` each rule into it —
  then emits **one** `data.Data` per head. A **malformed** rule `Var` (bad path)
  is a classified mapping error; mixing a plain and a nested rule for the same
  head is a classified error.
- **FR-6 — the worked example.** An example builds a nested output by mapping:
  a worker (or task) whose output rules assemble `order.total` + a list of
  `order.items[i].price` into one `order` record output, read back to prove the
  assembled shape (§3.4). Smoke to exit 0.

### Non-functional

- **NFR-1 — no new reflection.** S2 adds zero `reflect` (adapters are S4); the
  write walk is capability dispatch.
- **NFR-2 — whole-value mapping unchanged.** A plain `Var` (empty path) takes
  today's whole-datum path byte-identical; existing output-mapping tests stay
  green.
- **NFR-3 — coverage.** Diff-coverage ≥95% (aim 100%) per `make ci`; full
  `-race`.

## 3. Models (code-grounded)

### 3.1 The write walk (`pkg/model/data/path.go`)

```go
// SetPath sets v at the structural path within root (a path relative to root:
// "items[0].price"). It walks to the parent of the last step, creating missing
// intermediate records/lists on a permissive dynamic target, then sets the last
// step via Record.SetField (a field) or Collection.SetAt (an index). An empty
// path is an error — a whole-value write is Value.Update.
func SetPath(ctx context.Context, root Value, path string, v Value) error
```

Internals (unexported): `parseRelPath(path) ([]Step, error)` — the leading
segment is a field step (an index-leading path starts with `[i]`), reusing
`SplitPath`'s tokenizer; and `walkToParent(ctx, root, steps) (Value, error)`
that descends `steps[:len-1]`, at each step asserting `Record`/`Collection` and,
on a missing child, **vivifying** per the following step's kind and attaching it
(`SetField` / `SetAt`-append) before descending. The final step is applied by
`SetPath`: `SetField(field, v)` or `SetAt(index, v)`.

### 3.2 `Collection.SetAt` (`value.go` + `values/array.go`)

```go
// Collection gains, beside GetAt:
SetAt(ctx context.Context, index any, value any) error
```

`Array.SetAt` (untyped) + `Array.SetAtT(index int, value T) error` (typed):
type-check the value (`checkValue[T]`), then — under the array lock, **one op**
— set `elements[i]` for `i ∈ [0,len)`, `append` for `i == len`, else
`OutOfRangeError`. It does **not** touch the cursor (unlike `GoTo`+`Update`).
Interface assertion `_ data.Collection` already covers it via `*Array`.

### 3.3 Output-mapping assembly (`pkg/tasks/outputmapping.go`)

`ApplyOutputMapping` is restructured (the `OutputRule` shape is unchanged —
`Var` simply may now carry `.`/`[`):

```
for each rule r:
    head, steps, err := SplitPath(r.Var)   // a malformed Var path → classified
    if err != nil { return err }           //   mapping error (SplitPath returns it)
    group[head] += (r, steps)
for each head, its rules:
    if a lone rule with len(steps) == 0     // plain Var → whole value (today, NFR-2)
        out += MustParameter(head, evaluated whole value)
    else                                    // nested-path rules → assemble one record
        rec, err := values.NewRecord(); if err != nil { return err }
        for each rule: v := ee.Evaluate(r.Path, body); SetPath(rec, <Var below head>, v)
        out += MustParameter(head, rec)
    // a head mixing a plain and a nested rule → classified error
```

`SplitPath` (`path.go:36`) returns `(head, steps, err)`; the grouping key is
`head`, the plain-vs-nested discriminator is `len(steps) == 0`, and a malformed
rule `Var` surfaces `SplitPath`'s error as a classified mapping error.
`values.NewRecord` (`record.go:37`) returns `(*Record, error)`.

One datum per head (closing the clobber gap), committed as today via the track's
`re.Put` / `Frame.Commit` — **no commit-path change**; the write happens on the
value before it is committed whole.

### 3.4 The worked example

Extend/add an example where a worker returns a flat body and the output mapping
**assembles** a nested `order` record — e.g. rules `order.total`,
`order.items[0].price`, `order.items[1].price` → one `order` output — then a
downstream read (`order.items[1].price`) prints the assembled value. Entry split
per the >80-line rule.

## 4. Analysis & decisions

- **§4.1 A distinct walk-to-parent, not `WalkSteps`.** The read walk returns the
  leaf (and wraps raw scalars read-only); a write needs the mutable **owner** of
  the last step. Reusing `WalkSteps` then writing through its result is
  impossible (`scalarLeaf` is read-only by design). So `SetPath` walks
  `steps[:len-1]` itself.
- **§4.2 `SetAt` over `GoTo`+`Update`.** `GoTo`+`Update` mutates the shared
  cursor as a side-effect of a write and is two locked ops (a race window);
  `Insert` shifts, not sets. A dedicated atomic `SetAt` is the correct
  primitive and the sibling `GetAt` always implied. Cost: one `Collection`
  interface method + mock regen — contained.
- **§4.3 Append-at-`len`, no holes.** Lists grow only by append (`i==len`); a
  gap (`i>len`) is an error, matching the read side's strict bounds and avoiding
  nil holes. This is enough to assemble a list in order.
- **§4.4 Vivify is dynamic-only.** Creating a missing intermediate is legal only
  on a permissive value (`values.Record` adds fields; a typed adapter rejects) —
  so the create-missing branch keys on the parent being dynamic, deferring
  typed-shape enforcement to S4. Vivify type follows the **next** step
  (`.field`→Record, `[i]`→Array) — unambiguous.
- **§4.5 Assemble-by-head in the mapping, not a new commit path.** The
  clobber-on-same-`Var` gap is closed by building one value per head and setting
  into it, then committing that whole value as today. The commit machinery
  (`Frame.Commit`/`Scope.Commit`) is untouched — S2 changes only what value is
  produced, not how it lands.
- **§4.6 Rejected: `GoTo`+`Update` for the index set** — cursor side-effect +
  race (§4.2). **Rejected: index auto-grow with holes** — nil gaps, unclear
  semantics (§4.3). **Deferred: `DataOutputAssociation` nested-target writes** —
  a `DataOutputAssociation` has no native target sub-path (its target is a whole
  `ItemAwareElement`, whole-`Update`d); a nested-target mechanism there is a
  separate design, tracked as a follow-up. Associations still copy whole values
  in the standard's three shapes.

## 5. Public API surface

Additive: `data.SetPath`; `data.Collection.SetAt` (**a new interface method** —
the one breaking change for any third-party `Collection` implementer; none known
outside `values.Array`; mocks regenerate); `values.Array.SetAt` / `SetAtT`.
`OutputRule` is unchanged — `Var` gains the *ability* to be a structural path (a
plain name is the empty-path case). No removals. `Value.Update` (whole-value)
is unchanged.

## 6. Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | `TestSetPath` (data) | set an existing nested field; append a list element (`i==len`); auto-vivify a missing record and list; `i>len` → error; field-into-scalar / index-into-record mis-step → error; a bad field name rejected; empty path → error |
| T-2 | `TestArraySetAt` (values) | set `[0,len)`; append at `len`; `>len` → OutOfRangeError; type-mismatch → error; **cursor unchanged** after SetAt (contrast GoTo+Update) |
| T-3 | `TestOutputMappingAssembly` (tasks) | nested-path rules assemble one `order` record output (total + items); a plain `Var` still emits a whole datum unchanged (NFR-2); a plain+nested clash on one head → error; a **malformed** rule `Var` → classified error |
| T-4 | `TestOutputMappingPlainUnchanged` (tasks) | regression: existing whole-value output rules byte-identical |
| T-5 | example smoke | the assemble-a-nested-output example exits 0 with the pinned read-back |

## 7. Milestones

| # | Scope |
|---|---|
| **M1** | FR-1..FR-4: `Collection.SetAt` + `Array.SetAt`/`SetAtT` (+ mock regen), and `data.SetPath` + the walk-to-parent + auto-vivify. T-1, T-2. |
| **M2** | FR-5: the `ApplyOutputMapping` assembly-by-head rewrite. T-3, T-4. |
| **M3** | FR-6: the worked example + smoke (T-5); §10; `/check-srd`; SRD-043 → Accepted; roadmap note (ADR-011 v.6 stays Accepted — S2 is a slice, no ADR bump). |

## 8. Cross-doc

| Ref | Version | Direction | Role |
|---|---|---|---|
| ADR-011 | v.6 (Accepted) | SRD → ADR (up) | implements §2.9.3 (S2); **no bump** |
| ADR-010 | v.2 | up | the data plane / whole-value commit S2 writes before |
| ADR-012 | v.1 | up | public contracts in `pkg/*` |
| SRD-042 | (one-shot, by number) | sideways | the S1 read path this extends |

**Deferred (documented follow-up):** nested-target `DataOutputAssociation`
writes — a target sub-path mechanism on associations, a separate slice.

## 9. Definition of Done

- [ ] FR-1..FR-6 wired; every §6 test exists and is green.
- [ ] `Collection.SetAt` is cursor-free and atomic (T-2); no list grows by hole.
- [ ] Whole-value output mapping regression-proven (T-4); commit path untouched.
- [ ] `make ci` green; diff-coverage ≥95% (aim 100%); full `-race`; example smoke
      exits 0.
- [ ] SRD-043 flipped to Accepted; roadmap S2 note. ADR-011 v.6 unchanged.
- [ ] §10 filled with milestone SHAs and deltas.

## 10. Implementation summary

> ⚠️ TODO: filled after landing.

## Open questions

None. `SetPath` semantics, the `SetAt` interface addition + its bounds, the
dynamic-only vivify, and the assemble-by-head mapping are decided above; the
`DataOutputAssociation` nested-target write is explicitly a deferred follow-up
(§8), not an open question for S2.
