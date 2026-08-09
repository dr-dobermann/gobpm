# SRD-085 — In-instance delivery: per-delivery payload binding and correlated routing

| Field | Value |
|---|---|
| Status | Accepted (2026-08-08) |
| Date | 2026-08-08 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-006 v.5](../design/ADR-006-events-and-subscriptions.md) §2.9 (the in-instance delivery contract) |
| Upstream | [ADR-014 v.2](../design/ADR-014-message-handling.md) §2.5/§2.6 (the waiter/key seam; v.2 is this landing's wording sync), [ADR-016 v.1](../design/ADR-016-message-correlation.md) (the key machinery), [ADR-025 v.2](../design/ADR-025-activity-iteration-loop-and-multi-instance.md) §2.6 (the split item a key derives from) |
| Related | [SRD-082](SRD-082-checkpoint-composite-fidelity.md) (whose parallel-restore tests surfaced the slot race) |
| Tracking | #305 |

Lands ADR-006 v.5 §2.9 for the constructs that exist today: the payload
moves off the shared catch node onto the delivery, and a message routes
to the **correlated** waiter instead of the last-registered one.

## §1 Background (verified)

- **The shared payload slot.** `catchEvent.received` +the package-level
  `recvMu` (`pkg/model/events/event.go:282-314`): every delivery
  overwrites one cell on the node all iterations share; `UploadData`
  (`event.go:404-426`) later binds from it — an unbounded window in
  which a sibling's delivery replaces the payload. The guarding
  comment names this SRD's issue verbatim ("the payload-routing
  semantics of that node sharing are a follow-up issue").
- **The routing index holds ONE track.** `msgIdx map[string]*track`
  (`internal/instance/loop.go:38-42`): a second track registering the
  same message definition **overwrites** the first — the earlier
  iteration's wait becomes undeliverable, silently.
- **The key seam exists.** The message waiter gathers per-subscription
  keys from processors implementing `CorrelationKeys() []string`
  (`waiters/message.go:194-206`); the hub queues processors and
  removes exactly one on unregister (`message.go:140-167`).
- **Delivery is already per-track downstream.** `track.deliver`
  (`internal/instance/track.go:1661`) runs on the receiving track's
  goroutine with the fired `eDef` in hand — the natural capture point
  the ADR names.

## §2 Requirements

- **FR-1 — the frame carries the payload (ADR-006 v.5 §2.9.1).**
  `track.deliver` captures the fired definition's item
  (`msgflow.CaptureItem(eDef)`) onto the track; the execution frame the
  track opens for the node carries it (`Frame.SetReceived`/
  `Received`); `catchEvent.UploadData` binds from the frame. The
  `received` slot and `recvMu` are **removed**. Every catch position
  binds this way: intermediate catch, Event-Based-Gateway arm (the
  winning arm's upload), boundary exception flow, Event-Sub-Process
  start, and the wake/continuation path (which preloads `evtCh` and
  rides the same `deliver`).
- **FR-2 — multi-subscription routing (§2.9.2/§2.9.4).** `msgIdx`
  becomes `map[string][]msgSub` — an ordered subscription list of
  `(track, key)`; registration appends, unregistration removes exactly
  that pair, delivery serves **exactly one** subscription: the one
  whose key matches the envelope's correlation value; with a single
  keyless subscription, today's direct route is unchanged.
- **FR-3 — the iteration key (§2.9.3).** A message catch may declare
  `events.WithIterationCorrelation(keyName, expr)`: `keyName` names a
  **declared** process-level CorrelationKey — its retrieval
  expressions derive the envelope-side value (`msgflow.DeriveKey`) —
  and `expr`, a `FormalExpression` evaluated at **registration** over
  the registering track's scope (where ADR-025 v.2 §2.6 has bound the
  split item), produces the subscription-side value. The value joins
  the track's `CorrelationKeys()` contribution, so the hub-side
  subscription filter and the loop-side routing read one declared
  value; an iteration-granular key is **excluded from conversation
  gating** (`validateAndAssociate` skips it — one instance
  legitimately holds N values of it). Two latent gaps surfaced and
  fixed while wiring this ("no pre-existing errors"): the engine never
  forwarded the hub's `AddEventKey` capability, so an instance under
  the engine could never extend a live broker subscription (the
  conversation flow's extension silently no-opped too); and
  `extendReceivers` walked only TOP-LEVEL nodes, silently missing
  every message catch inside a composite body.
- **FR-4 — the ambiguity refusal (§2.9.3).** Registering a second
  subscription for the same message definition when either lacks a key
  is a classified, self-identifying error — the track faults loud; no
  arbitrary pick.
- **FR-5 — signal fan-out pinned (§2.9.2).** N inner tracks of a
  parallel MI waiting on one signal all wake on one broadcast, each
  binding **its own** delivery's payload — the regression test the
  slot made impossible to write.
- **NFR-1** — race-clean under `-race`; diff-coverage ≥95% (aim 100%).

## §3 Models

```go
// internal/instance/loop.go (SRD-085)
type msgSub struct {
    track *track
    key   string // "" = keyless (single-subscription only, FR-4)
}
// msgIdx map[string][]msgSub

// pkg/exec/frame.go (the frame seam, FR-1)
// Frame gains: SetReceived(*data.ItemDefinition) / Received() *data.ItemDefinition
```

**Worked trace.** A parallel MI over items `["a","b"]`, body = message
catch `confirm` with `WithIterationCorrelation(item)`. Fan-out opens
two scopes; each inner track registers `confirm` with its key (`"a"`,
`"b"`) — the waiter's queue holds two subscriptions, `msgIdx["confirm"]`
two entries. An envelope correlating `"b"` arrives: the waiter fires
the instance processor, the loop matches key `"b"` → track 2, whose
`deliver` captures THAT envelope's payload into its frame; iteration 2
binds it and completes. Iteration 1 keeps waiting; a later `"a"`
envelope serves it identically. Kill/restore between the two
deliveries replays the same registrations (the keys re-derive from the
restored scopes' items).

## §4 Analysis & decisions

- **Capture in `deliver`, not in a node method.** The track goroutine
  holds the fired `eDef` and owns the frame — capture is one
  assignment there; keeping `catchEvent.ProcessEvent` as the capture
  point would keep node-resident state in some form. The node method
  stays only where non-payload behavior needs it.
- **Key at registration, not at delivery.** The iteration's item is
  stable for the iteration's life; evaluating once at registration
  gives the hub a plain value to match (the existing
  `subscriptionKeys` shape) instead of a per-envelope callback.
- **Refuse on the second keyless subscription, not the first.** A
  single keyless waiter is today's common case and unambiguous;
  ambiguity begins with the second concurrent subscription — refusing
  there keeps existing single-catch models untouched (FR-4).
- **The loop routes; the hub filters.** The hub-side key narrows what
  the broker delivers (ADR-016 v.1); the loop-side match picks the
  track. Both read the same declared value, so they cannot disagree.

## §5 API deltas

| Surface | Change |
|---|---|
| `events.WithIterationCorrelation(expr)` | added (message catch option) |
| `exec.Frame` | + `SetReceived`/`Received` (payload carrier) |
| `catchEvent.ProcessEvent` payload capture | removed (slot + `recvMu` deleted) |

## §6 Test scenarios

| # | Test | Verifies |
|---|---|---|
| T-1 | two-iteration payload integrity (`internal/instance`) | FR-1: concurrent deliveries bind their own payloads — the slot race pinned |
| T-2 | correlated routing (`internal/instance`) | FR-2/FR-3: the §3 worked trace — out-of-order envelopes serve the matching iterations |
| T-3 | keyless ambiguity refusal (`internal/instance`) | FR-4: the second keyless subscription faults loud, naming the definition |
| T-4 | signal fan-out (`internal/instance`) | FR-5: one broadcast wakes all iterations, each with its own payload |
| T-5 | single keyless catch unchanged (`internal/instance`) | FR-2: the existing single-waiter path behaves as today |
| T-6 | catch-position sweep (`internal/instance`, `pkg/thresher`) | FR-1: every position that binds — EB-gateway arm, ReceiveTask, the wake path — binds from the frame (the existing gateway/dehydration suites are the net); boundary/event-sub never bound (the FR-1 recorded gap) |
| T-7 | kill/restore mid-trace (`pkg/thresher`) | the §3 trace across a crash — keys re-derive, the second envelope routes correctly |

## §7 Milestones

- **M1 — the payload moves to the delivery.** FR-1; T-1, T-6.
  `feat(instance): the payload binds per delivery — the shared catch slot retires (SRD-085 M1)`.
- **M2 — correlated routing.** FR-2, FR-3, FR-4; T-2, T-3, T-5.
  `feat(instance): iteration-correlated message routing (SRD-085 M2)`.
- **M3 — the pins + docs.** FR-5; T-4, T-7; guides (MI guide's
  event-catch note, messages guide), CHANGELOG, ADR-014 v.2 wording
  sync (§2.5, with approval).
  `feat(instance): the multiplicity pins; docs (SRD-085 M3)`.

## §8 Cross-doc

- Implements **ADR-006 v.5 §2.9**. Upstream: **ADR-014 v.1** §2.5/§2.6,
  **ADR-016 v.1**, **ADR-025 v.2** §2.6.
- **#305**: closes it (the leaf-MI realization is the accompanying
  SRD-086's scope and issue).

## §9 Definition of Done

- [x] FR-1…FR-5 implemented; every §6 test exists and passes.
- [x] `make ci` green; diff-coverage ≥95% (aim 100%); touched
      functions ≥80%.
- [x] No `received`/`recvMu` remains; the guides note the correlation
      option.
- [x] §10 filled.

## §10 Implementation summary

Landed on `feat/composite-followups` in three milestones — doc
`a27e07e`, M1 `e065c6f` (the frame carrier; both slots deleted; the
compensation sentinel's nil-method fix; the boundary/event-sub
never-bound reality recorded), M2 `d811fff` (msgSub routing, the
option, the conversation-gating exclusion, the engine's AddEventKey
passthrough and the recursive extension walk — both latent gaps fixed
under "no pre-existing errors"), M3 `8f8c53c` (the kill-and-resume
trace with the cuttable-broker crash shape; guides; ADR-014 v.2
wording sync; CHANGELOG).

Verification: `make ci` exit 0 end to end; **diff-coverage 95.9% of
366 changed coverable lines** (min 95%); every touched function ≥85%
in-package, most at 100%; all suites race-clean; golangci-lint incl.
tests 0 issues.

Deviations: FR-3 gained the `keyName` half (the declared key pairs the
envelope-side retrieval with the iteration-side expression) — folded
into the Draft when the derivation seam made it the natural shape.

## Open questions

*None — §4 records the resolved design points (capture locus, key
timing, refusal boundary, hub-vs-loop division).*
