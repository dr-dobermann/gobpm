package model

type Gateway struct {
	FlowNode
	Expression
	defaultPath Id // if 0 there is no default path
}
