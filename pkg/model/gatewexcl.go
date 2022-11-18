package model

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/common"
	"github.com/dr-dobermann/gobpm/pkg/expression"
	mid "github.com/dr-dobermann/gobpm/pkg/identity"
)

type ExclusiveGateway struct {
	Gateway
}

func NewExclusiveGateway(
	p *Process,
	name string,
	dir GatewayDirection,
	expr expression.Expression) *ExclusiveGateway {

	if p == nil {
		return nil
	}

	id := mid.NewID()
	name = strings.Trim(name, " ")
	if name == "" {
		name = Exclusive.String() + " #" + id.String()
	}

	eg := new(ExclusiveGateway)

	eg.SetNewID(id)
	eg.SetName(name)
	eg.expr = expr
	eg.SetType(common.EtGateway)
	eg.process = p
	eg.direction = dir
	eg.gType = Exclusive

	return eg
}

func (eg *ExclusiveGateway) Copy(snapshot *Process) (GatewayModel, error) {
	if err := eg.checkGatewayFlows(); err != nil {
		return nil, err
	}

	egc := new(ExclusiveGateway)

	*egc = *eg

	egc.process = snapshot
	egc.SetNewID(mid.NewID())

	return egc, nil
}
