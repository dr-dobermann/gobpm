package model

type EventClass uint8

const (
	EventIntermediate EventClass = iota
	EventStart
	EventEnd
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

type Event struct {
	BaseElement
	attachedTo   Id // 0 if not bounded (intermediate event)
	interrupting bool
	eType        EventClass
	trigger      EventTrigger
}

type Thrower interface {
	Throw()
}

type Catcher interface {
	Catch()
}
