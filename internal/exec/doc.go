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
     Once ProcessRunner recieve the event with event definition ID
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
  - recieving events and sending them to the registered EventProcessors.

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

Acces to the Instance's data provided by object implementing Scope interface.
Scope refers to the storage of available Data objects.

The initial Scope is created by an Instance of the process. It is filled with
process properties, input data parameters, and DataObjects.

Scopes can be organized into a tree structure, with the root of the tree
being the started Instance and the subsequent nodes being sub-processes
and tasks.

The data flow during Instance runtime follows these steps:

When a Node starts execution:
  - If the Node supports the NodeDataLoader interface, the Scope creates a new
    data path from its root and asks the Node to fill its data into it with
	RegisterData call:
      - The Node loads its Parameters and Properties.
      - After the Node execution finishes:
        - It stores output data parameters in the root of the Scope.
        - It fills all outgoing data associations.
  - The Scope deletes all node's data and deletes the data path.

When a Node tries to retrieve a value from the Scope, it could take the
following steps:
  - The Scope looks for the Name in the Node's data path.
  - If the Data name isn't in the data path, the Scope tries to look for the
    name in the upper data path until it reaches the root data path.
  - If the Scope has a parent Scope, it tries to get the data from the root of
    the parent Scope.
*/

package exec
