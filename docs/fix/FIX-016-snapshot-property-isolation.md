# FIX-016 — Snapshot property isolation (per-instance + frozen-template)

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-06-30 |
| Owner | Ruslan Gabitov |
| Related | [ADR-009 v.1 Per-instance node graph](../design/ADR-009-per-instance-node-graph.md), [ADR-019 v.1 Definition versioning](../design/ADR-019-definition-versioning.md) |

Remediation of the headline P1 finding of
`docs/audit/code-review-third-pass-2026-06-29.md` (§1.1): `Snapshot` shares
mutable process `Property` objects by reference, so concurrent instances of the
same process write to the **same** property objects — a data race plus
cross-instance (and cross-run) corruption. The snapshot mechanism exists to give
each instance private, isolated state (ADR-009); properties were the one part of
that state never cloned.

The sharing happens at **two** levels, both fixed here:

- **Clone → instance** (§1.1, the audited finding): `Snapshot.Clone` copies the
  `Properties` slice by reference, so every per-instance clone shares one set of
  property objects.
- **New → template** (§1.2): `Snapshot.New` stores `p.Properties()` by
  reference, so the snapshot — the *frozen* registered version — shares the live
  process definition's property objects. Its own comment claims "edits to the
  process after registration can't reach it" (true for the cloned node graph,
  **false** for the shared properties), breaking ADR-019's frozen-version
  guarantee.

## 1. Symptoms

- **1.1 (P1, audit §1.1) `Snapshot.Clone` shares properties across instances.**
  `Clone` builds the per-instance snapshot with `Properties: s.Properties`
  (`internal/instance/snapshot/snapshot.go:182`) — the slice and its
  `*data.Property` pointers are shared. `instanceScope.load` then commits those
  same pointers into each instance's data plane without copying
  (`internal/instance/scope.go:63-68`, `dd = append(dd, p)`), and `scope.Commit`
  stores them by reference (`internal/scope/scope.go:249`, `vv[names[i]] = d`). A
  `*data.Property` embeds a mutable `ItemAwareElement` (`pkg/model/data/
  property.go:15-18`) that execution updates **in place** — task output
  write-back (`pkg/model/activities/task.go:295`, `o.Value().Update(...)`) and
  data association (`pkg/model/data/association.go:176`,
  `iae.Value().Update(...)`). So N instances of one process mutate shared
  property objects: an unsynchronized data race across instance goroutines, and
  silent cross-instance corruption reproducible even sequentially (properties are
  never reset). **Empirically reproduced**: two `Clone`s return the *same*
  `*data.Property` pointer, and a `Value().Update` on one is read back through the
  other.
- **1.2 (P1, sibling) `Snapshot.New` shares the live process's properties into
  the frozen template.** `New` clones the node graph (`snapshot.go:79`,
  `n.Clone()`) so "edits to the process after registration can't reach it"
  (`:72-74`), but stores `Properties: p.Properties()` by reference (`:63`). A
  property mutated on the original `process.Process` after `snapshot.New`
  therefore *does* reach the registered snapshot — and, transitively, instances
  cloned from it — violating the immutable-version contract (ADR-019 §2.3).

## 2. Root-cause analysis

The per-instance isolation model (ADR-009) and the frozen-version model
(ADR-019) are both realized by **cloning the template** — but only the *node
graph* was cloned. Properties, the other piece of mutable per-instance state,
were classified with the genuinely-immutable header (process id/name,
correlation-key definitions, instantiating-start descriptors) and shared by
reference. There is no `Property.Clone`, so neither `New` nor `Clone` had a
deep-copy to call; the gap was masked because no test exercised property-data
isolation (`clone_race_test` covers only the node graph).

## 3. Solution

### 3.1 Considered alternatives
- **Clone in `instanceScope.load` (before `Commit`) instead of in
  `Snapshot.Clone`**: rejected — `Snapshot.Clone` is *the* per-instance isolation
  boundary (it already clones every node there); cloning properties at the same
  point keeps the whole per-instance copy in one place, and any future consumer
  of a clone gets isolated properties, not just the scope-load path.
- **Only fix `Clone`, leave `New` sharing the process's properties**: rejected
  (the user's catch) — `New` clones the node graph specifically so the frozen
  snapshot is independent of the live process (ADR-019 §2.3); leaving properties
  shared keeps a mutation channel from a post-registration process edit into the
  registered version. The template must own its properties too.

### 3.2 Per-site changes
- **3.2.1** `pkg/model/data/property.go` — add `Property.Clone() (*Property,
  error)`: deep-copy via the existing `ItemAwareElement.Clone`, carrying the
  `name`:
  `iae, err := p.ItemAwareElement.Clone(); … return &Property{name: p.name, ItemAwareElement: *iae}, nil`.
- **3.2.2** `snapshot.go` `New` (`:63`) — clone each process property into the
  snapshot instead of sharing the slice, so the frozen template owns private
  copies (consistent with the node-graph clone at `:79`). Propagate a clone
  error like the node path does.
- **3.2.3** `snapshot.go` `Clone` (`:182`) — clone each snapshot property into
  the per-instance clone instead of `Properties: s.Properties`, so each instance
  owns private property objects.
- **3.2.4** Fix the misleading doc-comments: `Clone` (`:174`) no longer lists
  properties under the shared immutable header; `New`'s "edits … can't reach it"
  (`:72-74`) now holds for properties too.

**Sibling audit (no change needed).** The other shared header fields are consumed
**read-only** per instance, so sharing them is safe: `CorrelationKeys` —
matching/derivation only (`internal/instance/instance.go:390`,
`internal/eventproc/eventhub/waiters/message.go:206`); `InstantiatingStarts` —
starter construction only (`pkg/thresher/instance_starter.go:110-112`). Only
`Properties` carry per-instance mutable runtime state.

## 4. Verification

### 4.1 Tests
| Test | Asserts |
|---|---|
| `TestPropertyCloneIsDeepCopy` | `Property.Clone` returns a distinct object whose `Value().Update` does not affect the source (and preserves `name`) (3.2.1) |
| `TestSnapshotClonePropertiesIsolated` | two `Snapshot.Clone`s of one snapshot hold **distinct** `*data.Property` objects; a `Value().Update` on one is **not** visible through the other (1.1; replaces the reproduced bug with the isolation invariant) |
| `TestSnapshotCloneConcurrentPropertyWrites` (`-race`) | concurrent property writes across two clones are race-free (1.1) |
| `TestSnapshotNewFreezesProperties` | mutating a process property **after** `snapshot.New` does not change the snapshot's property value (1.2; the frozen-template guarantee) |

## 5. Prevention
`Property.Clone` gives both isolation boundaries (`New`, `Clone`) the deep-copy
they lacked, so properties are now cloned exactly like nodes — the same
mechanism, applied to the same class of per-instance mutable state. The new
property-data isolation tests close the gap `clone_race_test` left, so a future
regression (sharing re-introduced) fails CI.

## 6. Regressions
No public API changes. `Snapshot.New`/`Clone` now allocate one `Property` copy
per property per call — bounded by the (small) property count, paid once at
registration and once per instance start; negligible against the node-graph
clone already done there. Behaviour for correctly-isolated single-instance runs
is unchanged; the only behavioural delta is the **removal** of the cross-instance
leak. `ItemAwareElement.Clone` errors on a nil value — surfaced as a build/clone
error exactly like the node-clone path, not a silent skip.

## 7. Related
ADR-009 v.1 (per-instance node graph — the isolation model this extends from
nodes to properties; §2.1 "instances share only the template"). ADR-019 v.1
(definition versioning — the frozen-snapshot guarantee §1.2 restores for
properties). No new ADR: this applies both existing decisions to the property
data that was missed, it does not change either contract.

## 8. Implementation summary
*(filled at landing.)*

## 9. Open questions
None.
