// Package lanes provides BPMN Lane and LaneSet — the engine's only
// **model-only** elements.
//
// A Lane is a sub-partition of a Process or Sub-Process that organizes its flow
// nodes for a reader. It carries no token semantics: activities in a lane
// execute exactly as if no lane existed, and BPMN 2.0.2 §2.3.1 accordingly lets
// a conformant tool ignore non-operational elements at run time.
//
// The engine still models them, because "non-operational" governs execution,
// not representation. §2.3.2 obliges a tool to support import of Process
// diagrams, and the converter guarantees a semantic round-trip (ADR-024 v.3
// §2.8) that export cannot deliver for a structure the model never stored —
// import a diagram with lanes, export it, and a model without lanes would have
// silently discarded the modeler's organization. So: parse and preserve,
// attach no behavior.
//
// Membership runs one way only. A Lane names the elements placed on it, via
// Lane.Place; nothing is added to flow.Node and no element can be asked which
// lane it is on. That keeps a purely visual concern out of the type every
// executing node implements, and makes "lanes are never executed" checkable by
// inspection — no execution path can consult a lane, because no element offers
// one.
package lanes

const errorClass = "LANES_ERRORS"
