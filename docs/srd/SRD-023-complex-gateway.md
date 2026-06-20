# SRD-023 — Complex gateway (activation-driven synchronizing join)

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-06-20 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-005 v.3 Gateways & Joins](../design/ADR-005-gateways-and-joins.md) §2.11 |

This SRD lands the **Complex gateway** decided in [ADR-005 v.3](../design/ADR-005-gateways-and-joins.md)
§2.11: a **converging** synchronizing join whose completion is an **activation rule** —
a disjunction of `(condition, count, requiredFlows)` triples — and a **diverging** split
that reuses the Inclusive split (§2.9). It reuses the OR-join's park/resume +
reachability machinery ([SRD-022](SRD-022-inclusive-or-join.md), sideways) wholesale,
changing only the completion rule and adding a data-guard evaluation path.

---

## 1. Background

ADR-005 v.3 §2.11 adds the Complex gateway as an explicit extension above the Common
Executable Subclass (`bpmn-spec/conformance.md`), for the Discriminator (WCP-9/28) and
Partial-Join (WCP-30/31) patterns. The converging gateway is a synchronizing join, so
it builds directly on what is already in the tree:

- The OR-join park/resume + death-trigger machinery: `internal/instance/track.go`
  `synchronize` (`track.go:491`) parks a reachability-join arrival on `TrackAwaitSync`
  + `parkCh` and the loop re-checks on arrival/death (`instance.go:767`
  `recheckAwaitingJoins`, `instance.go:804` `recheckJoin`, `instance.go:829`
  `fireOrJoin`, `instance.go:789` `hasInTransitArrival`).
- The reachability oracle: `internal/instance/reachability.go:15` `CheckFlows`
  (backward, condition-ignoring, cycle-guarded) over `occupiedNodes` (`reachability.go:43`).
- The model pattern: `pkg/model/gateways/inclusive.go` (`InclusiveGateway`,
  `var _ exec.ReachabilityJoin`, `Arrive`/`Recheck`/`unmarkedFlows`/`absorb`/`Clone`).
- The split: `inclusive.go:89` `Exec` forks the conditionally-true subset (§2.9), and
  `gateway.go:207` `checkCondition` evaluates a `data.FormalExpression` via
  `re.ExpressionEngine().Evaluate`.

**The gap.** No Complex gateway exists. Its completion rule differs from the OR-join
in two ways that the current contracts do not serve:

1. **Data guards.** Each triple carries an optional `condition` over **process data**.
   Conditions are evaluated only at `Exec` time today (with a `renv.RuntimeEnvironment`
   built from a per-node frame, `execenv.go:21` `newExecEnv`). The join's decision
   happens earlier, at `Arrive` (`track.go:502` — **no frame, no `re` yet**) and at
   `Recheck` (`instance.go:819` — the loop passes only `fc`, **no `re`**). So the data
   guards need a new, frame-free evaluation channel.
2. **Death means abort, not fire.** The OR-join fires on a death-trigger (a death can
   make an un-marked flow unreachable → complete). The Complex gateway's count is
   **monotonic** — a death never adds an arrival — so a death can only make a triple
   *unsatisfiable*. The death path therefore **aborts** (throws), it does not fire.

This SRD adds the Complex-specific contract, the model gateway, the track/loop wiring,
the registration-time validation hook, tests, and an example.

---

## 2. Requirements

### Functional

- **FR-1 — `ComplexGateway` model type.** A new `pkg/model/gateways/complex.go`
  `ComplexGateway` embedding `Gateway` (mirrors `InclusiveGateway`, `inclusive.go:25`),
  carrying its activation rule + per-instance arrival state (`arrived`, `order`,
  `fired`) under its own `mu`. `Clone()` (fresh arrival state, ADR-009), `Node()`.
- **FR-2 — activation triples.** The rule is `[]activationTriple`, each
  `{ cond data.FormalExpression; count int; required []string }` (incoming-flow ids).
  Constructors: `WithActivationThreshold(n int)` (sugar for one triple `{nil, n, nil}`)
  and `WithActivation(...activationTriple)` via a public triple builder. The two are
  **mutually exclusive** and **at least one** is required → build error otherwise
  (validate-all-params).
- **FR-3 — diverging split.** Diverging, `Exec` forks the conditionally-true outgoing
  subset exactly as the Inclusive split (§2.9) — reuse `checkCondition` + the
  default/exception rules (`inclusive.go:89`). A converging/`≤1`-outgoing Complex
  gateway's `Exec` is the survivor's post-merge continuation.
- **FR-4 — `exec.ActivationJoin` contract.** A new interface in `pkg/exec/exec.go`:
  the converging Complex gateway records arrivals and decides fire/park/abort using a
  caller-supplied **`GuardEval`** (data-guard evaluator) and the existing `FlowChecker`
  (reachability). See §3.3.
- **FR-5 — fire rule.** On an arrival, the gateway fires when **some triple is
  satisfied**: `eval(cond)` true (or `cond == nil`) **and** `|arrived| ≥ count`
  **and** `required ⊆ arrived`. The completing arrival is the survivor (last-in); the
  other arrived tracks are merged. Reuses `absorb` (`inclusive.go:216`).
- **FR-6 — park.** A non-firing arrival parks: `TrackAwaitSync` + `evParked` + block
  on `parkCh` — the **same** path the OR-join uses (`track.go` synchronize), extended
  to recognise an `ActivationJoin`.
- **FR-7 — abort (anti-hang).** On a token death (and on a non-firing arrival), the
  gateway aborts — **fails the instance** — when **every** triple is **dead**:
  `|arrived| + |reachable| < count`, **or** a `required` gate is neither arrived nor in
  `reachable` (a mandatory gate can never come). `reachable` is `CheckFlows` over the
  un-marked incoming flows. Exact (counts monotonic; gate-reachability structural).
- **FR-8 — exhaustion no-match.** When no more tokens can arrive (`reachable` empty)
  and no triple's guard holds at the maxed counts, the gateway throws "arrivals
  exhausted, no activation matched" — the Exclusive no-match analogue (§2.8).
- **FR-9 — trailing tokens.** After firing, a later arrival on another incoming flow is
  consumed (the track ends at the gateway); the gateway does not re-arm (single-pass).
- **FR-10 — guard evaluation channel.** The instance exposes a `GuardEval` built over
  its **root** data scope (`inst.dataPlane.Root()`, `instance.go:494`) +
  `inst.ExpressionEngine()` — frame-free, process-level. The track (`t.instance`) and
  the loop both obtain it; no node-execution frame is fabricated.
- **FR-11 — registration validation.** A per-node validation hook in `Process.Validate`
  (`process.go:213`) calls an optional `interface{ Validate() error }` on each node;
  `ComplexGateway` implements it to check, against its now-linked incoming flows:
  `1 ≤ count ≤ M`, `count ≥ len(required)`, and every `required` id is an actual
  incoming flow. Build-time checks (`count ≥ 1`, `count ≥ len(required)`, ≥1 triple)
  stay in the constructor; `count ≤ M` + id-membership are registration-time.

### Non-functional

- **NFR-1 — namespace isolation.** Activation `count`s and gate identities never enter
  the data namespace; a `condition` is an ordinary process-data expression. No reserved
  variable names, no prefixes (ADR-005 v.3 §2.11 Engine note).
- **NFR-2 — concurrency.** Arrival state is per-node, per-instance (ADR-009), mutated
  under the gateway's own `mu`; concurrent arrivals are atomic (ADR-005 §2.4). The
  `GuardEval` reads a committed root scope; no write races.
- **NFR-3 — Parallel/OR untouched.** `ParallelGateway` and `InclusiveGateway` keep
  their contracts; the new `ActivationJoin` path is additive in `synchronize` /
  `recheckJoin`.
- **NFR-4 — coverage.** Touched files finish ≥95% diff-coverage (`make ci`
  `cover-check`), aim 100%.

---

## 3. Models

### 3.1 `ComplexGateway` (`pkg/model/gateways/complex.go`)

```go
// activationTriple is one disjunct of a Complex gateway's activation rule: the join
// fires when cond holds (nil = always), count incoming flows have arrived, and every
// required incoming flow is among them.
type activationTriple struct {
    cond     data.FormalExpression // optional process-data guard
    count    int                   // total arrivals required
    required []string              // incoming-flow ids that must be among the arrived
}

type ComplexGateway struct {
    activation []activationTriple
    order      []string          // arrival order (survivor selection)
    arrived    map[string]string // incomingFlowID -> arrivingTrackID
    Gateway
    mu    sync.Mutex
    fired bool
}

var (
    _ exec.NodeExecutor   = (*ComplexGateway)(nil)
    _ exec.ActivationJoin = (*ComplexGateway)(nil)
)
```

Mirrors `InclusiveGateway` (`inclusive.go:25`): same `arrived`/`order`/`fired` shape,
same `Clone` (fresh state) and `Node` accessors.

### 3.2 Constructor + options (`gateway_options.go` peers)

```go
func NewComplexGateway(opts ...options.Option) (*ComplexGateway, error)

// WithActivationThreshold / WithActivation are Complex-specific options (a
// ComplexOption type) sorted by NewComplexGateway the way New() type-switches
// GatewayOption vs foundation.BaseOption (gateway.go:98); name/direction/id pass
// straight to the embedded Gateway via New().
func WithActivationThreshold(n int) ComplexOption // one guard-less triple {nil, n, nil}
func WithActivation(triples ...Triple) ComplexOption

// Triple is a public builder for one activation disjunct.
func NewTriple(count int, opts ...TripleOption) (Triple, error) // WithGuard, WithRequired
```

`NewComplexGateway` sorts its options (base/name/direction → `New(...)`; activation →
gatewayConfig-equivalent), then applies build-time validation (mirroring the
self-naming option-constructor convention): reject both activation sources, reject
zero triples, reject `count < 1`, reject `count < len(required)` — each with a
self-identifying `errs` message.

### 3.3 `exec.ActivationJoin` (`pkg/exec/exec.go`)

```go
// GuardEval evaluates a Complex gateway's data guard against process-level data.
// Supplied by the instance (root scope + expression engine); a nil cond is true.
type GuardEval func(cond data.FormalExpression) (bool, error)

// ActivationJoin is a converging gateway whose completion is an activation rule over
// per-triple data guards, arrival counts, and required gates (ADR-005 v.3 §2.11). It
// reuses the reachability machinery (FlowChecker) but, unlike a ReachabilityJoin, a
// token death makes it ABORT (the count is monotonic) rather than fire.
type ActivationJoin interface {
    NodeExecutor

    // Record registers arrivingTrackID's arrival on incomingFlowID and reports
    // whether the gateway already fired (the arrival is then a trailing token to
    // consume). It makes NO activation decision — reachability + guards are read only
    // by the loop (Recheck), never off the arriving track's goroutine, because the
    // live-token set CheckFlows reads is loop-owned and must not be raced.
    Record(incomingFlowID, arrivingTrackID string) (firedAlready bool)

    // Recheck is the loop's decision: fire (survivor + merged), abort (the rule is
    // unsatisfiable), or wait. Run after an arrival parks and on every token death.
    Recheck(eval GuardEval, fc FlowChecker) (Decision, error)
}

type Decision struct {
    Fired    bool
    Aborted  bool
    Survivor string
    Merged   []string
}
```

`ParallelGateway` (`SynchronizingJoin`) and `InclusiveGateway` (`ReachabilityJoin`) are
unchanged; `ActivationJoin` is a sibling, recognised additively at the call sites.

### 3.4 Per-node validation hook (`pkg/model/process/process.go`)

```go
// in Process.Validate, after the flow-connectivity pass:
for _, n := range p.nodes {
    if v, ok := n.(interface{ Validate() error }); ok {
        if err := v.Validate(); err != nil { ee = append(ee, err) }
    }
}
```

`ComplexGateway.Validate()` checks `1 ≤ count ≤ len(Incoming())`,
`count ≥ len(required)`, and `required ⊆ Incoming()` per triple.

---

## 4. Analysis

### 4.1 Why a `GuardEval` callback, not a threaded `re`

Ground truth: at `Arrive` (`track.go:502`) the arriving track has **not built its
execution frame** (frames are created in `executeNode`, after `synchronize`), and at
`Recheck` (`instance.go:819`) the loop passes only `fc` and is **not executing a single
node** — there is no per-execution frame and no obvious "which token's data view." A
`data.FormalExpression` is evaluated by `re.ExpressionEngine().Evaluate(ctx, cond, re)`
(`gateway.go:207`), and a `re` is an `execEnv{Instance, *scope.Frame}` (`execenv.go:21`).

The Complex guards read **process-level** data (properties), which live in the
instance **root** scope, not a node-local frame. So the instance can build a frame-free
evaluator: `newExecEnv(inst, inst.dataPlane.Root())` → a `RuntimeEnvironment` for
process-level reads. We expose that as a `GuardEval` closure and pass it into
**`Recheck`** (the loop's decision) — the track's `Record` takes no evaluator, because
reachability **and** guards are read only by the loop, never off the arriving track's
goroutine (the live-token set `CheckFlows` reads is loop-owned and must not be raced;
proven by `-race`). The gateway keeps ownership of the decision (it knows its triples);
the loop supplies the capability. This avoids fabricating a node frame (architecturally
wrong) and avoids putting counts in the namespace (NFR-1).

**Alternative considered — thread `renv.RuntimeEnvironment` through `Arrive`/`Recheck`.**
Rejected: forces a per-node frame at sites that have none, and couples the join contract
to the full runtime surface for a single capability (guard evaluation). The callback is
the minimal channel.

**Alternative considered — instance pre-evaluates guards and passes a bitset.**
Rejected: inverts ownership (the instance would need to know the gateway's triples) and
re-evaluates eagerly even when the structural part can't fire.

### 4.2 Why death aborts (not fires) — divergence from `ReachabilityJoin`

The OR-join's `Recheck` fires on a death (a death can make the last un-marked flow
unreachable → "all reachable arrived" → complete). The Complex count is a **monotonic
`≥` threshold**: a death never increases `|arrived|`, so it can only push a triple from
*maybe* to *dead*. Hence the Complex death path computes **abort**, never fire. This is
why `ActivationJoin` is a distinct contract rather than reusing `ReachabilityJoin` — the
`Decision.Aborted` arm has no OR-join analogue. Firing is decided by the loop's
`Recheck` (an arrival records then parks, and the loop's recheck fires it); a death only
ever aborts. The abort/exhaustion test reuses `CheckFlows` unchanged.

### 4.3 Why a per-node `Validate` hook

`Process.Validate` (`process.go:213`) today checks only flow connectivity; there is no
per-node validation pass (confirmed: no loop over `p.nodes` calling a node `Validate`).
`count ≤ M` and `required ⊆ incoming` are knowable only after linking, i.e. at
registration (`snapshot.New` → `p.Validate`, `thresher.go` `RegisterProcess`). Adding
an optional `interface{ Validate() error }` hook is the least-invasive place and is
reusable by other nodes later. Build-time-knowable checks (`count ≥ 1`,
`count ≥ len(required)`, ≥1 triple) stay in the constructor.

### 4.4 Track/loop wiring (reuse + the additive branch)

`synchronize` (`track.go`) gets an `ActivationJoin` branch **before** the
`SynchronizingJoin` check (Complex is not a `SynchronizingJoin`):
`synchronizeActivation` calls `Record(flowID, trackID)` and either **consumes** a
trailing token (the gateway already fired) or **parks** (reuse
`TrackAwaitSync`/`parkCh`/`evParked`) — the track makes **no** decision. `recheckJoin`
(`instance.go`) gets an `ActivationJoin` branch via a type-switch:
`Recheck(inst.guardEval(ctx), inst)` → `Fired` → `fireOrJoin` (resumes the parked
survivor); `Aborted` → `fail` the instance (lastErr + cancel, loop-only single writer);
else wait. The `hasInTransitArrival` guard applies unchanged.

---

## 5. Public API / contract surface

- `gateways.NewComplexGateway(opts...) (*ComplexGateway, error)` — `foundation.WithID`,
  `foundation.WithDoc`, `options.WithName`, `gateways.WithDirection`, plus
  `WithActivationThreshold(n)` **xor** `WithActivation(triples...)`.
- `gateways.NewTriple(count, WithGuard(expr), WithRequired(flowIDs...))`.
- `exec.ActivationJoin`, `exec.GuardEval`, `exec.Decision` (new public contract).
- No change to `SynchronizingJoin` / `ReachabilityJoin` / `FlowChecker`.

---

## 6. Test scenarios

**Model-unit** (`pkg/model/gateways/complex_test.go`, hand-written `FlowChecker` +
`GuardEval` stubs): `TestNewTriple` / `TestNewComplexGateway` (build validation +
threshold-xor-expression mutual exclusion); `TestComplexFiresAtThreshold` (Record then
Recheck fires at the threshold, survivor last-in); `TestComplexGuardGatesFire` (the
guard gates the fire); `TestComplexRequiredFires` (a required gate);
`TestComplexAbortCountUnreachable` / `TestComplexAbortRequiredUnreachable` /
`TestComplexExhaustionNoMatch` (the abort paths); `TestComplexRecheckReachabilityError`
/ `TestComplexGuardError` (conservative wait / guard-error propagation);
`TestComplexRecheckNoArrivals`; `TestComplexOptionApplyWrongConfig`;
`TestComplexIsActivationJoin`; `TestComplexValidate`; `TestComplexGatewayClone`;
`TestComplexSplitSubset`.

**In-package** (`internal/instance/complex_internal_test.go`, runs a diamond on a real
instance so the wiring is recorded by the per-package coverage profile):
`TestComplexDiscriminatorInstance`, `TestComplexGuardInstance`,
`TestComplexAbortInstance`, `TestComplexGuardEvalErrorInstance`,
`TestComplexGuardNotBoolInstance`.

**Registration** (`pkg/model/process/process_test.go`):
`TestProcessValidateComplexGateway` — an out-of-range threshold is rejected at
registration; a valid one passes; nodes without a `Validate()` method are untouched.

**Engine** (`pkg/thresher/complex_gateway_test.go`, `-race`): `TestComplexDiscriminator`
(1-of-3, the rest consumed), `TestComplexPartialJoin` (2-of-3, the 3rd consumed),
`TestComplexDataAware` (amount picks the threshold), `TestComplexRequiredGate`,
`TestComplexAbortOnDeath` (a diverted branch dies → death-recheck aborts, no hang).

**Example** (`examples/complex-gateway/`): a 3-approver data-aware partial join
(`process.go` + `main.go`, ≤80-line entry), smoke exit 0.

---

## 7. Worked example (data-aware partial join)

```
start ─OR┬→ manager ─┐
         ├→ finance ─┤ Complex join: [(amount<1000, 2), (amount>=1000, 3[cfo])]
         └→ cfo ─────┘        → finalize → end
```

`amount = 500` → two of {manager, finance, cfo} suffice → fires at the 2nd arrival.
`amount = 5000` → needs 3 **including cfo**; fires only when cfo + two others are in;
if cfo's branch dies first, the death-recheck aborts (no silent hang).

---

## 8. Cross-document references

- **Implements** [ADR-005 v.3](../design/ADR-005-gateways-and-joins.md) §2.11 (the
  Complex gateway decision); §2.9 (split reuse), §2.10 (park/resume + reachability),
  §2.4 (synchronization ownership).
- Refines pin [ADR-001 v.5](../design/ADR-001-execution-model.md) (tracks/tokens/loop),
  [ADR-009 v.1](../design/ADR-009-per-instance-node-graph.md) (per-instance node state).
- Sideways [SRD-022 v.1](SRD-022-inclusive-or-join.md) — the OR-join machinery reused.

(Up/sideways only; no downward references.)

---

## 9. Definition of Done

- FR-1…FR-11 wired (model type, triples, split, `ActivationJoin`, fire/park/abort/
  exhaustion/trailing, guard channel, registration validation).
- §6 model-unit + engine tests present and green under `-race`.
- `examples/complex-gateway` smokes exit 0; binary gitignored.
- `make ci` green: lint, build, `-race`, **diff-coverage ≥95%** on touched files (aim
  100%), govulncheck.
- ADR-005 v.3 §2.11 satisfied; NFR-1…NFR-4 held (namespace isolation, concurrency,
  Parallel/OR untouched, coverage).
- `/check-srd` PASS; then flip Accepted + RU twin + ADR-005 v.3 → Accepted (sync linked
  docs).

## 10. Implementation summary

Landed on `feat/complex-gateway` (off `master`): four milestones + the doc.

### 10.1 Commits

| M | Commit | Scope |
|---|---|---|
| doc | `b6d3da9` | SRD-023 draft |
| M1 | `b344795` | `exec.ActivationJoin` + `ComplexGateway` model + model-unit tests |
| M2 | `a956d76` | per-node `Process.Validate` hook |
| M3 | `a632602` | instance wiring (Record/Recheck, guardEval, recheckJoin, synchronizeActivation) |
| M4 | `55d02f7` | `examples/complex-gateway` + in-package coverage tests |

Adjacent in the same PR: debug-level event logging (`5cdbd52`) and **FIX-006**
(`6dcd370`) — the OR-join all-branches-arrive hang surfaced while building M3.

### 10.2 Key files

- `pkg/exec/exec.go` — `ActivationJoin` (`Record` + `Recheck`), `GuardEval`, `Decision`.
- `pkg/model/gateways/complex.go` — `ComplexGateway`, `Triple`, options, `Exec` (via
  `forkTrueSubset`), `Record`, `Recheck`, `decide`, `evalTriple`, `Validate`.
- `pkg/model/gateways/gateway.go` — `forkTrueSubset` (the shared §2.9 split).
- `pkg/model/process/process.go` — the per-node `Validate` hook.
- `internal/instance/activation.go` — `guardEval`, `fail`.
- `internal/instance/track.go` — `synchronizeActivation`.
- `internal/instance/instance.go` — the `recheckJoin` `ActivationJoin` branch.
- `examples/complex-gateway/`.

### 10.3 Verification

- `make ci` green: lint, build, `-race`, diff-coverage **97.4%** (≥95), govulncheck.
- Tests: model-unit (Record/Recheck), in-package (`internal/instance`), registration
  (`Process.Validate`), and engine (`-race`): discriminator, partial-join, data-aware,
  required-gate, abort-on-death.
- `examples/complex-gateway` smoke exit 0; all 13 examples exit 0.

### 10.4 Deltas vs the draft

- **`Activate` → `Record` + loop `Recheck`.** The draft had the track call
  `Activate(eval, fc)` and decide. But `CheckFlows` is **loop-only** (a track read
  races `inst.tracks`, proven by `-race`). So the track only **Records**; the loop owns
  the whole fire/abort decision via `Recheck`. §3.3 + §4 reflect this.
- **Trailing tokens via `Record` → `firedAlready`** — the post-fire consume; the same
  pattern was mirrored into the OR-join by FIX-006.
- §6 test names settled during landing; §6/§10.2 list the actual names.

## Open questions

- **None.**
