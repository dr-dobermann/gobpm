package model

type EventType uint8

const (
	EventStart EventType = iota
	EventEnd
	EventIntermediate
)

type EventTrigger uint8

const (
	TNone EventTrigger = iota
	TMessage
	TTimer
	TCondition
	TSignal
	TMultiple
	TParallelMultiple
	TEscalation
	TError
	TCompensation
	TTerminate
	TCancel
)

type EventDefinition struct {
	ID           uint64
	Doc          Documentation
	BoundedTo    uint64
	Interrupting bool
	Type         EventType
	TriggeredBy  EventTrigger
}
