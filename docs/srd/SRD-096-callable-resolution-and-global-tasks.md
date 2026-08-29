# SRD-096 — Callable resolution: the resolver seam, a qualified `calledElement`, and the GlobalTask family

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-27 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-023 v.5](../design/ADR-023-sub-process-and-call-activity.md) §2.7 (the callable reference, the resolver seam, the global task as a process), [ADR-024 v.7](../design/ADR-024-process-interchange-converters.md) §2.13 (the family's import, the QName reading of `calledElement`) |
| Upstream | [ADR-019 v.1](../design/ADR-019-definition-versioning.md) (the registry key; latest-at-launch), [ADR-040 v.2](../design/ADR-040-process-io-contract.md) (the process contract a global task's `ioSpecification` becomes), [ADR-024 v.7](../design/ADR-024-process-interchange-converters.md) §2.15 (the document result the synthesized processes join), §2.16 (the register row this consumes) |
| Related | [SRD-050](SRD-050-call-activity.md) (the call activity and `ProcessInvoker`, frozen), [SRD-089.E](SRD-089.E-bpmn-import-containers-and-lanes.md) §4.6 (the literal-key rule and the prefixed-`calledElement` refusal this supersedes, frozen), [SRD-089.C](SRD-089.C-bpmn-import-flow-nodes.md) §4.2 (the `notYet` refusal this retires, frozen), [SRD-089.I](SRD-089.I-bpmn-import-definitional-collaboration.md) (the multi-process document), [SRD-093](SRD-093-process-io-contract.md) (the process-level `ioSpecification` build reused) |
| Closes | [#325](https://github.com/dr-dobermann/gobpm/issues/325) — the last register row of the epic's "capability-blocked" list that touches the call path; `Part of` [#335](https://github.com/dr-dobermann/gobpm/issues/335) and [#284](https://github.com/dr-dobermann/gobpm/issues/284) |

## §1 Background

A call activity resolves its callable **at call time** and nowhere earlier:
`activities.NewCallActivity` (`NewCallActivity` (`pkg/model/activities/call_activity.go`))
stores `calledKey` verbatim and its doc comment says why — *"The registry is
deliberately NOT consulted here — resolution happens at call time (ADR-023
§2.7), so the callable may be registered later or re-versioned."* The
instance loop hands the key to the engine as `exec.ProcessCall{Key,
Version, …}` (`onCallWaiting`, `internal/instance/calls.go`), and the Thresher serves
it from its registration maps in `InvokeProcess`
(`pkg/thresher/invoker.go`, `resolveCallLocked` at
`resolveCallLocked` (`pkg/thresher/locked.go`)), failing a missing key with a classified
`ObjectNotFound` (`invoker.go`). There is no registry interface: the
registry is Thresher state.

The importer maps `<callActivity calledElement="k">` onto exactly that key
(`buildCallActivity`, `pkg/convert/bpmn/dispatch.go`), which is right
whenever the document's reference and the engine's registration key are the
same string, and wrong in two cases the same function already knows about:

1. **A prefixed `calledElement`.** `buildCallActivity` refuses any `:` in the
   value — *"names a callable in another definitions document … needs a
   callable-resolution seam this engine does not have (#325)"*. The
   converter has the machinery to resolve a prefix (`items.prefixes`,
   `items.prefixes` (`pkg/convert/bpmn/item.go`), filled by `declareNamespaces` at
   `declareNamespaces` (`item.go`); `importFor` at `importFor` (`item.go`) already resolves an
   `<itemDefinition>`'s `structureRef` prefix through the declared
   `<import>`s) and uses none of it here. The document's own
   `targetNamespace` is read by nobody (no hit in `pkg/convert/bpmn`
   outside the exporter's hard-coded `"http://bpmn.io/schema/bpmn"`,
   `exporter.go`), so even a self-qualified `tns:P` — what most
   modellers emit — is refused.
2. **The GlobalTask family.** Five `notYet` rows in the policy table
   (`dispatch.go`'s policy table) refuse `globalTask`, `globalUserTask`,
   `globalManualTask`, `globalScriptTask` and `globalBusinessRuleTask`
   through `notSupportedYet` (`notSupportedYet` (`pkg/convert/bpmn/errors.go`)) — *"reuse by
   reference needs a registry of callable definitions, which belongs to the
   server tier"*. The `notYet` kind (`dispatch.go`) has no other user.

Both were filed as one register row (#325; the import-coverage guide, row
`<callActivity calledElement="…">`), and the row was written before
ADR-040 gave a process a declared contract. With that carrier in place a
global task needs no new model element: ADR-023 v.5 §2.7 decides it is a
process whose body is the one task, and the registry already serves
processes. What the engine still lacks is the seam for the first case — a
host-supplied mapping from a *qualified* reference to a registration key —
and that seam is a Thresher option, because the Thresher is where the
registry lives and where the call is resolved.

## §2 Requirements

### Functional

- **FR-1 — A callable reference carries a namespace.** `CallActivity`
  gains an optional **called namespace**: `activities.WithCalledNamespace(ns
  string)` (a `CallActivityOption`, empty refused) and the accessor
  `CalledNamespace() string`. `exec.ProcessCall` gains `Namespace string`,
  filled by the instance loop from the node. An unqualified reference has
  an empty namespace. `Clone` copies it; `Validate` accepts any value.
- **FR-2 — The resolver seam.** `pkg/exec` declares
  `CallableRef{Namespace, Key string}` and
  `CallableResolver` — one method, `ResolveCallable(ctx context.Context,
  ref CallableRef) (string, error)`, answering the registry key a reference
  maps onto. `thresher.WithCallableResolver(r exec.CallableResolver)`
  supplies it; a nil resolver is refused at the option (the
  `WithLogger(nil)` rule).
- **FR-3 — The default resolver.** With no option, `Thresher` uses
  `exec.DefaultCallableResolver`: an empty namespace answers `ref.Key`; a
  non-empty one fails with a classified error naming the namespace and the
  key (*"no CallableResolver is configured for namespace %q"*). Today's
  behaviour for every existing call is therefore byte-identical.
- **FR-4 — Resolution at call time, outside every lock.** `InvokeProcess`
  calls the resolver once per call, after the argument checks and
  `ensureStarted` and **before** `resolveCallLocked`, holding neither
  `t.m` nor a key lock. A resolver error fails the call with the resolver's
  error wrapped and classified (the caller's activity faults through ADR-023
  §2.6, as a missing key does today). The resolved key — not the reference —
  is what `resolveCallLocked` and every fact attribute see; the reference's
  namespace is added as an attribute (`observability.AttrCalledNamespace`)
  when non-empty.
- **FR-5 — `calledElement` is read as a QName.** `buildCallActivity`
  resolves a `prefix:local` value through the document's declared
  prefixes and disposes of it per ADR-024 v.7 §2.13's table: unprefixed →
  key verbatim; prefix bound to the document's `targetNamespace` → the local
  part is the key; prefix bound to a declared `<import namespace>` → the
  local part is the key and `WithCalledNamespace(ns)` rides on the node; a
  prefix bound to no declared namespace, or to a namespace no `<import>`
  declares → refused, naming the prefix and the two ways out (declare the
  import; drop the prefix). The importer reads `<definitions
  targetNamespace>` for the purpose (today unread).
- **FR-6 — Each global task imports as a process.** `<globalTask>`,
  `<globalUserTask>`, `<globalManualTask>`, `<globalScriptTask>` and
  `<globalBusinessRuleTask>` under `<definitions>` are claimed by a
  `definitionsParsers` entry. Each produces one `*process.Process` appended
  to the document's set (`convert.Result.Processes`), with: the global
  task's id (claimed in the id ledger; a document already using it is
  refused as a duplicate) and name (falling back to the id); its
  `<ioSpecification>` built through the process-level path SRD-093 landed
  (`data.WithInputs`/`data.WithOutputs`, `constructProcess` (`importer.go`)), absent →
  contract-less; its `<documentation>` on the process; and a body of a None
  Start Event, the task, and a None End Event joined by two sequence flows.
- **FR-7 — The task is its in-process counterpart.** The task's node spec
  is recorded with its tag **rewritten to the in-process one** — `globalTask`
  and `globalManualTask` → `task`, `globalUserTask` → `userTask`,
  `globalScriptTask` → `scriptTask`, `globalBusinessRuleTask` →
  `businessRuleTask` — so pass 2's existing `nodeBuilders`, `paramOwners`
  and dialect handling build it with no second reading of any construct
  (`nodeBuilders` in `dispatch.go`; `buildNode` in `importer.go`). The body is
  read by the same `parseNodeBody` (`parseNodeBody` (`importer.go`)), so `<script>`,
  `<documentation>` and dialect attributes behave exactly as on the
  in-process form — and so does everything the in-process form refuses: a
  `<resourceRole>`/`<potentialOwner>` has no reader today
  (`nodeChildParsers`, `nodeChildParsers` (`dispatch.go`)) and is refused on both forms
  identically. Data associations are not read on the global form: a
  `CallableElement` is not an `Activity` and has none
  (`activities.md:646`).
- **FR-7a — The `<ioSpecification>` is the PROCESS's contract, not the
  task's.** The callable's `<ioSpecification>` becomes the synthesized
  process's declared contract (FR-6) and is **not** copied onto the task
  inside it.

  *Corrected at M5, by running it.* The draft gave it to both, reasoning that
  a declared output needs a node output parameter to fill it. That is true of
  a task fed by **data associations** and false here: a callable declares
  none, so parameters on the task are required-and-unfillable and the callable
  faults on its own contract before doing any work. What the contract actually
  needs at completion is the datum in the **root scope** under the declared
  name — which is exactly where a task's work lands (a rule task's decision
  row, a script task's outputs). The contract is fulfilled by what the task
  DOES, not by what it declares.
- **FR-7b — An imported `<callActivity>` declares what it passes.**
  `<ioSpecification>` on a call activity imports as its call parameters, which
  the instance loop binds into the child's root scope and reads back at
  completion — the direct mapping of ADR-023 §2.7, with no data associations,
  as §10.4 describes.

  §10.4.1's containment list names only Tasks and CallableElements, and read
  strictly it excludes a Call Activity — which is how SRD-089.G §4.7a read it.
  But §10.4's own CallActivity row says its DataInputs/DataOutputs *"are
  mapped to corresponding elements in the CallableElement"*, which presupposes
  it has them, and under the strict reading that row is unreachable: no
  imported document could hand data to a callable at all, and the whole
  contract half of this SRD would be dead on arrival. gobpm reads "Tasks" as
  the activities that do work.
- **FR-8 — Derived ids.** The process takes the global task's id; the five
  elements inside it take **five derived ids** — `<id>.start`, `<id>.task`,
  `<id>.end`, `<id>.start-task`, `<id>.task-end` (the None Start, the task
  node, the None End, and the two flows — the flows name their endpoints
  because `.in`/`.out` collide with the `<dataInput id="x.in">` a file may
  perfectly well declare) — each claimed in the document's id ledger,
  so a collision with a declared id is refused rather than silently rewired
  (ADR-024 v.7 §2.13). The ids the file itself declares inside the callable
  (its `<ioSpecification>`, its sets, its parameters) are claimed as they
  are today, unchanged and unprefixed.
- **FR-9 — `Import`'s pick ignores synthesized processes.** A global task is
  not the document's executable process, and SRD-089.I §4.2's pick counts
  candidates, so a synthesized process must not enter that count: `Import`
  (`Import` (`importer.go`)) considers only parsed `<process>` elements — both in
  the `len(procs) == 1` short-circuit and in the `isExecutable` tally — so
  the common file of one unmarked `<process>` plus a `<globalTask>` still
  imports, instead of becoming ambiguous. A document with global tasks and
  **no** `<process>` keeps today's refusal ("no `<process>` element found",
  `parse` (`importer.go`)), reworded to say that global tasks alone are not a
  process and to point at `ImportDocument`. `ImportDocument` returns every
  process, synthesized or parsed.
- **FR-10 — The `notYet` disposition is retired.** With FR-6 its five rows
  go, and with them the `notYet` kind (`dispatch.go`), `notSupportedYet`
  (`notSupportedYet` (`errors.go`)) and the "waiting on a subsystem" case of
  `TestTheThreeRefusalsAreDistinguishable` (`TestTheThreeRefusalsAreDistinguishable` (`refusals_test.go`)). A
  global task **inside** a `<process>` stays refused by the context rule
  (SRD-089.E §10 item 4) as an `UnsupportedElementError` with an **empty**
  section: `sections` (`sections` (`dispatch.go`)) has no entry for the family
  and cannot honestly gain one — the extract pins no § for it (ADR-023 v.5
  §3), which is the same reason `globalChoreographyTask` is pinned to `""`
  today (`dispositions_test.go`).
- **FR-11 — The register row leaves.** The `<callActivity calledElement>`
  / `globalTask` row leaves the import-coverage guide's capability-blocked
  table; the converters guide and `conformance.md`'s family row say what
  now happens; #325 closes with the PR.

### Non-functional

- **NFR-1 — No host call under a lock.** `make lock-sweep` learns
  `ResolveCallable` in its PATTERNS set and stays clean; T-13 proves it
  dynamically with a resolver that re-enters the Thresher.
- **NFR-2 — Diff-coverage ≥ `COVER_MIN`** on every touched file, measured
  after each milestone commit from `.ci/last-run.json`.
- **NFR-3 — Refusal wording.** Every new refusal names the construct, the
  reason and the way out; none says "yet" (ADR-024 v.7 §2.16).
- **NFR-4 — Export unchanged.** `CallActivity` is still not exported
  (ADR-024 §7 slice 3); the synthesized processes export like any other
  process whose nodes the exporter maps.

## §3 Models

### §3.1 `pkg/exec`

```go
// CallableRef is what a call activity names: a key, optionally qualified by
// the namespace of the definitions document that declared the callable.
type CallableRef struct {
	Namespace string // empty for an unqualified reference
	Key       string
}

// CallableResolver maps a reference onto the key the engine's registry
// serves. Consulted once per call, at call time, outside every engine lock.
type CallableResolver interface {
	ResolveCallable(ctx context.Context, ref CallableRef) (string, error)
}

// DefaultCallableResolver is the engine's resolver when the host supplies
// none: the unqualified case is exact, the qualified one is refused by name.
type DefaultCallableResolver struct{}

// CallableResolverFunc adapts a plain function, so a host with a one-line
// mapping writes no type (the http.HandlerFunc idiom).
type CallableResolverFunc func(context.Context, CallableRef) (string, error)

type ProcessCall struct {
	Key       string
	Namespace string // new; empty for an unqualified reference
	// … unchanged
}
```

### §3.2 `pkg/model/activities`

```go
func WithCalledNamespace(ns string) CallActivityOption // empty → error
func (ca *CallActivity) CalledNamespace() string
```

`callActivityConfig` and `CallActivity` gain `calledNamespace string`;
`Clone` copies it. The instance loop's capability interface
(`internal/instance/calls.go`) gains `CalledNamespace() string`,
and `onCallWaiting` (`internal/instance/calls.go`) copies it into
`ProcessCall.Namespace`.

### §3.3 `pkg/thresher`

```go
func WithCallableResolver(r exec.CallableResolver) Option // nil → error
```

`thresherConfig.callableResolver exec.CallableResolver`, defaulting to
`exec.DefaultCallableResolver{}`. `InvokeProcess`:

```go
key, err := t.callableResolver.ResolveCallable(ctx,
	exec.CallableRef{Namespace: call.Namespace, Key: call.Key})
if err != nil { /* wrap: "InvokeProcess: resolving called %q…" */ }
s, resolved, ok := t.resolveCallLocked(key, call.Version)
```

`InvokeProcess` currently discards its `context.Context` (`_`); it is now
passed to the resolver.

### §3.4 `pkg/convert/bpmn`

- `items` gains `targetNS string`, set from `<definitions targetNamespace>`
  where `declareNamespaces` is called on the root (`parseDefinitions` (`importer.go`)).
- `resolveCalledElement(p *parser, id, value string) (key, ns string, err
  error)` implements FR-5's table; `buildCallActivity` calls it.
- `parseGlobalTaskElem(p *parser, se xml.StartElement) (*assembly,
  error)` — a `defsParser` registered for the five tags — **records specs
  in pass 1 and builds nothing**, which is what the importer's two-pass
  shape requires: `parse()` hands every `p.asms` entry to `build`
  (`parse` and `build` in `importer.go`), and `asm.items` only exists from
  `buildItems(p)` at the start of pass 2 (`buildItems`, called from `parse` (`importer.go`)), so an
  `<ioSpecification>` naming an `itemSubjectRef` **cannot** be resolved at
  parse time. It therefore reads the element's leading `<documentation>`
  and `<ioSpecification>` the way `procBuild` does, then the body via
  `parseNodeBody`, and records into a fresh assembly: one `procSpec` (id,
  name, docs, `io`), one `nodeSpec` for the task with `se.Name.Local`
  **rewritten** to the in-process tag (FR-7) and the io stripped (FR-7a —
  `taskBody`), and two `flowSpec`s. Pass 2 then builds it with `constructProcess` → `buildNodes`
  → `linkFlow` → `Validate` unchanged — including `paramOwners` and
  `buildIOParams` (`buildNode` (`importer.go`)), which is why the rewrite is
  the whole of the mechanism.
- `defsParser`'s doc comment (`defsParser`'s doc comment (`dispatch.go`), *"returns a non-nil
  assembly when the child was a `<process>`"*) gains the second producer.
- `policy` loses the five `notYet` rows; `dispositionKind` loses `notYet`;
  `errors.go` loses `notSupportedYet`.

## §4 Analysis

### §4.1 Why the resolver is a Thresher option and not a converter option

The converter has no options by design (`pkg/convert/convert.go`'s registry comment:
*"a deliberate deviation from the functional-options norm because convert
is engine-independent"*), consults no registry, and finishes long before
any call. Resolution is a call-time act on the registry the Thresher owns,
so the seam lives beside the registry and every other host contract the
Thresher takes (`pkg/thresher/options.go` — 24 `With*` options, all
host-supplied seams). The converter's part is to carry the namespace the
resolver will need — the same split as `importFor` (`importFor` (`item.go`)), which
resolves a *prefix* to an `<import>` and hands the model the import, never
the type behind it.

*Rejected — a converter option*: would give the converter a registry view
it has deliberately never had, and would bind a document to one engine at
import instead of at call.
*Rejected — resolve inside `resolveCallLocked`*: puts host code under `t.m`,
the FIX-002 RC2 rule (`locked.go`'s lock-discipline comment) and the `lock-sweep` reason.
*Rejected — one resolver per document or per registration*: the reference a
resolver sees already carries the namespace that distinguishes documents, so
a single engine-wide resolver can route by it (a `switch` a host writes once)
while a per-registration one would multiply the seam and leave a call made
through a re-registered version ambiguous. One per engine, like every other
host contract the Thresher takes.

### §4.2 Why the resolver is consulted on every call, not only qualified ones

A host that maps keys — a tenant prefix, a naming convention — needs the
unqualified case too, and a resolver that is skipped for some references
cannot be reasoned about from its own code. One call per `InvokeProcess`,
always; the default resolver makes it the identity for the unqualified
case, so nothing changes for a host that configures nothing.

### §4.3 Why the default refuses a qualified reference instead of guessing

The alternatives are `local` (silently calling whatever the host registered
under a name that happened to coincide — exactly the failure
`buildCallActivity` refuses today) or `namespace + "#" + local` (an engine
naming convention no host asked for, which the standard does not supply —
`calledElement` is a plain `String`, §13.3.3). Refusing by name at call time
is the ADR-023 §2.7 contract for a reference the registry cannot serve, and
it tells the host which option to add.

### §4.4 Why a global task becomes a process, not a task the registry serves

The registry serves snapshots of processes (`resolveCallLocked` returns a
`*snapshot.Snapshot`); the call path launches a child instance around one
(`checkCallOutputs`, `checkCallOutputs` (`invoker.go`)); the caller binds inputs into the child's root scope
and reads outputs back by name (SRD-050, SRD-093). A "task registry" would
need a second call path, a second I/O binding and a second restart contract
(ADR-023 §2.7 restart) for a callable that §13.3.4 says calls like a
process. A process whose body is one task reuses all of it, keeps reuse by
reference (one registration, N callers, ADR-019 versions), and is what
ADR-023 v.5 §2.7 decides. ADR-024 v.7 §4 row M records the reversal of
the v.4 refusal.

### §4.5 Why the `<ioSpecification>` is the process's alone

A `GlobalTask` is a `CallableElement`, not an `Activity`
(`activities.md`): it carries **one** `ioSpecification` and no data
associations. The engine's realization has two candidates for it — the process
contract a caller binds against, and the task's own parameters — and it goes to
the process.

*This section argued the opposite until M5 ran the thing.* The case for giving
it to both was that a declared **output** needs a node output parameter to fill
it, or the callable promises what nothing produces. That is true of a task fed
by **data associations**, and a callable declares none: with parameters and no
associations the task's required input is declared and unfillable, so the
callable faults on its own contract — `required input "amount" of task "Add
tax" is unavailable` — before doing any work, on a document that is perfectly
well-formed.

The output is filled anyway, because that is not how a task's work reaches the
contract. What the contract reads at completion is the datum in the **root
scope** under the declared name, and that is exactly where a task's work lands:
a rule task's decision row, a script task's outputs. The contract is fulfilled
by what the task DOES, not by what it declares.

*Rejected — a second, converter-invented declaration on the task*: it would be
a shape the file does not contain, and the two could then disagree.

### §4.6 Why the ids are derived and claimed, not generated

Ids are never auto-generated on import (ADR-019; `parseProcess`,
`parseProcess` (`importer.go`)), and the lanes precedent (`buildLaneSet` (`lanes.go`)) generates
only for an element that is *unreferencable*. A synthesized start or flow
must be stable across imports (a re-import mints a new version of the same
process; its snapshot must compare) and must not collide silently, so the
ids are a pure function of the global task's id and go through `claimID`
like every declared id.

### §4.7 Why `notYet` goes

The kind exists for a refusal that says "the same file imports unchanged
once the subsystem lands" (`dispatch.go`). After FR-6 no row says
that; ADR-024 v.7 §2.16's three classes leave no room for a fourth, and
keeping an unused disposition invites its reuse for the next
capability-blocked construct, which the register (one issue per row) is
for instead.

## §4a Worked example

```xml
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:tns="http://example.com/orders"
    xmlns:ext="http://example.com/shared"
    targetNamespace="http://example.com/orders">
  <bpmn:import namespace="http://example.com/shared"
      location="shared.bpmn"
      importType="http://www.omg.org/spec/BPMN/20100524/MODEL"/>
  <bpmn:globalUserTask id="approve" name="Approve">
    <bpmn:ioSpecification id="approve.io">
      <bpmn:dataInput id="amount" name="amount"/>
      <bpmn:dataOutput id="approved" name="approved"/>
      <bpmn:inputSet id="approve.is"><bpmn:dataInputRefs>amount</bpmn:dataInputRefs></bpmn:inputSet>
      <bpmn:outputSet id="approve.os"><bpmn:dataOutputRefs>approved</bpmn:dataOutputRefs></bpmn:outputSet>
    </bpmn:ioSpecification>
  </bpmn:globalUserTask>
  <bpmn:process id="order" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:callActivity id="c1" calledElement="tns:approve"/>
    <bpmn:callActivity id="c2" calledElement="ext:audit"/>
    <bpmn:endEvent id="e"/>
    <!-- flows s→c1→c2→e -->
  </bpmn:process>
</bpmn:definitions>
```

`ImportDocument` returns two processes: `approve` (contract `amount` →
`approved`; body `approve.start` → `approve.task` (a `UserTask` named
"Approve") → `approve.end`) and `order`. `c1`'s `tns:` binds to the
document's own namespace, so its key is `approve` and its namespace empty.
`c2`'s `ext:` binds to the imported namespace, so its key is `audit` and
`CalledNamespace()` is `http://example.com/shared`. On a Thresher with no
resolver, `order` runs `c1` (the registry serves `approve`) and faults at
`c2` — *no CallableResolver is configured for namespace
"http://example.com/shared"*. With

```go
thresher.WithCallableResolver(exec.CallableResolverFunc(func(_ context.Context,
	ref exec.CallableRef) (string, error) {
	if ref.Namespace == "http://example.com/shared" {
		return "shared." + ref.Key, nil
	}
	return ref.Key, nil
}))
```

and `shared.audit` registered, `c2` runs too.

## §5 API deltas

| Package | Addition | Removal |
|---|---|---|
| `pkg/exec` | `CallableRef`, `CallableResolver`, `CallableResolverFunc`, `DefaultCallableResolver`, `ProcessCall.Namespace` | — |
| `pkg/model/activities` | `WithCalledNamespace`, `CallActivity.CalledNamespace` | — |
| `pkg/thresher` | `WithCallableResolver` | — |
| `pkg/observability` | `AttrCalledNamespace` | — |
| `pkg/convert/bpmn` | the five global-task tags import | the prefixed-`calledElement` refusal; the `notYet` refusal of the family |

Nothing existing changes signature. The consumer-smoke gate covers the
public surface.

## §6 Test scenarios

| T | Where | Scenario | Expect |
|---|---|---|---|
| T-1 | `activities` | `WithCalledNamespace("")` | refused, self-identifying message |
| T-2 | `activities` | `WithCalledNamespace(ns)`; `Clone` | accessor answers `ns` on both |
| T-3 | `exec` | `DefaultCallableResolver` on `{"" , "k"}` / `{"ns", "k"}` | `"k"` / classified error naming `ns` and `k` |
| T-4 | `thresher` | `WithCallableResolver(nil)` | refused |
| T-5 | `thresher` | caller with a qualified call, no resolver | the call activity faults; the fault names the namespace; the parent fails as a missing key does today |
| T-6 | `thresher` | resolver maps `{ns, "audit"}` → `"shared.audit"` (registered) | child runs; outputs bind back; `AttrCalledNamespace` on the call fact |
| T-7 | `thresher` | resolver returns an error | the call faults with the error wrapped and classified |
| T-8 | `thresher` | callee registered **after** the caller, before start | the call runs (the call-time contract; the issue's last deliverable) |
| T-9 | `bpmn` | `calledElement="k"` | key `k`, no namespace (T-1 of SRD-089.E, kept) |
| T-10 | `bpmn` | `tns:k` with `tns` bound to `targetNamespace` | key `k`, no namespace |
| T-11 | `bpmn` | `ext:k` with `ext` bound to an `<import>` namespace | key `k`, namespace = that URI |
| T-12 | `bpmn` | undeclared prefix; declared prefix with no `<import>` | refused, each naming the prefix and the way out; neither says "yet" |
| T-13 | `thresher` | a resolver that calls `t.Registrations(key)` and `t.Instances(InstanceQuery{})` from inside `ResolveCallable` | returns (no deadlock, under a test timeout) — NFR-1 |
| T-14 | `bpmn` | each of the five global tags, table-driven | one process per tag: id, name, node type of `<id>.task`, the five derived ids, two flows |
| T-15 | `bpmn` | global task with `<ioSpecification>` | `Inputs()`/`Outputs()` of the process carry the parameters; without → contract-less |
| T-16 | `bpmn` | a document id equal to a derived id (`approve.start`) | refused as a duplicate |
| T-17 | `bpmn` | `globalScriptTask` without `<script>`; `globalUserTask` with `camunda:assignee` | the in-process refusal / the in-process mapping, unchanged |
| T-18 | `bpmn` | `<globalTask>` inside a `<process>` | refused by section (context rule) |
| T-19 | `bpmn` | `Import` on: (a) global tasks + one **unmarked** `<process>`; (b) global tasks + one executable process; (c) global tasks only | (a) and (b) → that process, unambiguous; (c) → the reworded "no `<process>`" refusal pointing at `ImportDocument` |
| T-19a | `bpmn` | `ImportDocument` on (a) | both processes, in document order |
| T-20 | `bpmn` + `thresher` | §4a's document, both processes registered, resolver for `ext:` | `order` completes; the callee's declared `approved` is produced by its own task and reaches the caller's scope |
| T-21 | `bpmn` | the refusal sweeps | `refusals_test.go`: `TestGlobalTaskFamilyIsRefusedAsADeferral` gone, the `waiting on a subsystem` case dropped from `TestTheThreeRefusalsAreDistinguishable`; `refusalwording_test.go`: the `foreign calledElement` row **replaced** by T-12's undeclared-prefix refusal, so the capability-blocked/standing contrast the sweep exists for survives |
| T-23 | `bpmn` | a global task whose `<ioSpecification>` id collides with a document id | refused as a duplicate (the declared ids are ledger-claimed too) |
| T-24 | `examples` | the default resolver's refusal, shown in the example's own output | the run prints the fault for the un-resolvable call before configuring the resolver, then completes |
| T-22 | `examples` | `examples/bpmn-callable` (new): §4a's document from a `.bpmn` file, a resolver, a run to completion | exit 0 under the run sweep |

## §7 Milestones

| M | Content | Commit |
|---|---|---|
| M0a | ADR-024 §2.10 records the **delimiter override**: a `${…}` body is JUEL whenever present, including over a declared language; only a non-JUEL body consults the declaration, then the document default, then refuses. Row J and the §3 engine-note follow | docs |
| M0b | SRD-089.C's **T-11**: a thresher test running the stage's own elements — an inclusive gateway split/join, a `scriptTask` and a `businessRuleTask` — to completion, closing the DoD item that stage never closed | converter test |
| M0c | SRD-089.E's **T-7c**: `<transaction>` nested in a `<transaction>`, refused at import in the model's words (ADR-028 v.2 §2.8), the shape T-7b already uses | converter test |
| M0d | The five SRD-089 documents say what landed — supersession notes where later Accepted work overtook them (.A, .D, .E), an outright correction where the doc contradicted its own landing (.B), and the two now-covered scenarios marked (.C, .E). See §7a | docs |
| M0e | Re-audit all five; flip each doc that passes to Accepted, leave any that does not with its reason recorded | docs |

**A gate caveat this remediation did not cause.** `internal/instance` fails
about one package run in three on three checkpoint/restore tests that pass
20/20 in isolation ([#356](https://github.com/dr-dobermann/gobpm/issues/356)),
so a red `test-core` on this branch is not evidence about this branch until
that issue closes — judge it by which package failed. `govulncheck` is also
intermittently unable to reach `vuln.go.dev` from this environment. Neither is
reachable from `pkg/convert/bpmn`.
| M1 | `pkg/exec`: `CallableRef`, `CallableResolver`, `DefaultCallableResolver`, `ProcessCall.Namespace`; `activities.WithCalledNamespace`; the instance loop copies it (T-1–T-3) | model + exec |
| M2 | `pkg/thresher`: `WithCallableResolver`, resolution in `InvokeProcess` outside the lock, the fact attribute, `lock-sweep` PATTERNS (T-4–T-8, T-13) | engine |
| M3 | `pkg/convert/bpmn`: `targetNamespace`, `resolveCalledElement`, the refusal rewording (T-9–T-12, T-21 part) | converter, calls |
| M4 | `pkg/convert/bpmn`: the five global tags as processes; `notYet` retired (T-14–T-19, T-21) | converter, global tasks |
| M5 | `examples/bpmn-callable`; the end-to-end run (T-20, T-22) | example |
| M6 | Docs sync: the import-coverage guide (the row leaves), `converters.md`, `conformance.md:141`, `docs/guides/tasks/index.md:181-188` ("reuse by copy vs by reference" — the passage `conformance.md` links to), the examples tour, README + twins, changelog, §10 here; `/check-srd` | docs |

### §7a Housekeeping, verified

Checked before this SRD was written, so the flips are recorded facts and
not assumptions:

- **SRD-089.A–E stay `Draft`.** A `/check-srd` audit of all five (one per
  doc, against HEAD) found that none of them is flip-ready, so M0 does not
  flip them and this SRD does not claim they are done. Every §10 milestone
  SHA resolves with matching scope and every FR is wired, but each doc has at
  least one open item: **.C** never closed its own DoD item 3 — no test or
  example runs a `scriptTask`, `businessRuleTask`, `inclusiveGateway` or
  `eventBasedGateway` on a thresher (`run_test.go` arrived with a .D commit,
  `2c9198fe`); **.E**'s T-7c (`<transaction>` nested in a `<transaction>`)
  has no importer-level test, only a model-side one predating the stage;
  **.B** §3/FR-1 states a language-resolution order the code contradicts
  deliberately (`resolveLanguage` (`pkg/convert/bpmn/language.go`) lets the `${…}`
  delimiters outrank a *declared* language, pinned by
  `TestDelimitersOutrankADeclaredLanguage`) — and so does ADR-024 §2.10;
  **.A**'s FR-8/§4.4/T-6 still say the visual artifacts and `<import>` are
  skipped silently, which ADR-039 and ADR-024 v.5/v.6 re-decided into
  mapping; **.D**'s §6 T-4/T-5 list a conditional start and a cancel end that
  the model refuses in those positions. Closing them is its own work, tracked
  separately from this SRD.
- **ADR-023 and ADR-024** are both `Draft` and both carry landed contract
  besides this branch's change. The SRDs implementing them pin the version
  current when they landed (SRD-051 `v.1`, SRD-076 `v.3` and the rest stay
  frozen), so no pin moves. Both flip to Accepted at this branch's handover,
  once the v.5/v.7 changes are implemented — not at M0.
- **#324** stays open — PR #354 was `Part of` (the model/bind/import half;
  export rides ADR-024 slice 3). The import-coverage guide already says a
  transaction's `method`/`protocol` are not a refusal. No change.
- **The epic #335 register**: three capability-blocked rows remain (#325,
  #328, #331); this SRD consumes #325. `conformance.md`'s family row, the
  import-coverage row, `conformance-status.md`'s row 1 and the tasks guide all
  said the family was server-tier and waiting on a task registry; all four are
  corrected in M6. **[SAD-001 v.1.3](../design/SAD-001-vision-and-architecture.md)
  §14 carried the same reading and was corrected first**, with the owner's
  agreement, because a parent document cannot be left contradicting the ADR
  that refines it.

## §8 Cross-doc

| Direction | Document | Why |
|---|---|---|
| up | [ADR-023 v.5](../design/ADR-023-sub-process-and-call-activity.md) §2.7 | the callable reference, the resolver seam, the global task as a process |
| up | [ADR-024 v.7](../design/ADR-024-process-interchange-converters.md) §2.13, §2.9, §2.15, §2.16 | the family's import disposition, the QName table, the document result, the register row |
| up | [ADR-040 v.2](../design/ADR-040-process-io-contract.md) | the process contract a global task's `ioSpecification` becomes |
| up | [ADR-022 v.3](../design/ADR-022-error-propagation-and-logging-policy.md) | the observability vocabulary `called_namespace` joins; `internal/lintcfg` refuses an `Attr*` the ADR does not register |
| up | [ADR-019 v.1](../design/ADR-019-definition-versioning.md) | the key; latest-at-launch; ids never generated |
| side | [SRD-050](SRD-050-call-activity.md) | the caller-side call path (frozen) |
| side | [SRD-093](SRD-093-process-io-contract.md) | the process-level `ioSpecification` build reused (frozen) |
| side | [SRD-089.E](SRD-089.E-bpmn-import-containers-and-lanes.md) §4.6, [SRD-089.C](SRD-089.C-bpmn-import-flow-nodes.md) §4.2 | the refusals this supersedes (frozen; their text stays as the record of the earlier decision) |
| side | [SRD-089.G](SRD-089.G-bpmn-import-data-flow.md) | the parameter machinery reused; its §4.7a containment row is superseded in part by FR-7b (frozen, annotated in place) |
| side | [SRD-089.I](SRD-089.I-bpmn-import-definitional-collaboration.md) | the multi-process document result the synthesized processes join (frozen) |

No downward references.

## §9 Definition of Done

1. FR-1…FR-11 wired, each with its §6 test; NFR-1…NFR-4 held.
2. `make ci` PASS on the committed branch, judged by `.ci/last-run.json`;
   diff-coverage ≥ `COVER_MIN` after every milestone commit.
3. `make lock-sweep` clean with `ResolveCallable` in its set.
4. `examples/bpmn-callable` runs to exit 0 in the examples sweep.
5. `/check-srd` PASS; `/pr-review` triaged; linked docs synced (guides,
   `conformance.md`, README example tour, changelog); ADR-023 v.5 and
   ADR-024 v.7 flip to Accepted with this SRD at the handover; ADR-023's
   Russian twin refreshed then.
6. #325 closes with the PR; the epic's row is ticked.

## §10 Implementation summary

| M | Commit | What landed |
|---|---|---|
| M0a | `98221c2b` | ADR-024 §2.10: the `${…}` delimiters decide the language whatever is declared — the order the converter has used since SRD-089.B's own M3, which no document said |
| M0b/M0c | `4bd92b4f` | the two §6 scenarios SRD-089.C and .E specified and never delivered, plus the dialect assertion that stood in for one |
| M0d | `ac9fc301` | the five SRD-089 stages say what landed |
| M0e | `2c41c1ed`, `c10f6e91` | .A/.B/.C accepted, then .D/.E after two further audit rounds; the symbol-not-line convention that ends the largest defect class |
| M1 | `2d2e4b12` | `exec.CallableRef`/`CallableResolver`/`DefaultCallableResolver`, `ProcessCall.Namespace`, `activities.WithCalledNamespace`, the loop carrying it |
| M2 | `1d46ac13` | `thresher.WithCallableResolver`; resolution in `InvokeProcess` outside every lock; `called_namespace` on the call fact; `lock-sweep` taught `ResolveCallable` |
| M3 | `0940da70` | `targetNamespace`, `resolveCalledElement`, the four QName dispositions; the refusal-wording sweep restructured into three kinds |
| M4 | `cf350c35` | the five global tags as processes; `Import`'s pick ignoring them; `notYet` retired; ADR-022 v.3 registers `called_namespace`, which `internal/lintcfg` requires of every `Attr*` |
| M5 | `a5e730ce` | `examples/bpmn-callable` |
| M5a | `e736e2eb` | an imported `<callActivity>` declares what it passes; FR-7a corrected |
| M6 | *this commit* | the guides, `conformance.md`, the READMEs and the changelog |

### §10.1 Where the plan was wrong

**FR-7a, and the run is what found it.** The draft gave the callable's
`<ioSpecification>` to both the process and the task inside it, on a
`/review-srd` argument that a declared output otherwise cannot be filled. That
argument holds for a task fed by data associations and fails for a callable,
which declares none: the parameters are required-and-unfillable, and the
callable faults on its own contract before doing any work. The first draft had
it right. No static check caught the regression — the code was consistent, the
tests passed, the document read well — and M5's example failed on it in thirty
seconds.

**An imported call could not pass data at all**, which M5 also surfaced:
§10.4.1's containment list reads as excluding a `<callActivity>` while §10.4's
own CallActivity row maps its DataInputs/DataOutputs onto the callable's. The
strict reading made that row dead letter and the contract half of this SRD
unreachable from any file. Landed as M5a, superseding SRD-089.G §4.7a's row.

**`.in`/`.out` were bad derived ids.** The example's own fixture collided:
`<dataInput id="tax.in">` is an entirely natural thing to write. The refusal
was correct (T-16 working); the suffixes now name their endpoints.

**M0 was not housekeeping.** The five SRD-089 flips were expected to be a
status change; an audit found each doc short of its own DoD, including two
scenarios a Definition of Done had required and nobody had written, and a
validation three passages asserted that does not exist (#357). Five audit
rounds and six commits later they are accurate. The largest single defect class
across them was line-pin rot, which is why `CLAUDE.md` now requires symbols.

### §10.2 Verification

`go test ./...` green across the root module; `golangci-lint` 0 issues in the
root module and in `examples/bpmn-callable`; `make lock-sweep` clean with
`ResolveCallable` in its set; `make link-check` clean; the example runs to exit
0 and asserts the value that crossed the call boundary. Per-function coverage
on everything this SRD added is 100% except `parseGlobalTaskElem` (85.7%) and
`addGlobalTaskGraph` (90%), whose uncovered lines are bare `return nil, err`
that the diff-coverage gate excludes.

`make ci` PASSES on the branch tip. It was unreliable for most of this
branch's life for a reason the branch did not cause — `internal/instance`
flaked at about one package run in three (#356), which reddened two gate runs
here — and that is fixed on master (`ad82e4d5`) and merged in. `govulncheck`
can still fail by not reaching its database, which is a network fact, not a
verdict. Judge a red run by which step and package failed.

### §10.3 The independent review

`/pr-review` put the branch diff to an external reviewer (Antigravity /
`gemini-3.1-pro-high`, four lenses: correctness/concurrency, API design, tests,
and the build tooling), doc-blind — it was given no SRD, because a reviewer
holding the document grades the code against the document, which `/check-srd`
already did.

It returned 18 notes. **Ten were real and are fixed in M8**: a typed-nil
`CallableResolverFunc` reaching the engine as a panic; `DefaultCallableResolver`
answering an empty key rather than refusing it; `InvokeProcess` not validating
the `ctx` it hands to host code; the call fact naming an unresolved callable on
a re-attached child; and, in the tests, two that would have passed with the
feature broken. Three latent faults in the gate scripts came with them.

**Four were wrong, and the reasons are worth more than the notes:**

| Note | Why it is wrong |
|---|---|
| *"Authorization is checked against the unresolved key in `InvokeProcess`"* (blocker) | There is no authorization check in `InvokeProcess` at all. The engine's `auth.AuthorizationProvider` serves **user-task** authorization and is never consulted on invocation. The note described a call site that does not exist. |
| *"macOS SIP strips `export -f`, so every batch run fails immediately"* (blocker) | Demonstrably false on the machine in question, where `export -f` survives `xargs` and four full gate runs over 52 modules had already passed. |
| *"Validate `ctx != nil` inside both `ResolveCallable` implementations"* | `DefaultCallableResolver` takes `_ context.Context` — it cannot dereference what it never reads — and `CallableResolverFunc` passes it to host code, which the entry point now validates. A guard on a deliberately-ignored parameter is noise. |
| *"Word-splitting on unquoted `$wave` / `$ALL_MODULES`"* | `$(MODULES)` is a space-separated Make list by construction, so a module path containing a space breaks the caller before the script sees it. The realistic residue — a quote or backslash in a path — is fixed by the NUL-delimited `xargs`. |

One accepted note was also wrong **in its remedy**: it proposed asserting the
synthesized task's `IOSpec()` is `nil`, but every activity is built with an
empty specification, so that assertion fails on correct code. The defect was
real and the test asserts the property that actually breaks — that the task
declares no PARAMETERS. An agent's output is evidence, not a verdict, and the
difference showed up only by running it.

## Open questions

None. §4.2 (the resolver is consulted on every call, not only qualified ones)
and §4.5 (the `<ioSpecification>` is the process's alone) are the two points
with a defensible alternative; both are decided above with the alternative
recorded, and §4.5's was decided the hard way — see §10.1.
