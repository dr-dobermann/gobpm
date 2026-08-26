# SRD-093 — The Process I/O contract: carrier, launch binding, result surface, import

| Field | Value |
|---|---|
| Status | Draft |
| Date | 2026-08-26 |
| Owner | Ruslan Gabitov |
| Implements | [ADR-040 v.1](../design/ADR-040-process-io-contract.md) (the single-set Process I/O contract: launch binding of inputs, completion reading of outputs, by-name launch-time validation at the call boundary, the permissive contract-less process, the publishing routes, the event-wiring deferral) |
| Upstream | [ADR-011 v.7](../design/ADR-011-process-data-flow.md) §2.2/§2.5 (the single-set parameter model; the scheduled Start/End path); [ADR-023 v.3](../design/ADR-023-sub-process-and-call-activity.md) (the Call Activity's direct mapping); [ADR-019 v.1](../design/ADR-019-definition-versioning.md) (latest-at-launch); [ADR-010 v.2](../design/ADR-010-process-data-model.md) §2.3/§2.7 (parameters as templates; the `RUNTIME` source); [ADR-038 v.3](../design/ADR-038-converter-coverage-boundaries.md) §2.3/§2.4 (the #330 register row; capability first, converter row after); [SAD-001 v.1.2](../design/SAD-001-vision-and-architecture.md) §14.1/§14.2 |
| Related | [SRD-050](SRD-050-call-activity.md) (the caller-side mapping this completes), [SRD-089.G](SRD-089.G-bpmn-import-data-flow.md) (the activity `<ioSpecification>` machinery this reuses), [SRD-007](SRD-007-process-data-model.md) (frames, root scope) |
| Closes | [#330](https://github.com/dr-dobermann/gobpm/issues/330) |

## §1 Background

[ADR-040 v.1](../design/ADR-040-process-io-contract.md) decides that a
`Process` declares a single-set I/O contract, that inputs bind into the
instance's root scope at launch and outputs are read from it at normal
completion, that the call boundary corresponds by name and validates at
launch, and that a process without a contract keeps its permissive meaning.
This SRD reconciles that decision with the codebase and lands it in five
milestones: the model carrier, the launch binding, the completion surface,
the import row, and a runnable example.

**What the model has today** (verified against the working tree):

- The parameter machinery activities use is complete and reusable:
  `data.Parameter` (`pkg/model/data/io_spec_obj.go:47-52` — name, embedded
  `ItemAwareElement`, `optional`, `whileExecuting`), `NewParameter` (`:80`,
  validates the name via `CheckName` and a non-nil element), `Optional()` /
  `WhileExecuting()` (`:61`, `:71`), and `data.InputOutputSpecification`
  (`pkg/model/data/io_spec.go:27-30`) whose `InputSet()`/`OutputSet()`
  (`:190`, `:197`) *are* the single sets — there is no `Set` type; its
  `Validate()` (`:72`) checks duplicate names per direction.
- Activities embed it as `IoSpec *data.InputOutputSpecification`
  (`pkg/model/activities/activity.go:28`), built by
  `activities.WithParameters(dir, params...)` (`activity_options.go:190`),
  an `ActivityOption` — so it cannot be handed to `process.New`, whose
  option switch (`pkg/model/process/process.go:78-100`) accepts
  `RoleOption`, `data.PropertyOption`, `lanes.LaneSetOption` and
  `foundation.BaseOption` only.
- `process.Process` (`process.go:36-50`) holds properties, roles, nodes,
  flows, data objects, store refs, lane sets, artifacts — **no I/O field**.
  `Validate()` (`:348-425`) is the registration-time hook (reached from
  `snapshot.New`, `internal/instance/snapshot/snapshot.go:99`); the
  name-clash precedent is `addDataObject` refusing a data object named
  like a property (`process.go:165-184`).
- The runtime gate for required parameters exists on tasks:
  `data.RequiredItemIDs` (`io_spec_obj.go:155`) and the start gate in
  `pkg/model/activities/task.go:136-150` — *"required input %q of task %q
  is unavailable (gobpm does not wait for data)"* — with the completion
  twin at `task.go:475`.

**What the engine has today:**

- `snapshot.Snapshot` (`snapshot.go:22-59`) carries `Properties`,
  `DataObjects`, `CorrelationKeys`, `InstantiatingStarts` — no I/O
  specification. `Clone` (`:517-561`) deep-copies properties and shares
  the immutable header by reference.
- Every instance constructor funnels through `seedInitialData`
  (`internal/instance/instance.go:622-647`): the event payload
  (`bindEventPayload`) then `bindRootData(cfg.rootData)`
  (`internal/instance/scope.go:221-229`, a plain root-scope `Commit`).
  `New` (`:511`), `NewFromEvent` (`:657`), `NewChild` (`:699`) all reach
  it; `Restore` recommits recorded scopes instead (`restore.go:188-219`).
- The call path is **caller-named**: `resolveCallInputs`
  (`internal/instance/calls.go:235-270`) reads the Call Activity's own
  declared input names at the caller's frame and clones each as a
  `*data.Parameter` under that name (`cloneNamed`, `:348-362`);
  `exec.ProcessCall` (`pkg/exec/invoker.go:63-69`) carries `Inputs
  []data.Data` and no output names; `InvokeProcess`
  (`pkg/thresher/invoker.go:24`) resolves the callee snapshot at `:55` and
  builds the child with `instance.NewChild(s, …, call.Inputs, …)` (`:74`);
  `bindCallOutputs` (`calls.go:275-304`) reads the caller's declared output
  names through `child.Outputs(names)`, which is `reader.GetData(name)` on
  the child (`invoker.go:139-158`). A failure anywhere on the call path
  reaches the caller as `exec.NewCallOutcome(err)` on the parked track
  (`calls.go:76`, `:89`, `:159`) — a fault **at the Call Activity node**.
- The host has no data door: `StartProcess`/`StartLatest`/`StartVersion`
  (`pkg/thresher/thresher.go:1484`, `:1504`, `:1533`) take no data, and
  `launchInstance` (`:1620-1658`) passes only `t.instanceOptions(settled)`.
  `InstanceHandle.WaitCompletion` (`pkg/thresher/handle.go:316-326`)
  returns `(InstanceState, error)`; the only data window is `Data()`
  (`:151`).
- Normal completion is decided in `exitLoop`
  (`internal/instance/loop.go:331-375`): after the incident park check
  (`:348`) and before `settleFinalState(ls.stopping)` (`:362`, impl
  `lifecycle.go:153-161` — `Terminated` if stopping, else `Completed`) and
  `markSettled` (`:368`). A fault is `inst.fail(err)`
  (`internal/instance/activation.go:66-86`), loop-goroutine-only.
- The read seam already honors a path-qualified name: `Frame.GetData`
  splits on `/` and dispatches to the named source
  (`internal/scope/frame.go:263-290`); `RUNTIME/STARTED_AT` is read that
  way by `examples/basic-process/process.go:87` and
  `pkg/thresher/handle_test.go:263`.
- The importer parses an activity's `<ioSpecification>` completely
  (`pkg/convert/bpmn/dataflow.go:364-582`) and builds parameters with
  `buildIOParams` (`:188-264`); a process-level one is refused through the
  default `settle` with the `plannedNotes` text naming #330
  (`pkg/convert/bpmn/importer.go:1930-1943`), pinned by
  `refusalwording_test.go:82-100` and used as the section-bearing sample in
  `bpmn_test.go:181-189`. `procBuild` buffers construction options
  (`laneSet`/`property`, `importer.go:669-715`) into `procSpec`, applied
  in `constructProcess` (`:771-812`), where `asm.items` is already
  resolvable (`parse()` builds items at `:437` before `build` at `:447`).

## §2 Requirements

### Functional

**FR-1 — the Process carries a single-set I/O specification.** `Process`
gains `ioSpec *data.InputOutputSpecification`, nil when the process
declares no contract, exposed by `IOSpec()`. Declaration goes through a
new option in the `data` package — the package that owns parameters and
already supplies `WithProperties` to `process.New`:

```go
// IOSpecAdder is what a container's config implements to take I/O
// parameters (the PropertyAdder pattern).
type IOSpecAdder interface {
    AddIOParameters(dir Direction, params ...*Parameter) error
}

type IOSpecOption func(cfg IOSpecAdder) error

// WithInputs / WithOutputs declare a callable's input / output parameters
// (ADR-040 §2.1). Each accumulates across calls; a nil parameter is refused.
func WithInputs(params ...*Parameter) IOSpecOption
func WithOutputs(params ...*Parameter) IOSpecOption
```

`processConfig` implements the adder and `process.New` gains the switch
arm; `newProcess()` materializes the specification only when at least one
parameter was declared, so a process with none keeps `IOSpec() == nil` —
the ADR §2.5 permissive contract, distinguishable from an empty declared
one.

**FR-2 — one namespace, validated at registration.** `Process.Validate()`
refuses (`errs.DuplicateObject`) any declared parameter whose name
collides with a property, a data object, or a parameter of the other
direction; the specification's own `Validate()` (per-direction duplicates)
runs first. All root-scope residents share one name space (ADR-040 §2.6).

**FR-3 — the snapshot carries the contract by reference.** `Snapshot`
gains `IOSpec *data.InputOutputSpecification`, set by `New` and shared by
`Clone` like `CorrelationKeys` — a declaration, not per-instance state
(the same reasoning as an activity's `IoSpec: a.IoSpec` share,
`activity.go:125`).

**FR-4 — inputs bind at launch, through the declared parameter.**
`seedInitialData` gains, after `bindRootData`, the contract binding: for
each declared input, the root-scope datum of that name (whatever the entry
delivered — the caller's cloned parameter, the host's start data) is read;
its **value is bound into an instance of the declared input parameter**
(the declaration's item definition as the template, ADR-010 §2.3), and
that parameter replaces the raw datum in the root scope. So the value is
type-checked against the declaration at the boundary. A **required input
with no datum refuses the launch** with an error naming the process and
the input (ADR-040 §2.2); an optional one is simply absent.

**FR-5 — with a contract, the boundary is strict both ways.** When the
process declares a contract, a root-scope datum delivered at launch that
names **no declared input** refuses the launch too — a caller sending
`ammount` to a callee declaring `amount` fails once, at the boundary, with
both names in the message, instead of leaving a typo'd datum in scope and
a required input missing. With no contract (nil `IOSpec`), every delivered
datum binds as today (ADR-040 §2.5).

**FR-6 — the host gets a data door.** `StartProcess`, `StartLatest` and
`StartVersion` gain variadic `...StartOption`:

```go
// WithStartInputs supplies the launch's input values by name (ADR-040
// §2.2 — the host's start request). A nil datum is refused. "Start" says
// which moment: data.WithInputs DECLARES a slot, this SUPPLIES its value.
func WithStartInputs(dd ...data.Data) StartOption
// WithStartInput is the one-value convenience: a name and a Go value,
// lifted through values.NewVariable.
func WithStartInput(name string, value any) StartOption
```

`launchInstance` passes them as `withRootData`. Existing callers compile
unchanged.

**FR-7 — an event-born launch with a required input declared is
refused.** `NewFromEvent` runs the same binding; since no mechanism fills a
process input from a start-event payload until the attachment capability
lands (ADR-040 §2.7), a message- or signal-started process that declares a
**required** input cannot be launched — the refusal names #329. Optional
inputs are fine. Loud rather than silent: a process that claims a required
input and gets none is the exact failure the contract exists to end.

**FR-8 — outputs are read at normal completion.** In `exitLoop`, after the
incident park check and before `settleFinalState`: when the loop is not
stopping and the snapshot carries a contract, each declared output is read
from the root scope and cloned into the instance's **result**
(`[]data.Data`, exposed by `Instance.Outputs()`). A **required output that
is absent or not `Ready` faults the instance**: `inst.fail(err)` with the
output named, and the loop's `stopping` flips so the state settles
`Terminated` with `LastErr()` set — an abnormal end, no result surface
(ADR-040 §2.3). An optional output not produced is skipped.

**FR-9 — the result reaches the caller and the host.**
`childProcess.Outputs(names)` keeps its reader path (the contract-less
child, ADR-040 §2.5) but serves a contracted child's names from the
collected result; `InstanceHandle` gains `Outputs() []data.Data` — the
declared result of a completed instance, empty before completion or after
an abnormal end.

**FR-10 — the call boundary validates at launch, by name.**
`exec.ProcessCall` gains `Outputs []string` (the caller's declared output
names, filled by `onCallWaiting` from `ca.CallOutputs()`).
`InvokeProcess`, once the callee snapshot is resolved (the moment ADR-019
fixes the version), checks — when the callee declares a contract — that
every caller output names a declared callee output, and refuses the call
otherwise with both sides named. The input half of the check is FR-4/FR-5
inside `NewChild`, so the whole boundary faults at the Call Activity
through the existing `NewCallOutcome(err)` channel, catchable by an Error
boundary there.

**FR-11 — the importer maps `<ioSpecification>` on a `<process>`.**
`procBuild` buffers it like a lane set (refusing one that follows the flow
elements, the same ordering guard), reusing `parseIOSpecification` whole;
`constructProcess` builds the parameters through the spec-level half of
`buildIOParams` — extracted so it takes `[]paramSpec` and the owner's
name, the association-partner adoption staying with the activity caller —
and passes `data.WithInputs(...)`/`data.WithOutputs(...)`. The `plannedNotes` #330 text
retires; `dataParamNote` names the process beside the task as an
`<ioSpecification>` owner; a second `<ioSpecification>` on the process is
refused like an activity's. Bare `<dataInput>`/`<dataOutput>` under
`<process>` stay refused with the corrected note.

### Non-functional

**NFR-1 — zero behavioural change for contract-less processes.** Every
existing process, call and example runs exactly as before: no `IOSpec`
means the permissive path, the current code's behaviour.

**NFR-2 — the fault is at the boundary, never inside the child.** A
contract mismatch surfaces before the child's first track runs (launch)
or at the caller's Call Activity node; no new failure mode appears
mid-flow.

**NFR-3 — no import cycles.** `data` gains no imports; `process` and
`thresher` already import `data`; `exec.ProcessCall` gains a `[]string`.

**NFR-4 — the diff-coverage gate passes at `COVER_MIN`**, every touched
function at 100%; `make gen_mock_files` re-run if a mocked interface
moves (none is expected to).

**NFR-5 — the example runs under `run-examples`**: exit 0 within the 90 s
budget, stdin `/dev/null`, its own `go.mod`.

## §3 Models

### §3.1 `pkg/model/data` — the option

```go
type IOSpecAdder interface {
    AddIOParameters(dir Direction, params ...*Parameter) error
}

type IOSpecOption func(cfg IOSpecAdder) error

func (IOSpecOption) Option() {}

func WithInputs(params ...*Parameter) IOSpecOption  { return withIOParams(Input, params) }
func WithOutputs(params ...*Parameter) IOSpecOption { return withIOParams(Output, params) }

func withIOParams(dir Direction, params []*Parameter) IOSpecOption {
    return func(cfg IOSpecAdder) error {
        // a nil params[i] → EmptyNotAllowed with errs.D("index", i); then
        return cfg.AddIOParameters(dir, params...)
    }
}
```

### §3.2 `pkg/model/process` — the carrier

```go
type Process struct {
    // …existing fields…
    // ioSpec is the process's declared I/O contract (ADR-040 §2.1): one
    // input set, one output set, nil when the process declares none — the
    // permissive, contract-less process (§2.5).
    ioSpec *data.InputOutputSpecification
}

// IOSpec returns the declared I/O contract, or nil when there is none.
func (p *Process) IOSpec() *data.InputOutputSpecification
```

`processConfig` gains `ioParams map[data.Direction][]*data.Parameter` and
`AddIOParameters`; `newProcess()` builds the specification with
`data.NewIOSpec()` + `AddParameter` when any parameter was declared.

### §3.3 Snapshot and instance

```go
type Snapshot struct {
    // …
    // IOSpec is the process's declared I/O contract (ADR-040), shared by
    // reference across clones: a declaration, not per-instance state.
    IOSpec *data.InputOutputSpecification
}

// Instance gains:
//   result []data.Data — the declared outputs read at normal completion
func (inst *Instance) Outputs() []data.Data
```

`newConfig` is untouched: the contract travels in the snapshot, the
delivered data in `rootData` as today.

### §3.4 The engine surface

```go
// pkg/thresher
type StartOption func(*startConfig) error
func WithStartInputs(dd ...data.Data) StartOption
func WithStartInput(name string, value any) StartOption
func (t *Thresher) StartLatest(key string, opts ...StartOption) (*InstanceHandle, error)
// StartProcess, StartVersion likewise
func (h *InstanceHandle) Outputs() []data.Data

// pkg/exec
type ProcessCall struct {
    Key, ParentInstanceID, CallNodeID string
    Inputs  []data.Data
    Outputs []string // the caller's declared output names (SRD-093 FR-10)
    Version int
}
```

### §3.5 Importer

`procBuild` gains `io *ioSpec`; `procSpec` gains `io *ioSpec`;
`constructProcess` gains the block:

```go
if spec.io != nil {
    params, err := buildParamSpecs(p, asm,
        "process "+strconv.Quote(spec.id), spec.io.params)
    …
    if pp := params[data.Input]; len(pp) != 0 {
        opts = append(opts, data.WithInputs(pp...))
    }
    if pp := params[data.Output]; len(pp) != 0 {
        opts = append(opts, data.WithOutputs(pp...))
    }
}
```

`buildIOParams` becomes a thin activity wrapper over `buildParamSpecs`,
which owns the item resolution, the §4.3a duplicate-item guard, the
element construction and the option flags; the association-partner
adoption (`paramItem`'s `assocPartnerSpec` branch) stays in the wrapper —
a process has no associations to adopt from.

### §3.6 Worked examples — the whole data path

#### A. Authoring the contract and running it from the host

```go
// pricing declares: input subtotal (int, required), input discount (int,
// optional); outputs total (int, required), computedAt (string, optional).
intItem := func() *data.ItemAwareElement {
    return data.MustItemAwareElement(
        data.MustItemDefinition(values.NewVariable(0)), data.ReadyDataState)
}
sub := data.MustParameter("subtotal", intItem())
disc := data.MustParameter("discount", intItem(), data.Optional())
total := data.MustParameter("total", intItem())
at := data.MustParameter("computedAt",
    data.MustItemAwareElement(
        data.MustItemDefinition(values.NewVariable("")), data.ReadyDataState),
    data.Optional())

pricing, _ := process.New("pricing",
    foundation.WithID("pricing"),
    data.WithInputs(sub, disc),
    data.WithOutputs(total, at))

// A Go operation that reads the inputs and RUNTIME/STARTED_AT through its
// data reader (SAD-001 §14.2) and returns total + computedAt as its
// outputs; its output associations target the process outputs by name —
// the ADR-040 §2.3a publishing route.
compute, _ := activities.NewServiceTask("compute", pricingOp,
    activities.WithParameters(data.Output, /* total, computedAt */))
// …start → compute → end; output associations compute.total → "total",
//    compute.computedAt → "computedAt"…

th.RegisterProcess(pricing)
h, err := th.StartLatest("pricing",
    thresher.WithStartInput("subtotal", 120),
    thresher.WithStartInput("discount", 20))
state, err := h.WaitCompletion(ctx)   // Completed, nil
for _, d := range h.Outputs() {       // total=100, computedAt="2026-…"
    fmt.Println(d.Name(), d.Value().Get(ctx))
}
```

The refusal at the boundary, same process:

```go
_, err = th.StartLatest("pricing", thresher.WithStartInput("discount", 5))
// err: process "pricing": required input "subtotal" is unbound at launch
_, err = th.StartLatest("pricing",
    thresher.WithStartInput("subtotal", 120), thresher.WithStartInput("subttl", 1))
// err: process "pricing" declares no input "subttl" — delivered at launch
//      (declared inputs: subtotal, discount)
```

#### B. The call boundary

```go
// checkout declares a Call Activity "charge" calling "pricing" with
// Input "subtotal" and Output "total" (the caller's own names):
charge, _ := activities.NewCallActivity("charge", "pricing",
    activities.WithParameters(data.Input, callerSubtotal),
    activities.WithParameters(data.Output, callerTotal))
```

At launch `onCallWaiting` resolves `subtotal` at the caller's scope, the
call carries `Outputs: ["total"]`, `InvokeProcess` checks `total` is a
declared `pricing` output, `NewChild` binds `subtotal` through the declared
parameter — `discount` is optional and absent — and the child runs. At the
child's normal completion its result holds `total` and `computedAt`;
`bindCallOutputs` commits `total` into the caller's scope.

The mismatch, caught at the boundary: a caller output named `grandTotal`
fails in `InvokeProcess` — *call activity "charge": output "grandTotal" is
not declared by process "pricing" (declared outputs: total, computedAt)* —
delivered as the Call Activity's fault, catchable there.

#### C. The result faulting at completion

Remove the `compute` output association for `total`: the child reaches
its end with `total` never written. `exitLoop` reads the declared outputs,
finds the required `total` absent, `inst.fail(…)` with *process "pricing":
required output "total" is unavailable at completion*, settles
`Terminated`; the host's `WaitCompletion` returns `(Terminated, that
error)` and `Outputs()` is empty; a caller's `handleCallCompletion` sees
`Failed()` and faults at `charge`.

#### D. The XML import

```xml
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <bpmn:itemDefinition id="idInt" structureRef="xsd:int"/>
  <bpmn:process id="pricing" name="pricing">
    <bpmn:ioSpecification id="io">
      <bpmn:dataInput id="in-subtotal" name="subtotal" itemSubjectRef="idInt"/>
      <bpmn:dataInput id="in-discount" name="discount" itemSubjectRef="idInt"/>
      <bpmn:dataOutput id="out-total" name="total" itemSubjectRef="idInt"/>
      <bpmn:inputSet id="is">
        <bpmn:dataInputRefs>in-subtotal</bpmn:dataInputRefs>
        <bpmn:dataInputRefs>in-discount</bpmn:dataInputRefs>
        <bpmn:optionalInputRefs>in-discount</bpmn:optionalInputRefs>
      </bpmn:inputSet>
      <bpmn:outputSet id="os">
        <bpmn:dataOutputRefs>out-total</bpmn:dataOutputRefs>
      </bpmn:outputSet>
    </bpmn:ioSpecification>
    <bpmn:startEvent id="s"/>
    <bpmn:task id="t" name="compute"/>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>
```

Today: `UnsupportedElementError{Tag: "ioSpecification"}` naming #330.
After this SRD:

```go
res, _ := convert.ImportDocument(ctx, convert.BPMN, r)
ios := res.Processes[0].IOSpec()
ins := ios.InputSet()   // subtotal (required), discount (optional)
outs := ios.OutputSet() // total (required)
ins[1].IsOptional()     // true — from <optionalInputRefs>
```

A second `<ioSpecification>` on the process refuses like an activity's;
`<inputSet>` twice refuses with the single-set text; a bare `<dataInput>`
under `<process>` still refuses, its note now naming the process beside
the task as the owner whose `<ioSpecification>` it belongs inside.

## §4 Analysis

### §4.1 Why the option lives in `data`, and why it is not `WithParameters`

`activities.WithParameters` is typed `ActivityOption` over
`activityConfig`; reusing it would force `process.New` to type-switch on
an activity option and give `processConfig` an activity's config surface.
The `data` package already owns the parameter types and already ships the
container-agnostic `PropertyOption`/`PropertyAdder` pair that
`process.New` consumes — the I/O option is the same pattern one type
over. The pair is **direction-named** (`WithInputs`/`WithOutputs`) rather
than direction-parameterized like `activities.WithParameters(dir, …)`: the
call site reads as the contract itself, and a file building a process and
its tasks together never sees two `WithParameters`. The launch-side
options carry the `Start` prefix (`thresher.WithStartInputs`) so "inputs"
on `data` always means *declaring* and on `thresher` always means
*supplying at launch*.

### §4.2 Why binding goes *through* the declared parameter

The caller delivers a `*data.Parameter` under its own name with its own
item (`cloneNamed`); the host delivers whatever `data.Data` it built. If
the raw datum stayed in scope, the declaration would be documentation: a
caller could pass a string where the callee declared an `int` and nothing
would notice until a task read it. Instantiating the **declared**
parameter and binding the delivered value into it makes the declaration
the datum — the type check happens where ADR-040 §2.2 says the boundary
is, and every later read sees exactly the declared item.

### §4.3 Why strict both ways (FR-5), when the ADR lists only the required-input check

ADR-040 §2.4 names what *must* match; it does not say what to do with a
delivered datum the callee never declared. Accepting it would keep the
permissive behaviour alive *inside* a declared contract — and hide the
most common mistake, a misspelled name, as "required input missing"
alone, with the offending datum silently resident under the wrong name.
Refusing the undeclared datum turns that into one error naming both the
missing input and the stray datum. A callee that genuinely wants to accept
anything declares no contract (§2.5).

### §4.4 Why the event-born launch refuses a required input (FR-7)

The alternative — skip the check for event-born instances — lets a
process declare a required input and run without it whenever it is
message-started, reproducing inside the child the late scope failure the
whole contract exists to prevent. Refusing is honest about the deferral:
the fill needs the attachment capability (#329), and until then a required
process input and a message start do not combine. Nothing existing breaks:
no registered process declares an input today.

### §4.5 Why the fault at completion flips `stopping`

`settleFinalState` has exactly one discriminator (`ls.stopping`), and
`Failed` is a phase, not a state. An instance that reached its end without
a promised output must not settle `Completed` — a caller's
`bindCallOutputs` would then run and fail on a *completed* child, a worse
diagnosis than a terminal fault naming the output. Flipping `stopping`
after `inst.fail` reuses the existing terminal-fault shape: state
`Terminated`, `LastErr()` set, no outputs read (`handleCallCompletion`
checks `Failed()` first, `calls.go:148`), the caller faulted at its Call
Activity.

### §4.6 Why the output-name check is in `InvokeProcess` and the input check in the instance

The input check needs the delivered data, which exists only when the
child is constructed — and it must run for the host entry too, which
never passes through `InvokeProcess`. The output check needs the caller's
declared output names, which only the call carries — hence the new
`ProcessCall.Outputs` and the check where the callee's snapshot is first
in hand (`resolveCallLocked`). Both faults reach the same channel.

### §4.7 Why `Outputs()` copies at completion instead of reading lazily

`childProcess.Outputs` reads the child's live reader today; that works
because a completed instance's scope persists. A collected result is (a)
the contract's *committed* value at the completion moment — ADR-040
§2.3a's copy rule at the instance level — and (b) available after the
instance is reaped or restored from a terminal checkpoint, where the
reader is not (`newSettledChild` already decodes outputs from the
checkpoint's root scope, `invoker.go:283-314`, for exactly that reason).

### §4.8 Why the example is Go-built, and the XML path is a test

The one flow an example needs — a task *computing* an output — requires an
operation, and an imported `<serviceTask>` needs an interface catalog the
example would have to invent; a manual `<task>` computes nothing. So the
runnable example authors both processes in Go and shows the whole path
(host launch with inputs, the call boundary, the runtime-variable publish,
the boundary refusal), while the XML import of `<ioSpecification>` on a
`<process>` is proven by the converter tests (T-15…T-18) and shown in
§3.6-D.

## §5 API

| Symbol | Package | Change |
|---|---|---|
| `IOSpecAdder`, `IOSpecOption`, `WithInputs`, `WithOutputs` | `data` | new |
| `Process.IOSpec()` | `process` | new |
| `Snapshot.IOSpec` | `snapshot` | new field |
| `Instance.Outputs()` | `instance` | new |
| `StartOption`, `WithStartInputs`, `WithStartInput` | `thresher` | new |
| `StartProcess/StartLatest/StartVersion(…, opts ...StartOption)` | `thresher` | variadic widening, source-compatible |
| `InstanceHandle.Outputs()` | `thresher` | new |
| `ProcessCall.Outputs []string` | `exec` | new field (additive) |

No existing signature breaks; the consumer-smoke gate proves it.

## §6 Tests

| # | Test | Asserts | FR |
|---|---|---|---|
| T-1 | `TestWithInputsOutputs` | each option lands in its direction; nil parameter refused with index; accumulates across calls | FR-1 |
| T-2 | `TestProcessIOSpec` | declared parameters read back per direction; a process without any has `IOSpec() == nil` | FR-1 |
| T-3 | `TestProcessIONameSpace` | a parameter named like a property / a data object / an opposite-direction parameter fails `Validate()` naming both | FR-2 |
| T-4 | `TestSnapshotCarriesIOSpec` | `New` sets it; `Clone` shares the pointer | FR-3 |
| T-5 | `TestLaunchBindsDeclaredInputs` | host `WithStartInput` values arrive as the declared parameters (typed by the declaration); an optional absent stays absent | FR-4, FR-6 |
| T-6 | `TestLaunchRefusesUnboundRequiredInput` | the launch fails naming the process and the input; no instance exists afterwards | FR-4 |
| T-7 | `TestLaunchRefusesUndeclaredDatum` | a datum naming no declared input fails naming it and the declared set; a contract-less process accepts it | FR-5 |
| T-8 | `TestLaunchTypeChecksInput` | a string delivered to an `int` input refuses the launch | FR-4 |
| T-9 | `TestEventBornLaunchWithRequiredInputRefused` | a message-start process declaring a required input is refused naming #329; with only optional inputs it starts | FR-7 |
| T-10 | `TestOutputsCollectedAtCompletion` | `Instance.Outputs()`/`handle.Outputs()` hold the declared outputs after `Completed`; copies, not live | FR-8, FR-9 |
| T-11 | `TestMissingRequiredOutputFaults` | state `Terminated`, `LastErr()` names the output, `Outputs()` empty; an optional output absent is skipped | FR-8 |
| T-12 | `TestCallBoundaryValidatesOutputs` | a caller output not declared by the callee faults at the Call Activity, catchable by an Error boundary | FR-10 |
| T-13 | `TestCallBindsThroughDeclaredInputs` | the child's `subtotal` is the declared parameter; a contract-less callee keeps today's behaviour (the existing call-activity tests unchanged) | FR-4, NFR-1 |
| T-14 | `TestChildOutputsServedFromResult` | `childProcess.Outputs` for a contracted child come from the collected result | FR-9 |
| T-15 | `TestIOSpecificationOnAProcess` | the §3.6-D document imports; parameters, optionality, items as declared | FR-11 |
| T-16 | `TestProcessIOSpecOrdering` | an `<ioSpecification>` after the flow elements is refused (the laneSet guard) | FR-11 |
| T-17 | `TestProcessSecondIOSpecRefused` (+ the multi-set row) | the activity refusals hold at process level | FR-11 |
| T-18 | `TestProcessBareDataInputRefused` | the note names the process as an owner; `#330` no longer appears anywhere | FR-11 |
| T-19 | e2e `TestContractlessProcessesUnchanged` | the thresher suite's existing call and start tests pass untouched | NFR-1 |
| T-20 | `TestEventBornLaunchWithRequiredInputRefused` (fact sink) | a refused launch leaves no `Created` fact — the instance never existed | FR-4 |

The retired pins are rewritten in the same milestone: the
`"ioSpecification on a process"` row of `refusalwording_test.go`
(removed), the section-bearing sample in `bpmn_test.go:181-189` (a
different refused element).

## §7 Milestones

| M | Scope | Commit |
|---|---|---|
| M1 | `data.WithInputs`/`WithOutputs`, the `Process` carrier, the namespace validation (T-1…T-3) | one |
| M2 | Snapshot carriage, the launch binding with type check and both refusals, the host `StartOption`, the event-born rule (T-4…T-9) | one |
| M3 | Completion reading, the required-output fault, `Outputs()` on instance and handle, `ProcessCall.Outputs` + the invoker check, result-served child outputs (T-10…T-14, T-19) | one |
| M4 | Importer: the buffered process `<ioSpecification>`, the `buildParamSpecs` extraction, retired refusal text and pins (T-15…T-18) | one |
| M5 | `examples/process-io/` (process.go / handlers.go / launch.go / check.go / main.go / README) + the examples index and README rows | one |
| M5a | Found by M5's run log: `instance.New` announced `Created` before the scope load and the contract binding, so a refused launch left an orphan fact with no transition after it. The announcement now follows a successful seed (T-20) | one |

## §8 Cross-doc references

| Direction | Document | Why |
|---|---|---|
| up | [ADR-040 v.1](../design/ADR-040-process-io-contract.md) | the decision this implements |
| up | [ADR-011 v.7](../design/ADR-011-process-data-flow.md) §2.2, §2.5 | the single-set parameter model; the scheduled Start/End path this delivers the carrier of |
| up | [ADR-023 v.3](../design/ADR-023-sub-process-and-call-activity.md) | the call mapping whose callee side lands |
| up | [ADR-019 v.1](../design/ADR-019-definition-versioning.md) | latest-at-launch, fixing the validation moment |
| up | [ADR-010 v.2](../design/ADR-010-process-data-model.md) §2.3, §2.7 | parameters as per-execution templates; the `RUNTIME` source |
| up | [ADR-038 v.3](../design/ADR-038-converter-coverage-boundaries.md) §2.3, §2.4 | the #330 register row and the ordering |
| up | [SAD-001 v.1.2](../design/SAD-001-vision-and-architecture.md) §14.1, §14.2 | the deviations extended; the Go operation reader |
| side | [SRD-050](SRD-050-call-activity.md) | the caller-side mapping (frozen; its permissive behaviour is preserved for contract-less callees) |
| side | [SRD-089.G](SRD-089.G-bpmn-import-data-flow.md) | the ioSpecification parser reused (frozen; its #330 deferral row is superseded by this landing) |

No downward references.

## §9 Definition of Done

1. FR-1…FR-11 implemented and wired; NFR-1…NFR-5 held.
2. Every §6 test exists and passes; the two retired pins rewritten.
3. `make ci` green, both halves, the new example executing under
   `run-examples`.
4. Diff-coverage at `COVER_MIN`; every touched function at 100%.
5. The doc-sync commit: ADR-038 §2.3's #330 register row retired as
   consumed; ADR-011 §2.5's "lands with…" sentence updated to point at the
   landed carrier and the remaining event-wiring deferral; ADR-023's I/O
   bullet gains the callee side; `conformance-status.md`; the examples
   index and both READMEs.
6. [#330](https://github.com/dr-dobermann/gobpm/issues/330) closed by the
   landing PR; the [#335](https://github.com/dr-dobermann/gobpm/issues/335)
   checkbox ticked.

## §10 Implementation summary

*Post-landing placeholder.*

## Open questions

None — the implementation-level decisions a reviewer would ask about are
recorded in §4 (strict both ways, the event-born refusal, the completion
fault shape, the split of the boundary check, the collected result, the
Go-built example).
