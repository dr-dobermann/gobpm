# Link events — implementation kickoff brief

| Property | Value |
| :---- | :---- |
| **Status** | Superseded — **landed** (ADR-006 v.4 §2.8 + SRD-057, Accepted 2026-07-20) |
| **Date** | 2026-07-20 |
| **Author** | dr-dobermann |
| **Feeds** | a new **ADR-006 §2.8** (Link semantics) + **SRD-057** (the landing) |
| **Tracks** | GitHub epic **#90** (Compensation / Escalation / Cancel / Link events) |
| **Conformance** | `docs/bpmn-spec/conformance.md:61` — `LinkEventDefinition` (IntermediateCatch = target, IntermediateThrow = source) |

This is a **scoping brief**, not a spec. It captures the ground truth (what
exists, what's missing, the closest patterns) so the SRD/ADR authoring can start
evidence-first. It is a Living analytics artifact (EN-only), superseded once
ADR-006 §2.8 + SRD-057 are Accepted.

## 1. What Link events are

A **Link event** is an **intra-process GOTO**: an `IntermediateThrowEvent`
carrying a `LinkEventDefinition` (the **source**, filled marker) hands control to
a matching `IntermediateCatchEvent` carrying a same-**name** `LinkEventDefinition`
(the **target**, unfilled marker) in the **same process/sub-process level**.
It exists to avoid long sequence-flow lines and to express on-page loops /
"off-page connectors" — pure control-flow sugar, no data payload, no wait.

**This is not** a signal (no broadcast, no cross-instance reach), **not** a
message (no correlation, no external source), and **not** durable (it resolves
synchronously within one instance's own track set).

## 2. Spec grounding (authoritative)

- **Object model** — `docs/bpmn-spec/elements/event-definitions.md:192-211`:
  `LinkEventDefinition → EventDefinition → RootElement → BaseElement`. Own
  properties: `name: String [0..1]`, `target: LinkEventDefinition [0..1]`,
  `source: LinkEventDefinition [0..*]`.
- **Cardinality** — **many sources → one target** per link name: N throws
  (sources) resume the single catch (target) of that name. (`link.go` doc
  comment + the `[0..*]`/`[0..1]` above.)
- **Positions** — `docs/bpmn-spec/conformance.md:61`: **IntermediateThrow
  (source) and IntermediateCatch (target) only.** Link is **invalid** as a
  boundary event (`docs/bpmn-spec/semantics/event-handling.md:67`,
  `ADR-018:186`: "None and Link are invalid on a boundary"), and is **not** a
  valid event-based-gateway arm (`ADR-005:701`). No Start, no End, no boundary.
- **Scope** — "limited to a single Process level (cannot link a parent Process
  with a Sub-Process)" (`link.go` doc comment, from the spec). A throw resumes
  only a target in the **same** flow-elements container/instance.

## 3. Current state in code

**Exists:**
- The trigger constant — `pkg/model/flow/events.go:50`: `TriggerLink
  EventTrigger = "Link"` (defined, referenced nowhere else).
- A **bare, unwired model stub** — `pkg/model/events/link.go`: a
  `LinkEventDefinition` struct with exported bare fields (`Target`, `Name`,
  `Sources`) embedding `definition`, plus the spec doc comment. **No**
  constructor, **no** `Type()`, **no** getters, **no** conformance assertion
  (`var _ flow.EventDefinition = …`), **no** `_test.go`. Because it lacks
  `Type() EventTrigger`, it does **not** satisfy `flow.EventDefinition`
  (`pkg/model/flow/events.go:53-64`).
- Spec + conformance rows (above).

**Missing (the whole implementation):**
- Model: constructor + `Type()` + `Name()`/`Sources()`/`Target()` getters +
  name-required validation + conformance assertion + unit tests; unexported
  fields per the house pattern.
- `TriggerLink` is **absent** from both `intermediateThrowTriggers`
  (`pkg/model/events/intermediate_throw.go:20`) and `intermediateCatchTriggers`
  (`pkg/model/events/intermediate_catch.go:18`) — a Link throw/catch cannot be
  constructed today.
- No runtime resume path: `waiters.CreateWaiter`
  (`internal/eventproc/eventhub/waiters/waiters.go:54-78`) switches on
  `eDef.Type()` with only Timer/Message/Signal cases — a Link catch would hit
  the `default → ObjectNotFound` and hang forever (the exact class of gap
  SRD-048 §1 describes for Conditional).
- No governing ADR section, no SRD, no example.

## 4. Closest patterns to copy

| Layer | Copy from | Why |
|---|---|---|
| **Model** | `pkg/model/events/terminate.go` (skeleton: embed `definition`, `Type()`, `New…`) **+** the name-getter and `errs.CheckStr` name-validation of `pkg/model/events/signal.go:26-34,59-145` (incl. the `var _ flow.EventDefinition` assert at `signal.go:67` and the FIX-011 `GetItemsList` spelling lesson `signal.go:64-67,129-135`) | Link carries only a name, no `ItemDefinition` payload — `terminate.go` is the no-payload skeleton; `signal.go` adds the name + conformance assert |
| **Throw/catch allow-lists** | the two `set.New(...)` lists — add `TriggerLink` to `intermediate_throw.go:20` and `intermediate_catch.go:18` | mechanical; mirrors how every trigger is enrolled |
| **Runtime resume** | `internal/instance/conditional.go` (SRD-048's **loop-local** path — bypasses the EventHub entirely) as the primary template, cross-referenced with the **passive name-matched** shape of `internal/eventproc/eventhub/waiters/signal.go:21-30,44-100` | Link is synchronous, intra-process, name-keyed, no external source — like a signal in matching but like Conditional in *staying inside the instance loop* |
| **Doc** | `docs/srd/SRD-048-conditional-events.md` section skeleton (§1 Background → §10 Impl-summary; M-per-commit milestones; §9 DoD) | the most recent event SRD; Conditional is the nearest lifecycle sibling |

## 5. Recommended design direction (to be decided in ADR-006 §2.8 / SRD-057)

The scout's finding is decisive: **do NOT route Link through the EventHub's
cross-instance broadcast** (that is signal's semantics, and it is exactly what
Link must not do). The likely-correct shape:

- **Name-keyed match confined to the throwing instance's own scope** — a
  Link throw resolves the same-name catch within the same instance/container,
  resuming its track. This mirrors **Conditional's loop-local** resolution
  (`internal/instance/conditional.go`), not a hub fan-out.
- **Synchronous, passive, no goroutine, no external edge** — like the
  `signalWaiter` (`done` closed at construction, fired only by an in-process
  throw), but scoped to the instance rather than broadcast.
- **Many-throws → one-catch by name** — validate at registration that a name
  has exactly one target (catch) in scope; multiple sources (throws) are legal.
- **Scope confinement** — a throw must not resume a target in a parent or child
  sub-process level (spec §-cited above). The container boundary is the match
  horizon; decide how this interacts with the embedded/event sub-process scope
  levels (SRD-049/052/053).

Open questions to settle in the SRD:
1. **Where does the match live** — a loop-local index on the instance (like
   Conditional), or a scoped waiter registered through `Instance.RegisterEvent`
   (`internal/instance/eventproducer.go:18-56`)? Loop-local is favoured.
2. **Same-level enforcement** — how a sub-process's own Link namespace is
   isolated from the parent's (one index per flow-elements container?).
3. **Unmatched throw** — a Link throw with no same-name catch in scope: error at
   validation (preferred, deterministic) vs no-op at runtime.
4. **Loops** — Link's canonical use is on-page loops; confirm the resume path is
   re-entrant (a target caught, flowed, and re-thrown) without token leaks.
5. **`SubscriptionKey()` generalization** — Link is the **second** name-keyed
   event type after Signal; SRD-020/026 deferred the polymorphic
   `SubscriptionKey()` unification *until Link lands*. Decide whether SRD-057
   also lands that generalization or leaves it as a follow-up (see
   `docs/backlog.md` "Event-matching generalization").

## 6. Proposed work breakdown (milestone sketch, each = one commit, `make ci` green)

1. **M1 — model.** `LinkEventDefinition` reshaped: unexported fields,
   `NewLinkEventDefinition(name, opts…)` + `Must…`, `Type() → TriggerLink`,
   `Name()`/`Sources()`/`Target()` getters, name validation, `var _
   flow.EventDefinition` assert; `link_test.go` + the `edef_test.go` table row.
2. **M2 — throw/catch enrolment.** Add `TriggerLink` to both trigger
   allow-lists; construct-time validation for a Link throw/catch;
   `intermediate_throw_test.go`/`intermediate_catch_test.go` cases.
3. **M3 — runtime resume.** The loop-local, name-keyed, same-instance match +
   resume (the design of §5); the unmatched/duplicate-name guards.
4. **M4 — e2e + example.** `pkg/thresher/link_events_test.go` (throw→catch GOTO,
   incl. an on-page loop); `examples/link-events/` (self-contained module, the
   `examples/signal-broadcast/` layout); conformance-status row flip; changelog;
   README + guide sync.

## 7. Doc & tracking hooks

- **ADR-006 §2.8** — Link's intra-process semantics (its next free section;
  §2.7 is Conditional). Author concept-first, standard-grounded, before SRD-057.
- **SRD-057** — the landing (next free SRD number; 054 = Standard Loop is the
  current max). Mirror SRD-048's skeleton.
- **Epic #90** — tick the Link item on landing (`SAD-001:447`,
  `conformance-status.md:82`).
- **`docs/backlog.md`** — the "Event-matching generalization" item's trigger is
  Link landing (corrected 2026-07-20 — Link had *not* landed; the backlog note
  wrongly claimed it had).
- **Roadmap** — Link sits in **WS-C6 / milestone M4** (full conformance); this
  brief is linked from §2.2.

## 8. Bottom line

Greenfield execution design on a solid substrate: the trigger constant and a
spec-faithful (if unwired) model stub exist, the conformance/spec rows are
pinned, and there are close, proven patterns for every layer. The one real
design decision is **loop-local intra-instance name matching** (like Conditional)
rather than **hub broadcast** (like Signal) — everything else is mechanical
enrolment following the established event pattern.
