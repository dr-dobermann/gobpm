package flow

type Event interface {
	FlowNode

	EventType() string
}
