package model

type Executor interface {
	Exec() Error
}

type Operation struct {
	BaseElement
	inMessageRef  id
	outMessageRef id
	errors        []id
	impl          *Executor
}
