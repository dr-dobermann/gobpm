// Package bpmn is the batteries-included BPMN 2.0 XML converter for the
// github.com/dr-dobermann/gobpm/pkg/convert seam. It imports and exports the
// executable-core MVP subset (SRD-051 §FR-8) over the gobpm model:
//
//	<bpmn:process>                                  process.New (id via foundation.WithID)
//	<bpmn:startEvent> / <bpmn:endEvent> (none)      events.NewStartEvent / NewEndEvent
//	<bpmn:task> / <bpmn:manualTask>                 activities.NewManualTask
//	<bpmn:userTask>                                 activities.NewUserTask
//	<bpmn:serviceTask> (+ operationRef)             activities.NewServiceTask
//	  <bpmn:interface>/<bpmn:operation>             service.NewOperation (catalog stub)
//	<bpmn:sequenceFlow> (+ conditionExpression)     flow.Link (+ flow.WithCondition)
//	<bpmn:exclusiveGateway> (+ default)             gateways.NewExclusiveGateway
//	<bpmn:parallelGateway>                          gateways.NewParallelGateway
//
// The package has no exported surface beyond its init() self-registration:
// consumers use the convert façade and blank-import this package to turn
// BPMN on (SRD-051 §FR-4):
//
//	import _ "github.com/dr-dobermann/gobpm/pkg/convert/bpmn"
//
// Import uses a namespace-aware xml.Decoder token stream (SRD-051 §4.3):
// diagram-interchange and other foreign-namespace subtrees (bpmndi:*, dc:*,
// di:*) are skipped silently, as are non-executable BPMN annotations
// (documentation, extensionElements — nearly universal in modeler exports).
// An in-BPMN-namespace *flow element* outside the subset yields
// *convert.UnsupportedElementError (SRD-051 §FR-7) with a pinned spec §
// when known. Export marshals typed XML structs through xml.Encoder and
// emits no Diagram Interchange (SRD-051 §FR-6); the export walk checks
// ctx between elements.
//
// Import is semantic, not byte-lossless (SRD-051 §NFR-3): ids, node kinds,
// flows, conditions, gateway directions and defaults survive an
// import → export → re-import round-trip; whitespace, attribute order and
// the <bpmn:task> vs <bpmn:manualTask> spelling do not.
//
// One model-level workaround: gobpm's UserTask requires at least one output
// resource parameter, so an imported <bpmn:userTask> gets a synthesized
// optional placeholder output (see parseNode); it is not BPMN content and
// is not written back on export.
//
// serviceTask (SRD-051 §4.6): import resolves operationRef against the
// definitions-level interface/operation catalog into a service.Operation
// with matching id/name and a nil Implementor (the converter is not an
// execution engine — the host binds a real implementor or gooper after
// import). Export writes <serviceTask>; operationRef and the interface
// catalog are re-emitted only when gobpm exposes ServiceTask.Operation()
// (structural operationCarrier). On gobpm v0.9.0 the node kind and ids
// still round-trip; operationRef does not until that getter lands.
package bpmn
