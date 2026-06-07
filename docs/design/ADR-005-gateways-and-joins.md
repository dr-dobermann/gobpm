# ADR-005 — Gateways & Joins

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-06-07 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-001 v.3 Execution Model](ADR-001-execution-model.md) |

> **Draft — not yet implemented.** This ADR is the home for the gateway/join
> conception relocated out of ADR-001 (which scopes itself to the built runtime
> core). It is authored in full when the gateway workstream lands, with its own
> SRD and code in the same branch. Nothing here is implemented yet.

## 1. Context

ADR-001 defines the in-memory runtime: an Instance owning tracks, fork creating
a new track per extra outgoing flow, and the generic `context` cancellation
cascade. It deliberately does **not** define what happens at a node with **more
than one incoming flow** (a join/merge) or how a gateway's *type* selects which
outgoing flows activate at a fork. Those are this ADR's scope.

The runtime today has **no join accounting** and **no gateway types**: a fork
node activates all outgoing flows (uncontrolled split), and a node reached by
several tracks is simply executed once per arrival.

## 2. Decision (seed — to be expanded)

### 2.1 Join mechanics (relocated from ADR-001 §4.5)

A **join point** is any FlowNode with N>1 incoming flows (gateway or activity
uncontrolled merge). The Instance applies the join rule **by merge type**:

- **Synchronizing — Parallel** (wait for all expected) / **Inclusive** (wait
  for the expected reachable subset): once satisfied, the join **consumes** the
  arrived tokens and continues on **one** track — the first-arrived track
  survives and advances on the outgoing flow; the others **end** (`TrackMerged`),
  their token projections reading `Consumed`.
- **Non-synchronizing — Exclusive**, or any node with N>1 incoming flows
  (uncontrolled merge): **no wait, no consumption** — **each** arriving track
  passes straight through and continues independently. Several tracks
  legitimately cross the same node; this is a merge, not a fork.

No new track is created at a join — continuation always rides an arriving track.
The 1:1 track:position discipline holds throughout. Token **consumption** is
not a generic join outcome: tokens are consumed at End Events and Terminate, as
the merged-away tokens of a synchronizing join, and on withdrawal — never by a
non-synchronizing merge.

### 2.2 Fork-flow activation by gateway type (relocated from ADR-001 §4.4)

The fork node's type (parallel / inclusive split / activity uncontrolled split)
selects which outgoing flows activate (per
[gateways.md](../bpmn-spec/semantics/gateways.md)); only activated flows
participate. ADR-001's fork mechanic (parent continues on F1, one new track per
extra activated flow) is unchanged; this ADR defines *which* flows count as
active per gateway type.

### 2.3 Inclusive (OR-join) — must pin one semantics

The converging Inclusive Gateway's synchronization is conditional and
**non-local**: per BPMN ("a token *MAY* be synchronized with some other tokens
that arrive later", spec p.291 / KB §13.4.3), a token waits only for incoming
flows that *could still* receive a token — **not** "wait for all". This is the
hardest join to get right; the join coordinator must support conditional
subset-waiting via graph-reachability (ComplexGateway's reset phase reuses the
same test). The standard's OR-join treatment is **acknowledged-ambiguous** in
the literature (the spec's *global* upstream-reachability definition vs more
efficient *local* characterizations; published formalizations diverge) — so this
ADR must **pin one compliant semantics**, not assume.

### 2.4 Event-Based Gateway & the `Withdrawn` token state

Event-Based Gateway is not a join — it has one inbound flow and races on the
outbound side; race-loss siblings end as `TrackCanceled` with **end-reason =
withdrawn**. The `TokenWithdrawn` projection value already exists in the runtime
(`token.go`) with **no producer**; this ADR introduces its producer (the track
end-reason set on Event-Based Gateway race loss).

## 3. Known issue inherited from the runtime

A non-synchronizing merge (multiple tracks crossing one node) currently triggers
a **data race**: node execution calls `NodeDataLoader.RegisterData`, which
**mutates the shared node** (e.g. `EndEvent.dataPath`). This violates ADR-001
§4.7's "node definitions MUST stay immutable". The fix — a per-node state /
data-loading contract that does not mutate the shared definition — is owned by
the **Persistence & State ADR** (§4.7). Until it lands, non-synchronizing merge
over a shared node is not race-safe, and ADR-001's §7 gate does not claim it.

## 4. References

- [ADR-001 v.3 Execution Model](ADR-001-execution-model.md) — the runtime this refines.
- [docs/bpmn-spec/semantics/gateways.md](../bpmn-spec/semantics/gateways.md), [token-flow.md](../bpmn-spec/semantics/token-flow.md) — normative gateway/token semantics.

## Document History

| Version | Date | Author | Change |
|---|---|---|---|
| v.1 | 2026-06-07 | Ruslan Gabitov | Initial Draft seed — gateway/join conception relocated from ADR-001 v.3 (§4.4 flow-activation, §4.5 join mechanics, OR-join note, Event-Based Gateway/withdrawn). Not yet implemented. |
