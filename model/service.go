package model

type Executor interface {
	Exec(op *Operation) Error
}

type Operation struct {
	BaseElement
	inMessageRef  id
	outMessageRef id
	errors        []id
	impl          *Executor
}

type Interface struct {
	BaseElement
	name              string
	operations        []*Operation
	callabeElements   []*CallableElement
	implementationRef *interface{}
}
