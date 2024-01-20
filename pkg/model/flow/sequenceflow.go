package flow

import "github.com/dr-dobermann/gobpm/pkg/model/data"

// A Sequence Flow is used to show the order of Flow Elements in a Process or
// a Choreography. Each Sequence Flow has only one source and only one target.
// The source and target MUST be from the set of the following Flow Elements:
// Events (Start, Intermediate, and End), Activities (Task and Sub-Process;
// for Processes), Choreography Activities (Choreography Task and
// Sub-Choreography; for Choreographies), and Gateways.
type SequenceFlow struct {
	Element

	// The FlowNode that the Sequence Flow is connecting from.
	// For a Process: Of the types of FlowNode, only Activities,
	// Gateways, and Events can be the source. However, Activities that are
	// Event Sub-Processes are not allowed to be a source.
	// For a Choreography: Of the types of FlowNode, only Choreography
	// Activities, Gateways, and Events can be the source.
	Source *Node

	// The FlowNode that the Sequence Flow is connecting to.
	// For a Process: Of the types of FlowNode, only Activities, Gateways,
	// and Events can be the target. However, Activities that are Event
	// Sub-Processes are not allowed to be a target.
	// For a Choreography: Of the types of FlowNode, only Choreography
	// Activities, Gateways, and Events can be the target.
	Target *Node

	// An optional boolean Expression that acts as a gating condition. A
	// token will only be placed on this Sequence Flow if this
	// conditionExpression evaluates to true.
	ConditionExpression *data.Expression

	// An optional boolean value specifying whether Activities or Choreography
	// Activities not in the model containing the Sequence Flow can occur
	// between the elements connected by the Sequence Flow. If the value is
	// true, they MAY NOT occur. If the value is false, they MAY occur. Also
	// see the isClosed attribute on Process, Choreography, and Collaboration.
	// When the attribute has no value, the default semantics depends on the
	// kind of model containing Sequence Flows:
	//   • For non-executable Processes (public Processes and non-executable
	//     private Processes) and Choreographies no value has the same semantics
	//     as if the value were false.
	//   • For an executable Processes no value has the same semantics as if
	//     the value were true.
	//   • For executable Processes, the attribute MUST NOT be false.
	IsImmediate bool
}
