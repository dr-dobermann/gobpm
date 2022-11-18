package model

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/expression"
)

type GatewayModel interface {
	Node

	GwayType() GatewayType

	Direction() GatewayDirection

	Copy(snapshot *Process) (GatewayModel, error)
}

// ----------------------------------------------------------------------------

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

	expr      expression.Expression
	direction GatewayDirection
	// used only by
	flowType EventGatewayFlowType
	gType    GatewayType
}

func (g *Gateway) GwayType() GatewayType {

	return g.gType
}

func (g *Gateway) Direction() GatewayDirection {
	return g.direction
}

// checks standard's restriction and link Nodes if possible.
func (g *Gateway) ConnectFlow(sf *SequenceFlow, se SequenceEnd) error {
	// if g is a converging gateway there MUST be ONLY ONE outcoming
	// flow
	if g.direction == Converging && len(g.outcoming) > 0 &&
		se == SeSource {

		return fmt.Errorf("only one ougoing flow is allowed for "+
			"converging gateway '%s'", g.Name())
	}

	// if g is a diverging gateway there MUST be ONLY ONE incoming flow
	if g.direction == Diverging && len(g.incoming) > 0 &&
		se == SeTarget {

		return fmt.Errorf("only one incoming flow is allowed for "+
			"diverging gateway '%s'", g.Name())
	}

	return g.FlowNode.ConnectFlow(sf, se)
}

// checks standard's prescriptions on Copy call. Prevents copying of
// illegal gateways for instanciate a snapshots.
func (g *Gateway) checkGatewayFlows() error {
	switch {
	case g.direction == Converging && len(g.outcoming) > 0:
		return fmt.Errorf(
			"only one outcoming flow is allowed for converging gateway '%s'",
			g.Name())

	case g.direction == Diverging && len(g.incoming) > 0:
		return fmt.Errorf(
			"only one incoming flow allowed for diverging gateway '%s'",
			g.Name())

	case g.direction == Mixed &&
		(len(g.incoming) == 1 || len(g.outcoming) == 1):

		return fmt.Errorf(
			"mixed gateway '%s' should have multiple incoming "+
				"and outcoming flows",
			g.Name())
	}

	return nil
}
