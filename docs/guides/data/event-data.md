---
title: Event data and the process contract
description: The data an event carries, how it is wired to Data Objects, Data Stores and the process's own inputs and outputs, and what a message-born launch fills.
---

# Event data and the process contract

A **catch** event (Start, Intermediate catch, Boundary) receives something —
a message, a signal, an escalation — and has **data outputs**: the payload
it caught, pushed through its **output associations** into the scope. A
**throw** event (End, Intermediate throw) emits something and has **data
inputs**: what it will carry, filled through its **input associations**
from the scope. That is the standard's §10.4.2 in one sentence, and it is
the same association model a task uses — the only differences are that an
event has no input/output *sets* and **never waits for data**: a required
input that cannot be filled when the event fires is an error, not a wait.

The process's own **Start** and **End** events have one more role. A
process may declare its **I/O contract** — named, typed inputs bound at
launch and outputs read at completion — and the standard reserves two
positions for it: a Start event's output association may target a process
**input**, an End event's input association may source a process
**output**. So a process started by a message fills its contract from the
payload, and a process ending with a message throws its declared result.

This page covers the event side: what an event declares, how it is wired,
and the contract positions. The contract itself — `data.WithInputs` /
`data.WithOutputs` on `process.New`, `thresher.WithStartInput`, the Call
Activity binding — is the process's declaration; the message route here is
one more way to fill it.

## What an event declares

An event's data comes from what triggers it. An **item-bearing** trigger
declares its payload parameter itself:

| Trigger | Declares |
|---|---|
| Message | one parameter over the message's item — an output on a catch, an input on a throw |
| Signal / Escalation *with a structure* | the same, over the signal's / escalation's item |
| Timer, Conditional, Error, Terminate, Link, … | nothing — no payload, no parameter |

You can declare more with two options, one per side:

```go
events.WithDataOutputs(params ...*data.Parameter) // catch kinds: Start, Intermediate catch, Boundary
events.WithDataInputs(params ...*data.Parameter)  // throw kinds: End, Intermediate throw
```

The declared parameters pair with the event's item-bearing definitions
**in order** (BPMN p217): the first with the first definition, the second
with the second; a parameter past the last item-bearing definition is
refused, and a paired parameter's item must be the definition's item. A
declared parameter replaces the automatic one for its definition, which is
how you set the parameter's **state** — an End event whose input is
declared `Unavailable` and required can only fire once an association
filled it:

```go
end, err := events.NewEndEvent("end",
    events.WithMessageTrigger(events.MustMessageEventDefinition(
        bpmncommon.MustMessage("quote ready", item("quote_out", 0)), nil)),
    // required and Unavailable: only the association can make the throw fire
    events.WithDataInputs(data.MustParameter("quote",
        data.MustItemAwareElement(item("quote_out", 0), data.UnavailableDataState))))
```

Every event exposes its ends:

| Side | Methods |
|---|---|
| catch (`flow.AssociationSource`) | `Outputs() []*data.ItemAwareElement` · `BindOutgoing(*data.Association) error` · `OutputAssociations()` |
| throw (`flow.AssociationTarget`) | `Inputs() []*data.ItemAwareElement` · `BindIncoming(*data.Association) error` · `InputAssociations()` |

You rarely call `BindOutgoing`/`BindIncoming` yourself — the wiring methods
below do — but `Outputs()`/`Inputs()` give you the parameter **ids** the
process-level wiring takes.

## Wiring an event

An event is wired exactly like a task, from the data element's side:

| From → to | Call |
|---|---|
| catch event → Data Object | `dataObject.AssociateSource(catch, []string{itemID}, nil)` |
| catch event → Data Store | `storeRef.AssociateSource(catch, []string{itemID}, nil)` |
| Data Object → throw event | `dataObject.AssociateTargetInput(throw, throw.Inputs()[0].ID(), nil)` |
| Data Store → throw event | `storeRef.AssociateTargetInput(throw, throw.Inputs()[0].ID(), nil)` |

`AssociateSource` names the event's output by its **item id** — the
message's or signal's item — as it does for a task's output.
`AssociateTargetInput` is the id-addressed form of `AssociateTarget`: an
event's input carries its *definition's* item, so the input is named by its
own id rather than by item. Both forms exist on `DataObject` and
`DataStoreReference`.

A Data Object fed by an association is `Unavailable` until its producer
writes it; the push marks it `Ready`, so a throw downstream can read it:

```go
catch, _ := events.NewIntermediateCatchEvent("catch",
    events.MustMessageEventDefinition(bpmncommon.MustMessage("order placed",
        data.MustItemDefinition(values.NewVariable(""), foundation.WithID("order_in"))), nil))
throw, _ := events.NewIntermediateThrowEvent("throw",
    events.MustMessageEventDefinition(bpmncommon.MustMessage("order relayed",
        data.MustItemDefinition(values.NewVariable(""), foundation.WithID("order_relay"))), nil))
held, _ := dataobjects.New("held",
    data.MustItemDefinition(values.NewVariable(""), foundation.WithID("held-item")),
    data.ReadyDataState)

_ = held.AssociateSource(catch, []string{"order_in"}, nil)            // catch → held
_ = held.AssociateTargetInput(throw, throw.Inputs()[0].ID(), nil)     // held → throw
```

When "order placed" arrives, the catch fills `held`; when the token reaches
the throw, its input is filled from `held` and "order relayed" carries the
same value.

## The process contract's two positions

A process's declared inputs and outputs are not data elements — they are
the process's own, so the process wires them:

```go
// a Start event's output → the declared input "order"
err = p.AssociateInput("order", start, start.Outputs()[0].ID())

// the declared output "total" → an End event's input
err = p.AssociateOutput("total", end, end.Inputs()[0].ID())
```

| Method | Rule |
|---|---|
| `AssociateInput(inputName, from flow.AssociationSource, sourceID string)` | `from` must be one of **this** process's Start events, `inputName` a declared input, `sourceID` one of the start's `Outputs()` ids |
| `AssociateOutput(outputName, to flow.AssociationTarget, targetID string)` | `to` must be one of **this** process's End events, `outputName` a declared output, `targetID` one of the end's `Inputs()` ids |

Each refusal names the part: a contract-less process, an undeclared name,
an event that is not the process's own, an unknown id.

### What a message-born launch does

When a message instantiates the process, the engine runs the born Start
event's output associations **at the seed**, before the contract is
checked:

1. a Data Object target is updated in place;
2. a declared input target is filled with the payload and then bound
   through its declaration — type-checked exactly as a value the host
   supplied with `thresher.WithStartInput`;
3. a Data Store target is written only **after** the contract accepted the
   launch — a refused launch leaves the engine-global stores untouched.

A required input the associations did not fill refuses the launch, as an
unbound host input would. At the other end, when the token reaches an End
event whose input is sourced from a declared output, the output's value —
what the process wrote under that name — fills the input and the End event
throws it.

## Build it

The `examples/event-data/` process is the whole route — a message start
fills the contract and a data object, a task computes, a message end throws
the declared result:

```go
p, _ := process.New("quote",
    data.WithInputs(param("order", "")),
    data.WithOutputs(param("total", 0)))

start, _ := events.NewStartEvent("start",
    events.WithMessageTrigger(events.MustMessageEventDefinition(
        bpmncommon.MustMessage("order placed", item("order_in", "")), nil)))

end, _ := events.NewEndEvent("end",
    events.WithMessageTrigger(events.MustMessageEventDefinition(
        bpmncommon.MustMessage("quote ready", item("quote_out", 0)), nil)),
    events.WithDataInputs(data.MustParameter("quote",
        data.MustItemAwareElement(item("quote_out", 0), data.UnavailableDataState))))

received, _ := dataobjects.New("received", item("received-item", ""), nil)

// … add the nodes, link start → price → end, add the data object …

_ = p.AssociateInput("order", start, start.Outputs()[0].ID())      // start → input
_ = received.AssociateSource(start, []string{"order_in"}, nil)     // start → data object
_ = p.AssociateOutput("total", end, end.Inputs()[0].ID())          // output → end
```

## Run it

```bash
cd examples/event-data && go run .
```

```
  → published "order placed" with payload "ORD-2026-7"
  ▶ price: order="ORD-2026-7" (received="ORD-2026-7") → total=100
  ← received "quote ready" with payload 100
  ✓ the message route filled the contract and carried its output back
```

The host publishes "order placed"; the payload fills `order` and
`received`; `price` reads `order` and writes `total`; the End event's
input is filled from `total` and "quote ready" carries `100`.

## In BPMN XML

The importer wires the same shapes from a file. An event's
`<dataInput>`/`<dataOutput>` are its declared parameters (bare, directly
under the event — a task's live inside an `<ioSpecification>`); a bare
parameter without `itemSubjectRef` adopts its definition's item, and one
with a **different** `itemSubjectRef` than its definition's is refused
(p217). The associations name the ends the way the model does:

```xml
<bpmn:process id="quote">
  <bpmn:ioSpecification>
    <bpmn:dataInput id="p-in" name="order" itemSubjectRef="idStr"/>
    <bpmn:dataOutput id="p-out" name="total" itemSubjectRef="idInt"/>
  </bpmn:ioSpecification>
  <bpmn:startEvent id="s1">
    <bpmn:messageEventDefinition messageRef="m1"/>
    <bpmn:dataOutput id="s1-out" itemSubjectRef="idStr"/>
    <bpmn:dataOutputAssociation>
      <bpmn:sourceRef>s1-out</bpmn:sourceRef>
      <bpmn:targetRef>p-in</bpmn:targetRef>        <!-- the process input -->
    </bpmn:dataOutputAssociation>
  </bpmn:startEvent>
  <bpmn:endEvent id="e1">
    <bpmn:messageEventDefinition messageRef="m2"/>
    <bpmn:dataInput id="e1-in" itemSubjectRef="idInt"/>
    <bpmn:dataInputAssociation>
      <bpmn:sourceRef>p-out</bpmn:sourceRef>       <!-- the process output -->
      <bpmn:targetRef>e1-in</bpmn:targetRef>
    </bpmn:dataInputAssociation>
  </bpmn:endEvent>
</bpmn:process>
```

A `<dataObject>` or `<dataStoreReference>` as the other end wires through
`AssociateSource` / `AssociateTargetInput`. A data output on a throw kind,
a data input on a catch kind, or a process parameter in any position other
than the two above, is refused with the standard's rule in the message.

## See also

- [Data Objects](data-objects.md) · [Data Store](data-store.md) — the elements an event wires to.
- [Start & End](../events/start-and-end.md) · [Message](../events/message.md) · [Signal](../events/signal.md) · [Escalation](../events/escalation.md) — the events and their item-bearing triggers.
- [Converters](../extending/converters.md) — the importer's event data rows.
- Design: [ADR-040 — the process I/O contract](../../design/ADR-040-process-io-contract.md) (§2.7 the event attachment) · [ADR-011 — process data flow](../../design/ADR-011-process-data-flow.md) (§2.5 events carry data without sets).
- Example: `examples/event-data/`.
