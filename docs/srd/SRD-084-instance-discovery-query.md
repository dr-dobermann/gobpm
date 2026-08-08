# SRD-084 — Compositional instance-discovery queries

| Field | Value |
|---|---|
| Status | Accepted (2026-08-08) |
| Date | 2026-08-08 |
| Owner | Ruslan Gabitov |
| Upstream | [ADR-013 v.2](../design/ADR-013-instance-observability.md) (the observability posture discovery serves) |
| Related | [SRD-019](SRD-019-instance-control-lifecycle.md) (the discovery surface this reshapes; frozen one-shot), [SRD-082](SRD-082-checkpoint-composite-fidelity.md) (which added the root/child axis and the handle linkage) |
| Tracking | #306 |

`InstanceFilter` is a single-axis enum stretched over two axes — lifecycle
(All/Running/Completed) and kind (Roots/Children) — so the axes cannot
compose: "the running children of instance X" is inexpressible, and callers
fetch a superset and post-filter by walking handles. This SRD replaces the
enum with a compositional query value, adds the missing `ProcessID`
discovery getters, and retires the enum's silent include-all on unknown
values. No ADR accompanies it: the change is API surface, and §4 records
the decision; the query shape is chosen to map onto the future server
listing API (#286) so the two are designed once.

## §1 Background (verified)

- **One enum, two axes.** `InstanceFilter` (`pkg/thresher/discovery.go:8-27`)
  carries `All`/`Running`/`Completed` (lifecycle) and `Roots`/`Children`
  (kind, added by SRD-082 FR-7) as flat alternatives; `Instances(filter)`
  (`discovery.go:37-73`) switches over them, so exactly one axis applies
  per call. "Running children" or "roots of process X" require fetching a
  superset and post-filtering by hand.
- **Unknown values silently include everything.** The `switch` has no
  default arm: `Instances(InstanceFilter(99))` returns every tracked
  instance — the invalid-input-silently-widens class the project's
  public-API validation rule exists to kill.
- **The process axis has no getter.** Neither `InstanceHandle` (its
  surface: `pkg/thresher/handle.go`) nor `instance.Instance` exposes the
  process id (`inst.s.ProcessID` is internal only) — a discovery consumer
  holding a handle cannot ask which process it runs, so a "instances of
  process X" predicate is not even post-filterable without correlating
  through `Registrations`.
- **The parent axis is post-filter only.** `InstanceHandle.ParentID()` /
  `CallNodeID()` (`handle.go:371-375`, cached at adopt per the #277 review
  round) exist, but "children **of X**" still means listing
  `InstancesChildren` and walking every handle.
- **Callers to migrate are few and in-repo.** Non-test callers:
  `examples/event-based-parallel-start/fulfill.go:68` and
  `examples/signal-start/observe.go:19` (both `InstancesAll`); the
  discovery tests (`pkg/thresher/discovery_test.go`,
  `call_recovery_test.go`'s separation test); six guides name the enum
  (`concepts/engine.md:118`, `concepts/architecture.md:156`,
  `concepts/entities.md:253`, `operating/instance-lifecycle.md:179`,
  `events/signal.md:176`, `subprocesses/call-activity.md:167`). The
  `consumer-smoke` CI step compiles an external module against the
  public API, so a missed public-surface break cannot land silently.
- **Origin.** #306 — the A2 finding of #277's independent review: the
  enum predates the linkage work and cannot compose with it.

## §2 Requirements

### §2.1 Functional

- **FR-1 — the query value.** `InstanceQuery` composes four independent
  predicates, ANDed:

  - `Kind InstanceKind` — `KindAny` (zero) | `KindRoots` | `KindChildren`;
  - `Stage InstanceStage` — `StageAny` (zero) | `StageRunning`
    (non-terminal) | `StageSettled` (Completed/Terminated — the ones
    `Forget` can release);
  - `ProcessID string` — exact process key; `""` = any;
  - `ParentID string` — exact parent instance id; `""` = any.

  The zero value `InstanceQuery{}` selects every tracked instance.
- **FR-2 — the compositional listing.**
  `Instances(q InstanceQuery) ([]string, error)` replaces
  `Instances(filter InstanceFilter) []string`. An out-of-range `Kind` or
  `Stage` is a classified, self-identifying error (`"Instances: unknown
  Kind …"`) — the silent include-all retires. A **contradictory but
  well-formed** query (`KindRoots` + a non-empty `ParentID`) is a valid
  empty intersection, not an error: set semantics answer honestly.
  Snapshot-consistency under the engine lock and unspecified order are
  unchanged.
- **FR-3 — the process getters.** `instance.Instance.ProcessID()` and
  `InstanceHandle.ProcessID()` (cached at `adopt`, like the SRD-082
  linkage — the process key is immutable after birth) complete the
  discovery linkage: every query axis is also readable off the handle.
- **FR-4 — the enum retires.** `InstanceFilter` and its five constants
  are **removed** (pre-1.0 breaking change, decided at scoping): every
  in-repo caller — the two examples, the discovery and separation tests —
  migrates to the query; `consumer-smoke` and the examples run keep the
  break honest.
- **FR-5 — docs follow.** The six guides above show the query form; the
  CHANGELOG records the breaking change under `[Unreleased] Changed`.

### §2.2 Non-functional

- **NFR-1** — no allocation growth on the hot path beyond the result
  slice (the predicates read fields already under the engine lock).
- **NFR-2** — race-clean under `-race`; diff-coverage ≥95% (aim 100%) on
  touched files.

## §3 Models

```go
// pkg/thresher/discovery.go (SRD-084)
type InstanceKind uint8

const (
    KindAny InstanceKind = iota
    KindRoots
    KindChildren
)

type InstanceStage uint8

const (
    StageAny InstanceStage = iota
    StageRunning
    StageSettled
)

// InstanceQuery selects tracked instances; predicates AND together,
// and the zero value selects everything.
type InstanceQuery struct {
    ProcessID string
    ParentID  string
    Kind      InstanceKind
    Stage     InstanceStage
}
```

**Worked example — "the running children of instance X".** Before: list
`InstancesChildren`, then `Instance(id)` each and drop terminals and
foreign parents by hand. After:

```go
ids, err := engine.Instances(thresher.InstanceQuery{
    Stage:    thresher.StageRunning,
    ParentID: parent.ID(),
})
```

One call, one lock acquisition; `Kind` may stay `KindAny` — a non-empty
`ParentID` only ever matches children. And each returned handle now
answers every axis itself: `State()`, `ProcessID()`, `ParentID()`.

## §4 Analysis & decisions

- **Replace the enum, don't wrap it** (scoping decision). 1.0 freezes
  whatever survives to it; a compatibility enum would be frozen debt on
  a five-caller surface the `consumer-smoke` gate migrates safely.
- **A struct, not functional options.** A query is a *value* — it wants
  to be built from config, compared, and mapped 1:1 onto the future
  server listing endpoint's query parameters (#286, the design-once
  intent recorded in the issue). Functional options shine for
  constructors with invariants, not for a four-field predicate.
- **Two small enums, not booleans.** `Roots bool` cannot say "any", and
  inverting (`ExcludeChildren`) reads worse; a three-valued axis is the
  honest shape, and the zero value lands on "any" — the least surprising
  default.
- **`Stage`, not `State`.** `InstanceState` already names the concrete
  lifecycle states; the query axis is coarser (running vs settled), and
  reusing the name would suggest per-state filtering this SRD
  deliberately does not add (the server can, later, from the same
  struct).
- **Errors for out-of-range, empty sets for contradictions.** An unknown
  `Kind`/`Stage` is caller error → classified refusal (the validation
  rule; the enum's silent include-all was precisely the
  invalid-input-widens defect class). `KindRoots + ParentID` is a
  well-formed intersection that is provably empty → `nil, nil` — a set
  API that errors on empty sets punishes composition.

## §5 API deltas

| Surface | Before | After |
|---|---|---|
| `Thresher.Instances` | `(InstanceFilter) []string` | `(InstanceQuery) ([]string, error)` |
| `InstanceFilter` + 5 constants | exported | **removed** |
| `InstanceKind`, `InstanceStage`, `InstanceQuery` | — | added |
| `InstanceHandle.ProcessID()` | — | added (cached at adopt) |
| `instance.Instance.ProcessID()` | — | added (internal) |

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | zero-value query (`pkg/thresher`) | FR-1/FR-2: `InstanceQuery{}` lists every tracked instance |
| T-2 | axis composition (`pkg/thresher`) | FR-1/FR-2: each axis alone and composed — running children of X; roots of process P; settled instances — against a mixed registry (roots, children, terminals) |
| T-3 | validation refusals (`pkg/thresher`) | FR-2: out-of-range `Kind` and `Stage` each return a self-identifying error |
| T-4 | contradictory query (`pkg/thresher`) | FR-2: `KindRoots` + `ParentID` → empty, no error |
| T-5 | the process getters (`pkg/thresher`) | FR-3: `InstanceHandle.ProcessID()` matches the launched process for roots and children |
| T-6 | migration proof (CI) | FR-4: `consumer-smoke` + the examples run compile and pass against the new surface |

## §7 Milestones

- **M1 — the query API + migration.** FR-1…FR-4; T-1…T-5.
  `feat(thresher): compositional instance-discovery queries replace InstanceFilter (SRD-084 M1)`.
- **M2 — docs.** FR-5; the six guides, CHANGELOG.
  `docs: the discovery query in guides and CHANGELOG (SRD-084 M2)`.

## §8 Cross-doc

- Upstream: **ADR-013 v.2** (no contract change there — discovery is an
  engine surface the ADR's posture motivates, not prescribes).
- Related: **SRD-019** (frozen one-shot — its enum description stays a
  historical snapshot), **SRD-082** (the axis and linkage this
  composes).
- **#306**: closes it. The #286 (S1) listing endpoint is expected to map
  its query parameters onto `InstanceQuery` — recorded here as the
  design-once intent, not a dependency.

## §9 Definition of Done

- [x] FR-1…FR-5 implemented; every §6 test exists and passes.
- [x] `make ci` green (incl. consumer-smoke and the examples run);
      diff-coverage ≥95% (aim 100%); touched functions ≥80%.
- [x] No in-repo reference to `InstanceFilter` remains (code or
      current docs; frozen one-shots excepted — the CHANGELOG names
      the old constants only as the migration map).
- [x] §10 filled.

## §10 Implementation summary

Landed on `feat/composite-followups` in two milestones — doc
`4edb51e`, M1 `4a83174` (the query API, both getters, the enum's
removal and the migration of all ten in-repo call-site files), M2
`e80add3` (the six guides + the CHANGELOG migration map).

Verification: `make ci` green end to end (exit 0), consumer-smoke
building an external module against the reshaped public API and all
49 examples running; **diff-coverage 97.7% of 133 changed coverable
lines** (min 95%) across the branch head, with `Instances`, `matches`,
`adopt` and both `ProcessID` getters at 100%; suites race-clean;
golangci-lint incl. tests 0 issues.

Deviations from the plan: none. `TestInstancesFilter` keeps its
SRD-019 canary name with a comment pointing here.

## Open questions

*None — §4 records the resolved design points (replace vs wrap, struct
vs options, the three-valued axes, error vs empty-set semantics).*
