package model

import "context"

type Executor interface {
	Execute(ctx context.Context) Error
}

type Operation struct {
	BaseElement
	inMessageRef  Id
	outMessageRef Id
	errors        []Id
	impl          Executor
}

type Interface struct {
	BaseElement
	name              string
	operations        []*Operation
	callabeElements   []*CallableElement
	implementationRef *interface{} // TODO: need to decide how to use this field
	// or just abandon it for Operation Executor
}
