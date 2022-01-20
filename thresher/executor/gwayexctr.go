package executor

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/gateways"
	"github.com/dr-dobermann/gobpm/model"
)

type GatewayExecutor interface {
	model.GatewayModel

	NodeExecutor
}

// GateKeeper is an interface which provides keeping converging
// gateway's state between their Exec calls
//
// It allows to calculate incoming tokens from different flows
// directed to the gateway and wait for gateway open condition.
//
// Implementor of the GateKeeper interface should provide mechanisms
// for storing first object with GatewayExecutor id and returning it
// on next calls. So first state would propagate onto other calls and
// update accordingly.
type GateKeeper interface {
	GetGExecutorInstance(ge GatewayExecutor) GatewayExecutor
}

func GetGatewayExecutor(
	g model.GatewayModel,
	gk GateKeeper) (GatewayExecutor, error) {

	var ge GatewayExecutor

	switch gs := g.(type) {
	case *model.ExclusiveGateway:
		ge = gateways.GetExclusiveGExecutor(gs)

	default:
		return nil, fmt.Errorf("no executor for '%s' gateway type",
			g.Type().String())
	}

	return ge, nil
}
