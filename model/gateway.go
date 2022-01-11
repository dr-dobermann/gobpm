package model

type GatewayDirection uint8

const (
	// MAY have multiple input and multiple output flows
	Unspecified GatewayDirection = iota

	// MAY have one or multiple input and MUST have only one output flow
	Converging

	// MUST have only one input and MAY have multiple output flows
	Diverging

	// MUST have multiple input and multiply output flows
	Mixed
)

type EventGatewayFlowType uint8

const (
	ParallelFlow EventGatewayFlowType = iota
	ExclusiveFlow
)

type GatewayType uint8

const (
	Exclusive GatewayType = iota
	Inclusive
	Complex
	Parallel
	EventBased
)

func (gt GatewayType) String() string {
	return []string{
		"Exclusive",
		"Inclusive",
		"Complex",
		"Parallel",
		"EventBased",
	}[gt]
}

type Gateway struct {
	FlowNode

	expr      *Expression
	direction GatewayDirection
	flowType  EventGatewayFlowType
	gType     GatewayType
}

func (g *Gateway) GwayType() GatewayType {

	return g.gType
}

type GatewayModel interface {
	Node

	GwayType() GatewayType

	Copy(snapshot *Process) GatewayModel
}
