# SRD-074 — Ad-Hoc Sub-Process: the Router core

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-07-30 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-035 v.1](../design/ADR-035-adhoc-sub-process.md) §2.1–§2.9 (the Sub-Process variant on the existing nested scope; the Router replacing sequence-flow succession; drain-inherited completion; explicit Routers; the first containment step) |
| Upstream | [ADR-023 v.3](../design/ADR-023-sub-process-and-call-activity.md) (the nested token scope and its drain); [ADR-020 v.1](../design/ADR-020-human-interaction-execution-model.md) (the cooperative wait-node park reused by manual selection); [ADR-011 v.7](../design/ADR-011-process-data-flow.md) (the data the Router reads); [ADR-032 v.1](../design/ADR-032-language-routed-expression-engines.md) (the expression battery's evaluation seam); [ADR-013 v.2](../design/ADR-013-instance-observability.md) (the fact stream) |

## §1 Background

ADR-035 decides the Ad-Hoc Sub-Process as a Sub-Process variant whose inner
succession is answered by a host-supplied **Router** instead of sequence flows,
with an empty answer ending the track so that completion falls out of the
existing scope drain. This SRD lands the **core slice**: leaf Tasks and plain
embedded Sub-Processes inside the container, explicit Routers, both ordering
modes, automatic and manual selection, and the per-scope control handle. The
token-flow half — inner sequence flows, gateways and intermediate catch events
— is the accompanying second SRD and is *rejected at registration* here.

The engine already carries every mechanism this needs; the work is a
substitution at one seam plus a model element:

- `track.checkFlows` (`internal/instance/track.go:1414`) is the whole
  succession decision: `len(flows) == 0` sets `TrackEnded`, otherwise the first
  flow continues this track (cyclic-preferred) and the remainder are emitted as
  `evFork`.
- `loopState.spawnForks` (`internal/instance/loop.go:720`) builds each forked
  track with `newTrack(f.Target().Node(), …)` and records `nt.steps[0].inFlow = f`.
- `trackEvent.flows` (`internal/instance/event.go:21`) is how successors travel
  from a track to the loop.
- `loopState.completeScope` / `decScope` (`internal/instance/scope_runtime.go`)
  already turn "every track in the scope ended" into "the host activity
  completes" — the completion this element inherits.
- `std_loop.go:134` is the precedent for computing successors instead of
  reading `node.Outgoing()`.

## §2 Requirements

### Functional

- **FR-1** — `activities.NewSubProcess` accepts `WithAdHoc(...)`, producing an
  Ad-Hoc Sub-Process: a `SubProcess` reporting `IsAdHoc() == true`, mutually
  exclusive with `WithTriggeredByEvent` and `WithTransaction` (loud error at
  construction, the existing pattern).
- **FR-2** — a public `adhoc.Router` is consulted for successors **at scope
  open** and **after each inner activity settles**. Its answer replaces the
  node's outgoing sequence flows inside the container.
- **FR-3** — an **empty** answer ends the asking track. When the scope's last
  track ends, the existing drain completes the Ad-Hoc Sub-Process; no new
  completion path is added.
- **FR-4** — the Router receives progress state (completed counts, running
  counts, the settling activity) and a **read-only data reader over a transient
  frame opened at the Ad-Hoc scope**, with parent data visible by walk-up.
- **FR-5** — `ordering`: **`parallel`** (default) continues the track on the
  first successor and forks the rest; **`sequential`** permits at most one live
  inner activity and rejects a multi-successor answer with a classified error.
- **FR-6** — selection modes: **auto** takes the answer directly; **manual**
  (`WithAdHocManualSelection()`) parks the asking track with the answer
  recorded as the enabled set and resumes it onto the activity the host
  activates.
- **FR-7** — a per-scope control handle reachable from `InstanceHandle` exposes
  the enabled set, the running activities, and `Activate`. Activating an id
  that is not currently enabled is a classified error, never a silent no-op.
- **FR-8** — `cancelRemainingInstances` (default **true**, per the metamodel)
  cancels live inner tracks when the Router stops; `false` waits for them.
- **FR-9** — `WithAdHocCompletion(expr)` is Router sugar: evaluate after each
  settle, answer empty when true, else delegate to the wrapped Router.
- **FR-10** — batteries: `routers.Standard()` (the flow-less activities, each
  once), `routers.Expression(expr)` (a `FormalExpression` naming successors),
  `routers.Sequence(ids…)`.
- **FR-11** — registration-time validation: inner elements are restricted to
  leaf Tasks and plain embedded Sub-Processes; Start/End events, sequence
  flows, gateways, intermediate events, Event Sub-Processes, Transactions and
  Call Activities are each rejected with a message naming what was found and
  why. A container with **neither** Router nor completion condition is rejected.
- **FR-12** — observability: a new `KindAdHoc` fact kind carrying the routing
  decisions, per §3.6. The Ad-Hoc **scope's own** lifecycle keeps emitting the
  existing `KindScope` facts (a nested scope is a nested scope); `KindAdHoc`
  adds only what is peculiar to ad-hoc — *which* activities were offered, which
  was chosen and by whom, and *why* the container stopped.
- **FR-13** — a routing decision is reconstructible from the stream alone: for
  every activation there is a preceding offer naming the candidate set, and the
  terminal fact names the stop reason. Without that pairing an ad-hoc case is
  unauditable after the fact — the reason ADR-035 §5 asks for it.

### Non-functional

- **NFR-1** — the Router runs **off the instance loop**, so a slow Router
  degrades its own scope and never stalls the instance (the iteration-decorator
  precedent).
- **NFR-2** — the Router never writes: it receives a reader, and its transient
  frame is discarded, so no Router can leave scope residue.
- **NFR-3** — **one** fork-and-continue implementation. The flow path and the
  Router path build the same successor list; the continue/fork/end rules are
  not duplicated.
- **NFR-4** — touched files finish at ≥95% diff-coverage (target 100%);
  `make ci` green.

## §3 Models

### §3.1 `successor` — one shape for both succession sources (`internal/instance`)

`checkFlows` currently interleaves *what comes next* with *how a token
advances*. The Router answers with nodes and no flow, so the two are separated:

```go
// successor is one next step for a track: the node to run and, when the token
// arrived over a sequence flow, that flow (nil for a Router-chosen activity —
// an ad-hoc activation did not arrive on a branch).
type successor struct {
	node   flow.Node
	inFlow *flow.SequenceFlow
}
```

`checkFlows` becomes a thin mapper (flows → `[]successor`) over a new
`advance(succs []successor) error` carrying today's body verbatim: the empty →
`TrackEnded` rule, the cyclic-preferred pick, the compensation snapshot, the
`t.steps` append, `checkNodeType`, `evMoved`, and the fork emit. `trackEvent`
carries `succs []successor` in place of `flows`, and `spawnForks` builds each
track from `s.node`, assigning `inFlow` only when non-nil.

The five existing emit sites (`boundary_watch.go` ×3, `escalation_watch.go` ×2,
`instance.go`) map their flows through one helper — mechanical, and it leaves a
single fork implementation (NFR-3).

### §3.2 The Router contract (`pkg/adhoc`, new package)

```go
// Router answers "what may run next" inside an Ad-Hoc Sub-Process. An empty
// result ends the asking track; when the scope's last track ends, the Ad-Hoc
// Sub-Process completes. Implementations must be prompt, read-only and free of
// blocking I/O — waiting for a human is expressed by manual selection, not by
// a slow Router.
type Router interface {
	Next(ctx context.Context, s State) ([]string, error)
}

// State is what a routing decision may rest on.
type State struct {
	// Completed counts settled executions per inner activity id.
	Completed map[string]int
	// Running counts live instances per inner activity id.
	Running map[string]int
	// Last is the activity whose completion triggered this call; empty at scope open.
	Last string
	// Data reads the Ad-Hoc scope (parent data visible by walk-up).
	Data service.DataReader
}
```

`pkg/adhoc` imports only `pkg/model/service` (the `DataReader`, verified at
`pkg/model/service/datareader.go:10`) and stdlib — no cycle with
`pkg/model/activities`, which imports it for the option.

### §3.3 The model element (`pkg/model/activities`)

`SubProcess` gains an `adHoc *adHocSpec` field (nil for every other variant),
carrying the router, ordering, manual flag and `cancelRemainingInstances`.
Options, in the package's established shape: `WithAdHoc(router)`,
`WithAdHocOrdering(...)`, `WithAdHocManualSelection()`,
`WithAdHocCancelRemaining(bool)`, `WithAdHocCompletion(expr)`. `IsAdHoc()`
reports the variant; the runtime resolves the spec through a small capability
interface, as `IsTransaction`/`IsEventSubProcess` already are.

### §3.4 Runtime wiring (`internal/instance`)

An `adHocOf(node)` detector (sibling of `standardLoopOf`/`multiInstanceOf`)
resolves the capability. Where `scopeSeeds` supplies entry nodes for an
ordinary composite, an Ad-Hoc scope instead asks the Router; where
`checkFlows` would read `node.Outgoing()` for a node whose scope is Ad-Hoc, the
Router is asked again. Ask and answer travel as track↔loop events, so the
Router call itself happens off the loop goroutine (NFR-1).

Manual mode reuses the wait-node park: the track parks on `evtCh` exactly as a
User Task does, the enabled set is recorded on the scope entry, and the
activation arrives as an event that resumes the track onto the chosen node.

### §3.5 The control handle (`pkg/thresher`)

```go
func (h *InstanceHandle) AdHoc(nodeID string) (*AdHocHandle, error)

func (ah *AdHocHandle) Enabled() []ActivityView
func (ah *AdHocHandle) Running() []ActivityView
func (ah *AdHocHandle) Activate(ctx context.Context, activityID string) error
```

`Activate` passes the engine's authorization seam like `Take`/`Complete`, so
"who activated what" is answerable in an audit (ADR-035 §5).

### §3.6 Observability (`pkg/observability`)

One new `Kind`, four `Phase` values (two reused), three new attribute keys —
all typed constants beside the existing vocabulary, per the house rule that a
kind/phase field is a named type, never a bare string:

```go
KindAdHoc Kind = "AdHoc" // ad-hoc routing decisions (ADR-035)

PhaseOffered   Phase = "Offered"   // candidates from a Router answer
PhaseActivated Phase = "Activated" // one candidate selected and started
// PhaseCompleted / PhaseCanceled are reused for the container's outcome.

AttrCandidates = "candidates"  // comma-joined inner activity ids offered
AttrSelectedBy = "selected_by" // "router" | "host" (with the actor when known)
AttrStopReason = "stop_reason" // "router-empty" | "completion-condition" | "canceled"
```

| Moment | Kind · Phase | Carries |
|---|---|---|
| a Router answer arrives | `AdHoc` · `Offered` | the container node, `candidates` |
| an activity starts | `AdHoc` · `Activated` | the activity node, `selected_by` |
| the container settles | `AdHoc` · `Completed` \| `Canceled` | `stop_reason` |

`KindAdHoc` echoes at `slog.LevelInfo` in `kindLevel` — the level a human-steered
decision warrants, matching `KindScope` and `KindTaskState`. The Ad-Hoc scope's
open/close continues to ride `KindScope`, so nothing is emitted twice, and the
echo decision stays derived from the fact's own data rather than from its
producer.

An **empty** offer is still emitted: "the Router considered the state and chose
to stop" is exactly the fact an auditor needs, and suppressing it would make
the terminal indistinguishable from a Router that was never asked.

## §4 Analysis

### §4.1 Why generalize the successor, rather than synthesize flows

Synthesizing a `SequenceFlow` per Router answer would leave `checkFlows`
untouched, but those phantom flows exist in no model: they would enter token
history, join-arrival accounting (`inFlow` is exactly what a synchronizing join
reads) and the observability stream. ADR-035 §4 rejects it, and the `successor`
type is the honest alternative — one extra field where the flow used to be
implicit.

### §4.2 Why the Router is asked through the loop, not called inline

The track goroutine could call the Router directly, which is simpler. It is
rejected: host code on the track path can block, and the Router needs a
*consistent* data snapshot, which means opening a transient frame — a scope
operation. Routing through the loop keeps frame ownership where it already is
and bounds a misbehaving Router to its own scope.

### §4.3 Why `sequential` rejects rather than truncates

Taking only the first of several successors would silently discard a modelling
error and make the run order depend on the Router's incidental output order.
The engine's no-silent-caps rule applies: a `sequential` container whose Router
offers two activities is a defect, reported as one.

## §5 API

New public surface: `pkg/adhoc` (`Router`, `State`), `pkg/adhoc/routers`
(`Standard`, `Expression`, `Sequence`), the `activities.WithAdHoc*` option
family, `SubProcess.IsAdHoc`, and `thresher.AdHocHandle` with
`InstanceHandle.AdHoc`. Additive; no existing signature changes outside the
internal `successor` refactor.

## §6 Tests

| Test | Asserts |
|---|---|
| `TestAdHocOptionExclusivity` | `WithAdHoc` + `WithTransaction`/`WithTriggeredByEvent` → classified error (FR-1) |
| `TestAdHocValidationRejectsInnerElements` | flows, gateways, intermediate/start/end events, Event SP, Transaction, Call Activity each rejected by name (FR-11) |
| `TestAdHocValidationRequiresRouter` | no router and no completion condition → rejected at registration (FR-11) |
| `TestAdHocRouterDrivesSuccession` | a scripted Router runs activities in its stated order (FR-2) |
| `TestAdHocEmptyAnswerCompletesScope` | empty answer → track ends → scope drains → the Sub-Process completes, with no new completion path (FR-3) |
| `TestAdHocRouterReadsScopeData` | a Router branching on a value an earlier activity wrote takes the data-dependent path (FR-4) |
| `TestAdHocParallelForksAndRepeats` | a multi-successor answer forks; the same activity named twice yields two concurrent instances (FR-5) |
| `TestAdHocSequentialRejectsMultiple` | `sequential` + two successors → classified error (FR-5) |
| `TestAdHocManualSelectionParksAndActivates` | manual mode parks with the enabled set, `Activate` resumes onto the choice, an unknown id errors (FR-6, FR-7) |
| `TestAdHocCancelRemainingInstances` | `true` cancels live tracks at stop; `false` waits for them (FR-8) |
| `TestAdHocCompletionConditionSugar` | the condition ends the scope through the same empty-answer path (FR-9) |
| `TestAdHocBatteryRouters` | `Standard`, `Expression`, `Sequence` each drive their documented order (FR-10) |
| `TestAdHocFacts` | `Offered` carries the candidate set; `Activated` carries `selected_by`; the terminal carries `stop_reason`; the scope's own open/close still rides `KindScope`, not duplicated (FR-12) |
| `TestAdHocEmptyOfferEmitted` | a Router answering empty still emits `Offered` with no candidates, so "chose to stop" is distinguishable from "never asked" (FR-12) |
| `TestAdHocDecisionReconstructable` | over a multi-step run, every `Activated` has a preceding `Offered` naming it among the candidates, and the stream ends with a reason (FR-13) |
| `TestAdHocEchoLevel` | `KindAdHoc` resolves to `slog.LevelInfo` and is absent from `kindNoEcho` (FR-12) |
| `TestSuccessorRefactorPreservesFlowPath` | the existing gateway/boundary/escalation fork suites stay green over `successor` (NFR-3) |
| e2e | an Ad-Hoc process through the public engine: data-driven routing, one manual activation, completion |

## §7 Milestones

- **M1** — the `successor` extraction: `advance()`, `trackEvent.succs`,
  `spawnForks`, the five emit sites. No behaviour change; the existing suites
  are the proof.
- **M2** — `pkg/adhoc` + the model element, options and registration validation.
- **M3** — runtime: Router consultation at scope open and settle, ordering,
  drain-inherited completion, `cancelRemainingInstances`.
- **M4** — manual selection: park, the per-scope handle, `Activate`,
  authorization.
- **M5** — observability: the `KindAdHoc` vocabulary, its emission sites and
  the echo-level registration (§3.6). Lands as its own milestone rather than
  trailing M3/M4, so the facts are designed with the decisions rather than
  bolted on after — the DataChange kind shipped late once and cost a follow-up.
- **M6** — batteries, completion-condition sugar, e2e, example, docs
  (`docs/guides/subprocesses/` + the observability reference), CHANGELOG,
  conformance tracker row 13.

## §9 Definition of Done

- FR-1…FR-12 and NFR-1…NFR-4 satisfied and test-covered.
- `make ci` green across modules; diff-coverage ≥95% on touched lines.
- The example runs to exit 0 under the FIX-029 run-step.
- Conformance tracker row 13 flipped in this PR; `/check-srd` PASS before the
  status flip.

## §10 Implementation summary

*Filled at landing.*

## Open questions

None.
