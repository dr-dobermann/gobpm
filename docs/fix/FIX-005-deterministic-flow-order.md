# FIX-005 «Non-deterministic Outgoing/Incoming order makes the Exclusive first-true split random»

**Type:** FIX (one-shot bug-fix; not rewritten after landing).
**Status:** Accepted v.1 (2026-06-19, branch `feat/routing-gateways`, implemented).
**Date:** 2026-06-19.
**Author:** Ruslan Gabitov.
**Branch:** `feat/routing-gateways` (lands with the gateway routing it makes conformant — SRD-021/SRD-022; same PR. The first-true Exclusive split is only correct once flow order is deterministic).
**Paired doc:** [SRD-021 v.1](../srd/SRD-021-exclusive-inclusive-split.md) (the Exclusive first-true split this makes deterministic).
**Upstream:** [ADR-005 v.2 §2.8](../design/ADR-005-gateways-and-joins.md) (first-true, "conditions in declared order"); BPMN 2.0 §13.4.2.

**Grounded in (internal artifacts):**
- Verified at `e2f2fbe` (branch `feat/routing-gateways`). `pkg/model/flow/node.go:98,137,142`.
- Flakiness measured: `go test -run TestExclusiveGatewayExec/first_true -count=1` over 30 runs → **6 failures** (~20%).

## §1 Symptoms

### §1.1 The Exclusive first-true split returns a random winner on condition overlap

When two outgoing flows of an exclusive gateway both have a true condition, the
"first-true" rule (ADR-005 §2.8) must take the one declared first. Instead it
takes an arbitrary one, varying run to run. The regression test
`TestExclusiveGatewayExec/first_true_wins_on_overlap` (`exclusive_test.go`) is
consequently flaky:

```
--- FAIL: TestExclusiveGatewayExec/first_true_wins_on_overlap
    exclusive_test.go:188:
        []*flow.SequenceFlow{0xc0000c7e00} does not contain &flow.SequenceFlow{...id:"…"}
```

6/30 runs fail. In code: `ExclusiveGateway.Exec` (`exclusive.go`) iterates
`eg.Outgoing()` and returns the first flow whose condition is true — but
`Outgoing()` has no defined order (see §2), so "first" is random. BPMN §13.4.2
requires the conditions be "evaluated in [declared] order"; the landing is
non-conformant.

## §2 Root cause analysis

### §2.1 `Outgoing()`/`Incoming()` iterate a map → undefined order

`BaseNode` stores flows in a map and returns the map's values:

```go
// pkg/model/flow/node.go:98
flows map[data.Direction]map[string]*SequenceFlow
// :137
func (fn *BaseNode) Incoming() []*SequenceFlow { return maps.Values(fn.flows[data.Input]) }
// :142
func (fn *BaseNode) Outgoing() []*SequenceFlow { return maps.Values(fn.flows[data.Output]) }
```

`maps.Values` over a Go map yields entries in **randomized** iteration order. So
`Outgoing()` returns a different order each call/run. Every consumer that cares
about order — the **Exclusive first-true** split (§2.8) above all — is affected;
the **Inclusive** subset (§2.9) and the OR-join (SRD-022) iterate too and benefit
from a stable order. `AddFlow` (`node.go:146`) records insertion but the map
discards that order.

### §2.2 The test asserts a specific winner → flaky, not a hard signal

`first_true_wins_on_overlap` links flow A then flow B (both true) and expects A.
It passes only when the map happens to yield A first — so the defect surfaced as
intermittent CI flakiness, not a deterministic failure, and slipped through
SRD-021's landing.

## §3 Solution

### §3.1 Alternatives considered

| Alternative | Pros | Cons | Decision |
|---|---|---|---|
| A. Sort `Outgoing()` by flow id | deterministic | **not declaration order** — BPMN §13.4.2 wants declared order; id is not declaration | ❌ rejected |
| B. A single per-direction declaration-ordered slice (`map[Direction][]*SequenceFlow`) | one structure, naturally ordered, overwrite-safe; `Outgoing` is a single clone with no per-flow indirection | dedup is O(n) on add — but a node has only a handful of flows, so it is irrelevant | ✅ chosen |
| C. Keep the id-map + a parallel `[]id` order slice | O(1) dedup | **two structures to keep in sync**; `Outgoing` then pays a map lookup per flow to rebuild order; the map's only remaining job is the dedup check | ❌ rejected (redundant — O(1) dedup buys nothing at this scale) |

### §3.2 Changes by file

#### §3.2.1 `pkg/model/flow/node.go` — declaration-ordered flows

Hold each direction's flows in a single **declaration-ordered slice**.
`Outgoing()`/`Incoming()` return a defensive copy (`slices.Clone`, keeping the
internal slice unexposed as `maps.Values` did). `AddFlow` appends a new flow, or
overwrites in place when its id is already present (keeping its position). Drop
the inner id-map and the now-unused `golang.org/x/exp/maps` import (add `slices`).

```go
// before:
type BaseNode struct {
	flows map[data.Direction]map[string]*SequenceFlow
	BaseElement
}
func (fn *BaseNode) Outgoing() []*SequenceFlow { return maps.Values(fn.flows[data.Output]) }

// after:
type BaseNode struct {
	flows map[data.Direction][]*SequenceFlow
	BaseElement
}
func (fn *BaseNode) Outgoing() []*SequenceFlow { return fn.ordered(data.Output) }
// ordered returns a copy of the direction's flows, already in declaration order.
func (fn *BaseNode) ordered(dir data.Direction) []*SequenceFlow {
	return slices.Clone(fn.flows[dir])
}
```

`AddFlow` scans the direction's slice: on a matching id it overwrites in place;
otherwise it appends (`append` also covers the nil-slice case, so the old
"create the inner map if absent" step is gone).

## §4 Verification

Current coverage in the test dir:
- unit: `node` flow add/get tested, but **no order assertion** (the gap).
- the gateway first-true test exists but is flaky (the symptom).

### §4.1 Regression tests (mandatory)

#### §4.1.1 `TestBaseNodeFlowOrder`

**New:** `pkg/model/flow/node_test.go` (or the existing node test file).

| Test | Setup | Assertion |
|---|---|---|
| `TestBaseNodeFlowOrder` | add flows f1, f2, f3 (in/out) | `Outgoing()`/`Incoming()` return them in **add order**, stably across repeated calls; re-adding an id keeps its position and updates the value |

#### §4.1.2 De-flake the gateway test

`TestExclusiveGatewayExec/first_true_wins_on_overlap` becomes deterministic
(declaration order ⇒ the first-linked flow always wins): `go test -run … -count=50`
→ **0 failures**.

## §5 Prevention

- Doc-comment `Outgoing()`/`Incoming()` with the **declaration-order guarantee** and
  note that the gateway first-true/subset rules depend on it.
- `TestBaseNodeFlowOrder` is the canary — if it falls, ordering regressed and the
  gateway splits become non-deterministic again.

## §6 Regressions / side-effects

### §6.1 What may rely on the old behaviour

Nothing can rely on a *specific* order today (it was random). Consumers either
iterate all flows (order-agnostic) or want first-true (the gateways — fixed by
this). Audit: `grep -rn '\.Outgoing()\|\.Incoming()' pkg internal` — confirm each
hit is order-agnostic or a gateway. No behavioural regression expected; the change
only *adds* a guarantee.

### §6.2 Rollback path

Single-commit revert of `node.go` (+ test).

## §7 Related

- [SRD-021 v.1](../srd/SRD-021-exclusive-inclusive-split.md) (Exclusive first-true / Inclusive subset — the consumers made deterministic).
- [ADR-005 v.2 §2.8/§2.9](../design/ADR-005-gateways-and-joins.md); BPMN §13.4.2.
- [SRD-022 v.1](../srd/SRD-022-inclusive-or-join.md) (the OR-join, also iterates flows — benefits from stable order).

## §8 Implementation summary (stage-by-stage actual landings + deltas vs draft)

### §8.1 Stages by commit (branch `feat/routing-gateways`)

| Stage | Commit | Scope |
|---|---|---|
| doc | `76ea19b` | FIX-005 |
| fix | `36f3864` | `BaseNode.flows` → a per-direction declaration-ordered slice; `AddFlow` appends/overwrites-in-place; `Incoming`/`Outgoing` return `slices.Clone`; `maps` import dropped. `TestBaseNodeFlowOrder`. |

### §8.2 Empirical findings — where reality diverged from the §3 draft

**Single ordered slice, not map+order.** The §3.1 draft first chose a map plus a
parallel order slice (option B); on review that was redundant (two structures to
sync, plus a per-flow map lookup in `Outgoing`). It was reworked to a single
`map[Direction][]*SequenceFlow` before landing — `ordered` is one `slices.Clone`.
§3.1/§3.2 were corrected to reflect this.

**Result.** `first_true_wins_on_overlap` went from 6/30 flaky to 0/50;
`make ci` green (diff-coverage 100% on the touched lines).

## §9 Open questions

None.
