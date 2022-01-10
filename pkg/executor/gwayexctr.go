package executor

import "github.com/dr-dobermann/gobpm/model"

type GatewayExecutor interface {
	model.GatewayModel

	NodeExecutor
}
