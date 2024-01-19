package flow

// The FlowNode element is used to provide a single element as the source and
// target Sequence Flow associations instead of the individual associations of
// the elements that can connect to Sequence Flows.
// Only the Gateway, Activity, Choreography Activity, and Event elements can
// connect to Sequence Flows and thus, these elements are the only ones that
// are sub-classes of FlowNode.
type Node struct {
	Element

	// This attribute identifies the incoming Sequence Flow of the FlowNode.
	Incoming []*SequenceFlow

	// This attribute identifies the outgoing Sequence Flow of the FlowNode.
	// This is an ordered collection.
	Outcoming []*SequenceFlow
}