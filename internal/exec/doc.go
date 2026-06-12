/*
The exec package consists of interfaces and objects that support BPMN
process runtime.

#Process Instance

Process objects consist of a process model. To run a Process instance, a
few steps should be taken:
  1. Create a process Snapshot object. Snapshot holds a list of Nodes,
     SequenceFlows, Properties, and starting event definitions.
  2. The Snapshot should be registered in the object implementing the
     ProcessRunner interface.
  3. On Snapshot registration ProcessRunner loads all its initial events.
     Once ProcessRunner receive the event with event definition ID
	 matched with registered initial process, it creates the Instance from the
	 appropriate Snapshot and runs it.

#Events

Envets processed with two interfaces:
  1. EventProducer
  2. EventProcessor

##EventProcessor

EventProcessor implemented by every Node which awaits event. When event
arrives, ProcessEvent of the Node is called and event is sent into Node to
process it.

EventProcessor registers itself in **EventProducer** with event difinition
it waits for.

Despite that Node is the real event processe, Node doesn't registered in
EventProcessor. Track is registered on behalf of the Node it runs on current
step. Thus track is called to process the event and track calls the node's
ProcessEvent function and sets track state depending on result of this call.

##EventProducer

EventProducer is the hub which coordinates event gathering and routing. It is
responsible for:
  - EventProcessors registration
  - receiving events and sending them to the registered EventProcessors.

##Event flow

Upon creating a Snapshot from the Process model, the list of initial Events
is built. An initial Event has no incoming flows and isn't bound to any
Activity.

When the Snapshot is sent to the ProcessRunner, it registers event definitions
of initial events and links this list to the snapshot.

Once an Event arrives at the ProcessRunner (which implements EventProducer
interface), the Event is placed into events queue for processing.
  - ProcessRunner checks the registered event definitions list and determines
    if it is runtime event or start event. Runtime event has not-nil EventProcessor
    registered for the event definition.
    If registration hasn't registered EventProcessor, it starts a new Instance for
    registered Snapshot and runs it.
	and run with this Event.
  - ProcessRunner checks the registered runtime events list. If an event
    definition is found in this list, the Event is sent to the running instance
	linked to this event definition.

BPMN doesn't restrict the simultaneous usage of the Event by different
instances and nodes inside the instance. Every EventProcessor and starting
Instance receives a copy of the processed Event.

##Data management

Persistent process data lives in the Instance's data plane (scope.Scope): a
tree of container scopes rooted at the process scope, holding the process
properties and data committed by node executions (ADR-010). Sub-processes
will attach child container scopes to the tree.

Each node execution runs on its own scope.Frame keyed by (track, node):

  - Before the node executes, its NodeDataConsumer.LoadData (if implemented)
    instantiates the node's inputs, outputs and properties in the frame as
    per-execution copies of the node's immutable definitions, and fills the
    inputs from the incoming data associations.
  - The node executes against a per-execution RuntimeEnvironment: reads
    resolve frame-first (inputs, properties, node-produced puts) and then
    walk the container scopes from the frame's attachment point up to the
    root; results go to the frame (Put or output instances).
  - After a successful execution, its NodeDataProducer.UploadData (if
    implemented) fills the output instances and pushes the outgoing data
    associations; the track then commits the frame — outputs and puts reach
    the container scope as ONE atomic batch.
  - On any failure the frame is discarded: the container scope observes
    nothing of the failed execution.
*/

// Package exec provides node execution interfaces and implementations.
package exec
