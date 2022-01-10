package model

type Direction uint8

const (
	// MAY have multiple input and multiple output flows
	Unspecified Direction = iota

	// MAY have one or multiple input and MUST have only one output flow
	Converging

	// MUST have only one input and MAY have multiple output flows
	Diverging

	// MUST have multiple input and multiply output flows
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

type GatewayModel interface {
	Copy(snapshot *Process) GatewayModel
}
