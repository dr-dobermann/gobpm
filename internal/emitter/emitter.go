package emitter

// EventEmitter is an interface providing emitting of events
type EventEmitter interface {
	EmitEvent(name, descr string)
}

// EventEmittingFunc is a functor which could emit events
type EventEmittingFunc func(name, descr string)

func (f EventEmittingFunc) EmitEvent(name, descr string) {
	f(name, descr)
}
