# SRD-094 — Event data attachment: events as association ends, the process-level Start/End path, import

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-26 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-011 v.8](../design/ADR-011-process-data-flow.md) §2.5 (events carry data without sets — the attachment surface it presumes), [ADR-040 v.2](../design/ADR-040-process-io-contract.md) §2.7 (the process-level Start/End data path and the event-born binding order) |
| Upstream | [ADR-030 v.1](../design/ADR-030-data-objects-and-store.md) §2.3 (the scope-routed copy path); [ADR-010 v.2](../design/ADR-010-process-data-model.md) §2.3 (parameters as templates); [ADR-024 v.6](../design/ADR-024-process-interchange-converters.md) §2.16 (the #329 register row; capability first, converter row after); [SAD-001 v.1.2](../design/SAD-001-vision-and-architecture.md) §14.1 (never wait on data) |
| Related | [SRD-063](SRD-063-dataobject-scope-integration.md) (FR-5, the copy path the tasks use), [SRD-089.G](SRD-089.G-bpmn-import-data-flow.md) (the association pass this extends), [SRD-093](SRD-093-process-io-contract.md) (the contract this path fills), [SRD-015](SRD-015-message-correlation-instantiation.md) (the born-from-event launch) |
| Closes | [#329](https://github.com/dr-dobermann/gobpm/issues/329) |

## §1 Background

[ADR-011 v.8](../design/ADR-011-process-data-flow.md) §2.5 decides the
event data model — a throw event's input associations fill its inputs when
it fires, a catch event's output associations push the triggering
element's data into the environment, and events never wait. The **runtime
half of that is landed**; the **attachment half is not**, and the
process-level Start/End path that [ADR-040](../design/ADR-040-process-io-contract.md)
§2.7 deferred waits on it. This SRD lands the surface, lifts the runtime
half onto the copy path the tasks already use, closes the Start/End path,
and lets the importer follow.

**What exists today** (verified against the working tree):

- The catch side: `catchEvent` holds `dataOutputs map[string]*data.Parameter`
  and `outputAssociations []*data.Association`
  (`pkg/model/events/event.go:280-288`); `UploadData` (`:480-570`)
  instantiates the outputs in the frame, binds the received payload into
  the matching one (`f.Received()`, `:504-524`), and pushes every output
  association. The throw side: `throwEvent` holds `dataInputs` and
  `inputAssociations` (`:578-585`); `LoadData` (`:627-735`) instantiates
  the inputs, fills them from the associations with the never-wait gate
  (`data.RequiredItemIDs`, `:659`; `missingRequiredInputs`, `:740-762`),
  and the message publish then reads the item from the execution scope —
  frame inputs first (`msgflow.Send` → `service.BindInput`,
  `pkg/model/msgflow/send.go:47`, `service/operation.go:301`).
- **Nobody can attach.** The only writers of the two maps are the message
  triggers: `addMessagePayloadOutput` (`event.go:368-391`),
  `startConfig.setMessage` (`start_options.go:196-224`),
  `endConfig.setMessage` (`end_options.go:148-177`). Signal, Error and
  Escalation definitions carry an item too (`signal.go:54`,
  `bpmncommon/error.go:16-20`, `escalation.go:84`; all four report it
  through `GetItemsList`) and get no parameter. No option declares one.
- Events implement neither `flow.AssociationSource` (`Outputs()`,
  `BindOutgoing`) nor `flow.AssociationTarget` (`Inputs()`, `BindIncoming`)
  — `pkg/model/flow/data_object.go:20-43`; the only implementer is `task`
  (`pkg/model/activities/task.go:515-575`). So `DataObject.AssociateSource`
  / `AssociateTarget` (`data_objects/data_object.go:90, 139`), the one
  wiring API, cannot take an event, and the package's tests reach the
  fields directly (`events/event_data_test.go:102, 140, 298, 351`).
- **The event copy path is the pre-SRD-063 one.** A task routes both
  directions through the frame's scope by name — `f.GetData(oa.TargetName())`
  on output (`task.go:254`), `loadFromScope` on input (`task.go:131`),
  SRD-063 FR-5 — so a shared `Association` is read for its routing and
  the per-instance datum is updated in place. `catchEvent.UploadData`
  instead calls `oa.UpdateSource(ctx, out.ItemDefinition(), …)`
  (`event.go:552`), and `throwEvent.LoadData` reads `ia.Value(ctx)`
  (`event.go:690`): both act on the association's own item-aware elements
  — model objects shared by every instance of the snapshot. Unreachable
  today; a defect the moment anything attaches.
- **A born start never runs.** For an event-born launch `seedInitialData`
  commits the fired definition's items into the root scope by item id
  (`bindEventPayload`, `internal/instance/scope.go:138-175`) and
  `createTracks` seeds the start's *outgoing* targets, skipping the start
  itself (`seedableEntry`, `instance.go:778-780`; `:840-850`). The start's
  output associations therefore never execute for the launch that
  instantiated it — which is exactly the launch the Start/End special case
  is about.
- The contract side: `bindContract` runs after `bindRootData`
  (`instance.go:664-668`) and refuses an event-born launch with a required
  input, naming #329 (`io_contract.go:106-118`).
- The importer refuses a data association on an event
  (`pkg/convert/bpmn/dataassoc.go:59-68`, `eventNodeTags` `:16-24`) and a
  bare `<dataInput>`/`<dataOutput>` on one through `dataParamNote`
  (`importer.go:1983-1992`); both are pinned by
  `refusalwording_test.go:101-122`. Event builders take only the body's
  definitions as triggers (`dispatch.go:703-745`, `eventOptions`);
  `nodeBody.io` is parsed for `paramOwners` only (`dataflow.go:113-122`).
- The import-coverage guide carries the row
  (`docs/guides/extending/bpmn-import-coverage.md:33`).

**The standard** (`docs/bpmn-spec/semantics/data.md`): events have one set
of data associations, throw = input, catch = output, no `InputSet`/
`OutputSet`, never wait (§10.4.2 p224, `:243-257`); the Start/End special
case — process `DataInput`s as targets of the Start Event's output
associations, process `DataOutput`s as sources of the End Event's input
associations (`:259-264`); event data binding — one `DataInput`/`DataOutput`
per item-bearing `EventDefinition`, in definition order, with the item
equal to the definition's (p217, `:278-283`). The element tables list
`dataOutputs`/`dataOutputAssociations` on every catch kind and
`dataInputs`/`dataInputAssociations` on every throw kind
(`elements/events.md:52-53, 89-90, 131-132, 162-163, 192-193`).

## §2 Requirements

### Functional

**FR-1 — a catch event is an association source; a throw event is an
association target.** `catchEvent` implements `flow.AssociationSource`:
`Outputs()` returns its data outputs' item-aware elements,
`BindOutgoing(oa)` appends to `outputAssociations` (refusing nil and a
duplicate id, the task's rule). `throwEvent` implements
`flow.AssociationTarget`: `Inputs()` over its data inputs,
`BindIncoming(ia)` into `inputAssociations`. Every concrete event kind
inherits it: `StartEvent`, `IntermediateCatchEvent`, `BoundaryEvent`
(catch); `EndEvent`, `IntermediateThrowEvent` (throw). The existing
`DataObject.AssociateSource(event, ids, tf)` /
`AssociateTarget(event, tf)` then wire an event with no new API.

**FR-2 — the data is declared by option, under the standard's binding
rule.** `events.WithDataOutputs(params ...*data.Parameter)` (catch kinds)
and `events.WithDataInputs(params ...*data.Parameter)` (throw kinds) are
`EventOption`s. Validation at construction (p217): with *n* item-bearing
definitions on the event, the declared parameters pair with them **in
order**, and a paired parameter's item **must equal** the definition's
item (same `ItemDefinition` id) — a mismatch is refused naming both. A
declaration shorter than the definitions leaves the unpaired definitions
on today's behaviour: a message trigger keeps its auto-declared parameter
(`setMessage`, unchanged); the other item-bearing triggers now get one the
same way, so a Signal/Error/Escalation payload has an output to land in
and flow from. A parameter that pairs with no definition is refused: an
event's data comes from what triggered it (§10.4.2), a parameter nothing
fills is a modelling error said at construction, not a silent empty.
Timer, Conditional, Cancel, Compensation, Terminate carry no item and
accept no parameter — and so does an item-bearing definition whose item
has **no structure**: it has no value to carry, "if absent, payload does
not flow" (p217), so it declares nothing and pairs with nothing (the
pre-landing Undefined-state message parameter, which could never be
instantiated at run time, goes with it).

**Engine note — a throw's auto-declared input is association-only.** A
throw event's message binds its item from the execution by item id,
frame-first (`msgflow.Send` → `service.BindInput`). An auto-declared input
instantiated in the frame would therefore shadow the scope datum the
message bound from before this landing (the message-intermediate-events
example caught exactly that: the throw published the input's zero value
instead of the process property). So an auto-declared throw input is
**instantiated only when an input association targets it**
(`throwEvent.activeInputs`); untargeted, it stays out of the frame and the
message resolves as before. An input declared through `WithDataInputs` is
always instantiated — its state and gating are the caller's. And a message
throw with nothing Ready to bind — no input, no scope datum — publishes
the message's own item value rather than failing: the message is "sent
without payload data" (§10.4.1 p216's rule for a SendTask, read here for a
throw; `msgflow.SendResolved`). The end event had the shadow too, since
its message input was Ready from construction; it now binds the same way.

**FR-3 — the event copy path routes through scope (SRD-063 FR-5).**
`catchEvent.UploadData` pushes each output association the way
`task.UploadData` does: resolve the target by name from the frame
(`f.GetData(oa.TargetName())`), update the per-instance datum in place,
record the movement; a `DataStoreRef` target writes the store
(`uploadToStore`'s rule); an output not Ready is skipped (an optional
payload absent). `throwEvent.LoadData` fills each input the way
`task.loadFromScope` does — the source resolved by name from the frame,
the frame's input instance updated and flipped Ready; the never-wait gate
stays. The shared `Association` is read for routing only; no model object
is mutated at run time. The two helper paths are lifted out of `task`
into a shared unexported home in `pkg/model/data` or a sibling so the
task and the event run one implementation, not two.

**FR-4 — process parameters are association ends for Start and End
Events.** `process.Process` gains

```go
// AssociateInput wires a Start Event's output into the process's declared
// input named inputName (§10.4.2 — the process DataInputs are TARGETS of
// the Start Event's output associations). The event must be a StartEvent
// of this process; sourceID names one of its data outputs.
func (p *Process) AssociateInput(inputName string, from flow.AssociationSource, sourceID string) error

// AssociateOutput wires the process's declared output named outputName
// into an End Event's input (process DataOutputs are SOURCES of the End
// Event's input associations). The event must be an EndEvent of this
// process; targetID names one of its data inputs — the mirror of
// AssociateInput's sourceID (the importer hands the file's targetRef
// through; an item-based pick could not, since an imported definition's
// item is the converter's placeholder, §3.5).
func (p *Process) AssociateOutput(outputName string, to flow.AssociationTarget, targetID string) error
```

Both build a `data.Association` whose process-side end is the declared
parameter's item-aware element — so `TargetName()`/`SourceNames()` yield
the parameter's **name**, which is its root-scope name after ADR-040 —
and bind it on the event (`BindOutgoing`/`BindIncoming`). Refused: a
process with no contract, a name the contract does not declare, an event
that is not this process's Start/End, a source id the event does not
declare. The two ends' items are not compared — `DataObject.Associate*`
compares none either; an association copies a value, and a value the
target cannot hold faults where it is copied, at run time. The process
end is a fresh element over the declaration's item **named after the
parameter** (`namedElement`), so the declaration itself is untouched
(`NewAssociation` resets its target's state).

**FR-5 — a born start's output associations run at seed time, before the
contract binds.** In `seedInitialData`, after `bindEventPayload` and
`bindRootData` and before `bindContract`: when `cfg.bornStartID` names a
start that carries output associations, the instance runs them over the
root scope — the payload items just committed are the sources, the
declared process inputs (or data objects) the targets — through the same
FR-3 path, with a root-scope frame (`runBornStartAssociations`). A data
object target is already in the scope and is updated in place. A declared
input is **not** there yet — the contract binds it after this — so the
seed first commits a **placeholder** under each targeted input's name (a
fresh copy of the declaration's item, Ready), lets the association fill
it, and appends it to the launch's delivered data (`cfg.rootData`): the
value the message filled then binds through the declaration exactly as a
host-supplied one, type check included. A required input filled this way
passes `bindContract`; one still unbound is refused with the existing
message, which **no longer names #329** (`unboundInput`'s event-born
branch collapses into the plain one). The host and call launches are
unchanged (their start is not born, and runs as a node).

**FR-6 — an End Event's input associations may source the process
outputs.** No engine change beyond FR-3: at the end event's `LoadData`
the frame resolves the process output by name from the root scope, where
the flow left it. If the process output is not Ready there, the input
stays Unavailable and the throw's never-wait gate decides — a required
event input faults the node, an optional one does not flow.

**FR-7 — the importer attaches.** `wireDataAssoc` takes `eventNodeTags`
through the `Associate*` path: a `DataObject` end as for tasks; an end
whose id is a **process parameter** (the enclosing process's
`<ioSpecification>` input for a start's output association, output for an
end's input association) through FR-4; a `<property>` end stays #331. A
bare `<dataInput>`/`<dataOutput>` on an event maps to FR-2's options,
**always carrying the item of the definition it pairs with by position**
(p217): the converter builds every definition's payload item as a
placeholder rather than resolving the file's `itemRef` (`catalog.go`'s
`emptyItem`), and the engine binds a delivery by that item — so a file's
`itemSubjectRef` on an event parameter is checked for existence and kept
as the **file-level** identity §10.4.1's match compares when an
association joins the parameter to a data element (`paramItemRef`); a
parameter that names none is not compared. For the same reason an
event's input is addressed **by id** when a data element is wired into it
— `DataObject.AssociateTargetInput` / `DataStoreReference.AssociateTargetInput`,
the id-addressed siblings of `AssociateTarget` — where a task's input is
addressed by item, as before. A `<dataInputAssociation>` on a catch or a
`<dataOutputAssociation>` on a throw is refused as the position the
standard reserves (§10.4.2, one set per event); a bare parameter on a
task is refused pointing inside its `<ioSpecification>`, on any other
owner under the containment rule. The two refusals retire and the guide
row leaves.

**FR-8 — a runnable example.** `examples/event-data/`: a message-started
process with a declared input filled by the start's output association
from the message payload, a data object filled from an intermediate
message catch, and a message end event whose input association sources
the process output — proven end to end on a thresher with the in-memory
broker.

### Non-functional

**NFR-1 — no existing caller changes.** Message triggers still declare
their parameter; events without associations behave as before; the
`event_data_test.go` fixtures keep passing once rewritten through the
public surface.

**NFR-2 — scope isolation holds.** Two concurrent instances of one
snapshot exchanging data through event associations never observe each
other's values (the reason for FR-3).

**NFR-3 — never wait.** Nothing in this landing introduces a data wait:
a required event input unavailable at fire time faults the event; a
required process input unfilled after FR-5 refuses the launch.

**NFR-4 — the diff-coverage gate passes at `COVER_MIN`**, every reachable
branch of a touched function pinned in its own package; invariant
branches marked.

**NFR-5 — `make ci` green, both halves**, the example under
`run-examples`.

## §3 Models

### §3.1 `pkg/model/events` — the ends and the options

```go
// catchEvent — flow.AssociationSource
func (ce *catchEvent) Outputs() []*data.ItemAwareElement
func (ce *catchEvent) BindOutgoing(oa *data.Association) error

// throwEvent — flow.AssociationTarget
func (te *throwEvent) Inputs() []*data.ItemAwareElement
func (te *throwEvent) BindIncoming(ia *data.Association) error

// options (event_options.go); each config's sink pairs the parameters
// with its item-bearing definitions in order and validates the items
func WithDataOutputs(params ...*data.Parameter) EventOption // catch kinds
func WithDataInputs(params ...*data.Parameter) EventOption  // throw kinds
```

The `eventConfig` interface gains `setDataOutputs`/`setDataInputs`; the
start config accepts outputs and rejects inputs, the end config the
reverse; the intermediate and boundary constructors (which take a sole
definition positionally and `baseOpts`) accept the matching option among
their base options, the way they take `foundation.WithID`.

Validation happens **after** the triggers were folded (the definitions
must be known to pair): the constructors already apply options in order,
so the sinks record and the constructor's tail validates.

### §3.2 `pkg/model/dataflow` — the shared copy path

```go
// FillInput fills the frame input dst from its association's source — the
// per-instance datum by name (SRD-063 FR-5) or the Data Store the
// association names (SRD-068 FR-4) — flipping it Ready; a required input
// it cannot fill fails fast, an optional one stays Unavailable.
func FillInput(ctx, f exec.Frame, ia *data.Association, dst *data.Parameter, gating map[string]bool, owner string) error
// PushOutput copies the Ready output instance src into its association's
// target — the per-instance datum by name, or the Data Store; a not-Ready
// src pushes nothing.
func PushOutput(ctx, f exec.Frame, oa *data.Association, src *data.Parameter, owner string) error
```

The package is its own because `exec` imports `data` (a helper in `data`
would cycle), and the bodies are the task's `loadFromScope` /
`loadFromStore` / `uploadToStore` and the DataObject branch of its
`UploadData`, moved whole; `owner` labels the errors (`task "x"[id]`,
`event "y"[id]`). One implementation for tasks and events (FR-3);
`exec.Frame` already offers everything used (`GetData`,
`RecordDataMovement`, `DataStores`).

### §3.3 `pkg/model/process` — the process-parameter ends

`AssociateInput` / `AssociateOutput` per FR-4. Validation reads
`p.IOSpec()`; membership uses the process's node table; the event's
declared parameter is found by item equality (`Outputs()` /`Inputs()`),
mirroring `DataObject.AssociateSource`'s lookup by id.

### §3.4 `internal/instance` — the seed-time run

`seedInitialData` gains, between `bindRootData` and `bindContract`:

```go
if bornStart != nil {
    if err := inst.runBornStartAssociations(ctx, bornStart); err != nil { … }
}
```

`runBornStartAssociations` opens a root-scope frame, instantiates the
start's outputs, binds the received payload (the fired definition's
items, already in scope by id), and pushes the output associations
through FR-3 — the body of `catchEvent.UploadData` with the frame the
seed owns. `unboundInput` loses its event-born branch.

### §3.5 Importer

- `eventNodeTags` moves from "refuse" to "wire" (`eventdata.go`):
  `wireDataAssoc` checks the association's direction against the event's
  (`eventAssocDirection`), resolves the non-event end as today (data
  object / data store / property→#331) and, when the end id is a process
  parameter (`processParam`, the process spec's `io.params`), wires
  through `AssociateInput`/`AssociateOutput` (`bindProcessEnd`) — the
  process's own Start/End Events only. `bindAssoc` needs the one
  interface the direction uses and, on an event, wires a data element
  into the input **by id** (`AssociateTargetInput`).
- `nodeBody` gains `params []paramSpec` for an event's bare
  `<dataInput>`/`<dataOutput>` children (`parseEventParamElem`, over the
  extracted `parseParamSpec`), rendered by `eventOptions` /
  `soleEventOptions` into `WithDataOutputs`/`WithDataInputs`
  (`eventDataOptions`): the other direction refused, every parameter
  carrying its paired definition's item (`adoptedItems`), one past the
  item-bearing definitions refused. A bare parameter on a non-event owner
  is refused at build (`bareParamMisplaced`).
- `dataParamNote` drops its event clause; the association-on-event
  refusal is replaced by the one-set-per-direction refusal; the coverage
  guide row leaves and the converters guide names the events' data.
- The exporter is untouched (SRD scope: the importer); an event's data
  does not round-trip yet.

### §3.6 Worked example — the whole data path

```
quote (inputs: order (required); outputs: total (required))
  start[message "order placed", item order_in]
      ──dataOutputAssociation──▶ process input "order"
  price: reads "order", writes "total" into root scope
  end[message "quote ready", item quote_out]
      ◀──dataInputAssociation── process output "total"
```

1. A message `order placed` arrives; `NewFromEvent` commits `order_in`
   into the root scope by item id (today's `bindEventPayload`).
2. FR-5: the start's output association copies `order_in` → `order`
   (the process input's root-scope name); `bindContract` then binds
   `order` through its declaration — required, present, type-checked.
3. `price` runs, leaves `total` Ready in the root scope.
4. The end event's `LoadData` fills its `quote_out` input from `total`
   (FR-6, FR-3), and `msgflow.Send` publishes it — the input instance is
   what `BindInput` finds first.
5. `collectOutputs` reads `total` for the host (SRD-093 FR-8).

## §4 Analysis

### §4.1 Why interfaces, not a new wiring API

`DataObject.AssociateSource/Target` is the wiring API the whole data
family uses and the importer already calls; making events implement the
two node interfaces is the smallest change that lets every existing
caller wire an event. A parallel `events.Associate…` API would be a second
way to build the same `Association`.

### §4.2 Why the copy path is lifted now

The runtime half was written before SRD-063 moved data-object flow into
the per-instance scope, and stayed on the model-object path because
nothing could reach it. Attaching without lifting would ship a cross-
instance data leak on the first file that uses it. Sharing the task's
implementation rather than porting it is the "one rule, one place"
argument ADR-024 §2.16 makes for converters, applied to the engine.

### §4.3 Why the born start's associations run at seed time

The standard's Start/End case exists "to allow invoking a Process from
both a Call Activity and via Message Flow" (p224): the message-flow route
must fill the same process inputs the call route binds directly. The
engine treats a born start as already fired and never executes it, so
the only moment its associations can run is the seed — and it must be
*before* `bindContract`, or a required input the message supplies would be
refused as unbound. Running them in the seed also keeps ADR-040 §2.9's
promise that the contract touches exactly two moments.

### §4.4 Why a parameter that pairs with no definition is refused

The catch code keeps a "static-output path" for an output no payload
fills. Nothing in the standard fills such an output (§10.4.2: outputs are
filled with the triggering element), so a declared-but-never-filled
parameter is either a modelling mistake or a request for a constant —
and a constant belongs in a property. Refusing at construction says which
at the earliest moment (the SAD-001 §14.1 "error, never wait" shape).
Engine choice, called out in ADR-040 v.2 §2.7.

### §4.5 Why `AssociateInput`/`AssociateOutput` live on the Process

The process-side end is a declared parameter that the process owns and
whose root-scope name the process guarantees (SRD-093 FR-2's namespace);
membership of the event in the process is a process fact. A method on
`data.Parameter` could not check either.

### §4.6 Why the example is a message round trip

It is the one scenario that exercises every FR at once — the start's
association into a contract input (FR-4, FR-5), a catch into a data
object (FR-1, FR-3), an end sourcing a process output (FR-6) — on the
real broker, which is where the Start/End case earns its keep.

## §5 API

| Surface | Change |
|---|---|
| `events.StartEvent`, `IntermediateCatchEvent`, `BoundaryEvent` | implement `flow.AssociationSource` |
| `events.EndEvent`, `IntermediateThrowEvent` | implement `flow.AssociationTarget` |
| `events.WithDataOutputs`, `events.WithDataInputs` | new `EventOption`s (FR-2) |
| `process.Process.AssociateInput`, `AssociateOutput` | new (FR-4) |
| `dataflow.FillInput`, `dataflow.PushOutput` (new package `pkg/model/dataflow`) | the shared copy path (FR-3); `task` delegates |
| `internal/instance.seedInitialData` | runs the born start's output associations (FR-5) |
| `pkg/convert/bpmn` | events wire and declare; two refusals replaced |

All additive; `exec.Frame`, `data.Association`, the mocks unchanged.

## §6 Tests

| # | Test | Asserts | Covers |
|---|---|---|---|
| T-1 | `TestCatchEventIsAssociationSource` | `Outputs()` lists the declared outputs; `BindOutgoing` refuses nil and a duplicate | FR-1 |
| T-2 | `TestThrowEventIsAssociationTarget` | the mirror for inputs | FR-1 |
| T-3 | `TestWithDataOutputsPairsWithDefinitions` | in-order pairing, item mismatch refused naming both, extra parameter refused, message trigger's auto-parameter kept | FR-2 |
| T-4 | `TestWithDataInputsOnThrows` | same for throws; a timer/conditional catch refuses the option | FR-2 |
| T-5 | `TestSignalPayloadHasAnOutput` | a signal-triggered catch declares the signal item's output the way a message does | FR-2 |
| T-6 | `TestCatchUploadRoutesThroughScope` | two instances of one snapshot, a catch's output association each — values do not cross; the model association untouched | FR-3, NFR-2 |
| T-7 | `TestThrowLoadRoutesThroughScope` | the mirror on a throw's input association; required-unavailable faults the node | FR-3, NFR-3 |
| T-8 | `TestTaskAndEventShareTheCopyPath` | `task.UploadData`/`LoadData` call the shared helpers (existing task tests stay green) | FR-3 |
| T-9 | `TestAssociateInputValidates` | contract-less process, undeclared name, foreign event, unknown source id, item mismatch — each refused naming the part | FR-4 |
| T-10 | `TestAssociateOutputValidates` | the mirror | FR-4 |
| T-11 | `TestBornStartFillsProcessInput` (instance package) | a message-born launch with a required input wired from the start: bound, type-checked, instance runs; the same without the wiring refuses with the plain message and no `#329` | FR-5 |
| T-12 | `TestEndEventSourcesProcessOutput` | the end's input fills from the root-scope output; the published message carries it | FR-6 |
| T-13 | `TestImportEventAssociations` | a data object end on a start/catch/end/throw imports and runs | FR-7 |
| T-14 | `TestImportStartToProcessInput` | `<dataOutputAssociation targetRef=process-input>` wires through `AssociateInput`; the end mirror | FR-7 |
| T-15 | `TestImportBareEventIO` | `<dataOutput>` on a catch (with and without `itemSubjectRef`) declares; a wrong-direction association is refused as the reserved position | FR-7 |
| T-16 | `TestRefusalsSayWhichKindTheyAre` (rows) | the `#329` rows leave `refusalwording_test.go`; nothing names #329 | FR-7 |
| T-17 | e2e `TestEventDataRoundTrip` (thresher) | the §3.6 path on a real engine and broker | FR-5, FR-6, FR-8 |
| T-18 | `event_data_test.go` rewritten | the four fixtures attach through the public surface, not the fields | NFR-1 |
| T-19 | `TestThrowAutoInputIsAssociationOnly` | an untargeted auto input stays out of the frame and the scope datum resolves; a targeted one is instantiated and filled; a declared one is always instantiated | FR-2 |
| T-20 | `TestThrowBindsFromScopeWithoutAssociation` (thresher) | a message intermediate throw with no association publishes the scope datum of its item id — the `message-intermediate-events` shape | FR-2, NFR-1 |
| T-21 | `TestSendResolved`; `TestEndEventMessageThrowWithoutPayload`; the throw's "nothing to bind" case | a Ready datum is the payload, nothing Ready → the item's own value; the nil guards | FR-2 |

## §7 Milestones

| M | Scope | Commit |
|---|---|---|
| M1 | The ends and the options: `Outputs/BindOutgoing`, `Inputs/BindIncoming`, `WithDataOutputs/WithDataInputs`, the p217 pairing, item-bearing triggers' auto-parameters (T-1…T-5, T-18) | one |
| M2 | The shared copy path: the two helpers, `task` delegating, events routed through scope (T-6…T-8) | one |
| M3 | The process ends and the seed-time run: `AssociateInput/AssociateOutput`, `runBornStartAssociations`, the `#329` refusal retired (T-9…T-12) | one |
| M4 | Importer: events wire and declare, the refusals replaced, the guide row gone (T-13…T-16) | one |
| M5 | `examples/event-data/` + index and README rows; the e2e test (T-17) | one |
| M5a | Found by the gate's example sweep: a throw's auto-declared input, instantiated in the frame, shadowed the scope datum the message bound from by item id — `message-intermediate-events` published the input's zero value. An auto input is now association-only (`activeInputs`), a message with nothing Ready to bind goes with its own item value (`msgflow.SendResolved`); the FR-2 engine note (T-19…T-21) | one |

## §8 Cross-doc references

| Direction | Doc | What |
|---|---|---|
| up | [ADR-011 v.8](../design/ADR-011-process-data-flow.md) §2.5 | the event data model this attaches to |
| up | [ADR-040 v.2](../design/ADR-040-process-io-contract.md) §2.7, §2.9 | the Start/End path; the seed moment |
| up | [ADR-030 v.1](../design/ADR-030-data-objects-and-store.md) §2.3 | the scope-routed copy path |
| up | [ADR-010 v.2](../design/ADR-010-process-data-model.md) §2.3 | parameters as templates |
| up | [ADR-024 v.6](../design/ADR-024-process-interchange-converters.md) §2.16 | the register row this consumes |
| up | [SAD-001 v.1.2](../design/SAD-001-vision-and-architecture.md) §14.1 | never wait |
| side | [SRD-063](SRD-063-dataobject-scope-integration.md), [SRD-089.G](SRD-089.G-bpmn-import-data-flow.md), [SRD-093](SRD-093-process-io-contract.md), [SRD-015](SRD-015-message-correlation-instantiation.md) | the paths this reuses |

## §9 Definition of Done

1. FR-1…FR-8, NFR-1…NFR-5 wired.
2. Every §6 test exists and passes; `event_data_test.go` uses the public surface.
3. `make ci` green both halves; the example under `run-examples`.
4. Diff-coverage at `COVER_MIN`; every reachable branch pinned in its own package.
5. Doc-sync: ADR-011 §2.5's "lands with #329" sentence resolved (v.9); the coverage guide row gone; conformance-status; the examples index and READMEs; CHANGELOG.
6. #329 closed by the PR; the #335 checkbox ticked.

## §10 Implementation summary

*Post-landing placeholder.*

## Open questions

1. FR-2's "extra parameter refused" (§4.4) vs. keeping the static-output
   path for a catch: the draft refuses. If a static output has a use the
   author knows of, it becomes an engine choice the other way.
2. ~~Names in §3.2~~ — settled at M2: `pkg/model/dataflow`, `FillInput`
   and `PushOutput` (§3.2; `data` cannot host them, `exec` imports it).
