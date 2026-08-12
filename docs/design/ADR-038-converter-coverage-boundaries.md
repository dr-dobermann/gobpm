# ADR-038 — Converter coverage boundaries and their extension points

| Field | Value |
|---|---|
| Status | Draft |
| Version | v.1 |
| Date | 2026-08-12 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-024 v.4 Process Interchange Converters](ADR-024-process-interchange-converters.md) |

> **Scope.** This decides what the converters do at the edge of their coverage:
> which refusals are a schedule, which are a **standing** property of the
> engine, and which name a missing **model capability** that a later change
> would supply. It refines [ADR-024 v.4](ADR-024-process-interchange-converters.md),
> which fixed the disposition vocabulary and the report contract, and adds the
> rule that decides which disposition an element gets and what a refusal owes
> its reader. It touches neither the element mapping itself nor the model's
> execution semantics.

## 1. Context

### 1.1 What ADR-024 already fixed

[ADR-024 v.4](ADR-024-process-interchange-converters.md) §2.9 makes refusal the
**default** disposition — an element no table claims is reported unsupported,
so absence from a mapping can never mean silent acceptance. §2.13 separates *not
supported yet* (blocked elsewhere; the same file imports unchanged once that
lands) from an unsupported shape, and §2.14 requires a recognized construct the
import does not map to reach the host through the report rather than vanish.

### 1.2 What it did not decide

Those dispositions describe **what the converter does**. They do not say **why
an element is in one bucket rather than another**, and by the end of the
element-coverage work three different situations had collapsed into "refused":

1. The converter had not reached the element yet — a schedule, nothing more.
2. The element is executable and the document expresses it, and the **model**
   has no way to accept what the document carries.
3. The element is executable and **no document can ever express it**, because
   its constructor takes a Go value — a closure — that XML cannot carry.

The second and third look identical to a reader of the error and are opposite
in meaning. The second is a work item with a known unblocking change; the third
is permanent, and treating it as a gap invites someone to "finish" it.

### 1.3 The worked example

`<association>` is two elements wearing one tag, and today a single refusal
covers both.

As a **compensation link** it carries execution semantics — it names the
activity a compensation boundary event routes to, which is why the vendored
extract keeps it in scope while calling the surrounding artifacts "pure visual"
(`docs/bpmn-spec/conformance.md` line 166). The model has the constructor for
it; the converter has simply not reached the element, and a boundary event
carrying a compensation trigger is refused today by naming exactly that
(`pkg/convert/bpmn/dispatch.go:623`). That is a **schedule**.

As a **plain link** — from a `<textAnnotation>` to the activity it comments on —
it carries nothing executable, and it is refused for an unrelated reason.
`pkg/model/artifacts` declares `Association` as a struct of exported fields with
no constructor, and no package outside it imports the package at all;
`process.Process` holds properties, roles, nodes, flows, data objects, data
store references and lane sets, and has nowhere to put an artifact, though the
standard gives it `artifacts 0..*` (`docs/bpmn-spec/elements/process.md` line
24). The element is expressible, the converter parses it perfectly well, and the
**model** cannot accept the result. That is a **capability**.

Meanwhile the annotation that plain link points at is already `skipped` —
dropped as visual, the imported definition meaning the same without it. Two
halves of one diagram feature, three dispositions between them, and nothing
records why: the comment at the refusal site calls the whole tag "mapped work
for the composites stage", which is true of the compensation half and false of
the other.

## 2. Decision

### 2.1 A converter never compensates for a missing model capability

When the model cannot accept what a document carries, the converter **reports
and refuses**. It does not grow a private parser, a private router, a private
type or a second copy of a model rule to close the gap itself.

This is the same rule that keeps position and trigger validation in the model:
two implementations of one rule diverge, and when they do, the converter's copy
wins at import while the model's wins at run time — the worst possible split. A
converter-local parser, or a converter-minted type standing in for one the model
declines to expose, is that split in a different costume, and the model layer
would have to supersede it later while keeping the converter's behaviour intact.

The rule cuts the other way too, and that is the more common case: before
declaring a capability missing, look for the model's own way in. A timer reaches
the engine through the model's ISO 8601 constructors and the public `pkg/iso8601`
grammar rather than through anything the converter disassembles itself — and the
first draft of this record cited that same timer as capability-blocked, on a
limit that a sibling constructor already removed. §2.5's obligation to **name**
the capability exists partly to force that check: a capability you cannot name
precisely is usually one you have not looked for.

The cost is accepted deliberately: coverage lags the executed element set, and
some legal documents are refused by an engine that could execute them if built
in Go.

### 2.2 Three boundary classes

Every construct outside current coverage is exactly one of the three below. The
unit classified is the **construct, not the tag**: `<association>` is one tag
holding a scheduled compensation link and a capability-blocked plain one, and a
class assigned to the tag would have to be wrong about one of them.

- **Staged** — mapped work not yet reached. Not a boundary; it needs no record
  beyond the plan that schedules it.
- **Capability-blocked** — executable, expressible in a document, and blocked
  by an absent model capability. This is the class §2.3 registers. Its refusal
  **names the capability**, because that name is the specification of the work
  that removes it.
- **Standing** — the constructor's argument is a Go value no document can
  carry. A complex gateway's activation is per-incoming-flow token counts
  where the document holds a Boolean expression; an ad-hoc sub-process is
  entered by a host-supplied `Router` (see [ADR-035 v.1](ADR-035-adhoc-sub-process.md))
  which a file cannot contain. These never become extension points, and their
  refusal says what to do instead — build the element programmatically.

A standing boundary is **not** a defect and must not be re-filed as one. A
capability-blocked boundary is a work item and gets an issue.

### 2.3 The register

Each capability is a `pkg/model` change; each unblocks converter coverage that
is otherwise complete.

| Missing capability | What it unblocks | Tracking |
|---|---|---|
| An artifact collection on `Process`, and a constructor for `artifacts.Association` | the plain `<association>`, and with it the annotated diagrams whose annotation is already skipped but whose link is refused | [#323](https://github.com/dr-dobermann/gobpm/issues/323) |
| Transaction coordination attributes | `protocol` and `method` on a transaction sub-process | [#324](https://github.com/dr-dobermann/gobpm/issues/324) |
| A callable-resolution seam | `calledElement` beyond a literal key, and the whole GlobalTask family | [#325](https://github.com/dr-dobermann/gobpm/issues/325) |

A payload structure named by `itemRef` or `structureRef` is deliberately absent
from this table. It resolves into an external XSD or WSDL the converter neither
has nor can fetch, and an item definition needs a Go value — so it is closer to
standing than to capability-blocked, and it stays reported rather than
scheduled until a concrete need arrives.

### 2.4 A capability lands before the row that consumes it

An extension point is a model change and carries a model change's obligations:
its own decision record where it alters a contract, and its own landing
document. The converter row that consumes it is a one-line follow-up
afterwards — never the vehicle for the capability itself.

This ordering is what makes §2.1 affordable. A converter forbidden from
compensating locally would otherwise have to either wait indefinitely or
smuggle the capability in as "just a converter detail", and the second is how a
model gap becomes two model gaps.

### 2.5 What each boundary owes its reader

- A **capability-blocked element** is refused, naming the missing capability
  and offering the alternative that exists today (build it in Go). The refusal
  is actionable to a modeller *and* legible as a work item.
- A **capability-blocked attribute** is reported through the ADR-024 §2.14
  contract, because the element around it still imports — the document
  survives, minus a datum, and the host is told which.
- A **standing boundary** is refused with the reason and the programmatic
  route, and says nothing about waiting, because nothing is coming.

The distinction matters most where the same word would otherwise serve both: a
reader who cannot tell "not yet" from "not ever" will either wait for something
that will not arrive or rebuild something that is already correct.

## 3. Consequences

**The refusals become the backlog.** Coverage work stops being a survey of the
standard and becomes a queue with named prerequisites: every capability in §2.3
was discovered by an element refusing to import, and each names exactly what
would let it through.

**The model stays the single source of execution rules.** No converter carries
a shadow parser, a shadow type or a shadow copy of a position rule, so there is
no second implementation to diverge and no import-versus-runtime split.

**Coverage lags the executed element set, visibly.** A file whose only
unsupported content is a comment attached to a task is refused — not for the
comment, which is skipped, but for the line drawn to it. That is the accepted
price of §2.1, and §2.3 is what keeps it from being mistaken for neglect.

**A standing boundary is stable.** Ad-hoc routing and complex-gateway
activation will not reappear as bugs, because the record says why they are not.

## 4. Alternatives considered

**Let the converter implement what it needs locally.** Rejected. It is the
import-versus-runtime split of §2.1, and every local implementation becomes
something the model layer must later supersede while keeping the converter's
behaviour intact.

**One disposition for everything outside coverage.** Rejected — it is the state
§1.2 describes. It reads identically for a two-line follow-up and for a
permanent property of the engine, and the reader cannot tell which they have.

**Track the boundaries only as issues, with no decision record.** Rejected.
Issues record *what* to do; they do not survive as the rule that decides which
bucket the next boundary falls into, and without the rule the buckets drift back
together — as they did with `<association>`, where a source comment and the
refusal a modeller actually reads both call the tag scheduled work, while half
of it is blocked on a model gap neither mentions.

**Register a capability on the strength of the constructor in hand.** Rejected,
having been done: the first draft of this record registered expression result
types `Duration` and `int` as blocking duration and recurrence timers, because
the constructor examined demanded types the expression language does not
declare. A sibling constructor on the same type took the ISO 8601 string whole
and needed neither. A register that admits a capability without a search for the
model's existing way in produces work items for capabilities that already exist —
worse than no register, because it is believed.

## 5. Open questions

None. The one item deliberately left off the §2.3 register — the external
payload schema — is recorded there with the reason and its disposition, not
left open.

## 6. References

- [ADR-024 v.4 Process Interchange Converters](ADR-024-process-interchange-converters.md)
  — the disposition vocabulary and the report contract this refines.
- [ADR-035 v.1 Ad-Hoc Sub-Process](ADR-035-adhoc-sub-process.md) — the Router
  that makes the ad-hoc container a standing boundary.
- [ADR-023 v.3 Sub-Process and Call Activity](ADR-023-sub-process-and-call-activity.md)
  — call-time resolution, which the callable-resolution seam completes.
- [#284](https://github.com/dr-dobermann/gobpm/issues/284) — the coverage epic
  these boundaries were found under.

## Document History

| Version | Date | Author | Changes |
|---|---|---|---|
| v.1 | 2026-08-12 | Ruslan Gabitov | Initial decision. Three situations had collapsed into one refusal — work not yet reached, a **missing model capability**, and a **standing** property of the engine — and the last two read identically while meaning opposite things. The unit classified is the **construct, not the tag**: `<association>` holds a scheduled compensation link and a capability-blocked plain one. The converter **never compensates locally** for a missing capability (a private parser or type is the import-versus-runtime split in another costume, and the model layer would have to supersede it); it reports and refuses, **naming the capability**, so the refusal doubles as the specification of the work that removes it — and naming it forces the search for the model's existing way in, which is what a capability wrongly registered on the strength of one constructor costs. Standing boundaries — a complex gateway's token-count activation, an ad-hoc container's host-supplied Router — take a Go value no document can carry and **never become extension points**. Capability-blocked ones are registered with what unblocks them: an artifact collection on `Process` plus a constructor for `artifacts.Association` ([#323](https://github.com/dr-dobermann/gobpm/issues/323)), transaction coordination attributes ([#324](https://github.com/dr-dobermann/gobpm/issues/324)), and a callable-resolution seam ([#325](https://github.com/dr-dobermann/gobpm/issues/325)). A capability **lands before** the converter row consuming it. An external payload schema is deliberately unregistered — it needs a schema the converter cannot fetch and a Go value to bind, so it stays reported rather than scheduled. |
