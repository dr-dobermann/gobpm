# SRD-057 — Link events: intra-process GOTO by static name-pairing

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-07-20 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-006 v.4](../design/ADR-006-events-and-subscriptions.md) §2.8 (Link events — intra-process GOTO by static name-pairing); GitHub epic #90 |
| Upstream | [ADR-001 v.6](../design/ADR-001-execution-model.md) §4 (the single-writer loop the redirect runs on), [ADR-009 v.1](../design/ADR-009-per-instance-node-graph.md) (the per-instance clone the resolved edge rides), [ADR-005 v.4](../design/ADR-005-gateways-and-joins.md) §2.12 (Link excluded as a gateway arm) |
| Refines | SRD-026 (signal events — the name-keyed sibling; this SRD **retires** the `SubscriptionKey()` deferral SRD-020/026 parked, ADR-006 v.4 §2.8) — by number, sideways |

Note on numbering: SRD-055 (sequential MI) and SRD-056 (parallel MI) are
reserved by SRD-054 for the Multi-Instance slices; Link events take **057**. It
lands ADR-006 v.4 §2.8; on landing, ADR-006 v.4 flips Draft→Accepted.

## §1 Background

The model layer carries only a **bare, unwired** `LinkEventDefinition`
(`pkg/model/events/link.go`): a struct with exported fields (`Target`, `Name`,
`Sources`) embedding `definition`, with **no** constructor, **no** `Type()`,
**no** `Name()` getter, and **no** `var _ flow.EventDefinition` assertion — so
it does not satisfy `flow.EventDefinition` (`pkg/model/flow/events.go:53-64`),
and the exported `Name` field would collide with a `Name()` method. The trigger
constant `flow.TriggerLink` exists (`pkg/model/flow/events.go:50`) but is
referenced nowhere. Link is **absent** from both position allow-lists —
`intermediateThrowTriggers` (`intermediate_throw.go:20-25`) and
`intermediateCatchTriggers` (`intermediate_catch.go:18-23`) — so a Link
throw/catch cannot even be constructed today. There is no runtime path.

ADR-006 v.4 §2.8 settles the conception this SRD lands: **Link is not a wait
node.** A Link catch (target) is a flow entry point (no incoming sequence flow);
a Link throw (source) is a flow exit (no outgoing sequence flow) that
**redirects** the token to the same-name target's outgoing flow. Pairing is
**static** — by `name`, within one flow-elements container, many sources → one
target — resolved once at snapshot build and validated fail-fast at
registration. No subscription, no `EventHub`, no waiter.

This maps cleanly onto the runtime: a node's `Exec` returns
`([]*flow.SequenceFlow, error)` and the runner advances the token to each
returned flow's `Target().Node()` (`internal/instance/track.go:1015` →
`checkFlows:1095-1118`). **A Link throw returns the resolved target catch's
`Outgoing()`** — the token proceeds down the target's downstream path in one
hop, with **zero runner changes** and the catch node bypassed (it is a label,
never independently reached).

## §2 Requirements

### §2.1 Functional

- **FR-1 — a first-class `LinkEventDefinition`** (`pkg/model/events/link.go`,
  rewritten). Mirror `signal.go`/`terminate.go`: unexported `name`, a
  `NewLinkEventDefinition(name string, baseOpts ...options.Option)` constructor
  that `errs.CheckStr`-validates a non-empty name (`signal.go:26-34` pattern),
  `Type() → flow.TriggerLink`, a `Name()` getter, and
  `var _ flow.EventDefinition = (*LinkEventDefinition)(nil)`. The metamodel
  `Target`/`Sources` ref fields are **dropped** — gobpm pairs by **name at the
  container** (§4.1), not by object reference, so they carry no runtime meaning
  (a `MustLinkEventDefinition` panic-twin follows the house convention).

- **FR-2 — Link accepted at the two intermediate positions only.** Add
  `flow.TriggerLink` to `intermediateThrowTriggers` (`intermediate_throw.go:20`)
  and `intermediateCatchTriggers` (`intermediate_catch.go:18`). No other
  position gains it: Start (`start.go` `startTriggers`), End, and boundary
  (`boundary.go`) allow-lists are untouched, so a Link trigger there is rejected
  at construction — realizing ADR-006 §2.8's position table (boundary/start/end
  invalid, `event-handling.md`/`conformance.md`).

- **FR-3 — per-container pairing validation at registration**
  (`Process.Validate`, `process.go:220-290`; `SubProcess.Validate`,
  `subprocess.go:111-131`). A cross-node pass mirroring the existing conditional
  start-placement check (`process.go:264-283`) collects the Link throw and catch
  nodes **of this container only** (`Nodes()` is a flat single-level list,
  `container.go:171-179`; a nested `SubProcess` is one opaque node — so the pass
  never crosses levels, honoring "cannot link a parent Process with a
  Sub-Process", §10.5.1). For each Link **name** it enforces, fail-fast via the
  accumulated `errors.Join` (`process.go:285-287`):
  - **exactly one target** (catch) — zero targets with ≥1 source → error (throw
    into the void); two+ targets → error (ambiguous destination);
  - **≥1 source** (throw) — a lone target with no source is a dead label →
    error;
  - names are matched within the container; a source and target of the same name
    in **different** containers do **not** pair (each is validated in its own
    container, so a cross-level pair surfaces as "no target"/"no source" on each
    side).

- **FR-4 — static throw→target resolution at graph-wiring time**
  (`pkg/model/flow/`). Rather than a snapshot-stored `LinkTargets` map (the
  original sketch — top-level only, and it cannot reach a nested Sub-Process
  without a `flow`→`events` cycle), the resolution rides the **one wiring
  implementation for every level**: `flow.WireClonedGraph` gains a step 4,
  `resolveLinkEdges(clonedNodes)` (`flow/link.go`), that pairs each Link throw
  source to its same-name catch target **within that container's node set** and
  records the resolved target on the throw. `WireClonedGraph` runs at the
  top-level snapshot (`snapshot.New`/`Clone`) **and** inside each Sub-Process
  inner-graph clone (`ElementsContainer.CloneGraph`), so nested-container Links
  resolve **for free**, confined to their level, per instance. Two small `flow`
  capability interfaces carry it: `LinkEventNode { LinkName() string;
  IsLinkSource() bool }` (throw + catch) and `LinkSource { LinkEventNode;
  SetLinkTarget(Node) }` (throw), implemented by the events nodes — so `flow`
  needs no dependency on `events`.

- **FR-5 — the Link throw redirects to its target's outgoing flows.** A Link
  throw's `Exec` (`intermediate_throw.go`) returns **the resolved target catch's
  `Outgoing()`** (via the `linkTarget flow.Node` field `resolveLinkEdges` set at
  wiring) instead of emitting any definition or returning its own (empty)
  outgoing set. It emits **no** hub event (no `emitDefinition`/`PropagateEvent`)
  — the ADR-006 §2.8 divergence from the generic throw path. An unresolved Link
  throw (no target — impossible past registration validation) fails loud rather
  than nil-deref.

- **FR-6 — the redirect is re-entrant (on-page loops).** Link's canonical use
  is a loop back to an earlier point. Reaching a target, flowing, and reaching
  the same-name throw again redirects cleanly, the token following the normal
  per-track lifecycle (`track.go` `checkFlows` self/cyclic-flow handling
  `:1103-1109`) with no residue.

### §2.2 Non-functional

- **NFR-1 — zero runner change.** The redirect rides the existing
  `Exec → checkFlows` contract; `internal/instance/track.go` is not modified.
- **NFR-2 — no new concurrency.** The redirect is applied by the single loop
  goroutine (ADR-001 v.6 §4); no waiter, no hub registration, no lock.
- **NFR-3 — additive.** No landed event kind changes shape; the two allow-list
  additions and the snapshot field are purely additive.
- **NFR-4 — the CI gate.** `make ci` green; every touched function ≥80% (aim
  100%) diff-coverage per the project standard.
- **NFR-5 — fail-fast, never silent.** An unmatched/duplicate/cross-level Link
  is a **registration error** (before any instance runs), never a runtime hang —
  the deterministic counterpart of the SRD-048 silent-conditional-hang the event
  layer just closed.

## §3 Models

### §3.1 `LinkEventDefinition` (rewritten: `pkg/model/events/link.go`)

```go
// LinkEventDefinition is the intra-process GOTO connector (ADR-006 v.4 §2.8):
// a source Intermediate Throw hands control to the same-name target
// Intermediate Catch within one Process level. Pairing is by name, resolved
// statically at snapshot build (§3.4) — the definition carries only the name.
type LinkEventDefinition struct {
	definition
	name string
}

// NewLinkEventDefinition builds a Link definition with a required non-empty
// name (the pairing key). An empty name is a classified error.
func NewLinkEventDefinition(
	name string, baseOpts ...options.Option,
) (*LinkEventDefinition, error) {
	name = strings.TrimSpace(name)
	if err := errs.CheckStr(
		name, "a Link event requires a name", errorClass); err != nil {
		return nil, err
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &LinkEventDefinition{definition: *d, name: name}, nil
}

// Type identifies the trigger — flow.TriggerLink (flow/events.go:50).
func (*LinkEventDefinition) Type() flow.EventTrigger { return flow.TriggerLink }

// Name is the pairing key.
func (l *LinkEventDefinition) Name() string { return l.name }

var _ flow.EventDefinition = (*LinkEventDefinition)(nil)
```

### §3.2 Trigger allow-list deltas (`intermediate_throw.go`, `intermediate_catch.go`)

```go
// intermediate_throw.go:20 — add flow.TriggerLink
var intermediateThrowTriggers = set.New(
	flow.TriggerCompensation, flow.TriggerEscalation,
	flow.TriggerMessage, flow.TriggerSignal, flow.TriggerLink)

// intermediate_catch.go:18 — add flow.TriggerLink
var intermediateCatchTriggers = set.New[flow.EventTrigger](
	flow.TriggerConditional, flow.TriggerMessage,
	flow.TriggerSignal, flow.TriggerTimer, flow.TriggerLink)
```

### §3.3 Per-container pairing validation (`process.go`, `subprocess.go`)

A shared helper (e.g. `events.ValidateLinkPairing(nodes []flow.Node) error`)
collects Link throw/catch nodes by `Type()==flow.TriggerLink` + event class,
buckets by `Name()`, and returns a joined error for every name that is not
exactly-one-target-and-≥1-source. `Process.Validate` calls it over `p.Nodes()`
(after the existing L264-283 placement check); `SubProcess.Validate` calls it
over its container's `Nodes()` — each call is naturally single-level.

### §3.4 Graph-wiring resolution (`pkg/model/flow/link.go` + `container.go`)

```go
// pkg/model/flow/link.go — the events nodes implement these, so flow needs no
// dependency on events.
type LinkEventNode interface {
	Node
	LinkName() string   // "" when the node carries no Link definition
	IsLinkSource() bool // true = throw source, false = catch target
}

type LinkSource interface {
	LinkEventNode
	SetLinkTarget(target Node)
}

// resolveLinkEdges pairs each Link throw to its same-name catch within a node
// set and records the target. Runs as WireClonedGraph step 4, so it fires at
// every container level (top-level snapshot + each Sub-Process inner graph).
func resolveLinkEdges(nodes map[string]Node) { /* bucket by name; SetLinkTarget */ }
```

`WireClonedGraph` calls `resolveLinkEdges(clonedNodes)` after its flow/default/
boundary rewiring (`container.go`), so `snapshot.New`, `snapshot.Clone`, and
`ElementsContainer.CloneGraph` all pick it up with **no snapshot-struct field**
and **no per-caller change**.

### §3.5 Link throw redirect (`intermediate_throw.go` `Exec`)

```go
// in Exec: a Link throw redirects instead of emitting.
if name, ok := linkDefName(ite); ok { // carries a LinkEventDefinition
	if ite.linkTarget == nil {         // unresolved — fail loud, no nil-deref
		return nil, errs.New(errs.M("… Link %q has no resolved target catch", name), …)
	}
	return append([]*flow.SequenceFlow{}, ite.linkTarget.Outgoing()...), nil
}
// … existing emit-definitions path for non-Link throws …
return append([]*flow.SequenceFlow{}, ite.Outgoing()...), nil
```

`ite.linkTarget flow.Node` is set by `resolveLinkEdges` at wiring time (§3.4);
`Outgoing()` is the target catch's own downstream, so the token advances past
the (bypassed) catch.

## §4 Analysis & decisions

### §4.1 Pair by name at the container, not by object reference

The BPMN metamodel gives `LinkEventDefinition` `source`/`target` object refs,
but gobpm resolves pairing **by name within a container** and drops the ref
fields. *Why:* the ADR decides name-pairing (§2.8); a name + the container's
flat node list is sufficient and avoids a second source of truth that could
disagree with the names. *Alternative — keep the refs and resolve by them:*
redundant with the name, and the refs would need re-wiring across clones for no
gain. If the BPMN-XML converter (SRD-051) later needs the refs, it can add them
without touching this seam. Rejected.

### §4.2 Static resolution in the snapshot, not a runtime lookup

Pairing is known at definition time and never changes, so it is resolved
**once** at `snapshot.New` (a `map[throwID]catchID`) and the per-instance edge
is wired at `Clone` — mirroring how sequence flows are re-linked between clones
(`WireClonedGraph`). *Alternative — resolve by name at each throw fire:* a
per-fire container scan for zero benefit; the pairing is immutable. Rejected.
*Alternative — hold the resolution on the `renv`/instance and look up at Exec:*
couples the throw to a node-lookup surface; wiring a resolved node reference at
clone keeps `Exec` a one-liner and the runner untouched (NFR-1). Rejected.

### §4.3 The throw returns the target's outgoing flows; the catch is bypassed

The runner advances the token to each returned flow's `Target().Node()`
(`checkFlows:1114-1118`). A Link throw returning the **target catch's
`Outgoing()`** lands the token on the nodes downstream of the target in one hop.
The catch node's own `Exec` is **not** run — it is a pure label with no incoming
flow, never independently reached. *Why bypass:* the ADR models the catch as a
flow entry point, not a step; running it would add a no-op node visit (and a
spurious node-lifecycle fact) for nothing. *Alternative — route the token
through the catch node (return one synthetic flow to the catch):* needs a
synthetic flow the model doesn't have and gains only a redundant node visit.
Rejected — but the SRD notes observability may later emit a lightweight "link
followed" fact from the throw if a consumer needs it.

### §4.4 Fail-fast at registration, not at runtime

An unmatched/duplicate/cross-level Link is caught by `Process.Validate` at
registration (before any instance), per ADR-006 §2.8. *Why:* it is
deterministic and always wrong — unlike a signal broadcast with no listener
(legitimately a no-op). Catching it at registration turns a would-be runtime
hang/panic into a clear model error — the SRD-048 anti-silent-hang posture, one
layer earlier. Rejected alternative — a runtime "no target" error at fire: it
lets a broken model register and fail mid-run.

### §4.5 Link retires the `SubscriptionKey()` generalization

SRD-020/026 deferred unifying name-keyed hub matching "until Link — the second
name-keyed event." ADR-006 §2.8 retires that premise: Link is a **static
redirect**, not a runtime name-matched **subscription**, so it never touches the
hub's matching path. Signal stays the only name-keyed subscription; the
generalization has one consumer and stays unjustified. This SRD does **not**
implement it; `docs/backlog.md` is already re-scoped (this branch).

## §5 API / contract summary

| Surface | Change | Compatibility |
|---|---|---|
| `events.LinkEventDefinition` | rewritten: `NewLinkEventDefinition`/`Must…`, `Type()`, `Name()`, assert | the stub had no constructor/consumers — no break |
| `events.NewIntermediateThrowEvent` / `…CatchEvent` | accept `TriggerLink` | additive (previously rejected) |
| `Process.Validate` / `SubProcess.Validate` | new per-container Link pairing check | additive; rejects a previously-unconstructable model |
| `snapshot.Snapshot.LinkTargets` | new immutable field | additive |
| `internal/instance/track.go` | none (NFR-1) | — |
| observability | none | a Link redirect emits no new fact kind |

## §6 Test scenarios

| # | Test (location) | Covers |
|---|---|---|
| T-1 | `TestLinkEventDefinition` (`events/link_test.go`) | FR-1: ctor name-required (empty/blank rejected), `Type()==TriggerLink`, `Name()`, `Must…` panic; `edef_test.go` table row |
| T-2 | throw/catch accept Link (`events/intermediate_throw_test.go`, `intermediate_catch_test.go`) | FR-2: a Link throw and a Link catch construct; boundary/start/end still reject Link |
| T-3 | `TestValidateLinkPairing` (`process/process_test.go`, `activities/subprocess_test.go`) | FR-3: one-source→one-target OK; no-target-with-source, two-targets, lone-target, and cross-level-pair all error at `Validate`; a sub-process's Link namespace is isolated from the parent's |
| T-4 | snapshot resolution (`internal/instance/snapshot/snapshot_test.go`) | FR-4: `LinkTargets` computed per container; shared by `Clone`; per-instance throw→catch clone reference wired |
| T-5 | throw redirect (`internal/instance/*_test.go` or thresher) | FR-5: a Link throw's `Exec` yields the target catch's outgoing flows; emits no hub event; the catch node's `Exec` is not run |
| T-6 | `TestLinkThrowToCatch` + `TestLinkOnPageLoop` (`pkg/thresher/link_test.go`) | FR-5/FR-6 e2e: `start → A → throw"L"` and `catch"L" → B → end` completes with B run; an on-page loop (`throw` back to an earlier `catch`) runs N times then exits and settles `Completed`; multi-source (two throws → one target) both reach the target's downstream |
| T-7 | example smoke (§7 M3) | `examples/link-events/` exits 0 under timeout with the expected output |

## §7 Milestones

Each milestone is one commit, `make ci` green (the SRD-030 leaner event-landing
shape — Link is model + static resolution, no waiter):

- **M1 — model + positions + validation.** FR-1 (rewrite `link.go`), FR-2 (both
  allow-lists), FR-3 (`Process.Validate` + `SubProcess.Validate` pairing check);
  tests T-1, T-2, T-3. `feat(events,process): Link event definition, positions,
  pairing validation`.
- **M2 — graph-wiring resolution + throw redirect.** FR-4 (`flow.LinkSource`/
  `LinkEventNode` + `resolveLinkEdges` as `WireClonedGraph` step 4), FR-5 (Link
  throw `Exec` redirect), FR-6 (re-entrant); tests T-4, T-5.
  `feat(flow,events): static Link resolution at graph wiring + throw redirect`.
- **M3 — e2e + example + doc sync.** T-6 (`pkg/thresher/link_test.go`), T-7
  (`examples/link-events/`), changelog, conformance-tracker row, README + data/
  events guide sync. `feat: Link events — e2e, example, front-door sync`.

Post-M3: `/check-srd`, §10 fill, SRD status flip, **ADR-006 v.4 →
Accepted + RU twin**, sync linked docs (roadmap C6 row, epic #90, the
`SubscriptionKey` backlog note already re-scoped).

## §8 Cross-doc

- Implements **ADR-006 v.4 §2.8** (the Link conception); on landing, ADR-006 v.4
  flips Draft→Accepted and gains its RU twin.
- Upstream: **ADR-001 v.6 §4** (single-writer loop), **ADR-009 v.1** (per-
  instance clone), **ADR-005 v.4 §2.12** (Link excluded as a gateway arm).
- Sideways: **SRD-026** (signal — the name-keyed sibling); this SRD retires the
  `SubscriptionKey()` deferral SRD-020/026 recorded.
- Closes GitHub epic **#90**'s Link item (Compensation/Escalation/Cancel/Link);
  the other three remain.
- At landing (sync-linked-docs): the roadmap **WS-C6 / M4** row, the
  conformance-status tracker (Link → landed), README/`docs/guides` where the
  event catalog is listed.

## §9 Definition of Done

- [ ] FR-1…FR-6 implemented; every §6 test exists and passes.
- [ ] `make ci` green; touched functions ≥80% (aim 100%) diff-coverage.
- [ ] `internal/instance/track.go` unchanged (NFR-1); no new waiter/hub path.
- [ ] `examples/link-events/` runs exit 0 under timeout with expected output;
      its built binary gitignored.
- [ ] An on-page-loop e2e proves re-entrancy (T-6), not just a single hop.
- [ ] ADR-006 v.4 flipped to Accepted (+ RU twin); §10 filled; roadmap /
      conformance-tracker / epic #90 synced.

## §10 Implementation summary

*Filled at landing: touched files/lines per milestone, verification results,
milestone commit SHAs, deltas vs this draft.*

## Open questions

- None. The pairing model (name at container, §4.1), static snapshot resolution
  (§4.2), catch-bypass redirect (§4.3), fail-fast validation (§4.4), and the
  `SubscriptionKey` retirement (§4.5) are decided; ADR-level questions were
  closed by ADR-006 v.4 §2.8.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-07-20 | Ruslan Gabitov | Initial draft — lands ADR-006 v.4 §2.8: `LinkEventDefinition` reshaped to the event-definition skeleton, Link enrolled at the two intermediate positions, per-container pairing validation at registration, static throw→target resolution precomputed in the snapshot, and the Link throw `Exec` redirecting to the target catch's outgoing flows (zero runner change). Retires the `SubscriptionKey()` generalization premise. |
