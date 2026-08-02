# SRD-076 — `Lane` / `LaneSet` as a model-only carrier

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-08-02 |
| Owner | Ruslan Gabitov |
| Implements | [SAD-001](../design/SAD-001-vision-and-architecture.md) §14 (the library's §2.3.1 half; the model-only tier) and [conformance.md](../bpmn-spec/conformance.md) (Lane/LaneSet in scope, model-only) |
| Upstream | [ADR-024 v.3](../design/ADR-024-process-interchange-converters.md) §2.8 (the semantic round-trip this exists to make possible); [ADR-019 v.1](../design/ADR-019-definition-versioning.md) (definition vs instance) |
| Closes | [#261](https://github.com/dr-dobermann/gobpm/issues/261) |

## §1 Background

`Lane`/`LaneSet` is the engine's **only model-only element**: carried by the
definition, never executed. Neither type exists today, which is a gap in the
library's scope (`conformance-status.md` row 14) rather than a scope question.

**Why it is in scope at all, given lanes do nothing at run time.** Two
independent obligations, and only the second is about this library:

1. §2.3.1 lets a conformant tool ignore non-operational elements at execution.
   Lanes carry no token semantics — activities in lanes execute exactly as if no
   lanes existed — so the *runtime* genuinely may ignore them.
2. **"Non-operational" governs execution, not representation.** §2.3.2 obliges a
   tool to support import of Process diagrams, and
   [ADR-024 v.3](../design/ADR-024-process-interchange-converters.md) §2.8
   guarantees a **semantic round-trip**: "Import then Export yields a process
   that is structurally and semantically equivalent". Export can only reproduce
   what the model holds. A model with no lanes silently discards a modeller's
   organizational structure on the way through — the failure this document
   exists to prevent.

So the rule the scope list always stated — *engine MUST parse and preserve, MUST
NOT attach behaviour* — needs a place to preserve **into**. That place is the
definition model, and this SRD builds it.

**Normative shape** (Table 10.135, and the `LaneSet` entry beside it):

| Element | Own properties |
|---|---|
| `LaneSet` | `lanes: Lane [0..*]`, `name: string [0..1]` |
| `Lane` | `name: string [0..1]`, `partitionElement: BaseElement [0..1]`, `partitionElementRef: BaseElement [0..1]`, `childLaneSet: LaneSet [0..1]`, `flowNodeRefs: FlowNode [0..*]` |

Both inherit `BaseElement`. `laneSets: LaneSet [0..*]` hangs off
`FlowElementsContainer`, i.e. **`Process` and `SubProcess`**
(`docs/bpmn-spec/elements/process.md:22`, `elements/activities.md:410`).

Two normative constraints the spec states outright:

- *"A Lane element defines one specific partition in a LaneSet."*
- *"**All Lanes in a single LaneSet MUST define partition element of the same
  type**, e.g., all Lanes in a LaneSet reference a Resource as the partition
  element, but each Lane references a different Resource instance."*

The spec does **not** say a flow node may belong to only one lane, so this
document does not invent that rule.

## §2 Requirements

### Functional

**FR-1 — the two types.** A new package `pkg/model/lanes` provides `Lane` and
`LaneSet`, both embedding `foundation.BaseElement`, carrying exactly the
properties of Table 10.135 and its `LaneSet` sibling — nothing more.

**FR-2 — `name` is optional.** Both `name` attributes are `0..1` in the
standard, so an empty name is **accepted**, unlike the engine's flow-node
constructors which reject one. Lanes are a carrier; refusing a diagram because
a modeller left a lane unnamed would fail a conformant model
([preserve the standard's cardinality](../design/SAD-001-vision-and-architecture.md) §14).

**FR-3 — ordered, not keyed.** A container holds `[]*LaneSet` and a `LaneSet`
holds `[]*Lane`, in declaration order. Name-keying is impossible (names are
optional and need not be unique) and order is part of what a round-trip must
reproduce.

**FR-4 — nesting.** `Lane.childLaneSet` carries a nested `*LaneSet`, to
arbitrary depth, as the standard allows.

**FR-5 — node membership by reference, placed FROM the lane.** `Lane.flowNodeRefs`
holds `[]flow.Node` — the actual nodes, not ids — so export can emit their ids and
validation can check containment without a second lookup path.

Membership is established by a **`Lane.Place(nodes ...flow.Node)`** method, and
by no other route. **No element interface changes**: `flow.Node` gains nothing,
no node carries a lane id, and nothing on an element can be asked which lane it
is on. A lane is a modelling overlay laid *over* elements; the elements stay
unaware of it. `Place` is variadic, so a single element or a whole group goes on
in one call, and it may be called repeatedly as a model is assembled.

**FR-6 — the partition element is carried, not interpreted.** `partitionElement`
(embedded) and `partitionElementRef` (a reference by id) are both carried. gobpm
computes **no** partitioning from them: the standard describes them as what "a
BPMN compliant tool can determine the FlowElements" with, and this engine
determines nothing, because execution ignores lanes entirely.

**FR-7 — containers hold lane sets.** `Process` and `SubProcess` each accept
`WithLaneSets(...)` and expose `LaneSets()`.

**FR-8 — validated at registration.** A `ValidateLaneSets` check, wired into
both container `Validate()` hooks beside the existing validators, rejects:
- a `flowNodeRef` naming a node that is **not in the owning container**;
- a `LaneSet` whose lanes declare partition elements of **mixed types** (the
  Table 10.135 MUST);
- a nil `Lane` or nil nested `LaneSet`.

**FR-9 — never executed, never cloned.** Lane sets live on the **definition**
only. They are not copied into the per-instance node graph, no runtime type
reads them, and no engine decision consults them. A snapshot carries them
because a snapshot is a definition template; an instance does not.

### Non-functional

**NFR-1 — zero behavioural change.** Every existing process must register,
execute and complete exactly as before. Adding lanes to a process must not alter
its execution in any observable way — the engine's own definition of
"model-only".

**NFR-2 — round-trip ready.** The shape must be sufficient for the converter to
export what it imported, without this SRD building any converter support (that
lands with the converter's own slice).

**NFR-3 — no import cycle.** `lanes` may import `flow` and `foundation`;
`process` and `activities` import `lanes`. Nothing imports back.

**NFR-4 — the diff-coverage gate passes at `COVER_MIN`**, every touched
function at 100%.

## §3 Models

### §3.1 `pkg/model/lanes` (new)

```go
// LaneSet is one partitioning of a container's flow nodes (§10.8, Table 10.135's
// sibling). A container may declare several; a Lane may nest one.
type LaneSet struct {
    foundation.BaseElement

    name  string
    lanes []*Lane
}

// Lane is one partition within a LaneSet. It is carried and never executed:
// lanes have no token semantics, so the engine preserves them for interchange
// and attaches no behaviour (conformance.md, model-only tier).
type Lane struct {
    foundation.BaseElement

    name string

    // partitionElement / partitionElementRef say what this lane partitions by.
    // Carried verbatim, never interpreted — gobpm computes no partitioning.
    partitionElement    *foundation.BaseElement
    partitionElementRef string

    // flowNodeRefs are the container's nodes placed on this lane, via Place.
    // The reference is one-directional by design: a lane knows its elements, an
    // element never knows its lane (FR-5).
    flowNodeRefs []flow.Node

    // childLaneSet is the nested partitioning, if any.
    childLaneSet *LaneSet
}
```

Constructors follow the house shape — `NewLane` / `NewLaneSet` returning an
error, with `Must*` twins for tests and examples. Accessors: `Name()`,
`Lanes()`, `PartitionElement()`, `PartitionElementRef()`, `FlowNodes()`,
`ChildLaneSet()`, each returning a copy where the field is a slice.

Placement is a method on the lane:

```go
// Place puts nodes on the lane. A lane is a modelling overlay: it names the
// elements it covers, and the elements themselves are untouched and unaware —
// nothing is added to flow.Node and no element can be asked its lane (FR-5).
//
// Variadic, so one element or a whole group goes on in a single call, and it may
// be called repeatedly while a model is assembled. A nil node is refused; a node
// already on this lane is a no-op, so the call is safe to repeat.
func (l *Lane) Place(nodes ...flow.Node) error
```

### §3.2 Container wiring

`Process` and `SubProcess` each gain `laneSets []*lanes.LaneSet`, a
`WithLaneSets(...)` option in their existing option families, and a
`LaneSets()` accessor returning a copy.

### §3.3 Validation (`pkg/model/lanes/validation.go`)

```go
// ValidateLaneSets checks every lane set of a container against the nodes that
// container actually holds (FR-8). Lanes attach no behaviour, so this is the
// only place the engine looks at them.
func ValidateLaneSets(sets []*LaneSet, nodes []flow.Node) error
```

Called from `Process.Validate()` and `SubProcess.Validate()` beside
`ValidateResourceRoles` and `ValidateCompensationPlacement`. Nested lane sets
are checked recursively against the **same** container node set — a nested lane
partitions the same container, not a sub-container.

## §4 Analysis

### §4.1 Why a slice and not the name-keyed map every other collection uses

`Process` keys properties, roles, data objects and nodes by name, because each
has a required unique name. A `LaneSet` name is `0..1` and carries no uniqueness
rule, so a map would be unbuildable for the unnamed case and lossy for
duplicates. Order is also load-bearing here in a way it is not for the others:
lane order is visible in every diagram, and a round-trip that reorders lanes has
changed the model a human sees.

### §4.2 Why placement lives on the Lane and not on the element

The alternative — a `Lane()` accessor or a lane field on `flow.Node`, so an
element could report its own lane — was rejected. Lanes are a **modelling**
device with no execution semantics, and putting them on the element interface
would push a purely visual concern into the type every executing node
implements, where every future reader would have to determine that it means
nothing at run time. It would also invert the dependency: `flow` would need to
know about lanes, which are a higher-level container concern.

One direction only — the lane names its elements — keeps `flow.Node` untouched,
keeps the whole feature removable, and makes FR-9's "never executed" checkable
by inspection: no execution path can consult a lane, because no element offers
one.

### §4.3 Why nodes by reference rather than by id

Storing `[]string` would be closer to the XML, but it moves the containment
check to a lookup that can silently pass on a typo'd id, and it forces export to
re-resolve. Holding `flow.Node` makes FR-8's check a set-membership test and
makes a dangling reference impossible to construct in Go.

### §4.4 Why the partition element is carried but never interpreted

The standard is explicit that the partition element exists so "a BPMN compliant
tool **can determine** the FlowElements that have to be partitioned in this
Lane" — a capability, not an obligation. gobpm determines nothing, because
lanes have no execution semantics; `flowNodeRefs` already states the membership
directly. Carrying the field preserves round-trip; interpreting it would invent
behaviour for an element the engine is required to leave inert.

### §4.5 Why validation exists at all for an inert element

An element that changes no behaviour can still be *wrong* — a lane referencing a
node from a different process, or a lane set mixing partition-element types
against an explicit MUST. Because lanes never execute, such a model would
otherwise run perfectly and export garbage. Registration is the only moment the
error is visible, which is exactly the argument the value-less item-aware
element and the directory-mode role already rest on (SAD-001 §14.1).

## §5 API

| Symbol | Package | Note |
|---|---|---|
| `Lane`, `LaneSet` | `lanes` | new |
| `NewLane`, `NewLaneSet` (+ `Must*`) | `lanes` | new |
| `Lane.Place(nodes ...flow.Node)` | `lanes` | new — the ONLY way membership is established; no element interface changes |
| `Name`, `Lanes`, `PartitionElement`, `PartitionElementRef`, `FlowNodes`, `ChildLaneSet` | `lanes` | accessors |
| `ValidateLaneSets` | `lanes` | new |
| `WithLaneSets` | `process`, `activities` | new option |
| `Process.LaneSets()`, `SubProcess.LaneSets()` | `process`, `activities` | new accessors |

All additive. No existing signature changes.

## §6 Tests

| # | Test | Asserts | FR |
|---|---|---|---|
| T-1 | `TestNewLaneSet` | constructs with and without a name; lanes kept in declaration order | FR-1, FR-2, FR-3 |
| T-2 | `TestNewLane` | an unnamed lane is accepted; nil lane in a set refused | FR-2, FR-8 |
| T-3 | `TestLaneAccessors` | every carried field reads back; slices are copies, not aliases | FR-1, FR-5, FR-6 |
| T-3a | `TestLanePlace` | one node, a group in one call, repeated calls accumulate; a nil node refused; re-placing the same node is a no-op | FR-5 |
| T-4 | `TestLaneNesting` | `childLaneSet` round-trips to arbitrary depth | FR-4 |
| T-5 | `TestValidateLaneSets_foreignNode` | a `flowNodeRef` outside the container fails, error names lane and node | FR-8 |
| T-6 | `TestValidateLaneSets_mixedPartitionTypes` | a lane set mixing partition-element types fails (Table 10.135 MUST) | FR-8 |
| T-7 | `TestValidateLaneSets_nested` | a foreign node inside a **nested** lane set fails | FR-4, FR-8 |
| T-8 | `TestValidateLaneSets_accepted` | a well-formed set passes; an empty/absent set passes | FR-8 |
| T-9 | `TestProcessLaneSets` / `TestSubProcessLaneSets` | `WithLaneSets` accepted by both containers; `LaneSets()` returns them; a foreign node fails through `Validate()` | FR-7, FR-8 |
| T-10 | e2e `TestLanesDoNotAffectExecution` | the **same** process, once without lanes and once with every node laned, produces identical execution — same completion, same outputs | NFR-1, FR-9 |
| T-11 | `TestLaneSetsAreNotCloned` | the per-instance node graph carries no lane state; the definition still does | FR-9 |

T-10 is the load-bearing one: "model-only" is a claim about *behaviour*, and the
only way to assert it is to run the same process both ways and compare.

## §7 Milestones

| M | Scope | Commit |
|---|---|---|
| M1 | `pkg/model/lanes` — types, constructors, accessors (T-1…T-4) | one |
| M2 | `ValidateLaneSets` + both container wirings + options/accessors (T-5…T-9) | one |
| M3 | Execution-invariance proof + tracker/scope-doc updates (T-10, T-11) | one |

## §8 Cross-doc references

| Direction | Document | Why |
|---|---|---|
| up | [SAD-001](../design/SAD-001-vision-and-architecture.md) §14 | the model-only tier this implements |
| up | [ADR-024 v.3](../design/ADR-024-process-interchange-converters.md) §2.8 | the round-trip guarantee that makes carrying lanes necessary |
| up | [ADR-019 v.1](../design/ADR-019-definition-versioning.md) | definition vs instance, the basis for FR-9 |
| — | [conformance.md](../bpmn-spec/conformance.md) | the scope list declaring Lane model-only |

No downward references.

## §9 Definition of Done

1. FR-1…FR-9 implemented and wired; NFR-1…NFR-4 held.
2. Every §6 test exists and passes, including T-10's execution-invariance proof.
3. `make ci` green, including the examples half.
4. Diff-coverage at `COVER_MIN`; every touched function at 100%.
5. `conformance-status.md` row 14 flips ❌ → 🟡 (model-only, no execution wiring
   — the status vocabulary's exact meaning), and §1 gains the entry.
6. #261 closed by the landing PR.

## §10 Implementation summary

### §10.1 Milestones as landed (branch `feat/conformance-element-coverage`)

| Commit | Milestone |
|---|---|
| `af8eaf9` | this document |
| `b75814e` | **M1** — `pkg/model/lanes`: the two types, constructors, accessors, `Place` |
| `ed37e20` | **M2** — `ValidateLaneSets` + `WithLaneSets` + both container wirings |
| `90c5cd5` | **M3** — the execution-invariance proof; tracker row 14 ❌ → 🟡 |

### §10.2 Where reality diverged from the draft

Three, all found by writing the code rather than by review.

1. **The Table 10.135 same-type MUST moved from registration to construction**
   (M1). FR-8 listed it with the container checks, but a `LaneSet` sees all its
   lanes when it is built, so the violation is visible in the object itself —
   the same test that split ADR-020's two role refusals.

2. **That move exposed a flaw in §3.1's own shape.** `partitionElement` was
   typed `*foundation.BaseElement`, which would have made the check **vacuous**:
   every lane would carry the identical Go type, so no two could ever differ. It
   is now `foundation.Identifyer`, an interface, so distinct partition elements
   are distinct types and the constraint has something to compare. Had the check
   stayed at registration, the vacuity would probably have shipped unnoticed.

3. **Three unreachable guards, found by the coverage gate** (M2, final audit).
   `checkSet`'s nil-Lane guard, and `AddLaneSet`'s nil guard on **both**
   container configs. `NewLaneSet` refuses a
   nil lane, so a lane set cannot hold one by the time validation runs. Deleted
   rather than test-covered, per the standing rule about unreachable code. The
   nil-`LaneSet` guard in `ValidateLaneSets` was kept — that function is
   exported and a caller can hand it one directly. The `AddLaneSet` guards went
   for the same reason: `WithLaneSets` refuses a nil before calling, and both
   configs are unexported, so nothing else can reach them.

4. **`LaneSetAdder` no longer embeds `options.Configurator`.** It did, mirroring
   `RoleConfigurator` — but nothing here calls `Validate`, so the embed forced
   `subProcessConfig` to carry a stub that existed only to satisfy an interface
   requirement this SRD had invented. Dropping the embed deleted the stub
   instead of testing it.

Also worth recording: `WithLaneSets` **refuses** a nil lane set rather than
skipping it, unlike the variadic tolerance of `WithRoles` and `WithProperties`.
Silently dropping a whole partitioning is the exact loss this element exists to
prevent.

### §10.3 Verification

- `make ci` green, including the examples half.
- `make ci` reported **96.4% of 192 changed lines** on the first run — above the
  95% gate but below this landing's own standard. The shortfall was the three
  unreachable guards and the stub above; with those removed, every changed line
  is covered except the `Option()` marker, which `COVER_EXCLUDE` exempts by
  pattern (`func (…) Option() {}`).
- **Two claims are proven structurally rather than by test.** `git diff
  origin/master..HEAD -- pkg/model/flow/` is **empty**, so FR-5's "no element
  interface changes" is a property of the diff; and nothing under `internal/`,
  `pkg/thresher/`, `pkg/exec/` or `pkg/tasks/` imports `lanes`, so FR-9's "never
  executed" is a property of the import graph.
- 13 tests across §6. The load-bearing one is `TestLanesDoNotAffectExecution`:
  the same process run bare and fully laned — several lanes, a nested set, and a
  node deliberately on **two** lanes — completing identically.
  `TestLaneNodeIdentityIsNotReachableFromNodes` pins the one-directional rule
  structurally, so adding a `Lane()` accessor to an element would fail a test
  rather than quietly falsify the claim.

## Open questions

None.
