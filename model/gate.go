package model

type Gateway struct {
	FlowNode
	Expression
	defaultPath id // if 0 there is no default path
}
