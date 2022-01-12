package model

import "strings"

type ExclusiveGateway struct {
	Gateway
}

func NewExclusiveGateway(
	p *Process,
	name string,
	dir GatewayDirection,
	flow EventGatewayFlowType,
	expr *Expression) *ExclusiveGateway {

	if p == nil {
		return nil
	}

	id := NewID()
	name = strings.Trim(name, " ")
	if name == "" {
		name = Exclusive.String() + " #" + id.String()
	}

	eg := new(ExclusiveGateway)

	eg.id = id
	eg.name = name
	eg.expr = expr
	eg.elementType = EtGateway
	eg.process = p
	eg.direction = dir
	eg.gType = Exclusive
	eg.flowType = flow

	return eg
}

func (eg *ExclusiveGateway) Copy(snapshot *Process) (GatewayModel, error) {
	if err := eg.checkGatewayFlows(); err != nil {
		return nil, err
	}

	egc := new(ExclusiveGateway)

	*egc = *eg

	egc.process = snapshot
	egc.id = NewID()

	return egc, nil
}
