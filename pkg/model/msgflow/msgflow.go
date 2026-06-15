// Package msgflow holds the message-flow choreography shared by the BPMN nodes
// that send and receive messages (ADR-014 v.1). It bridges a node's
// bpmncommon.Message to the engine's MessageBroker without coupling the node to
// the broker's wire shape: SendTask uses Send today, and the throw message
// event will reuse it (the message-events SRD). It imports only public
// packages, so pkg/model nodes can call it without reaching into internal.
package msgflow

// errorClass identifies errors raised by the message-flow choreography.
const errorClass = "MSGFLOW_ERRORS"
