# ADR-040 — Process I/O: the callable's declared contract

| Field | Value |
|---|---|
| Status | Accepted |
| Version | v.1 |
| Date | 2026-08-26 |
| Owner | Ruslan Gabitov |
| Refines | [ADR-011 v.8](ADR-011-process-data-flow.md) §2.5 (the scheduled process-level Start/End data path — this record delivers its carrier half), [ADR-023 v.4](ADR-023-sub-process-and-call-activity.md) (the Call Activity's direct I/O mapping — this record supplies the callee side it names), [SAD-001 v.1.2](SAD-001-vision-and-architecture.md) §14.1 (the single-set deviation and the identity `ioBinding`, both of which extend here) |

> **Scope.** A BPMN `Process` is a `CallableElement` and may declare an
> `InputOutputSpecification` — process-level data inputs and outputs, the
> declared contract of the process *as a callable thing*. This decides what
> that contract is to this engine: the model carrier, when and how inputs
> bind and outputs are read, what the call boundary validates, and what a
> process without a contract means. It is the model capability
> [ADR-024 v.6](ADR-024-process-interchange-converters.md) §2.16 registers for
> `<ioSpecification>` on a `<process>` (#330), delivered per its second
> rule — a capability lands before the row that consumes it — as the
> capability's own decision record; the accompanying SRD lands it. The
> event-association wiring of the same conception is deferred to its own
> registered capability (§2.7).

## 1. Context

### 1.1 What the standard declares

Only *"Tasks and CallableElements (Processes, GlobalTasks)"* may define
DataInputs/DataOutputs, via their `InputOutputSpecification` (§10.4.1
p210 — the containment rule the importer already enforces for everything
else). The specification aggregates the contract: ordered `dataInputs` and
`dataOutputs`, where an empty set means *"no data required to start"* /
*"to finish"* (§10.4.1); it *"MUST have at least one InputSet"* (§10.4.1
p217).

The purpose of the process-level form is stated outright, and it is
invocation: *"To allow invoking a Process from both a Call Activity and via
Message Flow"* (§10.4.2), the standard makes the **enclosing Process's
DataInputs** available as *targets* of a Start Event's data output
associations, and its **DataOutputs** as *sources* of an End Event's data
input associations. And for the call itself no associations are needed at
all: a Call Activity's *"DataInputs / DataOutputs are mapped to
corresponding elements in the CallableElement **without any explicit
DataAssociation**"* (§10.4.1 p216).

### 1.2 Where the engine stands

[ADR-011 v.8](ADR-011-process-data-flow.md) §2.5 scheduled exactly this:
the process-level Start/End special case is *"part of the conception and
lands with the messaging/call-activity work that needs it."* The
call-activity work has since landed — and its decision record already
speaks the standard's language: [ADR-023 v.4](ADR-023-sub-process-and-call-activity.md)
prescribes that a Call Activity's inputs *"map to the callable's
InputOutputSpecification"*, inputs bound into the child's root scope at
launch, outputs bound back at completion.

But the callable has no InputOutputSpecification to map to. The contract
is realized one-sided: the **caller's** declared parameter names decide
what enters the child and what is read back, the callee declares nothing,
and nothing validates the boundary — a misnamed or missing input surfaces
as a data-resolution failure deep inside the running child, far from the
call site. A host launching a process directly has the same gap in both
directions: no declared launch payload, no declared result. And the
importer refuses a legal `<ioSpecification>` on a `<process>`, registered
capability-blocked ([ADR-024 v.6](ADR-024-process-interchange-converters.md)
§2.16, #330).

## 2. Decision

### 2.1 The Process declares a single-set I/O contract

The model's `Process` gains an optional I/O specification: **one input
set and one output set** of named, typed, required-or-optional
parameters — the same parameter concept its activities already use. The
single-set shape is [SAD-001 v.1.2](SAD-001-vision-and-architecture.md)
§14.1's standing deviation (multiple sets with data-driven selection are
a decided non-implementation) extended to the callable, where it binds
with *more* force, not less: the standard demands at least one InputSet
(§10.4.1 p217), and the engine holds exactly one. Order is preserved
(the sets are ordered in the standard); the optional/required distinction
lives *within* the set, as ADR-011 already keeps it.

### 2.2 Inputs are the launch surface

A process's declared inputs are bound **into the instance's root scope at
launch**, whatever the entry:

- a **Call Activity** binds them from its own declared inputs — the
  §10.4.1 p216 direct mapping, which finally has a callee side to map
  *to*;
- a **host launch** supplies them directly with the start request — the
  same contract, minus the calling diagram.

A **required input unbound at launch refuses the launch**, naming the
input — never a wait. This extends the engine's registered no-data-wait
deviation ([SAD-001 v.1.2](SAD-001-vision-and-architecture.md) §14.1) to
instantiation: a launch that would sit until data appears is hidden
synchronization, and the caller who cannot supply a required input has a
diagram problem the engine should say out loud at the boundary, not a
stalled instance. Root-scope residency means the inputs are visible to
every child scope through the ordinary walk-up, exactly as properties
are.

### 2.3 Outputs are the completion surface

A process's declared outputs are read **from the instance's root scope at
normal completion**:

- a **Call Activity** commits them into the caller's scope under its own
  declared output names (the same direct mapping, outbound);
- a **host launch** receives them as the completed instance's declared
  result.

A **required output unavailable at normal completion faults the
instance** — the process claimed a result it did not produce, and
completing silently without it would hand the caller a hole where the
contract promised a value. An optional output simply does not flow.
Nothing is read on a terminal fault or termination: an abnormal end has
no result surface.

### 2.3a What an output can carry, and how a value gets there

A declared parameter is a named, typed item-aware element, so anything
the model's value family expresses can cross the boundary: a scalar, a
record, a list, a map ([ADR-011 v.8](ADR-011-process-data-flow.md)'s
structural data), including values lifted from host-native structs
through the adapter tier.

**Publishing is the ordinary copy machinery — nothing new moves data.**
A declared output is a root-scope-resident datum under its name, and
root-scope data is what the existing data associations already target:

- an **activity's result** publishes by its output association targeting
  the process output — the common case;
- a **data object's** or a **property's** value publishes the same way,
  copied out by an activity that touches it;
- once the event attachment capability lands (§2.7), an **End Event's
  input associations** become the standard's dedicated gathering point
  (§10.4.2) — until then the mid-flow copies serve, and they are
  conformant: the §10.4.2 path is *available*, not exclusive.

**The engine's runtime variables publish by mapping, never by
declaration.** `STARTED_AT`, `STATE`, `TRACKS_CNT`, `COMPLETED_BY` live
under the reserved, read-only `RUNTIME` named source
([ADR-010 v.2](ADR-010-process-data-model.md) §2.7): they are
path-addressed reads (`RUNTIME/STARTED_AT`), not scope data, so they can
never *be* an output — an output is scope data the instance owns. They
reach the result surface the same two ways they reach anything else:

- a **mapping** sources the path-qualified read — a task's input
  association reads `RUNTIME/STARTED_AT` (the §2.7 uniform read seam),
  and the task's output association commits the value onward, into a
  data object or straight into a process output;
- an **in-process Go operation** reads them through its read-only data
  reader ([SAD-001 v.1.2](SAD-001-vision-and-architecture.md) §14.2) and
  returns them as its declared output.

Either way the published value is a **copy committed at a stated moment
of the flow** — which is the §2.3a rule in general form: the standard's
data associations *copy* (§10.4.2's own language; a later change to the
source does not propagate), so the result surface holds committed
values, not live views.

**Excluded from publishing, each for a stated reason:** a **data
store** — the engine-global port ([ADR-030 v.1](ADR-030-data-objects-and-store.md)),
shared across instances, not an instance's result; a **child scope's
locals** — a sub-process's data dies with its scope, so a value meant to
outlive it is copied out before the scope closes (the existing scope
rule, unchanged); and **aliasing** — declaring an output that *is* a
property or data object, filled implicitly. Aliasing would make the
result a live view of internal state, tie the public contract to an
internal name, and contradict the copy semantics above; the explicit
copy keeps "what this process returns" visible on the diagram, the same
visibility principle as the no-data-wait deviation.

### 2.4 The call boundary corresponds by name and validates at launch

The standard maps the two sides *"to corresponding elements"* without
explicit associations (§10.4.1 p216), and the vendored extract reads that
correspondence as positional. **Engine note: this engine corresponds by
name, and records the divergence.** Names are how this model addresses
data everywhere — properties, parameters, scope lookups — while a
positional correspondence ties the meaning of a call to declaration order
on both sides, so an innocent reorder silently rewires data: exactly the
class of quiet breakage a declared contract exists to end.
[ADR-023 v.4](ADR-023-sub-process-and-call-activity.md) already stated
the binding as "positional/by-name"; this record settles it on the name.

Validation happens **at launch, not at registration**, because it cannot
happen earlier: [ADR-019 v.1](ADR-019-definition-versioning.md) binds a
call to the callable's **latest version at launch** (unless pinned), so
the callee's contract is not fixed until the moment of the call. At
launch, before the child runs: every **required callee input** must be
matched by a caller input of the same name; every **caller output** must
name a declared callee output. A mismatch refuses the launch at the Call
Activity — a fault at the call site, catchable there — rather than a
resolution failure deep in the child.

With exactly one set on each side, the `ioBinding` identity of
[SAD-001 v.1.2](SAD-001-vision-and-architecture.md) §14.1 carries over
unchanged: there is nothing to select, so the binding *is* the name
correspondence.

### 2.5 A callable without a contract keeps its permissive meaning

A process that declares **no** I/O specification means what the standard's
empty sets mean — *"no data required to start"*, none promised to finish
(§10.4.1) — and the engine adds the permissive reading the call path has
always had: it accepts whatever its caller's declared inputs deliver into
its root scope, and returns whatever its caller's declared outputs ask of
it. **Declaring a contract is what makes the boundary binding.** This
keeps every existing process and call valid, and makes strictness an
opt-in of the callee — the party whose interface it is.

### 2.6 Properties and I/O stay distinct

A property is the process's **internal state**: declared with its own
item and initial value, born at instantiation. An input is **someone
else's value arriving**; an output is a value leaving. Both reside in the
root scope and are addressed by name, so one namespace holds them — a
declared input clashing with a property name is a validation error at
registration, not a shadowing rule. No implicit bridging: a property is
not filled from a like-named input, and an output does not read a
property unless the process's own flow put the value there.

### 2.7 The event wiring is deferred to the attachment capability

The standard's Start/End special case (§10.4.2) flows *through event data
associations*: a Start Event's output associations may target the process
inputs, an End Event's input associations may source the process outputs.
Data associations on events are their own registered model capability
([ADR-024 v.6](ADR-024-process-interchange-converters.md) §2.16, the
attachment API row) and are **not** decided here. Until that lands, the
contract binds through the direct paths of §2.2/§2.3 — which is the
standard's own mechanism for calls, no associations involved — and the
Message-Flow-triggered fill of process inputs arrives with the event
capability that carries it.

### 2.8 The converter follows

Once the carrier exists, `<ioSpecification>` on a `<process>` (and its
bare data inputs/outputs) is **mapped** — parsed into the process
contract — consuming the #330 register row per
[ADR-024 v.6](ADR-024-process-interchange-converters.md) §2.16. The
importer keeps refusing what this record does not give a home: multiple
input or output sets remain the standing multi-set boundary
([SAD-001 v.1.2](SAD-001-vision-and-architecture.md) §14.1), stated as a
choice, not a schedule.

### 2.9 The run, moment by moment

The contract touches an instance's life at **exactly two moments**, and is
invisible in between:

1. **Registration** validates the declaration: the one namespace (§2.6),
   the specification's own well-formedness. A snapshot carries the
   contract as a declaration, shared by every instance of that version.
2. **Launch, before any token exists.** Whatever the entry delivered —
   the caller's declared inputs, the host's start request — is bound
   through the declared input parameters (§2.2): each declared input
   takes its value from the delivered datum of its name, type-checked
   against the declaration; a required input with no datum, or a
   delivered datum naming no declared input, refuses the launch. No
   instance exists after a refusal — no loop, no track, nothing to clean
   up. An event-born launch runs the same binding; until the attachment
   capability (§2.7) can fill a process input from a start payload, a
   process declaring a **required** input cannot be event-started, and
   says so.
3. **The run.** The bound inputs are ordinary root-scope data — readable
   by name from every frame and every child scope, exactly as properties
   are. A declared output is a root-scope slot the flow fills by the
   ordinary copy machinery (§2.3a). **No engine decision reads the
   contract while tokens move**: not the scheduler, not the gateways,
   not the data-association gates. Checkpoint, restore, dehydration and
   hydration carry the bound data as the root-scope data it is and never
   re-bind.
4. **Normal completion, after the last token ended.** The declared
   outputs are read from the root scope and copied into the instance's
   result (§2.3); a required output absent or unavailable faults the
   instance at this moment — the terminal-fault shape the engine already
   has, so a caller faults at its Call Activity and a host receives the
   error from its wait, with no result surface either way. An abnormal
   end never reaches this step.
5. **Hand-back.** The result flows to whoever launched: committed into
   the caller's scope under the caller's declared output names (§2.4's
   name correspondence, checked at the call's launch), or exposed to the
   host as the completed instance's declared result.

A process without a contract skips moments 2 and 4 entirely and runs as
it always has (§2.5).

## 3. Consequences

- **The call contract becomes two-sided.** ADR-023's "map to the
  callable's InputOutputSpecification" stops being aspirational: the
  callee declares, and a broken call fails **at the call boundary at
  launch** with the mismatched name in hand — not as a scope-lookup
  failure somewhere inside the child.
- **A host gains a declared launch and result surface.** Starting a
  process takes a validated payload; a completed instance hands back a
  declared result — no more result-by-property-convention.
- **A legal file imports.** A modeller's reusable called process — the
  shape that declares `<ioSpecification>` on `<process>` — stops
  refusing; the #330 register row is consumed.
- **The Message-Flow half stays scheduled, visibly.** Filling process
  inputs from a message-start payload needs the event attachment
  capability; §2.7 records where that debt is paid.
- **Nothing waits on data, still.** The no-data-wait deviation now covers
  instantiation and completion as well as activities — one rule, three
  moments.

## 4. Alternatives considered

**Properties as the I/O surface** (no new carrier; callers fill
properties by convention). Rejected — it is the status quo's weakness
given a name: a property has no direction, no requiredness, and its
lifecycle is internal state, so the "contract" remains whatever the
caller guesses, and nothing can validate the boundary. The standard also
keeps `Property` and `DataInput`/`DataOutput` as distinct element kinds
with distinct containment (§10.4.1).

**Registration-time validation of the call boundary.** Rejected — it
cannot be done honestly: [ADR-019 v.1](ADR-019-definition-versioning.md)
resolves the callee at launch (latest-at-launch unless pinned), so a
registration-time check either forces early version binding or validates
against a version the call may not use. Launch-time is when the contract
is real.

**Positional correspondence at the call boundary** (the vendored
extract's reading of §10.4.1 p216). Rejected as this engine's rule, and
the divergence recorded in §2.4: position ties the meaning of a call to
declaration order on both sides, so an innocent reorder silently rewires
data. Names are the model's addressing scheme everywhere else.

**Waiting for a late input at launch** (the data-availability wait,
process edition). Rejected — the same hidden-synchronization argument as
[SAD-001 v.1.2](SAD-001-vision-and-architecture.md) §14.1's registered
deviation; a process that must pause for data models that with a catch
event, visibly.

**Multiple input/output sets on the Process.** Rejected without new
argument — the §14.1 standing deviation; the model is shaped so multi-set
selection could return as an extension if real demand appears.

## 5. Open questions

None. The two deliberate deferrals are recorded where they are paid: the
event-association wiring (§2.7, the attachment capability's row) and
multi-set selection (§2.8, a standing boundary).

## 6. References

- [ADR-011 v.8](ADR-011-process-data-flow.md) §2.5 — the scheduled
  conception this delivers the carrier for.
- [ADR-023 v.4](ADR-023-sub-process-and-call-activity.md) — the call
  contract whose callee side this supplies.
- [ADR-019 v.1](ADR-019-definition-versioning.md) — latest-at-launch,
  which fixes the validation moment (§2.4).
- [ADR-024 v.6](ADR-024-process-interchange-converters.md) §2.16 — the #330
  register row this consumes, and the attachment-API row §2.7 defers to.
- [SAD-001 v.1.2](SAD-001-vision-and-architecture.md) §14.1 — the
  single-set and no-data-wait deviations this extends.
- [ADR-010 v.2](ADR-010-process-data-model.md) §2.7 — the `RUNTIME` named
  source whose path-addressed reads §2.3a's mappings publish through.
- [ADR-030 v.1](ADR-030-data-objects-and-store.md) — the engine-global
  data store §2.3a excludes from the result surface.
- [semantics/data.md](../bpmn-spec/semantics/data.md) — the vendored
  extract carrying every §10.4.1/§10.4.2 clause cited above (containment
  p210, direct call mapping p216, the InputSet MUST p217, the process
  Start/End special case).

## Document History

| Version | Date | Author | Changes |
|---|---|---|---|
| v.1 | 2026-08-26 | Ruslan Gabitov | Initial decision. The Process gains a **single-set I/O contract** (SAD-001 §14.1's deviation extended to the callable): named, typed, required-or-optional parameters. **Inputs bind into the root scope at launch** — from the Call Activity's declared inputs (§10.4.1 p216 direct mapping, at last with a callee side) or from the host's start request; a required input unbound at launch refuses the launch, never waits. **Outputs are read from the root scope at normal completion** — committed to the caller or handed to the host; a required output unavailable at completion faults the instance; abnormal ends have no result surface. **Correspondence at the call boundary is by name** (engine note — the standard states no rule) and **validates at launch** (ADR-019's latest-at-launch makes earlier impossible). A process declaring **no contract keeps the permissive meaning** — strictness is the callee's opt-in. Properties stay distinct (one namespace, no implicit bridging; a name clash refuses at registration). §2.3a inventories the **publishing routes** — the ordinary copy machinery (activity output associations; data-object/property values via copies; the End-Event gathering once the attachment capability lands), the **runtime variables** published only by mapping the read-only `RUNTIME/…` reads (ADR-010 v.2 §2.7) through a task's associations or a Go operation's reader (never declared as outputs — they are not scope data), and the exclusions: data stores (engine-global), child-scope locals (copied out before the scope closes), and **aliasing** (an output is a committed copy, never a live view — the standard's own copy semantics). The **event-association wiring is deferred** to the attachment capability (§2.7); the converter maps `<ioSpecification>` on `<process>` once the carrier lands, consuming the #330 register row. §2.9 fixes the run's shape: the contract touches an instance at **exactly two moments** — launch (before any token) and normal completion (after the last) — and is invisible to every engine decision in between; refusal at launch leaves no instance behind, and an abnormal end never reaches the result step. **Accepted 2026-08-26** — landed whole via SRD-093 (the carrier, the launch binding, the completion reading, the call-boundary check, the importer row, `examples/process-io/`); the landing audit and an independent review folded into it (an unproduced optional output does not flow to the caller; outputs are bound through their declaration, so a type mismatch faults; an explicit empty `<ioSpecification/>` is a strict empty contract). Status flip; the register reference moved with ADR-038's retirement to ADR-024 v.6 §2.16. RU twin created. |
