package model

type Direction uint8

const (
	Unspecified Direction = iota
	Converging
	Diverging
	Mixed
)

type EventBasedGatewayType uint8

const (
	Parallel EventBasedGatewayType = iota
	Exclusive
)

type Gateway struct {
	FlowNode
	Expression

	direction   Direction
	defaultPath Id // if 0 there is no default path
}
