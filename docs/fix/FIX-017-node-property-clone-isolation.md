# FIX-017 — Node-property clone isolation and value-less rejection at registration

| Field    | Value                                                  |
| -------- | ------------------------------------------------------ |
| Status   | Accepted                                               |
| Date     | 2026-07-02                                             |
| Owner    | Руслан Габитов                                         |
| Related  | ADR-010 v.2, SAD-001 v.1 §14.1, FIX-016                |

## 1. Symptoms

Two defects with a single root, both at the process → snapshot → instance clone
boundary for **activity and event properties** (per BPMN, "only Processes,
Activities, and Events MAY contain Properties" — `pkg/model/data/property.go:11`).

### 1.1 Node properties leak across the clone boundary

`activity.clone()` (`pkg/model/activities/activity.go:91`) copies the property
**map by reference**:

```go
func (a *activity) clone() activity {
	return activity{
		...
		properties: a.properties,   // shared map header → same underlying map + same *Property objects
		...
	}
}
```

`Event.clone()` (`pkg/model/events/event.go:161`) does the same with the property
**slice**:

```go
func (e *Event) clone() Event {
	return Event{
		...
		properties: e.properties,   // shared slice header → same backing array + same *Property objects
		...
	}
}
```

So a task's/event's cloned node — the snapshot template *and* every per-instance
node — shares the source node's property objects. An edit to the source process
after registration (removing a property from a task/event, or updating a
property's value) leaks into the already-registered snapshot and any running
instance; conversely, per-instance execution that mutated a node property would
cross-contaminate. Only per-execution-frame cloning
(`Frame.LoadProperties → instantiate → src.Clone()`) hides this today, and only
because there is currently no API to mutate a built node's properties — the
isolation is *absent*, not merely unexercised. FIX-016 fixed exactly this class
for **process-level** properties (`cloneProperties` in `snapshot.New`) but left
**node-level** properties shared.

### 1.2 Value-less node properties are not rejected at registration

A *value-less* property — one whose `ItemAwareElement` has no `structure`, so
`Value()` returns `nil` (`pkg/model/data/item.go:339`) — can never be filled: an
`ItemDefinition`'s structure *is* its value, an immutable typed `Variable[T]`
bound at construction with no setter (ADR-010 v.2). A process declaring one
cannot be executed. FIX-016 makes `snapshot.New` reject a value-less **process**
property (its clone fails). But a value-less property on an **activity or event**
is not rejected at registration — it surfaces only later, per-frame, when
execution reaches the owner and `src.Clone()` fails deep inside instance
execution. Asymmetric and late.

### 1.3 Common root

Both stem from the same limitation: node clones do not clone their properties —
they share them — and `flow.Node.Clone() Node` (`pkg/model/flow/node.go:88`) has
**no error channel**, so `activity.clone()` / `Event.clone()` cannot deep-copy
with validation (a value-less property can't be cloned). FIX-016 could only fix
the process level because `snapshot.New` — where process properties are cloned —
*does* return an error; the node-level clone path does not.

## 2. Root cause

`flow.Node.Clone()` returns only a `Node`. Cloning a property can fail (a
value-less property is unclonable — `ItemAwareElement.Clone` rejects a `nil`
value, `property.go:110`). With no error return, the node-clone helpers took the
only option available to a non-failing function: share the properties by
reference. That single missing error channel is the cause of both the isolation
gap (1.1) and the value-less asymmetry (1.2).

## 3. Solution

Give `flow.Node.Clone()` an error return, so the node-clone helpers can
**deep-copy** their properties (isolation) and **fail** on a value-less one
(validation). One clone operation does both — the clone *is* the validation. No
separate value-less predicate, no redundant re-clone.

### 3.1 Alternatives considered

- **(A) `Clone()` returns `(Node, error)`; the clone deep-copies properties and
  fails on a value-less one.** Chosen. A single operation isolates and validates;
  value-less rejection becomes structural at registration for every owner
  (process — already via FIX-016; activity/event — new); and the missing error
  channel that caused this whole class is removed permanently.
- **(B) Deep-copy in the clone helpers, but keep `Clone()` errorless and add a
  separate `Value()==nil` inspection (or a second `cloneProperties`) in
  `snapshot.New` to reject value-less.** Rejected: it re-clones or re-inspects
  properties the clone already processed — redundant work — and leaves the
  no-error-channel limitation in place for the next clone-failure class.
- **(C) A dedicated `Property.Validate()` predicate wired into `Process.Validate`,
  properties still shared by reference.** Rejected: it does not fix the isolation
  gap (1.1) at all, and it duplicates, as a `Value()==nil` predicate, the exact
  knowledge the clone already has (it can't clone a value-less property).

### 3.2 Fix sites

#### 3.2.1 `flow.Node.Clone()` gains an error — interface + implementors

`pkg/model/flow/node.go` — `Clone() Node` → `Clone() (Node, error)`.
`BaseNode.Clone` (panics) matches the new signature. All **14** concrete
implementors are updated:

- Property-owning, real logic (§3.2.2–3.2.3): the activities (`ServiceTask`,
  `UserTask`, `SendTask`, `ReceiveTask`) and events (`StartEvent`, `EndEvent`,
  `IntermediateCatchEvent`, `IntermediateThrowEvent`, `BoundaryEvent`).
- Property-less, mechanical (`return node, nil`): the five gateways
  (`Exclusive`, `Inclusive`, `Parallel`, `Complex`, `EventBased`).

The internal `clone()` helpers on the path propagate the error:
`task.clone()` (`task.go:60`), `catchEvent.clone()` / `throwEvent.clone()`
(`event.go:287,423`), `Gateway.clone()` (`gateway.go:146`, `nil`). Generated
mocks that implement `flow.Node` are regenerated (`make gen_mock_files`).

A single `data.CloneProperties([]*Property) ([]*Property, error)` helper carries
the deep-copy-and-validate logic (it clones each `*Property`, returning an error
on the first value-less one). It replaces snapshot's now-duplicate local
`cloneProperties`, so the process level (FIX-016) and the node level share one
cloner.

#### 3.2.2 `activity.clone()` deep-copies properties, fails on value-less

`pkg/model/activities/activity.go` — `clone() activity` →
`clone() (activity, error)`; the shared `properties: a.properties` map becomes a
deep copy via `data.CloneProperties(maps.Values(a.properties))`, re-keyed by
name, returning the error if any property is value-less. The clone now owns
private property objects.

#### 3.2.3 `Event.clone()` deep-copies properties, fails on value-less

`pkg/model/events/event.go` — `clone() Event` → `clone() (Event, error)`; the
shared `properties: e.properties` slice becomes `data.CloneProperties(e.properties)`,
propagating a value-less error.

#### 3.2.4 Call sites propagate

`snapshot.New` and `snapshot.Clone` (`snapshot.go`) clone each node via an
extracted `cloneNode` helper that wraps the error with the node identity; both
functions already return errors. A value-less activity/event property now rejects
the process at `snapshot.New`, alongside the process-level rejection FIX-016
performs there (both now via `data.CloneProperties`). Value-less properties can
only enter at `snapshot.New` (a raw process); at per-instance `snapshot.Clone`
the snapshot's properties are already validated, so that path's clone error is
`nil` in practice — but the signature stays honest (exercised by an internal
guard-test).

## 4. Verification

### 4.1 Tests

| Test | Site | Asserts |
| ---- | ---- | ------- |
| `TestActivityCloneIsolatesProperties` | `activities/clone_test.go` | a cloned task's property is a distinct object; a write through the source does not reach the clone. |
| `TestActivityCloneRejectsValueLessProperty` | `activities/clone_test.go` | `ServiceTask.Clone()` errors for a value-less property. |
| `TestSendTaskCloneRejectsValueLessProperty` | `activities/clone_test.go` | same for a `SendTask`. |
| `TestReceiveTaskCloneRejectsValueLessProperty` | `activities/clone_test.go` | same for a `ReceiveTask`. |
| `TestUserTaskCloneRejectsValueLessProperty` | `activities/clone_internal_test.go` | `UserTask.Clone()` error branch — value-less property injected directly (its constructor doesn't accept properties yet; see §8). |
| `TestEventCloneIsolatesProperties` | `events/clone_test.go` | a cloned event's property is distinct; a source write does not leak. |
| `TestEventCloneRejectsValueLessProperty` | `events/clone_test.go` | `StartEvent.Clone()` errors for a value-less property. |
| `TestEndEventCloneRejectsValueLessProperty` | `events/clone_test.go` | same for an `EndEvent`. |
| `TestIntermediateCatchEventCloneRejectsValueLessProperty`, `…Throw…`, `TestBoundaryEventCloneRejectsValueLessProperty` | `events/clone_internal_test.go` | Clone error branches — value-less property injected directly (see §8). |
| `TestCloneProperties` | `data/property_test.go` | `data.CloneProperties`: nil→nil, valued→deep copies, value-less→error. |
| `TestSnapshotNewRejectsValueLessNodeProperty` | `snapshot/property_isolation_test.go` | a process whose node carries a value-less property is rejected at `snapshot.New` (previously surfaced only at run time). |
| `TestSnapshotNodeEditAfterRegistrationDoesNotLeak` | `snapshot/property_isolation_test.go` | a node-property edit on the source after `snapshot.New` does not reach the snapshot's cloned node. |
| `TestCloneRejectsValuelessNodeProperty` | `snapshot/clone_internal_test.go` | `snapshot.Clone`'s node-clone error path. |

`TestStartEventClone` was updated to use a valued property (properties are now
deep-copied, not shared). The existing FIX-016 tests (`TestSnapshotNewRejectsValuelessProperty`,
`TestCloneRejectsValuelessProperty`) stay green.

### 4.2 Gate

`make ci` green (tidy · lint · build · `-race` · diff-coverage ≥95% ·
govulncheck); examples smoke exit 0. Mocks regenerated.

## 5. Prevention

Property isolation and usability are now enforced by the clone itself, uniformly
for all three BPMN property owners, at registration. The `flow.Node.Clone()`
error channel means a future clone-failure class (any node component that can't
be safely copied) has a place to report, instead of silently degrading to
shared-by-reference — closing the structural gap that produced both defects,
rather than patching each symptom.

## 6. Regressions / behavioural change

- **Public API:** `flow.Node.Clone()` signature changes to `(Node, error)`. All
  in-tree implementors and call sites are updated; embedders that implement
  `flow.Node` must add the error return.
- A process carrying a value-less property on an **activity or event** is now
  rejected at `snapshot.New` (was accepted at registration, failed later or never
  until execution reached it). Such a process was never executable.
- Node properties are now **isolated** source → snapshot → instance; a
  post-registration edit to a node property no longer leaks into a registered
  snapshot or a running instance. No previously-correct process changes behaviour.

## 7. Related

- **ADR-010 v.2** — process data model: an `ItemDefinition`'s structure *is* its
  value (immutable, typed, no setter) — why a value-less property can never be
  filled.
- **SAD-001 v.1 §14.1** — already records the deviation from BPMN's optional
  `itemSubjectRef`/structure (`0..1`): a gobpm process declaring an
  underspecified item-aware element cannot be executed and is rejected at
  snapshot/registration. FIX-017 makes that rejection uniform (node-level as well
  as process-level), so the deviation statement needs no change.
- **FIX-016** — snapshot property isolation for process-level properties; this
  FIX generalizes it to node-level properties and unifies value-less rejection
  through the clone.

## 8. Implementation summary

- **Commits (branch `fix/reject-value-less-properties`):** doc `f2bd5e3`;
  implementation `31a2269` (M1 signature ripple + M2 deep-copy/validation landed
  together — M1 alone could not meet the diff-coverage gate, as its error
  branches only become reachable with M2's deep-copy).
- **Production (20 files):** `flow/node.go` (interface + `BaseNode`); the 4 task
  and 5 event concrete `Clone` + their internal `clone()` helpers
  (`activity/task`, `Event/catchEvent/throwEvent`); the 5 gateway `Clone` +
  `Gateway.clone` (no error — gateways carry no properties);
  `data/property.go` (`CloneProperties`); `snapshot.go` (`cloneNode`, `New`,
  `Clone`, local `cloneProperties` removed in favour of `data.CloneProperties`).
- **Tests (20 files):** the M1 call-site updates across every node package, plus
  the §4.1 additions (2 new internal guard-test files:
  `activities/clone_internal_test.go`, `events/clone_internal_test.go`).
  Regenerated mocks (gitignored).
- **Verification:** `make ci` exit 0 — tidy · lint · build · `-race` ·
  **diff-coverage 100.0% of 148 changed coverable lines** (min 95%) ·
  govulncheck clean across all modules.
- **Discovery — constructor property gap.** Only `NewServiceTask` /
  `NewSendTask` / `NewReceiveTask` and `NewStartEvent` / `NewEndEvent` accept
  `data.WithProperties`; `NewUserTask`, `NewIntermediateCatchEvent`,
  `NewIntermediateThrowEvent`, and `NewBoundaryEvent` reject it, so those four
  node types cannot declare a property today. Their `Clone` error branch is
  therefore a defensive guard (like the gateways'), exercised by internal
  guard-tests that inject a value-less property directly — the same pattern the
  snapshot package uses for a guard `New` prevents. The constructor gap is
  recorded in `docs/backlog.md` (*Property configuration missing on some
  Activity/Event constructors*); when it is closed those branches become
  reachable through the public API.

## 9. Open questions

None. This resolves backlog **Q1** in the "reject via cloning at registration"
direction for all three BPMN property owners (distinct from the broader
construction-time / fill-on-write data-model question, which remains recorded).
