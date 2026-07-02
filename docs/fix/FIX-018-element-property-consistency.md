# FIX-018 — Consistent element properties: uniform support + value-less rejection at construction

| Field    | Value                                             |
| -------- | ------------------------------------------------- |
| Status   | Accepted                                          |
| Date     | 2026-07-02                                        |
| Owner    | Руслан Габитов                                    |
| Related  | FIX-017, ADR-010 v.2, SAD-001 v.1 §14.1           |

## 1. Symptoms

Per BPMN, "only Processes, Activities, and Events MAY contain Properties"
(`pkg/model/data/property.go:11`). gobpm wires that support **inconsistently**,
on two independent axes, so the feature works end-to-end for only 4 of the 9
property-owning node types (surfaced during FIX-017, recorded in
`docs/backlog.md`).

### 1.1 Declaration is inconsistent (constructor)

`data.WithProperties` is accepted by `NewServiceTask` / `NewSendTask` /
`NewReceiveTask` and `NewStartEvent` / `NewEndEvent`, but rejected by:

- `NewUserTask` — `invalid option type` (`ACTIVITIES_ERRORS`).
- `NewIntermediateCatchEvent` / `NewIntermediateThrowEvent` /
  `NewBoundaryEvent` — `config doesn't support PropertyConfigurator`
  (`DATA_ERRORS`, `pkg/model/data/property_option.go:78`).

### 1.2 Runtime loading is inconsistent (frame)

Node properties are materialized per execution by `Frame.LoadProperties`, called
from `task.go:209` (all tasks) and `event.go:467` (`throwEvent` → EndEvent,
IntermediateThrow). **Catch events never load their properties** —
`catchEvent.UploadData` (`event.go`) has no `LoadProperties` call. So a
`StartEvent` property is *accepted* (§1.1) but never instantiated in the frame,
hence unreadable.

### 1.3 Net effect

| Node | declare (ctor) | load (frame) | usable |
| --- | --- | --- | --- |
| ServiceTask / SendTask / ReceiveTask | ✓ | ✓ | ✓ |
| EndEvent | ✓ | ✓ | ✓ |
| StartEvent | ✓ | ✗ | ✗ (accepted, never loaded) |
| UserTask | ✗ | ✓ | ✗ (can't declare) |
| IntermediateThrow | ✗ | ✓ | ✗ (can't declare) |
| IntermediateCatch / Boundary | ✗ | ✗ | ✗ (neither) |

This is technical debt from incremental wiring, not a conceptual gap: the BPMN
property model is unchanged; some constructors and one runtime path were simply
never connected. No new concept is introduced — hence a FIX, not an ADR.

### 1.4 Value-less properties are admitted by the lenient constructor

A property whose item has no structure (`Value() == nil`) can never be filled in
gobpm (ADR-010: structure *is* value, immutable), yet `NewProperty` / `NewProp` /
`MustProperty` **accept** one. Today that degenerate property is caught only
*downstream* — at clone / registration (FIX-016, FIX-017). That late rejection is
a defense-in-depth backstop; the fail-fast place is the **constructor** itself
(the validate-all-public-inputs rule; backlog **Q1 option (B)**). Folding this in
now — while the property wiring is already open — keeps the foundation sound
rather than leaving a lenient constructor under the newly-consistent wiring.

## 2. Root cause

A property reaches a node through two requirements, and each broken site misses
one:

- **Option routing + a `PropertyAdder` config.** `data.WithProperties` returns a
  `PropertyOption` (`func(PropertyAdder) error`); `PropertyOption.Apply` only
  works if the config implements `data.PropertyAdder` (`AddProperty`), and the
  constructor's option loop must dispatch `data.PropertyOption` to it.
  - `NewUserTask`'s option loop (`user_task.go`) has no `data.PropertyOption`
    case → `default` → `invalid option type`.
  - `NewIntermediateCatchEvent` / `…Throw` / `NewBoundaryEvent` forward
    `baseOpts` verbatim to `newEvent → flow.NewBaseNode`, where a
    `PropertyOption` meets the **BaseNode** config, which is not a
    `PropertyAdder`. (Start/End avoid this only because `startConfig` /
    `endConfig` *are* `PropertyAdder`s that consume the option and pass the
    collected props to `newEvent` **positionally**.)
- **Runtime load.** `LoadProperties` was added to the task and throw-event data
  paths but not to `catchEvent.UploadData`, so catch-event properties are never
  instantiated.

Semantics are already correct and unaffected: a node property is an
execution-local **constant**, re-instantiated from its declared value on every
activation (BPMN §10.3.1 "when a Flow Element is instantiated, all Properties
contained by it are also instantiated"), realized by
`LoadProperties → instantiate → Clone` (FIX-017). FIX-018 only connects the
missing wiring; it does not change how properties live or are scoped.

## 3. Solution

Make every activity and event both **declare** and **load** properties through
one uniform path, preserving the constant, execution-local semantics.

### 3.1 Alternatives considered

- **(B) Consistent end-to-end for activities + events** — chosen (owner). Honors
  the BPMN taxonomy ("Activities and Events MAY contain Properties"); it removes
  debt rather than adding a concept.
- **(C) Activities only, drop event properties** — rejected by owner. Event
  properties currently have no built-in consumer, but restricting the taxonomy
  is a conception change; (B) keeps the model whole and ready for a future
  consumer (e.g. an expression referencing a property).
- **Per-type event configs** (a `PropertyAdder` config for each of intermediate
  catch/throw/boundary, mirroring start/end) — rejected in favour of central, in
  `newEvent`, so every event constructor gains the capability with one change.

### 3.2 Fix sites

#### 3.2.1 `NewUserTask` — route the property option

`pkg/model/activities/user_task.go` — add `data.PropertyOption` to the option
loop's task-options case so it flows into `utc.taskOpts → newTask → newActivity`,
where `activity.go` already dispatches it to `activityConfig` (a
`PropertyAdder`):

```go
case foundation.BaseOption, ActivityOption, taskOption, data.PropertyOption:
	utc.taskOpts = append(utc.taskOpts, opt)
```

#### 3.2.2 `newEvent` — collect property options from `baseOpts`

`pkg/model/events/event.go` — separate `data.PropertyOption`s from the base
options: apply them to the event's property set, pass the remaining options to
`flow.NewBaseNode`. This gives every event constructor that forwards `baseOpts`
(intermediate catch/throw, boundary) property support in one place; start/end are
unaffected (they still pass collected props positionally). `Event` gains an
`AddProperty` so it satisfies `data.PropertyAdder` for the collection step.

#### 3.2.3 `catchEvent.UploadData` — load properties

`pkg/model/events/event.go` — call `f.LoadProperties(ce.properties)` in
`catchEvent.UploadData`, mirroring `throwEvent.LoadData` (`event.go:467`) and
`task` (`task.go:209`), so a catch event's declared properties are materialized
in its execution frame.

#### 3.2.4 Reject value-less at construction

`pkg/model/data/property.go` — `NewProperty` / `NewProp` reject a property whose
`Value()` is `nil`, with a self-identifying error; `MustProperty` / `MustProp`
panic (the `Must*` contract). This is the **primary, fail-fast** guard: a
value-less property can no longer be constructed through the public API. A shared
helper performs the check so all constructor paths share it.

### 3.3 Relationship to FIX-016 / FIX-017 (rejection moves to the constructor)

Value-less **rejection** now lives at the property constructor (§3.2.4) — the
single, fail-fast source of truth. The clone-path error return from FIX-016 /
FIX-017 is **not** a value-less "backstop" to keep or revert: it is the honest
**precondition of `ItemAwareElement.Clone`** — cloning an item with no value
would nil-deref on `structure.Clone()`, so the method returns an error instead of
panicking. That precondition is a data-layer concern; it stays, covered at the
data layer (`IAE.Clone` / `Property.Clone` / `data.CloneProperties` on a
value-less / zero-value item).

Because no *node* can hold a value-less property once the constructor rejects one,
the per-node value-less-`Clone` tests (FIX-016/017 constructor-built, plus the
ones added in M1) are **removed** as meaningless; the node `Clone` error branches
remain as pure propagation of the data-layer error — landed and harmless, not
re-tested per node, not reverted. The `docs/backlog.md` item *"Property
configuration missing on some Activity/Event constructors"* and open question
**Q1** are both resolved.

## 4. Verification

### 4.1 Tests

| Test | Site | Asserts |
| ---- | ---- | ------- |
| `TestUserTaskAcceptsProperty` | `activities/clone_test.go` | `NewUserTask(..., data.WithProperties(p))` succeeds and `Properties()` returns it. |
| `TestIntermediateCatchEventAcceptsProperty`, `…Throw…`, `TestBoundaryEventAcceptsProperty` | `events/clone_test.go` | each constructor accepts `data.WithProperties` and exposes it via `Properties()`. |
| `TestCatchEventUploadDataLoadsProperties` | `events/event_data_test.go` | after a catch event's data step its property is materialized in the frame (`GetData`); all catch kinds share `catchEvent.UploadData`, so one test covers Start / IntermediateCatch / Boundary. |
| `TestPropCollector`, `TestNewEventPropertyOptionError`, `TestCatchEventUploadDataPropertyError` | `events/event_property_internal_test.go` | cover `newEvent`'s property-option collection (collector paths, a failing option, and the load error wrap). |
| `TestNewPropertyRejectsValueLess` | `data/property_test.go` | `NewProperty` / `NewProp` return an error and `MustProperty` / `MustProp` panic for a value-less property; a valued one still succeeds. |
| data-layer clone precondition | `data/property_test.go` (`TestCloneProperties`, `TestPropertyCloneIsDeepCopy`), `scope/frame_test.go` | `Property.Clone` / `data.CloneProperties` / `Frame.LoadProperties` return an error for a value-less (zero-value) item — the retained clone precondition. |

Removed as meaningless after §3.2.4 (value-less no longer constructible into any
node/process): every constructor-built value-less test from FIX-016/017 and M1
(`Test*CloneRejectsValueLessProperty`, `TestSnapshotNewRejectsValueless*`,
`TestCloneRejectsValueless*`), and the three `clone_internal_test.go` injection
files. The value-less precondition is now covered with zero-value `data.Property`
structs at the data/frame layer.

### 4.2 Gate

`make ci` green (tidy · lint · build · `-race` · diff-coverage ≥95% ·
govulncheck); examples smoke exit 0.

## 5. Prevention

Property support now flows through **one** path: constructors route
`data.PropertyOption` to a `PropertyAdder` config (activities via
`activityConfig`, events via `newEvent`'s collection), and every executing node
loads via `Frame.LoadProperties`. No per-type ad-hoc wiring remains, so a new
activity/event type inherits property support by construction.

## 6. Regressions / behavioural change

- **Wiring — additive.** `NewUserTask` / `NewIntermediateCatchEvent` /
  `NewIntermediateThrowEvent` / `NewBoundaryEvent` now accept
  `data.WithProperties` (previously errored); catch events now load their
  properties into the frame (previously ignored). No previously-accepted process
  changes behaviour.
- **Construction guard — behavioural change (§3.2.4).** `NewProperty` / `NewProp`
  now return an error, and `MustProperty` / `MustProp` now panic, for a
  value-less property that they previously accepted. No real usage constructs a
  value-less property (grep-confirmed), so this rejects only degenerate input;
  it is the intended fail-fast tightening.
- Event properties are now declarable and loaded (available in the frame as
  constants) even though no built-in element *reads* an event property yet — they
  are ready for a future consumer (e.g. an expression), consistent with the BPMN
  taxonomy.
- The FIX-016/017 clone-path error return is retained as the **data-layer clone
  precondition** (`IAE.Clone` can't clone a no-value item); value-less
  *rejection* now lives at the constructor, so the node `Clone` error branches
  are pure propagation, no longer property-triggered.

## 7. Related

- **FIX-017** — node-property clone isolation and value-less rejection; surfaced
  this two-axis wiring gap and covered the four then-unreachable branches with
  injection tests that FIX-018 supersedes.
- **ADR-010 v.2** — process data model: a property's structure *is* its value
  (immutable, typed), so a loaded property is a constant.
- **SAD-001 v.1 §14.1** — records the value-less deviation (unchanged).
- **BPMN 2.0** — §10.3.1 (property instantiation/accessibility: instantiated with
  the parent Flow Element), §13.3.2 (activity instance lifecycle). The
  constant-per-activation reading follows from the §10.3.1 instantiation clause.

## 8. Implementation summary

- **Commits (branch `fix/element-property-consistency`):** doc `b85c511`;
  **M1** `3772277` (declaration: UserTask option case + `newEvent` collection);
  **M2** `952c16d` (catch-event load); **M3** `23326d0` (construction guard +
  value-less test migration); `952fb62` (coverage of the new event branches).
- **Production (4 files):** `activities/user_task.go` (route `data.PropertyOption`
  into `taskOpts`); `events/event.go` (`propCollector` + `newEvent` option
  collection + `catchEvent.UploadData` `LoadProperties`); `data/property.go`
  (`rejectValueless` + wire into `NewProperty`) and `data/data_options.go` (wire
  into `newProperty`, used by `NewProp`/`MustProp`).
- **Tests:** accept tests for the four newly-wired constructors; a catch-event
  load test + collector/branch coverage; `TestNewPropertyRejectsValueLess`; the
  data/frame value-less precondition converted to zero-value `data.Property`;
  every constructor-built value-less test removed as unreachable, and the three
  `clone_internal_test.go` injection files deleted.
- **Verification:** `make ci` exit 0 — tidy · lint · build · `-race` ·
  **diff-coverage 100.0% of 55 changed lines** (min 95%) · govulncheck clean.
  All **18** example modules run end-to-end (exit 0) — no example uses a
  value-less property, so the construction guard breaks none.

## 9. Open questions

None. Semantics (execution-local constant, re-instantiated per activation) are
settled against BPMN §10.3.1; scope is (B) — consistent for all activities and
events, keeping the taxonomy whole. Value-less rejection is moved to the
constructor (§3.2.4), which **resolves backlog Q1** in the "reject at
construction" direction (option B); the clone-site rejection remains as a
defensive backstop.
