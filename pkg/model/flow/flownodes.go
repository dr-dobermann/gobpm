package flow

// FlowNode indicates FlowNodes (Activities, Gateways and Events)
type FlowNode interface {
	GetNode() *Node

	NodeType() NodeType
}

// Event indicates Event FlowNode.
type Event interface {
	FlowNode

	EventType() string
}

type Activity interface {
	FlowNode

	ActivityType() string
}
