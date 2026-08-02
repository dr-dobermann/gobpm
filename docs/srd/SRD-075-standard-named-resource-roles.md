# SRD-075 — The standard-named resource roles, and `taskPriority`

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-08-02 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-020 v.3](../design/ADR-020-human-interaction-execution-model.md) §2.5.4 (the role kind, its resolution and composition, directory-mode rejection, activity-vs-process scope) and §2.11 (`taskPriority`) |
| Upstream | [ADR-032 v.1](../design/ADR-032-language-routed-expression-engines.md) (the expression evaluation seam a role's assignment expression rides); [ADR-011 v.7](../design/ADR-011-process-data-flow.md) (the data a role's expression reads); [ADR-013 v.2](../design/ADR-013-instance-observability.md) (the fact stream) |

## §1 Background

ADR-020 v.3 §2.5.4 decides that a declared `HumanPerformer` / `PotentialOwner`
becomes an **authorization source**, resolved at distribution through the path
the Camunda triad already uses and unioned into the eligible set. This document
lands that decision, plus §2.11's `taskPriority`.

The starting state is unusual and worth stating precisely, because it determines
how small the change is. The BPMN role model is **already modelled and already
reachable**, and is **nowhere executed**:

- `ResourceRole`, `ResourceAssignmentExpression` and `ResourceParameterBinding`
  exist (`pkg/model/hinteraction/resources.go:14`, `:102`, `:121`).
- They are declarable on any activity — `WithRoles(ress ...*hi.ResourceRole)`
  (`pkg/model/activities/role_option.go:25`) — and on a `Process`
  (`pkg/model/process/process.go:36`, `roles map[string]*hi.ResourceRole`).
- They are readable — `activity.Roles()` (`pkg/model/activities/activity.go:132`).
- They reach the embedder: `internal/instance/tasks.go:482` builds
  `interactor.TaskInfo{TaskRef: …, Roles: ht.Roles(), Eligible: inst.resolveEligibility(…)}`.

That last line is the whole defect in one expression. The roles ride along in the
announcement, while `Eligible` is resolved from the **triad only**
(`pkg/model/activities/user_task_authz.go:60` — `ResolveEligibility` reads
`ut.assignee` / `ut.candidateUsers` / `ut.candidateGroups` and nothing else). A
declared role is carried, surfaced, and never consulted.

Two further facts make the gap concrete rather than a matter of wiring:

1. **The assignment expression is typed to the non-executable variant.**
   `ResourceAssignmentExpression.Expression` is `data.Expression`
   (`resources.go:107`), a **struct** whose own doc-comment carries the spec's
   words: "These Expressions are not executable and are considered
   underspecified" (`pkg/model/data/expression.go:30`, `:38`). The engine
   evaluates `data.FormalExpression`, an **interface**
   (`pkg/model/expression/expression.go:33`, `data/expression.go:72`). So the
   field as declared could never have been evaluated by construction — this is
   not a missing call site but a type that cannot reach one.

2. **The role's own fields are write-only.** `ResourceRole` exposes exactly one
   accessor, `Name()` (`resources.go:93`). `resource`, `assignmentExpression` and
   `parameterBindings` are set by the constructor and readable by nothing.

The semantics being added, by contrast, already exist and are tested: an
`Assignment` resolves static ids or a `FormalExpression` to identifiers and
applies BPMN's failed-query rule (`pkg/model/hinteraction/assignment.go:114`),
and `Eligibility.permits` applies the verdict (`pkg/interactor/eligibility.go:106`).
This SRD adds a fourth resolved slot beside three that work.

## §2 Requirements

### Functional

**FR-1 — the role kind.** `ResourceRole` carries a `RoleKind` discriminator with
one typed constant per level of the BPMN chain (`RoleResource`, `RolePerformer`,
`RoleHumanPerformer`, `RolePotentialOwner`) and a `Kind()` accessor. Kind-specific
constructors `NewPerformer` / `NewHumanPerformer` / `NewPotentialOwner` (and
`Must*` twins) are added; `NewResourceRole` keeps producing the bare kind, so
every existing call site keeps its current meaning.

**FR-2 — the assignment expression becomes executable.**
`ResourceAssignmentExpression.Expression` is typed `data.FormalExpression`.
A nil expression is rejected at construction.

**FR-3 — Table 10.5's constraints are enforced at construction.** A role
carrying both a `resourceRef` and a `resourceAssignmentExpression` is refused
(this check exists at `resources.go:53` and is retained); a role carrying
`resourceParameterBindings` **without** a `resourceRef` is refused (new — the
spec's "only applicable if a `resourceRef` is specified" is currently
unenforced); an assignment expression carrying no expression is refused.

**FR-3a — an authorizing-kind role must name somebody.** A `HumanPerformer` or
`PotentialOwner` constructed with **neither** a `resourceRef` nor a
`resourceAssignmentExpression` is refused at construction: it resolves to the
empty set by construction, so it is declared authorization that authorizes
nobody — the defect this landing exists to remove (ADR-020 v.3 §2.5.4). A bare
`ResourceRole` or `Performer` still accepts a name-only role, where it is a
label rather than a broken promise.

**FR-4 — the role is readable.** `Resource()`, `AssignmentExpression()` and
`ParameterBindings()` accessors are added, so a role's contents can be inspected
by the resolver, the validator and an embedder.

**FR-5 — directory-mode roles of an authorizing kind are rejected at
registration.** A `HumanPerformer` or `PotentialOwner` carrying a `resourceRef`
is refused when the process is registered, by a validator wired into
`Process.Validate()` (`pkg/model/process/process.go:302`, which
`internal/instance/snapshot/snapshot.go:96` calls) **and** into
`SubProcess.Validate()` (`pkg/model/activities/subprocess.go:275`), which
recurses into its own children (`subprocess.go:462`) so every nesting level is
covered. The check spans roles declared on **nodes** and on the **container
itself**. The error names the offending role, the element carrying it, and the
missing directory subsystem. Declarative kinds (`RoleResource`,
`RolePerformer`) are **not** checked: they grant nothing whether or not they
resolve, so a directory-held resource named there is a conformant annotation
(ADR-020 v.3 §4, alternative 14).

**FR-6 — human-kind roles resolve at distribution.** `UserTask.ResolveEligibility`
resolves every `RoleHumanPerformer` / `RolePotentialOwner` role declared on the
task into a single additional resolved slot on `interactor.Eligibility`, using
the same expression engine, source and failed-query→empty-set rule the triad
slots use. Resolution happens **once**, at distribution, with the triad.

**FR-7 — a role identifier matches a user id or a group.** An identifier in the
role slot authorizes an actor when it equals `Actor.UserID()` **or** appears in
`Actor.Groups()`. This is the one respect in which role matching differs from a
triad slot, and it follows from the standard carrying no user-vs-group
discriminator on a `ResourceRole`.

**FR-8 — composition preserves the triad's precedence.** A declared `assignee`
remains the sole gate and excludes the role slot exactly as it excludes the
candidate slots. Otherwise an actor is authorized by a candidate slot **or** the
role slot. A task is **open** only when no triad member **and** no human-kind role
is declared.

**FR-9 — `taskPriority`.** A `UserTask` carries an integer priority, readable via
`TaskPriority()` and settable via a `WithTaskPriority(n)` option documented as an
engine extension. It is delivered to the distributor on `interactor.TaskInfo`,
the pre-authorization announcement already intended for "inbox routing/filtering"
(`pkg/interactor/taskview.go:19`). The engine reads it for no decision.

**FR-10 — process-level roles stay declarative.** Roles declared on a `Process`
are validated (FR-5) and carried, but contribute to no task's eligibility.

### Non-functional

**NFR-1 — no behaviour change for a model that declares no role.** Every existing
process must resolve, authorize and complete exactly as before. The role slot is
absent from the verdict unless a human-kind role is declared.

**NFR-2 — fail-closed is preserved.** `DeniedEligibility()` must continue to
authorize nobody, and a role whose expression fails must contribute an empty set
rather than an open task — the same rule the triad applies.

**NFR-3 — resolution stays single-shot.** No role is re-resolved after
distribution; the frozen-eligibility guarantee of ADR-020 §2.7 extends to the new
slot unchanged.

**NFR-4 — no reflection on the authorization path.** The kind is a typed
constant; classification is a comparison, not a type switch on four Go types.

**NFR-5 — the diff-coverage gate passes at `COVER_MIN`** (95% of changed lines),
with every touched function at ≥80% and the new ones at 100%.

## §3 Models

### §3.1 The role kind (`pkg/model/hinteraction/resources.go`)

```go
// RoleKind identifies which level of BPMN's ResourceRole chain a role occupies.
// The chain PotentialOwner → HumanPerformer → Performer → ResourceRole adds no
// attributes at any level (§10.3.4.1), so it carries exactly one bit: which role
// this is. The kind is that bit (ADR-020 v.3 §2.5.4).
type RoleKind string

const (
    RoleResource       RoleKind = "ResourceRole"
    RolePerformer      RoleKind = "Performer"
    RoleHumanPerformer RoleKind = "HumanPerformer"
    RolePotentialOwner RoleKind = "PotentialOwner"
)

// authorizingKinds are the role kinds that grant human authorization. A bare
// ResourceRole or a Performer may name a machine or an organization, which is why
// BPMN 2.0 introduced HumanPerformer beside the generic role.
var authorizingKinds = map[RoleKind]bool{
    RoleHumanPerformer: true,
    RolePotentialOwner: true,
}

// Authorizes reports whether a role of this kind contributes eligibility.
func (k RoleKind) Authorizes() bool { return authorizingKinds[k] }
```

The `ResourceRole` struct gains `kind RoleKind`, and `assignmentExpression`
becomes evaluatable:

```go
type ResourceRole struct {
    foundation.BaseElement

    name string
    kind RoleKind

    resource             *bpmncommon.Resource
    assignmentExpression *ResourceAssignmentExpression
    parameterBindings    []ResourceParameterBinding
}

type ResourceAssignmentExpression struct {
    foundation.BaseElement

    // Expression is the executable expression evaluated to resource identifiers.
    // It is a FormalExpression, not the natural-language data.Expression: BPMN's
    // Expression is "not executable and considered underspecified", so the engine
    // could never evaluate it (§4.1).
    Expression data.FormalExpression
}
```

### §3.2 The resolved slot (`pkg/interactor/eligibility.go`)

`Eligibility` gains one field; `ResolvedSlot` is reused unchanged.

```go
type Eligibility struct {
    Assignee        ResolvedSlot
    CandidateUsers  ResolvedSlot
    CandidateGroups ResolvedSlot

    // Roles is the union of every human-kind ResourceRole declared on the task,
    // resolved to identifiers. Unlike the triad slots it is NOT user-vs-group
    // typed: BPMN's role carries no discriminator, so an identifier here matches
    // either the actor's user id or one of its groups (ADR-020 v.3 §2.5.4).
    Roles ResolvedSlot
}
```

`Open()` gains `&& !e.Roles.Declared`. `permits()` gains one branch, placed
**after** the assignee gate so the existing early return at `eligibility.go:113`
gives FR-8's exclusion for free:

```go
func (e Eligibility) permits(actor hi.Actor) bool {
    if e.Open() {
        return true
    }

    if e.Assignee.Declared {
        return slices.Contains(e.Assignee.IDs, actor.UserID())
    }

    if e.CandidateUsers.Declared &&
        slices.Contains(e.CandidateUsers.IDs, actor.UserID()) {
        return true
    }

    if e.CandidateGroups.Declared &&
        intersects(e.CandidateGroups.IDs, actor.Groups()) {
        return true
    }

    // A role identifier may name a person or a group — the standard enumerates
    // both and distinguishes neither (Table 10.3), so both are checked.
    return e.Roles.Declared &&
        (slices.Contains(e.Roles.IDs, actor.UserID()) ||
            intersects(e.Roles.IDs, actor.Groups()))
}
```

`DeniedEligibility()` is unchanged: a declared assignee slot resolving to nobody
still short-circuits every other slot, including the new one.

### §3.3 Role resolution (`pkg/model/activities/user_task_authz.go`)

`ResolveEligibility` gains one slot, resolved from the activity's roles:

```go
func (ut *UserTask) ResolveEligibility(
    ctx context.Context,
    src data.Source,
    eng expression.Engine,
) interactor.Eligibility {
    return interactor.Eligibility{
        Assignee:        resolveSlot(ctx, ut.assignee, src, eng),
        CandidateUsers:  resolveSlot(ctx, ut.candidateUsers, src, eng),
        CandidateGroups: resolveSlot(ctx, ut.candidateGroups, src, eng),
        Roles:           resolveRoles(ctx, ut.Roles(), src, eng),
    }
}
```

`resolveRoles` collects the identifiers of every authorizing-kind role. It marks
the slot `Declared` when at least one such role exists — independently of what it
resolved to, matching `resolveSlot`'s existing rule that a declared-but-empty slot
authorizes nobody. A failed or nil expression contributes nothing, never an error:
BPMN treats a failed resource query as one returning an empty result set.

### §3.4 Registration validation (`pkg/model/activities/role_validation.go`, new)

```go
// ValidateResourceRoles rejects an authorizing-kind role that names its people
// through a directory query (ADR-020 v.3 §2.5.4): resourceRef resolution needs an
// Organizational Directory (§8.4.12 Resources) the engine does not provide, so
// such a role could only be carried and ignored — declared authorization that
// authorizes nobody. It is refused at registration instead, on the same principle
// as the value-less item-aware element (SAD-001 v.1.1 §14.1).
//
// Declarative kinds are not checked: they grant nothing whether or not they
// resolve, so a directory-held resource named there is a conformant annotation.
func ValidateResourceRoles(nodes []flow.Node, ownRoles []*hi.ResourceRole) error
```

Called from `Process.Validate()` beside `ValidateCompensationPlacement`
(`process.go:356`) and from `SubProcess.Validate()` (`subprocess.go:305`), the
container-agnostic pattern those validators already use. A container passes its
**own** roles as `ownRoles`; a Sub-Process passes `nil`, because its own roles
are already checked by the parent that holds it as a node — so no role is
reported twice. `Process` gains a `Roles()` accessor for this (it has the
`roles` field at `process.go:36` but no getter), mirroring `Properties()`.

### §3.5 `taskPriority` (`pkg/model/activities/user_task.go`, `pkg/interactor/taskview.go`)

`UserTask` gains `taskPriority int`, a `TaskPriority() int` reader, and a
`WithTaskPriority(int)` option in the existing UserTask option family.
`interactor.TaskInfo` gains `Priority int`, populated at
`internal/instance/tasks.go:482` beside `Roles` and `Eligible`.

## §4 Analysis

### §4.1 Why the expression type must change

BPMN models `FormalExpression` as a subtype of `Expression`, so a spec-faithful
`resourceAssignmentExpression: Expression` admits an executable expression. gobpm
has no such subtyping — `data.Expression` is a struct for the natural-language
variant and `data.FormalExpression` is the executable interface — so the faithful
field type in Go is the one the engine can evaluate. Keeping `data.Expression` and
adapting at the call site was considered and rejected: there is nothing to adapt,
because the struct carries no expression text or language, only a `BaseElement`.

The change is breaking in principle and inert in practice: the field has no
non-test reader anywhere in the repository (it is written by `NewResourceRole` and
read by nothing), so no working code can depend on its type.

### §4.2 Why one slot rather than two

A role could be split into user-identifiers and group-identifiers to mirror the
triad. That requires knowing which a resolved identifier is, and the standard does
not say — Table 10.3 enumerates "a specific individual, a group, an organization
role or position, or an organization" with no attribute recording the choice.
One slot matched against both halves of the actor's identity needs no such
knowledge, and the resulting permissiveness is confined to roles: a modeller
needing the distinction uses the triad, which exists for exactly that.

### §4.3 Why the role branch sits last in `permits`

Placing it after the candidate slots makes FR-8 structural rather than asserted:
the assignee gate's `return` already prevents any later slot from being consulted,
so "an assignee excludes roles too" needs no new code and cannot be forgotten in a
later edit. Ordering among the non-assignee slots is immaterial to the verdict
(they are OR-ed), so the position is chosen for the guarantee it makes free.

### §4.4 Why process-level roles stay inert

`Process.resources` and `Activity.resources` are the same type with different
scopes: Table 10.1 makes the first responsible for the **Process**, Table 10.3 the
second for the **Activity**. Treating a process-level `PotentialOwner` as
per-task eligibility would silently restrict every UserTask in the process the
moment a role is declared at the top — a much stronger claim than the standard
makes. They are still validated (FR-5), because a directory-mode role is
unsatisfiable wherever it is declared.

### §4.5 What the new slot means for `Claim` / `Unclaim` / `Reassign`

ADR-020 v.3 §2.5.4 requires the ownership operations to check nominees against
the **composed** set. This needs no new code: every one of them already routes
through `Eligibility.Authorize` — `pkg/thresher/tasks.go:227` (Claim),
`:332` (Reassign's nominee), `:384` (Unclaim), and `internal/instance/tasks.go:212`
— so extending `permits` extends all four at once. The requirement is realized
structurally, which is why no FR restates it.

One consequence must be stated, because it is a real asymmetry an implementer
will meet in the tests. `Reassign` checks its nominee through
`userIDActor(nomineeUserID)`, whose `Groups()` returns `nil`
(`pkg/thresher/tasks.go:422`) — an absent person's group membership cannot be
authenticated, only a present actor self-reports it. Therefore:

- a nominee eligible through a role identifier naming a **user** can be
  reassigned to (the identifier matches `UserID()`);
- a nominee eligible **only** through a role identifier naming a **group**
  cannot — the same limitation `candidateGroups` already has (ADR-020 §2.5.2),
  now reaching roles by the same mechanism and for the same reason.

This is the group-only-reassignment gap that M5 registers in SAD-001 §14.1. It is
not widened by this change; it is inherited, and the register now names roles
alongside `candidateGroups`.

### §4.6 M2a — closing FIX-026's `Must*` carve-outs (out of band)

This milestone is not about resource roles. It rides this branch because M1's
review surfaced it and the owner's rule is that questionable code is fixed where
it is found, not annotated.

`TestNoMustCallsInLibrary` (`internal/lintcfg`, FIX-026 §3.2.16) already bans
calling a panicking `Must*` constructor from library code — `pkg/` and
`internal/`, non-test. It carried two documented carve-outs: a `sanctioned` set
for the argless `MustBaseElement()` / `MustRecord()` literals, and a whole-file
exemption for `pkg/convert/bpmn/bpmn.go`'s `init()` registrations. Both were
justified in comments as structurally infallible.

The justification is the problem. "Provably infallible" is a claim about the
current call graph; the next change invalidates it silently, and the comment
keeps reading as settled. M2a removes the need for the carve-outs instead:

- **The total paths become total functions.** `NewBaseElement`, `NewRecord` and
  `NewMap` each have exactly one error source — the loop that validates their
  variadic input — so the no-argument path cannot fail. `foundation.
  EmptyBaseElement`, `values.EmptyRecord` and `values.EmptyMap` express that
  path and return no error. 15 call sites move to them.
- **`init()` gets somewhere to put its error.** `convert.RegisterImporterAtInit`
  / `RegisterExporterAtInit` replace the `MustRegister*` pair: a failure is
  recorded against the format and returned by `Import`/`Export` at first use.
  An embedder that never touches the format is no longer killed by a converter
  it does not use, and one that does gets a classified error rather than a load-
  time panic.
- **The guard becomes absolute** — `sanctioned` and `exemptFiles` are deleted,
  so no `Must*` call in `pkg/` or `internal/` non-test code passes.

`Must*` **declarations** remain, untouched and expected: they exist to simplify
tests and examples, which the guard does not walk. FIX-026 is an Accepted
one-shot document and is not retro-edited; the supersession is recorded in the
guard's own comment, where the next reader of that rule will meet it.

## §5 API

Added, all additive except the two noted:

| Symbol | Package | Note |
|---|---|---|
| `RoleKind`, `RoleResource`/`RolePerformer`/`RoleHumanPerformer`/`RolePotentialOwner`, `Kind()`, `Authorizes()` | `hinteraction` | new |
| `NewPerformer`, `NewHumanPerformer`, `NewPotentialOwner` (+ `Must*`) | `hinteraction` | new |
| `Resource()`, `AssignmentExpression()`, `ParameterBindings()` | `hinteraction` | new accessors |
| `ResourceAssignmentExpression.Expression` | `hinteraction` | **type change** `data.Expression` → `data.FormalExpression` (§4.1) |
| `Eligibility.Roles` | `interactor` | new field (struct literal without field names would break — none exists outside tests) |
| `TaskInfo.Priority` | `interactor` | new field |
| `ValidateResourceRoles` | `activities` | new |
| `Process.Roles()` | `process` | new accessor, mirroring `Properties()` |
| `UserTask.TaskPriority()`, `WithTaskPriority` | `activities` | new |

## §6 Tests

| # | Test (as landed) | Asserts | FR |
|---|---|---|---|
| T-1 | `TestRoleKindAuthorizes` (`hinteraction`) | only the two human kinds authorize; unknown and zero kinds do not | FR-1 |
| T-2 | `TestRoleConstructorsStampKind`, `TestRoleMustConstructors` | each constructor stamps its own kind; `NewResourceRole` stays bare | FR-1 |
| T-3 | `TestNewResourceRole` (subtests) | ref+expr refused; bindings without ref refused; each alone accepted; bindings ride along with a ref | FR-3 |
| T-4 | `TestNewResourceRole` / `TestNewResourceAssignmentExpression` | an assignment expression with no expression is refused; a nil `FormalExpression` is refused | FR-2, FR-3 |
| T-4a | `TestAuthorizingRoleMustNameSomebody` | both human kinds refused naming nobody; both declarative kinds still accept a name alone | FR-3a |
| T-5 | `TestResourceRoleAccessors` | the accessors return what was constructed, in both modes; bindings are copied, not aliased | FR-4 |
| T-6 | `TestValidateResourceRoles` (subtests) | a `resourceRef` `PotentialOwner` / `HumanPerformer` on a node fails; the error names role, element and the missing directory | FR-5 |
| T-7 | `TestProcessValidateResourceRoles` (`process`) | a container's **own** roles fail registration through `Process.Validate`; `Roles()` exposes them; a declarative role in the same shape passes | FR-5, FR-10 |
| T-8 | `TestValidateResourceRoles` (subtests) | expression mode accepted; **declarative** kinds in directory mode accepted; a role-less node skipped; every offending role reported | FR-5 |
| T-8a | `TestValidateResourceRolesNested` | a directory-mode `PotentialOwner` inside a nested Sub-Process fails through its own `Validate` hook | FR-5 |
| T-9 | `TestUserTaskResolveEligibilityRoles` | both human kinds populate the role slot; declarative kinds leave it undeclared and the task open; several roles union their identifiers | FR-6 |
| T-10 | `TestEligibilityRoleSlot` (`interactor`) | a role identifier authorizes by user id **and** by group; an actor matching neither is denied | FR-7 |
| T-11 | `TestEligibilityRoleSlot` (subtests) | a declared assignee excludes the role slot; the assignee still authorizes its own actor; roles union with the candidate slots | FR-8 |
| T-12 | `TestEligibilityOpenRequiresNoRole` | `Open()` is false with only a role declared, and that set is enforced; no member and no role stays open | FR-8 |
| T-13 | `TestEligibilityRoleSlot`, `TestDeniedEligibilityIgnoresRoles` | a declared role resolving to nobody denies; `DeniedEligibility` still denies with a role present | NFR-2 |
| T-14 | `TestUserTaskResolveEligibilityRoles` (subtests) | a failed role expression stays declared and denies; a nil engine leaves it unresolved; no role at all leaves the slot untouched | NFR-1, NFR-2 |
| T-15 | `TestUserTaskPriority`, `TestTaskPriorityReachesTheDistributor`, `TestUserTaskClone` | default zero, a set value, a negative value, last-setting-wins; the value reaches `TaskInfo` through a running engine; it survives `Clone` | FR-9 |
| T-16 | `TestPotentialOwnerAuthorizes` (`thresher`, e2e) | a UserTask declaring **only** a `PotentialOwner`: a stranger refused, a user-named and a group-named identifier both authorized, claim-then-strict-completion intact | FR-6, FR-8 |
| T-17 | `TestOwnershipAgainstRoleSlot` (`thresher`) | `Claim`/`Unclaim` work against the role slot; `Reassign` succeeds to a user-named role identifier and fails to a group-only one (§4.5) | FR-6, FR-8 |

## §7 Milestones

| M | Scope | Commit |
|---|---|---|
| M1 | Role kind, constructors, `FormalExpression` type change, Table 10.5 enforcement, accessors (T-1…T-5) | one |
| M2a | Close FIX-026's `Must*` carve-outs (out of band — see §4.6) | one |
| M2 | The "can never authorize" refusals: FR-3a at construction, `ValidateResourceRoles` + `Process`/`SubProcess` wiring, `Process.Roles()` (T-4a, T-6…T-8a) | one |
| M3 | `Eligibility.Roles`, `permits`/`Open`, `resolveRoles` (T-9…T-14, T-16, T-17) | one |
| M4 | `taskPriority` (T-15) | one |
| M5 | SAD-001 §14 registrations, the Table 8.49→10.4 extract correction, conformance-tracker rows, ADR-020 RU twin | one |

## §8 Cross-doc references

| Direction | Document | Why |
|---|---|---|
| up | [ADR-020 v.3](../design/ADR-020-human-interaction-execution-model.md) §2.5.4, §2.11 | the decision this implements |
| up | [ADR-032 v.1](../design/ADR-032-language-routed-expression-engines.md) | the engine that evaluates a role's assignment expression |
| up | [ADR-011 v.7](../design/ADR-011-process-data-flow.md) | the data source the expression reads |
| up | [ADR-013 v.2](../design/ADR-013-instance-observability.md) | the fact stream the registration failure is reported on |
| up | [SAD-001 v.1.1](../design/SAD-001-vision-and-architecture.md) §14.1, §14.2 | the deviation/extension register M5 updates |

No downward references: this document is referenced by none.

## §9 Definition of Done

1. FR-1…FR-10 (incl. FR-3a) implemented and wired; NFR-1…NFR-5 demonstrably held.
2. Every test in §6 exists and passes, including the e2e T-16.
3. `make ci` green — mock-check, link-check, tidy, lint, build, consumer-smoke,
   race tests, **diff-coverage at `COVER_MIN`**, govulncheck; plus the examples
   half (build **and** run).
4. Touched functions at ≥80% coverage, new ones at 100%, measured per-function
   on a committed tree (covercheck is HEAD-based).
5. `docs/design/conformance-status.md` §3 carries no ❌ row: the resource-model,
   `DataState`, `taskPriority` and group-only-reassignment rows read ✅ or 📐,
   with "Last verified" bumped.
6. SAD-001 §14.1/§14.2 carry the four new register rows.
7. `docs/bpmn-spec/semantics/instance-attributes.md` cites **Table 10.4** for the
   Activity instance attributes, carries its single `state` row, and no longer
   says `taskPriority` is unimplemented.
8. ADR-020's RU twin reflects v.3; ADR-020 flips Draft → Accepted and this
   document Draft → Accepted, after `/check-srd` passes.

## §10 Implementation summary

### §10.1 Milestones as landed (branch `feat/conformance-closeout`)

| Commit | Milestone |
|---|---|
| `d38f9ca` | ADR-020 v.3 — the decision |
| `c76b4bb` | this document |
| `5cec2bd` | **M1** — `RoleKind`, the kind constructors, the `FormalExpression` type change, Table 10.5 enforcement, the accessors |
| `b8c8ab9` | spec correction — the refusals scoped to the authorizing kinds, FR-3a added |
| `f219fe3` | **M2a** — every `Must*` call site removed from library code (§4.6) |
| `14eee90` | **M2** — FR-3a at construction, `ValidateResourceRoles`, `Process.Roles()` |
| `2f561cb` | **M3** — `Eligibility.Roles`, the verdict branch, `resolveRoles` |
| `b82807e` | **M4** — `taskPriority` |
| `bcc5c35` | **M5** — SAD-001 §14 registrations, the Table 8.49→10.4 erratum, the tracker, the RU twin |

### §10.2 Where reality diverged from the draft

Five things this document did not foresee. All were found by the flow rather
than by review, and each is worth recording because the *reason* it was missed
generalizes.

1. **The refusals had to be scoped, and one was missing entirely** (`b8c8ab9`).
   §2.5.4 said directory mode "is rejected at registration" without naming which
   kinds; scoping it to the authorizing kinds followed from its own rationale
   (silent non-authorization, which a declarative role cannot commit). The
   mirror case — an authorizing role with **neither** mode — was in neither
   document, and it is the same defect in a cheaper disguise. It became FR-3a,
   refused at construction. *Found by:* grounding the M2 plan against the code
   and noticing that both existing role-using tests build exactly that shape.

2. **`NewUserTask` rejected `RoleOption`** (`2f561cb`). Its dispatch listed four
   option families and `WithRoles` was in none, so a declared `HumanPerformer`
   or `PotentialOwner` could not reach the one task type whose eligibility it
   decides — the feature was unreachable from a UserTask, and every layer below
   it was already correct. *Found by:* a resolution test failing to construct
   its fixture. Nothing in §3's shapes would have caught it, because the model
   layer was right.

3. **`UserTask.Clone` dropped `taskPriority`** (`b82807e`). The engine runs each
   instance on a cloned node graph, so a value the distributor reports must
   survive the copy; without it every task announced a priority of 0 while the
   model said otherwise. *Found by:* the end-to-end delivery test failing on its
   first run. A unit test on the model object passes either way — this is the
   FIX-002 lesson (run the artifact) reaching a field assignment.

4. **`ResourceAssignmentExpression.Expression` was unevaluatable by type**
   (`5cec2bd`), which §1 recorded, but the consequence for *tests* was not
   planned: the existing "by assignment expression" case asserted success with a
   nil expression, so FR-2 turned a green assertion red by design.

5. **Growing `Eligibility` moved an unrelated linter verdict** (`2f561cb`). One
   extra `ResolvedSlot` pushed `loopState.cleanupTask` over gocritic's
   `rangeValCopy` threshold. A struct that other code ranges over by value has a
   size budget, and adding a field spends it.

Also landed out of band: **M2a** (§4.6), which was not in the original milestone
plan at all.

### §10.3 Verification

- `make ci` green — mock-check, link-check, tidy, lint, build, consumer-smoke,
  race tests, diff-coverage, govulncheck, plus the examples half (build **and**
  run).
- **diff-coverage: 100.0% of 217 changed coverable lines** (gate: 95%).
- Every function this landing created or updated finishes at **100%** — including
  `UserTask.Clone`, whose `return nil, err` from `ut.clone()` had been uncovered
  since before this landing. It is reachable: a value-less `Property` cannot be
  deep-copied (FIX-017), and the zero-value struct is the only way to hold one,
  so a UserTask built with `data.WithProperties(&data.Property{})` drives the
  branch (`TestUserTaskCloneRejectsUnclonableProperty`).
- 18 tests added across the §6 matrix (T-1…T-17, plus T-4a and T-8a from the
  spec correction, and the Clone error-path test above).

## Open questions

None.
